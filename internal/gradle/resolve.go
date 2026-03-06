package gradle

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ResolveOptions struct {
	GradleTimeoutSec int
	Offline          bool
}

var resolvedDepLineRE = regexp.MustCompile(`(?m)([A-Za-z0-9_.\-]+):([A-Za-z0-9_.\-]+):([^\s\(]+)(?:\s+->\s+([^\s\(]+))?`)

func EnrichResolvedDependencies(ctx context.Context, repoRoot string, modules []Module, declared []Dependency, opts ResolveOptions) ([]Dependency, []string) {
	if opts.Offline {
		return declared, []string{"offline mode enabled; skipping Gradle resolved dependency enrichment"}
	}
	if opts.GradleTimeoutSec <= 0 {
		opts.GradleTimeoutSec = 120
	}

	gradlewPath := filepath.Join(repoRoot, "gradlew")
	if _, err := os.Stat(gradlewPath); err != nil {
		return declared, []string{"gradlew not found; using declared dependencies only"}
	}

	resolved := make(map[string]string)
	var warnings []string
	configs := []string{"runtimeClasspath", "compileClasspath", "testRuntimeClasspath", "testCompileClasspath"}

	for _, module := range modules {
		task := "dependencies"
		if module.Name != ":" {
			task = module.Name + ":dependencies"
		}

		for _, configName := range configs {
			stdout, stderr, err := runGradleDependencies(ctx, repoRoot, task, configName, opts.GradleTimeoutSec)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("gradle %s --configuration %s failed: %v (%s)", task, configName, err, strings.TrimSpace(stderr)))
				continue
			}
			found := parseResolvedVersionMap(stdout)
			for ga, version := range found {
				key := module.Name + "|" + ga
				if _, exists := resolved[key]; !exists {
					resolved[key] = version
				}
			}
		}
	}

	updated := make([]Dependency, 0, len(declared))
	for _, dep := range declared {
		ga := dep.GroupID + ":" + dep.ArtifactID
		resolvedVersion, ok := resolved[dep.ModuleName+"|"+ga]
		if !ok {
			updated = append(updated, dep)
			continue
		}

		dep.Version = resolvedVersion
		dep.ResolutionType = ResolutionTypeResolved
		if dep.SourceStatus == SourceStatusUnresolvedVersion {
			dep.SourceStatus = SourceStatusNotFound
		}
		dep.Confidence = max(dep.Confidence, 0.9)
		updated = append(updated, dep)
	}

	return updated, dedupeWarnings(warnings)
}

func runGradleDependencies(ctx context.Context, repoRoot, task, configuration string, timeoutSec int) (string, string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	args := []string{task, "--configuration", configuration, "--console=plain", "--quiet", "--no-daemon"}
	cmd := exec.CommandContext(commandCtx, "./gradlew", args...)
	cmd.Dir = repoRoot

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if commandCtx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("timed out after %ds", timeoutSec)
	}

	return stdout.String(), stderr.String(), err
}

func parseResolvedVersionMap(output string) map[string]string {
	result := map[string]string{}
	for _, match := range resolvedDepLineRE.FindAllStringSubmatch(output, -1) {
		if len(match) < 4 {
			continue
		}
		groupID := strings.TrimSpace(match[1])
		artifactID := strings.TrimSpace(match[2])
		version := strings.TrimSpace(match[3])
		if len(match) > 4 {
			redirect := strings.TrimSpace(match[4])
			if redirect != "" {
				version = redirect
			}
		}
		if groupID == "" || artifactID == "" || version == "" {
			continue
		}
		result[groupID+":"+artifactID] = version
	}
	return result
}

func dedupeWarnings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
