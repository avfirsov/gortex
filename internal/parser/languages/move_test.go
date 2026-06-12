package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestMoveExtractor_Basics(t *testing.T) {
	src := []byte(`module demo::coin {
    use std::signer;
    use aptos_framework::coin;

    struct Vault has key {
        balance: u64,
    }

    public fun deposit(addr: address, amount: u64) {
        // ...
    }

    entry fun withdraw(addr: address, amount: u64) {
        // ...
    }
}
`)
	e := NewMoveExtractor()
	require.Equal(t, "move", e.Language())

	res, err := e.Extract("coin.move", src)
	require.NoError(t, err)

	var gotCoin, gotVault, gotDeposit, gotWithdraw bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "coin":
			gotCoin = true
		case "Vault":
			gotVault = true
		case "deposit":
			gotDeposit = true
		case "withdraw":
			gotWithdraw = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::std::signer" {
			gotImport = true
		}
	}
	assert.True(t, gotCoin)
	assert.True(t, gotVault)
	assert.True(t, gotDeposit)
	assert.True(t, gotWithdraw)
	assert.True(t, gotImport)
}

func TestMoveExtractor_EmptyInput(t *testing.T) {
	res, err := NewMoveExtractor().Extract("e.move", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
