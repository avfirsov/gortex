package indexer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestParseRustUseFactsRetainsIdentity(t *testing.T) {
	src := `
pub(crate) use crate::{engine::Renderer as Draw, shapes::Circle};
use self::local::Helper as LocalHelper;
pub use super::shared::*;
use serde::Serialize;
`
	facts := parseRustUseFacts(src, "crate/src/api/mod.rs")
	require.Len(t, facts, 4, "external serde path must be omitted")

	byLocal := map[string]rustUseFact{}
	for _, fact := range facts {
		key := fact.localName
		if fact.glob {
			key = "*"
		}
		byLocal[key] = fact
	}
	require.Equal(t, rustUseFact{
		fromFile: "crate/src/engine.rs", sourceModule: "crate::engine",
		sourceName: "Renderer", localName: "Draw", visibility: "pub(crate)",
	}, byLocal["Draw"])
	require.Equal(t, rustUseFact{
		fromFile: "crate/src/shapes.rs", sourceModule: "crate::shapes",
		sourceName: "Circle", localName: "Circle", visibility: "pub(crate)",
	}, byLocal["Circle"])
	require.Equal(t, rustUseFact{
		fromFile: "crate/src/api/local.rs", sourceModule: "self::local",
		sourceName: "Helper", localName: "LocalHelper",
	}, byLocal["LocalHelper"])
	require.Equal(t, rustUseFact{
		fromFile: "crate/src/shared.rs", sourceModule: "super::shared",
		visibility: "pub", glob: true,
	}, byLocal["*"])
}

func TestResolveRustModulePathIsExplicitAndModuleAware(t *testing.T) {
	const srcFile = "crate/src/api/v1.rs"
	require.Equal(t, "crate/src/api/v1/models.rs", resolveRustModulePath("self::models", srcFile))
	require.Equal(t, "crate/src/api/models.rs", resolveRustModulePath("super::models", srcFile))
	require.Equal(t, "crate/src/models.rs", resolveRustModulePath("super::super::models", srcFile))
	require.Equal(t, "crate/src/models.rs", resolveRustModulePath("crate::models", srcFile))
	require.Empty(t, resolveRustModulePath("serde::Serialize", srcFile))
	require.Empty(t, resolveRustModulePath("super::models", "crate/src/lib.rs"))
}

func TestResolveBareTypeViaImportsRustAliasChain(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "crate/src/model.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/model.rs"},
		{ID: "crate/src/legacy.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/legacy.rs"},
	}, nil)
	mi := &MultiIndexer{}
	srcCache := map[string][]byte{
		"crate/src/main.rs":  []byte(`use crate::api::Public as Local;`),
		"crate/src/api.rs":   []byte(`pub use crate::model::Original as Public;`),
		"crate/src/model.rs": []byte(`pub struct Original;`),
	}
	got := mi.resolveBareTypeViaImports(
		"crate/src/main.rs", "Local", g, srcCache, map[string]map[string]string{},
	)
	require.Equal(t, "crate/src/model.rs::Original", got)
}

func TestResolveBareTypeViaImportsRustAmbiguityAndExternalStayUnresolved(t *testing.T) {
	tests := []struct {
		name     string
		consumer string
		files    map[string][]byte
	}{
		{
			name:     "duplicate public alias",
			consumer: `use crate::api::Public;`,
			files: map[string][]byte{
				"crate/src/api.rs": []byte(`
pub use crate::a::Original as Public;
pub use crate::b::Original as Public;
`),
			},
		},
		{
			name:     "external import",
			consumer: `use serde::Original;`,
			files:    map[string][]byte{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := graph.New()
			g.AddBatch([]*graph.Node{
				{ID: "crate/src/a.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/a.rs"},
				{ID: "crate/src/b.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/b.rs"},
			}, nil)
			srcCache := map[string][]byte{"crate/src/main.rs": []byte(tt.consumer)}
			for path, src := range tt.files {
				srcCache[path] = src
			}
			got := (&MultiIndexer{}).resolveBareTypeViaImports(
				"crate/src/main.rs", "Public", g, srcCache, map[string]map[string]string{},
			)
			if tt.name == "external import" {
				got = (&MultiIndexer{}).resolveBareTypeViaImports(
					"crate/src/main.rs", "Original", g, srcCache, map[string]map[string]string{},
				)
			}
			require.Empty(t, got)
		})
	}
}

func TestFollowRustReExportChainRejectsCycleAndDepthOverflow(t *testing.T) {
	mi := &MultiIndexer{}
	cycle := map[string][]byte{
		"crate/src/a.rs": []byte(`pub use crate::b::X;`),
		"crate/src/b.rs": []byte(`pub use crate::a::X;`),
	}
	_, unsafe := mi.followReExportChainChecked("crate/src/a.rs", "X", cycle)
	require.True(t, unsafe, "a re-export cycle must be non-resolvable")

	deep := map[string][]byte{}
	for i := 0; i <= maxReExportDepth; i++ {
		deep[fmt.Sprintf("crate/src/m%d.rs", i)] = []byte(fmt.Sprintf("pub use crate::m%d::X;", i+1))
	}
	_, unsafe = mi.followReExportChainChecked("crate/src/m0.rs", "X", deep)
	require.True(t, unsafe, "a chain beyond maxReExportDepth must be non-resolvable")
}

