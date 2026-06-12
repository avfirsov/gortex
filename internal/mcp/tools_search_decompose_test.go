package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/search/rerank"
)

// searchResp runs search_symbols and returns the decoded JSON response
// map so tests can inspect both the result rows and top-level flags
// (decomposed, filters_relaxed, ...).
func searchResp(t *testing.T, srv *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = "search_symbols"
	req.Params.Arguments = args
	res, err := srv.handleSearchSymbols(context.Background(), req)
	require.NoError(t, err)
	require.Falsef(t, res.IsError, "search errored: %v", res.Content)
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &resp))
	return resp
}

func respIDs(resp map[string]any) map[string]bool {
	ids := map[string]bool{}
	if results, ok := resp["results"].([]any); ok {
		for _, r := range results {
			ids[r.(map[string]any)["id"].(string)] = true
		}
	}
	return ids
}

// --- Unit tests for the decomposition helpers. ---

func TestQueryHasDecomposableSeparator(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"UserService.FindUser", true},
		{"internal/auth/token", true},
		{"pkg::Symbol", true},
		{"validate_user_token", true},
		{"FindUser", true},    // PascalCase internal boundary
		{"httpHandler", true}, // camelCase boundary
		{"flush the cache", false},
		{"validateuser", false}, // single lowercase word, no boundary
		{"auth", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := queryHasDecomposableSeparator(tc.q); got != tc.want {
			t.Fatalf("queryHasDecomposableSeparator(%q) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestDecomposeQueryToLeaves(t *testing.T) {
	// "UserService.FindUser" -> user, service, find (the second "user"
	// is deduped by the tokenizer).
	got := decomposeQueryToLeaves("UserService.FindUser")
	require.Equal(t, []string{"user", "service", "find"}, got)

	// Tiny tokens are dropped.
	got = decomposeQueryToLeaves("a.b.parse")
	require.Equal(t, []string{"parse"}, got)

	// A single short token decomposes to nothing (it would just
	// repeat the original miss).
	require.Nil(t, decomposeQueryToLeaves("ab"))

	// Sanity: the tokenizer is the same one the rest of search uses.
	require.Equal(t, rerank.Tokenize("FindUser"), []string{"find", "user"})
}

// --- Handler integration: the zero-result decomposition fallback. ---

// TestSearchSymbols_DecomposeRescuesCompoundMiss is the core feature
// test: a compound query whose full form finds nothing is rescued by
// decomposing it into leaf terms, one of which hits a real symbol.
func TestSearchSymbols_DecomposeRescuesCompoundMiss(t *testing.T) {
	srv := equivalenceTestServer(t, []string{
		"FetchProfile", "RenderTemplate", "UnrelatedThing",
	}, nil)

	// A single CamelCase token whose FULL form the backend finds
	// nothing for (the query tokenizer does not camelCase-split, so
	// "ZzqwxnomatchProfile" is one unknown posting), but which the
	// camelCase-aware leaf decomposition splits into
	// [zzqwxnomatch, profile] — and "profile" hits FetchProfile.
	resp := searchResp(t, srv, map[string]any{"query": "ZzqwxnomatchProfile"})
	ids := respIDs(resp)
	require.Truef(t, ids["pkg/FetchProfile.go::FetchProfile"],
		"camelCase decomposition should rescue Profile -> FetchProfile; got %v flags=%v", ids, resp["decomposed"])
	require.Equal(t, true, resp["decomposed"], "the rescued response must carry decomposed:true")
}

// TestSearchSymbols_DecomposeSkipsProseMiss confirms a prose-shaped
// miss (no separator, no camelCase boundary) does NOT trigger
// decomposition — the flag stays absent.
func TestSearchSymbols_DecomposeSkipsProseMiss(t *testing.T) {
	srv := equivalenceTestServer(t, []string{"FetchProfile"}, nil)
	resp := searchResp(t, srv, map[string]any{"query": "zzqwxnomatch"})
	require.Empty(t, respIDs(resp))
	require.Nil(t, resp["decomposed"], "a bare prose miss must not set decomposed")
}

// TestSearchSymbols_DecomposeNotSetOnHit confirms the flag is absent
// when the primary query already found results — decomposition is a
// zero-result-only fallback.
func TestSearchSymbols_DecomposeNotSetOnHit(t *testing.T) {
	srv := equivalenceTestServer(t, []string{"FetchProfile"}, nil)
	resp := searchResp(t, srv, map[string]any{"query": "FetchProfile"})
	require.NotEmpty(t, respIDs(resp))
	require.Nil(t, resp["decomposed"], "decomposed must be absent when the primary query hit")
}
