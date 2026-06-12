package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCairoExtractor_Basics(t *testing.T) {
	src := []byte(`use starknet::ContractAddress;
use core::array::ArrayTrait;

mod storage {
    struct Balance {
        amount: u256,
    }
}

trait Greeter {
    fn greet(self: @Balance) -> felt252;
}

#[external]
fn transfer(to: ContractAddress, amount: u256) {
    // ...
}
`)
	e := NewCairoExtractor()
	require.Equal(t, "cairo", e.Language())

	res, err := e.Extract("token.cairo", src)
	require.NoError(t, err)

	var gotStorage, gotBalance, gotGreeter, gotTransfer bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "storage":
			gotStorage = true
		case "Balance":
			gotBalance = true
		case "Greeter":
			gotGreeter = true
		case "transfer":
			gotTransfer = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::starknet::ContractAddress" {
			gotImport = true
		}
	}
	assert.True(t, gotStorage)
	assert.True(t, gotBalance)
	assert.True(t, gotGreeter)
	assert.True(t, gotTransfer)
	assert.True(t, gotImport)
}

func TestCairoExtractor_EmptyInput(t *testing.T) {
	res, err := NewCairoExtractor().Extract("e.cairo", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
