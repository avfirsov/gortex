package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Nix is a lazy functional expression language. The common shapes
// Gortex extracts are attribute bindings at the top level of an
// attrset (`name = expr;`), lambda-bound names inside bindings
// (`name = { a, b }: expr;` → a one-arg function), and imports
// (`import ./path`, `fetchurl { ... }`, `builtins.fetchTarball`).
//
// Call-site resolution in Nix is ambiguous because function
// application is juxtaposition (`f x`), so we punt on calls in v1
// and only record definition + import structure.
var (
	nixBindingRe = regexp.MustCompile(`(?m)^\s*([A-Za-z_][\w-]*)\s*=\s*`)
	nixLambdaRe  = regexp.MustCompile(`(?m)^\s*([A-Za-z_][\w-]*)\s*=\s*(?:\{[^}]*\}\s*:|[A-Za-z_][\w-]*\s*:)`)
	// Two capture groups: "quoted", relative path, or <angle>. Exactly one
	// of them will match per import occurrence; the handler picks
	// whichever is non-empty.
	nixImportRe = regexp.MustCompile(`\b(?:import|builtins\.fetchTarball|fetchurl|fetchGit|fetchFromGitHub)\s+(?:\(\s*)?(?:"([^"]+)"|(\.?\./[^;\s)]+)|<([^>]+)>)`)
	nixWithRe   = regexp.MustCompile(`(?m)\bwith\s+(\w[\w.]*)\s*;`)
	nixPkgRefRe = regexp.MustCompile(`(?m)\b(?:inherit\s*\(\s*)(\w[\w.]*)\s*\)`)
)

// NixExtractor extracts Nix expressions using regex.
type NixExtractor struct{}

func NewNixExtractor() *NixExtractor { return &NixExtractor{} }

func (e *NixExtractor) Language() string     { return "nix" }
func (e *NixExtractor) Extensions() []string { return []string{".nix"} }

func (e *NixExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "nix",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	lambdaLines := map[int]bool{}

	// First pass: lambda-bound attribute names — treat as functions.
	for _, m := range nixLambdaRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isNixKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		id := filePath + "::" + name
		lambdaLines[line] = true
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: name,
			FilePath: filePath, StartLine: line, EndLine: findBlockEnd(lines, line),
			Language: "nix",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	// Second pass: plain attribute bindings that weren't matched as lambdas.
	for _, m := range nixBindingRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isNixKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		if lambdaLines[line] {
			continue
		}
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindVariable, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "nix",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	// Imports — `import "path"` / `import ./path` / `import <nixpkgs>` /
	// fetch* variants. The regex has three mutually-exclusive capture
	// groups (quoted, relative path, angle-bracketed); whichever is
	// non-empty is the target.
	for _, m := range nixImportRe.FindAllSubmatchIndex(src, -1) {
		var target string
		for _, g := range []struct{ s, e int }{
			{m[2], m[3]},
			{m[4], m[5]},
			{m[6], m[7]},
		} {
			if g.s >= 0 && g.e > g.s {
				target = string(src[g.s:g.e])
				break
			}
		}
		if target == "" {
			continue
		}
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + target,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	// `with pkgs;` as implicit import reference.
	for _, m := range nixWithRe.FindAllSubmatchIndex(src, -1) {
		target := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::" + target,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	// `inherit (pkgs) name` — track the source alias.
	for _, m := range nixPkgRefRe.FindAllSubmatchIndex(src, -1) {
		target := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::" + target,
			Kind: graph.EdgeReferences, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isNixKeyword(s string) bool {
	switch s {
	case "let", "in", "with", "rec", "if", "then", "else", "true", "false",
		"null", "import", "inherit", "or", "assert", "builtins":
		return true
	}
	return false
}

var _ parser.Extractor = (*NixExtractor)(nil)
