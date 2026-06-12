package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestActionScriptExtractor_Basics(t *testing.T) {
	src := []byte(`package com.example.util {
    import flash.events.Event;
    import com.example.model.*;

    public class Greeter {
        public static const VERSION:String = "1.0";
        private var _name:String;

        public function Greeter(name:String) {
            _name = name;
        }

        public function greet():String {
            return "hi " + _name;
        }
    }

    public interface IThing {
        function ping():void;
    }
}
`)
	e := NewActionScriptExtractor()
	require.Equal(t, "actionscript", e.Language())

	res, err := e.Extract("Greeter.as", src)
	require.NoError(t, err)

	var gotGreeter, gotGreet, gotIThing bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Greeter":
			gotGreeter = true
		case "greet":
			gotGreet = true
		case "IThing":
			gotIThing = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::flash.events.Event" {
			gotImport = true
		}
	}
	assert.True(t, gotGreeter)
	assert.True(t, gotGreet)
	assert.True(t, gotIThing)
	assert.True(t, gotImport)
}

func TestActionScriptExtractor_EmptyInput(t *testing.T) {
	res, err := NewActionScriptExtractor().Extract("e.as", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
