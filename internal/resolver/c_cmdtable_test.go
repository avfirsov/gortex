package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// loadCSources extracts each C-family fixture with the C extractor and loads
// its nodes/edges into a fresh graph — the faithful extract→resolve harness for
// generated-table reference recovery.
func loadCSources(t *testing.T, files map[string]string) graph.Store {
	t.Helper()
	g := graph.New()
	c := languages.NewCExtractor()
	for path, src := range files {
		r, err := c.Extract(path, []byte(src))
		if err != nil {
			t.Fatalf("extract %s: %v", path, err)
		}
		for _, n := range r.Nodes {
			g.AddNode(n)
		}
		for _, e := range r.Edges {
			g.AddEdge(e)
		}
	}
	return g
}

func refEdgeAt(g graph.Store, from string, line int) *graph.Edge {
	for _, e := range g.GetOutEdges(from) {
		if e.Kind == graph.EdgeReferences && e.Line == line && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == fnValueRegistrationVia {
				return e
			}
		}
	}
	return nil
}

// TestCCommandTableReferences pins the redis command-table shape: a generated
// `.def` fragment holding `MAKE_CMD(..., fooCommand, ...)` rows produces a usage
// edge from the fragment to the command function defined in another translation
// unit. Without it, find_usages(fooCommand) returns zero and mislabels the
// function as dead.
func TestCCommandTableReferences(t *testing.T) {
	g := loadCSources(t, map[string]string{
		"commands.def": "" +
			"struct redisCommand redisCommandTable[] = {\n" + // line 1
			"{MAKE_CMD(\"get\", getCommand, 2)},\n" + //         line 2
			"{MAKE_CMD(\"strlen\", strlenCommand, 2)},\n" + //   line 3
			"};\n",
		"t_string.c": "" +
			"robj *getCommand(client *c) { return lookupKey(c); }\n" +
			"void strlenCommand(client *c) { addReplyLongLong(c, 0); }\n",
	})

	require.Equal(t, "t_string.c::getCommand", resolveUniqueFnValue(g, "getCommand"),
		"the pointer-return command function must be a real node")

	ResolveFnValueCallbacks(g)

	get := refEdgeAt(g, "commands.def", 2)
	require.NotNil(t, get, "MAKE_CMD row must reference getCommand")
	assert.Equal(t, "t_string.c::getCommand", get.To)
	assert.Equal(t, "getCommand", get.Meta["fn_value_name"])

	strlen := refEdgeAt(g, "commands.def", 3)
	require.NotNil(t, strlen, "MAKE_CMD row must reference strlenCommand")
	assert.Equal(t, "t_string.c::strlenCommand", strlen.To)
}

// TestCCommandTableDesignatedInitializer covers the designated-initializer table
// form `{ .proc = fooCommand }`, not just the macro-call form.
func TestCCommandTableDesignatedInitializer(t *testing.T) {
	g := loadCSources(t, map[string]string{
		"table.c": "" +
			"struct cmd table[] = {\n" +
			"{ .name = \"ping\", .proc = pingCommand },\n" + // line 2
			"};\n",
		"server.c": "void pingCommand(client *c) { addReply(c); }\n",
	})

	ResolveFnValueCallbacks(g)

	e := refEdgeAt(g, "table.c", 2)
	require.NotNil(t, e, "designated .proc initializer must reference pingCommand")
	assert.Equal(t, "server.c::pingCommand", e.To)
}
