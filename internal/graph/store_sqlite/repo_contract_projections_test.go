package store_sqlite

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestSQLiteRepoContractProjectionsStayInRepositoryWorkspace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	store.AddBatch([]*graph.Node{
		{ID: "a-py", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/main.py", Language: "python"},
		{ID: "a-ts", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/mount.ts", Language: "typescript"},
		{ID: "a-other-ws", Kind: graph.KindFile, RepoPrefix: "a", WorkspaceID: "other", FilePath: "a/other.py", Language: "python"},
		{ID: "b-py", Kind: graph.KindFile, RepoPrefix: "b", WorkspaceID: "ws", FilePath: "b/main.py", Language: "python"},
		{ID: "a-reader", Kind: graph.KindType, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/R.java", Meta: map[string]any{"spring_config_keys": []string{"app.name"}}},
		{ID: "a-no-hint", Kind: graph.KindType, RepoPrefix: "a", WorkspaceID: "ws", FilePath: "a/N.java", Meta: map[string]any{"other": true}},
		{ID: "b-reader", Kind: graph.KindType, RepoPrefix: "b", WorkspaceID: "ws", FilePath: "b/R.java", Meta: map[string]any{"spring_config_keys": []string{"app.name"}}},
	}, nil)

	if got, want := store.RepoFilePaths("a", "ws", []string{"python"}, []string{".ts"}), []string{"a/main.py", "a/mount.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoFilePaths = %#v, want %#v", got, want)
	}
	readers := store.RepoNodesByKindsWithMetaKey("a", "ws", []graph.NodeKind{graph.KindType, graph.KindField}, "spring_config_keys")
	if len(readers) != 1 || readers[0].ID != "a-reader" {
		t.Fatalf("RepoNodesByKindsWithMetaKey = %#v, want a-reader only", readers)
	}

	rows, err := store.db.Query(`EXPLAIN QUERY PLAN
SELECT DISTINCT COALESCE(NULLIF(n.file_path, ''), n.id)
FROM nodes AS n
WHERE n.repo_prefix = ? AND n.kind = 'file' AND n.workspace_id = ?
  AND EXISTS (SELECT 1 FROM json_each(?) AS lang WHERE lower(n.language) = lower(CAST(lang.value AS TEXT)))`, "a", "ws", `["python"]`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "nodes_repo_files") {
		t.Fatalf("file projection plan does not use nodes_repo_files:\n%s", plan.String())
	}
}

func TestSQLiteEvictConfigNodesByIDsIsKindGuardedAndSetOriented(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.AddBatch([]*graph.Node{
		{ID: "reader", Kind: graph.KindType},
		{ID: "legacy", Kind: graph.KindConfigKey},
		{ID: "keep", Kind: graph.KindType},
	}, []*graph.Edge{
		{From: "reader", To: "legacy", Kind: graph.EdgeReadsConfig},
		{From: "keep", To: "reader", Kind: graph.EdgeCalls},
	})

	nodes, edges := store.EvictConfigNodesByIDs([]string{"legacy", "legacy", "keep"})
	if nodes != 1 || edges != 1 {
		t.Fatalf("EvictConfigNodesByIDs = (%d,%d), want (1,1)", nodes, edges)
	}
	if store.GetNode("legacy") != nil || store.GetNode("keep") == nil {
		t.Fatalf("kind guard failed: legacy=%#v keep=%#v", store.GetNode("legacy"), store.GetNode("keep"))
	}
}
