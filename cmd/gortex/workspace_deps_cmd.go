package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/zzet/gortex/internal/config"
)

var workspaceDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage cross-workspace dependency declarations in a repo's .gortex.yaml",
	Long: `Inspect and edit the 'cross_workspace_deps' block of a tracked repo.

The workspace boundary is a hard rail: a repo's resolver never sees
nodes from another workspace. 'cross_workspace_deps' is the explicit,
opt-in exception — it lets one workspace's resolver follow an external
import stub into another workspace's graph for symbol resolution.
Contract matching never crosses the boundary; only symbol references do.

These subcommands are the declaration tooling: list/add/mode/remove edit
the block atomically without disturbing other keys, so you never hand-edit
the YAML. Changes land in the repo's .gortex.yaml (the file the indexer
reads); a running daemon needs 'gortex daemon reload' to pick them up.`,
}

var workspaceDepsListCmd = &cobra.Command{
	Use:   "list [repo]",
	Short: "Show cross-workspace dependencies declared by tracked repos",
	Long: `Lists every cross_workspace_deps entry across all tracked repos.

Pass a repo (name, absolute path, or path suffix) to scope the listing
to one repo. The 'from workspace' column is the source workspace the
declaration is keyed to, resolved with the indexer's precedence:
global-config override > .gortex.yaml 'workspace:' > repo name.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWorkspaceDepsList,
}

var workspaceDepsAddMode string

var workspaceDepsAddCmd = &cobra.Command{
	Use:   "add <repo> <target-workspace> <module>...",
	Short: "Declare (or extend) a cross-workspace dependency",
	Long: `Adds a cross_workspace_deps entry to a repo's .gortex.yaml.

<repo> is matched against the global config (name, absolute path, or
path suffix). <target-workspace> is the workspace this repo may follow
import stubs into. One or more <module> arguments are the module paths
(e.g. github.com/gortexhq/gortex) whose stubs become eligible.

If an entry for <target-workspace> already exists, its module set is
extended (deduplicated). --mode sets the access mode; iteration 1
supports only "read-only".`,
	Args: cobra.MinimumNArgs(3),
	RunE: runWorkspaceDepsAdd,
}

var workspaceDepsModeCmd = &cobra.Command{
	Use:   "mode <repo> <target-workspace> <mode>",
	Short: "Set the access mode of an existing cross-workspace dependency",
	Long: `Updates the 'mode' of an existing cross_workspace_deps entry.

The entry for <target-workspace> must already exist (add it first with
'gortex workspace deps add'). Iteration 1 of the engine supports only
"read-only" — any other value (including "read-write") is rejected here
rather than written into a config the daemon would later refuse to load.`,
	Args: cobra.ExactArgs(3),
	RunE: runWorkspaceDepsMode,
}

var workspaceDepsRemoveCmd = &cobra.Command{
	Use:   "remove <repo> <target-workspace> [module]...",
	Short: "Remove a cross-workspace dependency, or specific modules from it",
	Long: `Removes a cross_workspace_deps entry from a repo's .gortex.yaml.

With no <module> arguments the whole entry for <target-workspace> is
dropped. With one or more <module> arguments only those module paths
are removed; an entry left with no modules is dropped entirely.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runWorkspaceDepsRemove,
}

func init() {
	workspaceDepsCmd.AddCommand(workspaceDepsListCmd)
	workspaceDepsCmd.AddCommand(workspaceDepsAddCmd)
	workspaceDepsCmd.AddCommand(workspaceDepsModeCmd)
	workspaceDepsCmd.AddCommand(workspaceDepsRemoveCmd)
	workspaceDepsAddCmd.Flags().StringVar(&workspaceDepsAddMode, "mode", "read-only",
		"access mode for the dependency (iteration 1: read-only only)")
	workspaceCmd.AddCommand(workspaceDepsCmd)
}

// printDaemonReloadNote emits the standard reminder that an already-running
// daemon caches config and needs an explicit reload to see the edit.
func printDaemonReloadNote(cmd *cobra.Command) {
	_, _ = fmt.Fprintln(cmd.OutOrStdout(),
		"\nNote: a running daemon needs `gortex daemon reload` (or restart) to pick up the change.")
}

// normalizeDepMode validates and canonicalises a cross-workspace
// dependency mode. Empty defaults to "read-only" — the only mode the
// engine supports in iteration 1 (see config.Config.validateWorkspaceSchema).
// Any other value is rejected so the CLI never writes a declaration the
// daemon would later refuse to load.
func normalizeDepMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", "read-only":
		return "read-only", nil
	default:
		return "", fmt.Errorf(
			"mode %s is unsupported in iteration 1; only \"read-only\" is allowed",
			strconv.Quote(mode))
	}
}

// readCrossWorkspaceDeps extracts the typed cross_workspace_deps list
// from a parsed .gortex.yaml map. The generic map carries the value as
// []any of map[string]any; a yaml round-trip is the cleanest way to
// coerce it into the typed slice without hand-walking the shape.
func readCrossWorkspaceDeps(payload map[string]any) ([]config.CrossWorkspaceDep, error) {
	raw, ok := payload["cross_workspace_deps"]
	if !ok || raw == nil {
		return nil, nil
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encode cross_workspace_deps: %w", err)
	}
	var deps []config.CrossWorkspaceDep
	if err := yaml.Unmarshal(data, &deps); err != nil {
		return nil, fmt.Errorf("malformed cross_workspace_deps: %w", err)
	}
	return deps, nil
}

