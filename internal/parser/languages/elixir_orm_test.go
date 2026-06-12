package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func runElixirExtract(t *testing.T, filePath, src string) *extractedFixture {
	t.Helper()
	ext := NewElixirExtractor()
	result, err := ext.Extract(filePath, []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return foldFixture(result)
}

func TestElixirORM_EctoSchema(t *testing.T) {
	src := `defmodule MyApp.User do
  use Ecto.Schema

  schema "users" do
    field :email, :string
    timestamps()
  end
end
`
	fix := runElixirExtract(t, "lib/myapp/user.ex", src)
	models := fix.edgesByKind[graph.EdgeModelsTable]
	require.Len(t, models, 1, "Ecto schema should produce a models_table edge")
	assert.Equal(t, "lib/myapp/user.ex::MyApp.User", models[0].From)
	assert.Equal(t, "db::orm::users", models[0].To)
	assert.Equal(t, "ecto", models[0].Meta["orm"])
	assert.Equal(t, "schema-macro", models[0].Meta["binding"])
	assert.Equal(t, "users", models[0].Meta["table_name"])
	assert.Equal(t, "override", models[0].Meta["derivation"])

	// KindTable node must materialise.
	tableNode := fix.nodesByID["db::orm::users"]
	require.NotNil(t, tableNode)
	assert.Equal(t, graph.KindTable, tableNode.Kind)
	assert.Equal(t, "elixir-orm", tableNode.Meta["source"])
}

func TestElixirORM_EctoCustomTableName(t *testing.T) {
	src := `defmodule MyApp.LegacyOrder do
  use Ecto.Schema

  schema "tbl_orders_legacy" do
    field :amount, :integer
  end
end
`
	fix := runElixirExtract(t, "lib/myapp/legacy.ex", src)
	models := fix.edgesByKind[graph.EdgeModelsTable]
	require.Len(t, models, 1)
	assert.Equal(t, "db::orm::tbl_orders_legacy", models[0].To)
}

func TestElixirORM_NonEctoModuleIgnored(t *testing.T) {
	src := `defmodule MyApp.Service do
  def call(arg), do: arg
end
`
	fix := runElixirExtract(t, "lib/svc.ex", src)
	assert.Empty(t, fix.edgesByKind[graph.EdgeModelsTable])
}

func TestElixirORM_EmbeddedSchemaSkipped(t *testing.T) {
	// `embedded_schema do …` has no DB binding; we must not produce
	// a phantom KindTable for it.
	src := `defmodule MyApp.Address do
  use Ecto.Schema

  embedded_schema do
    field :line1, :string
  end
end
`
	fix := runElixirExtract(t, "lib/addr.ex", src)
	assert.Empty(t, fix.edgesByKind[graph.EdgeModelsTable], "embedded_schema must not produce a models_table edge")
}

func TestElixirHEEx_LocalAndRemoteComponents(t *testing.T) {
	src := "defmodule MyApp.PageLive do\n" +
		"  use Phoenix.LiveView\n" +
		"\n" +
		"  def render(assigns) do\n" +
		"    ~H\"\"\"\n" +
		"    <div>\n" +
		"      <.button class=\"primary\">Save</.button>\n" +
		"      <MyApp.UI.Card>Hi</MyApp.UI.Card>\n" +
		"      <span>plain html</span>\n" +
		"    </div>\n" +
		"    \"\"\"\n" +
		"  end\n" +
		"end\n"
	fix := runElixirExtract(t, "lib/page_live.ex", src)
	rendered := fix.edgesByKind[graph.EdgeRendersChild]
	require.GreaterOrEqual(t, len(rendered), 2, "expected ≥2 renders_child edges from the HEEx sigil (got %d)", len(rendered))

	names := map[string]bool{}
	for _, e := range rendered {
		assert.Equal(t, "lib/page_live.ex::MyApp.PageLive", e.From)
		name, _ := e.Meta["child_name"].(string)
		names[name] = true
		assert.Equal(t, "heex", e.Meta["flavor"])
		assert.Equal(t, "unresolved::"+name, e.To)
	}
	assert.True(t, names[".button"], "local component .button missing")
	assert.True(t, names["MyApp.UI.Card"], "remote component MyApp.UI.Card missing")
	assert.False(t, names["span"], "lowercase HTML primitive must NOT produce an edge")
}

func TestElixirHEEx_SkipsModuleWithoutSigils(t *testing.T) {
	src := `defmodule MyApp.Plain do
  def call(x), do: x + 1
end
`
	fix := runElixirExtract(t, "lib/plain.ex", src)
	assert.Empty(t, fix.edgesByKind[graph.EdgeRendersChild])
}

func TestElixirHEEx_DedupesRepeatedComponent(t *testing.T) {
	src := "defmodule MyApp.PageLive do\n" +
		"  def render(assigns) do\n" +
		"    ~H\"\"\"\n" +
		"    <.button>One</.button>\n" +
		"    <.button>Two</.button>\n" +
		"    <.button>Three</.button>\n" +
		"    \"\"\"\n" +
		"  end\n" +
		"end\n"
	fix := runElixirExtract(t, "lib/page_live.ex", src)
	rendered := fix.edgesByKind[graph.EdgeRendersChild]
	require.Len(t, rendered, 1, "the same component repeated must collapse to one edge per parent")
}

func TestParseHEExComponentRefs(t *testing.T) {
	cases := map[string][]string{
		"<.button>x</.button>":                 {".button"},
		"<MyApp.Card />":                       {"MyApp.Card"},
		"<.button /><MyApp.Card />":            {".button", "MyApp.Card"},
		"<div><span></span></div>":             {},
		"<.button /><.button />":               {".button"},
		"<.button class=\"x\"><MyApp.UI.Card>y</MyApp.UI.Card></.button>": {".button", "MyApp.UI.Card"},
	}
	for input, want := range cases {
		got := parseHEExComponentRefs(input)
		assert.ElementsMatch(t, want, got, "input=%q", input)
	}
}
