package gradle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverModulesAndExtractDependencies(t *testing.T) {
	repo := t.TempDir()

	if err := os.WriteFile(filepath.Join(repo, "settings.gradle.kts"), []byte(`
rootProject.name = "demo"
include(":app", ":lib:core")
`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "build.gradle.kts"), []byte(`
dependencies {
  implementation("org.slf4j:slf4j-api:2.0.13")
  implementation("org.springframework.boot:spring-boot-starter-webflux")
  implementation(platform("org.springframework.ai:spring-ai-bom:2.0.0-M1"))
  testImplementation("org.junit.jupiter:junit-jupiter:$junitVersion")
}
`), 0o644); err != nil {
		t.Fatalf("write app build: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(repo, "lib", "core"), 0o755); err != nil {
		t.Fatalf("mkdir lib core: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "core", "build.gradle"), []byte(`
dependencies {
  api 'com.google.guava:guava:33.2.1-jre'
  runtimeOnly 'org.postgresql:postgresql'
}
`), 0o644); err != nil {
		t.Fatalf("write lib build: %v", err)
	}

	modules, warnings, err := DiscoverModules(repo)
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(modules) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(modules))
	}

	deps, depWarnings, err := ExtractDeclaredDependencies(repo, modules)
	if err != nil {
		t.Fatalf("extract dependencies: %v", err)
	}
	if len(depWarnings) != 0 {
		t.Fatalf("unexpected dependency warnings: %v", depWarnings)
	}
	if len(deps) != 6 {
		t.Fatalf("expected 6 dependencies, got %d: %#v", len(deps), deps)
	}

	var unresolvedCount int
	var platformCount int
	for _, dep := range deps {
		if dep.SourceStatus == SourceStatusUnresolvedVersion {
			unresolvedCount++
		}
		if dep.Type == "platform" {
			platformCount++
		}
	}
	if unresolvedCount != 3 {
		t.Fatalf("expected 3 unresolved dependencies, got %d", unresolvedCount)
	}
	if platformCount != 1 {
		t.Fatalf("expected 1 platform dependency, got %d", platformCount)
	}
}

func TestParseResolvedVersionMap(t *testing.T) {
	output := `
+--- org.slf4j:slf4j-api:2.0.7 -> 2.0.13
\--- com.google.guava:guava:33.2.0-jre
`
	resolved := parseResolvedVersionMap(output)
	if resolved["org.slf4j:slf4j-api"] != "2.0.13" {
		t.Fatalf("expected redirected version 2.0.13, got %q", resolved["org.slf4j:slf4j-api"])
	}
	if resolved["com.google.guava:guava"] != "33.2.0-jre" {
		t.Fatalf("expected guava version 33.2.0-jre, got %q", resolved["com.google.guava:guava"])
	}
}
