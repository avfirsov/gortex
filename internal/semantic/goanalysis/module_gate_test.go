package goanalysis

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

func writeProbeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("module probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoModulePresent(t *testing.T) {
	t.Run("root go.mod", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "go.mod"))
		if !goModulePresent(root) {
			t.Fatal("root go.mod not detected")
		}
	})
	t.Run("root go.work", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "go.work"))
		if !goModulePresent(root) {
			t.Fatal("root go.work not detected")
		}
	})
	t.Run("depth-two module", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "services", "api", "go.mod"))
		if !goModulePresent(root) {
			t.Fatal("depth-2 go.mod not detected")
		}
	})
	t.Run("depth-three module is out of reach", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "a", "b", "c", "go.mod"))
		if goModulePresent(root) {
			t.Fatal("depth-3 go.mod must not admit the pass")
		}
	})
	t.Run("non-Go repository", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "Cargo.toml"))
		writeProbeFile(t, filepath.Join(root, "src", "main.rs"))
		if goModulePresent(root) {
			t.Fatal("Cargo repo must not be admitted")
		}
	})
	t.Run("vendored and fixture manifests do not vouch", func(t *testing.T) {
		root := t.TempDir()
		writeProbeFile(t, filepath.Join(root, "vendor", "go.mod"))
		writeProbeFile(t, filepath.Join(root, "node_modules", "pkg", "go.mod"))
		writeProbeFile(t, filepath.Join(root, "testdata", "go.mod"))
		if goModulePresent(root) {
			t.Fatal("vendored/fixture go.mod must not admit the pass")
		}
	})
}

func TestGoTypesProbeTimeoutEnv(t *testing.T) {
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "")
	if got := goTypesProbeTimeout(); got != defaultGoTypesProbeTimeout {
		t.Fatalf("default = %v, want %v", got, defaultGoTypesProbeTimeout)
	}
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "off")
	if got := goTypesProbeTimeout(); got != 0 {
		t.Fatalf("off = %v, want 0", got)
	}
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "45s")
	if got := goTypesProbeTimeout(); got != 45*time.Second {
		t.Fatalf("override = %v, want 45s", got)
	}
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "garbage")
	if got := goTypesProbeTimeout(); got != defaultGoTypesProbeTimeout {
		t.Fatalf("malformed = %v, want default", got)
	}
}

func TestProbeGoPackagesLoadableRealModuleLoads(t *testing.T) {
	root := t.TempDir()
	writeProbeFile(t, filepath.Join(root, "go.mod"))
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/probe\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provider{}
	loadable, real, errored := p.probeGoPackagesLoadable(context.Background(), root)
	if !loadable || real < 1 {
		t.Fatalf("real module not loadable: loadable=%v real=%d errored=%d", loadable, real, errored)
	}
}

func TestProbeGoPackagesLoadableSubdirModuleRootFailsClosed(t *testing.T) {
	// go.mod lives under backend/, so `go list ./...` from the ROOT is
	// out-of-module and must fail closed (the growth/vioportals pathology).
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	if err := os.MkdirAll(backend, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "go.mod"),
		[]byte("module example.com/backend\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provider{}
	if loadable, real, _ := p.probeGoPackagesLoadable(context.Background(), root); loadable || real != 0 {
		t.Fatalf("out-of-module root must fail closed: loadable=%v real=%d", loadable, real)
	}
}

func TestProbeGoPackagesLoadableDisabledFailsOpen(t *testing.T) {
	// A disabled probe (timeout off) never skips, even on a non-module dir.
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "off")
	p := &Provider{}
	if loadable, _, _ := p.probeGoPackagesLoadable(context.Background(), t.TempDir()); !loadable {
		t.Fatal("disabled probe must fail open")
	}
}

func TestProbeGoPackagesLoadableHonorsInjectedLoader(t *testing.T) {
	t.Setenv("GORTEX_GOTYPES_PROBE_TIMEOUT", "30s")
	// Injected loader returning a real package -> loadable.
	ok := &Provider{packagesLoad: func(*packages.Config, ...string) ([]*packages.Package, error) {
		return []*packages.Package{{PkgPath: "example.com/x"}}, nil
	}}
	if loadable, real, _ := ok.probeGoPackagesLoadable(context.Background(), t.TempDir()); !loadable || real != 1 {
		t.Fatalf("valid injected package must be loadable: loadable=%v real=%d", loadable, real)
	}
	// Injected loader returning only errored packages -> not loadable (skip).
	bad := &Provider{packagesLoad: func(*packages.Config, ...string) ([]*packages.Package, error) {
		return []*packages.Package{{PkgPath: "", Errors: []packages.Error{{Msg: "boom"}}}}, nil
	}}
	if loadable, real, errored := bad.probeGoPackagesLoadable(context.Background(), t.TempDir()); loadable || real != 0 || errored != 1 {
		t.Fatalf("all-errored load must not be loadable: loadable=%v real=%d errored=%d", loadable, real, errored)
	}
}
