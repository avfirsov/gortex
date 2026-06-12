package cochange

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// gitRepo builds a throwaway git repository in a temp dir and returns
// its path. Skips the test when git is unavailable.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Tester")
	return dir
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// commit writes files (path->content) and commits them in dir.
func commit(t *testing.T, dir, msg string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "git", "add", rel)
	}
	run(t, dir, "git", "commit", "-q", "-m", msg)
}

func TestMine_DetectsCoChangingFiles(t *testing.T) {
	dir := gitRepo(t)
	// a.go and b.go always move together; c.go moves alone.
	commit(t, dir, "c1", map[string]string{"a.go": "1", "b.go": "1"})
	commit(t, dir, "c2", map[string]string{"a.go": "2", "b.go": "2"})
	commit(t, dir, "c3", map[string]string{"a.go": "3", "b.go": "3"})
	commit(t, dir, "c4", map[string]string{"c.go": "1"})

	res := Mine(context.Background(), dir, Options{})
	if res.CommitsScanned != 4 {
		t.Errorf("CommitsScanned = %d, want 4", res.CommitsScanned)
	}
	if len(res.Pairs) != 1 {
		t.Fatalf("expected exactly one pair, got %d: %+v", len(res.Pairs), res.Pairs)
	}
	p := res.Pairs[0]
	if p.FileA != "a.go" || p.FileB != "b.go" {
		t.Errorf("pair = %s/%s, want a.go/b.go", p.FileA, p.FileB)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3", p.Count)
	}
	// a.go and b.go each changed in exactly 3 commits and always
	// together -> cosine score is 1.0.
	if p.Score < 0.999 {
		t.Errorf("Score = %f, want ~1.0", p.Score)
	}
}

func TestMine_MinCountFiltersWeakPairs(t *testing.T) {
	dir := gitRepo(t)
	// a.go + b.go share only one commit -> below the default MinCount.
	commit(t, dir, "c1", map[string]string{"a.go": "1", "b.go": "1"})
	commit(t, dir, "c2", map[string]string{"a.go": "2"})

	res := Mine(context.Background(), dir, Options{})
	if len(res.Pairs) != 0 {
		t.Errorf("expected no pairs below MinCount, got %+v", res.Pairs)
	}

	// MinCount=1 surfaces the single-commit pair.
	res = Mine(context.Background(), dir, Options{MinCount: 1})
	if len(res.Pairs) != 1 {
		t.Errorf("expected one pair with MinCount=1, got %d", len(res.Pairs))
	}
}

func TestMine_SkipsSweepingCommits(t *testing.T) {
	dir := gitRepo(t)
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	sweep := map[string]string{}
	for _, f := range files {
		sweep[f] = "v1"
	}
	commit(t, dir, "sweep", sweep)
	for _, f := range files {
		sweep[f] = "v2"
	}
	commit(t, dir, "sweep2", sweep)

	// MaxFilesPerCommit=3 drops both five-file commits from pair
	// counting, leaving no co-change evidence.
	res := Mine(context.Background(), dir, Options{MaxFilesPerCommit: 3, MinCount: 1})
	if len(res.Pairs) != 0 {
		t.Errorf("sweeping commits should not produce pairs, got %+v", res.Pairs)
	}
}

func TestMine_NotAGitRepo(t *testing.T) {
	res := Mine(context.Background(), t.TempDir(), Options{})
	if len(res.Pairs) != 0 || res.CommitsScanned != 0 {
		t.Errorf("non-git dir should yield empty result, got %+v", res)
	}
}

func TestEnrichGraph_AddsSymmetricEdges(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "c1", map[string]string{"a.go": "1", "b.go": "1"})
	commit(t, dir, "c2", map[string]string{"a.go": "2", "b.go": "2"})

	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "a.go"})
	g.AddNode(&graph.Node{ID: "b.go", Kind: graph.KindFile, Name: "b.go", FilePath: "b.go"})

	added, err := EnrichGraph(g, dir, "")
	if err != nil {
		t.Fatalf("EnrichGraph: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2 (one pair, both directions)", added)
	}

	out := g.GetOutEdges("a.go")
	var found *graph.Edge
	for _, e := range out {
		if e.Kind == graph.EdgeCoChange {
			found = e
		}
	}
	if found == nil {
		t.Fatal("no EdgeCoChange out-edge from a.go")
	}
	if found.To != "b.go" {
		t.Errorf("edge target = %s, want b.go", found.To)
	}
	if found.Meta["count"].(int) != 2 {
		t.Errorf("Meta count = %v, want 2", found.Meta["count"])
	}
	if found.Confidence <= 0 {
		t.Errorf("Confidence = %f, want > 0", found.Confidence)
	}
}

func TestEnrichGraph_RepoPrefixScoping(t *testing.T) {
	dir := gitRepo(t)
	commit(t, dir, "c1", map[string]string{"a.go": "1", "b.go": "1"})
	commit(t, dir, "c2", map[string]string{"a.go": "2", "b.go": "2"})

	g := graph.New()
	// File nodes carry a multi-repo prefix; the git-relative paths
	// must still match via prefix stripping.
	g.AddNode(&graph.Node{ID: "myrepo/a.go", Kind: graph.KindFile, Name: "a.go", FilePath: "myrepo/a.go", RepoPrefix: "myrepo"})
	g.AddNode(&graph.Node{ID: "myrepo/b.go", Kind: graph.KindFile, Name: "b.go", FilePath: "myrepo/b.go", RepoPrefix: "myrepo"})

	added, err := EnrichGraph(g, dir, "myrepo")
	if err != nil {
		t.Fatalf("EnrichGraph: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if len(g.GetOutEdges("myrepo/a.go")) == 0 {
		t.Error("expected a co-change edge on the prefixed file node")
	}
}

func TestEnrichGraph_NotAGitRepo(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.go", Kind: graph.KindFile, FilePath: "a.go"})
	added, err := EnrichGraph(g, t.TempDir(), "")
	if err != nil {
		t.Fatalf("EnrichGraph: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0 for non-git dir", added)
	}
}
