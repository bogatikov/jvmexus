package gradle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ResolveOptions struct {
	GradleTimeoutSec int
	Offline          bool
	RetryCount       int
	RetryBackoffMS   int
}

var resolvedDepLineRE = regexp.MustCompile(`(?m)([A-Za-z0-9_.\-]+):([A-Za-z0-9_.\-]+):([^\s\(]+)(?:\s+->\s+([^\s\(]+))?`)
var treeEntryRE = regexp.MustCompile(`([A-Za-z0-9_.\-]+):([A-Za-z0-9_.\-]+):([^\s\(]+)(?:\s+->\s+([^\s\(]+))?`)

type resolvedEntry struct {
	GroupID    string
	ArtifactID string
	Version    string
	Depth      int
}

func EnrichResolvedDependencies(ctx context.Context, repoRoot string, modules []Module, declared []Dependency, opts ResolveOptions) ([]Dependency, []Dependency, []string) {
	if opts.Offline {
		return declared, nil, []string{"offline mode enabled; skipping Gradle resolved dependency enrichment"}
	}
	if opts.GradleTimeoutSec <= 0 {
		opts.GradleTimeoutSec = 120
	}
	if opts.RetryCount < 0 {
		opts.RetryCount = 0
	}
	if opts.RetryBackoffMS <= 0 {
		opts.RetryBackoffMS = 300
	}

	gradlewPath := filepath.Join(repoRoot, "gradlew")
	if _, err := os.Stat(gradlewPath); err != nil {
		return declared, nil, []string{"gradlew not found; using declared dependencies only"}
	}

	resolved := make(map[string]string)
	transitive := make(map[string]Dependency)
	directIndex := make(map[string]struct{}, len(declared))
	var warnings []string
	configs := []string{"runtimeClasspath", "compileClasspath", "testRuntimeClasspath", "testCompileClasspath"}

	for _, dep := range declared {
		directIndex[dep.ModuleName+"|"+dep.GroupID+":"+dep.ArtifactID] = struct{}{}
	}

	for _, module := range modules {
		task := "dependencies"
		if module.Name != ":" {
			task = module.Name + ":dependencies"
		}

		for _, configName := range configs {
			stdout, stderr, attempts, err := runGradleDependenciesWithRetry(ctx, repoRoot, task, configName, opts)
			if err != nil {
				warnings = append(warnings, formatGradleResolveWarning(task, configName, err, stderr, attempts))
				continue
			}
			found := parseResolvedVersionMap(stdout)
			for ga, version := range found {
				key := module.Name + "|" + ga
				if _, exists := resolved[key]; !exists {
					resolved[key] = version
				}
			}

			entries := parseResolvedTreeEntries(stdout)
			for _, entry := range entries {
				if entry.Depth <= 0 {
					continue
				}
				ga := entry.GroupID + ":" + entry.ArtifactID
				directKey := module.Name + "|" + ga
				if _, isDirect := directIndex[directKey]; isDirect {
					continue
				}
				depKey := strings.Join([]string{module.Name, configName, entry.GroupID, entry.ArtifactID, entry.Version}, "|")
				if _, exists := transitive[depKey]; exists {
					continue
				}
				transitive[depKey] = Dependency{
					ModuleName:     module.Name,
					GroupID:        entry.GroupID,
					ArtifactID:     entry.ArtifactID,
					Version:        entry.Version,
					Scope:          configName,
					Type:           "external",
					Kind:           DependencyKindTransitive,
					SourceStatus:   SourceStatusNotFound,
					ResolutionType: ResolutionTypeResolved,
					Confidence:     0.8,
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

	transitiveList := make([]Dependency, 0, len(transitive))
	for _, dep := range transitive {
		transitiveList = append(transitiveList, dep)
	}

	return updated, transitiveList, dedupeWarnings(warnings)
}

func runGradleDependenciesWithRetry(ctx context.Context, repoRoot, task, configuration string, opts ResolveOptions) (string, string, int, error) {
	maxAttempts := opts.RetryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastStdout string
	var lastStderr string
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stdout, stderr, err := runGradleDependencies(ctx, repoRoot, task, configuration, opts.GradleTimeoutSec)
		if err == nil {
			return stdout, stderr, attempt, nil
		}

		lastStdout = stdout
		lastStderr = stderr
		lastErr = err
		if attempt == maxAttempts || !isRetryableGradleError(err, stderr) {
			break
		}

		backoff := time.Duration(opts.RetryBackoffMS*attempt) * time.Millisecond
		select {
		case <-ctx.Done():
			return lastStdout, lastStderr, attempt, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastStdout, lastStderr, maxAttempts, lastErr
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
	if err != nil {
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			return stdout.String(), stderr.String(), fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
	}

	return stdout.String(), stderr.String(), err
}

func formatGradleResolveWarning(task, configuration string, err error, stderr string, attempts int) string {
	class := classifyGradleFailure(err, stderr)
	stderrSummary := compactStderr(stderr)
	warning := fmt.Sprintf("gradle %s --configuration %s failed (%s) after %d attempt(s): %v", task, configuration, class, attempts, err)
	if stderrSummary != "" {
		warning += " | stderr=" + stderrSummary
	}
	return warning
}

func classifyGradleFailure(err error, stderr string) string {
	if err == nil {
		return "unknown"
	}
	lowerErr := strings.ToLower(err.Error())
	lowerStderr := strings.ToLower(stderr)
	combined := lowerErr + " " + lowerStderr

	if strings.Contains(lowerErr, "timed out") || strings.Contains(combined, "deadline exceeded") {
		return "timeout"
	}
	if containsAny(combined, "unauthorized", "forbidden", "authentication", "credentials", "401", "403") {
		return "auth"
	}
	if containsAny(combined,
		"connection refused",
		"network is unreachable",
		"unknown host",
		"could not resolve",
		"read timed out",
		"connect timed out",
		"connection reset",
		"tls handshake timeout",
	) {
		return "network"
	}
	if containsAny(combined, "repo", "repository", "artifact", "maven") {
		return "repository"
	}
	if code := parseExitCode(lowerErr); code != 0 {
		return "execution"
	}
	return "execution"
}

func compactStderr(stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	const maxLen = 240
	if len(trimmed) > maxLen {
		return trimmed[:maxLen] + "..."
	}
	return trimmed
}

func isRetryableGradleError(err error, stderr string) bool {
	if err == nil {
		return false
	}
	class := classifyGradleFailure(err, stderr)
	return class == "timeout" || class == "network" || class == "repository"
}

func containsAny(input string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(input, candidate) {
			return true
		}
	}
	return false
}

func parseExitCode(errText string) int {
	const prefix = "exit code "
	idx := strings.Index(errText, prefix)
	if idx < 0 {
		return 0
	}
	raw := strings.TrimSpace(errText[idx+len(prefix):])
	if raw == "" {
		return 0
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return 0
	}
	value, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return value
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

func parseResolvedTreeEntries(output string) []resolvedEntry {
	lines := strings.Split(output, "\n")
	out := make([]resolvedEntry, 0, 128)
	for _, line := range lines {
		idx := strings.Index(line, "---")
		if idx < 0 {
			continue
		}
		match := treeEntryRE.FindStringSubmatch(line)
		if len(match) < 4 {
			continue
		}
		version := strings.TrimSpace(match[3])
		if len(match) > 4 {
			redirect := strings.TrimSpace(match[4])
			if redirect != "" {
				version = redirect
			}
		}
		if version == "" {
			continue
		}

		prefix := line[:idx]
		depth := strings.Count(prefix, "|    ") + strings.Count(prefix, "     ")
		out = append(out, resolvedEntry{
			GroupID:    strings.TrimSpace(match[1]),
			ArtifactID: strings.TrimSpace(match[2]),
			Version:    version,
			Depth:      depth,
		})
	}
	return out
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
