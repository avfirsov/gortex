//go:build embeddings_onnx

package embedding

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestONNXProvider_Embed(t *testing.T) {
	p, err := newONNXProvider()
	if err != nil {
		t.Skipf("ONNX provider not available: %v", err)
	}
	defer func() { _ = p.Close() }()

	assert.Equal(t, onnxDims, p.Dimensions())

	ctx := context.Background()

	v1, err := p.Embed(ctx, "function ValidateToken internal/auth/service.go")
	require.NoError(t, err)
	assert.Len(t, v1, onnxDims)

	v2, err := p.Embed(ctx, "function CheckAuthentication internal/auth/checker.go")
	require.NoError(t, err)

	v3, err := p.Embed(ctx, "function ParseJSON internal/parser/json.go")
	require.NoError(t, err)

	// Auth-related functions should be more similar to each other than to parsing.
	sim12 := cosine(v1, v2)
	sim13 := cosine(v1, v3)

	t.Logf("ValidateToken vs CheckAuth: %.4f", sim12)
	t.Logf("ValidateToken vs ParseJSON: %.4f", sim13)

	assert.Greater(t, sim12, sim13, "auth functions should be more similar to each other than to parser")
}

func TestONNXProvider_EmbedBatch(t *testing.T) {
	p, err := newONNXProvider()
	if err != nil {
		t.Skipf("ONNX provider not available: %v", err)
	}
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	vecs, err := p.EmbedBatch(ctx, []string{
		"function Foo internal/a.go",
		"method Bar internal/b.go",
	})
	require.NoError(t, err)
	assert.Len(t, vecs, 2)
	assert.Len(t, vecs[0], onnxDims)
	assert.Len(t, vecs[1], onnxDims)
}

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
