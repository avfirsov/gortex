package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestSQLExtractor_CreateTable(t *testing.T) {
	src := []byte(`CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE
);
`)
	e := NewSQLExtractor()
	result, err := e.Extract("schema.sql", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "users", types[0].Name)

	// Columns as variables with EdgeMemberOf.
	vars := nodesOfKind(result.Nodes, graph.KindVariable)
	assert.Len(t, vars, 3) // id, name, email

	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.Len(t, memberEdges, 3)
	for _, e := range memberEdges {
		assert.Equal(t, "schema.sql::users", e.To)
	}
}

func TestSQLExtractor_MultipleTables(t *testing.T) {
	src := []byte(`CREATE TABLE users (
    id INTEGER PRIMARY KEY
);

CREATE TABLE orders (
    id INTEGER PRIMARY KEY,
    user_id INTEGER
);
`)
	e := NewSQLExtractor()
	result, err := e.Extract("schema.sql", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	assert.Len(t, types, 2)

	names := make([]string, len(types))
	for i, t := range types {
		names[i] = t.Name
	}
	assert.Contains(t, names, "users")
	assert.Contains(t, names, "orders")
}

func TestSQLExtractor_FileNode(t *testing.T) {
	src := []byte(`CREATE TABLE t (id INT);`)
	e := NewSQLExtractor()
	result, err := e.Extract("test.sql", src)
	require.NoError(t, err)

	files := nodesOfKind(result.Nodes, graph.KindFile)
	require.Len(t, files, 1)
	assert.Equal(t, "test.sql", files[0].Name)
}

func TestSQLExtractor_Extensions(t *testing.T) {
	e := NewSQLExtractor()
	assert.Equal(t, "sql", e.Language())
	assert.Equal(t, []string{".sql"}, e.Extensions())
}
