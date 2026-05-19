package languages

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// findCellByName scans for a node whose Name matches and returns it
// along with whether one was found. Names are stable identifiers
// like `cell_0` or `markdown_cell_1`.
func findCellByName(nodes []*graph.Node, name string) (*graph.Node, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
	}
	return nil, false
}

func TestJupyterExtractor_ExtensionsAndLanguage(t *testing.T) {
	e := NewJupyterExtractor()
	require.Equal(t, "jupyter", e.Language())
	require.ElementsMatch(t, []string{".ipynb", ".dbc"}, e.Extensions())
}

func TestJupyterExtractor_IPYNB_CodeAndMarkdownCells(t *testing.T) {
	src := []byte(`{
  "nbformat": 4,
  "metadata": {
    "kernelspec": {"name": "python3", "language": "python"}
  },
  "cells": [
    {"cell_type": "markdown", "source": ["# Analysis\n", "\n", "Intro text."]},
    {"cell_type": "code", "source": ["import pandas as pd\n", "df = pd.read_csv('x.csv')\n"]},
    {"cell_type": "code", "source": "%%sql\nSELECT * FROM events WHERE day=CURRENT_DATE"},
    {"cell_type": "raw", "source": "raw content"}
  ]
}`)

	res, err := NewJupyterExtractor().Extract("notebooks/analysis.ipynb", src)
	require.NoError(t, err)

	// File node + 4 cell nodes.
	require.Len(t, res.Nodes, 5)
	require.Equal(t, graph.KindFile, res.Nodes[0].Kind)
	require.Equal(t, "notebooks/analysis.ipynb", res.Nodes[0].ID)
	require.Equal(t, "jupyter", res.Nodes[0].Language)

	md, ok := findCellByName(res.Nodes, "markdown_cell_0")
	require.True(t, ok, "markdown_cell_0 missing")
	assert.Equal(t, graph.KindVariable, md.Kind)
	assert.Equal(t, "markdown", md.Meta["cell_kind"])
	assert.Equal(t, 0, md.Meta["cell_index"])
	assert.Equal(t, "markdown", md.Meta["cell_language"])

	py, ok := findCellByName(res.Nodes, "cell_1")
	require.True(t, ok, "cell_1 (code) missing")
	assert.Equal(t, graph.KindFunction, py.Kind)
	assert.Equal(t, "code", py.Meta["cell_kind"])
	assert.Equal(t, 1, py.Meta["cell_index"])
	assert.Equal(t, "python", py.Meta["cell_language"])

	sqlCell, ok := findCellByName(res.Nodes, "cell_2")
	require.True(t, ok, "cell_2 (code %%sql) missing")
	assert.Equal(t, graph.KindFunction, sqlCell.Kind)
	// %%sql cell magic overrides the python kernel for this one cell.
	assert.Equal(t, "sql", sqlCell.Meta["cell_language"])

	raw, ok := findCellByName(res.Nodes, "raw_cell_3")
	require.True(t, ok, "raw_cell_3 missing")
	assert.Equal(t, graph.KindVariable, raw.Kind)
	assert.Equal(t, "raw", raw.Meta["cell_kind"])

	// Every cell connects to the file node via EdgeDefines.
	var defines int
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeDefines && ed.From == "notebooks/analysis.ipynb" {
			defines++
		}
	}
	assert.Equal(t, 4, defines, "expected 4 EdgeDefines from file -> cells")
}

func TestJupyterExtractor_IPYNB_StringSourceVariant(t *testing.T) {
	// nbformat permits cell.source as a plain string (not array).
	src := []byte(`{
  "nbformat": 4,
  "metadata": {"kernelspec": {"name": "python3", "language": "python"}},
  "cells": [{"cell_type": "code", "source": "x = 1\ny = 2\n"}]
}`)
	res, err := NewJupyterExtractor().Extract("nb.ipynb", src)
	require.NoError(t, err)
	c, ok := findCellByName(res.Nodes, "cell_0")
	require.True(t, ok)
	assert.Equal(t, graph.KindFunction, c.Kind)
	assert.Equal(t, "python", c.Meta["cell_language"])
}

func TestJupyterExtractor_IPYNB_KernelLanguageFallback(t *testing.T) {
	// No kernelspec; falls through language_info -> "scala".
	src := []byte(`{
  "nbformat": 4,
  "metadata": {"language_info": {"name": "Scala"}},
  "cells": [{"cell_type": "code", "source": "val x = 1"}]
}`)
	res, err := NewJupyterExtractor().Extract("nb.ipynb", src)
	require.NoError(t, err)
	c, ok := findCellByName(res.Nodes, "cell_0")
	require.True(t, ok)
	assert.Equal(t, "scala", c.Meta["cell_language"])
}

