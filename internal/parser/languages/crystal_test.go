package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCrystalExtractor_Module(t *testing.T) {
	src := []byte(`require "http/server"
require "./helpers"

module App
  class Server
    def initialize(@port : Int32)
    end

    def start
      listen(@port)
    end
  end

  def self.run
    Server.new(8080).start
  end
end
`)
	e := NewCrystalExtractor()
	require.Equal(t, "crystal", e.Language())

	res, err := e.Extract("app.cr", src)
	require.NoError(t, err)

	var gotMod, gotClass, gotInit, gotStart, gotRun bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "App":
			gotMod = true
		case "Server":
			gotClass = true
		case "initialize":
			gotInit = true
		case "start":
			gotStart = true
		case "run":
			gotRun = true
		}
	}
	var gotReq bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::http/server" {
			gotReq = true
		}
	}
	assert.True(t, gotMod)
	assert.True(t, gotClass)
	assert.True(t, gotInit)
	assert.True(t, gotStart)
	assert.True(t, gotRun)
	assert.True(t, gotReq)
}

func TestCrystalExtractor_EmptyInput(t *testing.T) {
	res, err := NewCrystalExtractor().Extract("e.cr", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
