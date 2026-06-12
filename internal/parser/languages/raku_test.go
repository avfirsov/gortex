package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestRakuExtractor_ClassAndSubs(t *testing.T) {
	src := []byte(`use v6;
unit class Greeter;

has Str $.name;

method greet() {
    say "hi $.name";
}

sub shout($msg) {
    say $msg.uc;
}

multi sub handle(Int $x) { say "int" }
multi sub handle(Str $x) { say "str" }
`)
	e := NewRakuExtractor()
	require.Equal(t, "raku", e.Language())

	res, err := e.Extract("Greeter.rakumod", src)
	require.NoError(t, err)

	var gotClass, gotGreet, gotShout, gotHandle bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Greeter":
			gotClass = true
		case "greet":
			gotGreet = true
		case "shout":
			gotShout = true
		case "handle":
			gotHandle = true
		}
	}
	assert.True(t, gotClass)
	assert.True(t, gotGreet)
	assert.True(t, gotShout)
	assert.True(t, gotHandle)
}

func TestRakuExtractor_RoleAndUse(t *testing.T) {
	src := []byte(`use Test;
role Printable {
    method print-me() { ... }
}
`)
	res, err := NewRakuExtractor().Extract("p.raku", src)
	require.NoError(t, err)

	var gotRole bool
	for _, n := range res.Nodes {
		if n.Name == "Printable" && n.Kind == graph.KindInterface {
			gotRole = true
		}
	}
	var gotUse bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Test" {
			gotUse = true
		}
	}
	assert.True(t, gotRole)
	assert.True(t, gotUse)
}

func TestRakuExtractor_EmptyInput(t *testing.T) {
	res, err := NewRakuExtractor().Extract("e.raku", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
