package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestBallerinaExtractor_Basics(t *testing.T) {
	src := []byte(`import ballerina/http;
import ballerina/io as bio;

type User record {
    string name;
    int age;
};

type UserId int;

class UserStore {
    User[] users = [];

    function add(User u) {
        self.users.push(u);
    }
}

service /api on new http:Listener(8080) {
    resource function get users() returns User[] {
        return [];
    }
}

public function main() returns error? {
    io:println("hello");
}
`)
	e := NewBallerinaExtractor()
	require.Equal(t, "ballerina", e.Language())

	res, err := e.Extract("app.bal", src)
	require.NoError(t, err)

	var gotUser, gotUserId, gotStore, gotAdd, gotMain, gotApi bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "User":
			gotUser = true
		case "UserId":
			gotUserId = true
		case "UserStore":
			gotStore = true
		case "add":
			gotAdd = true
		case "main":
			gotMain = true
		case "api":
			gotApi = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::ballerina/http" {
			gotImport = true
		}
	}
	assert.True(t, gotUser)
	assert.True(t, gotUserId)
	assert.True(t, gotStore)
	assert.True(t, gotAdd)
	assert.True(t, gotMain)
	assert.True(t, gotApi)
	assert.True(t, gotImport)
}

func TestBallerinaExtractor_EmptyInput(t *testing.T) {
	res, err := NewBallerinaExtractor().Extract("e.bal", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
