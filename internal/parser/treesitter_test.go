package parser

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/parser/tsitter/golang"
	"github.com/zzet/gortex/internal/parser/tsitter/python"
)

func TestParseFile_Go(t *testing.T) {
	src := []byte(`package main

func Hello() {
	fmt.Println("hello")
}
`)
	tree, err := ParseFile(src, golang.GetLanguage())
	require.NoError(t, err)
	defer tree.Close()

	root := tree.RootNode()
	assert.Equal(t, "source_file", root.Type())
	assert.True(t, root.ChildCount() > 0)
}

func TestRunQuery_GoFunction(t *testing.T) {
	src := []byte(`package main

func Hello() {}
func World(x int) string { return "" }
`)
	tree, err := ParseFile(src, golang.GetLanguage())
	require.NoError(t, err)
	defer tree.Close()

	pattern := `(function_declaration name: (identifier) @func.name) @func.def`
	results, err := RunQuery(pattern, golang.GetLanguage(), tree.RootNode(), src)
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "Hello", results[0].Captures["func.name"].Text)
	assert.Equal(t, "World", results[1].Captures["func.name"].Text)
}

func TestRunQuery_NoMatches(t *testing.T) {
	src := []byte(`package main

var x = 42
`)
	tree, err := ParseFile(src, golang.GetLanguage())
	require.NoError(t, err)
	defer tree.Close()

	pattern := `(function_declaration name: (identifier) @func.name) @func.def`
	results, err := RunQuery(pattern, golang.GetLanguage(), tree.RootNode(), src)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestParseFile_InvalidSource(t *testing.T) {
	// tree-sitter is error-tolerant; it returns a tree even for garbage input.
	src := []byte(`{{{{{not valid go at all!!!!`)
	tree, err := ParseFile(src, golang.GetLanguage())
	require.NoError(t, err)
	defer tree.Close()

	root := tree.RootNode()
	assert.NotNil(t, root) // just verify it doesn't crash
}

// TestParseFile_PoolGrammarSwitch verifies that a pooled parser, after
// parsing one grammar, correctly re-binds to a different grammar on the
// next checkout — the exact failure mode the legacy smacker pool hit.
func TestParseFile_PoolGrammarSwitch(t *testing.T) {
	goSrc := []byte("package main\n\nfunc Hello() {}\n")
	pySrc := []byte("def hello():\n    return 1\n")

	for i := 0; i < 20; i++ {
		goTree, err := ParseFile(goSrc, golang.GetLanguage())
		require.NoError(t, err)
		assert.Equal(t, "source_file", goTree.RootNode().Type())
		assert.False(t, goTree.RootNode().HasError())
		goTree.Close()

		pyTree, err := ParseFile(pySrc, python.GetLanguage())
		require.NoError(t, err)
		assert.Equal(t, "module", pyTree.RootNode().Type())
		assert.False(t, pyTree.RootNode().HasError())
		pyTree.Close()
	}
}

// TestParseFile_PoolConcurrent exercises the parser pool from many
// goroutines at once. Run under -race, it proves checkout/return is
// data-race free and that concurrent callers never share a live parser.
func TestParseFile_PoolConcurrent(t *testing.T) {
	srcs := [][]byte{
		[]byte("package a\n\nfunc A() int { return 1 }\n"),
		[]byte("package b\n\nfunc B() string { return \"\" }\n"),
		[]byte("package c\n\ntype C struct{ X int }\n"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			src := srcs[n%len(srcs)]
			tree, err := ParseFile(src, golang.GetLanguage())
			if err != nil {
				t.Errorf("concurrent parse %d: %v", n, err)
				return
			}
			defer tree.Close()
			if tree.RootNode().Type() != "source_file" {
				t.Errorf("concurrent parse %d: unexpected root %q", n, tree.RootNode().Type())
			}
		}(i)
	}
	wg.Wait()
}
