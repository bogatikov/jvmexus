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
	"sync"

	fastembed "github.com/anush008/fastembed-go"
)

type LocalEmbedder struct {
	modelID      string
	resolvedID   string
	modelDir     string
	dimensions   int
	batchSize    int
	initialized  bool
	useFastEmbed bool

	fast *fastembed.FlagEmbedding
	one  sync.Once
	err  error

	fallback *HashedEmbedder
}

func NewLocalEmbedder(modelID, cacheDir string) *LocalEmbedder {
	slug := sanitizeModelID(modelID)
	if slug == "" {
		slug = "local-model"
	}
	resolvedModel := resolveFastEmbedModel(modelID)
	return &LocalEmbedder{
		modelID:      modelID,
		resolvedID:   string(resolvedModel),
		modelDir:     filepath.Join(cacheDir, slug),
		dimensions:   384,
		batchSize:    128,
		useFastEmbed: true,
		fallback:     NewHashedEmbedder(modelID, cacheDir),
	}
}

func (e *LocalEmbedder) ModelID() string {
	if e.resolvedID == "" {
		return e.modelID
	}
	return e.resolvedID
}

func (e *LocalEmbedder) EnsureReady(_ context.Context) error {
	e.one.Do(func() {
		if err := os.MkdirAll(e.modelDir, 0o755); err != nil {
			e.err = fmt.Errorf("create model cache dir: %w", err)
			return
		}

		if e.useFastEmbed {
			showDownload := false
			fast, err := fastembed.NewFlagEmbedding(&fastembed.InitOptions{
				Model:                resolveFastEmbedModel(e.modelID),
				CacheDir:             e.modelDir,
				ShowDownloadProgress: &showDownload,
			})
			if err == nil {
				e.fast = fast
				e.initialized = true
				_ = e.writeManifest("fastembed")
				return
			}
		}

		if err := e.fallback.EnsureReady(context.Background()); err != nil {
			e.err = err
			return
		}
		e.initialized = true
		_ = e.writeManifest("hashed-fallback")
	})

	if e.err != nil {
		return e.err
	}
	if !e.initialized {
		return fmt.Errorf("embedder initialization failed")
	}
	return nil
}

func (e *LocalEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if err := e.EnsureReady(ctx); err != nil {
		return nil, err
	}
	if e.fast != nil {
		vecs, err := e.fast.Embed(texts, e.batchSize)
		if err == nil {
			return toFloat64Vectors(vecs), nil
		}
	}
	return e.fallback.Embed(ctx, texts)
}

func (e *LocalEmbedder) writeManifest(mode string) error {
	manifestPath := filepath.Join(e.modelDir, "model.manifest")
	manifest := fmt.Sprintf("model_id=%s\nresolved_id=%s\ndimensions=%d\nmode=%s\n", e.modelID, e.resolvedID, e.dimensions, mode)
	return os.WriteFile(manifestPath, []byte(manifest), 0o644)
}

type HashedEmbedder struct {
	modelID     string
	modelDir    string
	dimensions  int
	initialized bool
}

func NewHashedEmbedder(modelID, cacheDir string) *HashedEmbedder {
	slug := sanitizeModelID(modelID)
	if slug == "" {
		slug = "local-model"
	}
	return &HashedEmbedder{
		modelID:    modelID,
		modelDir:   filepath.Join(cacheDir, slug),
		dimensions: 384,
	}
}

func (e *HashedEmbedder) ModelID() string {
	return e.modelID
}

func (e *HashedEmbedder) EnsureReady(_ context.Context) error {
	if e.initialized {
		return nil
	}
	if err := os.MkdirAll(e.modelDir, 0o755); err != nil {
		return fmt.Errorf("create model cache dir: %w", err)
	}
	e.initialized = true
	return nil
}

func (e *HashedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if err := e.EnsureReady(ctx); err != nil {
		return nil, err
	}
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		out = append(out, embedText(text, e.dimensions))
	}
	return out, nil
}

func resolveFastEmbedModel(modelID string) fastembed.EmbeddingModel {
	modelID = strings.TrimSpace(modelID)
	switch strings.ToLower(modelID) {
	case "snowflake/snowflake-arctic-embed-xs", "snowflake-arctic-embed-xs", "arctic-embed-xs":
		return fastembed.BGESmallENV15
	case strings.ToLower(string(fastembed.AllMiniLML6V2)):
		return fastembed.AllMiniLML6V2
	case strings.ToLower(string(fastembed.BGEBaseEN)):
		return fastembed.BGEBaseEN
	case strings.ToLower(string(fastembed.BGEBaseENV15)):
		return fastembed.BGEBaseENV15
	case strings.ToLower(string(fastembed.BGESmallEN)):
		return fastembed.BGESmallEN
	case strings.ToLower(string(fastembed.BGESmallENV15)):
		return fastembed.BGESmallENV15
	case strings.ToLower(string(fastembed.BGESmallZH)):
		return fastembed.BGESmallZH
	default:
		for _, info := range fastembed.ListSupportedModels() {
			if strings.EqualFold(string(info.Model), modelID) {
				return info.Model
			}
		}
		return fastembed.BGESmallENV15
	}
}

func toFloat64Vectors(input [][]float32) [][]float64 {
	out := make([][]float64, 0, len(input))
	for _, vec := range input {
		converted := make([]float64, len(vec))
		for i := range vec {
			converted[i] = float64(vec[i])
		}
		out = append(out, converted)
	}
	return out
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