func TestJupyterExtractor_IPYNB_NBFormat3Legacy(t *testing.T) {
	// nbformat 3 (legacy) used worksheets[].cells[].
	src := []byte(`{
  "nbformat": 3,
  "metadata": {"kernelspec": {"language": "r"}},
  "worksheets": [{"cells": [
    {"cell_type": "code", "source": "x <- 1"},
    {"cell_type": "markdown", "source": "# heading"}
  ]}]
}`)
	res, err := NewJupyterExtractor().Extract("nb.ipynb", src)
	require.NoError(t, err)
	c0, ok := findCellByName(res.Nodes, "cell_0")
	require.True(t, ok)
	assert.Equal(t, "r", c0.Meta["cell_language"])
	c1, ok := findCellByName(res.Nodes, "markdown_cell_1")
	require.True(t, ok)
	assert.Equal(t, "markdown", c1.Meta["cell_kind"])
}

func TestJupyterExtractor_IPYNB_EmptyAndMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":     []byte(""),
		"whitespace": []byte("   \n\t\n"),
		"malformed": []byte(`{ "cells": [ broken `),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := NewJupyterExtractor().Extract("bad.ipynb", src)
			require.NoError(t, err)
			require.Len(t, res.Nodes, 1, "expected only file node")
			assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
		})
	}
}

func TestJupyterExtractor_IPYNB_CellLanguageExecutionMagic(t *testing.T) {
	// `%%time` is an execution magic — should NOT change the cell
	// language; it stays in the kernel language.
	src := []byte(`{
  "nbformat": 4,
  "metadata": {"kernelspec": {"language": "python"}},
  "cells": [{"cell_type": "code", "source": "%%time\nprint('hi')"}]
}`)
	res, err := NewJupyterExtractor().Extract("nb.ipynb", src)
	require.NoError(t, err)
	c, ok := findCellByName(res.Nodes, "cell_0")
	require.True(t, ok)
	assert.Equal(t, "python", c.Meta["cell_language"])
}

