package indexer

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/gradle"
	"github.com/bgtkv/jvmexus/internal/store"
)

type Options struct {
	Force bool
}

type Result struct {
	ProjectName string   `json:"projectName"`
	ProjectPath string   `json:"projectPath"`
	ModuleCount int      `json:"moduleCount"`
	FileCount   int      `json:"fileCount"`
	Warnings    []string `json:"warnings"`
}

type Service struct {
	store *store.Store
	cfg   config.Config
}

func New(s *store.Store, cfg config.Config) *Service {
	return &Service{store: s, cfg: cfg}
}

func (s *Service) IndexProject(ctx context.Context, path string, _ Options) (Result, error) {
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project path: %w", err)
	}

	projectName := filepath.Base(absRoot)
	project, err := s.store.UpsertProject(ctx, projectName, absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("upsert project: %w", err)
	}

	modules, moduleWarnings, err := gradle.DiscoverModules(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("discover modules: %w", err)
	}
	moduleRows := make([]store.Module, 0, len(modules))
	for _, m := range modules {
		moduleRows = append(moduleRows, store.Module{Name: m.Name, Path: m.Path})
	}
	if err := s.store.ReplaceModules(ctx, project.ID, moduleRows); err != nil {
		return Result{}, fmt.Errorf("persist modules: %w", err)
	}

	deps, dependencyWarnings, err := gradle.ExtractDeclaredDependencies(absRoot, modules)
	if err != nil {
		return Result{}, fmt.Errorf("extract dependencies: %w", err)
	}

	deps, resolveWarnings := gradle.EnrichResolvedDependencies(ctx, absRoot, modules, deps, gradle.ResolveOptions{
		GradleTimeoutSec: s.cfg.GradleTimeoutSeconds,
		Offline:          s.cfg.Offline,
	})

	enrichedDeps, sourceWarnings := gradle.AttachSourceJars(ctx, deps, gradle.SourceOptions{
		FetchMissingSources: s.cfg.FetchMissingSources,
		Offline:             s.cfg.Offline,
		DownloadTimeoutSec:  s.cfg.SourcesDownloadTimeout,
		ExtraSourceDir:      filepath.Join(absRoot, ".jvmexus", "sources"),
	})

	storeDeps := make([]store.Dependency, 0, len(enrichedDeps))
	for _, dep := range enrichedDeps {
		storeDeps = append(storeDeps, store.Dependency{
			ModuleName:     dep.ModuleName,
			GroupID:        dep.GroupID,
			ArtifactID:     dep.ArtifactID,
			Version:        dep.Version,
			Scope:          dep.Scope,
			Type:           dep.Type,
			BinaryJarPath:  dep.BinaryJarPath,
			SourceJarPath:  dep.SourceJarPath,
			SourceStatus:   dep.SourceStatus,
			ResolutionType: dep.ResolutionType,
			Confidence:     dep.Confidence,
		})
	}

	if err := s.store.ReplaceDependencies(ctx, project.ID, storeDeps); err != nil {
		return Result{}, fmt.Errorf("persist dependencies: %w", err)
	}

	fileCount, err := countSourceFiles(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("count source files: %w", err)
	}

	warnings := append([]string{}, moduleWarnings...)
	warnings = append(warnings, dependencyWarnings...)
	warnings = append(warnings, resolveWarnings...)
	warnings = append(warnings, sourceWarnings...)

	return Result{
		ProjectName: project.Name,
		ProjectPath: project.RootPath,
		ModuleCount: len(modules),
		FileCount:   fileCount,
		Warnings:    warnings,
	}, nil
}

func countSourceFiles(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == ".gradle" || base == ".idea" || base == "build" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".java" || ext == ".kt" || d.Name() == "build.gradle" || d.Name() == "build.gradle.kts" || d.Name() == "settings.gradle" || d.Name() == "settings.gradle.kts" {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
