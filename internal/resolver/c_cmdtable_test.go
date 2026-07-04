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

// tableRefTo reports whether targetID has an incoming bound command-table
// reference edge originating in fromFile.
func tableRefTo(g graph.Store, targetID, fromFile string) *graph.Edge {
	for _, e := range g.GetInEdges(targetID) {
		if e.From != fromFile || e.Kind != graph.EdgeReferences || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == fnValueRegistrationVia {
			return e
		}
	}
	return nil
}

// TestCDispatchTableInitializerListPositional covers the positional
// initializer-list dispatch table `{ "name", fnPtr, arity }` — the second-slot
// handler resolves to its cross-file definition, exactly like the macro form.
func TestCDispatchTableInitializerListPositional(t *testing.T) {
	g := loadCSources(t, map[string]string{
		"table.c": "" +
			"struct cmd table[] = {\n" + // line 1
			"{ \"ping\", pingCommand, 2 },\n" + // line 2
			"{ \"echo\", echoCommand, 2 },\n" + // line 3
			"};\n",
		"server.c": "" +
			"void pingCommand(client *c) { addReply(c); }\n" +
			"void echoCommand(client *c) { addReply(c); }\n",
	})

	ResolveFnValueCallbacks(g)

	ping := refEdgeAt(g, "table.c", 2)
	require.NotNil(t, ping, "positional dispatch-table row must reference pingCommand")
	assert.Equal(t, "server.c::pingCommand", ping.To)

	echo := refEdgeAt(g, "table.c", 3)
	require.NotNil(t, echo, "positional dispatch-table row must reference echoCommand")
	assert.Equal(t, "server.c::echoCommand", echo.To)
}

// TestCCommandTableNoiseProducesNoEdge is the strong precision pin: even when a
// repo genuinely defines functions whose names are ALL_CAPS or shorter than four
// characters, a command-table row naming them must NOT mint a reference. The
// capture guard drops them before the gate, so the coincidental function
// definitions can never become false usages — only the real mixed-case handler
// binds.
func TestCCommandTableNoiseProducesNoEdge(t *testing.T) {
	g := loadCSources(t, map[string]string{
		"commands.def": "" +
			"struct redisCommand t[] = {\n" + // line 1
			"{MAKE_CMD(\"get\", CMD_READONLY, run, getCommand)},\n" + // line 2
			"};\n",
		"t_string.c": "" +
			"robj *getCommand(client *c) { return 0; }\n" +
			// Decoys: an ALL_CAPS function name and a sub-4-char function name.
			// Both are real, uniquely-named functions the gate WOULD bind if a
			// candidate reached it — proving the guard, not the gate, is what
			// suppresses them.
			"void CMD_READONLY(void) {}\n" +
			"void run(void) {}\n",
	})

	ResolveFnValueCallbacks(g)

	require.NotNil(t, tableRefTo(g, "t_string.c::getCommand", "commands.def"),
		"the real mixed-case handler must be referenced by the table row")
	assert.Nil(t, tableRefTo(g, "t_string.c::CMD_READONLY", "commands.def"),
		"an ALL_CAPS function name must not be referenced from a table row")
	assert.Nil(t, tableRefTo(g, "t_string.c::run", "commands.def"),
		"a sub-4-char function name must not be referenced from a table row")
}

// TestCCommandTableEndToEndIncomingNotStub is the end-to-end pin: index a
// two-file fixture (the handler defined in defs.c, the row in a generated
// table.def), resolve, and assert the handler's incoming edges include the table
// row carrying the correct file:line and pointing at the real, non-stub function
// node — the exact shape find_usages(handler) walks.
func TestCCommandTableEndToEndIncomingNotStub(t *testing.T) {
	g := loadCSources(t, map[string]string{
		"defs.c": "void handleGet(client *c) { addReply(c); }\n",
		"table.def": "" +
			"struct redisCommand redisCommandTable[] = {\n" + // line 1
			"{MAKE_CMD(\"get\", \"Get the value\", 2, CMD_READONLY, handleGet, 1, 1)},\n" + // line 2
			"};\n",
	})

	ResolveFnValueCallbacks(g)

	const target = "defs.c::handleGet"
	node := g.GetNode(target)
	require.NotNil(t, node, "the handler must be a real node")
	assert.Equal(t, graph.KindFunction, node.Kind, "the handler is a function")
	assert.False(t, node.Stub, "the handler is real source, not a federation proxy")
	assert.False(t, graph.IsStub(target), "the handler id is not a stub id")

	ref := tableRefTo(g, target, "table.def")
	require.NotNil(t, ref, "the handler's incoming edges must include the .def table row")
	assert.Equal(t, "table.def", ref.FilePath, "the reference carries the .def file")
	assert.Equal(t, 2, ref.Line, "the reference carries the exact table-row line")
	assert.Equal(t, target, ref.To)
	assert.False(t, graph.IsUnresolvedTarget(ref.To), "the bound edge no longer points at an unresolved placeholder")
}
