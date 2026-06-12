package astquery

import "github.com/zzet/gortex/internal/parser"

// Additional Bandit-parity Python rules — split off from rules_sast_python.go
// so each file stays under ~1500 LOC. Plugin ID references in
// References:[] map back to the Bandit plugin numbers for cross-check.

func init() {
	registerPythonBanditExtras()
	registerPythonCryptoKeys()
	registerPythonFrameworkExtras()
	registerPythonMongoRedis()
	registerPythonTryExceptPass()
	registerPythonRequestsTimeout()
	registerPythonHTTPSConnection()
	registerPythonCElementTreeImport()
	registerPythonTempnam()
	registerPythonPyCryptoDeprecated()
	registerPythonSSLNoVersion()
	registerPythonLinuxCmdWildcard()
	registerPythonMarkupSafe()
	registerPythonJsonPickle()
	registerPythonCGIDeprecated()
	registerPythonHardcodedAssignment()
	registerPythonSubprocessNoShellAudit()
	registerPythonNumpyRandomSeed()
}

// 1. try/except: pass — Bandit B110
func registerPythonTryExceptPass() {
	mustRegisterSAST(sastRule{
		Name:        "py-try-except-pass",
		Description: "`except: pass` — silently swallow every exception. Hides production bugs and breaks observability. Log the exception or narrow the except clause.",
		Severity:    "info",
		CWE:         "CWE-703",
		Tags:        []string{"exception", "swallow"},
		References:  []string{"bandit:B110"},
		Pat: map[string]string{
			"python": `((except_clause (block (pass_statement) @body)) @match)`,
		},
	})
}

