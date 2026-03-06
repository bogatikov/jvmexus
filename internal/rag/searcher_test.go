package rag

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/store"
)

func TestSearcherRanksRelevantChunk(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := store.Open(filepath.Join(tmp, "rag.db"))
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

	chunks := []store.Chunk{
		{FilePath: "src/main/java/com/example/Util.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "public class Util { static String helper() { return \"pong\"; } }", TokenCount: 12},
		{FilePath: "src/main/java/com/example/Noise.java", Language: "java", ChunkType: "code_window", ChunkIndex: 0, Text: "public class Noise { void random() { int x = 1; } }", TokenCount: 12},
		{FilePath: "jar://org.slf4j:slf4j-api:2.0.13!/org/slf4j/Logger.java", Language: "java", ChunkType: "library_code_window", ChunkIndex: 0, Text: "public interface Logger { void info(String msg); }", TokenCount: 8},
	}
	if err := st.ReplaceChunks(ctx, project.ID, chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}

	searcher := NewSearcher(config.Config{EmbeddingModelID: "Snowflake/snowflake-arctic-embed-xs", ModelCacheDir: filepath.Join(tmp, "models")}, st)
	results, err := searcher.Search(ctx, project.ID, "Util helper", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected non-empty results")
	}
	if results[0].Chunk.FilePath != "src/main/java/com/example/Util.java" {
		t.Fatalf("expected top chunk from Util.java, got %s", results[0].Chunk.FilePath)
	}

	projectOnly, err := searcher.SearchWithScope(ctx, project.ID, "Logger info", 5, "project")
	if err != nil {
		t.Fatalf("search project scope: %v", err)
	}
	for _, item := range projectOnly {
		if item.SourceOrigin != "project" {
			t.Fatalf("expected project-only results, got origin=%s path=%s", item.SourceOrigin, item.Chunk.FilePath)
		}
	}

	librariesOnly, err := searcher.SearchWithScope(ctx, project.ID, "Logger info", 5, "libraries")
	if err != nil {
		t.Fatalf("search libraries scope: %v", err)
	}
	if len(librariesOnly) == 0 {
		t.Fatalf("expected non-empty library results")
	}
	if librariesOnly[0].SourceOrigin != "library_source" {
		t.Fatalf("expected library source origin, got %s", librariesOnly[0].SourceOrigin)
	}
	if librariesOnly[0].Dependency == "" || librariesOnly[0].JarEntryPath == "" {
		t.Fatalf("expected library provenance fields, got dependency=%q jarEntryPath=%q", librariesOnly[0].Dependency, librariesOnly[0].JarEntryPath)
	}
}
