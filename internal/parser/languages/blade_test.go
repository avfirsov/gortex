package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestBladeExtractor_Basics(t *testing.T) {
	src := []byte(`@extends('layouts.app')

@section('title', 'Home')

@section('content')
    <h1>Hello</h1>
    @include('partials.nav')
    @component('cards.user')
        Body
    @endcomponent
@endsection

@yield('sidebar')
`)
	e := NewBladeExtractor()
	require.Equal(t, "blade", e.Language())

	res, err := e.Extract("view.blade", src)
	require.NoError(t, err)

	var gotTitle, gotContent, gotInclude, gotComponent, gotYield bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "title":
			gotTitle = true
		case "content":
			gotContent = true
		case "partials.nav":
			gotInclude = true
		case "cards.user":
			gotComponent = true
		case "sidebar":
			gotYield = true
		}
	}
	assert.True(t, gotTitle)
	assert.True(t, gotContent)
	assert.True(t, gotInclude)
	assert.True(t, gotComponent)
	assert.True(t, gotYield)

	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::layouts.app" {
			gotImport = true
		}
	}
	assert.True(t, gotImport)
}

func TestBladeExtractor_EmptyInput(t *testing.T) {
	res, err := NewBladeExtractor().Extract("e.blade", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
