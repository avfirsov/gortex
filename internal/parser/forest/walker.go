package forest

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// extractByWalker is the fallback for grammars that do not ship a
// tags.scm. It walks every named node in the parse tree and matches
// kind names against a small set of suffix/prefix heuristics that
// catch the conventional tree-sitter naming pattern
// `<thing>_definition` / `<thing>_declaration` / `<thing>_specifier`.
//
// This is naive on purpose. For the long tail (~440 grammars without
// tags.scm) it produces good-enough signature-only extraction without
// hand-tuning queries per language. Languages where the heuristic
// underfits get a tags.scm contribution upstream or a bespoke
// extractor in internal/parser/languages.
func (e *Extractor) extractByWalker(
	root *sitter.Node, src []byte, filePath string, fileNode *graph.Node, result *parser.ExtractionResult,
) {
	seen := make(map[string]bool)

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if kind := classifyKind(e.language, n.Type()); kind != "" {
			if name := nodeName(n, src); name != "" {
				e.emitWalkerNode(filePath, fileNode, kind, name, n, seen, result)
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
}

// emitWalkerNode is the walker's adaptation of emitDefinition — it
// builds the same shape but takes raw sitter.Node positions rather
// than CapturedNode.
func (e *Extractor) emitWalkerNode(
	filePath string, fileNode *graph.Node, kind graph.NodeKind, name string,
	n *sitter.Node, seen map[string]bool, result *parser.ExtractionResult,
) {
	id := filePath + "::" + name
	if seen[id] {
		return
	}
	seen[id] = true

	startLine := int(n.StartPoint().Row) + 1
	endLine := int(n.EndPoint().Row) + 1

	result.Nodes = append(result.Nodes, &graph.Node{
		ID: id, Kind: kind, Name: name,
		FilePath: filePath, StartLine: startLine, EndLine: endLine,
		Language: e.language,
	})
	result.Edges = append(result.Edges, &graph.Edge{
		From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
		FilePath: filePath, Line: startLine,
	})
}

// classifyKind maps a tree-sitter node kind name to a graph.NodeKind.
// The dispatch is two-tier:
//
//  1. Per-language overrides — `languageKindMap[language][nodeKind]`
//     handles grammars whose rule names don't match the conventional
//     `*_definition` / `*_declaration` suffixes (Erlang's
//     `fun_decl` / `function_clause`, Haskell's `function` /
//     `signature`, Crystal's `class_def` / `method_def`, etc.).
//     Researched once per grammar via the dump_kinds_test helper.
//  2. Generic suffix matching — covers the long tail of grammars that
//     follow the standard `*_definition` / `*_declaration` /
//     `*_specifier` convention.
//
// Order matters within suffix matching: longer / more specific
// patterns checked first ("function_declaration" beats "_declaration").
func classifyKind(language, t string) graph.NodeKind {
	if t == "" {
		return ""
	}
	if perLang, ok := languageKindMap[language]; ok {
		if k, ok := perLang[t]; ok {
			return k
		}
	}

	// Methods first — `method_*` is more specific than `function_*`,
	// and a method declaration shouldn't fall through to function.
	switch {
	case hasAnySuffix(t, "method_definition", "method_declaration", "method_signature", "method_spec"):
		return graph.KindMethod
	case hasAnySuffix(t, "function_definition", "function_declaration", "function_signature", "function_spec", "function_item"):
		return graph.KindFunction
	case hasAnySuffix(t, "class_definition", "class_declaration", "class_specifier"):
		return graph.KindType
	case hasAnySuffix(t, "interface_definition", "interface_declaration"):
		return graph.KindInterface
	case hasAnySuffix(t, "trait_definition", "trait_declaration"):
		return graph.KindInterface
	case hasAnySuffix(t, "struct_definition", "struct_declaration", "struct_specifier", "struct_item"):
		return graph.KindType
	case hasAnySuffix(t, "enum_definition", "enum_declaration", "enum_specifier", "enum_item"):
		return graph.KindType
	case hasAnySuffix(t, "union_definition", "union_declaration", "union_specifier"):
		return graph.KindType
	case hasAnySuffix(t, "type_definition", "type_declaration", "type_alias_declaration", "type_alias", "type_item"):
		return graph.KindType
	case hasAnySuffix(t, "module_definition", "module_declaration", "namespace_definition", "namespace_declaration"):
		return graph.KindPackage
	case hasAnySuffix(t, "constant_declaration", "const_declaration", "const_item"):
		return graph.KindConstant
	case hasAnySuffix(t, "variable_declaration", "var_declaration"):
		return graph.KindVariable
	case hasAnySuffix(t, "field_declaration", "field_definition"):
		return graph.KindField
	case hasAnySuffix(t, "macro_definition", "macro_declaration"):
		return graph.KindFunction
	}
	return ""
}

// languageKindMap holds per-language node-kind → graph.NodeKind
// overrides. Add a row when a grammar's rule names diverge from the
// conventional `*_definition` / `*_declaration` patterns and the
// generic walker emits zero definitions on real source. Run the
// dump_kinds_test helper for that language to find the right names.
var languageKindMap = map[string]map[string]graph.NodeKind{
	"erlang": {
		"fun_decl": graph.KindFunction,
		// `-module(name)` is a `module_attribute` and the name lives
		// inside an `atom` child the generic nodeName helper doesn't
		// recognise — leave it to the regex idiom layer in
		// erlang.go.
	},
	"haskell": {
		"function":     graph.KindFunction,
		"signature":    graph.KindFunction,
		"data_type":    graph.KindType,
		"newtype":      graph.KindType,
		"type_synonym": graph.KindType,
		// Upstream tree-sitter-haskell ships the rule name as
		// `type_synomym` — typo and all. Match both spellings so
		// we don't depend on the grammar fixing it.
		"type_synomym": graph.KindType,
		"class":        graph.KindInterface,
		"instance":     graph.KindType,
	},
	"crystal": {
		"class_def":  graph.KindType,
		"module_def": graph.KindType,
		"struct_def": graph.KindType,
		"method_def": graph.KindMethod,
	},
	"nim": {
		"proc_declaration": graph.KindFunction,
		"func_declaration": graph.KindFunction,
		"type_declaration": graph.KindType,
		// object_declaration / enum_declaration nest inside
		// type_declaration; emit on the outer wrapper to avoid
		// duplicate nodes.
	},
	"ada": {
		"function_specification":  graph.KindFunction,
		"procedure_specification": graph.KindFunction,
	},
	"fortran": {
		"function":           graph.KindFunction,
		"function_statement": graph.KindFunction,
		"module":             graph.KindPackage,
		"module_statement":   graph.KindPackage,
	},
	"perl": {
		"function":                         graph.KindFunction,
		"subroutine_declaration_statement": graph.KindFunction,
	},
	"powershell": {
		"function_statement": graph.KindFunction,
		"class_statement":    graph.KindType,
	},
	"odin": {
		"procedure_declaration": graph.KindFunction,
		"struct_declaration":    graph.KindType,
		"package_declaration":   graph.KindPackage,
	},
	"cmake": {
		"function_def": graph.KindFunction,
		"macro_def":    graph.KindFunction,
	},
	"apex": {
		"class_declaration":   graph.KindType,
		"method_declaration":  graph.KindMethod,
		"trigger_declaration": graph.KindFunction,
	},
	"solidity": {
		"contract_declaration":  graph.KindType,
		"interface_declaration": graph.KindInterface,
		"modifier_definition":   graph.KindFunction,
		"event_definition":      graph.KindFunction,
		"enum_declaration":      graph.KindType,
		"struct_declaration":    graph.KindType,
		// function_definition already covered by the generic
		// `*_definition` suffix in classifyKind.
	},
	"tact": {
		"trait":            graph.KindInterface,
		"contract":         graph.KindType,
		"init_function":    graph.KindFunction,
		"receive_function": graph.KindFunction,
		"storage_function": graph.KindFunction,
	},
	"fsharp": {
		"function_or_value_defn": graph.KindFunction,
		"named_module":           graph.KindPackage,
		"record_type_defn":       graph.KindType,
		// type_definition handled by generic suffix.
	},
	"gdscript": {
		"class_name_statement": graph.KindType,
	},
	"jinja": {
		"macro_statement": graph.KindFunction,
	},
	"twig": {
		"macro_statement": graph.KindFunction,
	},
	"rescript": {
		"let_declaration":    graph.KindFunction,
		"module_declaration": graph.KindPackage,
		"type_declaration":   graph.KindType,
	},
	"objc": {
		"class_interface":          graph.KindType,
		"class_implementation":     graph.KindType,
		"method_declaration":       graph.KindMethod,
		"method_definition":        graph.KindMethod,
		"implementation_definition": graph.KindFunction,
	},
	"al": {
		"codeunit_declaration": graph.KindType,
		"table_declaration":    graph.KindType,
		"page_declaration":     graph.KindType,
		"procedure":            graph.KindMethod,
		// AL's `procedure` node holds the name in an `identifier`
		// child, but the test fixtures use `attributed_procedure`
		// wrappers; both routes converge on the same identifier.
	},
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if s == suf || strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// nodeName tries the conventional `name:` field first, then falls
// back to the first identifier-like child within a depth-3 search.
// Returns "" if neither is present (anonymous functions / unnamed
// structs).
//
// Three levels of recursion catches the common "wrapper holds the
// name in a typed sub-node" pattern: Erlang `fun_decl ▶
// function_clause ▶ atom`, Nim `proc_declaration ▶
// symbol_declaration ▶ exported_symbol ▶ identifier`. Going
// deeper would risk returning a parameter name when the
// function-name capture is missing entirely.
//
// "Identifier-like" covers the conventional names plus a few
// language-specific tokens that grammars use for the same role:
// `constant` (Ruby/Crystal class names), `atom` (Erlang),
// `variable` (Haskell binding names), `lower_case_identifier`
// and `upper_case_identifier` (Elm).
func nodeName(n *sitter.Node, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil {
		return strings.TrimSpace(name.Content(src))
	}
	return findFirstNameIn(n, src, 3)
}

func findFirstNameIn(n *sitter.Node, src []byte, depth int) string {
	if n == nil || depth < 0 {
		return ""
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		if isIdentifierKind(c.Type()) {
			return strings.TrimSpace(c.Content(src))
		}
		if name := findFirstNameIn(c, src, depth-1); name != "" {
			return name
		}
	}
	return ""
}

func isIdentifierKind(t string) bool {
	return strings.Contains(t, "identifier") ||
		t == "name" ||
		t == "type_identifier" ||
		t == "constant" ||
		t == "variable" ||
		t == "atom"
}
