package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestSolidityExtractor_ERC20Like(t *testing.T) {
	src := []byte(`// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/access/Ownable.sol";
import {IERC20} from "./IERC20.sol";

interface IFoo {
    function bar() external;
}

contract Token is Ownable {
    struct Holder { address who; uint256 amount; }
    enum State { Active, Paused }

    mapping(address => uint256) public balances;
    uint256 public totalSupply;

    event Transfer(address indexed from, address indexed to, uint256 value);

    modifier whenActive() {
        require(state == State.Active, "paused");
        _;
    }

    function mint(address to, uint256 amount) public whenActive {
        balances[to] += amount;
        totalSupply += amount;
        emit Transfer(address(0), to, amount);
    }

    function burn(uint256 amount) external {
        balances[msg.sender] -= amount;
        totalSupply -= amount;
    }
}
`)
	e := NewSolidityExtractor()
	require.Equal(t, "solidity", e.Language())

	res, err := e.Extract("Token.sol", src)
	require.NoError(t, err)

	var contracts, interfaces, structs, enums, methods, events, modifiers int
	for _, n := range res.Nodes {
		switch n.Kind {
		case graph.KindType:
			if n.Meta != nil {
				switch n.Meta["sol_kind"] {
				case "struct":
					structs++
				case "enum":
					enums++
				default:
					contracts++
				}
			}
		case graph.KindInterface:
			interfaces++
		case graph.KindMethod:
			if n.Meta != nil && n.Meta["sol_kind"] == "event" {
				events++
			} else if n.Meta != nil && n.Meta["sol_kind"] == "modifier" {
				modifiers++
			} else {
				methods++
			}
		}
	}

	assert.GreaterOrEqual(t, contracts, 1, "Token contract")
	assert.GreaterOrEqual(t, interfaces, 1, "IFoo interface")
	assert.GreaterOrEqual(t, structs, 1, "Holder")
	assert.GreaterOrEqual(t, enums, 1, "State")
	assert.GreaterOrEqual(t, methods, 2, "mint + burn (bar is in interface, also counts)")
	assert.GreaterOrEqual(t, events, 1)
	assert.GreaterOrEqual(t, modifiers, 1)

	imports := 0
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}
	assert.GreaterOrEqual(t, imports, 2)
}

func TestSolidityExtractor_EmptyInput(t *testing.T) {
	res, err := NewSolidityExtractor().Extract("empty.sol", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
