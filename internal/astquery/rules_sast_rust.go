package astquery

// Rust SAST starter pack. The legacy unsafe-rust-* family already
// covers unsafe blocks / unwrap / panic-macro / assert-macro. This
// file adds security-specific patterns absent from that bundle.

func init() {
	registerRustCommandShell()
	registerRustWeakCryptoCrate()
	registerRustHttpInsecure()
	registerRustMemForget()
	registerRustTransmute()
	registerRustEnvFromUser()
	registerRustStdRandForCrypto()
}

func registerRustCommandShell() {
	mustRegisterSAST(sastRule{
		Name:        "rust-command-sh-c",
		Description: "`Command::new(\"sh\").arg(\"-c\").arg(cmd)` — runs `cmd` through the shell. Any caller-controlled portion is command injection. Build an argv directly.",
		Severity:    "error",
		CWE:         "CWE-78",
		OWASP:       "A03:2021-Injection",
		Tags:        []string{"command-injection"},
		Pat: map[string]string{
			"rust": `((call_expression
                          function: (scoped_identifier path: (identifier) @ty name: (identifier) @method)
                          arguments: (arguments (string_literal) @shell)) @match
                        (#eq? @ty "Command") (#eq? @method "new")
                        (#match? @shell "\"(sh|bash|zsh|cmd|powershell)\""))`,
		},
	})
}

func registerRustWeakCryptoCrate() {
	mustRegisterSAST(sastRule{
		Name:        "rust-md5-or-sha1-crate",
		Description: "`use md5::*` / `use sha1::*` — crates that wrap broken hashes. Switch to `sha2`, `sha3`, `blake3`, or `argon2`.",
		Severity:    "error",
		CWE:         "CWE-327",
		Tags:        []string{"crypto", "weak-hash"},
		Pat: map[string]string{
			"rust": `((use_declaration argument: (scoped_use_list path: (identifier) @crate)) @match
                        (#match? @crate "^(md5|sha1|md4|md2)$"))
                       ((use_declaration argument: (scoped_identifier path: (identifier) @crate)) @match
                        (#match? @crate "^(md5|sha1|md4|md2)$"))`,
		},
	})
}

func registerRustHttpInsecure() {
	mustRegisterSAST(sastRule{
		Name:        "rust-reqwest-danger-accept-invalid-certs",
		Description: "`reqwest::Client::builder().danger_accept_invalid_certs(true)` — disables TLS validation. Any MITM intercepts.",
		Severity:    "error",
		CWE:         "CWE-295",
		Tags:        []string{"tls", "no-verify"},
		Pat: map[string]string{
			"rust": `((call_expression
                          function: (field_expression field: (field_identifier) @fn)
                          arguments: (arguments (boolean_literal) @v)) @match
                        (#match? @fn "^(danger_accept_invalid_certs|danger_accept_invalid_hostnames)$") (#eq? @v "true"))`,
		},
	})
}

func registerRustMemForget() {
	mustRegisterSAST(sastRule{
		Name:        "rust-mem-forget",
		Description: "`mem::forget(x)` — leaks `x` without running its destructor. Sometimes intentional (FFI hand-off); usually a bug. Audit every call site.",
		Severity:    "info",
		CWE:         "CWE-401",
		Tags:        []string{"memory", "audit-hook"},
		Pat: map[string]string{
			"rust": `((call_expression
                          function: (scoped_identifier path: (identifier) @mod name: (identifier) @fn)) @match
                        (#eq? @mod "mem") (#eq? @fn "forget"))`,
		},
	})
}

func registerRustTransmute() {
	mustRegisterSAST(sastRule{
		Name:        "rust-mem-transmute",
		Description: "`mem::transmute::<_, _>(...)` — bit-cast between unrelated types. Often the wrong primitive; usually a sign that a `From` / safe API exists. Hand-audit boundary.",
		Severity:    "warning",
		CWE:         "CWE-704",
		Tags:        []string{"memory", "unsafe"},
		Pat: map[string]string{
			"rust": `((call_expression
                          function: [(scoped_identifier name: (identifier) @fn) (generic_function function: (scoped_identifier name: (identifier) @fn))]) @match
                        (#eq? @fn "transmute"))`,
		},
	})
}

func registerRustEnvFromUser() {
	mustRegisterSAST(sastRule{
		Name:        "rust-env-set-from-user",
		Description: "`std::env::set_var(k, v)` — process-wide; thread-unsafe pre-1.0 / racy on Unix. Avoid using it to plumb user input into the env (subsequent processes inherit).",
		Severity:    "info",
		CWE:         "CWE-454",
		Tags:        []string{"env"},
		Pat: map[string]string{
			"rust": `((call_expression
                          function: (scoped_identifier path: (scoped_identifier path: (identifier) @std name: (identifier) @env) name: (identifier) @fn)) @match
                        (#eq? @std "std") (#eq? @env "env") (#eq? @fn "set_var"))`,
		},
	})
}

func registerRustStdRandForCrypto() {
	mustRegisterSAST(sastRule{
		Name:        "rust-thread-rng-for-crypto",
		Description: "`rand::thread_rng()` — chacha20-based and cryptographically secure *in current versions*, but `SmallRng::seed_from_u64` and other `rand` PRNGs are not. Audit each call site to confirm the chosen RNG meets the use case (use `rand::rngs::OsRng` / `ring::rand` for keys / nonces / tokens unambiguously).",
		Severity:    "info",
		CWE:         "CWE-338",
		Tags:        []string{"crypto", "audit-hook"},
		Pat: map[string]string{
			"rust": `((call_expression function: (scoped_identifier path: (identifier) @crate name: (identifier) @fn)) @match
                        (#eq? @crate "rand") (#match? @fn "^(thread_rng|random)$"))`,
		},
	})
}
