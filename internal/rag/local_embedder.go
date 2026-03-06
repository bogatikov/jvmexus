package rag

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type LocalEmbedder struct {
	modelID     string
	modelDir    string
	dimensions  int
	initialized bool
}

func NewLocalEmbedder(modelID, cacheDir string) *LocalEmbedder {
	slug := sanitizeModelID(modelID)
	if slug == "" {
		slug = "local-model"
	}
	return &LocalEmbedder{
		modelID:    modelID,
		modelDir:   filepath.Join(cacheDir, slug),
		dimensions: 384,
	}
}

func (e *LocalEmbedder) ModelID() string {
	return e.modelID
}

func (e *LocalEmbedder) EnsureReady(_ context.Context) error {
	if e.initialized {
		return nil
	}
	if err := os.MkdirAll(e.modelDir, 0o755); err != nil {
		return fmt.Errorf("create model cache dir: %w", err)
	}
	manifestPath := filepath.Join(e.modelDir, "model.manifest")
	if _, err := os.Stat(manifestPath); err != nil {
		manifest := fmt.Sprintf("model_id=%s\ndimensions=%d\n", e.modelID, e.dimensions)
		if writeErr := os.WriteFile(manifestPath, []byte(manifest), 0o644); writeErr != nil {
			return fmt.Errorf("write model manifest: %w", writeErr)
		}
	}
	e.initialized = true
	return nil
}

func (e *LocalEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if err := e.EnsureReady(ctx); err != nil {
		return nil, err
	}
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		out = append(out, embedText(text, e.dimensions))
	}
	return out, nil
}

func embedText(text string, dim int) []float64 {
	vec := make([]float64, dim)
	tokens := tokenize(text)
	for _, token := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		sum := h.Sum64()
		idx := int(sum % uint64(dim))
		sign := 1.0
		if (sum>>63)&1 == 1 {
			sign = -1.0
		}
		vec[idx] += sign
	}
	normalize(vec)
	return vec
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	dot := 0.0
	na := 0.0
	nb := 0.0
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func normalize(vec []float64) {
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return
	}
	inv := 1.0 / math.Sqrt(norm)
	for i := range vec {
		vec[i] *= inv
	}
}

var tokenRE = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]+`)
var modelSlugRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func tokenize(text string) []string {
	matches := tokenRE.FindAllString(strings.ToLower(text), -1)
	if len(matches) == 0 {
		return []string{strings.ToLower(strings.TrimSpace(text))}
	}
	return matches
}

func sanitizeModelID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "-")
	value = modelSlugRE.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-. ")
	return value
}
