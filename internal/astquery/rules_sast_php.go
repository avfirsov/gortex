package astquery

// PHP SAST starter pack.

func init() {
	registerPHPEval()
	registerPHPShellExec()
	registerPHPUnserialize()
	registerPHPSQLConcat()
	registerPHPWeakHash()
	registerPHPFileInclude()
	registerPHPEchoUserInput()
	registerPHPPregReplaceE()
	registerPHPMySQLDeprecated()
	registerPHPCURLNoVerify()
}

func registerPHPEval() {
	mustRegisterSAST(sastRule{
		Name:        "php-eval-use",
		Description: "`eval($str)` / `assert($str)` — executes arbitrary PHP. Trivial RCE with user input.",
		Severity:    "error",
		CWE:         "CWE-95",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"injection", "code-injection"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match (#match? @fn "^(eval|assert)$"))`,
		},
	})
}

func registerPHPShellExec() {
	mustRegisterSAST(sastRule{
		Name:        "php-shell-exec",
		Description: "`system()` / `exec()` / `passthru()` / `shell_exec()` / `popen()` / `` `cmd` `` — shell execution functions. Any user-controlled argument is command injection. Use `escapeshellarg` + an argv-style alternative.",
		Severity:    "error",
		CWE:         "CWE-78",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match
                        (#match? @fn "^(system|exec|passthru|shell_exec|popen|proc_open|pcntl_exec)$"))`,
		},
	})
}

func registerPHPUnserialize() {
	mustRegisterSAST(sastRule{
		Name:        "php-unserialize",
		Description: "`unserialize($x)` — magic methods on attacker-controlled classes are invoked. RCE primitive in any PHP framework. Use `json_decode` instead.",
		Severity:    "error",
		CWE:         "CWE-502",
		OWASP:       "A08:2021-Software and Data Integrity Failures",
		Tags:        []string{"deserialization"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match (#eq? @fn "unserialize"))`,
		},
	})
}

func registerPHPSQLConcat() {
	mustRegisterSAST(sastRule{
		Name:        "php-mysql-query-concat",
		Description: "`mysql_query`/`mysqli_query`/`pg_query`/`PDO::query` with a string literal containing `.` concatenation — SQL injection. Use parameterised queries.",
		Severity:    "error",
		CWE:         "CWE-89",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"sqli"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn
                        arguments: (arguments (argument (binary_expression operator: ".")))) @match
                      (#match? @fn "^(mysql_query|mysqli_query|pg_query|pg_send_query)$"))`,
		},
	})
}

func registerPHPWeakHash() {
	mustRegisterSAST(sastRule{
		Name:        "php-md5-sha1",
		Description: "`md5($x)` / `sha1($x)` — broken hashes. Use `password_hash($x, PASSWORD_DEFAULT)` for passwords and `hash('sha256', $x)` for fingerprinting.",
		Severity:    "error",
		CWE:         "CWE-327",
		Tags:        []string{"crypto", "weak-hash"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match (#match? @fn "^(md5|sha1)$"))`,
		},
	})
}

func registerPHPFileInclude() {
	mustRegisterSAST(sastRule{
		Name:        "php-file-include-with-variable",
		Description: "`include $f` / `require $f` / `include_once $f` / `require_once $f` with a variable — file-inclusion vulnerability. With user input + `allow_url_include=On` this is LFI/RFI / RCE.",
		Severity:    "warning",
		CWE:         "CWE-98",
		Tags:        []string{"file-inclusion"},
		Pat: map[string]string{
			"php": `(include_expression (variable_name) @arg) @match
                     (require_expression (variable_name) @arg) @match
                     (include_once_expression (variable_name) @arg) @match
                     (require_once_expression (variable_name) @arg) @match`,
		},
	})
}

func registerPHPEchoUserInput() {
	mustRegisterSAST(sastRule{
		Name:        "php-echo-user-input",
		Description: "`echo $_GET[...]` / `print $_POST[...]` / `echo $_REQUEST[...]` — reflected XSS unless `htmlspecialchars`-escaped. Use a templating engine that auto-escapes (Twig, Blade).",
		Severity:    "warning",
		CWE:         "CWE-79",
		Tags:        []string{"xss"},
		Pat: map[string]string{
			"php": `((echo_statement (subscript_expression
                          (variable_name) @src)) @match
                        (#match? @src "^_(GET|POST|REQUEST|COOKIE)$"))`,
		},
	})
}

func registerPHPPregReplaceE() {
	mustRegisterSAST(sastRule{
		Name:        "php-preg-replace-e-modifier",
		Description: "`preg_replace('/pattern/e', ...)` — the `e` modifier `eval`s the replacement. Deprecated in 5.5, removed in 7.0, but legacy projects still hit it. RCE primitive.",
		Severity:    "error",
		CWE:         "CWE-95",
		Tags:        []string{"injection", "deprecated"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn
                          arguments: (arguments (argument (string) @pat))) @match
                        (#eq? @fn "preg_replace") (#match? @pat "/[^\"']*/[a-zA-Z]*e[a-zA-Z]*[\"']"))`,
		},
	})
}

func registerPHPMySQLDeprecated() {
	mustRegisterSAST(sastRule{
		Name:        "php-mysql-extension-removed",
		Description: "`mysql_*` family — removed in PHP 7.0. Use `mysqli_*` or PDO with prepared statements.",
		Severity:    "info",
		CWE:         "CWE-1104",
		Tags:        []string{"deprecated"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn) @match (#match? @fn "^mysql_(connect|query|fetch_array|fetch_row|fetch_assoc|num_rows|result|select_db|close|escape_string|real_escape_string)$"))`,
		},
	})
}

func registerPHPCURLNoVerify() {
	mustRegisterSAST(sastRule{
		Name:        "php-curl-no-verify",
		Description: "`curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, false)` — disables TLS certificate validation. Any MITM intercepts the request.",
		Severity:    "error",
		CWE:         "CWE-295",
		Tags:        []string{"tls", "no-verify"},
		Pat: map[string]string{
			"php": `((function_call_expression function: (name) @fn
                        arguments: (arguments (argument) (argument (name) @opt) (argument))) @match
                      (#eq? @fn "curl_setopt") (#match? @opt "^(CURLOPT_SSL_VERIFYPEER|CURLOPT_SSL_VERIFYHOST)$"))`,
		},
	})
}
