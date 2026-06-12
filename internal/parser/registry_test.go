package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

// mockExtractor implements the Extractor interface for testing.
type mockExtractor struct {
	lang string
	exts []string
}

func (m *mockExtractor) Language() string     { return m.lang }
func (m *mockExtractor) Extensions() []string { return m.exts }
func (m *mockExtractor) Extract(filePath string, src []byte) (*ExtractionResult, error) {
	return &ExtractionResult{
		Nodes: []*graph.Node{{ID: filePath + "::mock", Name: "mock", Kind: graph.KindFunction}},
	}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockExtractor{lang: "go", exts: []string{".go"}})

	e, ok := r.GetByLanguage("go")
	assert.True(t, ok)
	assert.Equal(t, "go", e.Language())

	e, ok = r.GetByExtension(".go")
	assert.True(t, ok)
	assert.Equal(t, "go", e.Language())
}

func TestRegistry_DetectLanguage(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockExtractor{lang: "go", exts: []string{".go"}})
	r.Register(&mockExtractor{lang: "typescript", exts: []string{".ts", ".tsx"}})

	lang, ok := r.DetectLanguage("pkg/foo.go")
	assert.True(t, ok)
	assert.Equal(t, "go", lang)

	lang, ok = r.DetectLanguage("src/app.tsx")
	assert.True(t, ok)
	assert.Equal(t, "typescript", lang)
}

func TestRegistry_UnknownExtension(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockExtractor{lang: "go", exts: []string{".go"}})

	_, ok := r.GetByExtension(".txt")
	assert.False(t, ok)

	_, ok = r.DetectLanguage("readme.md")
	assert.False(t, ok)
}

func TestRegistry_SupportedLanguages(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockExtractor{lang: "go", exts: []string{".go"}})
	r.Register(&mockExtractor{lang: "python", exts: []string{".py"}})

	langs := r.SupportedLanguages()
	assert.Len(t, langs, 2)
	assert.Contains(t, langs, "go")
	assert.Contains(t, langs, "python")
}
