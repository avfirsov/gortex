package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestAdaExtractor_Package(t *testing.T) {
	src := []byte(`with Ada.Text_IO;
with Ada.Integer_Text_IO;

package Shapes is
   type Circle is record
      Radius : Float;
   end record;

   function Area (C : Circle) return Float;
   procedure Print (C : Circle);
end Shapes;

package body Shapes is
   function Area (C : Circle) return Float is
   begin
      return 3.14 * C.Radius * C.Radius;
   end Area;

   procedure Print (C : Circle) is
   begin
      Ada.Text_IO.Put_Line ("circle");
   end Print;
end Shapes;
`)
	e := NewAdaExtractor()
	require.Equal(t, "ada", e.Language())

	res, err := e.Extract("shapes.adb", src)
	require.NoError(t, err)

	var gotPkg, gotType, gotArea, gotPrint bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "Shapes":
			gotPkg = true
		case "Circle":
			gotType = true
		case "Area":
			gotArea = true
		case "Print":
			gotPrint = true
		}
	}
	var gotWith bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::Ada.Text_IO" {
			gotWith = true
		}
	}
	assert.True(t, gotPkg)
	assert.True(t, gotType)
	assert.True(t, gotArea)
	assert.True(t, gotPrint)
	assert.True(t, gotWith)
}

func TestAdaExtractor_EmptyInput(t *testing.T) {
	res, err := NewAdaExtractor().Extract("e.adb", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
