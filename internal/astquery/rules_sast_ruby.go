package astquery

// Ruby SAST starter pack. The existing sql-string-concat /
// hardcoded-secret detectors already cover Ruby; this file adds
// language-specific patterns.

func init() {
	registerRubyEval()
	registerRubyYAMLMarshal()
	registerRubyOpen3()
	registerRubyConstantize()
	registerRubyMassAssignment()
	registerRubyRailsSendFile()
	registerRubyRailsHTMLSafe()
	registerRubyDigestWeakHash()
	registerRubyOpenURIRedirect()
	registerRubyExecBacktick()
}

func registerRubyEval() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-eval-use",
		Description: "`eval(str)` / `instance_eval(str)` / `class_eval(str)` / `module_eval(str)` / `binding.eval(str)` — runs arbitrary Ruby. Any caller-controlled input is RCE.",
		Severity:    "error",
		CWE:         "CWE-95",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"injection", "code-injection"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn) @match (#match? @fn "^(eval|instance_eval|class_eval|module_eval)$"))`,
		},
	})
}

func registerRubyYAMLMarshal() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-yaml-load",
		Description: "`YAML.load(str)` / `Marshal.load(str)` — Ruby YAML / Marshal deserialise arbitrary classes, equivalent to Python `pickle.load`. RCE on untrusted input. Use `YAML.safe_load` / `JSON.parse`.",
		Severity:    "error",
		CWE:         "CWE-502",
		OWASP:       "A08:2021-Software and Data Integrity Failures",
		Tags:        []string{"deserialization"},
		Pat: map[string]string{
			"ruby": `((call receiver: (constant) @cls method: (identifier) @fn) @match
                        (#match? @cls "^(YAML|Marshal)$") (#match? @fn "^(load|load_file|restore)$"))`,
		},
	})
}

func registerRubyOpen3() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-open3-with-string",
		Description: "`Open3.capture3(cmd_string)` / `Open3.popen3(cmd_string)` — a single-string argument is shell-parsed. Pass an argv array (`Open3.capture3('ls', dir)`) to avoid injection.",
		Severity:    "warning",
		CWE:         "CWE-78",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"ruby": `((call receiver: (constant) @cls method: (identifier) @fn) @match
                        (#eq? @cls "Open3") (#match? @fn "^(capture3|capture2|popen3|popen2)$"))`,
		},
	})
}

func registerRubyExecBacktick() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-shell-backtick",
		Description: "`` `cmd #{var}` `` / `%x[cmd]` — shell-evaluated backticks. Any interpolation is command injection. Use `Open3.capture3` with an argv array.",
		Severity:    "error",
		CWE:         "CWE-78",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"ruby": `(subshell) @match`,
		},
	})
}

func registerRubyConstantize() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-constantize",
		Description: "`x.constantize` / `x.safe_constantize` with user input — turns an arbitrary string into a class reference. Combined with `.new` this can construct any object in the autoloader.",
		Severity:    "warning",
		CWE:         "CWE-470",
		Tags:        []string{"reflection"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn) @match (#match? @fn "^(constantize|safe_constantize)$"))`,
		},
	})
}

func registerRubyMassAssignment() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-mass-assignment-params-permit-all",
		Description: "`params.permit!` — strong-parameters mass-assignment guard disabled. Equivalent to `attr_accessible :all`. Restore an explicit permit list.",
		Severity:    "warning",
		CWE:         "CWE-915",
		Tags:        []string{"mass-assignment", "rails"},
		Pat: map[string]string{
			"ruby": `((call receiver: (identifier) @recv method: (identifier) @fn) @match
                        (#eq? @recv "params") (#eq? @fn "permit!"))`,
		},
	})
}

func registerRubyRailsSendFile() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-rails-send-file-user-input",
		Description: "`send_file(params[:path])` — path comes from request params. Any caller can read files outside the intended directory unless validated.",
		Severity:    "warning",
		CWE:         "CWE-22",
		Tags:        []string{"path-traversal", "rails"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn
                          arguments: (argument_list (element_reference object: (identifier) @recv))) @match
                        (#eq? @fn "send_file") (#eq? @recv "params"))`,
		},
	})
}

func registerRubyRailsHTMLSafe() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-html-safe",
		Description: "`.html_safe` / `raw(...)` — marks a string as already-HTML-escaped. With user input, this is XSS in any Rails view.",
		Severity:    "warning",
		CWE:         "CWE-79",
		Tags:        []string{"xss", "rails"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn) @match (#match? @fn "^(html_safe|raw)$"))`,
		},
	})
}

func registerRubyDigestWeakHash() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-digest-md5-sha1",
		Description: "`Digest::MD5` / `Digest::SHA1` — broken hashes for any security-sensitive use.",
		Severity:    "error",
		CWE:         "CWE-327",
		Tags:        []string{"crypto", "weak-hash"},
		Pat: map[string]string{
			"ruby": `((scope_resolution scope: (constant) @lib name: (constant) @alg) @match
                        (#eq? @lib "Digest") (#match? @alg "^(MD5|MD4|SHA1)$"))`,
		},
	})
}

func registerRubyOpenURIRedirect() {
	mustRegisterSAST(sastRule{
		Name:        "ruby-open-uri-with-userdata",
		Description: "`open(user_url)` (with `require 'open-uri'`) — `open` accepts file paths AND `http://` / `https://` URLs. User input invites both file disclosure and SSRF. Use `URI.parse(...).open` with an explicit scheme allow-list.",
		Severity:    "info",
		CWE:         "CWE-918",
		Tags:        []string{"ssrf", "audit-hook"},
		Pat: map[string]string{
			"ruby": `((call method: (identifier) @fn) @match (#eq? @fn "open"))`,
		},
	})
}
