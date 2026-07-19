package store_sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// entry_point / entry_point_kind are promoted columns: stored typed (blob
// stripped), restored into Meta on read, and readable by the analysis
// preflights without touching the blob.
func TestEntryPointPromotionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ep.sqlite")
	s, err := Open(path)
	require.NoError(t, err)

	s.AddNode(&graph.Node{
		ID: "main.go::main", Kind: graph.KindFunction, Name: "main",
		FilePath: "main.go", Language: "go",
		Meta: map[string]any{"entry_point": true, "entry_point_kind": "cli", "other": "kept"},
	})

	var ep sql.NullBool
	var epKind sql.NullString
	var blob []byte
	require.NoError(t, s.db.QueryRow(
		`SELECT entry_point, entry_point_kind, meta FROM nodes WHERE id = ?`, "main.go::main",
	).Scan(&ep, &epKind, &blob))
	require.True(t, ep.Valid && ep.Bool, "entry_point must land in its column")
	require.Equal(t, "cli", epKind.String, "entry_point_kind must land in its column")
	decoded, err := decodeMeta(blob)
	require.NoError(t, err)
	require.NotContains(t, decoded, "entry_point", "promoted key must be stripped from the blob")
	require.NotContains(t, decoded, "entry_point_kind", "promoted key must be stripped from the blob")
	require.Equal(t, "kept", decoded["other"])

	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s2.Close()) })
	n := s2.GetNode("main.go::main")
	require.NotNil(t, n)
	require.Equal(t, true, n.Meta["entry_point"], "restored Meta must carry the flag")
	require.Equal(t, "cli", n.Meta["entry_point_kind"])
	require.Equal(t, "kept", n.Meta["other"])
}

// The retrieval payload (search_* triplet + suppression flag + section_text)
// is promoted to post-meta columns: blob-free storage, byte-identical Meta
// after restore, and the light projection keeps excluding it.
func TestRetrievalPayloadPromotionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rp.sqlite")
	s, err := Open(path)
	require.NoError(t, err)

	s.AddNode(&graph.Node{
		ID: "doc.md::Sect", Kind: graph.KindDoc, Name: "Sect",
		FilePath: "doc.md", Language: "markdown",
		Meta: map[string]any{
			"search_signature": "sig-proj", "search_qual_name": "qn-proj",
			"search_doc": "doc-proj", "section_text": "full section body",
			"other": "kept",
		},
	})

	var blob []byte
	var sig, sect sql.NullString
	require.NoError(t, s.db.QueryRow(
		`SELECT meta, search_signature, section_text FROM nodes WHERE id = ?`, "doc.md::Sect",
	).Scan(&blob, &sig, &sect))
	require.Equal(t, "sig-proj", sig.String)
	require.Equal(t, "full section body", sect.String)
	decoded, err := decodeMeta(blob)
	require.NoError(t, err)
	for _, k := range []string{"search_signature", "search_qual_name", "search_doc", "section_text"} {
		require.NotContains(t, decoded, k, "promoted retrieval key %s must leave the blob", k)
	}

	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s2.Close()) })
	n := s2.GetNode("doc.md::Sect")
	require.NotNil(t, n)
	require.Equal(t, "sig-proj", n.Meta["search_signature"])
	require.Equal(t, "qn-proj", n.Meta["search_qual_name"])
	require.Equal(t, "doc-proj", n.Meta["search_doc"])
	require.Equal(t, "full section body", n.Meta["section_text"])
	require.Equal(t, "kept", n.Meta["other"])
	light := s2.GetRepoNodesLight("")
	for _, ln := range light {
		if ln.ID == "doc.md::Sect" {
			require.NotContains(t, ln.Meta, "section_text", "light projection must stay lean")
			require.NotContains(t, ln.Meta, "search_signature", "light projection must stay lean")
		}
	}
}
