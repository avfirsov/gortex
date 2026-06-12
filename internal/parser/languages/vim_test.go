package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestVimExtractor_FunctionsAndLet(t *testing.T) {
	src := []byte(`source ~/.vim/plugins.vim
let g:plugin_name = 'gortex'

function! s:setup()
  let l:flags = 'rnw'
endfunction

function g:MyPlugin#init()
  call s:setup()
endfunction

command! -nargs=1 Greet echo "hi"
`)
	e := NewVimExtractor()
	require.Equal(t, "vim", e.Language())

	res, err := e.Extract("plugin.vim", src)
	require.NoError(t, err)

	var gotSetup, gotInit, gotVar, gotCmd bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "setup":
			gotSetup = true
		case "MyPlugin#init":
			gotInit = true
		case "plugin_name":
			gotVar = true
		case "Greet":
			gotCmd = true
		}
	}
	var gotSource bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::~/.vim/plugins.vim" {
			gotSource = true
		}
	}
	assert.True(t, gotSetup)
	assert.True(t, gotInit)
	assert.True(t, gotVar)
	assert.True(t, gotCmd)
	assert.True(t, gotSource)
}

func TestVimExtractor_EmptyInput(t *testing.T) {
	res, err := NewVimExtractor().Extract("e.vim", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
