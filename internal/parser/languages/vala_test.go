package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestValaExtractor_Basics(t *testing.T) {
	src := []byte(`using Gtk;
using GLib;

namespace App {
    public interface IWidget {
        public abstract void render();
    }

    public class Window : Gtk.Window {
        public string title { get; set; }

        public void open() {
            print("opening\n");
        }
    }
}
`)
	e := NewValaExtractor()
	require.Equal(t, "vala", e.Language())

	res, err := e.Extract("window.vala", src)
	require.NoError(t, err)

	var gotWindow, gotWidget, gotOpen bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Window":
			gotWindow = true
		case "IWidget":
			gotWidget = true
		case "open":
			gotOpen = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Gtk" {
			gotImport = true
		}
	}
	assert.True(t, gotWindow)
	assert.True(t, gotWidget)
	assert.True(t, gotOpen)
	assert.True(t, gotImport)
}

func TestValaExtractor_EmptyInput(t *testing.T) {
	res, err := NewValaExtractor().Extract("e.vala", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
