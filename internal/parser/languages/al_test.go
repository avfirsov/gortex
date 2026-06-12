package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestALExtractor_TableAndProcedures(t *testing.T) {
	src := []byte(`using Microsoft.Sales;

table 50100 "Customer Ext"
{
    fields
    {
        field(1; "No."; Code[20]) { }
        field(2; "Name"; Text[100]) { }
    }

    procedure SendInvoice(): Boolean
    begin
        exit(true);
    end;

    local procedure FormatName(): Text
    begin
        exit("No." + ': ' + "Name");
    end;

    trigger OnInsert()
    begin
        FormatName();
    end;
}
`)
	e := NewALExtractor()
	require.Equal(t, "al", e.Language())
	require.Equal(t, []string{".al", ".dal"}, e.Extensions())

	res, err := e.Extract("CustomerExt.al", src)
	require.NoError(t, err)

	types := 0
	methods := 0
	imports := 0
	var objectNode *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType {
			types++
			objectNode = n
		}
		if n.Kind == graph.KindMethod {
			methods++
		}
	}
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports {
			imports++
		}
	}

	require.NotNil(t, objectNode)
	assert.Equal(t, "Customer Ext", objectNode.Name)
	assert.Equal(t, "table", objectNode.Meta["al_kind"])
	assert.GreaterOrEqual(t, types, 1)
	assert.GreaterOrEqual(t, methods, 3, "SendInvoice + FormatName + OnInsert")
	assert.Equal(t, 1, imports)
}

func TestALExtractor_MultipleObjectsInOneFile(t *testing.T) {
	src := []byte(`page 50200 "My Card"
{
    trigger OnOpenPage() begin end;
}

codeunit 50201 "My Helpers"
{
    procedure Noop() begin end;
}

interface "IFormatter"
{
    procedure Format(input: Text): Text;
}
`)
	res, err := NewALExtractor().Extract("x.al", src)
	require.NoError(t, err)

	var types, interfaces int
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType {
			types++
		}
		if n.Kind == graph.KindInterface {
			interfaces++
		}
	}
	assert.GreaterOrEqual(t, types, 2, "page + codeunit")
	assert.Equal(t, 1, interfaces)
}

func TestALExtractor_EmptyInput(t *testing.T) {
	res, err := NewALExtractor().Extract("empty.al", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
