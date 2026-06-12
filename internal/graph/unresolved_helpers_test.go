package graph

import "testing"

// TestUnresolvedHelpers locks in the multi-repo unresolved target
// normalisation: a literal `unresolved::Foo` (legacy single-repo) and
// a per-repo `gortex::unresolved::Foo` (multi-repo COPY rewrite) must
// both be recognised by IsUnresolvedTarget and decoded to "Foo" by
// UnresolvedName. Pre-fix, every caller used strings.HasPrefix on the
// literal form, which silently missed the prefixed form and left
// every multi-repo call edge dangling.
func TestUnresolvedHelpers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id     string
		isU    bool
		name   string
		prefix string
	}{
		// Legacy / single-repo form
		{"unresolved::AddNode", true, "AddNode", ""},
		{"unresolved::*.Foo", true, "*.Foo", ""},
		{"unresolved::import::fmt", true, "import::fmt", ""},
		// Multi-repo COPY-rewrite form
		{"gortex::unresolved::AddNode", true, "AddNode", "gortex"},
		{"tree-sitter-dart::unresolved::ACCEPT_TOKEN", true, "ACCEPT_TOKEN", "tree-sitter-dart"},
		// Non-stubs
		{"gortex/internal/graph/graph.go::Graph.AddNode", false, "", ""},
		{"", false, "", ""},
		{"stdlib::fmt::Errorf", false, "", ""},
		{"gortex::stdlib::fmt::Errorf", false, "", ""},
	}
	for _, c := range cases {
		if got := IsUnresolvedTarget(c.id); got != c.isU {
			t.Errorf("IsUnresolvedTarget(%q) = %v, want %v", c.id, got, c.isU)
		}
		if got := UnresolvedName(c.id); got != c.name {
			t.Errorf("UnresolvedName(%q) = %q, want %q", c.id, got, c.name)
		}
		if got := UnresolvedRepoPrefix(c.id); got != c.prefix {
			t.Errorf("UnresolvedRepoPrefix(%q) = %q, want %q", c.id, got, c.prefix)
		}
	}
}
