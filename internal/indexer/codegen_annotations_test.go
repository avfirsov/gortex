package indexer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestIndex_LombokGeneratedVisibility indexes a real Lombok @Data class
// and verifies the indexer flags it as carrying generated members.
func TestIndex_LombokGeneratedVisibility(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "User.java"), `package demo;

import lombok.Data;

@Data
public class User {
	private String name;
	private int age;
}
`)
	g := graph.New()
	reg := parser.NewRegistry()
	reg.Register(languages.NewJavaExtractor())
	idx := New(g, reg, config.Default().Index, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	var marked *graph.Node
	for _, n := range g.AllNodes() {
		if n.Meta == nil {
			continue
		}
		if v, _ := n.Meta["has_generated_members"].(bool); v {
			marked = n
			break
		}
	}
	require.NotNil(t, marked, "the @Data class must be flagged with generated members")
	require.Equal(t, "User", marked.Name)
	require.Equal(t, "lombok", marked.Meta["codegen_tool"])
	require.Contains(t, marked.Meta["generated_members"].([]string), "getters")
}