// 2. requests without timeout — Bandit B113
func registerPythonRequestsTimeout() {
	mustRegisterSAST(sastRule{
		Name:        "py-requests-no-timeout",
		Description: "`requests.get|post|...` without a `timeout=` kwarg — defaults to no timeout, lets a slow / unresponsive server wedge the caller indefinitely. Pass `timeout=(connect_s, read_s)`.",
		Severity:    "warning",
		CWE:         "CWE-400",
		Tags:        []string{"availability", "timeout"},
		References:  []string{"bandit:B113"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                          arguments: (argument_list) @args) @match
                        (#match? @mod "^(requests|httpx)$")
                        (#match? @attr "^(get|post|put|delete|patch|head|options|request)$"))`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			a, ok := qr.Captures["args"]
			if !ok {
				return false
			}
			return !containsStr(a.Text, "timeout")
		},
	})
}

// 3. HTTPSConnection no context — Bandit B309
func registerPythonHTTPSConnection() {
	mustRegisterSAST(sastRule{
		Name:        "py-httpsconnection-no-context",
		Description: "`httplib.HTTPSConnection(...)` / `http.client.HTTPSConnection(...)` without an explicit `context=` ssl context — TLS verification depends on Python version defaults. Pass `context=ssl.create_default_context()`.",
		Severity:    "info",
		CWE:         "CWE-295",
		Tags:        []string{"tls"},
		References:  []string{"bandit:B309"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                          arguments: (argument_list) @args) @match
                        (#match? @mod "^(httplib|http\\.client)$")
                        (#eq? @attr "HTTPSConnection"))`,
		},
		PostFilter: func(qr parser.QueryResult, _ []byte) bool {
			a, ok := qr.Captures["args"]
			if !ok {
				return false
			}
			return !containsStr(a.Text, "context=")
		},
	})
}

// 4. cElementTree import — Bandit B313 (alias path)
func registerPythonCElementTreeImport() {
	mustRegisterSAST(sastRule{
		Name:        "py-cElementTree-import",
		Description: "`from xml.etree.cElementTree import ...` — Python 2 era C-accelerated stdlib XML parser. XXE-vulnerable; use `defusedxml.ElementTree`.",
		Severity:    "error",
		CWE:         "CWE-611",
		Tags:        []string{"xxe", "xml", "deprecated"},
		References:  []string{"bandit:B313"},
		Pat: map[string]string{
			"python": `((import_from_statement module_name: (dotted_name) @mod) @match (#eq? @mod "xml.etree.cElementTree"))
                       ((import_statement name: (dotted_name) @mod) @match (#eq? @mod "xml.etree.cElementTree"))`,
		},
	})
}

// 5. os.tempnam / os.tmpnam — Bandit B325
func registerPythonTempnam() {
	mustRegisterSAST(sastRule{
		Name:        "py-os-tempnam",
		Description: "`os.tempnam()` / `os.tmpnam()` — race-prone temp-file name generation, deprecated. Use `tempfile.mkstemp()`.",
		Severity:    "warning",
		CWE:         "CWE-377",
		Tags:        []string{"tempfile", "race"},
		References:  []string{"bandit:B325"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)) @match
                        (#eq? @mod "os") (#match? @attr "^(tempnam|tmpnam)$"))`,
		},
	})
}

// 6. pyCrypto (the old C library, not pycryptodome) — Bandit B413
func registerPythonPyCryptoDeprecated() {
	mustRegisterSAST(sastRule{
		Name:        "py-pycrypto-deprecated-import",
		Description: "`import Crypto` (the original pyCrypto library, not pycryptodome) — unmaintained since 2013, multiple unpatched buffer-overflow CVEs (CVE-2013-7459 et al.). Migrate to `pycryptodome` (same API) or `cryptography`.",
		Severity:    "warning",
		CWE:         "CWE-1104",
		Tags:        []string{"crypto", "deprecated", "unmaintained"},
		References:  []string{"bandit:B413"},
		Pat: map[string]string{
			"python": `((import_statement name: (dotted_name) @mod) @match (#match? @mod "^Crypto(\\.|$)"))`,
		},
	})
}

// 7. ssl.wrap_socket without ssl_version arg — Bandit B504
func registerPythonSSLNoVersion() {
	mustRegisterSAST(sastRule{
		Name:        "py-ssl-wrap-socket-no-version",
		Description: "`ssl.wrap_socket(...)` without an explicit `ssl_version=ssl.PROTOCOL_TLS_CLIENT` — falls back to `PROTOCOL_TLS` which negotiates the highest supported, but older Python versions defaulted to v23 (SSLv2/v3 acceptable). Be explicit. `ssl.wrap_socket` itself is deprecated since 3.7 — use `SSLContext.wrap_socket`.",
		Severity:    "warning",
		CWE:         "CWE-326",
		Tags:        []string{"tls", "weak-protocol", "deprecated"},
		References:  []string{"bandit:B504"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)) @match
                        (#eq? @mod "ssl") (#eq? @attr "wrap_socket"))`,
		},
	})
}

// 8. shell wildcard expansion — Bandit B609
func registerPythonLinuxCmdWildcard() {
	mustRegisterSAST(sastRule{
		Name:        "py-shell-cmd-with-wildcard",
		Description: "Shell command string containing a glob (`*`, `?`) — `chown user *.txt` is shell-expanded; a file named `--reference=/etc/passwd` becomes an argument. Pass an explicit file list to a non-shell invocation.",
		Severity:    "warning",
		CWE:         "CWE-78",
		Tags:        []string{"command-injection", "wildcard"},
		References:  []string{"bandit:B609"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                          arguments: (argument_list (string) @cmd)) @match
                        (#match? @mod "^(subprocess|os)$") (#match? @cmd "[*?]"))`,
		},
	})
}

// 9. markupsafe.Markup(...) on user input — Bandit B704
func registerPythonMarkupSafe() {
	mustRegisterSAST(sastRule{
		Name:        "py-markupsafe-markup",
		Description: "`markupsafe.Markup(...)` — flags arbitrary text as HTML-safe. With user input it bypasses Jinja2 / Flask escaping and is XSS.",
		Severity:    "warning",
		CWE:         "CWE-79",
		Tags:        []string{"xss", "jinja2", "flask"},
		References:  []string{"bandit:B704"},
		Pat: map[string]string{
			"python": `((call function: [(identifier) @fn (attribute attribute: (identifier) @fn)]) @match (#eq? @fn "Markup"))`,
		},
	})
}

