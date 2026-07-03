//go:build !embeddings_onnx

package embedding

import "fmt"

func newONNXProvider() (Provider, error) {
	return nil, fmt.Errorf("ONNX provider not compiled in (build with -tags embeddings_onnx): %w", ErrBackendNotCompiled)
}
