package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const codeActionWaitTimeout = 2 * time.Second

// TestProvider_GetCodeActions_RoundTrip confirms that a code-action
// request flows through the provider and the action list is parsed
// correctly when the server returns a CodeAction literal.
func TestProvider_GetCodeActions_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nvar x = 1\n"), 0o644))

	server := newFakeLSPServer()
	server.handle("textDocument/codeAction", func(params json.RawMessage) (any, *jsonRPCError) {
		return []CodeActionOrCommand{{
			Title: "Organize imports",
			Kind:  CodeActionKindSourceOrganizeImports,
			Edit: &WorkspaceEdit{
				DocumentChanges: []TextDocumentEdit{{
					TextDocument: VersionedTextDocumentIdentifier{URI: pathToURI(src), Version: 1},
					Edits: []TextEdit{{
						Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 0}},
						NewText: "// added\n",
					}},
				}},
			},
		}}, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"go"})
	defer cleanup()

	require.NoError(t, p.openDocument(dir, "main.go"))

	actions, err := p.GetCodeActions(CodeActionsRequest{
		AbsPath: src,
		Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 0}},
		Only:    []string{CodeActionKindSourceOrganizeImports},
	})
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, "Organize imports", actions[0].Title)
	require.NotNil(t, actions[0].Edit)
}

// TestProvider_PublishDiagnostics_NotificationFanout confirms the
// publishDiagnostics notification handler updates LastDiagnostics and
// wakes a WaitForDiagnostics caller.
func TestProvider_PublishDiagnostics_NotificationFanout(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "x.go")
	c, serverIn, serverOut, cleanup := newPipedClient(t)
	defer cleanup()

	go func() {
		// Drain so the client write doesn't block; we don't need to
		// reply to anything.
		for {
			if _, ok := readFramed(serverIn); !ok {
				return
			}
		}
	}()

	p := NewProvider("fake", nil, []string{"go"}, false, 0, zap.NewNop())
	p.client = c
	// Wire the diagnostic handler manually since ensureClient never ran.
	p.client.OnNotification("textDocument/publishDiagnostics",
		func(_ string, params json.RawMessage) {
			var pd PublishDiagnosticsParams
			if err := json.Unmarshal(params, &pd); err != nil {
				return
			}
			absParsed := uriToAbsPath(pd.URI)
			if absParsed == "" {
				return
			}
			p.docMu.Lock()
			p.lastDiag[absParsed] = pd.Diagnostics
			p.docMu.Unlock()
			p.fanoutDiagnostics(absParsed, pd.Diagnostics)
		})

	// Push a notification framed like an LSP message — the client
	// should route it to the registered handler.
	pd := PublishDiagnosticsParams{
		URI: pathToURI(abs),
		Diagnostics: []Diagnostic{{
			Severity: DiagSeverityError,
			Message:  "boom",
			Range:    Range{Start: Position{Line: 1}, End: Position{Line: 1, Character: 5}},
		}},
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  pd,
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data)))
	_, _ = serverOut.Write(header)
	_, _ = serverOut.Write(data)

	got := p.WaitForDiagnostics(abs, codeActionWaitTimeout)
	require.Len(t, got, 1)
	require.Equal(t, "boom", got[0].Message)
	cached, ok := p.LastDiagnostics(abs)
	require.True(t, ok)
	require.Equal(t, "boom", cached[0].Message)
}
