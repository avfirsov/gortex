package store_sqlite

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestMetaBlobCensus is a diagnostic probe, not a regression test: it
// reports per-key row counts and approximate byte shares of the residual
// meta BLOB over a store snapshot, overall and for method nodes. Skipped
// unless GORTEX_BENCH_STORE points at a copied store file.
func TestMetaBlobCensus(t *testing.T) {
	path := os.Getenv("GORTEX_BENCH_STORE")
	if path == "" {
		t.Skip("set GORTEX_BENCH_STORE to a copied store.sqlite to run")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	rows, err := s.db.Query(`SELECT kind, meta FROM nodes WHERE meta IS NOT NULL LIMIT 200000`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type agg struct {
		rows  int
		bytes int
	}
	overall := map[string]*agg{}
	methods := map[string]*agg{}
	var blobRows, blobBytes, methodRows, methodBytes, decodeFails int

	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		meta, err := decodeMeta(raw)
		if err != nil {
			decodeFails++
			continue
		}
		blobRows++
		blobBytes += len(raw)
		isMethod := kind == "method"
		if isMethod {
			methodRows++
			methodBytes += len(raw)
		}
		for k, v := range meta {
			sz := len(k) + len(fmt.Sprintf("%v", v))
			a := overall[k]
			if a == nil {
				a = &agg{}
				overall[k] = a
			}
			a.rows++
			a.bytes += sz
			if isMethod {
				m := methods[k]
				if m == nil {
					m = &agg{}
					methods[k] = m
				}
				m.rows++
				m.bytes += sz
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	dump := func(label string, nRows, nBytes int, m map[string]*agg) {
		t.Logf("== %s: %d rows with meta, %d blob bytes, %d decode failures ==", label, nRows, nBytes, decodeFails)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]].bytes > m[keys[j]].bytes })
		for i, k := range keys {
			if i >= 30 {
				t.Logf("... %d more keys", len(keys)-30)
				break
			}
			t.Logf("%-26s rows=%-8d bytes=%-10d", k, m[k].rows, m[k].bytes)
		}
	}
	dump("all kinds", blobRows, blobBytes, overall)
	dump("method nodes", methodRows, methodBytes, methods)
}
