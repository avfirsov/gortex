package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestSwiftExtractor_ClassWithMethods(t *testing.T) {
	src := []byte(`class Server {
    var port: Int

    func start() {
        print("starting")
    }

    func stop() {
        print("stopping")
    }
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("server.swift", src)
	require.NoError(t, err)

	// Class should be extracted as KindType.
	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Server", types[0].Name)

	// Methods inside the class.
	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	require.Len(t, methods, 2)
	names := []string{methods[0].Name, methods[1].Name}
	assert.Contains(t, names, "start")
	assert.Contains(t, names, "stop")

	// Methods should NOT appear as top-level functions.
	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	assert.Len(t, funcs, 0)

	// The stored property is a field member of the class.
	fields := nodesOfKind(result.Nodes, graph.KindField)
	require.Len(t, fields, 1)
	assert.Equal(t, "port", fields[0].Name)

	// EdgeMemberOf edges: two methods plus the property.
	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.Len(t, memberEdges, 3)
	for _, e := range memberEdges {
		assert.Equal(t, "server.swift::Server", e.To)
	}
}

func TestSwiftExtractor_Struct(t *testing.T) {
	src := []byte(`struct Config {
    var port: Int
    var host: String
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("config.swift", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Config", types[0].Name)
}

func TestSwiftExtractor_Protocol(t *testing.T) {
	src := []byte(`protocol Repository {
    func findById(id: String) -> User?
    func save(user: User)
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("store.swift", src)
	require.NoError(t, err)

	ifaces := nodesOfKind(result.Nodes, graph.KindInterface)
	require.Len(t, ifaces, 1)
	assert.Equal(t, "Repository", ifaces[0].Name)

	// Protocol methods in Meta.
	methods, ok := ifaces[0].Meta["methods"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"findById", "save"}, methods)
}

func TestSwiftExtractor_Imports(t *testing.T) {
	src := []byte(`import Foundation
import UIKit
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("main.swift", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	require.Len(t, imports, 2)
}

func TestSwiftExtractor_Enum(t *testing.T) {
	src := []byte(`enum Direction {
    case north
    case south
    case east
    case west
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("direction.swift", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Direction", types[0].Name)
	assert.Equal(t, "enum", types[0].Meta["kind"])

	// Each case becomes a KindVariable member with a member_of edge.
	caseNames := map[string]bool{}
	for _, n := range result.Nodes {
		if n.Kind == graph.KindVariable && n.Meta != nil && n.Meta["kind"] == "enum_case" {
			caseNames[n.Name] = true
		}
	}
	assert.Equal(t, map[string]bool{"north": true, "south": true, "east": true, "west": true}, caseNames)
}

func TestSwiftExtractor_EnumAssociatedValues(t *testing.T) {
	// Cases with associated values used to false-match label
	// identifiers (`x` in `case labeled(x: Int)`) — confirm only the
	// case name itself is captured.
	src := []byte(`enum Result {
    case ok
    case err(code: Int, message: String)
    case payload(Data)
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("result.swift", src)
	require.NoError(t, err)

	caseNames := map[string]bool{}
	for _, n := range result.Nodes {
		if n.Kind == graph.KindVariable && n.Meta != nil && n.Meta["kind"] == "enum_case" {
			caseNames[n.Name] = true
		}
	}
	assert.Equal(t, map[string]bool{"ok": true, "err": true, "payload": true}, caseNames)
}

func TestSwiftExtractor_FreeFunction(t *testing.T) {
	src := []byte(`func greet(name: String) -> String {
    return "Hello \(name)"
}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("greet.swift", src)
	require.NoError(t, err)

	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	require.Len(t, funcs, 1)
	assert.Equal(t, "greet", funcs[0].Name)
}

func TestSwiftExtractor_DocAndVisibility(t *testing.T) {
	src := []byte(`/// The greeter.
public class Greeter {
    /// Says hi.
    public func hello() {}

    private func secret() {}
}

/// A protocol.
public protocol Friendly {
    func wave()
}

func internalHelper() {}
`)
	e := NewSwiftExtractor()
	result, err := e.Extract("greeter.swift", src)
	require.NoError(t, err)

	byID := map[string]*graph.Node{}
	for _, n := range result.Nodes {
		byID[n.ID] = n
	}

	greeter := byID["greeter.swift::Greeter"]
	require.NotNil(t, greeter)
	if greeter.Meta["visibility"] != "public" {
		t.Fatalf("Greeter.vis = %q", greeter.Meta["visibility"])
	}
	if greeter.Meta["doc"] != "The greeter." {
		t.Fatalf("Greeter.doc = %q", greeter.Meta["doc"])
	}

	hello := byID["greeter.swift::Greeter.hello"]
	require.NotNil(t, hello)
	if hello.Meta["visibility"] != "public" {
		t.Fatalf("hello.vis = %q", hello.Meta["visibility"])
	}
	if hello.Meta["doc"] != "Says hi." {
		t.Fatalf("hello.doc = %q", hello.Meta["doc"])
	}

	secret := byID["greeter.swift::Greeter.secret"]
	require.NotNil(t, secret)
	if secret.Meta["visibility"] != "private" {
		t.Fatalf("secret.vis = %q", secret.Meta["visibility"])
	}

	friendly := byID["greeter.swift::Friendly"]
	require.NotNil(t, friendly)
	if friendly.Meta["visibility"] != "public" {
		t.Fatalf("Friendly.vis = %q", friendly.Meta["visibility"])
	}

	internalFn := byID["greeter.swift::internalHelper"]
	require.NotNil(t, internalFn)
	if internalFn.Meta["visibility"] != "internal" {
		t.Fatalf("internalHelper.vis = %q", internalFn.Meta["visibility"])
	}
}

func TestSwiftExtractor_FactoryChainReceiver(t *testing.T) {
	src := `struct Widget { func withX() -> Widget { return self } }
func builder() -> Widget { return Widget() }
func run() {
  builder().withX().build()
}
`
	res, err := NewSwiftExtractor().Extract("w.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var build *graph.Edge
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls && e.To == "unresolved::*.build" {
			build = e
		}
	}
	if build == nil {
		t.Fatal("build() call edge not found")
	}
	if build.Meta["receiver_type"] != "Widget" {
		t.Errorf("receiver_type = %v, want Widget (chain builder().withX() returns Widget)", build.Meta["receiver_type"])
	}
}
