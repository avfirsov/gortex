package daemon

import "testing"

// legacyEditingToolNames mirrors the planning-mode editing set that used
// to live in internal/mcp (tools_mode.go::editingToolNames) before the
// consolidation. Duplicated here as a fixture so the parity test fails
// closed if the canonical set ever drops one of them.
var legacyEditingToolNames = []string{
	"edit_file", "edit_symbol", "write_file", "rename_symbol",
}

// cloudMutatingDenied mirrors gortex-cloud internal/proxy.MutatingDenied
// (a separate repo, not buildable from here). Duplicated as a fixture so
// the canonical set can never silently shrink below the cloud denylist.
var cloudMutatingDenied = []string{
	"edit_symbol", "batch_edit", "rename_symbol", "scaffold",
	"index_repository", "track_repository", "untrack_repository",
	"set_active_project", "edit_file", "write_file",
}

// TestMutatingTools_Superset asserts the canonical MutatingTools set is a
// superset of both legacy write-tool lists — the single source of truth
// the planning-mode gate and the federation write-gate both consult.
func TestMutatingTools_Superset(t *testing.T) {
	for _, name := range legacyEditingToolNames {
		if !MutatingTools[name] {
			t.Errorf("MutatingTools is missing legacy editing tool %q", name)
		}
		if !IsMutating(name) {
			t.Errorf("IsMutating(%q) should be true", name)
		}
	}
	for _, name := range cloudMutatingDenied {
		if !MutatingTools[name] {
			t.Errorf("MutatingTools is missing cloud-denied tool %q", name)
		}
	}
}

// TestMutatingTools_ReadToolsExcluded guards against over-broad blocking:
// pure read traversal tools must never be classified as mutating, or the
// planning-mode gate and write-gate would block reads.
func TestMutatingTools_ReadToolsExcluded(t *testing.T) {
	reads := []string{
		"find_usages", "get_callers", "get_call_chain", "find_implementations",
		"get_dependents", "search_symbols", "smart_context", "get_symbol_source",
		"read_file", "graph_stats",
		// Facade-v1 read-only boundaries.
		"capabilities", "change", "overlay_query", "read", "recall", "relations",
		"response", "search", "trace", "workspace",
	}
	for _, name := range reads {
		if got := EffectOf(name); got != EffectNone {
			t.Errorf("read tool %q has effect mask %d, want EffectNone", name, got)
		}
		if IsMutating(name) {
			t.Errorf("read tool %q must not be classified as mutating", name)
		}
	}
}

// TestToolEffects_DurableAudit is the reviewed list of legacy tools with a
// durable or external side effect. Keeping the complete fixture here makes a
// dropped classification fail closed instead of silently reopening planning
// mode or federation to a writer.
func TestToolEffects_DurableAudit(t *testing.T) {
	want := map[string]ToolEffect{
		"apply_code_action":  EffectFilesystemWrite,
		"batch_edit":         EffectFilesystemWrite,
		"delete_scope":       EffectConfigWrite,
		"edit_file":          EffectFilesystemWrite,
		"edit_memory":        EffectFilesystemWrite,
		"edit_symbol":        EffectFilesystemWrite,
		"enrich_churn":       EffectGraphWrite,
		"enrich_releases":    EffectGraphWrite,
		"export_graph":       EffectFilesystemWrite,
		"feedback":           EffectFilesystemWrite,
		"fix_all_in_file":    EffectFilesystemWrite,
		"generate_docs":      EffectFilesystemWrite,
		"generate_skill":     EffectFilesystemWrite,
		"generate_wiki":      EffectFilesystemWrite,
		"index_repository":   EffectGraphWrite,
		"inline_symbol":      EffectFilesystemWrite,
		"move_symbol":        EffectFilesystemWrite,
		"notebook_save":      EffectFilesystemWrite,
		"notebook_used":      EffectFilesystemWrite,
		"overlay_merge":      EffectSessionWrite | EffectFilesystemWrite,
		"post_review":        EffectExternalWrite,
		"reindex_repository": EffectGraphWrite,
		"rename_memory":      EffectFilesystemWrite,
		"rename_symbol":      EffectFilesystemWrite,
		"safe_delete_symbol": EffectFilesystemWrite,
		"save_note":          EffectFilesystemWrite,
		"save_scope":         EffectConfigWrite,
		"scaffold":           EffectFilesystemWrite,
		"set_active_project": EffectConfigWrite | EffectSessionWrite,
		"store_memory":       EffectFilesystemWrite,
		"suppress_finding":   EffectConfigWrite,
		"track_repository":   EffectConfigWrite | EffectGraphWrite,
		"untrack_repository": EffectConfigWrite | EffectGraphWrite,
		"write_file":         EffectFilesystemWrite,
	}

	for name, effect := range want {
		if got := EffectOf(name); got != effect {
			t.Errorf("EffectOf(%q) = %d, want %d", name, got, effect)
		}
		if !IsMutating(name) {
			t.Errorf("IsMutating(%q) = false for durable/external effect %d", name, effect)
		}
		if !MutatingTools[name] {
			t.Errorf("MutatingTools is missing audited writer %q", name)
		}
	}
}

