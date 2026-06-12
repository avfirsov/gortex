package astquery

import (
	"strings"

	"github.com/zzet/gortex/internal/parser"
)

// Bundled detectors. Each rule:
//   - Has a stable kebab-case Name (the agent-visible handle).
//   - Sets `match` as the row's anchor capture so engine.pickAnchor
//     lands on the meaningful span rather than the whole subtree.
//   - Defaults to ExcludeTests=true so test fixtures don't drown
//     real findings; the few rules that should also flag tests
//     opt out.
//
// Pattern style: every pattern is wrapped in `((…) @match (#…?))`
// when predicates apply to the rule as a whole. Capture names are
// short, lowercase identifiers documented at the rule.
//
// Adding a detector: write the pattern, register it from init(),
// add a golden test in detectors_test.go. Keep the count tight —
// ten high-signal rules age better than fifty noisy ones.

func init() {
	RegisterDetector(detectorErrorNotWrapped())
	RegisterDetector(detectorSQLStringConcat())
	RegisterDetector(detectorWeakCrypto())
	RegisterDetector(detectorPanicInLibrary())
	RegisterDetector(detectorGoroutineWithoutRecover())
	RegisterDetector(detectorHTTPClientNoTimeout())
	RegisterDetector(detectorHardcodedSecret())
	RegisterDetector(detectorEmptyCatch())
	RegisterDetector(detectorJavaStringEquality())
	RegisterDetector(detectorPythonMutableDefault())
	RegisterDetector(detectorRustUnwrap())
	RegisterDetector(detectorRustPanicMacro())
	RegisterDetector(detectorRustAssertMacro())
	RegisterDetector(detectorRustUnsafeBlock())
	RegisterDetector(detectorPythonAssert())
	RegisterDetector(detectorJSThrowInProd())
}

// UnsafePatternDetectors lists the detector names bundled by
// `analyze kind=unsafe_patterns`. The set is authoritative —
// `handleAnalyzeUnsafePatterns` iterates this slice to fan out the
// engine. Keeping the list here (next to the registrations) means a
// single edit adds a rule to both the bundle and the search_ast
// surface.
var UnsafePatternDetectors = []string{
	// Go — already in the panic-in-library detector.
	"panic-in-library",
	// Rust.
	"unsafe-rust-unwrap",
	"unsafe-rust-panic-macro",
	"unsafe-rust-assert-macro",
	"unsafe-rust-block",
	// Python.
	"unsafe-python-assert",
	// JavaScript / TypeScript.
	"unsafe-js-throw",
}

// 1. error-not-wrapped (Go) -------------------------------------------------
//
// Matches `if err != nil { return err }` (or any single-arg
// pass-through return) without a `fmt.Errorf(..., %w, err)` wrap.
// Captures @errvar from the condition and @retvar from the return,
// then asserts they're identical so we don't false-positive on
// unrelated err handling.
func detectorErrorNotWrapped() *Detector {
	return &Detector{
		Name:        "error-not-wrapped",
		Description: "Returning a Go error verbatim from `if err != nil` instead of wrapping with `fmt.Errorf(\"…: %w\", err)` — strips the call-site context that makes errors debuggable.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((if_statement
   condition: (binary_expression
     left: (identifier) @errvar
     operator: "!="
     right: (nil))
   consequence: (block
     (statement_list
       (return_statement
         (expression_list
           (identifier) @retvar))))) @match
 (#eq? @errvar @retvar))
`,
		},
	}
}

// 2. sql-string-concat (Go / Python / JS / TS / Ruby) -----------------------
//
// Flags a SQL-shaped call site that builds the query via string
// concatenation. The detector is conservative — it only fires on
// well-known method names (`Query`, `Exec`, `execute`, `query`,
// `find_by_sql`) so a generic `+` over strings doesn't spam the
// audit. Cross-language by definition.
func detectorSQLStringConcat() *Detector {
	return &Detector{
		Name:        "sql-string-concat",
		Description: "SQL-shaped database call whose query argument is built with string concatenation — strong indicator of SQL injection in any language.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (selector_expression
     field: (field_identifier) @fn)
   arguments: (argument_list
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(Query|QueryRow|Exec|QueryContext|ExecContext|QueryRowContext|Prepare|PrepareContext|Raw)$"))
`,
			"python": `
((call
   function: (attribute
     attribute: (identifier) @fn)
   arguments: (argument_list
     (binary_operator operator: "+") @concat)) @match
 (#match? @fn "^(execute|executemany|raw|fetch|fetchall|fetchone)$"))
`,
			"javascript": `
((call_expression
   function: (member_expression
     property: (property_identifier) @fn)
   arguments: (arguments
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(query|execute|exec|run|raw)$"))
`,
			"typescript": `
((call_expression
   function: (member_expression
     property: (property_identifier) @fn)
   arguments: (arguments
     (binary_expression operator: "+") @concat)) @match
 (#match? @fn "^(query|execute|exec|run|raw)$"))
`,
			"ruby": `
((call
   method: (identifier) @fn
   arguments: (argument_list
     (binary operator: "+") @concat)) @match
 (#match? @fn "^(execute|exec_query|find_by_sql|where|select_all)$"))
`,
		},
	}
}

