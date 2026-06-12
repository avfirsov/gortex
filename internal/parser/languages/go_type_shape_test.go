package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGoTypeShape_AliasEmitsAliasesEdge(t *testing.T) {
	src := `package foo

import "github.com/google/uuid"

type ID = uuid.UUID
`
	fix := runGoExtract(t, src)

	idNode, ok := fix.nodesByID["pkg/foo.go::ID"]
	if !ok {
		t.Fatalf("ID node missing")
	}
	if v, _ := idNode.Meta["alias"].(bool); !v {
		t.Errorf("alias meta missing on alias node")
	}

	aliases := fix.edgesByKind[graph.EdgeAliases]
	if len(aliases) != 1 {
		t.Fatalf("expected 1 EdgeAliases, got %d", len(aliases))
	}
	if aliases[0].From != "pkg/foo.go::ID" {
		t.Errorf("alias edge from = %q", aliases[0].From)
	}
	if aliases[0].To != "unresolved::UUID" {
		t.Errorf("alias edge to = %q (canonicalised to last segment)", aliases[0].To)
	}
}

func TestGoTypeShape_NewtypeEmitsExtendsEdge(t *testing.T) {
	src := `package foo

type Username string
type IDList []int
`
	fix := runGoExtract(t, src)

	extends := fix.edgesByKind[graph.EdgeExtends]
	gotTargets := map[string]bool{}
	for _, e := range extends {
		gotTargets[e.To] = true
	}
	if !gotTargets["unresolved::string"] {
		t.Errorf("Username should extend string, got: %v", gotTargets)
	}
	if !gotTargets["unresolved::int"] {
		t.Errorf("IDList should extend int (slice element), got: %v", gotTargets)
	}
}

func TestGoTypeShape_StructEmbeddingEmitsComposes(t *testing.T) {
	src := `package foo

type Base struct {
	ID int
}

type Server struct {
	Base
	Name string
}
`
	fix := runGoExtract(t, src)

	composes := fix.edgesByKind[graph.EdgeComposes]
	if len(composes) != 1 {
		t.Fatalf("expected 1 composes edge (Server embeds Base), got %d: %+v", len(composes), composes)
	}
	if composes[0].From != "pkg/foo.go::Server" {
		t.Errorf("composes from = %q", composes[0].From)
	}
	if composes[0].To != "unresolved::Base" {
		t.Errorf("composes to = %q", composes[0].To)
	}
}

func TestGoTypeShape_InterfaceEmbeddingEmitsComposes(t *testing.T) {
	src := `package foo

import "io"

type ReadWriteCloser interface {
	io.Reader
	io.Writer
	Close() error
}
`
	fix := runGoExtract(t, src)

	composes := fix.edgesByKind[graph.EdgeComposes]
	if len(composes) != 2 {
		t.Fatalf("expected 2 composes edges (Reader, Writer), got %d: %+v", len(composes), composes)
	}
	gotTargets := map[string]bool{}
	for _, e := range composes {
		if e.From != "pkg/foo.go::ReadWriteCloser" {
			t.Errorf("composes from = %q", e.From)
		}
		gotTargets[e.To] = true
	}
	if !gotTargets["unresolved::Reader"] || !gotTargets["unresolved::Writer"] {
		t.Errorf("composes targets = %v, want Reader + Writer", gotTargets)
	}
}

func TestGoTypeShape_AnonymousStructBodyDoesNotExtend(t *testing.T) {
	src := `package foo

type Empty struct{}
type Iface interface{}
`
	fix := runGoExtract(t, src)
	if len(fix.edgesByKind[graph.EdgeExtends]) != 0 {
		t.Errorf("anonymous struct/interface should not produce extends, got %+v",
			fix.edgesByKind[graph.EdgeExtends])
	}
	if len(fix.edgesByKind[graph.EdgeAliases]) != 0 {
		t.Errorf("non-alias decl should not produce aliases edges")
	}
}
