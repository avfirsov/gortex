package codex

import (
	"reflect"
	"testing"
)

func TestMigrateCodexFacadeToolApprovalsPreservesUserIntent(t *testing.T) {
	unknown := map[string]any{"approval_mode": "company-policy", "owner": "user"}
	tools := map[string]any{
		"read": map[string]any{
			"approval_mode": "writes",
			"owner":         "public",
		},
		"get_symbol_source": map[string]any{
			"approval_mode": "auto",
			"legacy_field":  true,
		},
		"private_extension": unknown,
	}
	entry := map[string]any{"tools": tools}

	if !migrateCodexFacadeToolApprovals(entry) {
		t.Fatal("expected recognized legacy approval to migrate")
	}
	if _, exists := tools["get_symbol_source"]; exists {
		t.Fatal("recognized legacy approval was not pruned")
	}
	read := tools["read"].(map[string]any)
	if got := read["approval_mode"]; got != "prompt" {
		t.Fatalf("conflicting modes must become prompt, got %#v", got)
	}
	if got := read["owner"]; got != "public" {
		t.Fatalf("public facade fields must win, got owner %#v", got)
	}
	if got := read["legacy_field"]; got != true {
		t.Fatalf("non-conflicting custom legacy field was lost: %#v", got)
	}
	if !reflect.DeepEqual(tools["private_extension"], unknown) {
		t.Fatalf("unknown tool config changed: %#v", tools["private_extension"])
	}
}

func TestMigrateCodexFacadeToolApprovalsMergesLegacyEntries(t *testing.T) {
	tools := map[string]any{
		"edit_file": map[string]any{
			"approval_mode": "approve",
			"file_policy":   "kept",
		},
		"edit_symbol": map[string]any{
			"approval_mode": "approve",
			"symbol_policy": "kept",
		},
	}
	entry := map[string]any{"tools": tools}

	if !migrateCodexFacadeToolApprovals(entry) {
		t.Fatal("expected legacy approvals to migrate")
	}
	if len(tools) != 1 {
		t.Fatalf("expected one facade approval, got %#v", tools)
	}
	edit, ok := tools["edit"].(map[string]any)
	if !ok {
		t.Fatalf("edit facade approval missing: %#v", tools)
	}
	if edit["approval_mode"] != "approve" || edit["file_policy"] != "kept" || edit["symbol_policy"] != "kept" {
		t.Fatalf("merged approval lost intent: %#v", edit)
	}
}

func TestMigrateCodexFacadeToolApprovalsKeepsUnknownShapes(t *testing.T) {
	tools := map[string]any{
		"get_symbol_source": "future-codex-shape",
		"read":              map[string]any{"approval_mode": "auto"},
	}
	entry := map[string]any{"tools": tools}

	if migrateCodexFacadeToolApprovals(entry) {
		t.Fatal("unsupported config shape must be left untouched")
	}
	if got := tools["get_symbol_source"]; got != "future-codex-shape" {
		t.Fatalf("unsupported legacy shape changed: %#v", got)
	}
}
