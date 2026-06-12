package elide

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// controlFlowKinds are the tree-sitter node kinds that carry a
// program's branching structure across the grammars elide supports.
// A line on which one of these nodes opens, shows its header, or
// closes is kept verbatim by SalienceTruncate even when it sits
// inside a function body. The set is a deliberate superset across
// languages — an unrecognised kind only means its line may be
// collapsed (the elided-count marker still flags it), never a crash.
//
// The parse walk visits named nodes only, so a bare-keyword entry
// (e.g. Ruby's "if") can never collide with an anonymous keyword
// token in another grammar.
var controlFlowKinds = map[string]struct{}{
	// conditionals
	"if_statement": {}, "if_expression": {}, "if": {},
	"elif_clause": {}, "else_clause": {}, "else_if_clause": {},
	"elsif": {}, "else": {}, "unless": {}, "guard_statement": {},
	// loops
	"for_statement": {}, "for_expression": {}, "for_in_statement": {},
	"for_of_statement": {}, "for_range_loop": {}, "enhanced_for_statement": {},
	"foreach_statement": {}, "for_each_statement": {}, "for": {},
	"while_statement": {}, "while_expression": {}, "while": {},
	"do_statement": {}, "do_while_statement": {}, "until": {},
	"loop_expression": {}, "loop_statement": {},
	// switch / match / case
	"switch_statement": {}, "switch_expression": {}, "switch_section": {},
	"switch_case": {}, "switch_default": {}, "switch_block_statement_group": {},
	"switch_rule": {}, "case_statement": {}, "case_clause": {}, "case": {},
	"default_statement": {}, "when": {}, "when_entry": {},
	"match_expression": {}, "match_statement": {}, "match_arm": {},
	"expression_case": {}, "type_case": {}, "default_case": {},
	"communication_case": {}, "select_statement": {}, "cond": {},
	// exception flow
	"try_statement": {}, "try_expression": {}, "try_with_resources_statement": {},
	"catch_clause": {}, "catch_block": {}, "except_clause": {},
	"finally_clause": {}, "finally_block": {}, "rescue": {}, "ensure": {},
	"begin": {}, "with_statement": {},
	// jumps
	"return_statement": {}, "return_expression": {}, "return": {},
	"throw_statement": {}, "throw_expression": {}, "raise_statement": {},
	"break_statement": {}, "continue_statement": {}, "yield_statement": {},
	"goto_statement": {}, "fallthrough_statement": {}, "defer_statement": {},
	"go_statement": {}, "labeled_statement": {}, "jump_expression": {},
	"next": {}, "redo": {},
}

// SalienceTruncate shrinks oversized source by keeping its control-flow
// skeleton and collapsing runs of leaf statements inside function
// bodies into a `… N lines elided …` marker. Signatures, declarations,
// imports, types, comments and every branch / loop / return keyword
// line survive; only the straight-line statements between them are
// dropped — a structure-preserving alternative to a hard line cut.
//
// It is a no-op (returns src, false, nil) when src already fits within
// maxLines or when maxLines <= 0. For a language elide cannot parse, or
// when parsing fails, it falls back to a plain head cut so callers
// still get a bounded result; the returned error is advisory and the
// returned bytes are always usable. When the skeleton itself still
// exceeds maxLines a final head cut keeps the budget a hard ceiling.
func SalienceTruncate(src []byte, lang string, maxLines int) ([]byte, bool, error) {
	if maxLines <= 0 || len(src) == 0 {
		return src, false, nil
	}
	lines := strings.Split(string(src), "\n")
	if len(lines) <= maxLines {
		return src, false, nil
	}
	comment := lineComment(lang)

	spec := getSpec(lang)
	if spec == nil {
		out := strings.Join(headCutLines(lines, maxLines, comment), "\n")
		return []byte(out), true, fmt.Errorf("%w: %q", ErrUnsupportedLang, lang)
	}
	grammar := spec.grammarFn()
	if grammar == nil {
		out := strings.Join(headCutLines(lines, maxLines, comment), "\n")
		return []byte(out), true, fmt.Errorf("%w: %q (no grammar binding)", ErrUnsupportedLang, lang)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(grammar)
	ctx, cancel := context.WithTimeout(context.Background(), parseTimeout)
	defer cancel()
	tree, perr := parser.ParseCtx(ctx, nil, src)
	if perr != nil || tree == nil {
		out := strings.Join(headCutLines(lines, maxLines, comment), "\n")
		return []byte(out), true, ErrParse
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil {
		return src, false, nil
	}

	inBody := make([]bool, len(lines))
	salient := make([]bool, len(lines))
	markSalience(root, spec, inBody, salient)

	skel := collapseLeafRuns(lines, inBody, salient, comment)
	if len(skel) > maxLines {
		skel = headCutLines(skel, maxLines, comment)
	}
	out := strings.Join(skel, "\n")
	return []byte(out), out != string(src), nil
}

// markSalience walks the parse tree and fills two per-line bitmaps:
// inBody[r] is true when row r sits inside some function / method
// body, and salient[r] is true when row r carries a control-flow
// header or its closing-delimiter line. A row that is inBody and not
// salient is a leaf-statement line and may be collapsed.
//
// Two grammar families need different bookkeeping. For indent-style
// bodies (Python, Ruby) the body block node spans the statements
// themselves, so every body row counts and the header ends one row
// above the body. For delimiter-style bodies (braces, Elixir do/end)
// the body node includes its open/close lines, so those are excluded
// and the closing delimiter of each construct is itself kept.
func markSalience(root *sitter.Node, spec *languageSpec, inBody, salient []bool) {
	set := func(s []bool, lo, hi int) {
		if lo < 0 {
			lo = 0
		}
		if hi >= len(s) {
			hi = len(s) - 1
		}
		for r := lo; r <= hi; r++ {
			s[r] = true
		}
	}
	indentBody := spec.style == stubPython || spec.style == stubRuby
	markEnd := spec.style != stubPython
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}
		kind := node.Type()
		if _, ok := controlFlowKinds[kind]; ok {
			start := int(node.StartPoint().Row)
			headerEnd := controlFlowHeaderEnd(node)
			if indentBody && headerEnd > start {
				headerEnd--
			}
			set(salient, start, headerEnd)
			if markEnd {
				end := int(node.EndPoint().Row)
				set(salient, end, end)
			}
		}
		if _, isParent := spec.parents[kind]; isParent {
			if body := spec.findBody(node); body != nil {
				bs := int(body.StartPoint().Row)
				be := int(body.EndPoint().Row)
				if indentBody {
					set(inBody, bs, be)
				} else {
					set(inBody, bs+1, be-1)
				}
			}
		}
		cnt := int(node.NamedChildCount())
		for i := range cnt {
			walk(node.NamedChild(i))
		}
	}
	walk(root)
}

