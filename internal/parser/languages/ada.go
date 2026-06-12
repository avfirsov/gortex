package languages

import (
	"regexp"
	"strings"

	adaforest "github.com/alexaandru/go-sitter-forest/ada"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Ada migration: forest's walker (per-language map for
// `function_specification` / `procedure_specification`) catches the
// callable shapes. Package declarations, type declarations, and
// `with` import clauses stay regex.
var (
	adaPackageRe = regexp.MustCompile(`(?im)^\s*package\s+(?:body\s+)?([\w.]+)`)
	adaTypeRe    = regexp.MustCompile(`(?im)^\s*(?:sub)?type\s+(\w+)\s+is`)
	adaWithRe    = regexp.MustCompile(`(?im)^\s*with\s+([\w.]+(?:\s*,\s*[\w.]+)*)\s*;`)
	adaSplitRe   = regexp.MustCompile(`[\s,]+`)
)

type AdaExtractor struct {
	forest *forest.Extractor
}

func NewAdaExtractor() *AdaExtractor {
	return &AdaExtractor{
		forest: forest.New("ada", []string{".ada", ".adb", ".ads", ".gpr"}, adaforest.GetLanguage, adaforest.GetQuery),
	}
}

func (e *AdaExtractor) Language() string { return "ada" }
func (e *AdaExtractor) Extensions() []string {
	return []string{".ada", ".adb", ".ads", ".gpr"}
}

func (e *AdaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	for _, n := range res.Nodes {
		seen[n.ID] = true
	}
	add := func(name string, kind graph.NodeKind, line int) {
		if name == "" {
			return
		}
		id := filePath + "::" + name
		if seen[id] {
			return
		}
		seen[id] = true
		res.Nodes = append(res.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: "ada",
		})
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	for _, m := range adaPackageRe.FindAllSubmatchIndex(src, -1) {
		add(string(src[m[2]:m[3]]), graph.KindType, lineAt(src, m[0]))
	}
	for _, m := range adaTypeRe.FindAllSubmatchIndex(src, -1) {
		add(string(src[m[2]:m[3]]), graph.KindType, lineAt(src, m[0]))
	}

	for _, m := range adaWithRe.FindAllSubmatchIndex(src, -1) {
		clause := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		for _, tok := range adaSplitRe.Split(clause, -1) {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			res.Edges = append(res.Edges, &graph.Edge{
				From: filePath, To: "unresolved::import::" + tok,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return res, nil
}

var _ parser.Extractor = (*AdaExtractor)(nil)
