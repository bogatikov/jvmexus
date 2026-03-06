package gradle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachSourceJarsFromGradleCache(t *testing.T) {
	gradleHome := t.TempDir()
	t.Setenv("GRADLE_USER_HOME", gradleHome)

	dep := Dependency{
		ModuleName:     ":",
		GroupID:        "org.slf4j",
		ArtifactID:     "slf4j-api",
		Version:        "2.0.13",
		Scope:          "implementation",
		Type:           "external",
		SourceStatus:   SourceStatusNotFound,
		ResolutionType: ResolutionTypeDeclared,
		Confidence:     0.75,
	}

	base := filepath.Join(gradleHome, "caches", "modules-2", "files-2.1", "org.slf4j", "slf4j-api", "2.0.13", "abc123")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	binaryPath := filepath.Join(base, "slf4j-api-2.0.13.jar")
	sourcePath := filepath.Join(base, "slf4j-api-2.0.13-sources.jar")
	if err := os.WriteFile(binaryPath, []byte("bin"), 0o644); err != nil {
		t.Fatalf("write binary jar: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("src"), 0o644); err != nil {
		t.Fatalf("write source jar: %v", err)
	}

	updated, warnings := AttachSourceJars(context.Background(), []Dependency{dep}, SourceOptions{
		FetchMissingSources: true,
		Offline:             true,
		DownloadTimeoutSec:  10,
		ExtraSourceDir:      filepath.Join(t.TempDir(), "sources"),
	})

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(updated))
	}
	if updated[0].SourceStatus != SourceStatusAttached {
		t.Fatalf("expected status %s, got %s", SourceStatusAttached, updated[0].SourceStatus)
	}
	if updated[0].SourceJarPath != sourcePath {
		t.Fatalf("expected source path %s, got %s", sourcePath, updated[0].SourceJarPath)
	}
	if updated[0].BinaryJarPath != binaryPath {
		t.Fatalf("expected binary path %s, got %s", binaryPath, updated[0].BinaryJarPath)
	}
}
