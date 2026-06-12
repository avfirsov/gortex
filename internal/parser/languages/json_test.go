package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestJSONExtractor_Basics(t *testing.T) {
	src := []byte(`{
  "name": "gortex",
  "version": "0.9.1",
  "scripts": {
    "build": "go build ./...",
    "test": "go test ./..."
  }
}
`)
	e := NewJSONExtractor()
	require.Equal(t, "json", e.Language())

	res, err := e.Extract("package.json", src)
	require.NoError(t, err)

	var gotName, gotVersion, gotScripts, gotBuildLeak bool
	for _, n := range res.Nodes {
		if n.Kind != graph.KindVariable {
			continue
		}
		switch n.Name {
		case "name":
			gotName = true
		case "version":
			gotVersion = true
		case "scripts":
			gotScripts = true
		case "build":
			// Nested keys should NOT be extracted.
			gotBuildLeak = true
		}
	}
	assert.True(t, gotName)
	assert.True(t, gotVersion)
	assert.True(t, gotScripts)
	assert.False(t, gotBuildLeak, "nested keys must not be extracted")
}

func TestJSONExtractor_EmptyInput(t *testing.T) {
	res, err := NewJSONExtractor().Extract("e.json", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
