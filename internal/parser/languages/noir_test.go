package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestNoirExtractor_Basics(t *testing.T) {
	src := []byte(`use dep::std::hash::pedersen;
use dep::aztec::context;

struct Proof {
    nullifier: Field,
}

trait Verifier {
    fn verify(self: Self, p: Proof) -> bool;
}

impl Proof {
    fn new() -> Self {
        Self { nullifier: 0 }
    }
}

fn main(secret: Field) -> pub Field {
    pedersen([secret])
}
`)
	e := NewNoirExtractor()
	require.Equal(t, "noir", e.Language())

	res, err := e.Extract("main.nr", src)
	require.NoError(t, err)

	var gotProof, gotVerifier, gotImpl, gotMain bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Proof":
			gotProof = true
		case "Verifier":
			gotVerifier = true
		case "main":
			gotMain = true
		}
		if n.Name == "Proof" && n.Kind == graph.KindType {
			gotImpl = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::dep::std::hash::pedersen" {
			gotImport = true
		}
	}
	assert.True(t, gotProof)
	assert.True(t, gotVerifier)
	assert.True(t, gotImpl)
	assert.True(t, gotMain)
	assert.True(t, gotImport)
}

func TestNoirExtractor_EmptyInput(t *testing.T) {
	res, err := NewNoirExtractor().Extract("e.nr", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
