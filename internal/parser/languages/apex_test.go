package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestApexExtractor_Basics(t *testing.T) {
	src := []byte(`public with sharing class AccountService {
    public static Account findById(Id accountId) {
        return [SELECT Id, Name FROM Account WHERE Id = :accountId];
    }

    private void logAccess(String who) {
        System.debug(who);
    }
}

public interface IGreeter {
    String greet(String name);
}

trigger AccountTrigger on Account (before insert, before update) {
    AccountService.findById(null);
}
`)
	e := NewApexExtractor()
	require.Equal(t, "apex", e.Language())

	res, err := e.Extract("Accounts.cls", src)
	require.NoError(t, err)

	var gotClass, gotIface, gotTrigger, gotFind bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "AccountService":
			gotClass = n.Kind == graph.KindType
		case "IGreeter":
			gotIface = n.Kind == graph.KindInterface
		case "AccountTrigger":
			gotTrigger = n.Kind == graph.KindType
		case "findById":
			gotFind = n.Kind == graph.KindMethod
		}
	}
	assert.True(t, gotClass)
	assert.True(t, gotIface)
	assert.True(t, gotTrigger)
	assert.True(t, gotFind)
}

func TestApexExtractor_EmptyInput(t *testing.T) {
	res, err := NewApexExtractor().Extract("e.cls", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
