package astquery

// Go SAST starter pack. Cross-language rules that already cover Go
// (sql-string-concat, weak-crypto, hardcoded-secret, http-client-no-timeout,
// goroutine-without-recover, panic-in-library) live in detectors.go;
// this file adds Go-specific patterns absent from the legacy list.

func init() {
	registerGoCommandExecution()
	registerGoTLSInsecure()
	registerGoTemplateUnsafe()
	registerGoJWTNone()
	registerGoFilepathTraversal()
	registerGoBcryptCost()
	registerGoMathRandForCrypto()
	registerGoCSRFDisable()
	registerGoSSRFAuditHook()
	registerGoCORSAllowAll()
	registerGoXMLDecoderNoEntity()
	registerGoSQLOpenWithHardcodedDSN()
	registerGoOSChmodWorldWritable()
	registerGoEnvGetenvSensitive()
	registerGoExpvarPublishAuditHook()
}

func registerGoSQLOpenWithHardcodedDSN() {
	mustRegisterSAST(sastRule{
		Name:        "go-sql-open-hardcoded-dsn",
		Description: "`sql.Open(driver, \"...credentials...\")` with a DSN string literal — embeds credentials in source. Read from env / a config struct populated by env.",
		Severity:    "warning",
		CWE:         "CWE-798",
		Tags:        []string{"secrets", "dsn"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                        arguments: (argument_list (_) (interpreted_string_literal) @dsn)) @match
                      (#eq? @pkg "sql") (#eq? @fn "Open")
                      (#match? @dsn ":[^/@\"]+@"))`,
		},
	})
}

func registerGoOSChmodWorldWritable() {
	mustRegisterSAST(sastRule{
		Name:        "go-os-chmod-world-writable",
		Description: "`os.Chmod(path, 0666)` / `0777` / `0o666` / `0o777` — world-writable file permissions. Almost always a privilege-management bug.",
		Severity:    "warning",
		CWE:         "CWE-732",
		Tags:        []string{"permissions"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                        arguments: (argument_list (_) (int_literal) @mode)) @match
                      (#eq? @pkg "os") (#match? @fn "^(Chmod|MkdirAll|Mkdir)$")
                      (#match? @mode "^0o?(666|667|676|677|766|767|776|777)$"))`,
		},
	})
}

func registerGoEnvGetenvSensitive() {
	mustRegisterSAST(sastRule{
		Name:        "go-getenv-sensitive-fallback",
		Description: "`os.Getenv(\"...SECRET...\")` followed by a string-literal fallback — silent failure mode that ships a known weak default. Use `os.LookupEnv` and fail fast.",
		Severity:    "info",
		CWE:         "CWE-1188",
		Tags:        []string{"secrets", "config"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                        arguments: (argument_list (interpreted_string_literal) @key)) @match
                      (#eq? @pkg "os") (#eq? @fn "Getenv")
                      (#match? @key "(?i)(SECRET|PASSWORD|TOKEN|KEY)"))`,
		},
	})
}

func registerGoExpvarPublishAuditHook() {
	mustRegisterSAST(sastRule{
		Name:        "go-expvar-publish-audit",
		Description: "`expvar.Publish(...)` — registers a public variable at `/debug/vars`. Audit hook: confirm the variable doesn't carry credentials / PII and that `/debug/vars` isn't exposed publicly.",
		Severity:    "info",
		CWE:         "CWE-200",
		Tags:        []string{"audit-hook", "info-leak"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)) @match
                      (#eq? @pkg "expvar") (#match? @fn "^(Publish|NewString|NewInt|NewFloat|NewMap)$"))`,
		},
	})
}

func registerGoCommandExecution() {
	mustRegisterSAST(
		sastRule{
			Name:        "go-exec-command-via-shell",
			Description: "`exec.Command(\"sh\", \"-c\", cmd)` / `exec.Command(\"bash\", \"-c\", cmd)` — any caller-controlled portion of `cmd` is shell-injection. Use a fixed argv and parse separately.",
			Severity:    "error",
			CWE:         "CWE-78",
			OWASP:       "A03:2021-Injection",
			Tags:        []string{"command-injection"},
			Pat: map[string]string{
				"go": `((call_expression
                            function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                            arguments: (argument_list (interpreted_string_literal) @shell)) @match
                          (#eq? @pkg "exec") (#eq? @fn "Command")
                          (#match? @shell "^[\"'](sh|bash|zsh|cmd|powershell)[\"']$"))`,
			},
		},
		sastRule{
			Name:        "go-exec-command-with-sprintf",
			Description: "`exec.Command(fmt.Sprintf(...))` — string interpolation into the command path or argv before exec. Easy command-injection vector; build the argv with literal strings.",
			Severity:    "warning",
			CWE:         "CWE-78",
			Tags:        []string{"command-injection"},
			Pat: map[string]string{
				"go": `((call_expression
                            function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                            arguments: (argument_list
                              (call_expression function: (selector_expression operand: (identifier) @sprintfpkg field: (field_identifier) @sprintf)))) @match
                          (#eq? @pkg "exec") (#eq? @fn "Command")
                          (#eq? @sprintfpkg "fmt") (#match? @sprintf "^(Sprintf|Sprint|Sprintln)$"))`,
			},
		},
	)
}

func registerGoTLSInsecure() {
	mustRegisterSAST(
		sastRule{
			Name:        "go-tls-insecure-skip-verify",
			Description: "`&tls.Config{InsecureSkipVerify: true}` — disables certificate validation. Any MITM can intercept the connection.",
			Severity:    "error",
			CWE:         "CWE-295",
			OWASP:       "A02:2021-Cryptographic Failures",
			Tags:        []string{"tls", "no-verify"},
			Pat: map[string]string{
				"go": `((keyed_element
                            (literal_element (identifier) @name)
                            (literal_element (true))) @match
                          (#eq? @name "InsecureSkipVerify"))`,
			},
		},
		sastRule{
			Name:        "go-tls-min-version-weak",
			Description: "`tls.Config.MinVersion = tls.VersionSSL30 | tls.VersionTLS10 | tls.VersionTLS11` — broken / deprecated TLS versions. Use `tls.VersionTLS12` or `tls.VersionTLS13`.",
			Severity:    "warning",
			CWE:         "CWE-326",
			Tags:        []string{"tls", "weak-protocol"},
			Pat: map[string]string{
				"go": `((keyed_element
                            (literal_element (identifier) @name)
                            (literal_element (selector_expression operand: (identifier) @pkg field: (field_identifier) @ver))) @match
                          (#eq? @name "MinVersion") (#eq? @pkg "tls")
                          (#match? @ver "^(VersionSSL30|VersionTLS10|VersionTLS11)$"))`,
			},
		},
	)
}

func registerGoTemplateUnsafe() {
	mustRegisterSAST(
		sastRule{
			Name:        "go-html-template-unsafe-conv",
			Description: "`template.HTML(x)` / `template.JS(x)` / `template.URL(x)` / `template.CSS(x)` / `template.HTMLAttr(x)` — explicit \"trust this string as already-safe HTML\" conversion. With user input this is XSS.",
			Severity:    "error",
			CWE:         "CWE-79",
			OWASP:       "A03:2021-Injection",
			Tags:        []string{"xss", "template"},
			Pat: map[string]string{
				"go": `((call_expression
                            function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @typ)
                            arguments: (argument_list (identifier) @arg)) @match
                          (#eq? @pkg "template") (#match? @typ "^(HTML|JS|URL|CSS|HTMLAttr|JSStr|Srcset)$"))`,
			},
		},
	)
}

func registerGoJWTNone() {
	mustRegisterSAST(sastRule{
		Name:        "go-jwt-none-alg",
		Description: "`jwt.SigningMethodNone` / `SigningMethodNone{}` — produces unsigned JWTs. Tokens are accepted with no signature; trivial impersonation.",
		Severity:    "error",
		CWE:         "CWE-347",
		Tags:        []string{"jwt", "broken-auth"},
		Pat: map[string]string{
			"go": `((selector_expression operand: (identifier) @pkg field: (field_identifier) @method) @match
                      (#eq? @pkg "jwt") (#eq? @method "SigningMethodNone"))`,
		},
	})
}

func registerGoFilepathTraversal() {
	mustRegisterSAST(sastRule{
		Name:        "go-filepath-join-user-input",
		Description: "`filepath.Join(base, userInput)` does NOT prevent path traversal: `userInput == \"../../etc/passwd\"` happily resolves out of `base`. Use `filepath.Clean` + an explicit prefix check, or `filepath.EvalSymlinks`.",
		Severity:    "info",
		CWE:         "CWE-22",
		Tags:        []string{"path-traversal", "audit-hook"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)) @match
                      (#eq? @pkg "filepath") (#eq? @fn "Join"))`,
		},
	})
}

func registerGoBcryptCost() {
	mustRegisterSAST(sastRule{
		Name:        "go-bcrypt-cost-too-low",
		Description: "`bcrypt.GenerateFromPassword(pw, 4..9)` — cost below 10 is too fast for password hashing on modern hardware. Use `bcrypt.DefaultCost` (10) or higher; 12 is recommended for new deployments.",
		Severity:    "warning",
		CWE:         "CWE-326",
		Tags:        []string{"crypto", "password"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                        arguments: (argument_list (_) (int_literal) @cost)) @match
                      (#eq? @pkg "bcrypt") (#eq? @fn "GenerateFromPassword") (#match? @cost "^[0-9]$"))`,
		},
	})
}

func registerGoMathRandForCrypto() {
	mustRegisterSAST(sastRule{
		Name:        "go-math-rand-for-crypto",
		Description: "`math/rand` import — fine for simulations / sampling, not for tokens / keys / nonces. Use `crypto/rand`. Worth flagging the import so an audit can confirm no security-sensitive call site.",
		Severity:    "info",
		CWE:         "CWE-338",
		Tags:        []string{"crypto", "audit-hook"},
		Pat: map[string]string{
			"go": `((import_spec path: (interpreted_string_literal) @path) @match (#eq? @path "\"math/rand\""))`,
		},
	})
}

func registerGoCSRFDisable() {
	mustRegisterSAST(sastRule{
		Name:        "go-gorilla-csrf-disable",
		Description: "`csrf.Protect(..., csrf.Secure(false))` — disables the CSRF cookie's `Secure` flag, allowing the token to leak over HTTP. Only safe in dev.",
		Severity:    "warning",
		CWE:         "CWE-614",
		Tags:        []string{"csrf"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)
                        arguments: (argument_list (false))) @match
                      (#eq? @pkg "csrf") (#eq? @fn "Secure"))`,
		},
	})
}

