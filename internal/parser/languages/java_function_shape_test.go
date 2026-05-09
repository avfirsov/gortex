package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func runJavaExtract(t *testing.T, path, src string) ([]*graph.Node, []*graph.Edge) {
	t.Helper()
	ext := NewJavaExtractor()
	result, err := ext.Extract(path, []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return result.Nodes, result.Edges
}

func TestJavaFunctionShape_MethodParamsAndReturn(t *testing.T) {
	src := `package x;

public class UserService {
	public Optional<User> getById(long id, AuthCtx ctx) {
		return null;
	}
}
`
	nodes, edges := runJavaExtract(t, "x/UserService.java", src)

	params := nodesOfKind(nodes, graph.KindParam)
	if len(params) != 2 {
		t.Fatalf("expected 2 params, got %d: %v", len(params), nodeNames(params))
	}

	typed := edgesByKind(edges, graph.EdgeTypedAs)
	hasAuthCtx := false
	for _, e := range typed {
		if e.To == "unresolved::AuthCtx" {
			hasAuthCtx = true
		}
	}
	if !hasAuthCtx {
		t.Errorf("expected EdgeTypedAs → AuthCtx; got %v", edgeTargets(typed))
	}

	returns := edgesByKind(edges, graph.EdgeReturns)
	hasUser := false
	for _, e := range returns {
		if e.To == "unresolved::User" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Errorf("expected EdgeReturns → User (Optional unwrapped); got %v", edgeTargets(returns))
	}
}

func TestJavaFunctionShape_GenericMethod(t *testing.T) {
	src := `package x;

public class Util {
	public static <T extends Number> T identity(T x) { return x; }
}
`
	nodes, edges := runJavaExtract(t, "x/Util.java", src)

	gp := nodesOfKind(nodes, graph.KindGenericParam)
	hasT := false
	for _, n := range gp {
		if n.Name == "T" {
			hasT = true
		}
	}
	if !hasT {
		t.Errorf("expected KindGenericParam T; got %v", nodeNames(gp))
	}

	memberOf := edgesByKind(edges, graph.EdgeMemberOf)
	hasMember := false
	for _, e := range memberOf {
		if e.From == "x/Util.java::Util.identity#tparam:T" {
			hasMember = true
		}
	}
	if !hasMember {
		t.Errorf("expected KindGenericParam → method EdgeMemberOf")
	}
}

func TestJavaFunctionShape_VariadicParam(t *testing.T) {
	src := `package x;

public class Args {
	public void log(String... messages) {}
}
`
	nodes, _ := runJavaExtract(t, "x/Args.java", src)
	params := nodesOfKind(nodes, graph.KindParam)
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	if v, _ := params[0].Meta["variadic"].(bool); !v {
		t.Errorf("varargs param should be marked variadic")
	}
}

func TestCanonicalizeJavaTypeRef(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"User", "User"},
		{"List<User>", "User"},
		{"Optional<User>", "User"},
		{"Mono<User>", "User"},
		{"User[]", "User"},
		{"com.example.User", "User"},
		{"Map<String, User>", "Map"}, // top-level wrapper not in unwrap list
	}
	for _, c := range cases {
		if got := canonicalizeJavaTypeRef(c.in); got != c.out {
			t.Errorf("canonicalizeJavaTypeRef(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
