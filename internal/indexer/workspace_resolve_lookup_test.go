package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/resolver"
)

func TestCrossWorkspaceLookupRefreshesRulesWithGlobalWorkspacePrecedence(t *testing.T) {
	repoDir := t.TempDir()
	writeWorkspaceLookupTestFile(t, filepath.Join(repoDir, ".gortex.yaml"), `workspace: repo-local
cross_workspace_deps:
  - workspace: shared
    modules: [example.com/shared]
    mode: read-only
`)

	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceLookupTestFile(t, globalPath, fmt.Sprintf(`repos:
  - path: %q
    name: repo-a
    workspace: global-source
`, repoDir))
	configMgr, err := config.NewConfigManager(globalPath)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	configMgr.LoadWorkspaceConfig("repo-a", repoDir)

	mi := &MultiIndexer{
		configMgr: configMgr,
		repos: map[string]*RepoMetadata{
			"repo-a": {RepoPrefix: "repo-a", RootPath: repoDir},
		},
	}
	lookup := mi.crossWorkspaceLookup()
	passLookup := mi.crossWorkspacePassLookup()

	if got := lookup("repo-local"); got != nil {
		t.Fatalf("local workspace must lose to global override, got %#v", got)
	}
	assertWorkspaceLookupRule(t, lookup("global-source"), "shared", "example.com/shared")

	// The long-lived watcher lookup refreshes after a workspace-config
	// revision, while a pass lookup retains its construction-time snapshot.
	writeWorkspaceLookupTestFile(t, filepath.Join(repoDir, ".gortex.yaml"), `workspace: repo-local
cross_workspace_deps:
  - workspace: changed
    modules: [example.com/changed]
    mode: read-only
`)
	configMgr.LoadWorkspaceConfig("repo-a", repoDir)
	assertWorkspaceLookupRule(t, lookup("global-source"), "changed", "example.com/changed")
	assertWorkspaceLookupRule(t, passLookup("global-source"), "shared", "example.com/shared")

	// A global reload also rotates the epoch. Reload clears per-repo config,
	// so reload it exactly as the daemon controller does before consulting
	// the existing watcher lookup again.
	writeWorkspaceLookupTestFile(t, globalPath, fmt.Sprintf(`repos:
  - path: %q
    name: repo-a
    workspace: reloaded-source
`, repoDir))
	if err := configMgr.Reload(); err != nil {
		t.Fatalf("reload global config: %v", err)
	}
	configMgr.LoadWorkspaceConfig("repo-a", repoDir)
	if got := lookup("global-source"); got != nil {
		t.Fatalf("stale global workspace survived reload: %#v", got)
	}
	assertWorkspaceLookupRule(t, lookup("reloaded-source"), "changed", "example.com/changed")
}

func TestCrossWorkspaceLookupDoesNotAllocateAfterConstruction(t *testing.T) {
	repoDir := t.TempDir()
	writeWorkspaceLookupTestFile(t, filepath.Join(repoDir, ".gortex.yaml"), `workspace: source
cross_workspace_deps:
  - workspace: target
    modules: [example.com/one, example.com/two]
    mode: read-only
`)

	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceLookupTestFile(t, globalPath, "repos: []\n")
	configMgr, err := config.NewConfigManager(globalPath)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	configMgr.LoadWorkspaceConfig("repo-a", repoDir)

	mi := &MultiIndexer{
		configMgr: configMgr,
		repos: map[string]*RepoMetadata{
			"repo-a": {RepoPrefix: "repo-a", RootPath: repoDir},
		},
	}
	lookup := mi.crossWorkspaceLookup()

	rules := lookup("source")
	if len(rules) != 1 || len(rules[0].Modules) != 2 {
		t.Fatalf("unexpected lookup: %#v", rules)
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		rules = lookup("source")
	}); allocs != 0 {
		t.Fatalf("lookup allocated after construction: %v allocs/run", allocs)
	}
}

func TestCrossWorkspaceLookupConcurrentRefresh(t *testing.T) {
	repoDir := t.TempDir()
	configPath := filepath.Join(repoDir, ".gortex.yaml")
	writeWorkspaceLookupTestFile(t, configPath, workspaceLookupConfig("target-a", "example.com/a"))
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	writeWorkspaceLookupTestFile(t, globalPath, "repos: []\n")
	configMgr, err := config.NewConfigManager(globalPath)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	configMgr.LoadWorkspaceConfig("repo-a", repoDir)
	mi := &MultiIndexer{
		configMgr: configMgr,
		repos: map[string]*RepoMetadata{
			"repo-a": {RepoPrefix: "repo-a", RootPath: repoDir},
		},
	}
	lookup := mi.crossWorkspaceLookup()

	stop := make(chan struct{})
	errCh := make(chan error, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rules := lookup("source")
				if len(rules) != 1 || len(rules[0].Modules) != 1 {
					errCh <- fmt.Errorf("incomplete snapshot: %#v", rules)
					return
				}
				if rules[0].Workspace != "target-a" && rules[0].Workspace != "target-b" {
					errCh <- fmt.Errorf("unexpected snapshot: %#v", rules)
					return
				}
			}
		}()
	}
	for i := range 20 {
		if i%2 == 0 {
			writeWorkspaceLookupTestFile(t, configPath, workspaceLookupConfig("target-b", "example.com/b"))
		} else {
			writeWorkspaceLookupTestFile(t, configPath, workspaceLookupConfig("target-a", "example.com/a"))
		}
		configMgr.LoadWorkspaceConfig("repo-a", repoDir)
	}
	close(stop)
	readers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func workspaceLookupConfig(target, module string) string {
	return fmt.Sprintf(`workspace: source
cross_workspace_deps:
  - workspace: %s
    modules: [%s]
    mode: read-only
`, target, module)
}

func assertWorkspaceLookupRule(t *testing.T, rules []resolver.CrossWorkspaceDepRule, workspace, module string) {
	t.Helper()
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1: %#v", len(rules), rules)
	}
	if rules[0].Workspace != workspace {
		t.Fatalf("workspace = %q, want %q", rules[0].Workspace, workspace)
	}
	if len(rules[0].Modules) != 1 || rules[0].Modules[0] != module {
		t.Fatalf("modules = %#v, want [%q]", rules[0].Modules, module)
	}
}

func writeWorkspaceLookupTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
