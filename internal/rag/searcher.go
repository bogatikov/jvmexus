package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/store"
)

type Searcher struct {
	store    *store.Store
	embedder Embedder
}

type Result struct {
	Chunk         store.Chunk `json:"chunk"`
	LexicalScore  float64     `json:"lexicalScore"`
	SemanticScore float64     `json:"semanticScore"`
	HybridScore   float64     `json:"hybridScore"`
	SourceOrigin  string      `json:"sourceOrigin,omitempty"`
	Dependency    string      `json:"dependency,omitempty"`
	JarEntryPath  string      `json:"jarEntryPath,omitempty"`
}

func NewSearcher(cfg config.Config, st *store.Store) *Searcher {
	return &Searcher{
		store:    st,
		embedder: NewLocalEmbedder(cfg.EmbeddingModelID, cfg.ModelCacheDir),
	}
}

func (s *Searcher) ModelID() string {
	if s == nil || s.embedder == nil {
		return ""
	}
	return s.embedder.ModelID()
}

func (s *Searcher) Search(ctx context.Context, projectID int64, query string, limit int) ([]Result, error) {
	return s.SearchWithScope(ctx, projectID, query, limit, "all")
}

func (s *Searcher) SearchWithScope(ctx context.Context, projectID int64, query string, limit int, scope string) ([]Result, error) {
	if limit <= 0 {
		limit = 10
	}
	scope = normalizeScope(scope)
	fetchLimit := limit * 6
	if fetchLimit < 20 {
		fetchLimit = 20
	}

	candidates, err := s.store.SearchChunksWithScope(ctx, projectID, query, fetchLimit, scope)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	queryVecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	queryVec := queryVecs[0]

	texts := make([]string, 0, len(candidates))
	for _, c := range candidates {
		texts = append(texts, c.Text)
	}
	chunkVecs, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed chunks: %w", err)
	}

	results := make([]Result, 0, len(candidates))
	for i, c := range candidates {
		lexical := rankByOrder(i, len(candidates))
		semantic := cosine(queryVec, chunkVecs[i])
		hybrid := 0.55*semantic + 0.45*lexical
		sourceOrigin, dependency, jarEntryPath := chunkProvenance(c.FilePath)
		results = append(results, Result{
			Chunk:         c,
			LexicalScore:  lexical,
			SemanticScore: semantic,
			HybridScore:   hybrid,
			SourceOrigin:  sourceOrigin,
			Dependency:    dependency,
			JarEntryPath:  jarEntryPath,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].HybridScore == results[j].HybridScore {
			return results[i].Chunk.Score < results[j].Chunk.Score
		}
		return results[i].HybridScore > results[j].HybridScore
	})

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func normalizeScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != "project" && scope != "libraries" && scope != "all" {
		return "all"
	}
	return scope
}

func chunkProvenance(filePath string) (string, string, string) {
	if !strings.HasPrefix(filePath, "jar://") {
		return "project", "", ""
	}
	trimmed := strings.TrimPrefix(filePath, "jar://")
	parts := strings.SplitN(trimmed, "!/", 2)
	coord := ""
	entry := ""
	if len(parts) > 0 {
		coord = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		entry = strings.TrimSpace(parts[1])
	}
	return "library_source", coord, entry
}

func rankByOrder(index, total int) float64 {
	if total <= 1 {
		return 1
	}
	return 1.0 - (float64(index) / float64(total-1))
}