// 3. weak-crypto (Go / Python) ---------------------------------------------
//
// Flags hashing or symmetric-cipher constructors known to be
// cryptographically weak: MD5, SHA-1, DES, RC4. Both for password
// hashing and for HMAC keys these are deprecated; the only
// legitimate use is checksumming non-security-relevant data.
func detectorWeakCrypto() *Detector {
	return &Detector{
		Name:        "weak-crypto",
		Description: "Use of MD5 / SHA-1 / DES / RC4 — cryptographically broken for any security-sensitive purpose. Use SHA-256+, AES-GCM, or ChaCha20-Poly1305 instead.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (selector_expression
     operand: (identifier) @pkg
     field: (field_identifier) @fn)) @match
 (#match? @pkg "^(md5|sha1|des|rc4)$")
 (#match? @fn "^(New|Sum|Sum256|NewCipher|NewTripleDESCipher)$"))
`,
			"python": `
((call
   function: (attribute
     object: (identifier) @lib
     attribute: (identifier) @fn)) @match
 (#eq? @lib "hashlib")
 (#match? @fn "^(md5|sha1|new)$"))
`,
		},
	}
}

// 4. panic-in-library (Go) -------------------------------------------------
//
// A direct `panic(...)` call. Excludes `_test.go` automatically; in
// tests panic is the right primitive. In library / production code
// panic should be reserved for "unreachable" invariants — return an
// error instead.
func detectorPanicInLibrary() *Detector {
	return &Detector{
		Name:        "panic-in-library",
		Description: "`panic(...)` call in non-test Go source. Library code should propagate errors; reserve panic for genuinely unreachable invariants.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((call_expression
   function: (identifier) @fn) @match
 (#eq? @fn "panic"))
`,
		},
		ExcludeTests: true,
	}
}

// 5. goroutine-without-recover (Go) ----------------------------------------
//
// A `go func() { … }()` whose body never calls `recover()`. A panic
// inside the goroutine's body crashes the process; the canonical
// fix is `defer func() { _ = recover() }()` at the top of the
// goroutine. Pure-AST predicates can't express "absence" of a node,
// so the post-filter reads the body text and looks for a recover
// call.
func detectorGoroutineWithoutRecover() *Detector {
	return &Detector{
		Name:        "goroutine-without-recover",
		Description: "`go func() { … }()` whose body never calls `recover()` — a panic anywhere in that goroutine crashes the whole process.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
(go_statement
  (call_expression
    function: (func_literal
      body: (block) @body))) @match
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			// Conservative containment check — false negatives
			// (recover() inside a string literal would suppress
			// the warning) are acceptable here; false positives
			// would erode trust.
			return !strings.Contains(body.Text, "recover()")
		},
	}
}

// 6. http-client-no-timeout (Go) -------------------------------------------
//
// `&http.Client{}` or `http.Client{}` literal that doesn't set
// `Timeout`. The default zero-value timeout means an upstream that
// never responds will wedge the goroutine forever — a classic
// production-grade outage trigger.
func detectorHTTPClientNoTimeout() *Detector {
	return &Detector{
		Name:        "http-client-no-timeout",
		Description: "`http.Client{}` literal without a `Timeout` field — defaults to no timeout, which lets a slow upstream wedge the goroutine indefinitely.",
		Severity:    "warning",
		Languages: map[string]string{
			"go": `
((composite_literal
   type: (qualified_type
     package: (package_identifier) @pkg
     name: (type_identifier) @typ)
   body: (literal_value) @body) @match
 (#eq? @pkg "http")
 (#eq? @typ "Client"))
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			return !strings.Contains(body.Text, "Timeout")
		},
	}
}

// 7. hardcoded-secret (Go / Python / JS / TS / Ruby) ------------------------
//
// Any assignment whose left-hand identifier name looks like a
// credential (password / secret / api_key / apiKey / token) and
// whose right-hand side is a string literal of meaningful length.
// The post-filter rejects placeholder strings (length < 12, or
// purely punctuation) so the detector doesn't spam every
// `password = ""` empty-default.
func detectorHardcodedSecret() *Detector {
	// (?i) makes the regex case-insensitive so apiKey, ApiKey,
	// APIKey, and api_key all match.
	const cred = "(?i)^(password|passwd|secret|api_?key|token|aws_?secret(_?key)?|access_?key|private_?key)$"
	return &Detector{
		Name:        "hardcoded-secret",
		Description: "Variable named like a credential (`password`, `secret`, `api_key`, `token`, …) assigned a non-trivial string literal. Move to env vars or a secret manager.",
		Severity:    "error",
		Languages: map[string]string{
			"go": `
((short_var_declaration
   left: (expression_list (identifier) @name)
   right: (expression_list (interpreted_string_literal) @val)) @match
 (#match? @name "` + cred + `"))
`,
			"python": `
((assignment
   left: (identifier) @name
   right: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"javascript": `
((variable_declarator
   name: (identifier) @name
   value: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"typescript": `
((variable_declarator
   name: (identifier) @name
   value: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
			"ruby": `
((assignment
   left: (identifier) @name
   right: (string) @val) @match
 (#match? @name "` + cred + `"))
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			val, ok := qr.Captures["val"]
			if !ok {
				return false
			}
			text := strings.Trim(val.Text, "\"'`")
			if len(text) < 12 {
				return false
			}
			// Reject obvious placeholders.
			lower := strings.ToLower(text)
			for _, marker := range []string{"todo", "fixme", "changeme", "placeholder", "example", "your-", "xxx"} {
				if strings.Contains(lower, marker) {
					return false
				}
			}
			return true
		},
	}
}

// 8. empty-catch (Java / JavaScript / TypeScript / Python) -----------------
//
// A try/except|catch whose body is empty (or only `pass` in
// Python). Silently swallowing an exception is a near-universal
// bug pattern — we want at least a log call or a comment that
// explains why.
func detectorEmptyCatch() *Detector {
	return &Detector{
		Name:        "empty-catch",
		Description: "Catch / except clause with an empty body — silently swallowing exceptions hides production bugs and breaks observability.",
		Severity:    "warning",
		Languages: map[string]string{
			"java": `
((catch_clause body: (block) @body) @match)
`,
			"javascript": `
((catch_clause body: (statement_block) @body) @match)
`,
			"typescript": `
((catch_clause body: (statement_block) @body) @match)
`,
			"python": `
((except_clause (block) @body) @match)
`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			body, ok := qr.Captures["body"]
			if !ok {
				return false
			}
			text := strings.TrimSpace(body.Text)
			text = strings.TrimPrefix(text, "{")
			text = strings.TrimSuffix(text, "}")
			text = strings.TrimSpace(text)
			// Strip trivial bodies — empty, pass, ellipsis,
			// comment-only.
			lines := strings.Split(text, "\n")
			meaningful := 0
			for _, ln := range lines {
				s := strings.TrimSpace(ln)
				if s == "" || s == "pass" || s == "..." {
					continue
				}
				if strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "*") {
					continue
				}
				meaningful++
			}
			return meaningful == 0
		},
	}
}

// 9. java-string-equality (Java) -------------------------------------------
//
// `s == "foo"` or `"foo" == s` — Java string comparison via `==`
// compares object identity, not content. The bug is famous and
// still common in code that came from C# / Python / JS.
func detectorJavaStringEquality() *Detector {
	return &Detector{
		Name:        "java-string-equality",
		Description: "Java string comparison via `==` (compares object identity, not content). Use `.equals()` or `Objects.equals()`.",
		Severity:    "warning",
		Languages: map[string]string{
			"java": `
[
  ((binary_expression
     left: (identifier)
     operator: "=="
     right: (string_literal)) @match)
  ((binary_expression
     left: (string_literal)
     operator: "=="
     right: (identifier)) @match)
]
`,
		},
	}
}

// 10. python-mutable-default-arg (Python) -----------------------------------
//
// `def foo(x=[])` — the list is created once at def time and
// shared across every call that omits x. One of the most-cited
// Python pitfalls; the safe form is `def foo(x=None): if x is
// None: x = []`.
func detectorPythonMutableDefault() *Detector {
	return &Detector{
		Name:        "python-mutable-default-arg",
		Description: "Python function default value is a mutable container (list / dict / set). The container is created once at def time and shared across every call — almost certainly a bug.",
		Severity:    "warning",
		Languages: map[string]string{
			"python": `
((default_parameter
   value: [(list) (dictionary) (set)]) @match)
`,
		},
	}
}

// 11. unsafe-rust-unwrap (Rust) ---------------------------------------------
//
// A `.unwrap()` / `.expect()` (or `_err` / `_or_else` variant) call on
// a `Result` / `Option`. Reaches for panic on the failure path —
// production code should propagate the error with `?` or handle the
// `None` / `Err` branch explicitly. Test code legitimately uses
// `.unwrap()` to assert preconditions, so the detector defaults to
// ExcludeTests.
func detectorRustUnwrap() *Detector {
	return &Detector{
		Name:        "unsafe-rust-unwrap",
		Description: "Rust `.unwrap()` / `.expect()` / `.unwrap_or_else()` / `.unwrap_err()` / `.expect_err()` in non-test source — panics on the failure path. Propagate with `?` or handle the `None` / `Err` branch explicitly.",
		Severity:    "warning",
		Languages: map[string]string{
			"rust": `
((call_expression
   function: (field_expression
     field: (field_identifier) @method)) @match
 (#match? @method "^(unwrap|expect|unwrap_or_else|unwrap_err|expect_err)$"))
`,
		},
		ExcludeTests: true,
	}
}

// 12. unsafe-rust-panic-macro (Rust) ----------------------------------------
//
// `panic!()`, `todo!()`, `unimplemented!()`, `unreachable!()`. All
// macro-invocation forms that abort on hit. `todo!` and
// `unimplemented!` are the strongest signal of incomplete code
// leaking into a non-test build.
func detectorRustPanicMacro() *Detector {
	return &Detector{
		Name:        "unsafe-rust-panic-macro",
		Description: "Rust `panic!` / `todo!` / `unimplemented!` / `unreachable!` macro invocation outside tests. `todo!` and `unimplemented!` mark incomplete code paths; `panic!` aborts the process on hit.",
		Severity:    "warning",
		Languages: map[string]string{
			"rust": `
((macro_invocation
   macro: (identifier) @name) @match
 (#match? @name "^(panic|todo|unimplemented|unreachable)$"))
`,
		},
		ExcludeTests: true,
	}
}

// 13. unsafe-rust-assert-macro (Rust) ---------------------------------------
//
// `assert!`, `assert_eq!`, `assert_ne!`, `debug_assert!`,
// `debug_assert_eq!`, `debug_assert_ne!`. `assert!` family panics
// in release builds; `debug_assert!` family is compiled out under
// `--release` — both are surprising to find in production code.
// Listed separately from `panic!` so an agent can keep `panic!`
// noise tight while still surfacing the `assert!` discussion.
func detectorRustAssertMacro() *Detector {
	return &Detector{
		Name:        "unsafe-rust-assert-macro",
		Description: "Rust `assert!` / `assert_eq!` / `assert_ne!` (and `debug_assert*` variants) outside tests. Plain `assert!` panics in release; `debug_assert!` is silently compiled out — both are usually a sign that an invariant should be a proper `Result` / typed error instead.",
		Severity:    "info",
		Languages: map[string]string{
			"rust": `
((macro_invocation
   macro: (identifier) @name) @match
 (#match? @name "^(assert|assert_eq|assert_ne|debug_assert|debug_assert_eq|debug_assert_ne)$"))
`,
		},
		ExcludeTests: true,
	}
}

// 14. unsafe-rust-block (Rust) ----------------------------------------------
//
// `unsafe { … }` block or an `unsafe fn` declaration. Every
// `unsafe` site is a hand-audit boundary; surfacing them lets a
// reviewer enumerate the full set without a manual grep that
// false-positives on `unsafe` substrings in comments / strings.
func detectorRustUnsafeBlock() *Detector {
	return &Detector{
		Name:        "unsafe-rust-block",
		Description: "Rust `unsafe { … }` block or `unsafe fn` declaration. Every `unsafe` site is a hand-audit boundary — soundness obligations cannot be checked by the compiler.",
		Severity:    "warning",
		Languages: map[string]string{
			"rust": `
[
  (unsafe_block) @match
  ((function_item
     (function_modifiers) @mods) @match
   (#match? @mods "unsafe"))
]
`,
		},
		ExcludeTests: true,
	}
}

// 15. unsafe-python-assert (Python) -----------------------------------------
//
// A Python `assert` statement in non-test code. Python's `-O` /
// `PYTHONOPTIMIZE` flag strips every `assert` at bytecode-compile
// time — so a production invariant guarded by `assert` silently
// disappears under optimised deployment. The fix is an explicit
// `if not cond: raise <ConcreteError>`.
func detectorPythonAssert() *Detector {
	return &Detector{
		Name:        "unsafe-python-assert",
		Description: "Python `assert` statement in non-test source. The `-O` / `PYTHONOPTIMIZE` flag strips every assert at bytecode-compile time, so production invariants guarded by assert silently disappear. Use `if not cond: raise <ConcreteError>` instead.",
		Severity:    "warning",
		Languages: map[string]string{
			"python": `
(assert_statement) @match
`,
		},
		ExcludeTests: true,
	}
}

// 16. unsafe-js-throw (JavaScript / TypeScript) -----------------------------
//
// `throw <expr>` in non-test source. Throws inside async / Promise
// chains skip every synchronous handler; throwing non-Error values
// breaks every consumer relying on `.message` / `.stack`. The
// detector flags every `throw_statement` and leaves the
// production-vs-error-handling judgement to the reviewer.
func detectorJSThrowInProd() *Detector {
	return &Detector{
		Name:        "unsafe-js-throw",
		Description: "JavaScript / TypeScript `throw` statement in non-test source. Throwing inside async / Promise chains skips every synchronous handler; throwing non-Error values breaks consumers relying on `.message` / `.stack`. Review every site.",
		Severity:    "info",
		Languages: map[string]string{
			"javascript": `
(throw_statement) @match
`,
			"typescript": `
(throw_statement) @match
`,
		},
		ExcludeTests: true,
	}
}
