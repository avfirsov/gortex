package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestNixExtractor_FlakeLike(t *testing.T) {
	src := []byte(`{ pkgs ? import <nixpkgs> {} }:

let
  name = "gortex";
  buildInputs = [ pkgs.go pkgs.git ];

  mkPackage = { src, version }:
    pkgs.stdenv.mkDerivation {
      pname = name;
      inherit version src;
      buildPhase = "go build ./...";
    };
in
  mkPackage {
    src = ./.;
    version = "0.9.1";
  }
`)
	e := NewNixExtractor()
	require.Equal(t, "nix", e.Language())

	res, err := e.Extract("default.nix", src)
	require.NoError(t, err)

	vars, funcs, imports := 0, 0, 0
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindVariable:
			vars++
		case graph.KindFunction:
			funcs++
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}

	assert.GreaterOrEqual(t, vars, 2, "name and buildInputs should be variables")
	assert.GreaterOrEqual(t, funcs, 1, "mkPackage is a lambda-bound function")
	assert.GreaterOrEqual(t, imports, 1, "import <nixpkgs> should record an import edge")
}

func TestNixExtractor_WithAndInherit(t *testing.T) {
	src := []byte(`{ pkgs, ... }:
with pkgs;
let
  inherit (pkgs.lib) mkDefault mkForce;
  tools = [ git go cmake ];
in
  tools
`)
	res, err := NewNixExtractor().Extract("w.nix", src)
	require.NoError(t, err)

	var gotWith, gotInherit bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::pkgs" {
			gotWith = true
		}
		if ed.Kind == graph.EdgeReferences && ed.To == "unresolved::pkgs.lib" {
			gotInherit = true
		}
	}
	assert.True(t, gotWith, "with pkgs; should create an import-style edge")
	assert.True(t, gotInherit, "inherit (pkgs.lib) should create a references edge")
}

func TestNixExtractor_EmptyInput(t *testing.T) {
	res, err := NewNixExtractor().Extract("empty.nix", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
