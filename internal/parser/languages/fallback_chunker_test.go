package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

const pl1Source = `/* a tiny PL/I-ish program */
MAIN: PROC OPTIONS(MAIN);
  PUT LIST('hi');
END MAIN;

HELPER: PROC;
  RETURN;
END HELPER;
`

func pl1Spec() config.FallbackChunkerSpec {
	return config.FallbackChunkerSpec{
		Language:   "pl1",
		Extensions: []string{".pl1"},
		Patterns: []config.ChunkPattern{
			{Kind: "function", Regex: `(?m)^\s*([A-Z][A-Z0-9_]*):\s+PROC`, NameGroup: 1},
		},
	}
}

func TestRegisterFallbackChunkers_RegistersAndExtracts(t *testing.T) {
	reg := parser.NewRegistry()
	RegisterFallbackChunkers(reg, []config.FallbackChunkerSpec{pl1Spec()}, nil)

	ext, ok := reg.GetByLanguage("pl1")
	require.True(t, ok)
	assert.Equal(t, "pl1", ext.Language())
	assert.Equal(t, []string{".pl1"}, ext.Extensions())

	res, err := ext.Extract("prog.pl1", []byte(pl1Source))
	require.NoError(t, err)
	require.NotNil(t, res)

	// File node first, then MAIN + HELPER function nodes.
	require.Len(t, res.Nodes, 3)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
	assert.Equal(t, "prog.pl1", res.Nodes[0].ID)

	main := res.Nodes[1]
	assert.Equal(t, "prog.pl1::MAIN", main.ID)
	assert.Equal(t, graph.KindFunction, main.Kind)
	assert.Equal(t, "MAIN", main.Name)
	assert.Equal(t, 2, main.StartLine) // line of `MAIN: PROC`

	helper := res.Nodes[2]
	assert.Equal(t, "prog.pl1::HELPER", helper.ID)
	assert.Equal(t, "HELPER", helper.Name)
	assert.Equal(t, 6, helper.StartLine) // line of `HELPER: PROC`

	// One EdgeDefines per function, from the file node.
	require.Len(t, res.Edges, 2)
	for _, e := range res.Edges {
		assert.Equal(t, "prog.pl1", e.From)
		assert.Equal(t, graph.EdgeDefines, e.Kind)
	}
	assert.Equal(t, "prog.pl1::MAIN", res.Edges[0].To)
	assert.Equal(t, "prog.pl1::HELPER", res.Edges[1].To)
}

func TestRegexChunker_DefaultsNameGroupToOne(t *testing.T) {
	spec := pl1Spec()
	spec.Patterns[0].NameGroup = 0 // unset -> defaults to group 1
	reg := parser.NewRegistry()
	RegisterFallbackChunkers(reg, []config.FallbackChunkerSpec{spec}, nil)

	ext, ok := reg.GetByLanguage("pl1")
	require.True(t, ok)
	res, err := ext.Extract("p.pl1", []byte(pl1Source))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 3)
	assert.Equal(t, "MAIN", res.Nodes[1].Name)
}

func TestRegexChunker_DedupesByID(t *testing.T) {
	// Two patterns matching the same name must yield only one node.
	spec := config.FallbackChunkerSpec{
		Language:   "pl1",
		Extensions: []string{".pl1"},
		Patterns: []config.ChunkPattern{
			{Kind: "function", Regex: `(?m)^\s*([A-Z][A-Z0-9_]*):\s+PROC`},
			{Kind: "type", Regex: `(?m)^\s*([A-Z][A-Z0-9_]*):\s+PROC`},
		},
	}
	reg := parser.NewRegistry()
	RegisterFallbackChunkers(reg, []config.FallbackChunkerSpec{spec}, nil)
	ext, _ := reg.GetByLanguage("pl1")
	res, err := ext.Extract("d.pl1", []byte(pl1Source))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 3) // file + MAIN + HELPER, no duplicates
}

func TestRegisterFallbackChunkers_SkipsInvalidAndCollisions(t *testing.T) {
	reg := parser.NewRegistry()
	reg.Register(NewGoExtractor()) // claims "go" and ".go"

	RegisterFallbackChunkers(reg, []config.FallbackChunkerSpec{
		{Language: "", Extensions: []string{".x"}, Patterns: []config.ChunkPattern{{Kind: "function", Regex: "(x)"}}},        // no language
		{Language: "x", Extensions: nil, Patterns: []config.ChunkPattern{{Kind: "function", Regex: "(x)"}}},                  // no extensions
		{Language: "x", Extensions: []string{".x"}, Patterns: nil},                                                           // no patterns
		{Language: "go", Extensions: []string{".golang"}, Patterns: []config.ChunkPattern{{Kind: "function", Regex: "(x)"}}}, // language collision
		{Language: "y", Extensions: []string{".go"}, Patterns: []config.ChunkPattern{{Kind: "function", Regex: "(x)"}}},      // extension collision
		{Language: "bad", Extensions: []string{".b"}, Patterns: []config.ChunkPattern{{Kind: "function", Regex: "([a-z"}}},   // bad regex
		{Language: "onlybadkind", Extensions: []string{".k"}, Patterns: []config.ChunkPattern{{Kind: "nope", Regex: "(x)"}}}, // only invalid kind
		pl1Spec(), // valid
	}, nil)

	_, hasPL1 := reg.GetByLanguage("pl1")
	assert.True(t, hasPL1)
	for _, lang := range []string{"x", "y", "bad", "onlybadkind"} {
		_, ok := reg.GetByLanguage(lang)
		assert.False(t, ok, lang)
	}
	// Built-in Go extractor untouched.
	e, ok := reg.GetByLanguage("go")
	require.True(t, ok)
	assert.Equal(t, "go", e.Language())
}

func TestRegexChunker_DropsInvalidKindKeepsValid(t *testing.T) {
	spec := config.FallbackChunkerSpec{
		Language:   "pl1",
		Extensions: []string{".pl1"},
		Patterns: []config.ChunkPattern{
			{Kind: "not_a_kind", Regex: `(?m)^\s*([A-Z][A-Z0-9_]*):\s+PROC`},
			{Kind: "function", Regex: `(?m)^\s*([A-Z][A-Z0-9_]*):\s+PROC`},
		},
	}
	reg := parser.NewRegistry()
	RegisterFallbackChunkers(reg, []config.FallbackChunkerSpec{spec}, nil)
	ext, ok := reg.GetByLanguage("pl1")
	require.True(t, ok) // the valid pattern survives, so the spec registers
	res, err := ext.Extract("k.pl1", []byte(pl1Source))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 3)
	assert.Equal(t, graph.KindFunction, res.Nodes[1].Kind)
}
