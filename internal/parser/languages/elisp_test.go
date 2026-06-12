package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestEmacsLispExtractor_Defs(t *testing.T) {
	src := []byte(`;;; my-mode.el -- A sample mode

(require 'cl-lib)
(load "shared-utils")

(defvar my-mode-version "1.0")

(defun my-mode-greet (name)
  "Greet NAME."
  (message "hi %s" name)
  (my-mode-log name))

(defun my-mode-log (x)
  (message "log: %s" x))

(provide 'my-mode)
`)
	e := NewEmacsLispExtractor()
	require.Equal(t, "elisp", e.Language())

	res, err := e.Extract("my-mode.el", src)
	require.NoError(t, err)

	var gotGreet, gotLog, gotVar bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "my-mode-greet":
			gotGreet = true
		case "my-mode-log":
			gotLog = true
		case "my-mode-version":
			gotVar = true
		}
	}
	var gotRequire, gotLoad, gotCall bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::cl-lib" {
			gotRequire = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::shared-utils" {
			gotLoad = true
		}
		if ed.Kind == graph.EdgeCalls && ed.To == "unresolved::my-mode-log" {
			gotCall = true
		}
	}
	assert.True(t, gotGreet)
	assert.True(t, gotLog)
	assert.True(t, gotVar)
	assert.True(t, gotRequire)
	assert.True(t, gotLoad)
	assert.True(t, gotCall)
}

func TestEmacsLispExtractor_EmptyInput(t *testing.T) {
	res, err := NewEmacsLispExtractor().Extract("e.el", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
