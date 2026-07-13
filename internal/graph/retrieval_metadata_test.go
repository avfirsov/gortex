package graph

import (
	"strings"
	"testing"
)

func TestNodeRetrievalMetadataFallbackAndOverride(t *testing.T) {
	n := &Node{
		QualName: "pkg.Service.Handle",
		Meta: map[string]any{
			"signature": "func (s *Service) Handle()",
			"doc":       "Handles requests.",
		},
	}
	fallback := n.RetrievalMetadata()
	if fallback.Signature != "func (s *Service) Handle()" || fallback.QualName != n.QualName || fallback.Doc != "Handles requests." {
		t.Fatalf("fallback metadata = %#v", fallback)
	}

	SetRetrievalMetadata(n, RetrievalMetadata{
		Signature: "func Service.Handle()",
		QualName:  "Service.Handle",
		Doc:       "Handle a request.",
	})
	got := n.RetrievalMetadata()
	if got.Signature != "func Service.Handle()" || got.QualName != "Service.Handle" || got.Doc != "Handle a request." {
		t.Fatalf("normalized metadata = %#v", got)
	}
	if n.QualName != "pkg.Service.Handle" || n.Meta["signature"] != "func (s *Service) Handle()" || n.Meta["doc"] != "Handles requests." {
		t.Fatalf("parser metadata mutated: %#v", n)
	}

	SetRetrievalMetadata(n, RetrievalMetadata{})
	if got := n.RetrievalMetadata(); got != fallback {
		t.Fatalf("fallback after clearing = %#v, want %#v", got, fallback)
	}
}

func TestSetRetrievalMetadataEmptyDoesNotAllocateMeta(t *testing.T) {
	n := &Node{}
	SetRetrievalMetadata(n, RetrievalMetadata{})
	if n.Meta != nil {
		t.Fatalf("empty metadata allocated map: %#v", n.Meta)
	}
}

func TestRetrievalMetadataCompactsLegacyAndNormalizedSignatures(t *testing.T) {
	n := &Node{
		Kind: KindFunction, Name: "run", Language: "rust",
		Meta: map[string]any{"signature": "fn run(value: Input) -> Output { let INLINE_SECRET = \"token\"; }"},
	}
	if got := n.RetrievalMetadata().Signature; got != "fn run(value: Input) -> Output" {
		t.Fatalf("legacy signature = %q", got)
	}
	SetRetrievalMetadata(n, RetrievalMetadata{
		Signature: "fn run<T>(value: T) -> T { expose(INLINE_SECRET) } " + strings.Repeat("x", 700),
	})
	got := n.RetrievalMetadata().Signature
	if got != "fn run<T>(value: T) -> T" || strings.Contains(got, "INLINE_SECRET") || len(got) > maxRetrievalSignature {
		t.Fatalf("normalized signature not compact: len=%d value=%q", len(got), got)
	}
	long := &Node{Kind: KindFunction, Name: "long", Language: "rust", Meta: map[string]any{
		"signature": "fn long(" + strings.Repeat("VeryLongType, ", 80) + ")",
	}}
	if got := long.RetrievalMetadata().Signature; len(got) > maxRetrievalSignature {
		t.Fatalf("legacy signature exceeded cap: len=%d", len(got))
	}
}

func TestRetrievalMetadataSuppressesLegacyVariableLocalsOnly(t *testing.T) {
	local := &Node{
		Kind: KindVariable, Name: "value", QualName: "pkg.resolve.value",
		Meta: map[string]any{"scope": "function", "signature": "let value = INLINE_SECRET", "doc": "local"},
	}
	if got := local.RetrievalMetadata(); got != (RetrievalMetadata{}) {
		t.Fatalf("legacy local metadata = %#v", got)
	}
	global := &Node{
		Kind: KindVariable, Name: "value", QualName: "pkg.value",
		Meta: map[string]any{"scope": "global", "signature": "var value string", "doc": "global"},
	}
	if got := global.RetrievalMetadata(); got.Signature != "var value string" || got.QualName != "pkg.value" || got.Doc != "global" {
		t.Fatalf("global metadata suppressed: %#v", got)
	}
}

func TestRetrievalMetadataSuppressesOwnerPayloadForChildScopes(t *testing.T) {
	for _, kind := range []NodeKind{KindParam, KindLocal, KindGenericParam} {
		n := &Node{
			Kind: kind, Name: "value", QualName: "pkg.resolve.value",
			Meta: map[string]any{
				"signature":            "fn resolve(value: Input) -> Output",
				"doc":                  "Resolves an input value.",
				metaRetrievalSignature: "fn resolve(value: Input) -> Output",
				metaRetrievalQualName:  "pkg.resolve",
				metaRetrievalDoc:       "Resolves an input value.",
			},
		}
		if got := n.RetrievalMetadata(); got != (RetrievalMetadata{}) {
			t.Errorf("kind %q leaked owner metadata: %#v", kind, got)
		}
	}
}
