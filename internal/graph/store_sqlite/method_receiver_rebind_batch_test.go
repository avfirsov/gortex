package store_sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

func TestRebindGoMethodReceiversForFilesScopesAndDedupes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "receiver-batch.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	canonical := &graph.Node{ID: "pkg/type.go::Thing", Kind: graph.KindType, Name: "Thing", FilePath: "pkg/type.go", Language: "go", RepoPrefix: "repo"}
	methodA := &graph.Node{ID: "pkg/a.go::Thing.A", Kind: graph.KindMethod, Name: "A", FilePath: "pkg/a.go", Language: "go", RepoPrefix: "repo"}
	methodB := &graph.Node{ID: "pkg/b.go::Thing.B", Kind: graph.KindMethod, Name: "B", FilePath: "pkg/b.go", Language: "go", RepoPrefix: "repo"}
	untouched := &graph.Node{ID: "pkg/c.go::Thing.C", Kind: graph.KindMethod, Name: "C", FilePath: "pkg/c.go", Language: "go", RepoPrefix: "repo"}
	store.AddBatch([]*graph.Node{canonical, methodA, methodB, untouched}, []*graph.Edge{
		{From: methodA.ID, To: "pkg/a.go::Thing", Kind: graph.EdgeMemberOf, FilePath: "pkg/a.go", Line: 2},
		{From: methodB.ID, To: "pkg/b.go::Thing", Kind: graph.EdgeMemberOf, FilePath: "pkg/b.go", Line: 2},
		{From: untouched.ID, To: "pkg/c.go::Thing", Kind: graph.EdgeMemberOf, FilePath: "pkg/c.go", Line: 2},
	})

	store.db.SetMaxOpenConns(1)
	heldReader, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer heldReader.Close() //nolint:errcheck // explicit close below; cleanup safety
	type result struct {
		changed int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		changed, rebindErr := store.RebindGoMethodReceiversForFiles([]string{"pkg/a.go", "pkg/b.go", "pkg/a.go", ""})
		done <- result{changed: changed, err: rebindErr}
	}()
	var changed int
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		changed = got.changed
	case <-time.After(2 * time.Second):
		t.Fatal("batch receiver rebind waited for saturated reader pool")
	}
	if err := heldReader.Close(); err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("changed = %d, want 2", changed)
	}
	for _, methodID := range []string{methodA.ID, methodB.ID} {
		out := store.GetOutEdges(methodID)
		if len(out) != 1 || out[0].To != canonical.ID {
			t.Fatalf("%s edges = %#v, want canonical receiver %s", methodID, out, canonical.ID)
		}
	}
	out := store.GetOutEdges(untouched.ID)
	if len(out) != 1 || out[0].To != "pkg/c.go::Thing" {
		t.Fatalf("untouched file changed: %#v", out)
	}
}
