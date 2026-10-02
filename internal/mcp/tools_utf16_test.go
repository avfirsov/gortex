package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
)

// The #846 read-path contract: the indexer decodes UTF-16 sources before
// extraction, so a symbol declared in a UTF-16 file is discoverable. The
// read tools must serve the same decoded text — not the raw NUL-interleaved
// bytes — and the edit tools must refuse the file with a message that names
// the encoding, instead of "old_string not found" pointing at the reads.

const utf16FixtureSrc = "public class Legacy\n{\n    public int B() { return 1; }\n}\n"

// utf16ServerWith indexes one UTF-16LE file and one UTF-8 control file,
// returning the server and the temp dir root.
func utf16ServerWith(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, "Legacy.cs")
	require.NoError(t, os.WriteFile(full, utf16LEWithBOM(t, utf16FixtureSrc), 0o644))
	plain := filepath.Join(dir, "Utf8.cs")
	require.NoError(t, os.WriteFile(plain, []byte("public class Utf8Only\n{\n}\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)
	return NewServer(query.NewEngine(g), g, idx, nil, zap.NewNop(), nil), dir
}

// utf16LEWithBOM encodes s as UTF-16LE with the byte-order mark a Windows
// tooling writer puts on the file.
func utf16LEWithBOM(t *testing.T, s string) []byte {
	t.Helper()
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+len(units)*2)
	out = append(out, 0xFF, 0xFE)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// utf16SymbolNamed looks up a node by name among the file's symbols.
func utf16SymbolNamed(t *testing.T, srv *Server, file, name string) *graph.Node {
	t.Helper()
	sg := srv.engineFor(context.Background()).GetFileSymbols(file)
	require.NotNil(t, sg, "file %s must be indexed", file)
	for _, n := range sg.Nodes {
		if n != nil && n.Name == name {
			return n
		}
	}
	t.Fatalf("symbol %s not found in %s", name, file)
	return nil
}

func utf16JSON(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, res.IsError, "%+v", res.Content)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out))
	return out
}

func TestGetSymbolSource_ServesDecodedUTF16Source(t *testing.T) {
	srv, dir := utf16ServerWith(t)
	node := utf16SymbolNamed(t, srv, "Legacy.cs", "Legacy")
	require.True(t, fileLooksUTF16(filepath.Join(dir, "Legacy.cs")))

	out := utf16JSON(t, callTool(t, srv, "get_symbol_source", map[string]any{"id": node.ID}))

	source, _ := out["source"].(string)
	require.NotContains(t, source, "\x00", "the source must not be NUL-interleaved raw bytes")
	require.Contains(t, source, "public class Legacy")
	require.Contains(t, source, "return 1;")

	omissions, _ := out["omissions"].([]any)
	var disclosed bool
	for _, o := range omissions {
		if m, ok := o.(map[string]any); ok && m["kind"] == "utf16_decoded" {
			disclosed = true
		}
	}
	require.True(t, disclosed, "a decoded UTF-16 source must carry the utf16_decoded omission")
}

func TestGetEditingContext_ServesDecodedUTF16Source(t *testing.T) {
	srv, _ := utf16ServerWith(t)

	out := utf16JSON(t, callTool(t, srv, "get_editing_context", map[string]any{"path": "Legacy.cs"}))

	raw, _ := json.Marshal(out)
	require.NotContains(t, string(raw), "\u0000", "the editing context must not carry NUL-interleaved text")
}

func TestReadFile_ServesDecodedUTF16Source(t *testing.T) {
	srv, _ := utf16ServerWith(t)

	out := utf16JSON(t, callTool(t, srv, "read_file", map[string]any{"path": "Legacy.cs"}))

	content, _ := out["content"].(string)
	require.NotContains(t, content, "\x00")
	require.Contains(t, content, "public class Legacy")
}

func TestEditFile_RefusesUTF16SourceWithEncodingMessage(t *testing.T) {
	srv, _ := utf16ServerWith(t)

	res := callTool(t, srv, "edit_file", map[string]any{
		"path": "Legacy.cs", "old_string": "return 1;", "new_string": "return 2;",
	})
	require.True(t, res.IsError, "a UTF-16 source must be refused, not spliced")
	msg := res.Content[0].(mcplib.TextContent).Text
	require.Contains(t, msg, "UTF-16", "the refusal must name the encoding")
	require.NotContains(t, msg, "not found", "the old misleading failure mode must be gone")
}

func TestEditSymbol_RefusesUTF16SourceWithEncodingMessage(t *testing.T) {
	srv, _ := utf16ServerWith(t)
	node := utf16SymbolNamed(t, srv, "Legacy.cs", "B")

	res := callTool(t, srv, "edit_symbol", map[string]any{
		"id": node.ID, "old_source": "return 1;", "new_source": "return 2;",
	})
	require.True(t, res.IsError, "a UTF-16 source must be refused, not spliced")
	msg := res.Content[0].(mcplib.TextContent).Text
	require.Contains(t, msg, "UTF-16", "the refusal must name the encoding")
	require.NotContains(t, msg, "not found", "the old misleading failure mode must be gone")
}

func TestUTF16ControlFileStillEdits(t *testing.T) {
	srv, _ := utf16ServerWith(t)

	res := callTool(t, srv, "edit_file", map[string]any{
		"path": "Utf8.cs", "old_string": "}\n", "new_string": "} // touched\n",
	})
	require.False(t, res.IsError, "a plain UTF-8 file must still edit: %+v", res.Content)
}

func TestFileLooksUTF16_BoundedProbe(t *testing.T) {
	_, dir := utf16ServerWith(t)
	require.True(t, fileLooksUTF16(filepath.Join(dir, "Legacy.cs")))
	require.False(t, fileLooksUTF16(filepath.Join(dir, "Utf8.cs")))
	require.False(t, fileLooksUTF16(filepath.Join(dir, "missing.cs")))
}
