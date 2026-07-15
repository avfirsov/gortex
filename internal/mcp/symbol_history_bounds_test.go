package mcp

import (
	"fmt"
	"testing"
)

func TestSymbolHistoryBoundsPerSymbol(t *testing.T) {
	history := &symbolHistory{}
	for i := 0; i < maxSymbolHistoryPerSymbol+17; i++ {
		history.Record("hot-symbol", i%2 == 0)
	}

	mods := history.Get("hot-symbol")
	if len(mods) != maxSymbolHistoryPerSymbol {
		t.Fatalf("modifications = %d, want %d", len(mods), maxSymbolHistoryPerSymbol)
	}
	all := history.All()
	if len(all) != 1 || len(all["hot-symbol"]) != maxSymbolHistoryPerSymbol {
		t.Fatalf("unexpected bounded snapshot: %#v", all)
	}
}

func TestSymbolHistoryBoundsAggregateAndEvictsLRU(t *testing.T) {
	history := &symbolHistory{}
	for i := 0; i < maxSymbolHistorySymbols; i++ {
		history.Record(fmt.Sprintf("symbol-%03d", i), false)
	}

	// Refresh the first symbol so adding one more evicts symbol-001 instead.
	history.Record("symbol-000", true)
	history.Record("symbol-new", false)

	all := history.All()
	if len(all) != maxSymbolHistorySymbols {
		t.Fatalf("symbols = %d, want %d", len(all), maxSymbolHistorySymbols)
	}
	if _, ok := all["symbol-000"]; !ok {
		t.Fatal("recently touched symbol was evicted")
	}
	if _, ok := all["symbol-001"]; ok {
		t.Fatal("least-recently-used symbol was retained")
	}
	if _, ok := all["symbol-new"]; !ok {
		t.Fatal("new symbol missing")
	}

	total := 0
	for _, mods := range all {
		total += len(mods)
		if len(mods) > maxSymbolHistoryPerSymbol {
			t.Fatalf("per-symbol history exceeded cap: %d", len(mods))
		}
	}
	if total > maxSymbolHistorySymbols*maxSymbolHistoryPerSymbol {
		t.Fatalf("aggregate history = %d, cap %d", total, maxSymbolHistorySymbols*maxSymbolHistoryPerSymbol)
	}
}

func TestSymbolHistoryConcurrentRecordStaysBounded(t *testing.T) {
	history := &symbolHistory{}
	done := make(chan struct{})
	for worker := 0; worker < 16; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 1000; i++ {
				history.Record(fmt.Sprintf("symbol-%03d", (worker*1000+i)%400), i%2 == 0)
			}
		}(worker)
	}
	for worker := 0; worker < 16; worker++ {
		<-done
	}

	all := history.All()
	if len(all) > maxSymbolHistorySymbols {
		t.Fatalf("symbols = %d, cap %d", len(all), maxSymbolHistorySymbols)
	}
	for id, mods := range all {
		if len(mods) > maxSymbolHistoryPerSymbol {
			t.Fatalf("%s modifications = %d, cap %d", id, len(mods), maxSymbolHistoryPerSymbol)
		}
	}
}
