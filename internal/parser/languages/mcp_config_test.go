package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// hasMCPEdge reports whether an edge of the given kind from→to exists.
// (nodeByID is shared with other tests in this package.)
func hasMCPEdge(edges []*graph.Edge, from, to string, kind graph.EdgeKind) bool {
	for _, e := range edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

func TestMCPConfigExtractor_NpmAndPypiServers(t *testing.T) {
	src := []byte(`{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_TOKEN": "x"}
    },
    "fetch": {
      "command": "uvx",
      "args": ["mcp-server-fetch"]
    }
  }
}`)
	const fp = ".mcp.json"
	e := NewMCPConfigExtractor()
	result, err := e.Extract(fp, src)
	require.NoError(t, err)

	// File node.
	files := nodesOfKind(result.Nodes, graph.KindFile)
	require.Len(t, files, 1)
	assert.Equal(t, fp, files[0].ID)

	// Both servers as KindResource.
	githubServer := nodeByID(result.Nodes, "mcp::server::"+fp+"::github")
	require.NotNil(t, githubServer, "github server node should exist")
	assert.Equal(t, graph.KindResource, githubServer.Kind)
	assert.Equal(t, "github", githubServer.Name)
	assert.Equal(t, "mcp_server", githubServer.Meta["resource_type"])
	assert.Equal(t, "npx", githubServer.Meta["command"])
	assert.Equal(t, "stdio", githubServer.Meta["transport"])
	assert.Equal(t, ".mcp.json", githubServer.Meta["source"])
	assert.True(t, hasMCPEdge(result.Edges, fp, githubServer.ID, graph.EdgeDefines))

	fetchServer := nodeByID(result.Nodes, "mcp::server::"+fp+"::fetch")
	require.NotNil(t, fetchServer, "fetch server node should exist")
	assert.Equal(t, graph.KindResource, fetchServer.Kind)

	// npm module for the github server.
	const npmID = "module::npm:@modelcontextprotocol/server-github"
	npmMod := nodeByID(result.Nodes, npmID)
	require.NotNil(t, npmMod, "npm module node should exist with exact ID")
	assert.Equal(t, graph.KindModule, npmMod.Kind)
	assert.Equal(t, "npm", npmMod.Meta["ecosystem"])
	assert.True(t, hasMCPEdge(result.Edges, githubServer.ID, npmID, graph.EdgeDependsOnModule),
		"github server should depend on the npm module")

	// pypi module for the fetch server.
	const pypiID = "module::pypi:mcp-server-fetch"
	pypiMod := nodeByID(result.Nodes, pypiID)
	require.NotNil(t, pypiMod, "pypi module node should exist with exact ID")
	assert.Equal(t, graph.KindModule, pypiMod.Kind)
	assert.Equal(t, "pypi", pypiMod.Meta["ecosystem"])
	assert.True(t, hasMCPEdge(result.Edges, fetchServer.ID, pypiID, graph.EdgeDependsOnModule),
		"fetch server should depend on the pypi module")

	// Env config key — ID must be exactly cfg::env::GITHUB_TOKEN so it
	// shares identity with os.Getenv("GITHUB_TOKEN") code nodes.
	const envID = "cfg::env::GITHUB_TOKEN"
	envKey := nodeByID(result.Nodes, envID)
	require.NotNil(t, envKey, "env config key node should exist with exact ID cfg::env::GITHUB_TOKEN")
	assert.Equal(t, graph.KindConfigKey, envKey.Kind)
	assert.Equal(t, "GITHUB_TOKEN", envKey.Name)
	assert.Equal(t, "env", envKey.Meta["source"])
	assert.True(t, hasMCPEdge(result.Edges, githubServer.ID, envID, graph.EdgeUsesEnv),
		"github server should use_env GITHUB_TOKEN")
}

func TestMCPConfigExtractor_ClaudeDesktopShape(t *testing.T) {
	// claude_desktop_config.json uses the same mcpServers shape, with a
	// scoped+versioned npm spec and a ${VAR} arg interpolation.
	src := []byte(`{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem@1.2.3", "${HOME_DIR}"],
      "env": {"FS_ROOT": "/data"}
    }
  }
}`)
	const fp = "claude_desktop_config.json"
	e := NewMCPConfigExtractor()
	result, err := e.Extract(fp, src)
	require.NoError(t, err)

	server := nodeByID(result.Nodes, "mcp::server::"+fp+"::filesystem")
	require.NotNil(t, server)
	assert.Equal(t, "claude_desktop_config.json", server.Meta["source"])

	// Scoped + versioned npm spec splits into name + version.
	const npmID = "module::npm:@modelcontextprotocol/server-filesystem@1.2.3"
	npmMod := nodeByID(result.Nodes, npmID)
	require.NotNil(t, npmMod, "versioned scoped npm module node should exist")
	assert.Equal(t, "@modelcontextprotocol/server-filesystem", npmMod.Name)
	assert.Equal(t, "1.2.3", npmMod.Meta["version"])
	assert.True(t, hasMCPEdge(result.Edges, server.ID, npmID, graph.EdgeDependsOnModule))

	// Declared env var.
	assert.True(t, hasMCPEdge(result.Edges, server.ID, "cfg::env::FS_ROOT", graph.EdgeUsesEnv))
	// ${HOME_DIR} arg interpolation.
	homeKey := nodeByID(result.Nodes, "cfg::env::HOME_DIR")
	require.NotNil(t, homeKey, "interpolated ${HOME_DIR} should produce a config key")
	assert.True(t, hasMCPEdge(result.Edges, server.ID, "cfg::env::HOME_DIR", graph.EdgeUsesEnv))
}

func TestMCPConfigExtractor_DockerServer(t *testing.T) {
	src := []byte(`{
  "mcpServers": {
    "gitlab": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "GITLAB_TOKEN", "mcp/gitlab:1.5"],
      "env": {"GITLAB_TOKEN": "secret"}
    }
  }
}`)
	const fp = ".mcp.json"
	e := NewMCPConfigExtractor()
	result, err := e.Extract(fp, src)
	require.NoError(t, err)

	server := nodeByID(result.Nodes, "mcp::server::"+fp+"::gitlab")
	require.NotNil(t, server)

	// Image node — the flag-value (-e GITLAB_TOKEN) must not be mistaken
	// for the image; the image is mcp/gitlab:1.5.
	const imgID = "image::mcp/gitlab:1.5"
	img := nodeByID(result.Nodes, imgID)
	require.NotNil(t, img, "docker image node should exist with exact ID image::mcp/gitlab:1.5")
	assert.Equal(t, graph.KindImage, img.Kind)
	assert.Equal(t, "1.5", img.Meta["tag"])
	assert.True(t, hasMCPEdge(result.Edges, server.ID, imgID, graph.EdgeDependsOn),
		"docker server should depend_on the image")

	// No module node should be emitted for a docker command.
	assert.Empty(t, nodesOfKind(result.Nodes, graph.KindModule))

	// Env var still wired.
	assert.True(t, hasMCPEdge(result.Edges, server.ID, "cfg::env::GITLAB_TOKEN", graph.EdgeUsesEnv))
}

func TestMCPConfigExtractor_VSCodeServersKey(t *testing.T) {
	// VS Code uses a top-level "servers" key and a remote http transport.
	src := []byte(`{
  "servers": {
    "remote": {
      "type": "http",
      "url": "https://mcp.example.com/sse"
    }
  }
}`)
	const fp = "mcp.json"
	e := NewMCPConfigExtractor()
	result, err := e.Extract(fp, src)
	require.NoError(t, err)

	server := nodeByID(result.Nodes, "mcp::server::"+fp+"::remote")
	require.NotNil(t, server, "servers-key layout should be accepted")
	assert.Equal(t, "http", server.Meta["transport"])
}

func TestMCPConfigExtractor_Extensions(t *testing.T) {
	e := NewMCPConfigExtractor()
	exts := e.Extensions()
	assert.Contains(t, exts, ".mcp.json")
	assert.Contains(t, exts, "mcp.json")
	assert.Contains(t, exts, "claude_desktop_config.json")
	// Critically: must NOT claim the bare .json extension, or it would
	// steal package.json / tsconfig.json from the generic JSON extractor.
	assert.NotContains(t, exts, ".json")
}
