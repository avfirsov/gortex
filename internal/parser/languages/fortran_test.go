package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestFortranExtractor_ModernModule(t *testing.T) {
	src := []byte(`module shapes_mod
  implicit none
  private
  public :: area, Circle

  type :: Circle
    real :: radius
  end type Circle

contains

  pure function area(c) result(a)
    type(Circle), intent(in) :: c
    real :: a
    a = 3.14159 * c%radius * c%radius
  end function area

  subroutine describe(c)
    type(Circle), intent(in) :: c
    call print_circle(c)
  end subroutine describe
end module shapes_mod
`)
	e := NewFortranExtractor()
	require.Equal(t, "fortran", e.Language())

	res, err := e.Extract("shapes.f90", src)
	require.NoError(t, err)

	modules, types, funcs := 0, 0, 0
	for _, n := range res.Nodes {
		switch {
		case n.Kind == graph.KindType && n.Meta != nil && n.Meta["fortran_kind"] == "module":
			modules++
		case n.Kind == graph.KindType:
			types++
		case n.Kind == graph.KindFunction:
			funcs++
		}
	}
	assert.GreaterOrEqual(t, modules, 1, "shapes_mod")
	assert.GreaterOrEqual(t, types, 1, "Circle type")
	assert.GreaterOrEqual(t, funcs, 2, "area function + describe subroutine")

	calls := 0
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeCalls {
			calls++
		}
	}
	assert.GreaterOrEqual(t, calls, 1, "call print_circle(c) must produce a call edge")
}

func TestFortranExtractor_Program(t *testing.T) {
	src := []byte(`program main
  use shapes_mod
  implicit none
  print *, "hello"
end program main
`)
	res, err := NewFortranExtractor().Extract("m.f90", src)
	require.NoError(t, err)

	var gotProgram, gotUse bool
	for _, n := range res.Nodes {
		if n.Name == "main" && n.Meta != nil && n.Meta["fortran_kind"] == "program" {
			gotProgram = true
		}
	}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeImports && e.To == "unresolved::import::shapes_mod" {
			gotUse = true
		}
	}
	assert.True(t, gotProgram)
	assert.True(t, gotUse)
}

func TestFortranExtractor_EmptyInput(t *testing.T) {
	res, err := NewFortranExtractor().Extract("empty.f90", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
