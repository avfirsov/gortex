package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestGroovyExtractor_Basics(t *testing.T) {
	src := []byte(`package com.example.app

import groovy.transform.CompileStatic
import static java.lang.Math.*

class Greeter {
    String name

    def greet(String who) {
        return "hi ${who}"
    }

    static def version() {
        return "1.0"
    }
}

interface Runnable {
    def run()
}
`)
	e := NewGroovyExtractor()
	require.Equal(t, "groovy", e.Language())

	res, err := e.Extract("Greeter.groovy", src)
	require.NoError(t, err)

	var gotGreeter, gotGreet, gotRunnable bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Greeter":
			gotGreeter = true
		case "greet":
			gotGreet = true
		case "Runnable":
			gotRunnable = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::groovy.transform.CompileStatic" {
			gotImport = true
		}
	}
	assert.True(t, gotGreeter)
	assert.True(t, gotGreet)
	assert.True(t, gotRunnable)
	assert.True(t, gotImport)
}

func TestGroovyExtractor_EmptyInput(t *testing.T) {
	res, err := NewGroovyExtractor().Extract("e.groovy", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
