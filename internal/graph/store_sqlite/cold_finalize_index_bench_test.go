package store_sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

const benchmarkLegacyReceiverIndexDDL = `CREATE INDEX edges_go_member_receiver ON edges(member_receiver_dir, member_receiver, from_id, to_id, id) WHERE kind = 'member_of' AND member_receiver IS NOT NULL AND member_receiver_dir IS NOT NULL`

func seedResolverIndexBenchmark(b *testing.B, methodCount int) *Store {
	b.Helper()
	store, err := Open(filepath.Join(b.TempDir(), "resolver-index.sqlite"))
	if err != nil {
		b.Fatalf("open benchmark store: %v", err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Errorf("close benchmark store: %v", err)
		}
	})

	const packageCount = 32
	nodes := make([]*graph.Node, 0, packageCount+methodCount)
	for pkg := range packageCount {
		path := fmt.Sprintf("repo/pkg%02d/types.go", pkg)
		nodes = append(nodes, &graph.Node{
			ID: path + "::T", Kind: graph.KindType, Name: "T",
			FilePath: path, RepoPrefix: "repo", Language: "go",
		})
	}
	edges := make([]*graph.Edge, 0, methodCount*2)
	for i := range methodCount {
		pkg := i % packageCount
		path := fmt.Sprintf("repo/pkg%02d/methods_%05d.go", pkg, i)
		methodID := path + "::T.M"
		nodes = append(nodes, &graph.Node{
			ID: methodID, Kind: graph.KindMethod, Name: "M",
			FilePath: path, RepoPrefix: "repo", Language: "go",
		})
		edges = append(edges,
			&graph.Edge{From: methodID, To: path + "::T", Kind: graph.EdgeMemberOf, FilePath: path, Line: 1},
			&graph.Edge{From: methodID, To: fmt.Sprintf("unresolved::noise_%d", i), Kind: graph.EdgeCalls, FilePath: path, Line: 2},
		)
	}
	store.AddBatch(nodes, edges)
	if got := store.EdgeCount(); got != methodCount*2 {
		b.Fatalf("seed edge count=%d, want %d", got, methodCount*2)
	}
	return store
}

// BenchmarkSQLiteReceiverCandidateQuery compares the production kind-driven
// candidate generation with the removed covering index on the same corpus.
// Both execute the exact INSERT-SELECT used by receiver repair; candidate rows
// stay in SQLite and are cleared outside the timed region.
func BenchmarkSQLiteReceiverCandidateQuery(b *testing.B) {
	const methodCount = 8192
	store := seedResolverIndexBenchmark(b, methodCount)
	if _, err := store.writerDB.Exec(benchmarkLegacyReceiverIndexDDL); err != nil {
		b.Fatalf("create legacy receiver index: %v", err)
	}
	legacySQL := strings.Replace(
		goMethodReceiverCandidatesGlobalSQL,
		"INDEXED BY edges_by_kind",
		"INDEXED BY edges_go_member_receiver",
		1,
	)

	for _, bench := range []struct {
		name string
		sql  string
	}{
		{name: "kind_index", sql: goMethodReceiverCandidatesGlobalSQL},
		{name: "legacy_covering_index", sql: legacySQL},
	} {
		b.Run(bench.name, func(b *testing.B) {
			ctx := context.Background()
			conn, err := store.writerDB.Conn(ctx)
			if err != nil {
				b.Fatalf("acquire writer: %v", err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, goMethodReceiverCandidateTableSQL); err != nil {
				b.Fatalf("create candidate table: %v", err)
			}
			// database/sql may return the same physical connection across benchmark
			// calibration invocations, and TEMP tables follow that connection.
			if _, err := conn.ExecContext(ctx, `DELETE FROM temp.go_receiver_rebind_candidates`); err != nil {
				b.Fatalf("clear retained candidates: %v", err)
			}
			insertSQL := `INSERT INTO temp.go_receiver_rebind_candidates (edge_id, new_to) ` + bench.sql
			b.ReportAllocs()
			b.ReportMetric(methodCount, "candidates/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i > 0 {
					b.StopTimer()
					if _, err := conn.ExecContext(ctx, `DELETE FROM temp.go_receiver_rebind_candidates`); err != nil {
						b.Fatalf("clear candidates: %v", err)
					}
					b.StartTimer()
				}
				result, err := conn.ExecContext(ctx, insertSQL)
				if err != nil {
					b.Fatalf("collect candidates: %v", err)
				}
				got, err := result.RowsAffected()
				if err != nil || got != methodCount {
					b.Fatalf("candidate count=%d err=%v, want %d,nil", got, err, methodCount)
				}
			}
		})
	}
}

// BenchmarkSQLiteResolverIndexBuild isolates CREATE INDEX wall time over one
// stable mixed corpus. The dense v5 and removed receiver DDLs are retained only
// as benchmark controls; production creates only the unresolved partial index.
func BenchmarkSQLiteResolverIndexBuild(b *testing.B) {
	const methodCount = 16384
	store := seedResolverIndexBenchmark(b, methodCount)
	cases := []struct {
		name  string
		index string
		ddl   string
	}{
		{name: "unresolved_partial", index: "edges_by_unresolved", ddl: `CREATE INDEX edges_by_unresolved ON edges(is_unresolved) WHERE is_unresolved = 1`},
		{name: "unresolved_dense_v5", index: "edges_by_unresolved", ddl: `CREATE INDEX edges_by_unresolved ON edges(is_unresolved)`},
		{name: "receiver_covering_removed", index: "edges_go_member_receiver", ddl: benchmarkLegacyReceiverIndexDDL},
	}
	for _, bench := range cases {
		b.Run(bench.name, func(b *testing.B) {
			b.StopTimer()
			if _, err := store.writerDB.Exec(`DROP INDEX IF EXISTS ` + bench.index); err != nil {
				b.Fatalf("drop %s: %v", bench.index, err)
			}
			b.ReportMetric(float64(methodCount*2), "edges/op")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.writerDB.Exec(bench.ddl); err != nil {
					b.Fatalf("create %s: %v", bench.index, err)
				}
				b.StopTimer()
				if _, err := store.writerDB.Exec(`DROP INDEX IF EXISTS ` + bench.index); err != nil {
					b.Fatalf("drop %s after build: %v", bench.index, err)
				}
				b.StartTimer()
			}
		})
	}
}
