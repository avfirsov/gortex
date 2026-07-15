package mcp

import (
	"encoding/json"
	"testing"
)

func TestLocalizationRefinementRequiredActionNamesFacadeReadSelector(t *testing.T) {
	const want = `Call Gortex MCP read(operation:"source", target:{symbol:"<candidate.id>"}); do not call a host file-read tool.`
	completion := newLocalizationRefinementCompletion()
	if got := completion.RequiredAction; got != want {
		t.Fatalf("refinement action = %q, want %q", got, want)
	}

	encoded, err := json.Marshal(completion)
	if err != nil {
		t.Fatalf("marshal completion: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal completion: %v", err)
	}
	if got := payload["required_action"]; got != want {
		t.Fatalf("serialized refinement action = %q, want %q", got, want)
	}
}
