package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestDart_AliasedImport_CapturesAndAttributesCalls(t *testing.T) {
	src := []byte(`import 'package:flutter/material.dart' as fl;
import 'package:other/svc.dart';

void main() {
  fl.runApp();
  bareCall();
}
`)
	e := NewDartExtractor()
	res, err := e.Extract("lib/main.dart", src)
	require.NoError(t, err)

	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeImports,
		"lib/main.dart", "unresolved::import::package:flutter/material.dart"),
		"import edge to package:flutter/material.dart should be present")

	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeCalls,
		"lib/main.dart::main", "unresolved::extern::package:flutter/material.dart::runApp"),
		"`fl.runApp()` should attribute to its aliased URI")

	// `bareCall()` has no alias prefix — it should land at the
	// name-only fallback, not on the aliased URI.
	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeCalls,
		"lib/main.dart::main", "unresolved::*.bareCall"),
		"non-aliased call should keep the legacy name-only stub")
}

func TestDart_NoAlias_FallsBackToNameOnlyStub(t *testing.T) {
	// No `as <alias>` in any import — every call must remain on
	// the name-only stub. Regression guard: the alias path must
	// not over-attribute when no alias matches.
	src := []byte(`import 'package:flutter/material.dart';

void main() {
  print('hi');
}
`)
	e := NewDartExtractor()
	res, err := e.Extract("lib/main.dart", src)
	require.NoError(t, err)

	require.True(t, hasEdgeBetween(res.Edges, graph.EdgeCalls,
		"lib/main.dart::main", "unresolved::*.print"))

	// And no extern edge slipped in for `print`.
	for _, edge := range res.Edges {
		if edge.Kind == graph.EdgeCalls && edge.From == "lib/main.dart::main" {
			assert.NotContains(t, edge.To, "unresolved::extern::",
				"non-aliased call must not attribute")
		}
	}
}

func TestDart_AliasExtractionParsesAsClause(t *testing.T) {
	cases := []struct {
		text  string
		want  string
	}{
		{`import 'package:flutter/material.dart' as fl;`, "fl"},
		{`import "dart:async" as async_lib show Future;`, "async_lib"},
		{`import 'lib.dart' as $special;`, "$special"},
		{`import 'lib.dart';`, ""},
		{`import 'package:as_in_uri/x.dart';`, ""},  // no `as` clause
	}
	for _, c := range cases {
		got := extractDartImportAlias(c.text)
		assert.Equal(t, c.want, got, "input: %s", c.text)
	}
}
