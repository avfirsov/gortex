package languages

import (
	"path/filepath"
	"regexp"
	"strings"
)

// DetectJSTSTestRunner classifies the JavaScript / TypeScript test runner
// driving a file. It is meant to be called by the JS / TS extractors once
// per file, after imports and call names have been collected from the
// tree-sitter walk. Returned values:
//
//	"bun-test"   — Bun built-in runner; `import ... from "bun:test"`.
//	"vitest"     — Vitest;             `import ... from "vitest"`.
//	"playwright" — Playwright Test;    `import ... from "@playwright/test"`.
//	"cypress"    — Cypress;            `import ... from "cypress"`.
//	"node-test"  — node:test stdlib runner; `import ... from "node:test"`.
//	"jest"       — Jest;               `import ... from "@jest/globals"` or
//	                                   `jest`, `jest-mock`, `ts-jest`, etc.
//	"mocha"      — Mocha;              `import ... from "mocha"`, the TDD
//	                                   `suite()` / `test()` interface, or
//	                                   the BDD `describe` / `it` calls when
//	                                   no other runner imported.
//	""           — No runner signal; caller should not stamp.
//
// Detection is import-first (deterministic, low false-positive) with a
// narrow byte-level fallback for Mocha TDD (`suite(` / `suiteSetup(`),
// since Mocha's TDD interface uses runner-provided globals with no module
// import. Plain BDD globals (`describe` / `it`) are claimed by Mocha only
// when no stronger signal is present and the file name looks like a test
// file — Jest, Vitest and Bun-test all also use these globals, so we
// can't disambiguate without a config file.
func DetectJSTSTestRunner(filePath string, src []byte, importPaths []string) string {
	// Import-driven signals — each maps to a runner with no ambiguity.
	for _, raw := range importPaths {
		p := strings.Trim(raw, "\"'`")
		// Strip any subpath: "vitest/config" → "vitest".
		switch {
		case p == "bun:test":
			return "bun-test"
		case p == "vitest" || strings.HasPrefix(p, "vitest/"):
			return "vitest"
		case p == "@playwright/test" || strings.HasPrefix(p, "@playwright/test/"):
			return "playwright"
		case p == "cypress" || strings.HasPrefix(p, "cypress/"):
			return "cypress"
		case p == "node:test" || strings.HasPrefix(p, "node:test/"):
			return "node-test"
		case p == "@jest/globals" || strings.HasPrefix(p, "@jest/globals/"),
			p == "jest" || strings.HasPrefix(p, "jest/"),
			p == "jest-mock", p == "ts-jest", p == "babel-jest",
			p == "@types/jest":
			return "jest"
		case p == "mocha" || strings.HasPrefix(p, "mocha/"),
			p == "@types/mocha", p == "mochawesome":
			return "mocha"
		}
	}

	// No explicit import. Fall back to Mocha-TDD byte detection: the TDD
	// interface uses runner-injected globals `suite()` / `test()` /
	// `suiteSetup()` and is not picked up by Jest/Vitest/Bun-test
	// (which use describe/it/test). A top-level `suite(` is a strong
	// Mocha-TDD signal.
	if looksLikeJSTestFile(filePath) && reMochaTDD.Match(src) {
		return "mocha"
	}
	return ""
}

// reMochaTDD matches a top-of-line `suite(` or `suiteSetup(` /
// `suiteTeardown(` call — the Mocha TDD interface entry points. Mocha
// BDD's `describe` is not used here because Jest/Vitest/Bun-test share
// it and the regex would produce false positives.
var reMochaTDD = regexp.MustCompile(`(?m)^\s*(suite|suiteSetup|suiteTeardown)\s*\(`)

// looksLikeJSTestFile is a small, dependency-free copy of the JS / TS
// branches of indexer.IsTestFile. Inlined to avoid the parser → indexer
// dependency cycle.
func looksLikeJSTestFile(p string) bool {
	if p == "" {
		return false
	}
	slash := filepath.ToSlash(p)
	for _, marker := range []string{"/__tests__/", "/tests/", "/test/", "/spec/"} {
		if strings.Contains(slash, marker) {
			return true
		}
	}
	if strings.HasPrefix(slash, "tests/") || strings.HasPrefix(slash, "test/") || strings.HasPrefix(slash, "spec/") {
		return true
	}
	base := filepath.Base(slash)
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		stem := strings.TrimSuffix(base, ext)
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec")
	}
	return false
}
