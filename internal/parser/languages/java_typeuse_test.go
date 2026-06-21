package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestJavaTypeUse_LocalVariableEmitsTypedAs pins the LSP-free recall fix
// for Java: a `HttpResponse resp = client.get();` local declaration
// references type HttpResponse and must emit exactly one EdgeTypedAs to
// unresolved::HttpResponse, attributed to the enclosing method. Before
// the fix it only seeded the local type-env map (recall ~0 without an
// LSP). A primitive (`int`) declaration in the same body must emit none.
func TestJavaTypeUse_LocalVariableEmitsTypedAs(t *testing.T) {
	src := `package app;

public class Handler {
	public void handle() {
		HttpResponse resp = client.get();
		int count = 0;
		use(resp, count);
	}
}
`
	_, edges := runJavaExtract(t, "app/Handler.java", src)

	var hits []*graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::HttpResponse" {
			hits = append(hits, e)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 EdgeTypedAs -> unresolved::HttpResponse for the local `HttpResponse resp`, got %d: %v", len(hits), edgeTargets(edgesByKind(edges, graph.EdgeTypedAs)))
	}
	if !strings.Contains(hits[0].From, "handle") {
		t.Errorf("EdgeTypedAs From = %q, want it attributed to handle()", hits[0].From)
	}
	if hits[0].Origin != graph.OriginASTInferred {
		t.Errorf("EdgeTypedAs Origin = %q, want OriginASTInferred", hits[0].Origin)
	}

	// The primitive local `int count` must not seed a type-use edge.
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && (e.To == "unresolved::int" || e.To == "unresolved::count") {
			t.Errorf("primitive local emitted a type-use edge: %s -> %s", e.From, e.To)
		}
	}
}

// TestJavaTypeUse_FieldEmitsTypedAs pins the field-annotation half of the
// fix: a typed field (`Clock clock;`) references type Clock and must emit
// an EdgeTypedAs to unresolved::Clock without an LSP.
func TestJavaTypeUse_FieldEmitsTypedAs(t *testing.T) {
	src := `package app;

public class Service {
	private Clock clock;
	private int retries;
}
`
	_, edges := runJavaExtract(t, "app/Service.java", src)

	var hits []*graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::Clock" {
			hits = append(hits, e)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 EdgeTypedAs -> unresolved::Clock for field `Clock clock`, got %d: %v", len(hits), edgeTargets(edgesByKind(edges, graph.EdgeTypedAs)))
	}

	// The primitive field `int retries` must not seed a type-use edge.
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::int" {
			t.Errorf("primitive field emitted a type-use edge: %s -> %s", e.From, e.To)
		}
	}
}

// TestJavaTypeUse_GenericLocalUnwraps confirms a parameterized local
// (`List<User> users = ...`) emits the bare element / container type per
// canonicalizeJavaTypeRef (List<User> -> User), matching how param /
// return edges treat the same wrappers.
func TestJavaTypeUse_GenericLocalUnwraps(t *testing.T) {
	src := `package app;

public class Repo {
	public void load() {
		List<User> users = fetch();
		use(users);
	}
}
`
	_, edges := runJavaExtract(t, "app/Repo.java", src)

	hasUser := false
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::User" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Errorf("expected EdgeTypedAs -> unresolved::User (List<User> unwrapped); got %v", edgeTargets(edgesByKind(edges, graph.EdgeTypedAs)))
	}
}