// 10. jsonpickle.decode — RCE primitive
func registerPythonJsonPickle() {
	mustRegisterSAST(sastRule{
		Name:        "py-jsonpickle-decode",
		Description: "`jsonpickle.decode(...)` / `jsonpickle.loads(...)` — under the hood unpickles arbitrary class refs. RCE on attacker-controlled input.",
		Severity:    "error",
		CWE:         "CWE-502",
		Tags:        []string{"deserialization"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)) @match
                        (#eq? @mod "jsonpickle") (#match? @attr "^(decode|loads)$"))`,
		},
	})
}

// 11. cgi module — deprecated, removed in 3.13
func registerPythonCGIDeprecated() {
	mustRegisterSAST(sastRule{
		Name:        "py-cgi-import-deprecated",
		Description: "`import cgi` — deprecated in 3.11, removed in 3.13. Functions are XSS-prone unless every caller escapes; replace with `urllib.parse.parse_qs` + a templating engine that auto-escapes.",
		Severity:    "info",
		CWE:         "CWE-1104",
		Tags:        []string{"deprecated"},
		Pat: map[string]string{
			"python": `((import_statement name: (dotted_name) @mod) @match (#eq? @mod "cgi"))`,
		},
	})
}

// 12. Bare-assignment hardcoded secret — Bandit B105
func registerPythonHardcodedAssignment() {
	mustRegisterSAST(sastRule{
		Name:        "py-hardcoded-credential-assignment",
		Description: "`password = \"...\"` / `secret = \"...\"` / `api_key = \"...\"` at module scope — leaks the credential into source control. Read from env or a secrets manager.",
		Severity:    "error",
		CWE:         "CWE-798",
		OWASP:       "A07:2021-Identification and Authentication Failures",
		Tags:        []string{"secrets", "credentials"},
		References:  []string{"bandit:B105"},
		Pat: map[string]string{
			"python": `((assignment left: (identifier) @name right: (string) @val) @match
                        (#match? @name "(?i)^(password|passwd|secret|api_?key|token|aws_?secret(_?key)?|access_?key|private_?key)$"))`,
		},
		PostFilter: secretLiteralLooksReal,
	})
}

// 13. subprocess.* without shell=True — Bandit B603 (audit hook)
func registerPythonSubprocessNoShellAudit() {
	mustRegisterSAST(sastRule{
		Name:        "py-subprocess-no-shell-audit",
		Description: "`subprocess.Popen|call|run|check_output|check_call(...)` without `shell=True` — the safe form, but still worth flagging when args are user-derived. Audit hook: confirm every argv element is sanitised or from a fixed allow-list.",
		Severity:    "info",
		CWE:         "CWE-78",
		Tags:        []string{"audit-hook", "subprocess"},
		References:  []string{"bandit:B603"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)) @match
                        (#eq? @mod "subprocess") (#match? @attr "^(Popen|call|run|check_output|check_call)$"))`,
		},
	})
}

// 14. numpy.random.seed — predictable
func registerPythonNumpyRandomSeed() {
	mustRegisterSAST(sastRule{
		Name:        "py-numpy-random-fixed-seed",
		Description: "`numpy.random.seed(<literal>)` — fixed-seed numpy random is deterministic across runs. Fine for reproducible experiments, never use a fixed-seed numpy RNG for tokens / IVs / keys.",
		Severity:    "info",
		CWE:         "CWE-330",
		Tags:        []string{"random", "numpy"},
		Pat: map[string]string{
			"python": `((call function: (attribute object: (attribute object: (identifier) @np attribute: (identifier) @sub) attribute: (identifier) @attr)
                          arguments: (argument_list (integer))) @match
                        (#eq? @np "numpy") (#eq? @sub "random") (#eq? @attr "seed"))
                       ((call function: (attribute object: (attribute object: (identifier) @np attribute: (identifier) @sub) attribute: (identifier) @attr)
                          arguments: (argument_list (integer))) @match
                        (#eq? @np "np") (#eq? @sub "random") (#eq? @attr "seed"))`,
		},
	})
}

