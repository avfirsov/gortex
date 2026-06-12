package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestGDScriptExtractor_PlayerScript(t *testing.T) {
	src := []byte(`class_name Player
extends CharacterBody2D

const SPEED := 200.0
var health: int = 100

signal died(reason: String)

enum State { IDLE, RUNNING, JUMPING }

class InnerHelper:
    var count := 0
    func bump():
        count += 1

func _ready() -> void:
    print("ready")
    _spawn_weapon()

func _spawn_weapon() -> void:
    var w := preload("res://weapons/sword.tres")
    add_child(w)
`)
	e := NewGDScriptExtractor()
	require.Equal(t, "gdscript", e.Language())
	require.Equal(t, []string{".gd"}, e.Extensions())

	res, err := e.Extract("player.gd", src)
	require.NoError(t, err)

	types, funcs, vars, signals, imports := 0, 0, 0, 0, 0
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindType:
			types++
		case graph.KindFunction:
			funcs++
		case graph.KindVariable:
			vars++
		case graph.KindMethod:
			if n.Meta != nil && n.Meta["gd_kind"] == "signal" {
				signals++
			}
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}

	assert.GreaterOrEqual(t, types, 3, "Player + InnerHelper + State enum")
	assert.GreaterOrEqual(t, funcs, 3, "bump + _ready + _spawn_weapon")
	assert.GreaterOrEqual(t, vars, 2, "SPEED const + health")
	assert.GreaterOrEqual(t, signals, 1, "died signal")
	assert.GreaterOrEqual(t, imports, 2, "extends CharacterBody2D + preload")
}

func TestGDScriptExtractor_CallEdges(t *testing.T) {
	src := []byte(`func main():
    hello()
    world()

func hello():
    pass
`)
	res, err := NewGDScriptExtractor().Extract("x.gd", src)
	require.NoError(t, err)
	calls := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls {
			calls++
		}
	}
	assert.GreaterOrEqual(t, calls, 2)
}

func TestGDScriptExtractor_EmptyInput(t *testing.T) {
	res, err := NewGDScriptExtractor().Extract("empty.gd", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
