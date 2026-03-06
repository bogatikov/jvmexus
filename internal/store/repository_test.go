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

func TestSearchChunks_ReturnsFTSMatches(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "chunks.db"))
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

	chunks := []Chunk{
		{FilePath: "src/main/java/com/example/Util.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "public class Util { static String helper() { return \"pong\"; } }", TokenCount: 12},
		{FilePath: "src/main/java/com/example/Bot.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "public class Bot { String ping() { return Util.helper(); } }", TokenCount: 12},
	}
	if err := st.ReplaceChunks(ctx, project.ID, chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}

	results, err := st.SearchChunks(ctx, project.ID, "Util helper", 5)
	if err != nil {
		t.Fatalf("search chunks: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one chunk match")
	}
}

func TestIndexedFiles_ReplaceAndList(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "indexed.db"))
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

	files := []IndexedFile{
		{FilePath: "app/src/main/java/A.java", FileKind: "source", SHA256: "a1", SizeBytes: 10, MtimeUnix: 1000},
		{FilePath: "app/build.gradle.kts", FileKind: "build", SHA256: "b1", SizeBytes: 20, MtimeUnix: 1001},
	}
	if err := st.ReplaceIndexedFiles(ctx, project.ID, files); err != nil {
		t.Fatalf("replace indexed files: %v", err)
	}

	stored, err := st.ListIndexedFilesByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("list indexed files: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 indexed files, got %d", len(stored))
	}
	if stored[0].FilePath != "app/build.gradle.kts" || stored[1].FilePath != "app/src/main/java/A.java" {
		t.Fatalf("unexpected indexed file ordering: %#v", stored)
	}

	if err := st.ReplaceIndexedFiles(ctx, project.ID, files[:1]); err != nil {
		t.Fatalf("replace indexed files second pass: %v", err)
	}
	stored, err = st.ListIndexedFilesByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("list indexed files second pass: %v", err)
	}
	if len(stored) != 1 || stored[0].FilePath != "app/src/main/java/A.java" {
		t.Fatalf("expected single replaced indexed file, got %#v", stored)
	}
}

func TestDeleteByFilePathHelpers(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "delete-by-path.db"))
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
		{FilePath: "src/A.java", Language: "java", Name: "A", FQName: "A", Kind: "Type", StartLine: 1, EndLine: 1},
		{FilePath: "src/B.java", Language: "java", Name: "B", FQName: "B", Kind: "Type", StartLine: 1, EndLine: 1},
	}
	if err := st.ReplaceSymbols(ctx, project.ID, symbols); err != nil {
		t.Fatalf("replace symbols: %v", err)
	}

	refs := []SymbolReference{
		{FromName: "A", FromFile: "src/A.java", ToName: "println", RefType: "CALLS", Confidence: 0.8},
		{FromName: "B", FromFile: "src/B.java", ToName: "println", RefType: "CALLS", Confidence: 0.8},
	}
	if err := st.ReplaceSymbolReferences(ctx, project.ID, refs); err != nil {
		t.Fatalf("replace symbol refs: %v", err)
	}

	chunks := []Chunk{
		{FilePath: "src/A.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "class A {}", TokenCount: 3},
		{FilePath: "src/B.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "class B {}", TokenCount: 3},
	}
	if err := st.ReplaceChunks(ctx, project.ID, chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}

	paths := []string{"src/A.java"}
	if err := st.DeleteSymbolsByFilePaths(ctx, project.ID, paths); err != nil {
		t.Fatalf("delete symbols by file path: %v", err)
	}
	if err := st.DeleteSymbolReferencesByFromFilePaths(ctx, project.ID, paths); err != nil {
		t.Fatalf("delete refs by file path: %v", err)
	}
	if err := st.DeleteChunksByFilePaths(ctx, project.ID, paths); err != nil {
		t.Fatalf("delete chunks by file path: %v", err)
	}

	remainingSymbols, err := st.FindSymbolsWithFilter(ctx, project.ID, "", "", 100)
	if err != nil {
		t.Fatalf("find symbols after delete: %v", err)
	}
	if len(remainingSymbols) != 1 || remainingSymbols[0].FilePath != "src/B.java" {
		t.Fatalf("expected only src/B.java symbol to remain, got %#v", remainingSymbols)
	}

	outgoing, err := st.ListOutgoingReferences(ctx, project.ID, Symbol{Name: "B", FilePath: "src/B.java"}, 10)
	if err != nil {
		t.Fatalf("list outgoing refs: %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("expected 1 outgoing ref for B, got %d", len(outgoing))
	}

	searchResults, err := st.SearchChunks(ctx, project.ID, "class", 10)
	if err != nil {
		t.Fatalf("search chunks after delete: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].FilePath != "src/B.java" {
		t.Fatalf("expected only src/B.java chunk to remain, got %#v", searchResults)
	}
}
