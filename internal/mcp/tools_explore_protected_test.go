package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/rerank"
)

func TestReserveExploreConceptImplementationAcrossLanguages(t *testing.T) {
	const task = "Incorrect results in regex alternation prefilter matching: the optimizer produces false negatives when case-insensitive branches are combined"
	if rerank.ClassifyQuery(task) != rerank.QueryClassConcept {
		t.Fatalf("fixture must be a concept query: %q", task)
	}
	tests := []struct {
		language string
		name     string
		qualName string
	}{
		{language: "rust", name: "extract_alternation", qualName: "crate::regex::Extractor::extract_alternation"},
		{language: "go", name: "extractAlternation", qualName: "regex.Extractor.extractAlternation"},
		{language: "typescript", name: "buildAlternationPrefilter", qualName: "RegexOptimizer.buildAlternationPrefilter"},
	}
	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			field := &graph.Node{ID: test.language + "::regex", Name: "regex", QualName: "RegexMatcher.regex", Kind: graph.KindField, Language: test.language}
			enum := &graph.Node{ID: test.language + "::insensitive", Name: "Insensitive", QualName: "CaseMode.Insensitive", Kind: graph.KindVariable, Language: test.language}
			consumer := &graph.Node{ID: test.language + "::one_regex", Name: "one_regex", QualName: "InnerLiterals.one_regex", Kind: graph.KindMethod, Language: test.language}
			implementation := &graph.Node{ID: test.language + "::implementation", Name: test.name, QualName: test.qualName, Kind: graph.KindMethod, Language: test.language}
			candidates := []*rerank.Candidate{
				{Node: field, VectorRank: 0, TextRank: -1},
				{Node: enum, VectorRank: 1, TextRank: -1},
				{Node: consumer, VectorRank: 2, TextRank: 1},
				{Node: implementation, VectorRank: -1, TextRank: 2},
			}
			got, protectedID := reserveExploreConceptImplementation(task, rerank.QueryClassConcept, candidates, 3)
			if len(got) != len(candidates) {
				t.Fatalf("candidate count = %d, want %d", len(got), len(candidates))
			}
			if got[0].Node.ID != field.ID {
				t.Fatalf("semantic head changed: got %q want %q", got[0].Node.ID, field.ID)
			}
			if protectedID != implementation.ID || got[1].Node.ID != implementation.ID {
				t.Fatalf("protected implementation = %q at %q, want %q at rank 2", protectedID, got[1].Node.ID, implementation.ID)
			}
		})
	}
}

func TestExploreAnswerDraftReservesMentionedShortSameOwnerCallee(t *testing.T) {
	parent := &graph.Node{
		ID: "literal::extract_alternation", Name: "extract_alternation",
		QualName: "crate::regex::Extractor::extract_alternation", Kind: graph.KindMethod,
		FilePath: "crates/regex/src/literal.rs", Language: "rust",
	}
	union := &graph.Node{
		ID: "literal::union", Name: "union", QualName: "crate::regex::Extractor::union",
		Kind: graph.KindMethod, FilePath: parent.FilePath, Language: "rust",
	}
	finite := &graph.Node{
		ID: "literal::is_finite", Name: "is_finite", QualName: "crate::regex::Extractor::is_finite",
		Kind: graph.KindMethod, FilePath: parent.FilePath, Language: "rust",
	}
	targets := []exploreTarget{{
		node: parent, conceptImplementation: true,
		source:  "fn extract_alternation(&self) { // union both branches; union preserves safety\n self.union(); if self.is_finite() { self.union(); } }",
		callees: []*graph.Node{finite, union},
	}}
	const task = "Incorrect results in regex alternation literal optimization: the prefilter produces false negatives when case-insensitive branches are combined"
	entries := exploreAnswerDraft(task, targets)
	structural := 0
	for _, entry := range entries {
		if !entry.structural {
			continue
		}
		structural++
		if entry.node.ID != union.ID {
			t.Fatalf("reserved structural neighbor = %q, want %q: %#v", entry.node.ID, union.ID, entries)
		}
	}
	if structural != 1 {
		t.Fatalf("structural quota = %d, want exactly one: %#v", structural, entries)
	}
	if len(entries) > exploreDraftTotalLimit {
		t.Fatalf("draft exceeded bounded cardinality: %d", len(entries))
	}

	reads := 0
	materialized := materializeExploreStructuralSourceWithReader(
		context.Background(), task, targets, query.QueryOptions{},
		func(context.Context, *graph.Node) string {
			reads++
			return "fn union(&self) { /* SHORT_CAUSAL_BODY */ }"
		},
	)
	if reads != 1 {
		t.Fatalf("promoted source reads = %d, want exactly one", reads)
	}
	if len(materialized) != len(targets)+1 || materialized[len(materialized)-1].node.ID != union.ID {
		t.Fatalf("materialized targets = %#v, want one appended short causal callee", materialized)
	}
	if !strings.Contains(materialized[len(materialized)-1].source, "SHORT_CAUSAL_BODY") {
		t.Fatalf("short causal source was not materialized: %#v", materialized[len(materialized)-1])
	}
}

