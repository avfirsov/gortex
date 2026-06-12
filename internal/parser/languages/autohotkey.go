package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// AutoHotkey covers both v1 and v2 source. Functions in v2 look like
// `MyFunc(a, b) { ... }` while v1 accepts the same plus the legacy
// braceless form. Hotkeys `^!c::` and hotstrings `::abbr::expansion`
// are modelled as function-kind nodes so graph queries can reach them
// with `search_symbols`.
var (
	ahkFuncRe      = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][\w]*)\s*\([^)]*\)\s*\{`)
	ahkHotkeyRe    = regexp.MustCompile(`(?m)^[ \t]*([~$*!^+#<>& 0-9A-Za-z_]+?)::`)
	ahkHotstringRe = regexp.MustCompile(`(?m)^[ \t]*::([^:\n]+)::`)
	ahkLabelRe     = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][\w]*):\s*$`)
	ahkClassRe     = regexp.MustCompile(`(?m)^[ \t]*class\s+([A-Za-z_][\w]*)`)
	ahkIncludeRe   = regexp.MustCompile(`(?mi)^\s*#include\s+(?:<?)([^<>\r\n]+?)(?:>?)\s*$`)
	ahkCallRe      = regexp.MustCompile(`\b([A-Za-z_][\w]*)\s*\(`)
)

// AutoHotkeyExtractor extracts AutoHotkey source using regex.
type AutoHotkeyExtractor struct{}

func NewAutoHotkeyExtractor() *AutoHotkeyExtractor { return &AutoHotkeyExtractor{} }

func (e *AutoHotkeyExtractor) Language() string     { return "autohotkey" }
func (e *AutoHotkeyExtractor) Extensions() []string { return []string{".ahk", ".ahkl", ".ah2"} }

func (e *AutoHotkeyExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "autohotkey",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	addSymbol := func(name string, kind graph.NodeKind, start, end int, meta map[string]any) {
		if name == "" || isAHKKeyword(strings.ToLower(name)) {
			return
		}
		id := filePath + "::" + name
		if seen[id] {
			return
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: start, EndLine: end,
			Language: "autohotkey",
			Meta:     meta,
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range ahkClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		addSymbol(name, graph.KindType, line, end, nil)
	}

	for _, m := range ahkFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findBlockEnd(lines, line)
		addSymbol(name, graph.KindFunction, line, end, nil)
	}

	for _, m := range ahkHotkeyRe.FindAllSubmatchIndex(src, -1) {
		name := strings.TrimSpace(string(src[m[2]:m[3]]))
		if name == "" || strings.ContainsAny(name, " ") && !strings.HasPrefix(name, "~") {
			// Bare words followed by `::` are usually labels, not hotkeys.
			// Require a modifier or punctuation to classify as hotkey.
			if !strings.ContainsAny(name, "~^!+#<>&*$") {
				continue
			}
		}
		line := lineAt(src, m[0])
		addSymbol("hotkey:"+name, graph.KindFunction, line, line, map[string]any{"ahk_kind": "hotkey"})
	}

	for _, m := range ahkHotstringRe.FindAllSubmatchIndex(src, -1) {
		name := strings.TrimSpace(string(src[m[2]:m[3]]))
		line := lineAt(src, m[0])
		addSymbol("hotstring:"+name, graph.KindFunction, line, line, map[string]any{"ahk_kind": "hotstring"})
	}

	for _, m := range ahkLabelRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		addSymbol(name, graph.KindFunction, line, line, map[string]any{"ahk_kind": "label"})
	}

	for _, m := range ahkIncludeRe.FindAllSubmatchIndex(src, -1) {
		file := strings.TrimSpace(string(src[m[2]:m[3]]))
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + file,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range ahkCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isAHKKeyword(strings.ToLower(name)) {
			continue
		}
		line := lineAt(src, m[0])
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+name) {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isAHKKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "loop", "until", "break", "continue",
		"return", "exit", "global", "local", "static", "throw", "try", "catch",
		"finally", "switch", "case", "default", "class", "this", "true", "false",
		"not", "and", "or":
		return true
	}
	return false
}

var _ parser.Extractor = (*AutoHotkeyExtractor)(nil)
