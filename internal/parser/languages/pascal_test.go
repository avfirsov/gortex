package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestPascalExtractor_Unit(t *testing.T) {
	src := []byte(`unit Shapes;

interface

uses
  SysUtils, Classes;

type
  TCircle = class
  public
    constructor Create(R: Double);
    function Area: Double;
  end;

implementation

constructor TCircle.Create(R: Double);
begin
end;

function TCircle.Area: Double;
begin
  Result := 3.14 * 1.0;
end;

procedure Hello;
begin
  WriteLn('hi');
end;

end.
`)
	e := NewPascalExtractor()
	require.Equal(t, "pascal", e.Language())

	res, err := e.Extract("Shapes.pas", src)
	require.NoError(t, err)

	var gotUnit, gotType, gotCreate, gotArea, gotHello bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Shapes":
			gotUnit = true
		case "TCircle":
			gotType = true
		case "TCircle.Create":
			gotCreate = true
		case "TCircle.Area":
			gotArea = true
		case "Hello":
			gotHello = true
		}
	}
	var gotUses bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::SysUtils" {
			gotUses = true
		}
	}
	assert.True(t, gotUnit)
	assert.True(t, gotType)
	assert.True(t, gotCreate)
	assert.True(t, gotArea)
	assert.True(t, gotHello)
	assert.True(t, gotUses)
}

func TestPascalExtractor_EmptyInput(t *testing.T) {
	res, err := NewPascalExtractor().Extract("e.pas", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