func TestExploreAnswerReadyRequiresProtectedBodyBearingImplementation(t *testing.T) {
	const task = "Incorrect results in regex alternation prefilter matching: the optimizer produces false negatives when case-insensitive branches are combined"
	field := &graph.Node{ID: "matcher::regex", Name: "regex", QualName: "RegexMatcher.regex", Kind: graph.KindField}
	if exploreAnswerReady(task, []exploreTarget{{node: field}}) {
		t.Fatal("a symptom field must not terminate ordinary concept localization")
	}

	implementation := &graph.Node{
		ID: "literal::extract_alternation", Name: "extract_alternation",
		QualName: "crate::regex::Extractor::extract_alternation", Kind: graph.KindMethod,
	}
	union := &graph.Node{ID: "literal::union", Name: "union", QualName: "crate::regex::Extractor::union", Kind: graph.KindMethod}
	protected := exploreTarget{
		node: implementation, conceptImplementation: true,
		source: "fn extract_alternation(&self) { self.union(); }", callees: []*graph.Node{union},
	}
	if !exploreAnswerReady(task, []exploreTarget{{node: field}, protected}) {
		t.Fatal("a body-bearing protected implementation with a callable causal neighbor must be answer-ready")
	}
	protected.source = ""
	if exploreAnswerReady(task, []exploreTarget{{node: field}, protected}) {
		t.Fatal("a signature-only protected callable must remain nonterminal")
	}

	uniqueLiteral := exploreTarget{node: field, exactContent: true}
	if !exploreAnswerReady("find the exact literal \"alternation sentinel\"", []exploreTarget{uniqueLiteral}) {
		t.Fatal("unique verified literal evidence must retain its terminal fast path")
	}
}

var benchmarkProtectedExploreID string

func BenchmarkReserveExploreConceptImplementation80(b *testing.B) {
	candidates := make([]*rerank.Candidate, 80)
	for i := range candidates {
		node := &graph.Node{
			ID: fmt.Sprintf("candidate::%02d", i), Name: fmt.Sprintf("config_field_%02d", i),
			QualName: fmt.Sprintf("Config.field%02d", i), Kind: graph.KindField,
		}
		if i%5 == 0 {
			node.Name = fmt.Sprintf("loadConfig%02d", i)
			node.QualName = "ConfigLoader." + node.Name
			node.Kind = graph.KindMethod
		}
		candidates[i] = &rerank.Candidate{Node: node, VectorRank: i, TextRank: -1}
	}
	candidates[79] = &rerank.Candidate{
		Node: &graph.Node{
			ID: "candidate::protected", Name: "buildAlternationPrefilter",
			QualName: "RegexOptimizer.buildAlternationPrefilter", Kind: graph.KindMethod,
		},
		VectorRank: -1, TextRank: 0,
	}
	const task = "Incorrect results in regex alternation prefilter matching: the optimizer produces false negatives when case-insensitive branches are combined"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, protectedID := reserveExploreConceptImplementation(task, rerank.QueryClassConcept, candidates, 10)
		if len(got) != len(candidates) || protectedID == "" {
			b.Fatal("protected selection failed")
		}
		benchmarkProtectedExploreID = protectedID
	}
}
