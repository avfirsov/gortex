package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeETag_Deterministic(t *testing.T) {
	data := map[string]any{
		"id":     "server.go::Server",
		"source": "func (s *Server) Foo() {}",
	}

	etag1 := computeETag(data)
	etag2 := computeETag(data)

	assert.Equal(t, etag1, etag2)
	assert.Len(t, etag1, 16) // 8 bytes hex-encoded
}

func TestComputeETag_DifferentContent(t *testing.T) {
	data1 := map[string]any{"source": "func Foo() {}"}
	data2 := map[string]any{"source": "func Bar() {}"}

	etag1 := computeETag(data1)
	etag2 := computeETag(data2)

	assert.NotEqual(t, etag1, etag2)
}

func TestNotModifiedResult(t *testing.T) {
	result := notModifiedResult("abc123")
	assert.False(t, result.IsError)
	assert.NotEmpty(t, result.Content)
}

func TestWithETag(t *testing.T) {
	data := map[string]any{
		"id":     "foo.go::Bar",
		"source": "func Bar() {}",
	}

	result, err := withETag(data)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// The etag should now be in the data map.
	etag, ok := data["etag"].(string)
	assert.True(t, ok)
	assert.Len(t, etag, 16)
}
