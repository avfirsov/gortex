package goanalysis

import "testing"

func TestGoTypesConcurrencyDefaults(t *testing.T) {
	cases := []struct {
		name    string
		hostRAM uint64
		env     string
		want    int
	}{
		{"small host stays serial", 8 << 30, "", 1},
		{"floor host admits two", 16 << 30, "", 2},
		{"large host admits two", 64 << 30, "", 2},
		{"unknown RAM stays serial", 0, "", 1},
		{"env wins over small host", 8 << 30, "3", 3},
		{"env wins over large host", 64 << 30, "1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GORTEX_GOTYPES_CONCURRENCY", tc.env)
			if got := goTypesConcurrency(tc.hostRAM); got != tc.want {
				t.Errorf("goTypesConcurrency(%d) with env %q = %d, want %d",
					tc.hostRAM, tc.env, got, tc.want)
			}
		})
	}
}
