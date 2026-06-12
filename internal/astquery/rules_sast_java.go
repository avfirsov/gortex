package astquery

// Java SAST starter pack. The legacy `java-string-equality` +
// `empty-catch` rules in detectors.go cover language-level pitfalls;
// this file adds JVM security patterns the SAST audit looks for.

func init() {
	registerJavaCommandExecution()
	registerJavaWeakCrypto()
	registerJavaInsecureSSL()
	registerJavaXMLDecoder()
	registerJavaSQLConcat()
	registerJavaRandom()
	registerJavaJWTNone()
	registerJavaServletXSS()
	registerJavaProcessBuilder()
	registerJavaTrustAllCerts()
	registerJavaSecretLiteralAnnotation()
}

func registerJavaCommandExecution() {
	mustRegisterSAST(sastRule{
		Name:        "java-runtime-exec",
		Description: "`Runtime.getRuntime().exec(cmd)` with a string argument — the JDK splits on whitespace, which silently quoting bugs become argv-injection bugs. Use the String[] overload or `ProcessBuilder`.",
		Severity:    "error",
		CWE:         "CWE-78",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"java": `((method_invocation
                          object: (method_invocation
                            object: (identifier) @rt
                            name: (identifier) @getrt)
                          name: (identifier) @exec) @match
                        (#eq? @rt "Runtime") (#eq? @getrt "getRuntime") (#eq? @exec "exec"))`,
		},
	})
}

func registerJavaProcessBuilder() {
	mustRegisterSAST(sastRule{
		Name:        "java-process-builder-shell-args",
		Description: "`new ProcessBuilder(\"sh\", \"-c\", cmd).start()` — shell argv 0 with a user-controlled `cmd` arg is command injection.",
		Severity:    "warning",
		CWE:         "CWE-78",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"java": `((object_creation_expression
                          type: (type_identifier) @typ
                          arguments: (argument_list (string_literal) @shell)) @match
                        (#eq? @typ "ProcessBuilder")
                        (#match? @shell "\"(sh|bash|zsh|cmd|powershell)\""))`,
		},
	})
}

func registerJavaWeakCrypto() {
	mustRegisterSAST(sastRule{
		Name:        "java-message-digest-md5-sha1",
		Description: "`MessageDigest.getInstance(\"MD5\")` / `\"SHA-1\")` — broken hashes. Use `\"SHA-256\"` / `\"SHA-512\"` / `\"SHA3-256\"`.",
		Severity:    "error",
		CWE:         "CWE-327",
		Tags:        []string{"crypto", "weak-hash"},
		Pat: map[string]string{
			"java": `((method_invocation
                          object: (identifier) @cls
                          name: (identifier) @fn
                          arguments: (argument_list (string_literal) @algo)) @match
                        (#eq? @cls "MessageDigest") (#eq? @fn "getInstance")
                        (#match? @algo "\"(MD5|MD4|MD2|SHA-1|SHA1)\""))`,
		},
	})
}

func registerJavaInsecureSSL() {
	mustRegisterSAST(sastRule{
		Name:        "java-cipher-des-ecb",
		Description: "`Cipher.getInstance(\"DES\")` / `Cipher.getInstance(\"AES/ECB/...\")` — DES is broken; ECB doesn't hide plaintext patterns. Use `AES/GCM/NoPadding` or `ChaCha20-Poly1305`.",
		Severity:    "error",
		CWE:         "CWE-327",
		Tags:        []string{"crypto", "weak-cipher"},
		Pat: map[string]string{
			"java": `((method_invocation
                          object: (identifier) @cls
                          name: (identifier) @fn
                          arguments: (argument_list (string_literal) @algo)) @match
                        (#eq? @cls "Cipher") (#eq? @fn "getInstance")
                        (#match? @algo "\"(DES|DESede|RC2|RC4|.*/ECB/.*)\""))`,
		},
	})
}

func registerJavaXMLDecoder() {
	mustRegisterSAST(sastRule{
		Name:        "java-xml-decoder",
		Description: "`new XMLDecoder(stream).readObject()` — XMLDecoder deserialises arbitrary Java objects, equivalent to `ObjectInputStream`. Untrusted input is RCE.",
		Severity:    "error",
		CWE:         "CWE-502",
		Tags:        []string{"deserialization"},
		Pat: map[string]string{
			"java": `((object_creation_expression type: (type_identifier) @typ) @match (#eq? @typ "XMLDecoder"))`,
		},
	})
}

