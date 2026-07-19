package store_sqlite

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestSemanticBindingWritersAndEvictionUsePinnedBulkConnection(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	// Pin the only database connection. Any writer that bypasses beginWrite or
	// the active bulk connection will wait forever on the pool.
	store.db.SetMaxOpenConns(1)
	store.db.SetMaxIdleConns(1)
	store.BeginBulkLoad()
	require.NotNil(t, store.bulkConn, "empty on-disk store must enter bulk mode")

	a := graph.SemanticBindingSite{RepoPrefix: "repo", FilePath: "repo/a.go", Line: 1, Name: "a"}
	b := graph.SemanticBindingSite{RepoPrefix: "repo", FilePath: "repo/b.go", Line: 2, Name: "b"}
	c := graph.SemanticBindingSite{RepoPrefix: "repo", FilePath: "repo/c.go", Line: 3, Name: "c"}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("panic: %v", recovered)
			}
		}()
		if err := store.ReplaceSemanticBindingTypes("repo", []graph.SemanticBindingType{
			{Site: a, TypeName: "A"},
			{Site: b, TypeName: "B"},
		}); err != nil {
			done <- err
			return
		}
		if err := store.ReplaceSemanticBindingTypesForFiles("repo", []string{a.FilePath}, []graph.SemanticBindingType{{Site: a, TypeName: "A2"}}); err != nil {
			done <- err
			return
		}
		if err := store.DeleteSemanticBindingTypesByFiles("repo", []string{b.FilePath}); err != nil {
			done <- err
			return
		}

		store.AddBatch([]*graph.Node{{
			ID: "repo/a.go::A", Kind: graph.KindFunction, Name: "A",
			FilePath: a.FilePath, RepoPrefix: "repo",
		}}, nil)
		if nodes, _ := store.EvictFile(a.FilePath); nodes != 1 {
			done <- fmt.Errorf("EvictFile removed %d nodes, want 1", nodes)
			return
		}

		if err := store.ReplaceSemanticBindingTypesForFiles("repo", []string{c.FilePath}, []graph.SemanticBindingType{{Site: c, TypeName: "C"}}); err != nil {
			done <- err
			return
		}
		store.AddBatch([]*graph.Node{{
			ID: "repo/c.go::C", Kind: graph.KindFunction, Name: "C",
			FilePath: c.FilePath, RepoPrefix: "repo",
		}}, nil)
		if nodes, _ := store.EvictRepo("repo"); nodes != 1 {
			done <- fmt.Errorf("EvictRepo removed %d nodes, want 1", nodes)
			return
		}
		done <- nil
	}()

	var operationErr error
	timedOut := false
	select {
	case operationErr = <-done:
	case <-time.After(5 * time.Second):
		timedOut = true
		// Unpin a second pool slot so a regressed implementation can unwind and
		// the test can clean up instead of leaking a permanently blocked goroutine.
		store.db.SetMaxOpenConns(2)
		operationErr = <-done
	}
	require.NoError(t, store.FlushBulk())
	if timedOut {
		t.Fatalf("semantic binding lifecycle deadlocked with a pinned bulk connection: %v", operationErr)
	}
	require.NoError(t, operationErr)

	got, err := store.SemanticBindingTypes([]graph.SemanticBindingSite{a, b, c})
	require.NoError(t, err)
	assert.Empty(t, got, "file and repo eviction must clear binding rows on the active writer")
}
