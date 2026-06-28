package tstypes

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// A primary-constructor `val` parameter is both a constructor parameter
// and a class property, so a call through it resolves:
// `class C(val dep: Foo) { fun f() { dep.bar() } }` → dep.bar() lands on
// Foo::bar.
func TestKotlin_PrimaryCtorFieldResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"Foo.kt": `class Foo {
    fun bar() {}
}
`,
		"App.kt": `class C(val dep: Foo) {
    fun f() {
        dep.bar()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	res, err := p.Enrich(g, dir)
	if err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("primary-ctor field call %s -> %s not resolved; edges: %v", caller.ID, target.ID, g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "kotlin-types")
	if res.EdgesConfirmed+res.EdgesAdded == 0 {
		t.Errorf("result reported no edge work: %+v", res)
	}
}

// A local bound from a `Foo()` constructor call (Kotlin has no `new`)
// propagates its type to a later call: `val x = Foo(); x.bar()` → x.bar()
// resolves to Foo::bar.
func TestKotlin_ConstructorInferenceResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"Foo.kt": `class Foo {
    fun bar() {}
}
`,
		"App.kt": `class App {
    fun main() {
        val x = Foo()
        x.bar()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("constructor-inferred call not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "kotlin-types")
}

// `(Foo()).bar()` — a constructor call standing in receiver position —
// types its receiver directly from the construction.
func TestKotlin_ConstructorReceiverChainResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"Foo.kt": `class Foo {
    fun bar() {}
}
`,
		"App.kt": `class App {
    fun main() {
        Foo().bar()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	if callEdgeTo(g, caller.ID, target.ID) == nil {
		t.Fatalf("constructor-receiver chain not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
}

// A declared parameter type grounds its receiver, and a `this.field`
// access resolves through the declared property type.
func TestKotlin_ParamAndThisFieldReceivers(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"Foo.kt": `class Foo {
    fun run() {}
}
`,
		"App.kt": `class App {
    private val worker: Foo = makeFoo()

    fun direct(s: Foo) {
        s.run()
    }

    fun helper() {
        this.worker.run()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	direct := nodeByNameKind(t, g, "direct", graph.KindMethod)
	helper := nodeByNameKind(t, g, "helper", graph.KindMethod)
	run := nodeByNameKind(t, g, "run", graph.KindMethod)
	if callEdgeTo(g, direct.ID, run.ID) == nil {
		t.Fatalf("typed-param s.run() not resolved; edges: %v", g.GetOutEdges(direct.ID))
	}
	if callEdgeTo(g, helper.ID, run.ID) == nil {
		t.Fatalf("this.worker.run() not resolved through field type; edges: %v", g.GetOutEdges(helper.ID))
	}
}

// `class C : B(), I` synthesizes the inheritance edges (extends the base
// class, implements the interface), and a call to an inherited base-class
// method resolves through the extends climb.
func TestKotlin_ExtendsImplementsAndInheritedCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"B.kt": `open class B {
    fun run() {}
}
`,
		"I.kt": `interface I {
    fun greet()
}
`,
		"C.kt": `class C : B(), I {
    override fun greet() {}

    fun go(c: C) {
        c.run()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	c := nodeByNameKind(t, g, "C", graph.KindType)
	b := nodeByNameKind(t, g, "B", graph.KindType)
	iface := nodeByNameKind(t, g, "I", graph.KindInterface)

	ee := edgeBetween(g, c.ID, graph.EdgeExtends, b.ID)
	if ee == nil {
		t.Fatalf("extends edge C -> B missing; edges: %v", g.GetOutEdges(c.ID))
	}
	assertASTProvenance(t, ee, "kotlin-types")

	ie := edgeBetween(g, c.ID, graph.EdgeImplements, iface.ID)
	if ie == nil {
		t.Fatalf("implements edge C -> I missing; edges: %v", g.GetOutEdges(c.ID))
	}
	assertASTProvenance(t, ie, "kotlin-types")

	goMethod := nodeByNameKind(t, g, "go", graph.KindMethod)
	run := nodeByNameKind(t, g, "run", graph.KindMethod)
	if callEdgeTo(g, goMethod.ID, run.ID) == nil {
		t.Fatalf("inherited method call did not resolve through extends; edges: %v", g.GetOutEdges(goMethod.ID))
	}
}

// An ambiguous overload (two same-named methods, no way to choose) is
// skipped rather than guessed.
func TestKotlin_AmbiguousOverloadSkipped(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"K.kt": `class K {
    fun bar() {}
    fun bar(n: Int) {}
}
`,
		"App.kt": `class App {
    fun f(k: K) {
        k.bar()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	assertUntouched(t, g, caller.ID, "bar", "kotlin-types")
}

// EnrichFile resolves only the named file's calls, leaving others alone.
func TestKotlin_EnrichFileScopesToOneFile(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"Foo.kt": `class Foo {
    fun bar() {}
    fun baz() {}
}
`,
		"App.kt": `class App {
    fun main(x: Foo) {
        x.bar()
    }
}
`,
		"Other.kt": `class Other {
    fun go(x: Foo) {
        x.baz()
    }
}
`,
	})
	p := NewProvider(KotlinSpec(), zap.NewNop())
	if _, err := p.EnrichFile(g, dir, "App.kt"); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	bar := nodeByNameKind(t, g, "bar", graph.KindMethod)
	if callEdgeTo(g, caller.ID, bar.ID) == nil {
		t.Fatalf("EnrichFile did not resolve the target file's call")
	}
	other := nodeByNameKind(t, g, "go", graph.KindMethod)
	assertUntouched(t, g, other.ID, "baz", "kotlin-types")
}
