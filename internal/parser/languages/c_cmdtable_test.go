package languages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cValueRefCandidates returns the function-as-value candidates the C extractor
// captured for src — the placeholder edges (via=callback_candidate) awaiting the
// resolver gate — as a name → source-line map. It is the capture-side view a
// generated command table exercises before any resolution runs.
func cValueRefCandidates(t *testing.T, path, src string) map[string]int {
	t.Helper()
	r, err := NewCExtractor().Extract(path, []byte(src))
	require.NoError(t, err)
	out := map[string]int{}
	for _, e := range r.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "callback_candidate" {
			continue
		}
		if name, _ := e.Meta["fn_value_name"].(string); name != "" {
			out[name] = e.Line
		}
	}
	return out
}

// TestCExtractor_CommandTableMacroCandidates pins the redis command-table shape:
// a `MAKE_CMD(..., handler, ...)` macro row in a generated `.def` fragment
// captures the handler as a function-value candidate, while the string summary,
// the ALL_CAPS flag macro, the short arity counts, and the MAKE_CMD callee
// itself do not become references.
func TestCExtractor_CommandTableMacroCandidates(t *testing.T) {
	src := "" +
		"struct redisCommand redisCommandTable[] = {\n" + // line 1
		"{MAKE_CMD(\"get\", \"Get the value\", -2, CMD_READONLY, getCommand, 1, 2)},\n" + // line 2
		"{MAKE_CMD(\"strlen\", \"String length\", 2, CMD_READONLY, strlenCommand, 1, 2)},\n" + // line 3
		"};\n"
	cands := cValueRefCandidates(t, "commands.def", src)

	assert.Equal(t, 2, cands["getCommand"], "handler in a macro arg list is captured")
	assert.Equal(t, 3, cands["strlenCommand"], "handler in a macro arg list is captured")

	assert.NotContains(t, cands, "CMD_READONLY", "an ALL_CAPS flag macro is not a function reference")
	assert.NotContains(t, cands, "MAKE_CMD", "the macro callee is a call, not a value reference")
	assert.NotContains(t, cands, "get", "a short string-literal table key is not an identifier reference")
	assert.NotContains(t, cands, "Get the value", "a string literal is never captured")
}

// TestCExtractor_DispatchTableInitListCandidates covers the classic positional
// initializer-list dispatch table `{ "name", fnPtr, arity }` — the handler in
// the second slot is captured as a function-value candidate.
func TestCExtractor_DispatchTableInitListCandidates(t *testing.T) {
	src := "" +
		"struct cmd table[] = {\n" + // line 1
		"{ \"ping\", pingCommand, 2 },\n" + // line 2
		"{ \"echo\", echoCommand, 2 },\n" + // line 3
		"};\n"
	cands := cValueRefCandidates(t, "table.c", src)

	assert.Equal(t, 2, cands["pingCommand"], "handler in an initializer-list slot is captured")
	assert.Equal(t, 3, cands["echoCommand"], "handler in an initializer-list slot is captured")
	assert.NotContains(t, cands, "ping", "a string key is not an identifier reference")
}

// TestCExtractor_CommandTableNoiseNotCaptured is the negative precision pin: the
// three noise classes the guard exists for — ALL_CAPS macros / enum constants,
// sub-4-character identifiers, and string literals — never become candidates,
// while the one mixed-case, long-enough handler in the same row does.
func TestCExtractor_CommandTableNoiseNotCaptured(t *testing.T) {
	src := "" +
		"struct redisCommand t[] = {\n" + // line 1
		"{MAKE_CMD(\"noise\", ARG_TYPE_KEY, CMD_WRITE, run, xy, realCommand)},\n" + // line 2
		"};\n"
	cands := cValueRefCandidates(t, "n.def", src)

	assert.Contains(t, cands, "realCommand", "the mixed-case, long-enough handler is captured")
	assert.NotContains(t, cands, "ARG_TYPE_KEY", "an ALL_CAPS enum constant is filtered")
	assert.NotContains(t, cands, "CMD_WRITE", "an ALL_CAPS flag macro is filtered")
	assert.NotContains(t, cands, "run", "a sub-4-character identifier is filtered")
	assert.NotContains(t, cands, "xy", "a two-character identifier is filtered")
}

// TestCExtractor_CommandTableErrorRecovery pins that a malformed generated
// fragment (a missing close paren plus a line of garbage — the shape a `.def`
// degrades to when its surrounding translation-unit context is absent) does not
// suppress extraction: the extractor recovers the handlers whose argument lists
// still parse rather than emitting nothing.
func TestCExtractor_CommandTableErrorRecovery(t *testing.T) {
	src := "" +
		"MAKE_CMD(\"get\", getCommand, 2)\n" + // line 1: clean
		"MAKE_CMD(\"strlen\", strlenCommand, 2\n" + // line 2: missing close paren
		"%%% garbage !!! {{{ nonsense\n" + // line 3: junk
		"MAKE_CMD(\"append\", appendCommand, 3)\n" // line 4

	r, err := NewCExtractor().Extract("broken.def", []byte(src))
	require.NoError(t, err, "extraction must not fail on an ERROR-recovered tree")

	cands := map[string]bool{}
	for _, e := range r.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "callback_candidate" {
			continue
		}
		if name, _ := e.Meta["fn_value_name"].(string); name != "" {
			cands[name] = true
		}
	}
	assert.True(t, cands["getCommand"], "the cleanly-parsed row before the error survives")
	assert.True(t, cands["strlenCommand"], "the handler still in an arg list after the missing paren survives recovery")
}

