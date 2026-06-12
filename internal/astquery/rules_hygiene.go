package astquery

// Cross-language hygiene rules. Lower severity than SAST proper —
// these are debugging-print / dev-debugger leftovers / dead asserts.
// Category is CategoryHygiene so an agent can run `analyze sast` and
// keep them out of the security bundle when they don't want noise.

func init() {
	registerHygieneJSConsoleLog()
	registerHygieneJSDebugger()
	registerHygienePyPrint()
	registerHygienePyPdbSetTrace()
	registerHygienePyBreakpoint()
	registerHygieneRubyByebug()
	registerHygieneFmtPrintln()
	registerHygienePhpVarDump()
	registerHygieneJavaStackTrace()
}

func registerHygieneJSConsoleLog() {
	pat := `((call_expression
              function: (member_expression object: (identifier) @obj property: (property_identifier) @fn)) @match
            (#eq? @obj "console") (#match? @fn "^(log|debug|info)$"))`
	mustRegisterSAST(sastRule{
		Name:        "hygiene-js-console-log",
		Description: "`console.log(...)` / `console.debug` / `console.info` in non-test source — leaks debug payloads to production. Use a structured logger.",
		Severity:    "info",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat:         map[string]string{"javascript": pat, "typescript": pat},
	})
}

func registerHygieneJSDebugger() {
	pat := `(debugger_statement) @match`
	mustRegisterSAST(sastRule{
		Name:        "hygiene-js-debugger",
		Description: "`debugger;` statement in non-test source — pauses execution when devtools are open. Never ship.",
		Severity:    "warning",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat:         map[string]string{"javascript": pat, "typescript": pat},
	})
}

func registerHygienePyPrint() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-py-print",
		Description: "`print(...)` in non-test source — debug leftover. Use a logger.",
		Severity:    "info",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"python": `((call function: (identifier) @fn) @match (#eq? @fn "print"))`,
		},
	})
}

func registerHygienePyPdbSetTrace() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-py-pdb-set-trace",
		Description: "`pdb.set_trace()` / `ipdb.set_trace()` / `pudb.set_trace()` — debugger entrypoint left in source. Crashes production on hit.",
		Severity:    "error",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @fn)) @match
                        (#match? @mod "^(pdb|ipdb|pudb|pdbpp)$") (#eq? @fn "set_trace"))`,
		},
	})
}

func registerHygienePyBreakpoint() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-py-breakpoint",
		Description: "`breakpoint()` — the 3.7+ shortcut for `pdb.set_trace()`. Crashes production on hit unless `PYTHONBREAKPOINT=0`.",
		Severity:    "warning",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"python": `((call function: (identifier) @fn) @match (#eq? @fn "breakpoint"))`,
		},
	})
}

func registerHygieneRubyByebug() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-ruby-byebug",
		Description: "`byebug` / `binding.pry` — debugger entrypoint. Crashes production on hit (or worse, leaks an interactive shell over stderr).",
		Severity:    "error",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn) @match (#match? @fn "^(byebug|pry)$"))
                     ((call receiver: (call method: (identifier) @b) method: (identifier) @fn) @match
                      (#eq? @b "binding") (#match? @fn "^(pry|irb)$"))`,
		},
	})
}

func registerHygieneFmtPrintln() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-go-fmt-println",
		Description: "`fmt.Println(...)` / `fmt.Printf(...)` in non-test Go source — debug leftover. Use a structured logger (`slog`, `zap`).",
		Severity:    "info",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"go": `((call_expression function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)) @match
                      (#eq? @pkg "fmt") (#match? @fn "^(Println|Printf|Print)$"))`,
		},
	})
}

func registerHygienePhpVarDump() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-php-var-dump",
		Description: "`var_dump($x)` / `print_r($x)` / `var_export($x)` — debug-dump functions. Leaks structure / credentials to the response in production.",
		Severity:    "warning",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match (#match? @fn "^(var_dump|print_r|var_export|debug_print_backtrace)$"))`,
		},
	})
}

func registerHygieneJavaStackTrace() {
	mustRegisterSAST(sastRule{
		Name:        "hygiene-java-printstacktrace",
		Description: "`e.printStackTrace()` in non-test Java source — writes to stderr only; production loggers miss the entry. Use a logger.",
		Severity:    "info",
		Category:    CategoryHygiene,
		Tags:        []string{"hygiene", "debug-leftover"},
		Pat: map[string]string{
			"java": `((method_invocation name: (identifier) @fn) @match (#eq? @fn "printStackTrace"))`,
		},
	})
}