// ---------------------------------------------------------------------------
// 15. Bandit-extras catch-all — bind to (host="0.0.0.0"), httpx verify=False,
//      starlette debug, scapy.send tx flooding hooks, asyncpg etc.
// ---------------------------------------------------------------------------

func registerPythonBanditExtras() {
	mustRegisterSAST(
		sastRule{
			Name:        "py-bind-host-zero",
			Description: "`...(host=\"0.0.0.0\", ...)` — Flask / FastAPI / aiohttp / Tornado application bound to every interface. Almost certainly unintentional in production; bind to `127.0.0.1` unless explicitly public.",
			Severity:    "warning",
			CWE:         "CWE-605",
			Tags:        []string{"bind", "all-interfaces"},
			Pat: map[string]string{
				"python": `((call arguments: (argument_list (keyword_argument name: (identifier) @kw value: (string) @val))) @match
                              (#eq? @kw "host") (#match? @val "[\"']?(0\\.0\\.0\\.0|::)[\"']?"))`,
			},
		},
		sastRule{
			Name:        "py-pyramid-config-debug",
			Description: "Pyramid `config.add_settings({'pyramid.debug_all': True})` — enables the introspection debug view that leaks app internals.",
			Severity:    "warning",
			CWE:         "CWE-489",
			Tags:        []string{"debug", "pyramid"},
			Pat: map[string]string{
				"python": `((call function: (attribute attribute: (identifier) @fn)
                                arguments: (argument_list (string) @val)) @match
                              (#eq? @fn "add_settings") (#match? @val "pyramid\\.debug"))`,
			},
		},
		sastRule{
			Name:        "py-fastapi-debug-true",
			Description: "`fastapi.FastAPI(debug=True)` — exposes stack traces / route table in error responses. Disable in production.",
			Severity:    "warning",
			CWE:         "CWE-489",
			Tags:        []string{"debug", "fastapi"},
			Pat: map[string]string{
				"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                                arguments: (argument_list (keyword_argument name: (identifier) @kw value: (true)))) @match
                              (#eq? @mod "fastapi") (#eq? @attr "FastAPI") (#eq? @kw "debug"))`,
			},
		},
		sastRule{
			Name:        "py-tornado-xss-render",
			Description: "Tornado `RequestHandler.render(...)` with `{% raw user_input %}` template tag — `{% raw %}` skips HTML escaping; with user data this is XSS. Use `{{ var }}` for escaped output.",
			Severity:    "info",
			CWE:         "CWE-79",
			Tags:        []string{"xss", "tornado"},
			Pat: map[string]string{
				"python": `((string) @match (#match? @match "\\{% raw "))`,
			},
		},
		sastRule{
			Name:        "py-werkzeug-debug-pin",
			Description: "`werkzeug.debug.DebuggedApplication(app, evalex=True)` — Werkzeug debugger PIN can be bypassed if `WERKZEUG_DEBUG_PIN=off`. Never deploy with `evalex=True`.",
			Severity:    "error",
			CWE:         "CWE-489",
			Tags:        []string{"debug", "werkzeug"},
			Pat: map[string]string{
				"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                                arguments: (argument_list . (_) (keyword_argument name: (identifier) @kw value: (true)))) @match
                              (#eq? @attr "DebuggedApplication") (#eq? @kw "evalex"))`,
			},
		},
	)
}

// 16. Weak crypto key sizes — Bandit B505
func registerPythonCryptoKeys() {
	mustRegisterSAST(
		sastRule{
			Name:        "py-rsa-weak-key-size",
			Description: "`RSA.generate(<1024)` / `rsa.generate_private_key(public_exponent=..., key_size=<2048)` — 1024-bit RSA factorable by nation-state adversaries; NIST minimum is 2048. Use 3072+ for new deployments.",
			Severity:    "warning",
			CWE:         "CWE-326",
			Tags:        []string{"crypto", "weak-key"},
			References:  []string{"bandit:B505"},
			Pat: map[string]string{
				"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                                arguments: (argument_list (integer) @bits)) @match
                              (#match? @mod "^(RSA|rsa)$") (#eq? @attr "generate") (#match? @bits "^(512|768|1024)$"))
                              ((call arguments: (argument_list (keyword_argument name: (identifier) @kw value: (integer) @bits))) @match
                               (#eq? @kw "key_size") (#match? @bits "^(512|768|1024)$"))`,
			},
		},
		sastRule{
			Name:        "py-dsa-weak-key-size",
			Description: "`DSA.generate(<2048)` — 1024-bit DSA is below NIST minimum since 2013.",
			Severity:    "warning",
			CWE:         "CWE-326",
			Tags:        []string{"crypto", "weak-key"},
			Pat: map[string]string{
				"python": `((call function: (attribute object: (identifier) @mod attribute: (identifier) @attr)
                                arguments: (argument_list (integer) @bits)) @match
                              (#eq? @mod "DSA") (#eq? @attr "generate") (#match? @bits "^(512|768|1024)$"))`,
			},
		},
		sastRule{
			Name:        "py-ec-weak-curve",
			Description: "`ec.generate_private_key(ec.SECT163K1())` / similar — < 224-bit elliptic curves are below NIST minimum. Use `SECP256R1` / `SECP384R1` / `SECP521R1` / `Curve25519`.",
			Severity:    "warning",
			CWE:         "CWE-326",
			Tags:        []string{"crypto", "weak-curve"},
			Pat: map[string]string{
				"python": `((call function: (attribute attribute: (identifier) @curve)) @match
                              (#match? @curve "^(SECT163K1|SECT163R2|SECP160R1|SECP160R2|SECP160K1|SECP192R1|SECT193R1|SECT193R2)$"))`,
			},
		},
	)
}

// 17. Mongo / Redis injection
func registerPythonMongoRedis() {
	mustRegisterSAST(
		sastRule{
			Name:        "py-mongo-where-injection",
			Description: "Mongo `.find({\"$where\": user_input})` — `$where` runs JavaScript on the Mongo server; user-controlled JS is RCE on the DB host.",
			Severity:    "error",
			CWE:         "CWE-943",
			Tags:        []string{"nosql-injection", "mongodb"},
			Pat: map[string]string{
				"python": `((string) @match (#match? @match "[\"']\\$where[\"']"))`,
			},
		},
		sastRule{
			Name:        "py-redis-eval-script",
			Description: "Redis `.eval(script, ...)` / `.evalsha(...)` — Lua scripts run on the Redis server. User-controlled script content is RCE.",
			Severity:    "warning",
			CWE:         "CWE-943",
			Tags:        []string{"nosql-injection", "redis", "lua"},
			Pat: map[string]string{
				"python": `((call function: (attribute attribute: (identifier) @fn)
                                arguments: (argument_list . (string) @script)) @match
                              (#match? @fn "^(eval|evalsha)$") (#match? @script "[\"']?[A-Z]+"))`,
			},
		},
	)
}

// 18. Framework extras — Django middleware order, etc.
func registerPythonFrameworkExtras() {
	mustRegisterSAST(
		sastRule{
			Name:        "py-django-format-html-join-user-input",
			Description: "Django `format_html_join(sep, fmt, args)` with user-controlled `args` — the format string is trusted but `sep` and elements still need escaping. Audit every site.",
			Severity:    "info",
			CWE:         "CWE-79",
			Tags:        []string{"xss", "django"},
			Pat: map[string]string{
				"python": `((call function: [(identifier) @fn (attribute attribute: (identifier) @fn)]) @match (#eq? @fn "format_html_join"))`,
			},
		},
		sastRule{
			Name:        "py-django-render-with-context-from-request",
			Description: "Django `render(request, template, request.GET)` — passes the raw request mapping as template context. Any auto-escaping the template skips becomes user-controlled.",
			Severity:    "info",
			CWE:         "CWE-79",
			Tags:        []string{"xss", "django", "audit-hook"},
			Pat: map[string]string{
				"python": `((call function: [(identifier) @fn (attribute attribute: (identifier) @fn)]
                                arguments: (argument_list (_) (_) (attribute object: (identifier) @src))) @match
                              (#eq? @fn "render") (#eq? @src "request"))`,
			},
		},
	)
}

