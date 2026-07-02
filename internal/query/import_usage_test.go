package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

// Import / re-export statements are usages: pyright and tsserver both
// count `from x import name` / `export {name} from …` lines in their
// reference sets, and a symbol consumed only through a façade module
// otherwise reports zero usages ("likely unused") despite live consumers.
func TestFindUsages_IncludesImportAndReExportEdges(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "src/vanilla.ts::createStore", Kind: graph.KindFunction, Name: "createStore", FilePath: "src/vanilla.ts"})
	g.AddNode(&graph.Node{ID: "src/index.ts", Kind: graph.KindFile, Name: "index.ts", FilePath: "src/index.ts"})
	g.AddNode(&graph.Node{ID: "tests/basic.test.tsx", Kind: graph.KindFile, Name: "basic.test.tsx", FilePath: "tests/basic.test.tsx"})

	g.AddEdge(&graph.Edge{
		From: "tests/basic.test.tsx", To: "src/vanilla.ts::createStore",
		Kind: graph.EdgeImports, FilePath: "tests/basic.test.tsx", Line: 3,
		Origin: graph.OriginASTResolved,
	})
	g.AddEdge(&graph.Edge{
		From: "src/index.ts", To: "src/vanilla.ts::createStore",
		Kind: graph.EdgeReExports, FilePath: "src/index.ts", Line: 1,
		Origin: graph.OriginASTResolved,
	})

	e := NewEngine(g)
	sg := e.FindUsagesScoped("src/vanilla.ts::createStore", QueryOptions{})

	kinds := map[graph.EdgeKind]int{}
	for _, edge := range sg.Edges {
		kinds[edge.Kind]++
	}
	assert.Equal(t, 1, kinds[graph.EdgeImports], "import statement must count as a usage")
	assert.Equal(t, 1, kinds[graph.EdgeReExports], "re-export statement must count as a usage")
}

func TestRefContextOf_ImportEdges(t *testing.T) {
	imp := &graph.Edge{Kind: graph.EdgeImports}
	assert.Equal(t, graph.RefContextImport, graph.RefContextOf(imp, graph.KindFile))
	rex := &graph.Edge{Kind: graph.EdgeReExports}
	assert.Equal(t, graph.RefContextImport, graph.RefContextOf(rex, graph.KindFile))
}
