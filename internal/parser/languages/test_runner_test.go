package languages

import "testing"

func TestDetectJSTSTestRunner_ImportSignals(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		imports []string
		want    string
	}{
		// Bun-test
		{"bun-test bare", "src/foo.test.ts", []string{"bun:test"}, "bun-test"},
		{"bun-test quoted", "src/foo.test.ts", []string{`"bun:test"`}, "bun-test"},

		// Vitest
		{"vitest bare", "src/foo.test.ts", []string{"vitest"}, "vitest"},
		{"vitest config subpath", "src/foo.test.ts", []string{"vitest/config"}, "vitest"},

		// Jest
		{"jest globals", "src/foo.test.ts", []string{"@jest/globals"}, "jest"},
		{"jest plain", "src/foo.test.ts", []string{"jest"}, "jest"},
		{"jest types", "src/foo.test.ts", []string{"@types/jest"}, "jest"},
		{"ts-jest", "src/foo.test.ts", []string{"ts-jest"}, "jest"},

		// Mocha
		{"mocha bare", "test/foo.js", []string{"mocha"}, "mocha"},
		{"mocha types", "test/foo.js", []string{"@types/mocha"}, "mocha"},

		// node:test
		{"node-test", "test/foo.mjs", []string{"node:test"}, "node-test"},

		// Playwright / Cypress
		{"playwright", "e2e/foo.spec.ts", []string{"@playwright/test"}, "playwright"},
		{"cypress", "cypress/e2e/foo.cy.js", []string{"cypress"}, "cypress"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectJSTSTestRunner(tc.path, []byte("// noop"), tc.imports)
			if got != tc.want {
				t.Fatalf("DetectJSTSTestRunner(%q) = %q, want %q", tc.imports, got, tc.want)
			}
		})
	}
}

func TestDetectJSTSTestRunner_MochaTDD_NoImport(t *testing.T) {
	// Mocha TDD interface uses `suite()` / `test()` globals — no
	// module import is needed. Detect by byte signature when the file
	// looks like a test file.
	src := []byte(`
suite("array", function () {
  test("push", function () { /* ... */ });
  suiteSetup(function () { /* ... */ });
});
`)
	got := DetectJSTSTestRunner("test/arrayTdd.js", src, nil)
	if got != "mocha" {
		t.Fatalf("Mocha TDD suite() detection: got %q, want %q", got, "mocha")
	}
}

func TestDetectJSTSTestRunner_NoSignal(t *testing.T) {
	// Plain BDD describe()/it() with no import is intentionally not
	// claimed by Mocha — Jest / Vitest / Bun-test share the globals,
	// so we can't classify without more context. Returns "".
	src := []byte(`describe("x", () => { it("works", () => {}); });`)
	got := DetectJSTSTestRunner("src/foo.test.ts", src, nil)
	if got != "" {
		t.Fatalf("ambiguous BDD globals must not classify; got %q", got)
	}
}

func TestDetectJSTSTestRunner_SuiteOutsideTestFile_Ignored(t *testing.T) {
	// `suite(` appearing in a production source file should NOT be
	// treated as Mocha — only when the file looks like a test file.
	src := []byte(`function suite() {} suite();`)
	got := DetectJSTSTestRunner("src/router.ts", src, nil)
	if got != "" {
		t.Fatalf("suite in non-test file must not classify; got %q", got)
	}
}

func TestDetectJSTSTestRunner_NotJSFile(t *testing.T) {
	// Empty / non-JS path with no imports → no classification.
	if got := DetectJSTSTestRunner("", nil, nil); got != "" {
		t.Fatalf("empty path: got %q, want \"\"", got)
	}
}

func TestLooksLikeJSTestFile(t *testing.T) {
	yes := []string{
		"src/foo.test.ts",
		"src/foo.spec.tsx",
		"web/app.test.js",
		"web/app.spec.jsx",
		"pkg/util.test.mjs",
		"pkg/util.spec.cjs",
		"a/b.test.mts",
		"a/b.spec.cts",
		"src/__tests__/x.ts",
		"tests/x.ts",
		"test/x.js",
		"spec/x.rb",
	}
	for _, p := range yes {
		if !looksLikeJSTestFile(p) {
			t.Errorf("looksLikeJSTestFile(%q) = false, want true", p)
		}
	}
	no := []string{
		"",
		"src/foo.ts",
		"src/router.ts",
		"src/test.go", // .go is handled elsewhere
		"src/x.testing.ts",
	}
	for _, p := range no {
		if looksLikeJSTestFile(p) {
			t.Errorf("looksLikeJSTestFile(%q) = true, want false", p)
		}
	}
}
