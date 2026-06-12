package packstrategy

import (
	"reflect"
	"testing"
)

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestNormalizeAndFromEnv(t *testing.T) {
	if Normalize("topk") != StrategyTopK {
		t.Fatal("topk alias")
	}
	if Normalize("dense") != StrategyDensity {
		t.Fatal("dense alias")
	}
	if Normalize("file") != StrategyFileGrouped {
		t.Fatal("file alias")
	}
	if Normalize("nonsense") != DefaultStrategy {
		t.Fatal("unknown should fall back to default")
	}
	t.Setenv("GORTEX_PACK_STRATEGY", "top-k")
	if FromEnv() != StrategyTopK {
		t.Fatal("FromEnv should read GORTEX_PACK_STRATEGY")
	}
}

func TestTopKRespectsRankAndBudget(t *testing.T) {
	items := []Item{
		{ID: "a", Score: 9, Tokens: 40},
		{ID: "b", Score: 8, Tokens: 40},
		{ID: "c", Score: 7, Tokens: 40},
	}
	// Budget fits two.
	got := ids(Select(StrategyTopK, items, 90))
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("top-k budget=90 = %v, want [a b]", got)
	}
	// Unlimited budget = all in rank order.
	if got := ids(Select(StrategyTopK, items, 0)); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("top-k unlimited = %v", got)
	}
}

func TestTopKSkipsOverlargeButFillsBudget(t *testing.T) {
	items := []Item{
		{ID: "big", Score: 10, Tokens: 100},
		{ID: "small1", Score: 5, Tokens: 10},
		{ID: "small2", Score: 4, Tokens: 10},
	}
	got := ids(Select(StrategyTopK, items, 30))
	// big doesn't fit; the two small ones do.
	if !reflect.DeepEqual(got, []string{"small1", "small2"}) {
		t.Fatalf("top-k fill = %v, want [small1 small2]", got)
	}
}

func TestDensityPrefersSignalPerToken(t *testing.T) {
	items := []Item{
		{ID: "fat", Score: 10, Tokens: 100}, // density 0.1
		{ID: "lean", Score: 6, Tokens: 10},  // density 0.6
	}
	got := ids(Select(StrategyDensity, items, 1000))
	if !reflect.DeepEqual(got, []string{"lean", "fat"}) {
		t.Fatalf("density order = %v, want [lean fat]", got)
	}
}

func TestFileGroupedKeepsFilesTogether(t *testing.T) {
	items := []Item{
		{ID: "f1.go::A", FilePath: "f1.go", Score: 5},
		{ID: "f2.go::X", FilePath: "f2.go", Score: 9},
		{ID: "f1.go::B", FilePath: "f1.go", Score: 4},
		{ID: "f2.go::Y", FilePath: "f2.go", Score: 1},
	}
	// f2 sum=10 > f1 sum=9, so f2's symbols come first, kept together.
	got := ids(Select(StrategyFileGrouped, items, 0))
	want := []string{"f2.go::X", "f2.go::Y", "f1.go::A", "f1.go::B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file-grouped = %v, want %v", got, want)
	}
}

func TestSelectEmpty(t *testing.T) {
	if got := Select(StrategyDensity, nil, 100); len(got) != 0 {
		t.Fatalf("empty input should yield empty, got %v", got)
	}
}
