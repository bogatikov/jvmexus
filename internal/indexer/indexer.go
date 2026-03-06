package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/gradle"
	"github.com/bgtkv/jvmexus/internal/parser"
	"github.com/bgtkv/jvmexus/internal/store"
)

type Options struct {
	Force    bool
	Progress func(message string)
}

type Result struct {
	ProjectName  string   `json:"projectName"`
	ProjectPath  string   `json:"projectPath"`
	ModuleCount  int      `json:"moduleCount"`
	FileCount    int      `json:"fileCount"`
	Mode         string   `json:"mode"`
	ChangedFiles int      `json:"changedFiles"`
	DeletedFiles int      `json:"deletedFiles"`
	SkippedFiles int      `json:"skippedFiles"`
	Warnings     []string `json:"warnings"`
}

type Service struct {
	store *store.Store
	cfg   config.Config
}

func New(s *store.Store, cfg config.Config) *Service {
	return &Service{store: s, cfg: cfg}
}

func (s *Service) IndexProject(ctx context.Context, path string, opts Options) (Result, error) {
	notify := func(message string) {
		if opts.Progress != nil {
			opts.Progress(message)
		}
	}

	notify("Resolving project path")
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project path: %w", err)
	}

	notify("Upserting project in store")
	projectName := filepath.Base(absRoot)
	project, err := s.store.UpsertProject(ctx, projectName, absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("upsert project: %w", err)
	}

	notify("Scanning indexable files")
	currentFiles, scanWarnings, err := s.scanIndexedFiles(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("scan indexable files: %w", err)
	}

	if opts.Force {
		notify("Force mode: running full reindex")
		result, err := s.fullReindex(ctx, absRoot, project, notify)
		if err != nil {
			return Result{}, err
		}
		result.Mode = "full"
		result.ChangedFiles = len(currentFiles)
		result.Warnings = append(result.Warnings, scanWarnings...)
		if err := s.store.ReplaceIndexedFiles(ctx, project.ID, currentFiles); err != nil {
			return Result{}, fmt.Errorf("persist indexed files: %w", err)
		}
		return result, nil
	}

	previousFiles, err := s.store.ListIndexedFilesByProjectID(ctx, project.ID)
	if err != nil {
		return Result{}, fmt.Errorf("load indexed files: %w", err)
	}

	if len(previousFiles) == 0 {
		notify("No prior index snapshot found: running full reindex")
		result, err := s.fullReindex(ctx, absRoot, project, notify)
		if err != nil {
			return Result{}, err
		}
		result.Mode = "full"
		result.ChangedFiles = len(currentFiles)
		result.Warnings = append(result.Warnings, scanWarnings...)
		if err := s.store.ReplaceIndexedFiles(ctx, project.ID, currentFiles); err != nil {
			return Result{}, fmt.Errorf("persist indexed files: %w", err)
		}
		return result, nil
	}

	changedPaths, deletedPaths, unchangedCount := diffIndexedFiles(previousFiles, currentFiles)
	if len(changedPaths) == 0 && len(deletedPaths) == 0 {
		notify("No indexed file changes detected")
		modules, modErr := s.store.ListModulesByProjectID(ctx, project.ID)
		if modErr != nil {
			return Result{}, fmt.Errorf("load modules: %w", modErr)
		}
		fileCount, countErr := countSourceFiles(absRoot)
		if countErr != nil {
			return Result{}, fmt.Errorf("count source files: %w", countErr)
		}
		return Result{
			ProjectName:  project.Name,
			ProjectPath:  project.RootPath,
			ModuleCount:  len(modules),
			FileCount:    fileCount,
			Mode:         "incremental",
			ChangedFiles: 0,
			DeletedFiles: 0,
			SkippedFiles: unchangedCount,
			Warnings:     scanWarnings,
		}, nil
	}

	if hasBuildImpact(changedPaths, deletedPaths) {
		notify("Build-impacting files changed: running full reindex")
		result, err := s.fullReindex(ctx, absRoot, project, notify)
		if err != nil {
			return Result{}, err
		}
		result.Mode = "full"
		result.ChangedFiles = len(changedPaths)
		result.DeletedFiles = len(deletedPaths)
		result.SkippedFiles = unchangedCount
		result.Warnings = append(result.Warnings, scanWarnings...)
		if err := s.store.ReplaceIndexedFiles(ctx, project.ID, currentFiles); err != nil {
			return Result{}, fmt.Errorf("persist indexed files: %w", err)
		}
		return result, nil
	}

	notify("Applying smart incremental updates for source graph and retrieval")
	reindexTargets := make([]string, 0, len(changedPaths))
	deleteTargets := make([]string, 0, len(changedPaths)+len(deletedPaths))
	for _, p := range changedPaths {
		if isSourceLikePath(p) {
			reindexTargets = append(reindexTargets, p)
			deleteTargets = append(deleteTargets, p)
		}
	}
	for _, p := range deletedPaths {
		if isSourceLikePath(p) {
			deleteTargets = append(deleteTargets, p)
		}
	}

	if err := s.store.DeleteSymbolReferencesByFromFilePaths(ctx, project.ID, deleteTargets); err != nil {
		return Result{}, fmt.Errorf("delete symbol refs by file path: %w", err)
	}
	if err := s.store.DeleteSymbolsByFilePaths(ctx, project.ID, deleteTargets); err != nil {
		return Result{}, fmt.Errorf("delete symbols by file path: %w", err)
	}
	if err := s.store.DeleteChunksByFilePaths(ctx, project.ID, deleteTargets); err != nil {
		return Result{}, fmt.Errorf("delete chunks by file path: %w", err)
	}

	newSymbols, newRefs, symbolWarnings, err := extractSymbolsFromFiles(absRoot, reindexTargets)
	if err != nil {
		return Result{}, fmt.Errorf("extract symbols incrementally: %w", err)
	}
	storeSymbols := make([]store.Symbol, 0, len(newSymbols))
	for _, sym := range newSymbols {
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
	if err := s.store.InsertSymbols(ctx, project.ID, storeSymbols); err != nil {
		return Result{}, fmt.Errorf("insert incremental symbols: %w", err)
	}

	storeRefs := make([]store.SymbolReference, 0, len(newRefs))
	for _, ref := range newRefs {
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
	if err := s.store.InsertSymbolReferences(ctx, project.ID, storeRefs); err != nil {
		return Result{}, fmt.Errorf("insert incremental symbol refs: %w", err)
	}

	newChunks, chunkWarnings, err := extractChunksFromFiles(absRoot, reindexTargets)
	if err != nil {
		return Result{}, fmt.Errorf("extract chunks incrementally: %w", err)
	}
	if err := s.store.InsertChunks(ctx, project.ID, newChunks); err != nil {
		return Result{}, fmt.Errorf("insert incremental chunks: %w", err)
	}

	if err := s.store.ReplaceIndexedFiles(ctx, project.ID, currentFiles); err != nil {
		return Result{}, fmt.Errorf("persist indexed files: %w", err)
	}

	modules, err := s.store.ListModulesByProjectID(ctx, project.ID)
	if err != nil {
		return Result{}, fmt.Errorf("load modules: %w", err)
	}

	notify("Counting source files")
	fileCount, err := countSourceFiles(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("count source files: %w", err)
	}

	warnings := append([]string{}, scanWarnings...)
	warnings = append(warnings, symbolWarnings...)
	warnings = append(warnings, chunkWarnings...)

	notify("Index complete")
	return Result{
		ProjectName:  project.Name,
		ProjectPath:  project.RootPath,
		ModuleCount:  len(modules),
		FileCount:    fileCount,
		Mode:         "incremental",
		ChangedFiles: len(changedPaths),
		DeletedFiles: len(deletedPaths),
		SkippedFiles: unchangedCount,
		Warnings:     warnings,
	}, nil
}

func (s *Service) fullReindex(ctx context.Context, absRoot string, project store.Project, notify func(message string)) (Result, error) {

	notify("Discovering Gradle modules")
	modules, moduleWarnings, err := gradle.DiscoverModules(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("discover modules: %w", err)
	}
	moduleRows := make([]store.Module, 0, len(modules))
	for _, m := range modules {
		moduleRows = append(moduleRows, store.Module{Name: m.Name, Path: m.Path})
	}
	notify("Persisting modules")
	if err := s.store.ReplaceModules(ctx, project.ID, moduleRows); err != nil {
		return Result{}, fmt.Errorf("persist modules: %w", err)
	}

	notify("Extracting declared dependencies")
	deps, dependencyWarnings, err := gradle.ExtractDeclaredDependencies(absRoot, modules)
	if err != nil {
		return Result{}, fmt.Errorf("extract dependencies: %w", err)
	}

	notify("Resolving dependencies via Gradle (this may take a while)")
	deps, transitiveDeps, resolveWarnings := gradle.EnrichResolvedDependencies(ctx, absRoot, modules, deps, gradle.ResolveOptions{
		GradleTimeoutSec: s.cfg.GradleTimeoutSeconds,
		Offline:          s.cfg.Offline,
	})

	notify("Attaching source jars")
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

	notify("Persisting dependencies")
	if err := s.store.ReplaceDependencies(ctx, project.ID, storeDeps); err != nil {
		return Result{}, fmt.Errorf("persist dependencies: %w", err)
	}

	notify("Extracting symbols and references")
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
	notify("Persisting symbols")
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
	notify("Persisting symbol references")
	if err := s.store.ReplaceSymbolReferences(ctx, project.ID, storeRefs); err != nil {
		return Result{}, fmt.Errorf("persist symbol references: %w", err)
	}

	notify("Chunking project files for retrieval")
	chunks, chunkWarnings, err := extractProjectChunks(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("extract chunks: %w", err)
	}
	notify("Persisting retrieval chunks")
	if err := s.store.ReplaceChunks(ctx, project.ID, chunks); err != nil {
		return Result{}, fmt.Errorf("persist chunks: %w", err)
	}

	notify("Counting source files")
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

	result := Result{
		ProjectName: project.Name,
		ProjectPath: project.RootPath,
		ModuleCount: len(modules),
		FileCount:   fileCount,
		Mode:        "full",
		Warnings:    warnings,
	}
	notify("Index complete")
	return result, nil
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

func (s *Service) scanIndexedFiles(root string) ([]store.IndexedFile, []string, error) {
	files := make([]store.IndexedFile, 0, 256)
	warnings := make([]string, 0, 8)

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

		relPath := path
		if rel, relErr := filepath.Rel(root, path); relErr == nil {
			relPath = rel
		}
		if !isTrackedIndexedPath(relPath) {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			warnings = append(warnings, fmt.Sprintf("stat error on %s: %v", relPath, statErr))
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("read error on %s: %v", relPath, readErr))
			return nil
		}

		hash := sha256.Sum256(content)
		files = append(files, store.IndexedFile{
			FilePath:  relPath,
			FileKind:  indexedFileKind(relPath),
			SHA256:    hex.EncodeToString(hash[:]),
			SizeBytes: info.Size(),
			MtimeUnix: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil {
		return nil, warnings, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].FilePath < files[j].FilePath
	})
	return files, warnings, nil
}

func diffIndexedFiles(previous, current []store.IndexedFile) (changed []string, deleted []string, unchanged int) {
	previousByPath := make(map[string]store.IndexedFile, len(previous))
	for _, file := range previous {
		previousByPath[file.FilePath] = file
	}
	currentByPath := make(map[string]store.IndexedFile, len(current))
	for _, file := range current {
		currentByPath[file.FilePath] = file
		old, ok := previousByPath[file.FilePath]
		if !ok {
			changed = append(changed, file.FilePath)
			continue
		}
		if old.SHA256 != file.SHA256 || old.SizeBytes != file.SizeBytes || old.MtimeUnix != file.MtimeUnix {
			changed = append(changed, file.FilePath)
			continue
		}
		unchanged++
	}
	for _, file := range previous {
		if _, ok := currentByPath[file.FilePath]; !ok {
			deleted = append(deleted, file.FilePath)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted, unchanged
}

func hasBuildImpact(changed, deleted []string) bool {
	for _, path := range changed {
		if isBuildImpactPath(path) {
			return true
		}
	}
	for _, path := range deleted {
		if isBuildImpactPath(path) {
			return true
		}
	}
	return false
}

func isBuildImpactPath(path string) bool {
	name := filepath.Base(path)
	if name == "settings.gradle" || name == "settings.gradle.kts" || name == "build.gradle" || name == "build.gradle.kts" || name == "gradle.properties" {
		return true
	}
	return path == "gradle/libs.versions.toml" || strings.HasSuffix(path, "/gradle/libs.versions.toml")
}

func isSourceLikePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".java" || ext == ".kt" || ext == ".kts"
}

func isTrackedIndexedPath(path string) bool {
	if isBuildImpactPath(path) {
		return true
	}
	if isSourceLikePath(path) {
		return true
	}
	name := filepath.Base(path)
	return name == "settings.gradle" || name == "settings.gradle.kts" || name == "build.gradle" || name == "build.gradle.kts"
}

func indexedFileKind(path string) string {
	if isBuildImpactPath(path) || filepath.Base(path) == "settings.gradle" || filepath.Base(path) == "settings.gradle.kts" || filepath.Base(path) == "build.gradle" || filepath.Base(path) == "build.gradle.kts" {
		return "build"
	}
	return "source"
}

func extractSymbolsFromFiles(root string, filePaths []string) ([]parser.Symbol, []parser.Reference, []string, error) {
	symbols := make([]parser.Symbol, 0, len(filePaths)*2)
	refs := make([]parser.Reference, 0, len(filePaths)*4)
	warnings := make([]string, 0, 8)

	for _, relPath := range filePaths {
		fullPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("unable to read %s: %v", relPath, err))
			continue
		}
		fileSymbols, fileRefs := parser.ParseFile(relPath, content)
		symbols = append(symbols, fileSymbols...)
		refs = append(refs, fileRefs...)
	}

	return symbols, refs, warnings, nil
}

func extractChunksFromFiles(root string, filePaths []string) ([]store.Chunk, []string, error) {
	chunks := make([]store.Chunk, 0, len(filePaths)*2)
	warnings := make([]string, 0, 8)

	for _, relPath := range filePaths {
		fullPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("unable to read %s: %v", relPath, err))
			continue
		}
		ext := strings.ToLower(filepath.Ext(relPath))
		language := "text"
		switch ext {
		case ".java":
			language = "java"
		case ".kt", ".kts":
			language = "kotlin"
		}

		isBuild := isBuildImpactPath(relPath) || filepath.Base(relPath) == "settings.gradle" || filepath.Base(relPath) == "settings.gradle.kts" || filepath.Base(relPath) == "build.gradle" || filepath.Base(relPath) == "build.gradle.kts"
		chunkType := "code_window"
		if isBuild {
			chunkType = "build_window"
			if language == "text" {
				language = "gradle"
			}
		}
		chunks = append(chunks, chunkText(string(content), relPath, language, chunkType)...)
	}

	return chunks, warnings, nil
}
