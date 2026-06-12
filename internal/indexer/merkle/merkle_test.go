package merkle

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bumpMtime(t *testing.T, abs string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAndDiff_DetectsContentChange(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", "package main\n")
	write(t, root, "pkg/util.go", "package pkg\n")
	files := []string{"main.go", "pkg/util.go"}

	t1 := Build(root, files, nil)
	if len(t1.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(t1.Files))
	}

	write(t, root, "main.go", "package main\n\nfunc main() {}\n")
	bumpMtime(t, filepath.Join(root, "main.go"))

	t2 := Build(root, files, t1)
	changed, removed := t2.Diff(t1)
	if len(removed) != 0 {
		t.Errorf("no removals expected, got %v", removed)
	}
	if len(changed) != 1 || changed[0] != "main.go" {
		t.Errorf("changed = %v, want [main.go]", changed)
	}
}

func TestDiff_MtimeTouchIsNotAChange(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\n")
	files := []string{"a.go"}

	t1 := Build(root, files, nil)
	bumpMtime(t, filepath.Join(root, "a.go")) // touch: new mtime, same content

	t2 := Build(root, files, t1)
	changed, _ := t2.Diff(t1)
	if len(changed) != 0 {
		t.Errorf("a touch with unchanged content must not be a change, got %v", changed)
	}
	if t2.Root != t1.Root {
		t.Error("root hash must be stable across a content-preserving touch")
	}
}

func TestDiff_AddAndRemove(t *testing.T) {
	root := t.TempDir()
	write(t, root, "keep.go", "package main\n")
	write(t, root, "gone.go", "package main\n")
	t1 := Build(root, []string{"keep.go", "gone.go"}, nil)

	write(t, root, "new.go", "package main\n")
	t2 := Build(root, []string{"keep.go", "new.go"}, t1)

	changed, removed := t2.Diff(t1)
	if len(changed) != 1 || changed[0] != "new.go" {
		t.Errorf("changed = %v, want [new.go]", changed)
	}
	if len(removed) != 1 || removed[0] != "gone.go" {
		t.Errorf("removed = %v, want [gone.go]", removed)
	}
}

func TestDiff_NilPriorYieldsEverything(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "x")
	write(t, root, "b.go", "y")
	t1 := Build(root, []string{"a.go", "b.go"}, nil)

	changed, removed := t1.Diff(nil)
	if len(changed) != 2 {
		t.Errorf("nil prior must mark every file changed, got %v", changed)
	}
	if removed != nil {
		t.Errorf("nil prior has no removals, got %v", removed)
	}
}

func TestSubtreeChanged(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x.go", "package a\n")
	write(t, root, "b/y.go", "package b\n")
	files := []string{"a/x.go", "b/y.go"}
	t1 := Build(root, files, nil)

	write(t, root, "a/x.go", "package a\n\nfunc X() {}\n")
	bumpMtime(t, filepath.Join(root, "a", "x.go"))
	t2 := Build(root, files, t1)

	if !t2.SubtreeChanged("a", t1) {
		t.Error("subtree a changed and must report so")
	}
	if t2.SubtreeChanged("b", t1) {
		t.Error("subtree b is untouched and must report unchanged")
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", "package main\n")
	t1 := Build(root, []string{"main.go"}, nil)

	path := filepath.Join(t.TempDir(), "nested", "merkle.json")
	if err := t1.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.Root != t1.Root {
		t.Fatalf("loaded root mismatch: %+v", loaded)
	}
	// A tree must diff clean against its own reload.
	changed, _ := t1.Diff(loaded)
	if len(changed) != 0 {
		t.Errorf("a tree must diff clean against its own reload, got %v", changed)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	loaded, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if loaded != nil {
		t.Error("missing file must yield a nil tree")
	}
}
