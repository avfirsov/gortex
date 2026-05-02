package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGoConcurrency_BareGoroutineSpawn(t *testing.T) {
	src := `package foo

func worker() {}

func Run() {
	go worker()
}
`
	fix := runGoExtract(t, src)

	spawns := fix.edgesByKind[graph.EdgeSpawns]
	if len(spawns) != 1 {
		t.Fatalf("expected 1 EdgeSpawns, got %d: %+v", len(spawns), spawns)
	}
	e := spawns[0]
	if e.From != "pkg/foo.go::Run" {
		t.Errorf("from = %q", e.From)
	}
	if e.To != "unresolved::worker" {
		t.Errorf("to = %q", e.To)
	}
	if mode, _ := e.Meta["mode"].(string); mode != "goroutine" {
		t.Errorf("mode = %q, want goroutine", mode)
	}

	// EdgeCalls must still be present alongside the spawn.
	hasCall := false
	for _, ce := range fix.edgesByKind[graph.EdgeCalls] {
		if ce.From == e.From && ce.To == e.To {
			hasCall = true
		}
	}
	if !hasCall {
		t.Errorf("EdgeCalls should be emitted alongside EdgeSpawns")
	}
}

func TestGoConcurrency_MethodReceiverGoroutineSpawn(t *testing.T) {
	src := `package foo

type Server struct{}

func (s *Server) Tick() {}

func Start(s *Server) {
	go s.Tick()
}
`
	fix := runGoExtract(t, src)

	spawns := fix.edgesByKind[graph.EdgeSpawns]
	if len(spawns) != 1 {
		t.Fatalf("expected 1 EdgeSpawns, got %d: %+v", len(spawns), spawns)
	}
	if spawns[0].From != "pkg/foo.go::Start" {
		t.Errorf("from = %q", spawns[0].From)
	}
}

func TestGoConcurrency_RegularCallNotSpawn(t *testing.T) {
	src := `package foo

func worker() {}

func Run() {
	worker()
}
`
	fix := runGoExtract(t, src)

	if len(fix.edgesByKind[graph.EdgeSpawns]) != 0 {
		t.Errorf("regular call must not emit EdgeSpawns: %+v",
			fix.edgesByKind[graph.EdgeSpawns])
	}
}

func TestGoConcurrency_ClosureSpawn(t *testing.T) {
	// Anonymous-function spawns are very common in Go (`go func() {…}()`).
	// The closure body's calls produce EdgeSpawns when the closure
	// itself is launched via `go`. The current capture only handles
	// named-call spawns; anonymous-function spawn-of-the-closure is a
	// known v1 limitation tracked separately. Document by asserting
	// 0 spawns for this shape.
	src := `package foo

func Run() {
	go func() {
		println("hi")
	}()
}
`
	fix := runGoExtract(t, src)

	// The println inside the closure is NOT a goroutine spawn (it
	// runs synchronously inside the spawned closure). 0 spawn edges.
	if got := len(fix.edgesByKind[graph.EdgeSpawns]); got != 0 {
		t.Errorf("println inside spawned closure should not be a spawn site, got %d", got)
	}
}
