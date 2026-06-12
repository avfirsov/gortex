package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// outEdgeTo returns the first out-edge of fromID whose target is toID.
func outEdgeTo(g graph.Store, fromID, toID string) *graph.Edge {
	for _, e := range g.GetOutEdges(fromID) {
		if e.To == toID {
			return e
		}
	}
	return nil
}

// TestGRPCStubResolution_EndToEnd indexes a two-file Go repo — a server
// that registers a gRPC service impl, and a client that calls an RPC
// through both the inline-chained and variable stub forms — and
// asserts both call sites land on the server-side handler method.
func TestGRPCStubResolution_EndToEnd(t *testing.T) {
	dir := t.TempDir()

	server := `package app

type userServer struct{}

func (s *userServer) GetUser(ctx Context, req *Req) (*Resp, error) { return nil, nil }
func (s *userServer) ListUsers(ctx Context, req *Req) (*Resp, error) { return nil, nil }

func wire(s *GRPCServer) {
	RegisterUserServiceServer(s, &userServer{})
}
`
	client := `package app

func callInline(conn *ClientConn) {
	NewUserServiceClient(conn).GetUser(ctx, req)
}

func callViaVar(conn *ClientConn) {
	c := NewUserServiceClient(conn)
	c.ListUsers(ctx, req)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server.go"), []byte(server), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte(client), 0o644))

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	idx.ResolveAll()

	getUserID := "server.go::userServer.GetUser"
	listUsersID := "server.go::userServer.ListUsers"
	require.NotNil(t, g.GetNode(getUserID), "GetUser handler node must exist")
	require.NotNil(t, g.GetNode(listUsersID), "ListUsers handler node must exist")

	// Inline chained call: NewUserServiceClient(conn).GetUser(...).
	inline := outEdgeTo(g, "client.go::callInline", getUserID)
	require.NotNil(t, inline, "inline chained stub call must land on the GetUser handler")
	assert.Equal(t, graph.EdgeCalls, inline.Kind)
	assert.Equal(t, "grpc.stub", inline.Meta["via"])
	assert.Equal(t, graph.OriginASTResolved, inline.Origin)
	assert.Equal(t, "UserService", inline.Meta["grpc_service"])

	// Variable stub call: c := New...Client(conn); c.ListUsers(...).
	viaVar := outEdgeTo(g, "client.go::callViaVar", listUsersID)
	require.NotNil(t, viaVar, "variable stub call must land on the ListUsers handler")
	assert.Equal(t, "grpc.stub", viaVar.Meta["via"])
	assert.Equal(t, graph.OriginASTResolved, viaVar.Origin)

	// No grpc.stub placeholder edges should remain unresolved.
	for _, e := range g.AllEdges() {
		if e.Meta != nil && e.Meta["via"] == "grpc.stub" {
			assert.NotContains(t, e.To, "unresolved::grpc::",
				"every grpc.stub call should have resolved to a handler")
		}
	}
}

// TestGRPCStubResolution_InterfaceFallback covers the case where the
// server impl is discovered via the generated `<Service>Server`
// interface (InferImplements) rather than a registration call.
func TestGRPCStubResolution_InterfaceFallback(t *testing.T) {
	dir := t.TempDir()

	// The generated interface plus a server impl that structurally
	// satisfies it — but no Register call anywhere.
	generated := `package app

type OrderServiceServer interface {
	PlaceOrder(ctx Context, req *Req) (*Resp, error)
}
`
	server := `package app

type orderServer struct{}

func (s *orderServer) PlaceOrder(ctx Context, req *Req) (*Resp, error) { return nil, nil }
`
	client := `package app

func placeIt(conn *ClientConn) {
	NewOrderServiceClient(conn).PlaceOrder(ctx, req)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gen.go"), []byte(generated), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server.go"), []byte(server), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte(client), 0o644))

	g := graph.New()
	idx := newTestIndexer(g)
	_, err := idx.Index(dir)
	require.NoError(t, err)
	idx.ResolveAll()

	handlerID := "server.go::orderServer.PlaceOrder"
	require.NotNil(t, g.GetNode(handlerID))

	edge := outEdgeTo(g, "client.go::placeIt", handlerID)
	require.NotNil(t, edge, "stub call must land on the handler via the interface signal")
	assert.Equal(t, graph.OriginASTInferred, edge.Origin,
		"interface-signal resolution rides at ast_inferred")
}
