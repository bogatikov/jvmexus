package indexer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/gradle"
	"github.com/bgtkv/jvmexus/internal/parser"
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

	deps, transitiveDeps, resolveWarnings := gradle.EnrichResolvedDependencies(ctx, absRoot, modules, deps, gradle.ResolveOptions{
		GradleTimeoutSec: s.cfg.GradleTimeoutSeconds,
		Offline:          s.cfg.Offline,
	})

	enrichedDirectDeps, sourceWarnings := gradle.AttachSourceJars(ctx, deps, gradle.SourceOptions{
		FetchMissingSources: s.cfg.FetchMissingSources,
		Offline:             s.cfg.Offline,
		DownloadTimeoutSec:  s.cfg.SourcesDownloadTimeout,
		ExtraSourceDir:      filepath.Join(absRoot, ".jvmexus", "sources"),
	})

	allDeps := append(enrichedDirectDeps, transitiveDeps...)

	storeDeps := make([]store.Dependency, 0, len(allDeps))
	for _, dep := range allDeps {
		kind := dep.Kind
		if kind == "" {
			kind = gradle.DependencyKindDirect
		}
		storeDeps = append(storeDeps, store.Dependency{
			ModuleName:     dep.ModuleName,
			GroupID:        dep.GroupID,
			ArtifactID:     dep.ArtifactID,
			Version:        dep.Version,
			Scope:          dep.Scope,
			Type:           dep.Type,
			Kind:           kind,
			BinaryJarPath:  dep.BinaryJarPath,
			SourceJarPath:  dep.SourceJarPath,
			SourceStatus:   dep.SourceStatus,
			ResolutionType: dep.ResolutionType,
			MetadataJSON:   dep.MetadataJSON,
			Confidence:     dep.Confidence,
		})
	}

	if err := s.store.ReplaceDependencies(ctx, project.ID, storeDeps); err != nil {
		return Result{}, fmt.Errorf("persist dependencies: %w", err)
	}

	symbols, refs, symbolWarnings, err := extractProjectSymbols(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("extract symbols: %w", err)
	}
	storeSymbols := make([]store.Symbol, 0, len(symbols))
	for _, sym := range symbols {
		storeSymbols = append(storeSymbols, store.Symbol{
			FilePath:  sym.FilePath,
			Language:  sym.Language,
			Name:      sym.Name,
			FQName:    sym.FQName,
			Kind:      sym.Kind,
			StartLine: sym.StartLine,
			EndLine:   sym.EndLine,
			Signature: sym.Signature,
		})
	}
	if err := s.store.ReplaceSymbols(ctx, project.ID, storeSymbols); err != nil {
		return Result{}, fmt.Errorf("persist symbols: %w", err)
	}

	storeRefs := make([]store.SymbolReference, 0, len(refs))
	for _, ref := range refs {
		storeRefs = append(storeRefs, store.SymbolReference{
			FromName:   ref.FromName,
			FromFile:   ref.FromFile,
			ToName:     ref.ToName,
			ToFQName:   ref.ToFQName,
			RefType:    ref.RefType,
			Confidence: ref.Confidence,
			Evidence:   ref.Evidence,
		})
	}
	if err := s.store.ReplaceSymbolReferences(ctx, project.ID, storeRefs); err != nil {
		return Result{}, fmt.Errorf("persist symbol references: %w", err)
	}

	chunks, chunkWarnings, err := extractProjectChunks(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("extract chunks: %w", err)
	}
	if err := s.store.ReplaceChunks(ctx, project.ID, chunks); err != nil {
		return Result{}, fmt.Errorf("persist chunks: %w", err)
	}

	fileCount, err := countSourceFiles(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("count source files: %w", err)
	}

	warnings := append([]string{}, moduleWarnings...)
	warnings = append(warnings, dependencyWarnings...)
	warnings = append(warnings, resolveWarnings...)
	warnings = append(warnings, sourceWarnings...)
	warnings = append(warnings, symbolWarnings...)
	warnings = append(warnings, chunkWarnings...)

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

func extractProjectSymbols(root string) ([]parser.Symbol, []parser.Reference, []string, error) {
	var symbols []parser.Symbol
	var refs []parser.Reference
	var warnings []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("walk error on %s: %v", path, err))
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == ".gradle" || base == ".idea" || base == "build" || base == "node_modules" || base == ".jvmexus" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".java" && ext != ".kt" && ext != ".kts" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("unable to read %s: %v", path, readErr))
			return nil
		}
		relPath := path
		if rel, relErr := filepath.Rel(root, path); relErr == nil {
			relPath = rel
		}
		fileSymbols, fileRefs := parser.ParseFile(relPath, content)
		symbols = append(symbols, fileSymbols...)
		refs = append(refs, fileRefs...)
		return nil
	})
	if err != nil {
		return nil, nil, warnings, err
	}

	return symbols, refs, warnings, nil
}

func extractProjectChunks(root string) ([]store.Chunk, []string, error) {
	var chunks []store.Chunk
	var warnings []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("walk error on %s: %v", path, err))
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == ".gradle" || base == ".idea" || base == "build" || base == "node_modules" || base == ".jvmexus" {
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		ext := strings.ToLower(filepath.Ext(name))
		isCode := ext == ".java" || ext == ".kt" || ext == ".kts"
		isBuild := name == "build.gradle" || name == "build.gradle.kts" || name == "settings.gradle" || name == "settings.gradle.kts"
		if !isCode && !isBuild {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("unable to read %s: %v", path, readErr))
			return nil
		}
		relPath := path
		if rel, relErr := filepath.Rel(root, path); relErr == nil {
			relPath = rel
		}

		language := "text"
		switch ext {
		case ".java":
			language = "java"
		case ".kt", ".kts":
			language = "kotlin"
		}
		chunkType := "code_window"
		if isBuild {
			chunkType = "build_window"
			if language == "text" {
				language = "gradle"
			}
		}

		fileChunks := chunkText(string(content), relPath, language, chunkType)
		chunks = append(chunks, fileChunks...)
		return nil
	})
	if err != nil {
		return nil, warnings, err
	}

	return chunks, warnings, nil
}

func chunkText(content, filePath, language, chunkType string) []store.Chunk {
	const windowSize = 80
	const overlap = 20
	step := windowSize - overlap
	if step <= 0 {
		step = windowSize
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	chunks := make([]store.Chunk, 0, (len(lines)/step)+1)
	chunkIdx := 0
	for start := 0; start < len(lines); start += step {
		end := start + windowSize
		if end > len(lines) {
			end = len(lines)
		}
		text := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if text == "" {
			if end == len(lines) {
				break
			}
			continue
		}
		chunks = append(chunks, store.Chunk{
			FilePath:   filePath,
			Language:   language,
			ChunkType:  chunkType,
			ChunkIndex: chunkIdx,
			Text:       text,
			TokenCount: len(strings.Fields(text)),
		})
		chunkIdx++
		if end == len(lines) {
			break
		}
	}
	return chunks
}
