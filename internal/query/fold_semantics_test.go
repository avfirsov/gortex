package query

import "testing"

func TestContainsFoldSemantics(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"HandleServerRequest", "server", true},
		{"handleserverrequest", "server", true},
		{"HANDLESERVERREQUEST", "server", true},
		{"handle", "server", false},
		{"srv", "server", false},
		{"", "server", false},
		{"anything", "", true},
		{"pkg/a.go::HandleServer", "server", true},
		// Length-changing Unicode foldings (the Kelvin sign folding to a
		// one-byte k) are a documented non-goal of the byte-windowed fold —
		// see containsFold.
		{"caféServer", "server", true},
	}
	for _, tc := range cases {
		if got := containsFold(tc.s, tc.sub); got != tc.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}