func TestIsDatabricksSourceFile(t *testing.T) {
	cases := []struct {
		name string
		path string
		src  string
		want bool
	}{
		{"python notebook", "etl/job.py", "# Databricks notebook source\nimport pandas\n", true},
		{"python no header", "etl/lib.py", "import pandas\n", false},
		{"scala notebook", "etl/job.scala", "// Databricks notebook source\nval x = 1\n", true},
		{"sql notebook", "etl/job.sql", "-- Databricks notebook source\nSELECT 1\n", true},
		{"r notebook upper-case ext", "etl/job.R", "# Databricks notebook source\nx <- 1\n", true},
		{"r notebook lower-case ext", "etl/job.r", "# Databricks notebook source\nx <- 1\n", true},
		{"wrong marker for ext", "etl/job.py", "// Databricks notebook source\nx = 1\n", false},
		{"unsupported ext", "etl/job.go", "# Databricks notebook source\n", false},
		{"empty file", "etl/x.py", "", false},
		{"leading blank lines", "etl/x.py", "\n\n# Databricks notebook source\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsDatabricksSourceFile(tc.path, []byte(tc.src))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMaybeEnrichDatabricks_PythonNotebook(t *testing.T) {
	src := []byte(`# Databricks notebook source
# MAGIC %md
# MAGIC # Daily ETL
# COMMAND ----------
import pandas as pd
df = pd.read_csv("/dbfs/x.csv")
# COMMAND ----------
# MAGIC %sql
# MAGIC SELECT * FROM events WHERE date = current_date()
# COMMAND ----------
df.write.saveAsTable("results")
`)
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("etl/job.py", "etl/job.py", src, res)
	require.True(t, ok)

	// Three cells: markdown, python (default), sql magic, python.
	// Counts (skipping empty): 4 cells total.
	require.Len(t, res.Nodes, 4)

	c0, ok := findCellByName(res.Nodes, "dbx_cell_0")
	require.True(t, ok)
	assert.Equal(t, "markdown", c0.Meta["cell_language"])
	assert.Equal(t, "markdown", c0.Meta["cell_kind"])
	assert.Equal(t, graph.KindVariable, c0.Kind)
	assert.Equal(t, "databricks", c0.Meta["notebook"])
	assert.Equal(t, "python", c0.Meta["host_language"])

	c1, ok := findCellByName(res.Nodes, "dbx_cell_1")
	require.True(t, ok)
	assert.Equal(t, "python", c1.Meta["cell_language"])
	assert.Equal(t, "code", c1.Meta["cell_kind"])
	assert.Equal(t, graph.KindFunction, c1.Kind)

	c2, ok := findCellByName(res.Nodes, "dbx_cell_2")
	require.True(t, ok)
	assert.Equal(t, "sql", c2.Meta["cell_language"])
	assert.Equal(t, "code", c2.Meta["cell_kind"])

	c3, ok := findCellByName(res.Nodes, "dbx_cell_3")
	require.True(t, ok)
	assert.Equal(t, "python", c3.Meta["cell_language"])
}

func TestMaybeEnrichDatabricks_ScalaNotebook(t *testing.T) {
	src := []byte(`// Databricks notebook source
// MAGIC %md
// MAGIC ## Scala demo
// COMMAND ----------
val data = spark.range(10).toDF("id")
// COMMAND ----------
// MAGIC %python
// MAGIC print("polyglot")
`)
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("etl/demo.scala", "etl/demo.scala", src, res)
	require.True(t, ok)
	require.Len(t, res.Nodes, 3)

	c0, _ := findCellByName(res.Nodes, "dbx_cell_0")
	require.NotNil(t, c0)
	assert.Equal(t, "markdown", c0.Meta["cell_language"])

	c1, _ := findCellByName(res.Nodes, "dbx_cell_1")
	require.NotNil(t, c1)
	assert.Equal(t, "scala", c1.Meta["cell_language"])

	c2, _ := findCellByName(res.Nodes, "dbx_cell_2")
	require.NotNil(t, c2)
	assert.Equal(t, "python", c2.Meta["cell_language"])
}

func TestMaybeEnrichDatabricks_SQLNotebook(t *testing.T) {
	src := []byte(`-- Databricks notebook source
-- MAGIC %md
-- MAGIC # Report
-- COMMAND ----------
SELECT * FROM accounts
-- COMMAND ----------
SELECT * FROM transactions WHERE created_at > current_date() - 30
`)
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("reports/q1.sql", "reports/q1.sql", src, res)
	require.True(t, ok)
	require.Len(t, res.Nodes, 3)

	c1, _ := findCellByName(res.Nodes, "dbx_cell_1")
	require.NotNil(t, c1)
	assert.Equal(t, "sql", c1.Meta["cell_language"])
	assert.Equal(t, "sql", c1.Meta["host_language"])
}

func TestMaybeEnrichDatabricks_RNotebook(t *testing.T) {
	src := []byte(`# Databricks notebook source
# MAGIC %md
# MAGIC ## R demo
# COMMAND ----------
x <- 1:10
mean(x)
`)
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("etl/demo.R", "etl/demo.R", src, res)
	require.True(t, ok)
	require.Len(t, res.Nodes, 2)
	c1, _ := findCellByName(res.Nodes, "dbx_cell_1")
	require.NotNil(t, c1)
	assert.Equal(t, "r", c1.Meta["cell_language"])
}

func TestMaybeEnrichDatabricks_NotADatabricksFile(t *testing.T) {
	// Plain Python — no magic header.
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("lib.py", "lib.py", []byte("import os\nx = 1\n"), res)
	assert.False(t, ok)
	assert.Empty(t, res.Nodes)
}

func TestMaybeEnrichDatabricks_NoSeparators(t *testing.T) {
	// Magic header only; whole body is one cell.
	src := []byte(`# Databricks notebook source
import pandas as pd
df = pd.read_csv("x")
`)
	res := &parser.ExtractionResult{}
	ok := MaybeEnrichDatabricks("etl/job.py", "etl/job.py", src, res)
	require.True(t, ok)
	require.Len(t, res.Nodes, 1)
	c0, _ := findCellByName(res.Nodes, "dbx_cell_0")
	require.NotNil(t, c0)
	assert.Equal(t, "python", c0.Meta["cell_language"])
	assert.Equal(t, 0, c0.Meta["cell_index"])
}

func TestJupyterExtractor_DBCArchive(t *testing.T) {
	// Build a .dbc fixture in-memory: a ZIP holding one nbformat
	// .ipynb and one Databricks-native JSON notebook.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Entry 1: nbformat ipynb
	ipynb, err := zw.Create("notebooks/jupyter_one.ipynb")
	require.NoError(t, err)
	_, err = ipynb.Write([]byte(`{
  "nbformat": 4,
  "metadata": {"kernelspec": {"language": "python"}},
  "cells": [
    {"cell_type": "code", "source": "print('hi')"},
    {"cell_type": "markdown", "source": "# heading"}
  ]
}`))
	require.NoError(t, err)

	// Entry 2: Databricks-native JSON
	dbc, err := zw.Create("notebooks/databricks_native.json")
	require.NoError(t, err)
	_, err = dbc.Write([]byte(`{
  "language": "scala",
  "commands": [
    {"command": "val x = 1", "language": "scala"},
    {"command": "# Doc heading", "subtype": "markdownCommand"}
  ]
}`))
	require.NoError(t, err)

	require.NoError(t, zw.Close())

	res, err := NewJupyterExtractor().Extract("export.dbc", buf.Bytes())
	require.NoError(t, err)

	// File node + 2 cells per entry = 5 nodes.
	require.Len(t, res.Nodes, 5)
	require.Equal(t, "databricks", res.Nodes[0].Language)

	// Per-archive-entry cells carry archive_member meta.
	var archiveMembers []string
	for _, n := range res.Nodes[1:] {
		if member, ok := n.Meta["archive_member"].(string); ok {
			archiveMembers = append(archiveMembers, member)
		}
	}
	assert.Contains(t, archiveMembers, "notebooks/jupyter_one.ipynb")
	assert.Contains(t, archiveMembers, "notebooks/databricks_native.json")

	// IDs are entry-qualified so two cell_0s don't collide.
	ids := make(map[string]int)
	for _, n := range res.Nodes[1:] {
		ids[n.ID]++
	}
	for id, count := range ids {
		assert.Equal(t, 1, count, "duplicate cell id %q across archive members", id)
	}
}

func TestJupyterExtractor_DBC_InvalidZip(t *testing.T) {
	// Garbage bytes — JupyterExtractor must not error, just yield
	// the file node.
	res, err := NewJupyterExtractor().Extract("bad.dbc", []byte("not a zip"))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}

func TestSplitDatabricksCells_FlexibleSeparator(t *testing.T) {
	// Separator with extra dashes is still recognised.
	src := []byte(`# Databricks notebook source
import os
# COMMAND ------------------
print("two")
# COMMAND ----------
print("three")
`)
	cells := splitDatabricksCells(src, "#")
	require.Len(t, cells, 3)
	assert.Equal(t, 2, cells[0].startLine)
	// Cells are non-empty.
	for _, c := range cells {
		assert.NotEmpty(t, c.body)
	}
}

func TestClassifyDatabricksCell_PreservesHostLang(t *testing.T) {
	body := "import pandas as pd\ndf = pd.read_csv('x')\n"
	lang, clean := classifyDatabricksCell(body, "#", "python")
	assert.Equal(t, "python", lang)
	assert.Equal(t, body, clean)
}

func TestClassifyDatabricksCell_SQLMagic(t *testing.T) {
	body := "# MAGIC %sql\n# MAGIC SELECT * FROM x\n# MAGIC WHERE 1=1"
	lang, clean := classifyDatabricksCell(body, "#", "python")
	assert.Equal(t, "sql", lang)
	// MAGIC prefix should be stripped from kept lines; the bare
	// %sql directive is dropped.
	assert.Contains(t, clean, "SELECT * FROM x")
	assert.NotContains(t, clean, "MAGIC")
	assert.NotContains(t, clean, "%sql")
}

func TestClassifyDatabricksCell_MarkdownMagic(t *testing.T) {
	body := "-- MAGIC %md\n-- MAGIC # Heading\n-- MAGIC Body text"
	lang, clean := classifyDatabricksCell(body, "--", "sql")
	assert.Equal(t, "markdown", lang)
	assert.Contains(t, clean, "# Heading")
	assert.NotContains(t, clean, "MAGIC")
}

func TestJupyterExtractor_RegistryWiring(t *testing.T) {
	// The forest-registration table must not have claimed `.ipynb`
	// — bespoke wins. After RegisterAll, both extensions resolve to
	// our extractor.
	reg := parser.NewRegistry()
	RegisterAll(reg)
	for _, ext := range []string{".ipynb", ".dbc"} {
		e, ok := reg.GetByExtension(ext)
		require.True(t, ok, "extension %q not registered", ext)
		assert.Equal(t, "jupyter", e.Language())
	}
}
