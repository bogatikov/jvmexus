package gradle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var includeCallRE = regexp.MustCompile(`include\(([^\)]*)\)`)

var dependencyCallRE = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(platform\s*\(\s*)?["']([^:"'\s]+):([^:"'\s]+)(?::([^"']+))?["']\s*\)?\s*\)`)

var dependencyNoParenRE = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s+(platform\s*\(\s*)?["']([^:"'\s]+):([^:"'\s]+)(?::([^"']+))?["']\s*\)?`)

var dependencyNoParenNoVersionRE = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s+(platform\s*\(\s*)?["']([^:"'\s]+):([^:"'\s]+)["']\s*\)?`)

var supportedScopes = map[string]struct{}{
	"implementation":            {},
	"api":                       {},
	"compileonly":               {},
	"runtimeonly":               {},
	"annotationprocessor":       {},
	"kapt":                      {},
	"testimplementation":        {},
	"testruntimeonly":           {},
	"testcompileonly":           {},
	"androidtestimplementation": {},
}

func DiscoverModules(repoRoot string) ([]Module, []string, error) {
	modules := []Module{{Name: ":", Path: "."}}
	var warnings []string

	settingsPath, found := firstExisting(
		filepath.Join(repoRoot, "settings.gradle.kts"),
		filepath.Join(repoRoot, "settings.gradle"),
	)
	if !found {
		warnings = append(warnings, "settings.gradle(.kts) not found; using root module only")
		return modules, warnings, nil
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, warnings, fmt.Errorf("read settings file: %w", err)
	}

	matches := includeCallRE.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		return modules, warnings, nil
	}

	seen := map[string]struct{}{":": {}}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		for _, token := range strings.Split(m[1], ",") {
			token = strings.TrimSpace(token)
			token = strings.Trim(token, "\"'")
			if token == "" {
				continue
			}
			if !strings.HasPrefix(token, ":") {
				token = ":" + token
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			modules = append(modules, Module{
				Name: token,
				Path: modulePath(token),
			})
		}
	}

	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	return modules, warnings, nil
}

func ExtractDeclaredDependencies(repoRoot string, modules []Module) ([]Dependency, []string, error) {
	var out []Dependency
	var warnings []string

	for _, module := range modules {
		buildPath, found := firstExisting(
			filepath.Join(repoRoot, module.Path, "build.gradle.kts"),
			filepath.Join(repoRoot, module.Path, "build.gradle"),
		)
		if !found {
			continue
		}

		content, err := os.ReadFile(buildPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("unable to read %s: %v", buildPath, err))
			continue
		}

		matches := dependencyCallRE.FindAllStringSubmatch(string(content), -1)
		for _, match := range matches {
			if dep, ok := parseDependencyCallMatch(module.Name, match); ok {
				out = append(out, dep)
			}
		}

		matches = dependencyNoParenRE.FindAllStringSubmatch(string(content), -1)
		for _, match := range matches {
			if dep, ok := parseDependencyNoParenMatch(module.Name, match); ok {
				out = append(out, dep)
			}
		}

		matches = dependencyNoParenNoVersionRE.FindAllStringSubmatch(string(content), -1)
		for _, match := range matches {
			if dep, ok := parseDependencyNoParenNoVersionMatch(module.Name, match); ok {
				out = append(out, dep)
			}
		}
	}

	return dedupeDependencies(out), warnings, nil
}

func parseDependencyCallMatch(moduleName string, match []string) (Dependency, bool) {
	if len(match) < 6 {
		return Dependency{}, false
	}

	scope := strings.TrimSpace(match[1])
	if !isSupportedScope(scope) {
		return Dependency{}, false
	}

	isPlatform := strings.TrimSpace(match[2]) != ""
	groupID := strings.TrimSpace(match[3])
	artifactID := strings.TrimSpace(match[4])
	version := strings.TrimSpace(match[5])

	return buildDependency(moduleName, scope, groupID, artifactID, version, isPlatform), true
}

func parseDependencyNoParenMatch(moduleName string, match []string) (Dependency, bool) {
	if len(match) < 5 {
		return Dependency{}, false
	}

	scope := strings.TrimSpace(match[1])
	if !isSupportedScope(scope) {
		return Dependency{}, false
	}

	isPlatform := strings.TrimSpace(match[2]) != ""
	groupID := strings.TrimSpace(match[3])
	artifactID := strings.TrimSpace(match[4])
	version := ""
	if len(match) > 5 {
		version = strings.TrimSpace(match[5])
	}

	return buildDependency(moduleName, scope, groupID, artifactID, version, isPlatform), true
}

func parseDependencyNoParenNoVersionMatch(moduleName string, match []string) (Dependency, bool) {
	if len(match) < 5 {
		return Dependency{}, false
	}

	scope := strings.TrimSpace(match[1])
	if !isSupportedScope(scope) {
		return Dependency{}, false
	}

	isPlatform := strings.TrimSpace(match[2]) != ""
	groupID := strings.TrimSpace(match[3])
	artifactID := strings.TrimSpace(match[4])

	return buildDependency(moduleName, scope, groupID, artifactID, "", isPlatform), true
}

func buildDependency(moduleName, scope, groupID, artifactID, version string, isPlatform bool) Dependency {
	depType := "external"
	if isPlatform {
		depType = "platform"
	}

	status := SourceStatusNotFound
	resolutionType := ResolutionTypeDeclared
	confidence := 0.75
	if version == "" || strings.Contains(version, "$") || strings.Contains(version, "{") {
		status = SourceStatusUnresolvedVersion
		confidence = 0.65
	}

	return Dependency{
		ModuleName:     moduleName,
		GroupID:        groupID,
		ArtifactID:     artifactID,
		Version:        version,
		Scope:          scope,
		Type:           depType,
		SourceStatus:   status,
		ResolutionType: resolutionType,
		Confidence:     confidence,
	}
}

func isSupportedScope(scope string) bool {
	_, ok := supportedScopes[strings.ToLower(strings.TrimSpace(scope))]
	return ok
}

func firstExisting(paths ...string) (string, bool) {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func modulePath(moduleName string) string {
	if moduleName == ":" {
		return "."
	}
	parts := strings.Split(strings.TrimPrefix(moduleName, ":"), ":")
	return filepath.Join(parts...)
}

func dedupeDependencies(deps []Dependency) []Dependency {
	seen := make(map[string]struct{}, len(deps))
	result := make([]Dependency, 0, len(deps))
	for _, dep := range deps {
		key := strings.Join([]string{
			dep.ModuleName,
			dep.Scope,
			dep.GroupID,
			dep.ArtifactID,
			dep.Version,
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, dep)
	}
	return result
}