func TestResolveRustModulePathUsesCurrentModuleForEmptyTail(t *testing.T) {
	require.Equal(t, "crate/src/lib.rs", resolveRustModulePath("crate", "crate/src/lib.rs"))
	require.Equal(t, "crate/src/main.rs", resolveRustModulePath("crate", "crate/src/main.rs"))
	logicalRoot := resolveRustModulePath("crate", "crate/src/api/mod.rs")
	require.Equal(t, rustLogicalCrateRoot("crate/src"), logicalRoot)
	require.ElementsMatch(t, []string{"crate/src/lib.rs", "crate/src/main.rs"}, rustFileCandidates(logicalRoot))
	require.Equal(t, "crate/src/lib.rs", resolveRustModulePath("self", "crate/src/lib.rs"))
	require.Equal(t, "crate/src/main.rs", resolveRustModulePath("self", "crate/src/main.rs"))
	require.Equal(t, "crate/src/api/mod.rs", resolveRustModulePath("self", "crate/src/api/mod.rs"))
	require.Equal(t, "crate/src/api.rs", resolveRustModulePath("self", "crate/src/api.rs"))
}

func TestResolveBareTypeViaImportsRustRootAndSelfChains(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "crate/src/model.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/model.rs"},
	}, nil)

	tests := []struct {
		name     string
		srcFile  string
		srcCache map[string][]byte
	}{
		{
			name:    "crate root library",
			srcFile: "crate/src/consumer.rs",
			srcCache: map[string][]byte{
				"crate/src/consumer.rs": []byte(`use crate::Public as Local;`),
				"crate/src/lib.rs":      []byte(`pub use crate::model::Original as Public;`),
			},
		},
		{
			name:    "crate root binary",
			srcFile: "crate/src/main.rs",
			srcCache: map[string][]byte{
				"crate/src/main.rs": []byte(`
use crate::Public as Local;
pub use crate::model::Original as Public;
`),
			},
		},
		{
			name:    "nested module in binary-only crate",
			srcFile: "crate/src/api/mod.rs",
			srcCache: map[string][]byte{
				"crate/src/api/mod.rs": []byte(`use crate::Public as Local;`),
				"crate/src/main.rs":    []byte(`pub use crate::model::Original as Public;`),
			},
		},
		{
			name:    "current mod file",
			srcFile: "crate/src/api/mod.rs",
			srcCache: map[string][]byte{
				"crate/src/api/mod.rs": []byte(`
use self::Public as Local;
pub use crate::model::Original as Public;
`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.srcCache["crate/src/model.rs"] = []byte(`pub struct Original;`)
			got := (&MultiIndexer{}).resolveBareTypeViaImports(
				tt.srcFile, "Local", g, tt.srcCache, map[string]map[string]string{},
			)
			require.Equal(t, "crate/src/model.rs::Original", got)
		})
	}
}

func TestParseRustUseFactsMasksCommentsAndStrings(t *testing.T) {
	src := `
// use crate::fake::Line;
/* use crate::fake::Block;
   /* pub use crate::fake::Nested; */
*/
const NORMAL: &str = "use crate::fake::Normal;";
const BYTE: &[u8] = b"pub use crate::fake::Byte;";
const RAW: &str = r#"use crate::fake::Raw;"#;
const RAW_BYTE: &[u8] = br##"pub use crate::fake::RawByte;"##;
use crate::real::Actual;
`
	facts := parseRustUseFacts(src, "crate/src/lib.rs")
	require.Equal(t, []rustUseFact{{
		fromFile: "crate/src/real.rs", sourceModule: "crate::real",
		sourceName: "Actual", localName: "Actual",
	}}, facts)
}

func TestResolveBareTypeViaImportsRustDuplicateAliasInOneSourceIsUnsafe(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "crate/src/same.rs::A", Kind: graph.KindType, Name: "A", FilePath: "crate/src/same.rs"},
		{ID: "crate/src/same.rs::B", Kind: graph.KindType, Name: "B", FilePath: "crate/src/same.rs"},
	}, nil)
	srcCache := map[string][]byte{
		"crate/src/main.rs": []byte(`use crate::api::Public;`),
		"crate/src/api.rs":  []byte(`pub use crate::same::{A as Public, B as Public};`),
		"crate/src/same.rs": []byte(`pub struct A; pub struct B;`),
	}

	edges := parseRustReExports(string(srcCache["crate/src/api.rs"]), "crate/src/api.rs")
	require.Len(t, edges, 1)
	require.True(t, edges[0].ambiguousName["Public"])
	require.NotContains(t, edges[0].names, "Public")
	require.Empty(t, (&MultiIndexer{}).resolveBareTypeViaImports(
		"crate/src/main.rs", "Public", g, srcCache, map[string]map[string]string{},
	))
}

