package blame

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

func TestParse_PorcelainBasic(t *testing.T) {
	// Synthetic porcelain: line 1 from Alice, lines 2-3 reuse the
	// same commit (cached header), line 4 from Bob (new commit
	// with full header). Tab-prefixed source lines are required —
	// porcelain emits `\t` even for blank source.
	out := []byte("1234567890abcdef1234567890abcdef12345678 1 1 3\n" +
		"author Alice\n" +
		"author-mail <alice@example.com>\n" +
		"author-time 1700000000\n" +
		"author-tz +0000\n" +
		"committer Alice\n" +
		"committer-mail <alice@example.com>\n" +
		"committer-time 1700000000\n" +
		"committer-tz +0000\n" +
		"summary first\n" +
		"filename foo.go\n" +
		"\tpackage main\n" +
		"1234567890abcdef1234567890abcdef12345678 2 2\n" +
		"\t\n" +
		"1234567890abcdef1234567890abcdef12345678 3 3\n" +
		"\tfunc Hello() {}\n" +
		"abcdef0123456789abcdef0123456789abcdef01 4 4 1\n" +
		"author Bob\n" +
		"author-mail <bob@example.com>\n" +
		"author-time 1710000000\n" +
		"author-tz +0000\n" +
		"committer Bob\n" +
		"committer-mail <bob@example.com>\n" +
		"committer-time 1710000000\n" +
		"committer-tz +0000\n" +
		"summary edit\n" +
		"filename foo.go\n" +
		"\t// later edit\n")
	got, err := Parse(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 lines, got %d: %+v", len(got), got)
	}
	if got[1].Email != "alice@example.com" {
		t.Errorf("line 1 email = %q", got[1].Email)
	}
	if got[1].Timestamp.Unix() != 1700000000 {
		t.Errorf("line 1 timestamp = %v", got[1].Timestamp)
	}
	if got[4].Email != "bob@example.com" {
		t.Errorf("line 4 email = %q", got[4].Email)
	}
	if got[2].Email != "alice@example.com" {
		t.Errorf("line 2 email = %q (should reuse cached header)", got[2].Email)
	}
	if got[3].Email != "alice@example.com" {
		t.Errorf("line 3 email = %q", got[3].Email)
	}
}

func TestParse_EmptyInput(t *testing.T) {
	got, err := Parse(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d lines", len(got))
	}
}

func TestPickLatest(t *testing.T) {
	older := Author{Commit: "a", Email: "alice@x", Timestamp: time.Unix(1000, 0)}
	newer := Author{Commit: "b", Email: "bob@x", Timestamp: time.Unix(2000, 0)}
	lines := map[int]Author{
		1: older,
		2: newer,
		3: older,
	}
	got := pickLatest(lines, 1, 3)
	if got == nil {
		t.Fatal("expected a result")
	}
	if got.Commit != "b" {
		t.Errorf("expected newest (Bob/b), got %+v", got)
	}
}

func TestPickLatest_NoCoverage(t *testing.T) {
	lines := map[int]Author{1: {}, 2: {}}
	got := pickLatest(lines, 10, 20)
	if got != nil {
		t.Errorf("expected nil for uncovered range, got %+v", got)
	}
}

func TestPickLatest_StartGreaterThanEnd(t *testing.T) {
	// Some node kinds emit StartLine == EndLine; ensure the
	// degenerate range still resolves a single-line author.
	a := Author{Commit: "x", Timestamp: time.Unix(1000, 0)}
	got := pickLatest(map[int]Author{5: a}, 5, 0) // EndLine == 0 → treated as StartLine
	if got == nil || got.Commit != "x" {
		t.Errorf("expected author at line 5, got %+v", got)
	}
}

func TestEnrichGraph_StampsLastAuthored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := t.TempDir()
	if err := runCmd(t, repoDir, "git", "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(t, repoDir, "git", "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(t, repoDir, "git", "config", "user.name", "Tester"); err != nil {
		t.Fatal(err)
	}
	source := "package main\n\nfunc Hello() {}\n"
	if err := writeFile(filepath.Join(repoDir, "main.go"), source); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(t, repoDir, "git", "add", "main.go"); err != nil {
		t.Fatal(err)
	}
	if err := runCmd(t, repoDir, "git", "commit", "-q", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	g := graph.New()
	g.AddNode(&graph.Node{
		ID:        "main.go::Hello",
		Kind:      graph.KindFunction,
		Name:      "Hello",
		FilePath:  "main.go",
		StartLine: 3,
		EndLine:   3,
	})

	count, err := EnrichGraph(g, repoDir)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 enriched node, got %d", count)
	}

	n := g.GetNode("main.go::Hello")
	la, ok := n.Meta["last_authored"].(map[string]any)
	if !ok {
		t.Fatalf("last_authored missing or wrong shape: %+v", n.Meta)
	}
	if la["email"] != "test@example.com" {
		t.Errorf("email = %v", la["email"])
	}
	if _, ok := la["commit"].(string); !ok {
		t.Errorf("commit not a string: %v", la["commit"])
	}
	if _, ok := la["timestamp"].(int64); !ok {
		t.Errorf("timestamp not int64: %T %v", la["timestamp"], la["timestamp"])
	}
}

func TestShouldEnrichBlame(t *testing.T) {
	cases := []struct {
		kind graph.NodeKind
		want bool
	}{
		{graph.KindFunction, true},
		{graph.KindMethod, true},
		{graph.KindType, true},
		{graph.KindConstant, true},
		{graph.KindFile, false},
		{graph.KindImport, false},
		{graph.KindTodo, false},
		{graph.KindLicense, false},
		{graph.KindTeam, false},
		{graph.KindFlag, false},
	}
	for _, c := range cases {
		if got := shouldEnrichBlame(c.kind); got != c.want {
			t.Errorf("shouldEnrichBlame(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// --- helpers ---

func runCmd(t *testing.T, dir, name string, args ...string) error {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &cmdError{name: name, args: args, out: string(out), err: err}
	}
	return nil
}

type cmdError struct {
	name string
	args []string
	out  string
	err  error
}

func (e *cmdError) Error() string {
	return e.name + " " + strings.Join(e.args, " ") + ": " + e.err.Error() + "\n" + e.out
}

func writeFile(path, content string) error {
	cmd := exec.Command("sh", "-c", "cat > "+path)
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}
