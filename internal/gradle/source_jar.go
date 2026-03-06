package gradle

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

func AttachSourceJars(ctx context.Context, deps []Dependency, opts SourceOptions) ([]Dependency, []string) {
	gradleHome := gradleUserHome()
	cacheBase := filepath.Join(gradleHome, "caches", "modules-2", "files-2.1")

	if opts.DownloadTimeoutSec <= 0 {
		opts.DownloadTimeoutSec = 60
	}
	if opts.ExtraSourceDir == "" {
		opts.ExtraSourceDir = filepath.Join(".jvmexus", "sources")
	}

	var warnings []string
	updated := make([]Dependency, 0, len(deps))

	for _, dep := range deps {
		if dep.Version == "" || dep.SourceStatus == SourceStatusUnresolvedVersion {
			dep.SourceStatus = SourceStatusUnresolvedVersion
			updated = append(updated, dep)
			continue
		}

		binaryPath, sourcePath := findCachedArtifacts(cacheBase, dep)
		dep.BinaryJarPath = binaryPath
		if sourcePath != "" {
			dep.SourceJarPath = sourcePath
			dep.SourceStatus = SourceStatusAttached
			dep.ResolutionType = ResolutionTypeResolved
			dep.Confidence = max(dep.Confidence, 0.95)
			updated = append(updated, dep)
			continue
		}

		if !opts.FetchMissingSources || opts.Offline {
			if dep.SourceStatus == "" {
				dep.SourceStatus = SourceStatusNotFound
			}
			updated = append(updated, dep)
			continue
		}

		downloadedPath, err := downloadSourceJar(ctx, dep, opts)
		if err != nil {
			dep.SourceStatus = SourceStatusDownloadFailed
			warnings = append(warnings, fmt.Sprintf("source download failed for %s:%s:%s (%v)", dep.GroupID, dep.ArtifactID, dep.Version, err))
			updated = append(updated, dep)
			continue
		}
		if downloadedPath == "" {
			dep.SourceStatus = SourceStatusNotFound
			updated = append(updated, dep)
			continue
		}

		dep.SourceJarPath = downloadedPath
		dep.SourceStatus = SourceStatusDownloaded
		dep.ResolutionType = ResolutionTypeResolved
		dep.Confidence = max(dep.Confidence, 0.92)
		updated = append(updated, dep)
	}

	return updated, warnings
}

func findCachedArtifacts(cacheBase string, dep Dependency) (binaryJar string, sourceJar string) {
	artifactDir := filepath.Join(cacheBase, dep.GroupID, dep.ArtifactID, dep.Version)
	_ = filepath.WalkDir(artifactDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jar") {
			return nil
		}
		if strings.HasSuffix(name, "-sources.jar") {
			if sourceJar == "" {
				sourceJar = path
			}
			return nil
		}
		if strings.HasSuffix(name, "-javadoc.jar") {
			return nil
		}
		if binaryJar == "" {
			binaryJar = path
		}
		return nil
	})
	return binaryJar, sourceJar
}

func downloadSourceJar(ctx context.Context, dep Dependency, opts SourceOptions) (string, error) {
	relPath := filepath.Join(strings.Split(dep.GroupID, ".")...)
	fileName := fmt.Sprintf("%s-%s-sources.jar", dep.ArtifactID, dep.Version)
	url := fmt.Sprintf("https://repo1.maven.org/maven2/%s/%s/%s/%s", relPath, dep.ArtifactID, dep.Version, fileName)

	downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.DownloadTimeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	destDir := filepath.Join(opts.ExtraSourceDir, relPath, dep.ArtifactID, dep.Version)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create source dir: %w", err)
	}
	destPath := filepath.Join(destDir, fileName)

	tmpPath := destPath + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("download copy: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("move file: %w", err)
	}

	absPath, err := filepath.Abs(destPath)
	if err != nil {
		return destPath, nil
	}
	return absPath, nil
}

func gradleUserHome() string {
	if value := strings.TrimSpace(os.Getenv("GRADLE_USER_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".gradle")
	}
	if current, err := user.Current(); err == nil && current.HomeDir != "" {
		return filepath.Join(current.HomeDir, ".gradle")
	}
	return ".gradle"
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
