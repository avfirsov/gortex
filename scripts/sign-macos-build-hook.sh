#!/bin/sh
# Goreleaser build hook: codesigns darwin Mach-O binaries with rcodesign
# so they survive macOS Gatekeeper / notary checks. No-op on non-darwin
# targets — the hook fires for every {goos, goarch} pair, and we gate on
# the first arg ($1 = template "{{ .Os }}").
#
# Expects the workflow step to have populated MACOS_SIGNING_DIR with:
#   rcodesign      — apple-codesign linux-musl binary
#   cert.p12       — Developer ID Application certificate
#   cert.pass      — P12 export password (newline-free)
set -eu

[ "$1" = darwin ] || exit 0

SIGNING_DIR="${MACOS_SIGNING_DIR:-/tmp/macos-signing}"

exec "$SIGNING_DIR/rcodesign" sign \
  --p12-file "$SIGNING_DIR/cert.p12" \
  --p12-password-file "$SIGNING_DIR/cert.pass" \
  --code-signature-flags runtime \
  "$2"
