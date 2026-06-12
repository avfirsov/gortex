package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	"github.com/zzet/gortex/internal/parser/tsitter/bash"
)

// bashQAll is a single tree-sitter query alternating over every
// pattern the Bash extractor needs — function definitions, top-level
// variable assignments, and command calls. One cursor walk per file
// replaces the three independent EachMatch passes the previous design
// made. Capture names are disjoint across patterns so the dispatch in
// Extract branches on which one is set.
const bashQAll = `
[
  (function_definition
    name: (word) @func.name) @func.def

  (variable_assignment
    name: (variable_name) @var.name) @var.def

  (command
    name: (command_name) @cmd.name) @cmd.expr
]
`

// BashExtractor extracts Bash/Shell source files. A single precompiled
// alternation query drives one cursor walk per file.
type BashExtractor struct {
	lang *sitter.Language
	qAll *parser.PreparedQuery
}

func NewBashExtractor() *BashExtractor {
	lang := bash.GetLanguage()
	return &BashExtractor{
		lang: lang,
		qAll: parser.MustPreparedQuery(bashQAll, lang),
	}
}

func (e *BashExtractor) Language() string     { return "bash" }
func (e *BashExtractor) Extensions() []string { return []string{".sh", ".bash", ".zsh"} }

// bashDeferredCmd is a command call site held back until the single
// walk completes — command attribution needs funcRanges, which is
// built from the function nodes emitted during that same walk.
type bashDeferredCmd struct {
	name string
	line int
}

func (e *BashExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
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
		Language: "bash",
	}
	fileID := fileNode.ID
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	var cmds []bashDeferredCmd

	parser.EachMatch(e.qAll, root, src, func(m parser.QueryResult) {
		switch {

		case m.Captures["func.def"] != nil:
			name := m.Captures["func.name"].Text
			def := m.Captures["func.def"]
			id := filePath + "::" + name
			if seen[id] {
				return
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindFunction, Name: name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "bash", Meta: map[string]any{"signature": name + "()"},
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
			})

		case m.Captures["var.def"] != nil:
			name := m.Captures["var.name"].Text
			def := m.Captures["var.def"]
			// Only top-level: parent is program.
			if def.Node == nil || def.Node.Parent() == nil || def.Node.Parent().Type() != "program" {
				return
			}
			id := filePath + "::" + name
			if seen[id] {
				return
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindVariable, Name: name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "bash",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
			})

		case m.Captures["cmd.expr"] != nil:
			cmdName := m.Captures["cmd.name"].Text
			expr := m.Captures["cmd.expr"]

			// Source/dot imports (`source foo.sh` / `. foo.sh`)
			// attach to the file node, so they emit during the walk —
			// no funcRanges needed.
			if cmdName == "source" || cmdName == "." {
				cmdNode := expr.Node
				if cmdNode != nil && cmdNode.NamedChildCount() >= 2 {
					arg := cmdNode.NamedChild(1)
					if arg != nil {
						importPath := strings.Trim(arg.Content(src), "\"'")
						result.Edges = append(result.Edges, &graph.Edge{
							From: fileID, To: "unresolved::import::" + importPath,
							Kind: graph.EdgeImports, FilePath: filePath, Line: expr.StartLine + 1,
						})
					}
				}
				return
			}

			// Regular command call — deferred until funcRanges is built.
			cmds = append(cmds, bashDeferredCmd{name: cmdName, line: expr.StartLine + 1})
		}
	})

	// Command attribution needs the enclosing function, so it runs
	// after the single walk has emitted every function node.
	funcRanges := buildFuncRanges(result)
	for _, c := range cmds {
		callerID := findEnclosingFunc(funcRanges, c.line)
		if callerID == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + c.name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: c.line,
		})
	}

	return result, nil
}
