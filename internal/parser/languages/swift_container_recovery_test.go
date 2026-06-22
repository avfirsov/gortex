package languages

import (
	"os"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// TestSwiftRecoverMissingTypeContainer verifies the ERROR-recovery scan emits a
// type container the tree-sitter query missed. The vendored Swift grammar can
// drop a large attributed class declaration into an ERROR node, so its
// class_declaration pattern never matches and no type node is produced even
// though the type's members parse and attribute to it. The recovery scan
// re-emits the missing container.
func TestSwiftRecoverMissingTypeContainer(t *testing.T) {
	e := NewSwiftExtractor()
	res := &parser.ExtractionResult{}
	// Simulate the post-query state: the query never emitted a Session type
	// node (seen is empty), but the source declares one.
	src := []byte("open class Session: @unchecked Sendable {\n    public let session: URLSession\n}\n")
	seen := map[string]bool{}
	var ranges []swiftTypeRange
	e.recoverMissingTypeContainers(src, "S.swift", "S.swift", res, seen, &ranges)

	var session *graph.Node
	for _, n := range res.Nodes {
		if n.ID == "S.swift::Session" && n.Kind == graph.KindType {
			session = n
		}
	}
	if session == nil {
		t.Fatal("recovery did not emit the missing Session type container")
	}
	if session.Meta["visibility"] != VisibilityPublic {
		t.Errorf("recovered Session visibility = %v, want public", session.Meta["visibility"])
	}
	if !seen["S.swift::Session"] {
		t.Error("recovery should mark the recovered id as seen")
	}
	// The recovered container's range is folded into the type-range table.
	if name, ok := findEnclosingSwiftType(ranges, 1); !ok || name != "Session" {
		t.Errorf("recovered range should enclose its body line; got %q ok=%v", name, ok)
	}
	// A file → Session defines edge ties the recovered node into the graph.
	var defines bool
	for _, edge := range res.Edges {
		if edge.Kind == graph.EdgeDefines && edge.From == "S.swift" && edge.To == "S.swift::Session" {
			defines = true
		}
	}
	if !defines {
		t.Error("recovery should emit a file -> Session defines edge")
	}
}

// TestSwiftRecoverIsInertWhenSeen verifies the recovery scan never emits a
// duplicate for a container the query already produced.
func TestSwiftRecoverIsInertWhenSeen(t *testing.T) {
	e := NewSwiftExtractor()
	res := &parser.ExtractionResult{}
	src := []byte("class Widget {\n    var x: Int\n}\n")
	seen := map[string]bool{"W.swift::Widget": true} // query already emitted it
	var ranges []swiftTypeRange
	e.recoverMissingTypeContainers(src, "W.swift", "W.swift", res, seen, &ranges)
	for _, n := range res.Nodes {
		if n.ID == "W.swift::Widget" {
			t.Fatalf("recovery emitted a duplicate Widget node for an already-seen id: %+v", n)
		}
	}
}

// TestSwiftRecoverEnumKind verifies a recovered enum is stamped Meta["kind"].
func TestSwiftRecoverEnumKind(t *testing.T) {
	e := NewSwiftExtractor()
	res := &parser.ExtractionResult{}
	src := []byte("public enum Direction {\n    case north\n}\n")
	seen := map[string]bool{}
	var ranges []swiftTypeRange
	e.recoverMissingTypeContainers(src, "D.swift", "D.swift", res, seen, &ranges)
	var dir *graph.Node
	for _, n := range res.Nodes {
		if n.ID == "D.swift::Direction" {
			dir = n
		}
	}
	if dir == nil {
		t.Fatal("recovery did not emit the missing Direction enum")
	}
	if dir.Meta["kind"] != "enum" {
		t.Errorf("recovered enum should carry Meta[kind]=enum; got %v", dir.Meta["kind"])
	}
}

// TestSwiftSessionTypeNodeEmitted is the empirical regression: the full
// Alamofire Session.swift must emit a Session *type* node (it previously
// vanished because the grammar mis-parsed the attributed class declaration),
// and the stranded stored property must not displace it.
func TestSwiftSessionTypeNodeEmitted(t *testing.T) {
	src, err := os.ReadFile("/Users/zzet/cg-bench/repos/alamofire/Source/Core/Session.swift")
	if err != nil {
		t.Skip("real Session.swift not available")
	}
	res, err := NewSwiftExtractor().Extract("Source/Core/Session.swift", src)
	if err != nil {
		t.Fatal(err)
	}
	typeCount := 0
	for _, n := range res.Nodes {
		if n.ID == "Source/Core/Session.swift::Session" && n.Kind == graph.KindType {
			typeCount++
		}
	}
	if typeCount != 1 {
		t.Fatalf("expected exactly one Session type node, got %d", typeCount)
	}
}
