package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestShaderExtractor_GLSL(t *testing.T) {
	src := []byte(`#version 450
#include "common.glsl"

layout(location = 0) in vec3 aPosition;
uniform mat4 uModel;

struct Light {
    vec3 position;
    vec3 color;
};

vec3 lighting(Light l, vec3 n) {
    return l.color * max(dot(n, l.position), 0.0);
}

void main() {
    gl_Position = uModel * vec4(aPosition, 1.0);
}
`)
	e := NewShaderExtractor()
	require.Equal(t, "shader", e.Language())

	res, err := e.Extract("pass.vert", src)
	require.NoError(t, err)

	var gotStruct, gotLighting, gotMain, gotUniform, gotInclude bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Light":
			gotStruct = true
		case "lighting":
			gotLighting = true
		case "main":
			gotMain = true
		case "uModel":
			gotUniform = true
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::common.glsl" {
			gotInclude = true
		}
	}
	assert.True(t, gotStruct)
	assert.True(t, gotLighting)
	assert.True(t, gotMain)
	assert.True(t, gotUniform)
	assert.True(t, gotInclude)
}

func TestShaderExtractor_HLSL(t *testing.T) {
	src := []byte(`cbuffer PerFrame {
    float4x4 view;
};

float4 PSMain(float2 uv : TEXCOORD0) : SV_Target {
    return float4(uv, 0.0, 1.0);
}
`)
	res, err := NewShaderExtractor().Extract("pixel.hlsl", src)
	require.NoError(t, err)

	var gotCB, gotPS bool
	for _, n := range res.Nodes {
		if n.Name == "PerFrame" {
			gotCB = true
		}
		if n.Name == "PSMain" {
			gotPS = true
		}
	}
	assert.True(t, gotCB, "cbuffer PerFrame as type")
	assert.True(t, gotPS, "PSMain as function")
}

func TestShaderExtractor_EmptyInput(t *testing.T) {
	res, err := NewShaderExtractor().Extract("e.glsl", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
