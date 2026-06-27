package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/daemon"
)

func readFileResultMap(t *testing.T, srv *Server, ctx context.Context, path string) map[string]any {
	t.Helper()
	res := callToolByName(t, srv, ctx, "read_file", map[string]any{"path": path})
	require.False(t, res.IsError, "read_file: %s", toolText(res))
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(toolText(res)), &out))
	return out
}

// TestReadFile_OverlayDriftSurfacesError pins the existing safety: when an
// editor-buffer overlay has drifted from disk (its captured base no longer
// matches), read_file must not serve the stale buffer — the overlay view guard
// turns the call into a drift error so the client re-reads, exactly as the
// graph tools do.
func TestReadFile_OverlayDriftSurfacesError(t *testing.T) {
	srv, _, targetFile, _ := setupOverlayServer(t)
	sessID := "rf-drift"
	require.NoError(t, srv.OverlayManager().RegisterWithID(sessID, ""))
	require.NoError(t, srv.OverlayManager().Push(sessID, daemon.OverlayFile{
		Path:    targetFile,
		Content: "package main\n\nfunc OverlayOnly() {}\n",
		BaseSHA: "0000000000000000000000000000000000000000", // never matches disk → drift
	}, nil))

	ctx := WithSessionID(context.Background(), sessID)
	res := callToolByName(t, srv, ctx, "read_file", map[string]any{"path": filepath.Base(targetFile)})
	require.True(t, res.IsError, "a drifted overlay must not be served silently")
	assert.Contains(t, toolText(res), "overlay base SHA mismatch")
}

// TestReadFile_FreshOverlayIsServed: a non-drifted overlay (its base matches
// disk) is still served as the buffer view, flagged so the agent knows it is
// reading an editor buffer rather than the file currently on disk.
func TestReadFile_FreshOverlayIsServed(t *testing.T) {
	srv, _, targetFile, _ := setupOverlayServer(t)

	data, err := os.ReadFile(targetFile)
	require.NoError(t, err)
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	baseSHA := hex.EncodeToString(h.Sum(nil))

	sessID := "rf-fresh"
	require.NoError(t, srv.OverlayManager().RegisterWithID(sessID, ""))
	require.NoError(t, srv.OverlayManager().Push(sessID, daemon.OverlayFile{
		Path:    targetFile,
		Content: "package main\n\nfunc Target() {}\n\nfunc OverlayOnly() {}\n",
		BaseSHA: baseSHA,
	}, nil))

	ctx := WithSessionID(context.Background(), sessID)
	out := readFileResultMap(t, srv, ctx, filepath.Base(targetFile))

	content, _ := out["content"].(string)
	assert.Contains(t, content, "OverlayOnly", "the fresh overlay buffer is served")
	assert.Equal(t, "overlay", out["served_from"], "served_from flags the overlay provenance")
	assert.Nil(t, out["overlay_bypassed"], "no drift bypass on a fresh overlay")
}
