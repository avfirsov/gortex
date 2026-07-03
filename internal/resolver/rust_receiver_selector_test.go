package resolver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A `mat.buffer()` selector call, where `mat` is a param typed with a
// generic struct (`&SinkMatch<'_>`), binds to that struct's `buffer`
// method — not to its like-named field, and not left unresolved. The
// generic resolver keys methods by their verbatim receiver
// (`SinkMatch<'b>`), which the generics-stripped inferred receiver_type
// (`SinkMatch`) misses; the scope pass' base-name alias binds it.
func TestRustScope_ReceiverTypeSelector(t *testing.T) {
	g := buildRustGraph(t, map[string]string{
		"lib.rs": `
struct SinkMatch<'b> { buffer: &'b [u8] }

impl<'b> SinkMatch<'b> {
    fn buffer(&self) -> &'b [u8] { self.buffer }
}

fn take(mat: &SinkMatch<'_>) {
    let _b = mat.buffer();
}
`,
	})
	ResolveRustScopeCalls(g)
	targets := callTargetsFromRust(g, "lib.rs::take")
	require.Contains(t, targets, "lib.rs::SinkMatch<'b>.buffer")
}
