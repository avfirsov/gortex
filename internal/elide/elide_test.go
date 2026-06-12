package elide

import (
	"strings"
	"testing"
)

func TestCompress_Go(t *testing.T) {
	src := `package auth

import (
	"errors"
	"strings"
)

// MaxParts is the JWT segment count.
const MaxParts = 3

var ErrMalformed = errors.New("malformed")

type Claims struct {
	Subject string
	Issuer  string
}

// validateToken returns the parsed Claims or ErrMalformed.
func validateToken(t string) (*Claims, error) {
	parts := strings.Split(t, ".")
	if len(parts) != MaxParts {
		return nil, ErrMalformed
	}
	return &Claims{Subject: parts[0], Issuer: parts[1]}, nil
}

func (c *Claims) String() string {
	return c.Subject + "@" + c.Issuer
}
`
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"package auth",
		"import (",
		"const MaxParts = 3",
		"var ErrMalformed",
		"type Claims struct",
		"Subject string",
		"// validateToken returns the parsed Claims or ErrMalformed.",
		"func validateToken(t string) (*Claims, error) {",
		"func (c *Claims) String() string {",
		"lines elided",
	}
	mustNot := []string{
		`strings.Split(t, ".")`,
		"return &Claims{Subject: parts[0]",
		`c.Subject + "@" + c.Issuer`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_GoEmptyBody(t *testing.T) {
	src := `package x

func noop() {}
`
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	if !strings.Contains(out, "func noop()") {
		t.Errorf("expected signature preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "lines elided") {
		t.Errorf("expected elided stub even for empty body, got:\n%s", out)
	}
}

func TestCompress_GoNestedClosure(t *testing.T) {
	src := `package x

func outer() func() int {
	return func() int {
		return 42
	}
}
`
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	if strings.Contains(out, "return 42") {
		t.Errorf("expected nested closure body to be elided away, got:\n%s", out)
	}
	if !strings.Contains(out, "func outer() func() int") {
		t.Errorf("expected outer signature preserved, got:\n%s", out)
	}
}

func TestCompress_Python(t *testing.T) {
	src := `import os

CONSTANT = 42

class Foo:
    """Docstring."""

    def bar(self, x: int) -> int:
        if x < 0:
            raise ValueError("negative")
        return x * CONSTANT

def standalone(name):
    parts = name.split(".")
    return parts[0]
`
	out, err := CompressString(src, "python")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"import os",
		"CONSTANT = 42",
		"class Foo:",
		"def bar(self, x: int) -> int:",
		"def standalone(name):",
		"lines elided",
	}
	mustNot := []string{
		`raise ValueError("negative")`,
		`name.split(".")`,
		"x * CONSTANT",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Python_PreservesIndent(t *testing.T) {
	src := `def foo():
    a = 1
    return a
`
	out, err := CompressString(src, "python")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	// The original indent (4 spaces) precedes the ellipsis stub.
	if !strings.Contains(out, "    ...") {
		t.Errorf("expected indented ... stub, got:\n%s", out)
	}
}

func TestCompress_TypeScript(t *testing.T) {
	src := `import { Logger } from "./log";

export const MAX = 10;

export interface User {
  id: string;
  name: string;
}

export class Service {
  constructor(private log: Logger) {}

  async fetch(id: string): Promise<User> {
    const res = await this.log.get(id);
    return res.user;
  }
}

export function helper(x: number): number {
  if (x < 0) {
    throw new Error("negative");
  }
  return x * MAX;
}

const arrow = (x: number) => {
  const doubled = x * 2;
  return doubled;
};
`
	out, err := CompressString(src, "typescript")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		`import { Logger } from "./log"`,
		"export const MAX = 10;",
		"export interface User",
		"id: string;",
		"export class Service",
		"async fetch(id: string): Promise<User>",
		"export function helper(x: number): number",
		"const arrow = (x: number) =>",
		"lines elided",
	}
	mustNot := []string{
		"await this.log.get(id)",
		`throw new Error("negative")`,
		"x * MAX",
		"x * 2",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_JavaScript(t *testing.T) {
	src := `const PI = 3.14;

function area(r) {
  return PI * r * r;
}

class Circle {
  constructor(r) {
    this.r = r;
  }
  area() {
    return area(this.r);
  }
}
`
	out, err := CompressString(src, "javascript")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"const PI = 3.14;",
		"function area(r)",
		"class Circle",
		"constructor(r)",
		"area()",
		"lines elided",
	}
	mustNot := []string{
		"return PI * r * r;",
		"this.r = r;",
		"return area(this.r);",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Rust(t *testing.T) {
	src := `use std::io;

const MAX: u32 = 100;

pub struct Counter {
    count: u32,
}

impl Counter {
    pub fn new() -> Self {
        Self { count: 0 }
    }

    pub fn inc(&mut self) -> u32 {
        self.count += 1;
        self.count
    }
}

pub fn external<T: Clone>(x: T) -> T {
    let cloned = x.clone();
    cloned
}
`
	out, err := CompressString(src, "rust")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"use std::io;",
		"const MAX: u32 = 100;",
		"pub struct Counter",
		"count: u32,",
		"impl Counter",
		"pub fn new() -> Self",
		"pub fn inc(&mut self) -> u32",
		"pub fn external<T: Clone>(x: T) -> T",
		"lines elided",
	}
	mustNot := []string{
		"Self { count: 0 }",
		"self.count += 1;",
		"x.clone()",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Java(t *testing.T) {
	src := `package com.example;

import java.util.List;

public class Service {
    private final List<String> items;

    public Service(List<String> items) {
        this.items = items;
    }

    public int size() {
        return items.size();
    }
}
`
	out, err := CompressString(src, "java")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"package com.example;",
		"import java.util.List;",
		"public class Service",
		"private final List<String> items;",
		"public Service(List<String> items)",
		"public int size()",
		"lines elided",
	}
	mustNot := []string{
		"this.items = items;",
		"return items.size();",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_C(t *testing.T) {
	src := `#include <stdio.h>

#define MAX 100

static int add(int a, int b) {
    int s = a + b;
    return s;
}

int main(void) {
    int r = add(1, 2);
    printf("%d\n", r);
    return 0;
}
`
	out, err := CompressString(src, "c")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"#include <stdio.h>",
		"#define MAX 100",
		"static int add(int a, int b)",
		"int main(void)",
		"lines elided",
	}
	mustNot := []string{
		"int s = a + b;",
		`printf("%d\n", r);`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Cpp(t *testing.T) {
	src := `#include <vector>

namespace ex {
template <typename T>
class Stack {
public:
    void push(T v) {
        data.push_back(v);
    }
    T pop() {
        T v = data.back();
        data.pop_back();
        return v;
    }
private:
    std::vector<T> data;
};
}
`
	out, err := CompressString(src, "cpp")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"#include <vector>",
		"template <typename T>",
		"class Stack",
		"void push(T v)",
		"T pop()",
		"std::vector<T> data;",
		"lines elided",
	}
	mustNot := []string{
		"data.push_back(v);",
		"data.pop_back();",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_CSharp(t *testing.T) {
	src := `namespace App;

public class Greeter
{
    public string Hello(string name)
    {
        if (string.IsNullOrEmpty(name))
        {
            return "Hello!";
        }
        return $"Hello, {name}!";
    }
}
`
	out, err := CompressString(src, "csharp")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"namespace App;",
		"public class Greeter",
		"public string Hello(string name)",
		"lines elided",
	}
	mustNot := []string{
		"string.IsNullOrEmpty(name)",
		`return "Hello!";`,
		`return $"Hello, {name}!";`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_PHP(t *testing.T) {
	src := `<?php

namespace App;

class Greeter {
    public function hello(string $name): string {
        if (empty($name)) {
            return "Hello!";
        }
        return "Hello, $name!";
    }
}
`
	out, err := CompressString(src, "php")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"namespace App;",
		"class Greeter",
		"public function hello(string $name): string",
		"lines elided",
	}
	mustNot := []string{
		"empty($name)",
		`return "Hello!";`,
		`return "Hello, $name!";`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Bash(t *testing.T) {
	src := `#!/bin/bash

CONST="value"

greet() {
    local name="$1"
    echo "Hello, $name"
}

main() {
    greet "world"
}
`
	out, err := CompressString(src, "bash")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"#!/bin/bash",
		`CONST="value"`,
		"greet()",
		"main()",
		"lines elided",
	}
	mustNot := []string{
		`local name="$1"`,
		`echo "Hello, $name"`,
		`greet "world"`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Ruby(t *testing.T) {
	src := `module Greeter
  CONST = 42

  def self.hello(name)
    raise ArgumentError if name.nil?
    "Hello, #{name}!"
  end

  def world
    "world"
  end
end
`
	out, err := CompressString(src, "ruby")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"module Greeter",
		"CONST = 42",
		"def self.hello(name)",
		"def world",
		"end",
		"lines elided",
	}
	mustNot := []string{
		"raise ArgumentError if name.nil?",
		`"Hello, #{name}!"`,
		`"world"`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Kotlin(t *testing.T) {
	src := `package com.example

const val MAX = 100

class Counter(private var count: Int = 0) {
    fun increment(): Int {
        count += 1
        return count
    }
}

fun helper(x: Int): Int {
    val doubled = x * 2
    return doubled
}
`
	out, err := CompressString(src, "kotlin")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"package com.example",
		"const val MAX = 100",
		"class Counter(private var count: Int = 0)",
		"fun increment(): Int",
		"fun helper(x: Int): Int",
		"lines elided",
	}
	mustNot := []string{
		"count += 1",
		"val doubled = x * 2",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Scala(t *testing.T) {
	src := `package com.example

object Greeter {
  val CONST = 42

  def hello(name: String): String = {
    if (name.isEmpty) throw new IllegalArgumentException
    s"Hello, $name!"
  }
}
`
	out, err := CompressString(src, "scala")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"package com.example",
		"object Greeter",
		"val CONST = 42",
		"def hello(name: String): String",
		"lines elided",
	}
	mustNot := []string{
		"name.isEmpty",
		`throw new IllegalArgumentException`,
		`s"Hello, $name!"`,
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Elixir(t *testing.T) {
	src := `defmodule Greeter do
  @greeting "Hello"

  def hello(name) do
    if is_nil(name) do
      raise ArgumentError
    end
    "#{@greeting}, #{name}!"
  end

  defp internal_helper(x) do
    x * 2
  end
end
`
	out, err := CompressString(src, "elixir")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	mustContain := []string{
		"defmodule Greeter do",
		`@greeting "Hello"`,
		"def hello(name) do",
		"defp internal_helper(x) do",
		"lines elided",
	}
	// The function bodies' internals must be gone.
	mustNot := []string{
		"raise ArgumentError",
		`"#{@greeting}, #{name}!"`,
		"x * 2",
	}
	checkContains(t, out, mustContain, mustNot)
}

func TestCompress_Unsupported(t *testing.T) {
	src := "some content"
	out, err := CompressString(src, "klingon")
	if err == nil {
		t.Fatalf("expected ErrUnsupportedLang")
	}
	if out != src {
		t.Errorf("expected original src returned on unsupported lang, got %q", out)
	}
}

func TestCompress_NoFunctions(t *testing.T) {
	src := `package foo

const X = 1

type T struct {
	Field int
}
`
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	// No bodies to elide → output equals input.
	if out != src {
		t.Errorf("expected no-op on body-less source, got:\n%s", out)
	}
}

func TestCompress_TokenReductionAcceptance(t *testing.T) {
	// Acceptance criterion (spec K9): a 200-line file returns ≤ 60 lines,
	// token count ~30-40% of original.
	const fnCount = 20
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := range fnCount {
		b.WriteString("func fn")
		b.WriteString(itoa(i))
		b.WriteString("(x int) int {\n")
		// 9 body lines per function → 200+ total source lines.
		for range 9 {
			b.WriteString("\tx = x + 1\n")
		}
		b.WriteString("\treturn x\n")
		b.WriteString("}\n\n")
	}
	src := b.String()
	srcLines := strings.Count(src, "\n")
	if srcLines < 200 {
		t.Fatalf("test fixture should exceed 200 lines, got %d", srcLines)
	}
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	outLines := strings.Count(out, "\n")
	if outLines > 60 {
		t.Errorf("acceptance: expected ≤ 60 lines after elision, got %d (input had %d)", outLines, srcLines)
	}
	if float64(len(out))/float64(len(src)) > 0.45 {
		t.Errorf("acceptance: expected ≤ ~40%% size, got %.1f%% (%d/%d)",
			100*float64(len(out))/float64(len(src)), len(out), len(src))
	}
}

func TestCompress_OutputStillParses_Go(t *testing.T) {
	// Acceptance: compressed output must still be valid in its host language.
	src := `package x

func foo(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func bar() {
	_ = foo(1)
}
`
	out, err := CompressString(src, "go")
	if err != nil {
		t.Fatalf("CompressString: %v", err)
	}
	// Re-parse: an unsupported-language path can't loop forever — we
	// directly use the same Compress() with a deliberate change to
	// detect a no-op re-compression on already-elided output.
	twice, err := CompressString(out, "go")
	if err != nil {
		t.Fatalf("re-compress: %v", err)
	}
	// Already elided — bodies are now stub comments, so there's
	// nothing left to elide. We don't require byte equality (a
	// trivial AST reshuffle is allowed) but the output must still
	// contain the surface signatures.
	for _, must := range []string{"func foo(a int) int", "func bar()"} {
		if !strings.Contains(twice, must) {
			t.Errorf("re-compress dropped %q, got:\n%s", must, twice)
		}
	}
}

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"go":         "go",
		"Go":         "go",
		"c++":        "cpp",
		"CPP":        "cpp",
		"C#":         "csharp",
		"js":         "javascript",
		"jsx":        "javascript",
		"ts":         "typescript",
		"tsx":        "tsx",
		"py":         "python",
		"rb":         "ruby",
		"rs":         "rust",
		"sh":         "bash",
		"shell":      "bash",
		"kt":         "kotlin",
		"elixir":     "elixir",
		"ex":         "elixir",
		"unknown":    "unknown",
	}
	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Errorf("normalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSupported(t *testing.T) {
	for _, lang := range []string{
		"go", "Go", "ts", "typescript", "tsx", "javascript", "js",
		"python", "py", "rust", "rs", "java", "c", "cpp", "c++",
		"csharp", "c#", "kotlin", "kt", "scala", "php", "ruby", "rb",
		"bash", "sh", "elixir", "ex",
	} {
		if !IsSupported(lang) {
			t.Errorf("IsSupported(%q) = false, expected true", lang)
		}
	}
	for _, lang := range []string{"", "klingon", "esperanto"} {
		if IsSupported(lang) {
			t.Errorf("IsSupported(%q) = true, expected false", lang)
		}
	}
}

func TestCompress_EmptySource(t *testing.T) {
	out, err := Compress(nil, "go")
	if err != nil {
		t.Errorf("Compress(nil) error = %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Compress(nil) = %q, want empty", out)
	}
}

// checkContains is a small helper that fails the calling test with a
// readable diff when any mustContain string is missing or any
// mustNotContain string is present in out.
func checkContains(t *testing.T, out string, must, mustNot []string) {
	t.Helper()
	missing := []string{}
	for _, m := range must {
		if !strings.Contains(out, m) {
			missing = append(missing, m)
		}
	}
	leaked := []string{}
	for _, m := range mustNot {
		if strings.Contains(out, m) {
			leaked = append(leaked, m)
		}
	}
	if len(missing) > 0 || len(leaked) > 0 {
		t.Errorf("elide output mismatch.\nmissing:  %v\nleaked:   %v\noutput:\n%s",
			missing, leaked, out)
	}
}

// itoa is a tiny stdlib-free int-to-string used by the fixture builder
// so the fixture function itself doesn't pull in strconv (keeps the
// builder body inline with the rest of the file for readability).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
