package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the structural `type_flavor` Meta key onto type nodes
// for the mechanical-stamp languages, and assert that the pre-existing
// legacy markers (kind=enum, …) survive beside it (dual-write).

func TestTypeFlavor_Cpp(t *testing.T) {
	src := []byte(`class Foo {};
struct Bar { int x; };
enum Baz { A, B };
`)
	res, err := NewCppExtractor().Extract("a.cpp", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "Foo").Meta["type_flavor"])
	assert.Equal(t, "struct", nodeByName(res.Nodes, "Bar").Meta["type_flavor"])
	assert.Equal(t, "enum", nodeByName(res.Nodes, "Baz").Meta["type_flavor"])
}

func TestTypeFlavor_C(t *testing.T) {
	src := []byte(`struct Point { int x; };
enum Color { RED, GREEN };
typedef int MyInt;
`)
	res, err := NewCExtractor().Extract("a.c", src)
	require.NoError(t, err)
	assert.Equal(t, "struct", nodeByName(res.Nodes, "Point").Meta["type_flavor"])
	assert.Equal(t, "enum", nodeByName(res.Nodes, "Color").Meta["type_flavor"])
	assert.Equal(t, "typedef", nodeByName(res.Nodes, "MyInt").Meta["type_flavor"])
}

func TestTypeFlavor_Java(t *testing.T) {
	src := []byte(`class Foo {}
interface Bar {}
enum Baz { A, B }
`)
	res, err := NewJavaExtractor().Extract("A.java", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "Foo").Meta["type_flavor"])
	assert.Equal(t, "interface", nodeByName(res.Nodes, "Bar").Meta["type_flavor"])
	baz := nodeByName(res.Nodes, "Baz")
	assert.Equal(t, "enum", baz.Meta["type_flavor"])
	assert.Equal(t, "enum", baz.Meta["kind"]) // dual-write

	anonSrc := []byte(`class Host {
    Runnable r = new Runnable() { public void run() {} };
}
`)
	anonRes, err := NewJavaExtractor().Extract("Host.java", anonSrc)
	require.NoError(t, err)
	anon, _ := anonTypeAndExtends(t, anonRes)
	require.NotNil(t, anon)
	assert.Equal(t, "anonymous_class", anon.Meta["type_flavor"])
	assert.Equal(t, true, anon.Meta["anonymous"]) // dual-write
}

func TestTypeFlavor_Rust(t *testing.T) {
	src := []byte(`struct S { x: i32 }
enum E { A, B }
trait T {}
`)
	res, err := NewRustExtractor().Extract("a.rs", src)
	require.NoError(t, err)
	assert.Equal(t, "struct", nodeByName(res.Nodes, "S").Meta["type_flavor"])
	e := nodeByName(res.Nodes, "E")
	assert.Equal(t, "enum", e.Meta["type_flavor"])
	assert.Equal(t, "enum", e.Meta["kind"]) // dual-write
	assert.Equal(t, "trait", nodeByName(res.Nodes, "T").Meta["type_flavor"])
}

func TestTypeFlavor_TypeScript(t *testing.T) {
	src := []byte(`class C {}
interface I {}
type A = string;
enum E { X, Y }
`)
	res, err := NewTypeScriptExtractor().Extract("a.ts", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "C").Meta["type_flavor"])
	assert.Equal(t, "interface", nodeByName(res.Nodes, "I").Meta["type_flavor"])
	assert.Equal(t, "type_alias", nodeByName(res.Nodes, "A").Meta["type_flavor"])
	e := nodeByName(res.Nodes, "E")
	assert.Equal(t, "enum", e.Meta["type_flavor"])
	assert.Equal(t, "enum", e.Meta["kind"]) // dual-write
}

func TestTypeFlavor_JavaScript(t *testing.T) {
	src := []byte(`class Widget {}
`)
	res, err := NewJavaScriptExtractor().Extract("a.js", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "Widget").Meta["type_flavor"])
}

func TestTypeFlavor_Python(t *testing.T) {
	src := []byte(`class Service:
    pass
`)
	res, err := NewPythonExtractor().Extract("a.py", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "Service").Meta["type_flavor"])
}

func TestTypeFlavor_Ruby(t *testing.T) {
	src := []byte(`class Service
end
`)
	res, err := NewRubyExtractor().Extract("a.rb", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "Service").Meta["type_flavor"])
}

func TestTypeFlavor_Kotlin(t *testing.T) {
	src := []byte(`class C
object O
interface I
enum class E { A, B }
`)
	res, err := NewKotlinExtractor().Extract("a.kt", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "C").Meta["type_flavor"])
	assert.Equal(t, "object", nodeByName(res.Nodes, "O").Meta["type_flavor"])
	assert.Equal(t, "interface", nodeByName(res.Nodes, "I").Meta["type_flavor"])
	e := nodeByName(res.Nodes, "E")
	assert.Equal(t, "enum", e.Meta["type_flavor"])
	assert.Equal(t, "enum", e.Meta["kind"]) // dual-write
}

func TestTypeFlavor_PHP(t *testing.T) {
	src := []byte(`<?php
class C {}
interface I {}
trait T {}
enum E {}
`)
	res, err := NewPHPExtractor().Extract("a.php", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "C").Meta["type_flavor"])
	assert.Equal(t, "interface", nodeByName(res.Nodes, "I").Meta["type_flavor"])
	tr := nodeByName(res.Nodes, "T")
	assert.Equal(t, "trait", tr.Meta["type_flavor"])
	assert.Equal(t, "trait", tr.Meta["kind"]) // dual-write
	e := nodeByName(res.Nodes, "E")
	assert.Equal(t, "enum", e.Meta["type_flavor"])
	assert.Equal(t, "enum", e.Meta["kind"]) // dual-write
}

func TestTypeFlavor_Scala(t *testing.T) {
	src := []byte(`class C
object O
trait T
enum E { case A }
`)
	res, err := NewScalaExtractor().Extract("a.scala", src)
	require.NoError(t, err)
	assert.Equal(t, "class", nodeByName(res.Nodes, "C").Meta["type_flavor"])
	assert.Equal(t, "object", nodeByName(res.Nodes, "O").Meta["type_flavor"])
	assert.Equal(t, "trait", nodeByName(res.Nodes, "T").Meta["type_flavor"])
	e := nodeByName(res.Nodes, "E")
	assert.Equal(t, "enum", e.Meta["type_flavor"])
	assert.Equal(t, "enum", e.Meta["kind"]) // dual-write
}
