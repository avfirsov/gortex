package mcp

import (
	"encoding/json"
	"testing"
)

func TestLocalizationRefinementRequiredActionNamesFacadeReadSelector(t *testing.T) {
	const symbol = "repo/pkg/file.go::Resolver.Run"
	const want = `Call Gortex MCP read(operation:"source", target:{symbol:"repo/pkg/file.go::Resolver.Run"}); do not call a host file-read tool.`
	completion := newLocalizationRefinementCompletion(symbol)
	if got := completion.RequiredAction; got != want {
		t.Fatalf("refinement action = %q, want %q", got, want)
	}
	if completion.refinementSymbol != symbol {
		t.Fatalf("refinement symbol = %q, want %q", completion.refinementSymbol, symbol)
	}
	if completion.ExactSymbol != "" {
		t.Fatalf("uncertain refinement falsely advertised exact symbol %q", completion.ExactSymbol)
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
	if _, exists := payload["exact_symbol"]; exists {
		t.Fatalf("uncertain refinement serialized exact_symbol: %#v", payload)
	}
}
