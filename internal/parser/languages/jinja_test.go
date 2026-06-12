package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestJinjaExtractor_Basics(t *testing.T) {
	src := []byte(`{% extends "base.html" %}
{% include "nav.html" %}
{% import "macros.html" as m %}
{% from "forms.html" import input %}

{% block content %}
  <h1>Home</h1>
{% endblock %}

{% macro button(label, kind='primary') %}
  <button class="{{ kind }}">{{ label }}</button>
{% endmacro %}
`)
	e := NewJinjaExtractor()
	require.Equal(t, "jinja", e.Language())

	res, err := e.Extract("page.j2", src)
	require.NoError(t, err)

	var gotBlock, gotMacro bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "content":
			gotBlock = true
		case "button":
			gotMacro = true
		}
	}
	assert.True(t, gotBlock)
	assert.True(t, gotMacro)

	var gotExtends, gotInclude, gotImport, gotFrom bool
	for _, ed := range res.Edges {
		if ed.Kind != graph.EdgeImports {
			continue
		}
		switch ed.To {
		case "unresolved::import::base.html":
			gotExtends = true
		case "unresolved::import::nav.html":
			gotInclude = true
		case "unresolved::import::macros.html":
			gotImport = true
		case "unresolved::import::forms.html":
			gotFrom = true
		}
	}
	assert.True(t, gotExtends)
	assert.True(t, gotInclude)
	assert.True(t, gotImport)
	assert.True(t, gotFrom)
}

func TestJinjaExtractor_EmptyInput(t *testing.T) {
	res, err := NewJinjaExtractor().Extract("e.j2", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
