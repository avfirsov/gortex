package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCompressTestServer wires a fresh test server, then drops a
// service.go fixture into the indexer's existing root and re-indexes
// it so the file is present in the graph alongside the harness's
// default main.go.
func setupCompressTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, dir := setupTestServer(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.go"), []byte(`package service

import (
	"errors"
	"strings"
)

// MaxParts is the maximum JWT segment count.
const MaxParts = 3

var ErrMalformed = errors.New("malformed")

// Claims captures the parsed JWT body.
type Claims struct {
	Subject string
	Issuer  string
}

// ValidateToken splits the token, validates the segment count, and
// returns a Claims pointer on success.
func ValidateToken(t string) (*Claims, error) {
	parts := strings.Split(t, ".")
	if len(parts) != MaxParts {
		return nil, ErrMalformed
	}
	return &Claims{Subject: parts[0], Issuer: parts[1]}, nil
}

func (c *Claims) String() string {
	return c.Subject + "@" + c.Issuer
}
`), 0o644))
	require.NoError(t, srv.indexer.IndexFile(filepath.Join(dir, "service.go")))
	srv.RunAnalysis()
	return srv, dir
}

// extractTextResult pulls the JSON-encoded body out of a tool result.
func extractTextResult(t *testing.T, r *mcplib.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, r.IsError, "unexpected tool error: %+v", r.Content)
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(mcplib.TextContent)
	require.True(t, ok, "expected TextContent, got %T", r.Content[0])
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func TestGetSymbolSource_CompressBodies(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	_ = dir

	// Look up the symbol ID first so the test doesn't bake in the
	// path-prefix layout. ValidateToken is the function with the
	// body we want elided.
	var symbolID string
	for _, n := range srv.graph.AllNodes() {
		if n.Name == "ValidateToken" && (n.Kind == "function" || n.Kind == "method") {
			symbolID = n.ID
			break
		}
	}
	require.NotEmpty(t, symbolID, "ValidateToken symbol not indexed")

	// Baseline: without the flag, the body is in the response.
	baseline := callTool(t, srv, "get_symbol_source", map[string]any{
		"id":            symbolID,
		"context_lines": float64(0),
	})
	baseMap := extractTextResult(t, baseline)
	baseSource, _ := baseMap["source"].(string)
	assert.Contains(t, baseSource, `strings.Split(t, ".")`)
	assert.Contains(t, baseSource, "ErrMalformed")

	// With the flag, the body should be replaced by the stub.
	elided := callTool(t, srv, "get_symbol_source", map[string]any{
		"id":              symbolID,
		"context_lines":   float64(0),
		"compress_bodies": true,
	})
	elidedMap := extractTextResult(t, elided)
	elidedSource, _ := elidedMap["source"].(string)

	assert.NotContains(t, elidedSource, `strings.Split(t, ".")`,
		"compressed source should not contain the function body")
	assert.Contains(t, elidedSource, "func ValidateToken",
		"signature must survive elision")
	assert.Contains(t, elidedSource, "lines elided",
		"stub marker must appear")
	assert.True(t, len(elidedSource) < len(baseSource),
		"compressed length %d should be < baseline length %d",
		len(elidedSource), len(baseSource))
	assert.Equal(t, true, elidedMap["bodies_elided"],
		"response must flag that compression happened")
}

func TestGetSymbolSource_CompressBodies_NoOpOnType(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	// A type declaration has no function body — the elider should
	// return the source unchanged and bodies_elided should be absent
	// or false.
	var typeID string
	for _, n := range srv.graph.AllNodes() {
		if n.Name == "Claims" && n.Kind == "type" {
			typeID = n.ID
			break
		}
	}
	require.NotEmpty(t, typeID, "Claims type not indexed")
	r := callTool(t, srv, "get_symbol_source", map[string]any{
		"id":              typeID,
		"compress_bodies": true,
	})
	m := extractTextResult(t, r)
	src, _ := m["source"].(string)
	assert.Contains(t, src, "type Claims struct")
	assert.Contains(t, src, "Subject string")
	// No function body to elide → no flag.
	_, hasFlag := m["bodies_elided"]
	assert.False(t, hasFlag, "bodies_elided should be absent when no elision happened")
}

func TestGetEditingContext_CompressBodies(t *testing.T) {
	srv, _ := setupCompressTestServer(t)

	// Baseline: no flag → no source_compressed field.
	base := callTool(t, srv, "get_editing_context", map[string]any{
		"path": "service.go",
	})
	baseMap := extractTextResult(t, base)
	_, hasBaseSrc := baseMap["source_compressed"]
	assert.False(t, hasBaseSrc, "source_compressed should be absent without the flag")

	// With flag.
	r := callTool(t, srv, "get_editing_context", map[string]any{
		"path":            "service.go",
		"compress_bodies": true,
	})
	m := extractTextResult(t, r)

	src, _ := m["source_compressed"].(string)
	require.NotEmpty(t, src, "source_compressed must be populated when compress_bodies=true")
	// File-level: signatures, constants, types survive.
	assert.Contains(t, src, "package service")
	assert.Contains(t, src, "const MaxParts = 3")
	assert.Contains(t, src, "type Claims struct")
	assert.Contains(t, src, "Subject string")
	assert.Contains(t, src, "func ValidateToken")
	assert.Contains(t, src, "func (c *Claims) String()")
	assert.Contains(t, src, "lines elided")
	// Bodies do not.
	assert.NotContains(t, src, `strings.Split(t, ".")`)
	assert.NotContains(t, src, `c.Subject + "@" + c.Issuer`)
	assert.Equal(t, true, m["bodies_elided"])
}

func TestReadFile_BaselineAndCompressed(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	_ = dir

	// Baseline: full source returned.
	base := callTool(t, srv, "read_file", map[string]any{
		"path": "service.go",
	})
	baseMap := extractTextResult(t, base)
	baseContent, _ := baseMap["content"].(string)
	assert.Contains(t, baseContent, `strings.Split(t, ".")`)
	assert.Contains(t, baseContent, "func ValidateToken")
	_, hasFlag := baseMap["bodies_elided"]
	assert.False(t, hasFlag)
	assert.Equal(t, "go", baseMap["language"])

	// Compressed.
	r := callTool(t, srv, "read_file", map[string]any{
		"path":            "service.go",
		"compress_bodies": true,
	})
	m := extractTextResult(t, r)
	compressed, _ := m["content"].(string)
	assert.NotContains(t, compressed, `strings.Split(t, ".")`)
	assert.Contains(t, compressed, "func ValidateToken")
	assert.Contains(t, compressed, "lines elided")
	assert.Equal(t, true, m["bodies_elided"])
	assert.True(t, len(compressed) < len(baseContent),
		"compressed (%d bytes) should be smaller than baseline (%d bytes)",
		len(compressed), len(baseContent))
}

func TestReadFile_UnsupportedLanguage(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	// Drop a fake unindexed config file.
	cfgPath := filepath.Join(dir, "config.unknown")
	_ = os.WriteFile(cfgPath, []byte("key=value\nother=42\n"), 0o644)

	r := callTool(t, srv, "read_file", map[string]any{
		"path":            "config.unknown",
		"compress_bodies": true,
	})
	m := extractTextResult(t, r)
	content, _ := m["content"].(string)
	// Unsupported language → falls back to raw content, no flag set.
	assert.Equal(t, "key=value\nother=42\n", content)
	_, hasFlag := m["bodies_elided"]
	assert.False(t, hasFlag, "bodies_elided must not be set when language is unsupported")
}

func TestReadFile_EtagRoundtrip(t *testing.T) {
	srv, _ := setupCompressTestServer(t)
	r1 := callTool(t, srv, "read_file", map[string]any{
		"path": "service.go",
	})
	m1 := extractTextResult(t, r1)
	etag, _ := m1["etag"].(string)
	require.NotEmpty(t, etag)

	// Second call with if_none_match should not_modify.
	r2 := callTool(t, srv, "read_file", map[string]any{
		"path":          "service.go",
		"if_none_match": etag,
	})
	m2 := extractTextResult(t, r2)
	notMod, _ := m2["not_modified"].(bool)
	assert.True(t, notMod, "etag round-trip must short-circuit; got payload: %v", m2)
	assert.Equal(t, etag, m2["etag"], "returned etag must match the input if_none_match")
}

func TestReadFile_DirectoryRejected(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	r := callTool(t, srv, "read_file", map[string]any{
		"path": filepath.Base(dir),
	})
	assert.True(t, r.IsError, "expected error when path is a directory")
}

// TestCompressBodies_Acceptance is the spec-level acceptance check:
// a 200-line file should compress to ≤ 60 lines.
func TestCompressBodies_Acceptance(t *testing.T) {
	srv, dir := setupCompressTestServer(t)

	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; i < 18; i++ {
		b.WriteString("// helper")
		b.WriteString(itoaInt(i))
		b.WriteString(" does some work.\n")
		b.WriteString("func helper")
		b.WriteString(itoaInt(i))
		b.WriteString("(x int) int {\n")
		for j := 0; j < 9; j++ {
			b.WriteString("\tx = x + 1\n")
		}
		b.WriteString("\treturn x\n")
		b.WriteString("}\n\n")
	}
	src := b.String()
	require.GreaterOrEqual(t, strings.Count(src, "\n"), 200,
		"fixture must exceed 200 lines")

	path := filepath.Join(dir, "big.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	require.NoError(t, srv.indexer.IndexFile(path))

	r := callTool(t, srv, "read_file", map[string]any{
		"path":            "big.go",
		"compress_bodies": true,
	})
	m := extractTextResult(t, r)
	content, _ := m["content"].(string)
	lines := strings.Count(content, "\n")
	require.LessOrEqual(t, lines, 60,
		"acceptance: 200+ line file must compress to ≤ 60 lines, got %d", lines)
}

func TestGetEditingContext_CompressBodies_Keep(t *testing.T) {
	srv, _ := setupCompressTestServer(t)

	// keep=ValidateToken pins that function's body to verbatim source
	// while the rest of service.go is still stubbed.
	r := callTool(t, srv, "get_editing_context", map[string]any{
		"path":            "service.go",
		"compress_bodies": true,
		"keep":            "ValidateToken",
	})
	m := extractTextResult(t, r)
	src, _ := m["source_compressed"].(string)
	require.NotEmpty(t, src, "source_compressed must be populated")

	// ValidateToken body kept verbatim.
	assert.Contains(t, src, `strings.Split(t, ".")`,
		"kept symbol body must survive")
	// String() body still elided.
	assert.NotContains(t, src, `c.Subject + "@" + c.Issuer`,
		"non-kept symbol body must still be stubbed")
	assert.Contains(t, src, "lines elided",
		"non-kept symbols must still produce a stub")
	assert.Equal(t, true, m["bodies_elided"])

	// The response reports which symbols stayed verbatim.
	kept, _ := m["kept_symbols"].([]any)
	require.Len(t, kept, 1)
	assert.Equal(t, "ValidateToken", kept[0])
}

func TestGetEditingContext_CompressBodies_KeepByKind(t *testing.T) {
	srv, _ := setupCompressTestServer(t)

	// keep=method keeps every method's body; the plain function
	// ValidateToken is still compressed.
	r := callTool(t, srv, "get_editing_context", map[string]any{
		"path":            "service.go",
		"compress_bodies": true,
		"keep":            "method",
	})
	m := extractTextResult(t, r)
	src, _ := m["source_compressed"].(string)
	require.NotEmpty(t, src)

	assert.Contains(t, src, `c.Subject + "@" + c.Issuer`,
		"method body must survive when keep=method")
	assert.NotContains(t, src, `strings.Split(t, ".")`,
		"plain function must still be compressed when keep=method")
}

func TestReadFile_CompressBodies_Keep(t *testing.T) {
	srv, _ := setupCompressTestServer(t)

	r := callTool(t, srv, "read_file", map[string]any{
		"path":            "service.go",
		"compress_bodies": true,
		"keep":            "ValidateToken",
	})
	m := extractTextResult(t, r)
	content, _ := m["content"].(string)

	assert.Contains(t, content, `strings.Split(t, ".")`,
		"kept symbol body must survive in read_file")
	assert.NotContains(t, content, `c.Subject + "@" + c.Issuer`,
		"non-kept body must still be stubbed in read_file")
	assert.Equal(t, true, m["bodies_elided"])
	kept, _ := m["kept_symbols"].([]any)
	require.Len(t, kept, 1)
	assert.Equal(t, "ValidateToken", kept[0])
}

func TestGetSymbolSource_MaxLines(t *testing.T) {
	srv, dir := setupCompressTestServer(t)

	// A function long enough to bust a small max_lines budget, with
	// an if and a for so there is a skeleton worth keeping.
	var b strings.Builder
	b.WriteString("package big\n\nfunc Crunch(n int) int {\n\ttotal := 0\n")
	for i := 0; i < 20; i++ {
		b.WriteString("\ttotal = total + ")
		b.WriteString(itoaInt(i))
		b.WriteString("\n")
	}
	b.WriteString("\tif total > 100 {\n\t\ttotal = total * 2\n\t}\n")
	b.WriteString("\tfor i := 0; i < n; i++ {\n\t\ttotal = total - i\n\t}\n")
	b.WriteString("\treturn total\n}\n")
	path := filepath.Join(dir, "crunch.go")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	require.NoError(t, srv.indexer.IndexFile(path))

	var symbolID string
	for _, n := range srv.graph.AllNodes() {
		if n.Name == "Crunch" && n.Kind == "function" {
			symbolID = n.ID
			break
		}
	}
	require.NotEmpty(t, symbolID, "Crunch symbol not indexed")

	r := callTool(t, srv, "get_symbol_source", map[string]any{
		"id":            symbolID,
		"context_lines": float64(0),
		"max_lines":     float64(15),
	})
	m := extractTextResult(t, r)
	src, _ := m["source"].(string)
	assert.Contains(t, src, "func Crunch(n int) int {", "signature must survive")
	assert.Contains(t, src, "if total > 100 {", "control flow must survive")
	assert.Contains(t, src, "for i := 0; i < n; i++ {", "control flow must survive")
	assert.Contains(t, src, "return total", "return must survive")
	assert.Contains(t, src, "lines elided", "leaf runs must be collapsed")
	assert.NotContains(t, src, "total = total + 7", "a leaf statement must be dropped")
	assert.Equal(t, true, m["salience_truncated"], "response must flag salience truncation")
}

func TestReadFile_MaxLines(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	var b strings.Builder
	b.WriteString("package big\n\nfunc Walk(n int) int {\n\tx := 0\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\tx = x + ")
		b.WriteString(itoaInt(i))
		b.WriteString("\n")
	}
	b.WriteString("\tif x > 0 {\n\t\tx = x - 1\n\t}\n\treturn x\n}\n")
	path := filepath.Join(dir, "walk.go")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	require.NoError(t, srv.indexer.IndexFile(path))

	r := callTool(t, srv, "read_file", map[string]any{
		"path":      "walk.go",
		"max_lines": float64(20),
	})
	m := extractTextResult(t, r)
	content, _ := m["content"].(string)
	assert.Contains(t, content, "func Walk(n int) int {")
	assert.Contains(t, content, "if x > 0 {")
	assert.Contains(t, content, "return x")
	assert.Contains(t, content, "lines elided")
	assert.NotContains(t, content, "x = x + 15", "a leaf statement must be dropped")
	assert.Equal(t, true, m["salience_truncated"])
}

func TestReadFile_MaxLines_NonCodeHeadCut(t *testing.T) {
	srv, dir := setupCompressTestServer(t)
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("log line ")
		b.WriteString(itoaInt(i))
		b.WriteString("\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte(b.String()), 0o644))

	r := callTool(t, srv, "read_file", map[string]any{
		"path":      "app.log",
		"max_lines": float64(10),
	})
	m := extractTextResult(t, r)
	content, _ := m["content"].(string)
	// A non-code file cannot be parsed — max_lines falls back to a
	// plain head cut with a marker.
	assert.Contains(t, content, "log line 0")
	assert.NotContains(t, content, "log line 40", "the tail must be cut")
	assert.Contains(t, content, "max_lines budget")
	assert.Equal(t, true, m["salience_truncated"])
}

// itoaInt is a tiny stdlib-free int-to-string for fixture builders.
func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