// controlFlowHeaderEnd returns the last row of a control-flow node's
// header — the keyword line plus any multi-line condition, up to and
// including the line its body block opens on. It treats the widest
// later-starting named child as the body. Falls back to the node's
// start row when no child stands out (a single-line construct).
func controlFlowHeaderEnd(node *sitter.Node) int {
	start := int(node.StartPoint().Row)
	bodyStart := start
	widest := -1
	cnt := int(node.NamedChildCount())
	for i := range cnt {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		cs := int(c.StartPoint().Row)
		if cs <= start {
			continue
		}
		span := int(c.EndPoint().Row) - cs
		if span > widest {
			widest = span
			bodyStart = cs
		}
	}
	return bodyStart
}

// collapseLeafRuns rewrites lines, replacing each maximal run of
// in-body non-salient lines with a single indented marker. Blank-only
// runs are emitted verbatim so the skeleton keeps its vertical
// spacing.
func collapseLeafRuns(lines []string, inBody, salient []bool, comment string) []string {
	out := make([]string, 0, len(lines))
	i, n := 0, len(lines)
	for i < n {
		if !inBody[i] || salient[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		start := i
		nonBlank := 0
		for i < n && inBody[i] && !salient[i] {
			if strings.TrimSpace(lines[i]) != "" {
				nonBlank++
			}
			i++
		}
		if nonBlank == 0 {
			out = append(out, lines[start:i]...)
			continue
		}
		out = append(out, fmt.Sprintf("%s%s … %d lines elided …",
			leadingWhitespace(lines[start]), comment, nonBlank))
	}
	return out
}

// headCutLines keeps the first maxLines lines and replaces the tail
// with a single marker. It is the fallback when a language cannot be
// parsed or a skeleton still busts the budget.
func headCutLines(lines []string, maxLines int, comment string) []string {
	if len(lines) <= maxLines || maxLines < 0 {
		return lines
	}
	dropped := len(lines) - maxLines
	out := make([]string, 0, maxLines+1)
	out = append(out, lines[:maxLines]...)
	out = append(out, fmt.Sprintf("%s … %d more lines elided (max_lines budget) …", comment, dropped))
	return out
}

// leadingWhitespace returns the run of spaces and tabs that opens a
// line, used to align an elision marker with the code it replaces.
func leadingWhitespace(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return line[:i]
		}
	}
	return line
}

// lineComment returns the single-line comment prefix for a language so
// elision markers stay syntactically inert in the host source.
func lineComment(lang string) string {
	switch normalizeLang(lang) {
	case "python", "ruby", "bash", "elixir":
		return "#"
	default:
		return "//"
	}
}
