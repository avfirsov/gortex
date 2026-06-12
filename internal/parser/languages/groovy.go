package languages

import (
	"regexp"

	groovyforest "github.com/alexaandru/go-sitter-forest/groovy"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/forest"
)

// Groovy migration: forest's walker handles class_definition /
// function_definition via generic suffix matching. `import` edges
// stay regex.
var groovyImportRe = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([\w.\*]+)`)

type GroovyExtractor struct {
	forest *forest.Extractor
}

func NewGroovyExtractor() *GroovyExtractor {
	return &GroovyExtractor{
		forest: forest.New("groovy",
			[]string{".groovy", ".gvy", ".gy", ".gradle"},
			groovyforest.GetLanguage, groovyforest.GetQuery),
	}
}

func (e *GroovyExtractor) Language() string { return "groovy" }
func (e *GroovyExtractor) Extensions() []string {
	return []string{".groovy", ".gvy", ".gy", ".gradle"}
}

func (e *GroovyExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	res, err := e.forest.Extract(filePath, src)
	if err != nil {
		return nil, err
	}
	for _, m := range groovyImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		res.Edges = append(res.Edges, &graph.Edge{
			From: filePath, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	return res, nil
}

var _ parser.Extractor = (*GroovyExtractor)(nil)
