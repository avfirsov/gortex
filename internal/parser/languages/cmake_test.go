package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCMakeExtractor_Basics(t *testing.T) {
	src := []byte(`cmake_minimum_required(VERSION 3.20)
project(demo)

include(utils.cmake)
add_subdirectory(src)

set(SOURCES main.cpp util.cpp)

function(greet NAME)
  message(STATUS "hello ${NAME}")
endfunction()

macro(log_msg MSG)
  message(STATUS ${MSG})
endmacro()

add_library(mylib STATIC ${SOURCES})
add_executable(app main.cpp)
`)
	e := NewCMakeExtractor()
	require.Equal(t, "cmake", e.Language())

	res, err := e.Extract("CMakeLists.txt", src)
	require.NoError(t, err)

	var gotGreet, gotLog, gotLib, gotExe, gotSources bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "greet":
			gotGreet = true
		case "log_msg":
			gotLog = true
		case "mylib":
			gotLib = true
		case "app":
			gotExe = true
		case "SOURCES":
			gotSources = true
		}
	}
	var gotInclude bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::utils.cmake" {
			gotInclude = true
		}
	}
	assert.True(t, gotGreet)
	assert.True(t, gotLog)
	assert.True(t, gotLib)
	assert.True(t, gotExe)
	assert.True(t, gotSources)
	assert.True(t, gotInclude)
}

func TestCMakeExtractor_EmptyInput(t *testing.T) {
	res, err := NewCMakeExtractor().Extract("CMakeLists.txt", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