// writeCrossWorkspaceDeps stamps the typed list back into a repo's
// .gortex.yaml without disturbing other keys. An empty list deletes the
// key entirely rather than leaving a dangling `cross_workspace_deps: []`.
func writeCrossWorkspaceDeps(repoPath string, deps []config.CrossWorkspaceDep) error {
	payload, err := readRepoYAML(repoPath)
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		delete(payload, "cross_workspace_deps")
	} else {
		payload["cross_workspace_deps"] = deps
	}
	return writeRepoYAML(repoPath, payload)
}

// effectiveWorkspace resolves the workspace slug a repo's
// cross_workspace_deps are keyed to, mirroring the indexer's precedence:
// global-config override > .gortex.yaml `workspace:` > the repo label.
func effectiveWorkspace(r config.RepoEntry, payload map[string]any) string {
	if r.Workspace != "" {
		return r.Workspace
	}
	if ws, _ := payload["workspace"].(string); ws != "" {
		return ws
	}
	return repoLabel(r)
}

// mergeModules appends `add` onto `existing`, trimming blanks and
// dropping duplicates while preserving first-seen order.
func mergeModules(existing, add []string) []string {
	seen := make(map[string]bool, len(existing)+len(add))
	out := make([]string, 0, len(existing)+len(add))
	for _, group := range [][]string{existing, add} {
		for _, m := range group {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// upsertCrossWorkspaceDep merges a declaration into the list. When an
// entry for targetWS already exists its module set is extended and —
// if modeExplicit is true or its mode is unset — its mode is updated.
// Otherwise a fresh entry is appended. The bool reports whether an
// existing entry was extended (vs. a new one created).
func upsertCrossWorkspaceDep(deps []config.CrossWorkspaceDep, targetWS string, modules []string, mode string, modeExplicit bool) ([]config.CrossWorkspaceDep, bool) {
	for i := range deps {
		if deps[i].Workspace != targetWS {
			continue
		}
		deps[i].Modules = mergeModules(deps[i].Modules, modules)
		if modeExplicit || deps[i].Mode == "" {
			deps[i].Mode = mode
		}
		return deps, true
	}
	return append(deps, config.CrossWorkspaceDep{
		Workspace: targetWS,
		Modules:   mergeModules(nil, modules),
		Mode:      mode,
	}), false
}

// setCrossWorkspaceDepMode updates the mode of an existing entry.
// Returns an error when no entry for targetWS is declared.
func setCrossWorkspaceDepMode(deps []config.CrossWorkspaceDep, targetWS, mode string) ([]config.CrossWorkspaceDep, error) {
	for i := range deps {
		if deps[i].Workspace == targetWS {
			deps[i].Mode = mode
			return deps, nil
		}
	}
	return nil, fmt.Errorf(
		"no cross-workspace dependency into %q is declared; add it first with `gortex workspace deps add`",
		targetWS)
}

// removeCrossWorkspaceDep drops modules from the targetWS entry. With no
// modules it removes the whole entry. An entry left with zero modules is
// dropped entirely. Returns an error when no entry for targetWS exists,
// or when a named module isn't present on it.
func removeCrossWorkspaceDep(deps []config.CrossWorkspaceDep, targetWS string, modules []string) ([]config.CrossWorkspaceDep, error) {
	idx := -1
	for i := range deps {
		if deps[i].Workspace == targetWS {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("no cross-workspace dependency into %q is declared", targetWS)
	}
	if len(modules) == 0 {
		return append(deps[:idx:idx], deps[idx+1:]...), nil
	}

	drop := make(map[string]bool, len(modules))
	for _, m := range modules {
		drop[strings.TrimSpace(m)] = true
	}
	present := make(map[string]bool, len(deps[idx].Modules))
	remaining := make([]string, 0, len(deps[idx].Modules))
	for _, m := range deps[idx].Modules {
		present[m] = true
		if !drop[m] {
			remaining = append(remaining, m)
		}
	}
	var notFound []string
	for m := range drop {
		if !present[m] {
			notFound = append(notFound, m)
		}
	}
	if len(notFound) > 0 {
		sort.Strings(notFound)
		return nil, fmt.Errorf("module(s) not declared on %q: %s", targetWS, strings.Join(notFound, ", "))
	}
	if len(remaining) == 0 {
		return append(deps[:idx:idx], deps[idx+1:]...), nil
	}
	deps[idx].Modules = remaining
	return deps, nil
}

// matchRepoEntry resolves a repo argument against the global config and
// returns the matched RepoEntry. Shared by every deps subcommand.
func matchRepoEntry(target string) (config.RepoEntry, error) {
	repos, err := loadGlobalRepos()
	if err != nil {
		return config.RepoEntry{}, err
	}
	idx, err := matchRepo(repos, target)
	if err != nil {
		return config.RepoEntry{}, err
	}
	return repos[idx], nil
}

func runWorkspaceDepsList(cmd *cobra.Command, args []string) error {
	repos, err := loadGlobalRepos()
	if err != nil {
		return err
	}
	scoped := len(args) == 1
	if scoped {
		idx, err := matchRepo(repos, args[0])
		if err != nil {
			return err
		}
		repos = repos[idx : idx+1]
	}
	if len(repos) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(no tracked repos)")
		return nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(cmd.OutOrStdout())
	t.SetStyle(table.StyleLight)
	t.AppendHeader(table.Row{"repo", "from workspace", "→ workspace", "modules", "mode"})

	rows := 0
	for _, r := range repos {
		payload, err := readRepoYAML(r.Path)
		if err != nil {
			t.AppendRow(table.Row{repoLabel(r), "(error)", err.Error(), "", ""})
			rows++
			continue
		}
		deps, err := readCrossWorkspaceDeps(payload)
		if err != nil {
			t.AppendRow(table.Row{repoLabel(r), "(error)", err.Error(), "", ""})
			rows++
			continue
		}
		if len(deps) == 0 {
			continue
		}
		from := effectiveWorkspace(r, payload)
		for _, d := range deps {
			mode := d.Mode
			if mode == "" {
				mode = "read-only (default)"
			}
			t.AppendRow(table.Row{repoLabel(r), from, d.Workspace, strings.Join(d.Modules, "\n"), mode})
			rows++
		}
	}
	if rows == 0 {
		if scoped {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(no cross-workspace dependencies declared)")
		} else {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(no tracked repo declares cross-workspace dependencies)")
		}
		return nil
	}
	t.Render()
	return nil
}

func runWorkspaceDepsAdd(cmd *cobra.Command, args []string) error {
	r, err := matchRepoEntry(args[0])
	if err != nil {
		return err
	}
	targetWS := args[1]
	modules := mergeModules(nil, args[2:])
	if len(modules) == 0 {
		return fmt.Errorf("at least one non-empty module path is required")
	}
	mode, err := normalizeDepMode(workspaceDepsAddMode)
	if err != nil {
		return err
	}

	payload, err := readRepoYAML(r.Path)
	if err != nil {
		return err
	}
	from := effectiveWorkspace(r, payload)
	if from == targetWS {
		return fmt.Errorf(
			"repo %s already belongs to workspace %q — a cross-workspace dependency into the same workspace is a no-op",
			repoLabel(r), targetWS)
	}
	deps, err := readCrossWorkspaceDeps(payload)
	if err != nil {
		return err
	}
	deps, extended := upsertCrossWorkspaceDep(deps, targetWS, modules, mode, cmd.Flags().Changed("mode"))
	if err := writeCrossWorkspaceDeps(r.Path, deps); err != nil {
		return err
	}

	var result config.CrossWorkspaceDep
	for _, d := range deps {
		if d.Workspace == targetWS {
			result = d
			break
		}
	}
	verb := "declared"
	if extended {
		verb = "extended"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"%s cross-workspace dependency in %s/.gortex.yaml: %s → %s [%s]\n  modules: %s\n",
		verb, r.Path, from, targetWS, result.Mode, strings.Join(result.Modules, ", "))
	printDaemonReloadNote(cmd)
	return nil
}

func runWorkspaceDepsMode(cmd *cobra.Command, args []string) error {
	r, err := matchRepoEntry(args[0])
	if err != nil {
		return err
	}
	targetWS := args[1]
	mode, err := normalizeDepMode(args[2])
	if err != nil {
		return err
	}

	payload, err := readRepoYAML(r.Path)
	if err != nil {
		return err
	}
	deps, err := readCrossWorkspaceDeps(payload)
	if err != nil {
		return err
	}
	deps, err = setCrossWorkspaceDepMode(deps, targetWS, mode)
	if err != nil {
		return err
	}
	if err := writeCrossWorkspaceDeps(r.Path, deps); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"updated %s/.gortex.yaml: cross-workspace dependency → %s mode=%s\n",
		r.Path, targetWS, mode)
	printDaemonReloadNote(cmd)
	return nil
}

func runWorkspaceDepsRemove(cmd *cobra.Command, args []string) error {
	r, err := matchRepoEntry(args[0])
	if err != nil {
		return err
	}
	targetWS := args[1]
	modules := args[2:]

	payload, err := readRepoYAML(r.Path)
	if err != nil {
		return err
	}
	deps, err := readCrossWorkspaceDeps(payload)
	if err != nil {
		return err
	}
	deps, err = removeCrossWorkspaceDep(deps, targetWS, modules)
	if err != nil {
		return err
	}
	if err := writeCrossWorkspaceDeps(r.Path, deps); err != nil {
		return err
	}
	if len(modules) == 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"removed cross-workspace dependency from %s/.gortex.yaml: → %s\n", r.Path, targetWS)
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"removed module(s) from %s/.gortex.yaml cross-workspace dependency → %s: %s\n",
			r.Path, targetWS, strings.Join(modules, ", "))
	}
	printDaemonReloadNote(cmd)
	return nil
}
