package lsp

import "testing"

// discardWriteCloser is a no-op transport write-end so closeDocument's
// didClose notification can be "sent" without a live LSP server.
type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

// TestCloseDocument_DropsSourceCache asserts closeDocument frees the
// file's cached bytes. Without this an interactive session that keeps
// the provider warm (hover / definition traffic) retains every
// navigated file's contents for the daemon's lifetime.
func TestCloseDocument_DropsSourceCache(t *testing.T) {
	const abs = "/work/main.go"
	p := &Provider{
		openDocs:    map[string]bool{abs: true},
		docVersions: map[string]int{abs: 1},
		lastDiag:    map[string][]Diagnostic{},
		sourceCache: map[string][]byte{abs: []byte("package main")},
		client:      &Client{stdin: discardWriteCloser{}},
	}

	if err := p.closeDocument(abs); err != nil {
		t.Fatalf("closeDocument: %v", err)
	}
	if _, ok := p.sourceCache[abs]; ok {
		t.Fatalf("sourceCache still holds %q after closeDocument", abs)
	}
	if p.openDocs[abs] {
		t.Fatalf("openDocs still marks %q open after closeDocument", abs)
	}
}

// TestResetForReconnect_ClearsSourceCache asserts a reconnect frees
// every cached file's bytes, not just the doc-version / open-doc
// bookkeeping. A nil client makes resetForReconnect skip the LSP
// shutdown handshake.
func TestResetForReconnect_ClearsSourceCache(t *testing.T) {
	p := &Provider{
		openDocs:    map[string]bool{"/a": true},
		docVersions: map[string]int{"/a": 1},
		lastDiag:    map[string][]Diagnostic{"/a": nil},
		sourceCache: map[string][]byte{"/a": []byte("x"), "/b": []byte("y")},
	}

	p.resetForReconnect()

	if len(p.sourceCache) != 0 {
		t.Fatalf("sourceCache = %d entries after resetForReconnect, want 0", len(p.sourceCache))
	}
	if len(p.openDocs) != 0 {
		t.Fatalf("openDocs = %d entries after resetForReconnect, want 0", len(p.openDocs))
	}
}
