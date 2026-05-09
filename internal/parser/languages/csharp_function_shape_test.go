package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func runCSharpExtract(t *testing.T, path, src string) ([]*graph.Node, []*graph.Edge) {
	t.Helper()
	ext := NewCSharpExtractor()
	result, err := ext.Extract(path, []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return result.Nodes, result.Edges
}

func TestCSharpAsyncSpawns_AwaitInvocation(t *testing.T) {
	src := `using System.Threading.Tasks;

public class Svc {
    public async Task<User> Load(int id) {
        var u = await FetchUser(id);
        var r = await client.Query();
        return u;
    }
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	spawns := edgesByKind(edges, graph.EdgeSpawns)
	want := map[string]bool{"unresolved::FetchUser": false, "unresolved::Query": false}
	for _, e := range spawns {
		if mode, _ := e.Meta["mode"].(string); mode != "async" {
			continue
		}
		if _, ok := want[e.To]; ok {
			want[e.To] = true
		}
	}
	for tgt, found := range want {
		if !found {
			t.Errorf("expected EdgeSpawns mode=async → %s; got %v", tgt, edgeTargets(spawns))
		}
	}
}

func TestCSharpAsyncSpawns_TaskRun(t *testing.T) {
	src := `using System.Threading.Tasks;

public class Bg {
    public void Kick() {
        Task.Run(() => Worker());
        ThreadPool.QueueUserWorkItem(_ => Worker());
    }
}
`
	_, edges := runCSharpExtract(t, "x/Bg.cs", src)
	spawns := edgesByKind(edges, graph.EdgeSpawns)
	hasTaskRun := false
	hasThreadPool := false
	for _, e := range spawns {
		if e.To == "unresolved::Task.Run" {
			hasTaskRun = true
		}
		if e.To == "unresolved::ThreadPool.QueueUserWorkItem" {
			hasThreadPool = true
		}
	}
	if !hasTaskRun {
		t.Errorf("expected EdgeSpawns → Task.Run; got %v", edgeTargets(spawns))
	}
	if !hasThreadPool {
		t.Errorf("expected EdgeSpawns → ThreadPool.QueueUserWorkItem; got %v", edgeTargets(spawns))
	}
}