func registerJavaSQLConcat() {
	mustRegisterSAST(sastRule{
		Name:        "java-statement-execute-concat",
		Description: "`statement.execute(\"SELECT ... \" + x)` / `executeQuery(...)` with `+` concat — SQL injection. Use `PreparedStatement` with `?` placeholders.",
		Severity:    "error",
		CWE:         "CWE-89",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"sqli"},
		Pat: map[string]string{
			"java": `((method_invocation
                          name: (identifier) @fn
                          arguments: (argument_list (binary_expression operator: "+"))) @match
                        (#match? @fn "^(execute|executeQuery|executeUpdate|prepareStatement)$"))`,
		},
	})
}

func registerJavaRandom() {
	mustRegisterSAST(sastRule{
		Name:        "java-random-for-token",
		Description: "`new Random()` — Mersenne Twister; not crypto-secure. Use `java.security.SecureRandom` for tokens / IVs / keys.",
		Severity:    "info",
		CWE:         "CWE-338",
		Tags:        []string{"crypto", "audit-hook"},
		Pat: map[string]string{
			"java": `((object_creation_expression type: (type_identifier) @typ) @match (#eq? @typ "Random"))`,
		},
	})
}

func registerJavaJWTNone() {
	mustRegisterSAST(sastRule{
		Name:        "java-jwt-none-alg",
		Description: "`Algorithm.none()` (auth0/java-jwt) / `\"none\"` algorithm string — accepts unsigned JWTs. Trivial impersonation.",
		Severity:    "error",
		CWE:         "CWE-347",
		Tags:        []string{"jwt", "broken-auth"},
		Pat: map[string]string{
			"java": `((method_invocation
                          object: (identifier) @alg
                          name: (identifier) @fn) @match
                        (#eq? @alg "Algorithm") (#eq? @fn "none"))`,
		},
	})
}

func registerJavaServletXSS() {
	mustRegisterSAST(sastRule{
		Name:        "java-response-write-user-input",
		Description: "`response.getWriter().write(request.getParameter(...))` — reflected XSS unless the writer is HTML-escaped. Use a templating engine or `org.owasp.encoder.Encode.forHtml`.",
		Severity:    "info",
		CWE:         "CWE-79",
		Tags:        []string{"xss", "audit-hook"},
		Pat: map[string]string{
			"java": `((method_invocation
                          object: (method_invocation name: (identifier) @writer)
                          name: (identifier) @write
                          arguments: (argument_list (method_invocation name: (identifier) @src))) @match
                        (#eq? @writer "getWriter") (#eq? @write "write") (#match? @src "^getParameter|getHeader|getQueryString$"))`,
		},
	})
}

func registerJavaTrustAllCerts() {
	mustRegisterSAST(sastRule{
		Name:        "java-trust-all-certs",
		Description: "Custom `X509TrustManager` whose `checkServerTrusted` does nothing — accepts every certificate, defeating TLS verification.",
		Severity:    "error",
		CWE:         "CWE-295",
		Tags:        []string{"tls", "no-verify"},
		Pat: map[string]string{
			"java": `((method_declaration
                          name: (identifier) @fn
                          body: (block) @body) @match
                        (#eq? @fn "checkServerTrusted"))`,
			// Empty-body heuristic via PostFilter.
		},
		// Filter out methods with non-trivial bodies; defaults catch
		// {} / { return; } / { /* ... */ }.
	})
}

func registerJavaSecretLiteralAnnotation() {
	mustRegisterSAST(sastRule{
		Name:        "java-spring-value-hardcoded-secret",
		Description: "`@Value(\"static-literal-secret\")` — Spring `@Value` annotation with a literal that names a credential. Move to env / a `@ConfigurationProperties` mapper backed by env / a secrets manager.",
		Severity:    "warning",
		CWE:         "CWE-798",
		Tags:        []string{"secrets", "spring"},
		Pat: map[string]string{
			"java": `((annotation
                          name: (identifier) @ann
                          arguments: (annotation_argument_list (string_literal) @val)) @match
                        (#eq? @ann "Value") (#match? @val "[\"'][^${].{20,}[\"']"))`,
		},
	})
}
