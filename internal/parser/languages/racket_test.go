package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestRacketExtractor_Defs(t *testing.T) {
	src := []byte(`#lang racket

(require "utils.rkt")
(require racket/list)

(define-struct point (x y))

(define pi 3.14159)

(define (area r)
  (* pi r r))

(define-syntax (my-if stx) #'stx)
`)
	e := NewRacketExtractor()
	require.Equal(t, "racket", e.Language())

	res, err := e.Extract("geo.rkt", src)
	require.NoError(t, err)

	var gotStruct, gotPi, gotArea, gotSyntax bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "point":
			gotStruct = true
		case "pi":
			gotPi = true
		case "area":
			gotArea = true
		case "my-if":
			gotSyntax = true
		}
	}
	var gotReqFile, gotReqMod bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::utils.rkt" {
			gotReqFile = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::racket/list" {
			gotReqMod = true
		}
	}
	assert.True(t, gotStruct)
	assert.True(t, gotPi)
	assert.True(t, gotArea)
	assert.True(t, gotSyntax)
	assert.True(t, gotReqFile)
	assert.True(t, gotReqMod)
}

func TestRacketExtractor_EmptyInput(t *testing.T) {
	res, err := NewRacketExtractor().Extract("e.rkt", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
