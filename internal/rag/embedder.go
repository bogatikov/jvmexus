package rag

import "context"

type Embedder interface {
	EnsureReady(ctx context.Context) error
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	ModelID() string
}
