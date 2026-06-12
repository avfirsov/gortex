package embedding

import (
	"context"
	"errors"
)

// ErrDisabled is returned when embeddings are not enabled.
var ErrDisabled = errors.New("embeddings disabled")

// NopProvider is a no-op embedding provider used when embeddings are disabled.
type NopProvider struct{}

func (NopProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, ErrDisabled
}

func (NopProvider) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, ErrDisabled
}

func (NopProvider) Dimensions() int { return 0 }
func (NopProvider) Close() error    { return nil }
