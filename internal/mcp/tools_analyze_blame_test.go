package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/graph"
)

func TestAnalyzeBlame_StampsLastAuthored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	srv, dir := setupTestServer(t)

	// setupTestServer wrote main.go but not a git repo; turn the
	// indexed dir into a real repo so blame has something to chew
	// on.
	for _, args := range [][]string{
		{"git", "init", "-q"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Tester"},
		{"git", "add", "main.go"},
		{"git", "commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Tester",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Tester",
			"GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	req := mcplib.CallToolRequest{}
	req.Params.Name = "analyze"
	req.Params.Arguments = map[string]any{"kind": "blame"}
	res, err := srv.handleAnalyze(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAnalyze: %v", err)
	}
	if res.IsError {
		t.Fatalf("error: %+v", res.Content)
	}
	textBlock, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textBlock.Text), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, textBlock.Text)
	}
	enriched, _ := out["enriched"].(float64)
	if enriched < 1 {
		t.Errorf("expected at least 1 enriched node, got %v\n%s", enriched, textBlock.Text)
	}

	// Spot-check at least one symbol got authorship metadata.
	// blame now persists in the typed sidecar (change A), not Node.Meta.
	found := false
	if r, ok := srv.graph.(graph.BlameEnrichmentReader); ok {
		for _, e := range r.BlameRows("") {
			if e.Email == "test@example.com" {
				found = true
				break
			}
		}
	}
	if !found {
		// Fallback for capability-less backends: scan Meta.
		for _, n := range srv.graph.AllNodes() {
			if la, ok := n.Meta["last_authored"].(map[string]any); ok && la["email"] == "test@example.com" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no function/method node carries last_authored.email = test@example.com")
	}
}
