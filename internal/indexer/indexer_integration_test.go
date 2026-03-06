package indexer

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/gradle"
	"github.com/bgtkv/jvmexus/internal/store"
)

func TestIndexProject_AttachesSourceJarFromCache(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo, javaFile := createBasicRepo(t, tmp, `
package com.example;
public class Bot {
  String ping() { return "pong"; }
}
`)
	_ = javaFile

	binaryJar, sourceJar := prepareGradleCache(t, tmp)
	st, cfg := openStoreAndConfig(t, ctx, tmp)
	defer st.Close()

	ix := New(st, cfg)
	result, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("index project: %v", err)
	}

	if result.ModuleCount != 2 {
		t.Fatalf("expected 2 modules (root + app), got %d", result.ModuleCount)
	}

	project, err := st.FindProject(ctx, result.ProjectName)
	if err != nil {
		t.Fatalf("find project: %v", err)
	}
	deps, err := st.ListDependenciesByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("list dependencies: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(deps))
	}

	dep := deps[0]
	if dep.SourceStatus != gradle.SourceStatusAttached {
		t.Fatalf("expected source status %q, got %q", gradle.SourceStatusAttached, dep.SourceStatus)
	}
	if dep.BinaryJarPath != binaryJar {
		t.Fatalf("expected binary jar path %q, got %q", binaryJar, dep.BinaryJarPath)
	}
	if dep.SourceJarPath != sourceJar {
		t.Fatalf("expected source jar path %q, got %q", sourceJar, dep.SourceJarPath)
	}
}

func TestIndexProject_SmartIncrementalSourceOnlyChanges(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo, javaFile := createBasicRepo(t, tmp, `
package com.example;
public class Bot {
  String ping() { return "pong"; }
}
`)
	prepareGradleCache(t, tmp)
	st, cfg := openStoreAndConfig(t, ctx, tmp)
	defer st.Close()

	ix := New(st, cfg)
	first, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("first index project: %v", err)
	}
	if first.Mode != "full" {
		t.Fatalf("expected first indexing mode full, got %q", first.Mode)
	}

	if err := os.WriteFile(javaFile, []byte(`
package com.example;
public class Bot {
  String ping() { return helper(); }
  String helper() { return "pong"; }
}
`), 0o644); err != nil {
		t.Fatalf("rewrite java file: %v", err)
	}

	second, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("second index project: %v", err)
	}
	if second.Mode != "incremental" {
		t.Fatalf("expected second indexing mode incremental, got %q", second.Mode)
	}
	if second.ChangedFiles < 1 {
		t.Fatalf("expected changed files to be > 0, got %d", second.ChangedFiles)
	}

	project, err := st.FindProject(ctx, first.ProjectName)
	if err != nil {
		t.Fatalf("find project: %v", err)
	}
	symbols, err := st.FindSymbolsWithFilter(ctx, project.ID, "helper", "Bot.java", 10)
	if err != nil {
		t.Fatalf("find helper symbol: %v", err)
	}
	if len(symbols) == 0 {
		t.Fatalf("expected helper symbol after incremental update")
	}
}

func TestIndexProject_BuildFileChangeForcesFullReindex(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo, _ := createBasicRepo(t, tmp, `
package com.example;
public class Bot {
  String ping() { return "pong"; }
}
`)
	prepareGradleCache(t, tmp)
	st, cfg := openStoreAndConfig(t, ctx, tmp)
	defer st.Close()

	ix := New(st, cfg)
	first, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("first index project: %v", err)
	}
	if first.Mode != "full" {
		t.Fatalf("expected first indexing mode full, got %q", first.Mode)
	}

	buildPath := filepath.Join(repo, "app", "build.gradle.kts")
	if err := os.WriteFile(buildPath, []byte(`
dependencies {
  implementation("org.slf4j:slf4j-api:2.0.13")
  testImplementation("junit:junit:4.13.2")
}
`), 0o644); err != nil {
		t.Fatalf("rewrite build file: %v", err)
	}

	second, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("second index project: %v", err)
	}
	if second.Mode != "full" {
		t.Fatalf("expected second indexing mode full after build change, got %q", second.Mode)
	}
	if second.ChangedFiles < 1 {
		t.Fatalf("expected changed files to be > 0, got %d", second.ChangedFiles)
	}
}

