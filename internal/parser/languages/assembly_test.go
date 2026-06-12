package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestAssemblyExtractor_NASMStyle(t *testing.T) {
	src := []byte(`%include "macros.inc"
global _start
extern printf

section .text
_start:
    mov rax, 1
    call print_hello
    ret

print_hello:
    lea rdi, [msg]
    call printf
    ret

section .data
msg: db "hi", 0
`)
	e := NewAssemblyExtractor()
	require.Equal(t, "assembly", e.Language())

	res, err := e.Extract("hello.asm", src)
	require.NoError(t, err)

	funcs := 0
	imports := 0
	calls := 0
	var startNode *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindFunction {
			funcs++
			if n.Name == "_start" {
				startNode = n
			}
		}
	}
	for _, ed := range res.Edges {
		switch ed.Kind {
		case graph.EdgeImports:
			imports++
		case graph.EdgeCalls:
			calls++
		}
	}

	assert.GreaterOrEqual(t, funcs, 2, "_start + print_hello")
	assert.GreaterOrEqual(t, imports, 2, "%include + extern printf")
	assert.GreaterOrEqual(t, calls, 2)
	require.NotNil(t, startNode)
	assert.True(t, startNode.Meta["global"] == true, "_start should be marked global")
}

func TestAssemblyExtractor_GASStyle(t *testing.T) {
	src := []byte(`.include "defs.s"
.globl main
.extern puts

.text
main:
    pushq %rbp
    call greet
    xorl %eax, %eax
    popq %rbp
    ret

greet:
    leaq msg(%rip), %rdi
    call puts
    ret

.data
msg:
    .asciz "hi"
`)
	res, err := NewAssemblyExtractor().Extract("g.s", src)
	require.NoError(t, err)
	funcs, calls, imports := 0, 0, 0
	for _, n := range res.Nodes {
		if n.Kind == graph.KindFunction {
			funcs++
		}
	}
	for _, e := range res.Edges {
		switch e.Kind {
		case graph.EdgeCalls:
			calls++
		case graph.EdgeImports:
			imports++
		}
	}
	assert.GreaterOrEqual(t, funcs, 2)
	assert.GreaterOrEqual(t, calls, 2)
	assert.GreaterOrEqual(t, imports, 2)
}

func TestAssemblyExtractor_ARM(t *testing.T) {
	src := []byte(`.global main
main:
    bl helper
    bx lr

helper:
    bx lr
`)
	res, err := NewAssemblyExtractor().Extract("a.S", src)
	require.NoError(t, err)

	calls := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls {
			calls++
		}
	}
	assert.GreaterOrEqual(t, calls, 1, "bl helper must produce a call edge")
}

func TestAssemblyExtractor_EmptyInput(t *testing.T) {
	res, err := NewAssemblyExtractor().Extract("empty.asm", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