func TestResolveBareTypeViaImportsRustMultipleGlobsUseExactGraphMatch(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*graph.Node
		want  string
	}{
		{
			name: "unique",
			nodes: []*graph.Node{
				{ID: "crate/src/a.rs::Foo", Kind: graph.KindType, Name: "Foo", FilePath: "crate/src/a.rs"},
				{ID: "crate/src/b.rs::Bar", Kind: graph.KindType, Name: "Bar", FilePath: "crate/src/b.rs"},
			},
			want: "crate/src/a.rs::Foo",
		},
		{
			name: "ambiguous",
			nodes: []*graph.Node{
				{ID: "crate/src/a.rs::Foo", Kind: graph.KindType, Name: "Foo", FilePath: "crate/src/a.rs"},
				{ID: "crate/src/b.rs::Foo", Kind: graph.KindType, Name: "Foo", FilePath: "crate/src/b.rs"},
			},
		},
		{name: "zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := graph.New()
			if len(tt.nodes) > 0 {
				g.AddBatch(tt.nodes, nil)
			}
			srcCache := map[string][]byte{
				"crate/src/main.rs":    []byte(`use crate::prelude::Foo;`),
				"crate/src/prelude.rs": []byte(`pub use crate::a::*; pub use crate::b::*;`),
			}
			got := (&MultiIndexer{}).resolveBareTypeViaImports(
				"crate/src/main.rs", "Foo", g, srcCache, map[string]map[string]string{},
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveBareTypeViaImportsRustDirectMultipleGlobs(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "crate/src/b.rs::Foo", Kind: graph.KindType, Name: "Foo", FilePath: "crate/src/b.rs"},
	}, nil)
	srcCache := map[string][]byte{
		"crate/src/main.rs": []byte(`use crate::a::*; use crate::b::*;`),
	}
	require.Equal(t, "crate/src/b.rs::Foo", (&MultiIndexer{}).resolveBareTypeViaImports(
		"crate/src/main.rs", "Foo", g, srcCache, map[string]map[string]string{},
	))
}

func TestResolveRustModulePathTopLevelSuperUsesLogicalRoot(t *testing.T) {
	logicalRoot := resolveRustModulePath("super", "crate/src/api/mod.rs")
	require.Equal(t, rustLogicalCrateRoot("crate/src"), logicalRoot)
	require.ElementsMatch(t, []string{"crate/src/lib.rs", "crate/src/main.rs"}, rustFileCandidates(logicalRoot))
}

func TestResolveBareTypeViaImportsRustTopLevelSuperChains(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "crate/src/model.rs::Original", Kind: graph.KindType, Name: "Original", FilePath: "crate/src/model.rs"},
	}, nil)
	tests := []struct {
		name     string
		srcFile  string
		srcCache map[string][]byte
	}{
		{
			name:    "private use from top-level mod",
			srcFile: "crate/src/api/mod.rs",
			srcCache: map[string][]byte{
				"crate/src/api/mod.rs": []byte(`use super::Public as Local;`),
				"crate/src/main.rs":    []byte(`pub use crate::model::Original as Public;`),
			},
		},
		{
			name:    "public use from top-level mod",
			srcFile: "crate/src/consumer.rs",
			srcCache: map[string][]byte{
				"crate/src/consumer.rs": []byte(`use crate::api::Public as Local;`),
				"crate/src/api/mod.rs":  []byte(`pub use super::Public;`),
				"crate/src/main.rs":     []byte(`pub use crate::model::Original as Public;`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.srcCache["crate/src/model.rs"] = []byte(`pub struct Original;`)
			got := (&MultiIndexer{}).resolveBareTypeViaImports(
				tt.srcFile, "Local", g, tt.srcCache, map[string]map[string]string{},
			)
			require.Equal(t, "crate/src/model.rs::Original", got)
		})
	}
}

func TestResolveBareTypeViaImportsRustLogicalRootRequiresUniqueGraphMatch(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*graph.Node
		want  string
	}{
		{
			name: "main only",
			nodes: []*graph.Node{
				{ID: "crate/src/main.rs::Public", Kind: graph.KindType, Name: "Public", FilePath: "crate/src/main.rs"},
			},
			want: "crate/src/main.rs::Public",
		},
		{
			name: "lib and main ambiguous",
			nodes: []*graph.Node{
				{ID: "crate/src/lib.rs::Public", Kind: graph.KindType, Name: "Public", FilePath: "crate/src/lib.rs"},
				{ID: "crate/src/main.rs::Public", Kind: graph.KindType, Name: "Public", FilePath: "crate/src/main.rs"},
			},
		},
		{name: "zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := graph.New()
			if len(tt.nodes) > 0 {
				g.AddBatch(tt.nodes, nil)
			}
			srcCache := map[string][]byte{
				"crate/src/api/mod.rs": []byte(`use crate::Public as Local;`),
				"crate/src/lib.rs":     []byte(`pub struct Public;`),
				"crate/src/main.rs":    []byte(`pub struct Public;`),
			}
			got := (&MultiIndexer{}).resolveBareTypeViaImports(
				"crate/src/api/mod.rs", "Local", g, srcCache, map[string]map[string]string{},
			)
			require.Equal(t, tt.want, got)
		})
	}
}
