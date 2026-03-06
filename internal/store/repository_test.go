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

func TestSymbolQueries_FindWithFilterAndOutgoingRefs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "symbols.db"))
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

	symbols := []Symbol{
		{FilePath: "src/main/kotlin/com/example/A.kt", Language: "kotlin", Name: "BotService", FQName: "com.example.BotService", Kind: "Type", StartLine: 3, EndLine: 3},
		{FilePath: "src/main/kotlin/com/example/A.kt", Language: "kotlin", Name: "sendMessage", FQName: "com.example.sendMessage", Kind: "Function", StartLine: 6, EndLine: 6},
		{FilePath: "src/test/kotlin/com/example/B.kt", Language: "kotlin", Name: "BotService", FQName: "com.example.test.BotService", Kind: "Type", StartLine: 2, EndLine: 2},
	}
	if err := st.ReplaceSymbols(ctx, project.ID, symbols); err != nil {
		t.Fatalf("replace symbols: %v", err)
	}

	refs := []SymbolReference{
		{FromName: "sendMessage", FromFile: "src/main/kotlin/com/example/A.kt", ToName: "println", RefType: "CALLS", Confidence: 0.7, Evidence: "println(text)"},
		{FromName: "BotService", FromFile: "src/main/kotlin/com/example/A.kt", ToName: "List", ToFQName: "kotlin.collections.List", RefType: "IMPORTS", Confidence: 0.95, Evidence: "import kotlin.collections.List"},
	}
	if err := st.ReplaceSymbolReferences(ctx, project.ID, refs); err != nil {
		t.Fatalf("replace refs: %v", err)
	}

	filtered, err := st.FindSymbolsWithFilter(ctx, project.ID, "BotService", "src/main", 20)
	if err != nil {
		t.Fatalf("find symbols with filter: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered symbol, got %d", len(filtered))
	}

	exact, err := st.FindSymbolsByExactName(ctx, project.ID, "com.example.sendMessage", 10)
	if err != nil {
		t.Fatalf("find exact symbol: %v", err)
	}
	if len(exact) != 1 {
		t.Fatalf("expected 1 exact symbol match, got %d", len(exact))
	}

	outgoing, err := st.ListOutgoingReferences(ctx, project.ID, symbols[1], 20)
	if err != nil {
		t.Fatalf("list outgoing refs: %v", err)
	}
	if len(outgoing) == 0 {
		t.Fatalf("expected outgoing refs, got 0")
	}
}
