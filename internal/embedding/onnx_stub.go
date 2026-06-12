//go:build !embeddings_onnx

package embedding

import "errors"

func newONNXProvider() (Provider, error) {
	return nil, errors.New("ONNX provider not compiled in (build with -tags embeddings_onnx)")
}
