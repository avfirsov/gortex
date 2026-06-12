package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	"github.com/zzet/gortex/internal/parser/tsitter/css"
)

// qCssAll is a single tree-sitter query alternating over every pattern
// the CSS extractor needs — @import rules, class selectors, id
// selectors, and custom-property (`--name`) declarations. One cursor
// walk per file replaces the three EachMatch passes plus the separate
// recursive declaration walk the previous design made. Capture names
// are disjoint across patterns so the dispatch in Extract branches on
// which one is set.
const qCssAll = `
[
  (import_statement) @import.def

  (class_selector
    (class_name) @class.name) @class.def

  (id_selector
    (id_name) @id.name) @id.def

  (declaration
    (property_name) @prop.name) @prop.def
]
`

// CSSExtractor extracts CSS files into graph nodes and edges. A single
// precompiled alternation query drives one cursor walk per file.
type CSSExtractor struct {
	lang *sitter.Language
	qAll *parser.PreparedQuery
}

func NewCSSExtractor() *CSSExtractor {
	lang := css.GetLanguage()
	return &CSSExtractor{
		lang: lang,
		qAll: parser.MustPreparedQuery(qCssAll, lang),
	}
}

func (e *CSSExtractor) Language() string     { return "css" }
func (e *CSSExtractor) Extensions() []string { return []string{".css"} }

func (e *CSSExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	tree, err := parser.ParseFile(src, e.lang)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: int(root.EndPoint().Row) + 1,
		Language: "css",
	}
	fileID := fileNode.ID
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	parser.EachMatch(e.qAll, root, src, func(m parser.QueryResult) {
		switch {

		case m.Captures["import.def"] != nil:
			def := m.Captures["import.def"]
			// Extract the path from @import url("...") or @import "...".
			importPath := extractCSSImportPath(def.Text)
			if importPath == "" {
				return
			}
			result.Edges = append(result.Edges, &graph.Edge{
				From:     fileID,
				To:       "unresolved::import::" + importPath,
				Kind:     graph.EdgeImports,
				FilePath: filePath,
				Line:     def.StartLine + 1,
			})

		case m.Captures["class.def"] != nil:
			name := m.Captures["class.name"].Text
			def := m.Captures["class.def"]
			id := filePath + "::." + name
			if seen[id] {
				return
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindType, Name: "." + name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "css",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines,
				FilePath: filePath, Line: def.StartLine + 1,
			})

		case m.Captures["id.def"] != nil:
			name := m.Captures["id.name"].Text
			def := m.Captures["id.def"]
			id := filePath + "::#" + name
			if seen[id] {
				return
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindVariable, Name: "#" + name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "css",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines,
				FilePath: filePath, Line: def.StartLine + 1,
			})

		case m.Captures["prop.def"] != nil:
			// CSS custom properties — declarations whose property
			// name starts with "--".
			name := m.Captures["prop.name"].Text
			if !strings.HasPrefix(name, "--") {
				return
			}
			def := m.Captures["prop.def"]
			id := filePath + "::" + name
			if seen[id] {
				return
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindVariable, Name: name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "css", Meta: map[string]any{
					"custom_property": true,
				},
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines,
				FilePath: filePath, Line: def.StartLine + 1,
			})
		}
	})

	return result, nil
}

// extractCSSImportPath extracts the path from an @import statement.
// Handles: @import url("path"); @import url('path'); @import "path"; @import 'path';
func extractCSSImportPath(text string) string {
	text = strings.TrimPrefix(text, "@import")
	text = strings.TrimSpace(text)
	text = strings.TrimSuffix(text, ";")
	text = strings.TrimSpace(text)

	// Handle url("...") or url('...')
	if strings.HasPrefix(text, "url(") {
		text = strings.TrimPrefix(text, "url(")
		text = strings.TrimSuffix(text, ")")
		text = strings.TrimSpace(text)
	}

	text = strings.Trim(text, `"'`)
	return text
}
