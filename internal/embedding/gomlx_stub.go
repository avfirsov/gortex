//go:build !embeddings_gomlx

package embedding

import "errors"

func newGoMLXProvider() (Provider, error) {
	return nil, errors.New("GoMLX provider not compiled in (build with -tags embeddings_gomlx)")
}
