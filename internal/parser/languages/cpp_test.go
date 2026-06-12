package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestCppExtractor_Function(t *testing.T) {
	src := []byte(`#include <iostream>

void greet(const std::string& name) {
    std::cout << "Hello " << name << std::endl;
}
`)
	e := NewCppExtractor()
	result, err := e.Extract("main.cpp", src)
	require.NoError(t, err)

	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	assert.GreaterOrEqual(t, len(funcs), 1)
	assert.Equal(t, "greet", funcs[0].Name)
}

func TestCppExtractor_Class(t *testing.T) {
	src := []byte(`class Point {
public:
    int x, y;

    Point(int x, int y) : x(x), y(y) {}

    int distance() {
        return x * x + y * y;
    }
};
`)
	e := NewCppExtractor()
	result, err := e.Extract("point.cpp", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	assert.GreaterOrEqual(t, len(types), 1)
	assert.Equal(t, "Point", types[0].Name)

	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	assert.GreaterOrEqual(t, len(methods), 1)

	// Check MemberOf edges point to class.
	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.GreaterOrEqual(t, len(memberEdges), 1)
	for _, edge := range memberEdges {
		assert.Equal(t, "point.cpp::Point", edge.To)
	}
}

func TestCppExtractor_Struct(t *testing.T) {
	src := []byte(`struct Vec3 {
    float x, y, z;
};
`)
	e := NewCppExtractor()
	result, err := e.Extract("vec.cpp", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Vec3", types[0].Name)
}

func TestCppExtractor_Include(t *testing.T) {
	src := []byte(`#include <iostream>
#include "mylib.h"
`)
	e := NewCppExtractor()
	result, err := e.Extract("main.cpp", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	assert.Len(t, imports, 2)
}

func TestCppExtractor_Namespace(t *testing.T) {
	src := []byte(`namespace math {
    int add(int a, int b) {
        return a + b;
    }
}
`)
	e := NewCppExtractor()
	result, err := e.Extract("math.cpp", src)
	require.NoError(t, err)

	pkgs := nodesOfKind(result.Nodes, graph.KindPackage)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "math", pkgs[0].Name)
}

func TestCppExtractor_Enum(t *testing.T) {
	src := []byte(`enum class Color {
    Red,
    Green,
    Blue
};
`)
	e := NewCppExtractor()
	result, err := e.Extract("color.cpp", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Color", types[0].Name)
}

func TestCppExtractor_Calls(t *testing.T) {
	src := []byte(`void greet() {}

void run() {
    greet();
}
`)
	e := NewCppExtractor()
	result, err := e.Extract("main.cpp", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	assert.GreaterOrEqual(t, len(calls), 1)
}

func TestCppExtractor_Extensions(t *testing.T) {
	e := NewCppExtractor()
	assert.Equal(t, "cpp", e.Language())
	exts := e.Extensions()
	assert.Contains(t, exts, ".cpp")
	assert.Contains(t, exts, ".cc")
	assert.Contains(t, exts, ".cxx")
	assert.Contains(t, exts, ".hpp")
	assert.NotContains(t, exts, ".h")
}
