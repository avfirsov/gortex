package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGoChannels_SendStatement(t *testing.T) {
	src := `package foo

func Run(ch chan int) {
	ch <- 1
	ch <- 2
}
`
	fix := runGoExtract(t, src)
	sends := fix.edgesByKind[graph.EdgeSends]
	if len(sends) != 2 {
		t.Fatalf("expected 2 EdgeSends, got %d", len(sends))
	}
	for _, e := range sends {
		if e.From != "pkg/foo.go::Run" {
			t.Errorf("from = %q", e.From)
		}
		if e.To != "unresolved::ch" {
			t.Errorf("to = %q", e.To)
		}
	}
}

func TestGoChannels_RecvExpression(t *testing.T) {
	src := `package foo

func Run(ch chan int) int {
	x := <-ch
	return x
}
`
	fix := runGoExtract(t, src)
	recvs := fix.edgesByKind[graph.EdgeRecvs]
	if len(recvs) != 1 {
		t.Fatalf("expected 1 EdgeRecvs, got %d: %+v", len(recvs), recvs)
	}
	if recvs[0].To != "unresolved::ch" {
		t.Errorf("to = %q", recvs[0].To)
	}
}

func TestGoChannels_SelectStatement(t *testing.T) {
	src := `package foo

func Run(in chan int, out chan int) {
	select {
	case v := <-in:
		out <- v
	case <-in:
	}
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.edgesByKind[graph.EdgeSends]); got != 1 {
		t.Errorf("expected 1 send (out <- v), got %d", got)
	}
	if got := len(fix.edgesByKind[graph.EdgeRecvs]); got != 2 {
		t.Errorf("expected 2 recvs (both <-in branches), got %d", got)
	}
}

func TestGoChannels_NoFalsePositiveOnNonChannelCode(t *testing.T) {
	src := `package foo

func Run(x int) int {
	return -x
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.edgesByKind[graph.EdgeSends]); got != 0 {
		t.Errorf("unary minus must not produce sends, got %d", got)
	}
	if got := len(fix.edgesByKind[graph.EdgeRecvs]); got != 0 {
		t.Errorf("unary minus must not produce recvs, got %d", got)
	}
}

func TestGoChannels_ClosureBoundariesNotRecursed(t *testing.T) {
	// Channel ops inside a closure body should be walked when the
	// closure's own emitGoFunctionShape runs (closures are
	// extracted as separate function-shape nodes), not by the
	// outer function's walker. This test pins the v1 stance —
	// channel ops inside `func() {...}` don't fall through to the
	// outer Run.
	src := `package foo

func Run(ch chan int) {
	go func() {
		ch <- 1
	}()
}
`
	fix := runGoExtract(t, src)
	sends := fix.edgesByKind[graph.EdgeSends]
	// The send happens inside a func_literal — the outer Run's
	// walker stops at func_literal boundaries, and the closure
	// node itself doesn't (yet) get its body re-walked for
	// channel ops since emitGoFunctionShape is only called for
	// declared functions/methods. Today's behaviour: zero sends.
	// When closures gain function-shape extraction, this becomes
	// 1 send attributed to the closure node.
	if len(sends) != 0 {
		t.Errorf("v1: closure-internal sends should not attribute to outer fn (got %d)", len(sends))
	}
}
