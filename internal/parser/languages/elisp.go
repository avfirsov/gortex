package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Emacs Lisp. Definitions are S-expressions; we grab the common `def*`
// forms and module-level `require` / `load` / `provide`. Call sites
// are any `(name ...)` inside a `defun` body; the extractor filters
// against a keyword list to reduce noise.
var (
	elispDefRe     = regexp.MustCompile(`\(def(?:un|macro|subst|generic|method|advice)\*?\s+([\w:*/+<>?!=.-]+)`)
	elispVarRe     = regexp.MustCompile(`\(def(?:var|const|custom|face|group)\s+([\w:*/+<>?!=.-]+)`)
	elispRequireRe = regexp.MustCompile(`\(require\s+'([\w:*/+<>?!=.-]+)`)
	elispLoadRe    = regexp.MustCompile(`\(load(?:-file|-library)?\s+"([^"]+)"`)
	elispCallRe    = regexp.MustCompile(`\(([\w:*/+<>?!=.-]+)`)
)

// EmacsLispExtractor extracts Emacs Lisp source using regex.
type EmacsLispExtractor struct{}

func NewEmacsLispExtractor() *EmacsLispExtractor { return &EmacsLispExtractor{} }

func (e *EmacsLispExtractor) Language() string { return "elisp" }
func (e *EmacsLispExtractor) Extensions() []string {
	return []string{".el", ".elc"}
}

func (e *EmacsLispExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "elisp",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" {
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
			Language: "elisp",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range elispDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findIndentedBlockEnd(lines, line))
	}
	for _, m := range elispVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range elispRequireRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range elispLoadRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range elispCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isElispKeyword(name) {
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

func isElispKeyword(s string) bool {
	switch s {
	case "if", "when", "unless", "cond", "and", "or", "not", "let", "let*",
		"letrec", "progn", "prog1", "prog2", "lambda", "function", "quote",
		"setq", "setf", "save-excursion", "save-restriction", "while", "dolist",
		"dotimes", "defun", "defmacro", "defvar", "defconst", "defcustom",
		"defface", "defgroup", "require", "provide", "load", "t", "nil":
		return true
	}
	return false
}

var _ parser.Extractor = (*EmacsLispExtractor)(nil)