// TestCExtractor_LexerLookaheadFilteredAndDeduped pins the generated-lexer flood
// shape. An uninitialised local (`int32_t lookahead;`) compared on ==/!= across
// many lines is a local, never a function address, so it yields no candidate; a
// genuinely-undeclared free name compared the same way collapses to exactly one
// candidate regardless of how many lines mention it (the per-name dedup).
func TestCExtractor_LexerLookaheadFilteredAndDeduped(t *testing.T) {
	src := "" +
		"void lexScan(void) {\n" + // line 1
		"  int32_t lookahead;\n" + // line 2: uninitialised local
		"  if (lookahead == 65) return;\n" + // 3
		"  if (lookahead != 66) return;\n" + // 4
		"  if (lookahead == 67) return;\n" + // 5
		"  if (externScanHook == 68) return;\n" + // 6: undeclared free name
		"  if (externScanHook != 69) return;\n" + // 7
		"  if (externScanHook == 70) return;\n" + // 8
		"}\n"
	r, err := NewCExtractor().Extract("lexer.c", []byte(src))
	require.NoError(t, err)

	counts := map[string]int{}
	for _, e := range r.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "callback_candidate" {
			continue
		}
		if name, _ := e.Meta["fn_value_name"].(string); name != "" {
			counts[name]++
		}
	}
	assert.Zero(t, counts["lookahead"], "an uninitialised local compared on ==/!= is never a function-address candidate")
	assert.Equal(t, 1, counts["externScanHook"], "an undeclared free name on N comparison lines collapses to one candidate")
}

// TestCExtractor_EnumMembersNotCaptured pins the generated-parser enum flood: an
// in-file anonymous enum's members, referenced from a designated-initializer
// action table, are compile-time constants — never function addresses — so none
// becomes a candidate, while a genuine cross-TU handler in the same table does.
func TestCExtractor_EnumMembersNotCaptured(t *testing.T) {
	src := "" +
		"enum { sym_alpha, sym_beta, sym_gamma };\n" + // line 1
		"void *actionTable[] = {\n" + // line 2
		"  [0] = sym_alpha,\n" + // 3
		"  [1] = sym_beta,\n" + // 4
		"  [2] = sym_gamma,\n" + // 5
		"  [3] = reduceAction,\n" + // 6: a real cross-TU handler
		"};\n"
	cands := cValueRefCandidates(t, "parser.c", src)

	assert.NotContains(t, cands, "sym_alpha", "an in-file enum member is not a function reference")
	assert.NotContains(t, cands, "sym_beta", "an in-file enum member is not a function reference")
	assert.NotContains(t, cands, "sym_gamma", "an in-file enum member is not a function reference")
	assert.Contains(t, cands, "reduceAction", "a genuine handler in the same table is still captured")
}

// TestCExtractor_PerFileCandidateFuse pins the per-file fuse: a pathological
// generated file with more distinct value-position handlers than the cap emits
// exactly the cap — the bound that stops a generated dispatch table from
// exploding the placeholder set.
func TestCExtractor_PerFileCandidateFuse(t *testing.T) {
	var b strings.Builder
	b.WriteString("void *bigDispatch[] = {\n")
	for i := 0; i < cFnAddressMaxPerFile+200; i++ {
		fmt.Fprintf(&b, "  handlerFn%05d,\n", i)
	}
	b.WriteString("};\n")

	r, err := NewCExtractor().Extract("generated.c", []byte(b.String()))
	require.NoError(t, err)

	n := 0
	for _, e := range r.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == "callback_candidate" {
			n++
		}
	}
	assert.Equal(t, cFnAddressMaxPerFile, n, "the per-file fuse caps emitted candidates at the const bound")
}

// TestCExtractor_DispatchTableCandidateFields pins the candidate metadata a
// realistic cross-TU dispatch table produces: each handler is captured exactly
// once (dedup), marked ungated so the resolver binds it cross-translation-unit,
// tagged with the capturing grammar, and carries no address-of form (a plain
// value slot).
func TestCExtractor_DispatchTableCandidateFields(t *testing.T) {
	src := "" +
		"struct cmd table[] = {\n" + // line 1
		"{ \"get\", getCommand, 2 },\n" + // 2
		"{ \"set\", setCommand, 3 },\n" + // 3
		"};\n"
	r, err := NewCExtractor().Extract("dispatch.c", []byte(src))
	require.NoError(t, err)

	type capture struct {
		count   int
		ungated bool
		form    string
		lang    string
	}
	got := map[string]capture{}
	for _, e := range r.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "callback_candidate" {
			continue
		}
		name, _ := e.Meta["fn_value_name"].(string)
		if name == "" {
			continue
		}
		c := got[name]
		c.count++
		if u, _ := e.Meta["fn_value_ungated"].(bool); u {
			c.ungated = true
		}
		c.form, _ = e.Meta["fn_ref_form"].(string)
		c.lang, _ = e.Meta["fn_ref_lang"].(string)
		got[name] = c
	}

	for _, name := range []string{"getCommand", "setCommand"} {
		c := got[name]
		assert.Equal(t, 1, c.count, "%s is captured exactly once", name)
		assert.True(t, c.ungated, "%s is ungated for cross-TU binding", name)
		assert.Equal(t, "", c.form, "%s sits in a plain value position (no address-of form)", name)
		assert.Equal(t, "c", c.lang, "%s is tagged with the capturing grammar", name)
	}
}
