package mcp

import "testing"

// TestSmartContextSeedCount checks the repo-size buckets that drive the
// adaptive smart_context seed default.
func TestSmartContextSeedCount(t *testing.T) {
	cases := []struct {
		nodes int
		want  int
	}{
		{0, 4},
		{1_999, 4},
		{2_000, 6},
		{39_999, 6},
		{40_000, 8},
		{119_999, 8},
		{120_000, 10},
		{5_000_000, 10},
	}
	for _, c := range cases {
		if got := smartContextSeedCount(c.nodes); got != c.want {
			t.Errorf("smartContextSeedCount(%d) = %d, want %d", c.nodes, got, c.want)
		}
	}
}
