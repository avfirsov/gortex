//go:build !embeddings_gomlx

package embedding

import "fmt"

func newGoMLXProvider() (Provider, error) {
	return nil, fmt.Errorf("GoMLX provider not compiled in (build with -tags \"embeddings_gomlx XLA\"): %w", ErrBackendNotCompiled)
}
