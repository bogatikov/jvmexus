package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestListDependenciesByProjectIDWithMode_FiltersTransitive(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	project, err := st.UpsertProject(ctx, "demo", "/tmp/demo")
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	deps := []Dependency{
		{
			ModuleName:     ":",
			GroupID:        "a",
			ArtifactID:     "direct-lib",
			Version:        "1.0.0",
			Scope:          "implementation",
			Type:           "external",
			Kind:           "direct",
			SourceStatus:   "not_found",
			ResolutionType: "resolved",
			Confidence:     0.9,
		},
		{
			ModuleName:     ":",
			GroupID:        "b",
			ArtifactID:     "transitive-lib",
			Version:        "2.0.0",
			Scope:          "runtimeClasspath",
			Type:           "external",
			Kind:           "transitive",
			SourceStatus:   "not_found",
			ResolutionType: "resolved",
			Confidence:     0.8,
		},
	}

	if err := st.ReplaceDependencies(ctx, project.ID, deps); err != nil {
		t.Fatalf("replace dependencies: %v", err)
	}

	directOnly, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, false)
	if err != nil {
		t.Fatalf("list direct dependencies: %v", err)
	}
	if len(directOnly) != 1 {
		t.Fatalf("expected 1 direct dependency, got %d", len(directOnly))
	}
	if directOnly[0].Kind != "direct" {
		t.Fatalf("expected direct kind, got %s", directOnly[0].Kind)
	}

	withTransitive, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, true)
	if err != nil {
		t.Fatalf("list all dependencies: %v", err)
	}
	if len(withTransitive) != 2 {
		t.Fatalf("expected 2 dependencies including transitive, got %d", len(withTransitive))
	}
}
