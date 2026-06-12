package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestTactExtractor_Basics(t *testing.T) {
	src := []byte(`import "@stdlib/deploy";

message Transfer {
    to: Address;
    amount: Int;
}

struct Account {
    balance: Int;
}

trait Ownable {
    fun owner(): Address;
}

contract Wallet with Ownable {
    balance: Int;

    init(owner: Address) {
        self.balance = 0;
    }

    receive(msg: Transfer) {
        self.balance = self.balance - msg.amount;
    }

    get fun balance(): Int {
        return self.balance;
    }
}
`)
	e := NewTactExtractor()
	require.Equal(t, "tact", e.Language())

	res, err := e.Extract("wallet.tact", src)
	require.NoError(t, err)

	var gotWallet, gotAccount, gotTransfer, gotOwnable, gotBalance, gotInit bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Wallet":
			gotWallet = true
		case "Account":
			gotAccount = true
		case "Transfer":
			gotTransfer = true
		case "Ownable":
			gotOwnable = true
		case "balance":
			gotBalance = true
		case "init":
			gotInit = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::@stdlib/deploy" {
			gotImport = true
		}
	}
	assert.True(t, gotWallet)
	assert.True(t, gotAccount)
	assert.True(t, gotTransfer)
	assert.True(t, gotOwnable)
	assert.True(t, gotBalance)
	assert.True(t, gotInit)
	assert.True(t, gotImport)
}

func TestTactExtractor_EmptyInput(t *testing.T) {
	res, err := NewTactExtractor().Extract("e.tact", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
