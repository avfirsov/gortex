package goanalysis

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// An interface object mis-mapped onto a non-interface node (the innermost
// file/line match lands on a PARAM node when the interface is referenced on
// a signature line) must never enter the implements passes: one empty
// interface mapped that way fanned 130,250 junk edges — 57% of a real
// workspace's implements set — from every concrete type to a single
// `#param:ctx` node. The kind gate is the backstop regardless of how the
// object→node mapping mis-fires.
func TestImplementsPassesRejectMisMappedInterfaceNodes(t *testing.T) {
	ifaceType := types.NewInterfaceType(nil, nil)
	ifaceType.Complete()
	ifaceTN := types.NewTypeName(token.NoPos, nil, "Empty", nil)
	types.NewNamed(ifaceTN, ifaceType, nil)

	structTN := types.NewTypeName(token.NoPos, nil, "Impl", nil)
	types.NewNamed(structTN, types.NewStruct(nil, nil), nil)

	paramNodeID := "repo/noop.go::noopCache[T].Set#param:ctx"
	typeNodeID := "repo/impl.go::Impl"
	ifaceNodeID := "repo/iface.go::Empty"
	nodesByID := map[string]*graph.Node{
		paramNodeID: {ID: paramNodeID, Kind: graph.KindParam, Name: "ctx", FilePath: "repo/noop.go"},
		typeNodeID:  {ID: typeNodeID, Kind: graph.KindType, Name: "Impl", FilePath: "repo/impl.go"},
		ifaceNodeID: {ID: ifaceNodeID, Kind: graph.KindInterface, Name: "Empty", FilePath: "repo/iface.go"},
	}
	g := graph.New()
	for _, node := range nodesByID {
		g.AddNode(node)
	}
	p := &Provider{}

	// Mis-mapped: the interface object points at the param node. Nothing may
	// be written, from either the add or the confirm pass.
	junk := map[types.Object]string{ifaceTN: paramNodeID, structTN: typeNodeID}
	require.Zero(t, p.addMissingImplements(g, junk, nodesByID),
		"a param-hosted interface must not source implements edges")
	assert.Empty(t, g.GetInEdges(paramNodeID))
	require.Zero(t, p.enrichImplements(g, junk, nodesByID))

	// Concrete-side mirror: a concrete object mapped onto the param node
	// must not source edges either.
	junkConcrete := map[types.Object]string{ifaceTN: ifaceNodeID, structTN: paramNodeID}
	require.Zero(t, p.addMissingImplements(g, junkConcrete, nodesByID))
	assert.Empty(t, g.GetOutEdges(paramNodeID))

	// Positive control: correctly-mapped objects still produce the edge —
	// the gate rejects mis-mappings, not the pass.
	good := map[types.Object]string{ifaceTN: ifaceNodeID, structTN: typeNodeID}
	added := p.addMissingImplements(g, good, nodesByID)
	require.Equal(t, 1, added, "a correctly-mapped empty interface still gains its satisfying type")
	inbound := g.GetInEdges(ifaceNodeID)
	require.Len(t, inbound, 1)
	assert.Equal(t, typeNodeID, inbound[0].From)
	assert.Equal(t, graph.EdgeImplements, inbound[0].Kind)
}
