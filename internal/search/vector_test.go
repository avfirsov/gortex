package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVectorBackend_AddAndSearch(t *testing.T) {
	v := NewVector(4)

	v.Add("func-a", []float32{1, 0, 0, 0})
	v.Add("func-b", []float32{0, 1, 0, 0})
	v.Add("func-c", []float32{0.9, 0.1, 0, 0})

	assert.Equal(t, 3, v.Count())

	// Search for vector close to func-a.
	results := v.Search([]float32{1, 0, 0, 0}, 2)
	require.Len(t, results, 2)
	assert.Equal(t, "func-a", results[0], "nearest should be func-a (exact match)")
	assert.Equal(t, "func-c", results[1], "second should be func-c (closest)")
}

func TestVectorBackend_EmptySearch(t *testing.T) {
	v := NewVector(4)
	results := v.Search([]float32{1, 0, 0, 0}, 5)
	assert.Nil(t, results)
}

func TestVectorBackend_Dims(t *testing.T) {
	v := NewVector(384)
	assert.Equal(t, 384, v.Dims())
}
