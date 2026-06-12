package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// TestHelm_HelpersTplDefinesAndIncludes covers a `_helpers.tpl` with two
// named templates where the second includes the first: we expect two
// KindFunction template nodes and an EdgeCalls from the including
// define into the included template.
func TestHelm_HelpersTplDefinesAndIncludes(t *testing.T) {
	src := []byte(`{{- define "mychart.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
{{- end -}}

{{- define "mychart.selectorLabels" -}}
{{ include "mychart.labels" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
`)
	e := NewHelmExtractor()
	result, err := e.Extract("mychart/templates/_helpers.tpl", src)
	require.NoError(t, err)

	nodes := nodesByID(result.Nodes)

	labelsID := "helm::template::mychart.labels"
	selectorID := "helm::template::mychart.selectorLabels"
	require.Contains(t, nodes, labelsID, "labels named template node")
	require.Contains(t, nodes, selectorID, "selectorLabels named template node")
	assert.Equal(t, graph.KindFunction, nodes[labelsID].Kind)
	assert.Equal(t, graph.KindFunction, nodes[selectorID].Kind)
	assert.Equal(t, "mychart.labels", nodes[labelsID].Name)
	assert.Equal(t, "named_template", nodes[labelsID].Meta["helm_kind"])

	// Both defines are owned by the file node.
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeDefines,
		"mychart/templates/_helpers.tpl", labelsID))
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeDefines,
		"mychart/templates/_helpers.tpl", selectorID))

	// The include sits inside the selectorLabels define block, so the
	// call edge originates from that template (not the file node).
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeCalls, selectorID, labelsID),
		"enclosing define should call the included template")
}

// TestHelm_ChartYamlChartAndDependencies covers a Chart.yaml with a
// name/version and a dependencies list: we expect the versioned
// KindPackage chart node and a cross-chart EdgeDependsOn to each
// dependency.
func TestHelm_ChartYamlChartAndDependencies(t *testing.T) {
	src := []byte(`apiVersion: v2
name: mychart
version: 1.2.3
description: A Helm chart
dependencies:
  - name: postgresql
    version: 12.0.0
    repository: https://charts.bitnami.com/bitnami
  - name: redis
    version: 17.0.0
    repository: https://charts.bitnami.com/bitnami
`)
	e := NewHelmExtractor()
	result, err := e.Extract("mychart/Chart.yaml", src)
	require.NoError(t, err)

	nodes := nodesByID(result.Nodes)
	chartID := "helm::chart::mychart@1.2.3"
	require.Contains(t, nodes, chartID, "versioned chart package node")
	assert.Equal(t, graph.KindPackage, nodes[chartID].Kind)
	assert.Equal(t, "mychart", nodes[chartID].Name)
	assert.Equal(t, "chart", nodes[chartID].Meta["helm_kind"])
	assert.Equal(t, "1.2.3", nodes[chartID].Meta["version"])

	// File defines the chart.
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeDefines,
		"mychart/Chart.yaml", chartID))

	// Cross-chart dependency edges target the version-less dependency ID.
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeDependsOn,
		chartID, "helm::chart::postgresql"),
		"chart should depend on postgresql")
	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeDependsOn,
		chartID, "helm::chart::redis"),
		"chart should depend on redis")
}

// TestHelm_RenderManifestIncludeViaYAMLExtractor covers a
// templates/deployment.yaml routed through the YAML extractor: the
// embedded include directive must yield an EdgeCalls into the named
// template, and the file must still parse as YAML without erroring.
func TestHelm_RenderManifestIncludeViaYAMLExtractor(t *testing.T) {
	src := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels:
    {{ include "mychart.labels" . }}
spec:
  replicas: 1
`)
	e := NewYAMLExtractor()
	result, err := e.Extract("mychart/templates/deployment.yaml", src)
	require.NoError(t, err, "render manifest should still parse as YAML")

	assert.True(t, hasEdgeBetween(result.Edges, graph.EdgeCalls,
		"mychart/templates/deployment.yaml", "helm::template::mychart.labels"),
		"render manifest should call the included named template")

	// The file node is still present (the YAML dispatch ran).
	require.NotEmpty(t, nodesOfKind(result.Nodes, graph.KindFile))
}
