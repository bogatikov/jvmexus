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
}
