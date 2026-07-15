package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/daemon"
)

func TestRetryableProxyRequestMirrorsDaemonRawIDBoundary(t *testing.T) {
	t.Parallel()

	atLimit := `"` + strings.Repeat("a", daemon.MCPResponseCacheMaxIDBytes-2) + `"`
	if got := len(bytes.TrimSpace(json.RawMessage(atLimit))); got != daemon.MCPResponseCacheMaxIDBytes {
		t.Fatalf("boundary raw ID length = %d, want %d", got, daemon.MCPResponseCacheMaxIDBytes)
	}
	if !retryableProxyRequest(proxyReadRequestWithRawID(atLimit)) {
		t.Fatal("read request at daemon cache ID limit was not replayable")
	}

	overLimit := `"` + strings.Repeat("a", daemon.MCPResponseCacheMaxIDBytes-1) + `"`
	if got := len(bytes.TrimSpace(json.RawMessage(overLimit))); got != daemon.MCPResponseCacheMaxIDBytes+1 {
		t.Fatalf("oversized raw ID length = %d, want %d", got, daemon.MCPResponseCacheMaxIDBytes+1)
	}
	if retryableProxyRequest(proxyReadRequestWithRawID(overLimit)) {
		t.Fatal("read request above daemon cache ID limit was replayable")
	}
}

func TestRetryableProxyRequestMeasuresEscapedIDBeforeDecoding(t *testing.T) {
	t.Parallel()

	rawID := `"` + strings.Repeat(`\u0061`, daemon.MCPResponseCacheMaxIDBytes/6+1) + `"`
	var decoded string
	if err := json.Unmarshal([]byte(rawID), &decoded); err != nil {
		t.Fatalf("decode test ID: %v", err)
	}
	if len(rawID) <= daemon.MCPResponseCacheMaxIDBytes {
		t.Fatalf("raw escaped ID length = %d, want over %d", len(rawID), daemon.MCPResponseCacheMaxIDBytes)
	}
	if len(decoded) >= daemon.MCPResponseCacheMaxIDBytes {
		t.Fatalf("decoded ID length = %d, test must distinguish raw from decoded", len(decoded))
	}
	if retryableProxyRequest(proxyReadRequestWithRawID(rawID)) {
		t.Fatal("raw oversized escaped ID was replayable after decoding shorter")
	}
}

func proxyReadRequestWithRawID(rawID string) []byte {
	frame := make([]byte, 0, len(rawID)+128)
	frame = append(frame, `{"jsonrpc":"2.0","id":   `...)
	frame = append(frame, rawID...)
	frame = append(frame, `   ,"method":"tools/call","params":{"name":"read","arguments":{"operation":"source"}}}`...)
	return frame
}
