package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestSASExtractor_Basics(t *testing.T) {
	src := []byte(`libname mylib '/data/mylib';
%include 'common.sas';

data sales;
    set mylib.rawsales;
run;

proc means data=sales;
    var amount;
run;

%macro summarize(ds);
    proc print data=&ds;
    run;
%mend summarize;
`)
	e := NewSASExtractor()
	require.Equal(t, "sas", e.Language())

	res, err := e.Extract("demo.sas", src)
	require.NoError(t, err)

	var gotSales, gotMeans, gotMacro bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "sales":
			gotSales = n.Kind == graph.KindVariable
		case "means":
			gotMeans = n.Kind == graph.KindFunction
		case "summarize":
			gotMacro = n.Kind == graph.KindFunction
		}
	}
	var gotIncludeImport, gotLibImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::common.sas" {
			gotIncludeImport = true
		}
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::mylib" {
			gotLibImport = true
		}
	}
	assert.True(t, gotSales)
	assert.True(t, gotMeans)
	assert.True(t, gotMacro)
	assert.True(t, gotIncludeImport)
	assert.True(t, gotLibImport)
}

func TestSASExtractor_EmptyInput(t *testing.T) {
	res, err := NewSASExtractor().Extract("e.sas", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
