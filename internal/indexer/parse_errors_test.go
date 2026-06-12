package indexer

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	gosit "github.com/zzet/gortex/internal/parser/tsitter/golang"
)

func TestStampParseErrors_StampsCountOnFileNode(t *testing.T) {
	// Genuine Go syntax with a missing closing brace — tree-sitter
	// recovers but emits ERROR / MISSING nodes.
	broken := []byte("package main\n\nfunc Foo() {\n  if x {\n    println(\"hi\")\n}\n")
	tree, err := parser.ParseFile(broken, gosit.GetLanguage())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := parser.NewParseTree(tree, broken, "go")

	result := &parser.ExtractionResult{
		Nodes: []*graph.Node{
			{ID: "broken.go", Kind: graph.KindFile, FilePath: "broken.go"},
		},
		Tree: pt,
	}
	stampParseErrors(result)

	file := result.Nodes[0]
	if got, _ := file.Meta["has_parse_errors"].(bool); !got {
		t.Errorf("has_parse_errors = false; want true")
	}
	if got, _ := file.Meta["parse_errors"].(int); got <= 0 {
		t.Errorf("parse_errors = %d; want > 0", got)
	}
	pt.Release()
}

func TestStampParseErrors_NoMetaWhenClean(t *testing.T) {
	clean := []byte("package main\n\nfunc Foo() {}\n")
	tree, err := parser.ParseFile(clean, gosit.GetLanguage())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt := parser.NewParseTree(tree, clean, "go")

	result := &parser.ExtractionResult{
		Nodes: []*graph.Node{
			{ID: "clean.go", Kind: graph.KindFile, FilePath: "clean.go"},
		},
		Tree: pt,
	}
	stampParseErrors(result)

	file := result.Nodes[0]
	if file.Meta != nil {
		if _, ok := file.Meta["has_parse_errors"]; ok {
			t.Errorf("has_parse_errors stamped on clean file")
		}
	}
	pt.Release()
}

func TestStampParseErrors_NilTreeSafe(t *testing.T) {
	result := &parser.ExtractionResult{
		Nodes: []*graph.Node{
			{ID: "x.go", Kind: graph.KindFile, FilePath: "x.go"},
		},
		Tree: nil,
	}
	// Must not panic and must not stamp.
	stampParseErrors(result)
	if result.Nodes[0].Meta != nil {
		if _, ok := result.Nodes[0].Meta["has_parse_errors"]; ok {
			t.Errorf("nil-tree path stamped meta")
		}
	}
}

// silence unused-import warning if golang.go doesn't ship with the
// reference grammar in some build configurations.
var _ = languages.NewGoExtractor
