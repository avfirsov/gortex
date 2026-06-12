package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// grpcStubEdge returns the single EdgeCalls edge tagged as a gRPC stub
// call, failing the test when zero or more than one is present.
func grpcStubEdge(t *testing.T, fix *extractedFixture) *graph.Edge {
	t.Helper()
	var found []*graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["via"] == "grpc.stub" {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "expected exactly one grpc.stub call edge")
	return found[0]
}

func TestGoGRPC_InlineChainedStubCall(t *testing.T) {
	fix := runGoExtract(t, `package client

import pb "example.com/proto"

func run(conn *grpc.ClientConn) {
	pb.NewUserServiceClient(conn).GetUser(ctx, req)
}
`)
	e := grpcStubEdge(t, fix)
	assert.Equal(t, "pkg/foo.go::run", e.From)
	assert.Equal(t, "unresolved::grpc::UserService::GetUser", e.To)
	assert.Equal(t, "UserService", e.Meta["grpc_service"])
	assert.Equal(t, "GetUser", e.Meta["grpc_method"])
}

func TestGoGRPC_VariableStubCall(t *testing.T) {
	fix := runGoExtract(t, `package client

import pb "example.com/proto"

func run(conn *grpc.ClientConn) {
	c := pb.NewUserServiceClient(conn)
	c.ListUsers(ctx, req)
}
`)
	e := grpcStubEdge(t, fix)
	assert.Equal(t, "pkg/foo.go::run", e.From)
	assert.Equal(t, "unresolved::grpc::UserService::ListUsers", e.To)
	assert.Equal(t, "UserService", e.Meta["grpc_service"])
	assert.Equal(t, "ListUsers", e.Meta["grpc_method"])
}

func TestGoGRPC_UnqualifiedConstructor(t *testing.T) {
	// Same-package generated stub: the constructor has no package
	// qualifier.
	fix := runGoExtract(t, `package proto

func run(conn *grpc.ClientConn) {
	NewOrderServiceClient(conn).PlaceOrder(ctx, req)
}
`)
	e := grpcStubEdge(t, fix)
	assert.Equal(t, "unresolved::grpc::OrderService::PlaceOrder", e.To)
}

func TestGoGRPC_LongerChainNotMistakenForStub(t *testing.T) {
	// In `New<Service>Client(c).WithTimeout().GetUser()`, GetUser is
	// invoked on WithTimeout's return value, not on the stub itself —
	// its receiver is a longer chain than a single balanced call, so it
	// must not be tagged as a grpc.stub call. (WithTimeout, called
	// directly on the stub, is tagged by the heuristic and harmlessly
	// left unresolved by the resolver — that is expected.)
	fix := runGoExtract(t, `package client

import pb "example.com/proto"

func run(conn *grpc.ClientConn) {
	pb.NewUserServiceClient(conn).WithTimeout().GetUser(ctx, req)
}
`)
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["via"] == "grpc.stub" && e.Meta["grpc_method"] == "GetUser" {
			t.Fatalf("GetUser on a chained receiver was wrongly tagged as a grpc.stub call: %s", e.To)
		}
	}
}

func TestGoGRPC_PlainMethodCallNotStub(t *testing.T) {
	fix := runGoExtract(t, `package client

func run(s *Server) {
	s.DoWork(ctx)
}
`)
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["via"] == "grpc.stub" {
			t.Fatalf("plain method call wrongly tagged as grpc.stub: %s", e.To)
		}
	}
}

func TestGoGRPC_ServerRegistration_CompositeLiteral(t *testing.T) {
	fix := runGoExtract(t, `package main

import pb "example.com/proto"

func main() {
	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, &userServer{})
}
`)
	var reg *graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["grpc_register_service"] != nil {
			require.Nil(t, reg, "expected exactly one registration edge")
			reg = e
		}
	}
	require.NotNil(t, reg, "registration call edge must carry grpc_register_* meta")
	assert.Equal(t, "UserService", reg.Meta["grpc_register_service"])
	assert.Equal(t, "userServer", reg.Meta["grpc_register_impl"])
}

func TestGoGRPC_ServerRegistration_IdentifierViaTypeEnv(t *testing.T) {
	fix := runGoExtract(t, `package main

import pb "example.com/proto"

func main() {
	s := grpc.NewServer()
	srv := &userServer{}
	pb.RegisterUserServiceServer(s, srv)
}
`)
	var reg *graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["grpc_register_service"] != nil {
			reg = e
		}
	}
	require.NotNil(t, reg, "registration via identifier must resolve through the type env")
	assert.Equal(t, "UserService", reg.Meta["grpc_register_service"])
	assert.Equal(t, "userServer", reg.Meta["grpc_register_impl"])
}

func TestGoGRPC_ServerRegistration_Unqualified(t *testing.T) {
	fix := runGoExtract(t, `package proto

func wire(s *grpc.Server) {
	RegisterPaymentServiceServer(s, NewPaymentServer())
}
`)
	var reg *graph.Edge
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["grpc_register_service"] != nil {
			reg = e
		}
	}
	require.NotNil(t, reg, "unqualified registration call must carry grpc_register_* meta")
	assert.Equal(t, "PaymentService", reg.Meta["grpc_register_service"])
	assert.Equal(t, "PaymentServer", reg.Meta["grpc_register_impl"])
}

func TestGoGRPC_NonRegisterCallNoMeta(t *testing.T) {
	fix := runGoExtract(t, `package main

func main() {
	doSomething(a, b)
}
`)
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.Meta != nil && e.Meta["grpc_register_service"] != nil {
			t.Fatalf("non-registration call wrongly carries grpc_register_* meta")
		}
	}
}