// TestToolEffects_SessionOnlyAudit documents stateful controls which must be
// visible to effect introspection but must not be hidden by planning mode.
// In particular, hiding set_planning_mode/workflow would make recovery from a
// read-only phase impossible.
func TestToolEffects_SessionOnlyAudit(t *testing.T) {
	names := []string{
		"agent_registry",
		"nav",
		"overlay_delete", "overlay_drop", "overlay_drop_branch", "overlay_fork",
		"overlay_keepalive", "overlay_push", "overlay_register", "overlay_switch",
		"proxy_disable", "proxy_enable", "set_planning_mode", "simulate_chain",
		"subscribe_daemon_health", "subscribe_diagnostics", "subscribe_graph_invalidated",
		"subscribe_stale_refs", "subscribe_workspace_readiness",
		"unsubscribe_daemon_health", "unsubscribe_diagnostics", "unsubscribe_graph_invalidated",
		"unsubscribe_stale_refs", "unsubscribe_workspace_readiness", "tools_search", "workflow",
	}

	for _, name := range names {
		if got := EffectOf(name); got != EffectSessionWrite {
			t.Errorf("EffectOf(%q) = %d, want EffectSessionWrite", name, got)
		}
		if !IsEffectful(name) {
			t.Errorf("IsEffectful(%q) = false", name)
		}
		if IsMutating(name) || MutatingTools[name] {
			t.Errorf("session-only tool %q must not enter the durable mutation gate", name)
		}
	}
}

func TestToolEffects_FacadeBoundaries(t *testing.T) {
	want := map[string]ToolEffect{
		"edit":            EffectFilesystemWrite | EffectSessionWrite,
		"refactor":        EffectFilesystemWrite,
		"remember":        EffectFilesystemWrite | EffectConfigWrite,
		"workspace_admin": EffectFilesystemWrite | EffectGraphWrite | EffectConfigWrite | EffectSessionWrite,
		"publish_review":  EffectExternalWrite,
		"overlay":         EffectSessionWrite,
		"session":         EffectSessionWrite,
	}

	for name, effect := range want {
		if got := EffectOf(name); got != effect {
			t.Errorf("EffectOf facade %q = %d, want %d", name, got, effect)
		}
		wantMutating := effect&durableToolEffects != 0
		if got := IsMutating(name); got != wantMutating {
			t.Errorf("IsMutating facade %q = %v, want %v", name, got, wantMutating)
		}
	}
}

func TestMutatingTools_DerivedFromEffects(t *testing.T) {
	for name, effect := range ToolEffects {
		want := effect&durableToolEffects != 0
		if got := MutatingTools[name]; got != want {
			t.Errorf("MutatingTools[%q] = %v for effect %d, want %v", name, got, effect, want)
		}
	}
	for name := range MutatingTools {
		if EffectOf(name)&durableToolEffects == 0 {
			t.Errorf("MutatingTools contains %q without a durable/external effect", name)
		}
	}
}

func TestHasEffect_AllRequestedBits(t *testing.T) {
	if !HasEffect("track_repository", EffectConfigWrite|EffectGraphWrite) {
		t.Fatal("track_repository should carry config and graph effects")
	}
	if HasEffect("track_repository", EffectFilesystemWrite) {
		t.Fatal("track_repository must not carry a filesystem effect")
	}
	if HasEffect("read_file", EffectNone) {
		t.Fatal("EffectNone is not an actionable effect")
	}
}

// TestSortedMutatingTools_Stable asserts the surfaced list is sorted and
// complete.
func TestSortedMutatingTools_Stable(t *testing.T) {
	sorted := SortedMutatingTools()
	if len(sorted) != len(MutatingTools) {
		t.Fatalf("sorted list length %d != set size %d", len(sorted), len(MutatingTools))
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] >= sorted[i] {
			t.Fatalf("not strictly sorted at %d: %q >= %q", i, sorted[i-1], sorted[i])
		}
	}
}

func TestSortedEffectfulTools_Stable(t *testing.T) {
	sorted := SortedEffectfulTools()
	if len(sorted) != len(ToolEffects) {
		t.Fatalf("sorted list length %d != effect registry size %d", len(sorted), len(ToolEffects))
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] >= sorted[i] {
			t.Fatalf("not strictly sorted at %d: %q >= %q", i, sorted[i-1], sorted[i])
		}
	}
}
