# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do not** open a public issue
2. Email the maintainer directly or use GitHub's private vulnerability reporting
3. Include a description of the vulnerability and steps to reproduce

We will acknowledge receipt within 48 hours and provide a timeline for a fix.

## Scope

Gortex runs locally and processes source code on the user's machine. Security considerations include:

- **File system access**: Gortex reads files within the indexed directory. It does not write to source files.
- **MCP transport**: The stdio transport communicates only with the parent process. The HTTP transport (web UI) binds to localhost by default.
- **No network access**: Gortex makes no outbound network requests at runtime.
- **CGO**: Tree-sitter C bindings are compiled via CGO. The grammars are vendored from `github.com/smacker/go-tree-sitter`.
