package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// refEdgeTo reports whether edges contains an edge of the given kind to
// unresolved::<name> carrying the given Meta["ref_context"]. A blank
// refContext matches any.
func refEdgeTo(edges []*graph.Edge, kind graph.EdgeKind, name, refContext string) bool {
	target := "unresolved::" + name
	for _, e := range edges {
		if e.Kind != kind || e.To != target {
			continue
		}
		if refContext == "" {
			return true
		}
		if rc, _ := e.Meta["ref_context"].(string); rc == refContext {
			return true
		}
	}
	return false
}

// refEdgeFrom is refEdgeTo with an additional From-owner assertion.
func refEdgeFrom(edges []*graph.Edge, kind graph.EdgeKind, from, name, refContext string) bool {
	target := "unresolved::" + name
	for _, e := range edges {
		if e.Kind != kind || e.To != target || e.From != from {
			continue
		}
		if refContext == "" {
			return true
		}
		if rc, _ := e.Meta["ref_context"].(string); rc == refContext {
			return true
		}
	}
	return false
}

// --- JSX component usage references ----------------------------------

func TestTSRef_JSXSelfClosingReferencesComponent(t *testing.T) {
	src := `function Shell() {
	return <App foo="bar" />;
}
`
	_, edges := runTSExtract(t, "src/shell.tsx", src)
	if !refEdgeFrom(edges, graph.EdgeReferences, "src/shell.tsx::Shell", "App", "jsx") {
		t.Errorf("expected EdgeReferences ref_context=jsx Shell -> unresolved::App; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
}

func TestTSRef_JSXChildrenReferencesComponent(t *testing.T) {
	src := `function Shell() {
	return <App>hello</App>;
}
`
	_, edges := runTSExtract(t, "src/shell.tsx", src)
	if !refEdgeTo(edges, graph.EdgeReferences, "App", "jsx") {
		t.Errorf("expected EdgeReferences ref_context=jsx -> unresolved::App from <App>...</App>; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
}

func TestTSRef_JSXFileScopeReferencesComponent(t *testing.T) {
	// File-scope JSX (not inside any function) attributes to the file
	// node — the old render pass only ran inside function bodies.
	src := `const tree = <App />;
`
	_, edges := runTSExtract(t, "src/tree.tsx", src)
	if !refEdgeFrom(edges, graph.EdgeReferences, "src/tree.tsx", "App", "jsx") {
		t.Errorf("expected file-scope EdgeReferences ref_context=jsx src/tree.tsx -> unresolved::App; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
}

func TestTSRef_JSXQualifiedComponent(t *testing.T) {
	src := `function Shell() {
	return <Foo.Bar />;
}
`
	_, edges := runTSExtract(t, "src/shell.tsx", src)
	if !refEdgeTo(edges, graph.EdgeReferences, "Foo.Bar", "jsx") {
		t.Errorf("expected EdgeReferences ref_context=jsx -> unresolved::Foo.Bar; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
}

func TestTSRef_JSXIntrinsicHTMLSkipped(t *testing.T) {
	// Zero false positives: lowercase intrinsic HTML elements must never
	// produce a usage reference.
	src := `function Shell() {
	return <div><span>hi</span><p /></div>;
}
`
	_, edges := runTSExtract(t, "src/shell.tsx", src)
	for _, e := range edgesByKind(edges, graph.EdgeReferences) {
		if rc, _ := e.Meta["ref_context"].(string); rc != "jsx" {
			continue
		}
		switch e.To {
		case "unresolved::div", "unresolved::span", "unresolved::p":
			t.Errorf("intrinsic HTML element produced a jsx reference: %s", e.To)
		}
	}
}

func TestTSRef_JSXAlsoKeepsRendersChild(t *testing.T) {
	// The usage reference is additive — the component-tree EdgeRendersChild
	// edge must still be emitted alongside it.
	src := `function Shell() {
	return <App />;
}
`
	_, edges := runTSExtract(t, "src/shell.tsx", src)
	if !refEdgeTo(edges, graph.EdgeReferences, "App", "jsx") {
		t.Errorf("expected jsx reference to App")
	}
	hasRender := false
	for _, e := range edgesByKind(edges, graph.EdgeRendersChild) {
		if e.To == "unresolved::App" {
			hasRender = true
		}
	}
	if !hasRender {
		t.Errorf("EdgeRendersChild -> unresolved::App must still be emitted")
	}
}

// --- type-only import references -------------------------------------

func TestTSRef_ImportTypeBlockReferencesType(t *testing.T) {
	src := `import type { ExcalidrawElement, AppState } from "./types";
`
	_, edges := runTSExtract(t, "src/a.ts", src)
	if !refEdgeFrom(edges, graph.EdgeReferences, "src/a.ts", "ExcalidrawElement", "import_type") {
		t.Errorf("expected import-type EdgeReferences -> unresolved::ExcalidrawElement; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
	if !refEdgeTo(edges, graph.EdgeReferences, "AppState", "import_type") {
		t.Errorf("expected import-type EdgeReferences -> unresolved::AppState")
	}
}

func TestTSRef_InlineTypeImportReferencesOnlyTypeBinding(t *testing.T) {
	// `import { type Foo, normal }` — only Foo is type-only; `normal` is a
	// value (already referenced by its call/read sites) and must NOT get
	// an import_type reference.
	src := `import { type Foo, normal } from "./mod";
`
	_, edges := runTSExtract(t, "src/a.ts", src)
	if !refEdgeTo(edges, graph.EdgeReferences, "Foo", "import_type") {
		t.Errorf("expected import-type EdgeReferences -> unresolved::Foo; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
	if refEdgeTo(edges, graph.EdgeReferences, "normal", "import_type") {
		t.Errorf("value import `normal` must not produce an import_type reference")
	}
}

func TestTSRef_PlainValueImportNotReferenced(t *testing.T) {
	// Zero false positives: a non-type import emits no import_type ref.
	src := `import { foo } from "./mod";
`
	_, edges := runTSExtract(t, "src/a.ts", src)
	if refEdgeTo(edges, graph.EdgeReferences, "foo", "import_type") {
		t.Errorf("plain value import must not produce an import_type reference")
	}
}

// --- type-only re-export references ----------------------------------

func TestTSRef_ExportTypeReexportReferencesType(t *testing.T) {
	src := `export type { ExcalidrawElement, AppState } from "./types";
`
	_, edges := runTSExtract(t, "src/index.ts", src)
	if !refEdgeFrom(edges, graph.EdgeReferences, "src/index.ts", "ExcalidrawElement", "export_type") {
		t.Errorf("expected export-type EdgeReferences -> unresolved::ExcalidrawElement; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
	if !refEdgeTo(edges, graph.EdgeReferences, "AppState", "export_type") {
		t.Errorf("expected export-type EdgeReferences -> unresolved::AppState")
	}
}

func TestTSRef_InlineTypeReexportReferencesOnlyTypeBinding(t *testing.T) {
	src := `export { type Foo, normal } from "./mod";
`
	_, edges := runTSExtract(t, "src/index.ts", src)
	if !refEdgeTo(edges, graph.EdgeReferences, "Foo", "export_type") {
		t.Errorf("expected export-type EdgeReferences -> unresolved::Foo; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeReferences)))
	}
	if refEdgeTo(edges, graph.EdgeReferences, "normal", "export_type") {
		t.Errorf("value re-export `normal` must not produce an export_type reference")
	}
}

// --- class / interface heritage -------------------------------------

func TestTSRef_ClassExtendsImplements(t *testing.T) {
	src := `class App extends Component implements IThing, IOther {}
`
	_, edges := runTSExtract(t, "src/app.ts", src)
	if !refEdgeFrom(edges, graph.EdgeExtends, "src/app.ts::App", "Component", "") {
		t.Errorf("expected EdgeExtends App -> unresolved::Component; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeExtends)))
	}
	if !refEdgeFrom(edges, graph.EdgeImplements, "src/app.ts::App", "IThing", "") {
		t.Errorf("expected EdgeImplements App -> unresolved::IThing; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeImplements)))
	}
	if !refEdgeTo(edges, graph.EdgeImplements, "IOther", "") {
		t.Errorf("expected EdgeImplements -> unresolved::IOther")
	}
}

func TestTSRef_ClassExtendsMemberExpressionWithTypeArgs(t *testing.T) {
	// `extends React.Component<Props, State>` — base name is the last
	// member segment; type args surface as generic-arg references.
	src := `class App extends React.Component<Props, State> {}
`
	_, edges := runTSExtract(t, "src/app.tsx", src)
	if !refEdgeFrom(edges, graph.EdgeExtends, "src/app.tsx::App", "Component", "") {
		t.Errorf("expected EdgeExtends App -> unresolved::Component from member-expression base; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeExtends)))
	}
	if !refEdgeTo(edges, graph.EdgeReferences, "Props", "generic_arg") {
		t.Errorf("expected generic-arg EdgeReferences -> unresolved::Props")
	}
	if !refEdgeTo(edges, graph.EdgeReferences, "State", "generic_arg") {
		t.Errorf("expected generic-arg EdgeReferences -> unresolved::State")
	}
}

func TestTSRef_InterfaceExtends(t *testing.T) {
	src := `interface Y extends Z, W<Foo> {}
`
	_, edges := runTSExtract(t, "src/y.ts", src)
	if !refEdgeFrom(edges, graph.EdgeExtends, "src/y.ts::Y", "Z", "") {
		t.Errorf("expected EdgeExtends Y -> unresolved::Z; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeExtends)))
	}
	if !refEdgeTo(edges, graph.EdgeExtends, "W", "") {
		t.Errorf("expected EdgeExtends Y -> unresolved::W")
	}
	if !refEdgeTo(edges, graph.EdgeReferences, "Foo", "generic_arg") {
		t.Errorf("expected generic-arg EdgeReferences -> unresolved::Foo from interface base type arg")
	}
}

// --- indexed-access (lookup) types -----------------------------------

func TestTSRef_IndexedAccessTypeRefs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`ExcalidrawElement["type"]`, []string{"ExcalidrawElement"}},
		{`Foo[Key]`, []string{"Foo", "Key"}},
		{`Config["nested"]["deep"]`, []string{"Config"}},
		{`number["x"]`, nil},       // primitive object type dropped
		{`Foo[]`, []string{"Foo"}}, // array suffix still works, not a lookup
	}
	for _, c := range cases {
		got := tsTypeRefs(c.in)
		if !sliceEq(got, c.want) {
			t.Errorf("tsTypeRefs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTSRef_IndexedAccessAliasBody(t *testing.T) {
	src := `type Kind = ExcalidrawElement["type"];
`
	_, edges := runTSExtract(t, "src/kind.ts", src)
	if !typedAsEdgeTo(edges, "ExcalidrawElement", "type_annotation") {
		t.Errorf("expected EdgeTypedAs -> unresolved::ExcalidrawElement from indexed-access alias body; got %v",
			edgeTargets(edgesByKind(edges, graph.EdgeTypedAs)))
	}
}

// --- as-const zero-FP guard -----------------------------------------

func TestTSRef_AsConstEmitsNoBogusRef(t *testing.T) {
	// `x as const` must not emit a reference to a phantom `const` type.
	src := `function f() {
	const c = [1, 2] as const;
	return c;
}
`
	_, edges := runTSExtract(t, "src/c.ts", src)
	for _, e := range edges {
		if e.To == "unresolved::const" {
			t.Errorf("`as const` must not reference a `const` type; got edge kind=%s", e.Kind)
		}
	}
}
