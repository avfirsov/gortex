package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

func swiftHasType(t *testing.T, src, name string) bool {
	t.Helper()
	res, err := NewSwiftExtractor().Extract("S.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.Name == name {
			return true
		}
	}
	return false
}

// TestSwiftExtractor_ParseErrorContainerFallback: when a construct inside a
// type body defeats the tree-sitter grammar (here a compound
// `#if … && !canImport(...)`), the enclosing class_declaration is corrupted and
// the query never matches it. The brace-matched fallback must still emit the
// container node — otherwise the type and every reference to it strand on
// unresolved::Name (the alamofire Session 0-vs-449 bug). Self-proving: it only
// asserts the fallback while the grammar genuinely errors on this body; if a
// future grammar parses it cleanly the query covers the container and we skip.
func TestSwiftExtractor_ParseErrorContainerFallback(t *testing.T) {
	src := `open class Session: @unchecked Sendable {
    public let session: URLSession

    func send() {
        #if compiler(>=6) && !canImport(FoundationNetworking)
        doThing()
        #endif
    }
}`
	e := NewSwiftExtractor()
	tree, err := parser.ParseFile([]byte(src), e.lang)
	if err != nil {
		t.Fatal(err)
	}
	hasErr := tree.RootNode().HasError()
	tree.Close()
	if !hasErr {
		t.Skip("grammar parses this body cleanly now; fallback path not exercised")
	}

	res, err := e.Extract("S.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	gotType := false
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.Name == "Session" {
			gotType = true
		}
		// The case-twin member must attribute to the container, not leak as a
		// file-scope constant.
		if n.ID == "S.swift::session" {
			t.Errorf("stray file-scope ::session constant emitted (member not attributed to Session)")
		}
	}
	if !gotType {
		t.Errorf("class Session container node not emitted under a body parse error (fallback failed)")
	}
}

// TestSwiftExtractor_CleanClassStillEmitted guards the common path: a clean
// class (no parse error) is still emitted exactly once, not duplicated by the
// fallback.
func TestSwiftExtractor_CleanClassStillEmitted(t *testing.T) {
	src := `public final class Widget {
    let name: String
}`
	if !swiftHasType(t, src, "Widget") {
		t.Errorf("clean class Widget not emitted")
	}
	res, _ := NewSwiftExtractor().Extract("S.swift", []byte(src))
	n := 0
	for _, node := range res.Nodes {
		if node.Kind == graph.KindType && node.Name == "Widget" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("clean class Widget emitted %d times, want 1 (fallback double-emit?)", n)
	}
}