func TestIndexProject_NoChangesSkipsWork(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	repo, _ := createBasicRepo(t, tmp, `
package com.example;
public class Bot {
  String ping() { return "pong"; }
}
`)
	prepareGradleCache(t, tmp)
	st, cfg := openStoreAndConfig(t, ctx, tmp)
	defer st.Close()

	ix := New(st, cfg)
	first, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("first index project: %v", err)
	}
	if first.Mode != "full" {
		t.Fatalf("expected first mode full, got %q", first.Mode)
	}

	second, err := ix.IndexProject(ctx, repo, Options{})
	if err != nil {
		t.Fatalf("second index project: %v", err)
	}
	if second.Mode != "incremental" {
		t.Fatalf("expected second mode incremental, got %q", second.Mode)
	}
	if second.ChangedFiles != 0 {
		t.Fatalf("expected changed files=0, got %d", second.ChangedFiles)
	}
	if second.DeletedFiles != 0 {
		t.Fatalf("expected deleted files=0, got %d", second.DeletedFiles)
	}
	if second.SkippedFiles == 0 {
		t.Fatalf("expected skipped files to be > 0")
	}
}

func createBasicRepo(t *testing.T, tmp, javaSource string) (string, string) {
	t.Helper()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "app", "src", "main", "java", "com", "example"), 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "settings.gradle.kts"), []byte("include(\":app\")\n"), 0o644); err != nil {
		t.Fatalf("write settings file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "build.gradle.kts"), []byte(`
dependencies {
  implementation("org.slf4j:slf4j-api:2.0.13")
}
`), 0o644); err != nil {
		t.Fatalf("write build file: %v", err)
	}
	javaFile := filepath.Join(repo, "app", "src", "main", "java", "com", "example", "Bot.java")
	if err := os.WriteFile(javaFile, []byte(javaSource), 0o644); err != nil {
		t.Fatalf("write java file: %v", err)
	}
	return repo, javaFile
}

func prepareGradleCache(t *testing.T, tmp string) (string, string) {
	t.Helper()
	gradleHome := filepath.Join(tmp, "gradle-home")
	t.Setenv("GRADLE_USER_HOME", gradleHome)
	cachePath := filepath.Join(gradleHome, "caches", "modules-2", "files-2.1", "org.slf4j", "slf4j-api", "2.0.13", "hash")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatalf("create gradle cache path: %v", err)
	}
	binaryJar := filepath.Join(cachePath, "slf4j-api-2.0.13.jar")
	sourceJar := filepath.Join(cachePath, "slf4j-api-2.0.13-sources.jar")
	if err := os.WriteFile(binaryJar, []byte("binary"), 0o644); err != nil {
		t.Fatalf("write binary jar: %v", err)
	}
	sourceFile, err := os.Create(sourceJar)
	if err != nil {
		t.Fatalf("create source jar: %v", err)
	}
	zipWriter := zip.NewWriter(sourceFile)
	entry, err := zipWriter.Create("org/slf4j/Logger.java")
	if err != nil {
		_ = sourceFile.Close()
		t.Fatalf("create source entry: %v", err)
	}
	if _, err := entry.Write([]byte("package org.slf4j; public interface Logger { void info(String msg); }")); err != nil {
		_ = sourceFile.Close()
		t.Fatalf("write source entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		_ = sourceFile.Close()
		t.Fatalf("close source zip writer: %v", err)
	}
	if err := sourceFile.Close(); err != nil {
		t.Fatalf("close source jar: %v", err)
	}
	return binaryJar, sourceJar
}

func openStoreAndConfig(t *testing.T, ctx context.Context, tmp string) (*store.Store, config.Config) {
	t.Helper()
	dbPath := filepath.Join(tmp, "index.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		t.Fatalf("migrate store: %v", err)
	}
	cfg := config.Config{
		DatabasePath:           dbPath,
		EmbeddingsProvider:     "local",
		EmbeddingModelID:       "Snowflake/snowflake-arctic-embed-xs",
		ModelCacheDir:          filepath.Join(tmp, "models"),
		GradleTimeoutSeconds:   5,
		FetchMissingSources:    true,
		Offline:                true,
		SourcesDownloadTimeout: 5,
	}
	return st, cfg
}
