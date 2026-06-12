package indexer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMinifiedArtifactReason(t *testing.T) {
	// A minified bundle — the whole file on one very long line.
	minified := strings.Repeat("function a(b){return b+1};", 200)
	assert.Equal(t, "minified", minifiedArtifactReason("javascript", []byte(minified)))

	// A Source Map v3 document — classified regardless of language.
	sourcemap := `{"version":3,"file":"app.js","sources":["app.ts"],"names":[],"mappings":"` +
		strings.Repeat("AAAA;", 500) + `"}`
	assert.Equal(t, "sourcemap", minifiedArtifactReason("json", []byte(sourcemap)))

	// Generated code with a sourceMappingURL link but normal line
	// lengths — a (possibly pretty-printed) bundle.
	bundled := strings.Repeat("const value = helper(x, y);\n", 90) +
		"//# sourceMappingURL=app.js.map\n"
	assert.Equal(t, "bundled", minifiedArtifactReason("javascript", []byte(bundled)))

	// Genuine, multi-line source — not an artifact.
	normal := strings.Repeat("const result = computeValue(alpha, beta);\n", 80)
	assert.Equal(t, "", minifiedArtifactReason("javascript", []byte(normal)))

	// A small file is never classified, however dense.
	assert.Equal(t, "", minifiedArtifactReason("javascript", []byte("a=1;a=1;a=1;")))

	// The line-length heuristic is gated to JS/TS/CSS — a long-lined
	// file in another language is left alone.
	longGo := "package main\n" + strings.Repeat("x", 4000)
	assert.Equal(t, "", minifiedArtifactReason("go", []byte(longGo)))

	// Ordinary Go source is never an artifact.
	goSrc := strings.Repeat("func handle(req Request) error { return nil }\n", 60)
	assert.Equal(t, "", minifiedArtifactReason("go", []byte(goSrc)))
}

func TestLooksLikeSourceMap(t *testing.T) {
	assert.True(t, looksLikeSourceMap([]byte(`{"version":3,"sources":["a.ts"],"mappings":"AAAA"}`)))
	assert.True(t, looksLikeSourceMap([]byte(`  {"version": 3, "mappings": "AAAA"}`)))
	assert.False(t, looksLikeSourceMap([]byte(`{"version":2,"sources":["a.ts"]}`)))
	assert.False(t, looksLikeSourceMap([]byte(`function f(){}`)))
	assert.False(t, looksLikeSourceMap([]byte(`{"name":"pkg","version":"3.0.0"}`)))
}