func registerGoSSRFAuditHook() {
	mustRegisterSAST(sastRule{
		Name:        "go-http-do-with-userdata",
		Description: "`http.Get(url)` / `http.Post(...)` / `http.Client.Do(req)` — flag for SSRF audit. Most uses are fine; production breaches typically trace back to a non-validated `url` parameter.",
		Severity:    "info",
		CWE:         "CWE-918",
		Tags:        []string{"ssrf", "audit-hook"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)) @match
                      (#eq? @pkg "http") (#match? @fn "^(Get|Post|PostForm|Head)$"))`,
		},
	})
}

func registerGoCORSAllowAll() {
	mustRegisterSAST(sastRule{
		Name:        "go-cors-allow-all-origins",
		Description: "CORS handler returning `\"*\"` for `Access-Control-Allow-Origin` while also setting `Access-Control-Allow-Credentials: true` is rejected by browsers — and combining the two anyway often hides an unintentional same-origin bypass.",
		Severity:    "warning",
		CWE:         "CWE-942",
		Tags:        []string{"cors"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @hdr field: (field_identifier) @fn)
                        arguments: (argument_list (interpreted_string_literal) @key (interpreted_string_literal) @val)) @match
                      (#match? @fn "^(Set|Add)$") (#match? @key "\"Access-Control-Allow-Origin\"") (#match? @val "\"\\*\""))`,
		},
	})
}

func registerGoXMLDecoderNoEntity() {
	mustRegisterSAST(sastRule{
		Name:        "go-xml-decoder-no-strict",
		Description: "`xml.NewDecoder(...)` without setting `Strict = true` (default is true, but the `Entity` field can still be set to allow external entities). Flag for audit when XML payloads come from untrusted sources.",
		Severity:    "info",
		CWE:         "CWE-611",
		Tags:        []string{"xxe", "xml", "audit-hook"},
		Pat: map[string]string{
			"go": `((call_expression
                        function: (selector_expression operand: (identifier) @pkg field: (field_identifier) @fn)) @match
                      (#eq? @pkg "xml") (#eq? @fn "NewDecoder"))`,
		},
	})
}
