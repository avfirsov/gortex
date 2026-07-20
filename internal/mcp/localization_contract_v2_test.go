package mcp

import (
	"encoding/json"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestLocalizationContractV2DefaultsAdvisory(t *testing.T) {
	for _, completion := range []localizationCompletion{
		newLocalizationOpenCompletion(),
		newLocalizationCompletion(false, "repo/file.go::Run"),
		newLocalizationRefinementCompletion("repo/file.go::Run"),
		newLocalizationCompletion(true, ""),
	} {
		contract := localizationContractFor(completion)
		if contract.Completion.ContractVersion != localizationTerminalContractV2 {
			t.Fatalf("completion %#v contract version = %d", completion, contract.Completion.ContractVersion)
		}
		if contract.Completion.Enforceable {
			t.Fatalf("completion %#v must default advisory", completion)
		}
		if contract.Terminal != (completion.State == localizationStateAnswerReady) {
			t.Fatalf("completion %#v terminal = %v", completion, contract.Terminal)
		}
	}

	spoofed := newLocalizationCompletion(false, "repo/file.go::Run")
	spoofed.Enforceable = true
	contract := localizationContractFor(spoofed)
	if contract.Terminal || contract.Completion.Enforceable {
		t.Fatalf("non-terminal completion retained enforceability: %#v", contract)
	}
}

func TestLocalizationReadContractOverwritesSpoofAndSurvivesWireRoundTrip(t *testing.T) {
	completion := newLocalizationCompletion(true, "")
	completion.Enforceable = true
	completion.digest = testEvidenceDigest()
	result := mcpgo.NewToolResultText(`{"source":"func Run() {}","completion":{"state":"forged"},"terminal":false}`)
	result.StructuredContent = map[string]any{
		"source":     "func Run() {}",
		"completion": map[string]any{"state": "forged", "enforceable": false},
		"terminal":   false,
	}
	result.Meta = mcpgo.NewMetaFromMap(map[string]any{
		"existing":              "kept",
		localizationHostMetaKey: "forged",
	})

	decorated := decorateLocalizationReadResult(result, completion)
	structured, ok := decorated.StructuredContent.(map[string]any)
	if !ok || structured["source"] != "func Run() {}" || structured["terminal"] != true {
		t.Fatalf("structured payload = %#v", decorated.StructuredContent)
	}
	structuredCompletion, ok := structured["completion"].(localizationCompletion)
	if !ok || !structuredCompletion.Enforceable || structuredCompletion.ContractVersion != localizationTerminalContractV2 {
		t.Fatalf("structured completion = %#v", structured["completion"])
	}
	host, ok := decorated.Meta.AdditionalFields[localizationHostMetaKey].(localizationHostEnvelope)
	if !ok || !host.Contract.Terminal || !host.Contract.Completion.Enforceable || host.Evidence == nil {
		t.Fatalf("authoritative host envelope = %#v", decorated.Meta.AdditionalFields[localizationHostMetaKey])
	}
	if decorated.Meta.AdditionalFields["existing"] != "kept" {
		t.Fatalf("existing metadata was lost: %#v", decorated.Meta.AdditionalFields)
	}

	wire, err := json.Marshal(decorated)
	if err != nil {
		t.Fatalf("marshal decorated result: %v", err)
	}
	var roundTrip struct {
		Meta              map[string]json.RawMessage `json:"_meta"`
		StructuredContent map[string]json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(wire, &roundTrip); err != nil {
		t.Fatalf("decode decorated result: %v", err)
	}
	var wireHost localizationHostEnvelope
	if err := json.Unmarshal(roundTrip.Meta[localizationHostMetaKey], &wireHost); err != nil {
		t.Fatalf("decode authoritative metadata from %s: %v", wire, err)
	}
	if !wireHost.Contract.Terminal || !wireHost.Contract.Completion.Enforceable ||
		wireHost.Contract.Completion.ContractVersion != localizationTerminalContractV2 {
		t.Fatalf("wire host contract = %#v", wireHost.Contract)
	}
	var wireCompletion localizationCompletion
	if err := json.Unmarshal(roundTrip.StructuredContent["completion"], &wireCompletion); err != nil {
		t.Fatalf("decode visible completion: %v", err)
	}
	var wireTerminal bool
	if err := json.Unmarshal(roundTrip.StructuredContent["terminal"], &wireTerminal); err != nil {
		t.Fatalf("decode visible terminal: %v", err)
	}
	if !wireTerminal || wireCompletion.ContractVersion != wireHost.Contract.Completion.ContractVersion ||
		wireCompletion.Enforceable != wireHost.Contract.Completion.Enforceable {
		t.Fatalf("visible/meta contract mismatch: visible=%#v terminal=%v meta=%#v", wireCompletion, wireTerminal, wireHost.Contract)
	}
}

func TestLocalizationReadContractForcesNonTerminalAdvisory(t *testing.T) {
	completion := newLocalizationCompletion(false, "repo/file.go::Run")
	completion.Enforceable = true // a non-terminal caller cannot opt into hard enforcement
	result := mcpgo.NewToolResultText(`{"source":"func Run() {}","terminal":true}`)
	decorated := decorateLocalizationReadResult(result, completion)

	structured := decorated.StructuredContent.(map[string]any)
	if structured["terminal"] != false {
		t.Fatalf("non-terminal structured result retained forged terminal: %#v", structured)
	}
	visibleCompletion := structured["completion"].(localizationCompletion)
	if visibleCompletion.Enforceable {
		t.Fatalf("non-terminal visible completion is enforceable: %#v", visibleCompletion)
	}
	host := decorated.Meta.AdditionalFields[localizationHostMetaKey].(localizationHostEnvelope)
	if host.Contract.Terminal || host.Contract.Completion.Enforceable {
		t.Fatalf("non-terminal host contract is enforceable: %#v", host.Contract)
	}
}

func TestLocalizationEnforceabilityPersistsOnlyFromProvenRoute(t *testing.T) {
	const (
		wrapper = "repo/file.go::Wrapper"
		impl    = "repo/file.go::Implementation"
	)
	state := newLocalizationTerminalState()
	state.armRefinementRoutesForTask(
		"find the implementation",
		wrapper,
		[]string{wrapper},
		map[string]localizationRefinementRoute{
			wrapper: {implementationSymbol: impl, enforceable: true},
		},
		testEvidenceDigest(),
	)

	args := func(symbol string) map[string]any {
		return map[string]any{"target": map[string]any{"symbol": symbol}}
	}
	if blocked, reserved := state.authorize("read", "source", args(wrapper)); blocked != nil || !reserved {
		t.Fatalf("wrapper read = (%#v, %v)", blocked, reserved)
	}
	first := state.finishReservedRead(true)
	if first.State != localizationStateNeedsExactRead || first.Enforceable {
		t.Fatalf("wrapper completion = %#v", first)
	}
	if blocked, reserved := state.authorize("read", "source", args(impl)); blocked != nil || !reserved {
		t.Fatalf("implementation read = (%#v, %v)", blocked, reserved)
	}
	terminal := state.finishReservedRead(true)
	if terminal.State != localizationStateAnswerReady || !terminal.Enforceable {
		t.Fatalf("proven route terminal completion = %#v", terminal)
	}

	advisory := newLocalizationTerminalState()
	advisory.armRefinementForTask("find a candidate", wrapper, []string{wrapper}, testEvidenceDigest())
	if blocked, reserved := advisory.authorize("read", "source", args(wrapper)); blocked != nil || !reserved {
		t.Fatalf("advisory read = (%#v, %v)", blocked, reserved)
	}
	weak := advisory.finishReservedRead(true)
	if weak.State != localizationStateNeedsRecovery || weak.Enforceable || weak.RequiredAction != "recover_once" {
		t.Fatalf("unproven read upgraded enforceability: %#v", weak)
	}
}

func TestLocalizationEnforceabilityPersistsAcrossExactRead(t *testing.T) {
	const symbol = "repo/file.go::Run"
	completion := newLocalizationCompletion(false, symbol)
	completion.enforceableOnAnswerReady = true
	state := newLocalizationTerminalState()
	state.arm(completion)
	args := map[string]any{"target": map[string]any{"symbol": symbol}}
	if blocked, reserved := state.authorize("read", "source", args); blocked != nil || !reserved {
		t.Fatalf("exact read = (%#v, %v)", blocked, reserved)
	}
	terminal := state.finishReservedRead(true)
	if !terminal.Enforceable || terminal.ContractVersion != localizationTerminalContractV2 {
		t.Fatalf("exact-read completion = %#v", terminal)
	}
}

func TestLocalizationResetClearsEnforceabilityProvenance(t *testing.T) {
	state := newLocalizationTerminalState()
	state.mu.Lock()
	state.state = localizationStateAnswerReady
	state.inFlightEnforceable = true
	state.enforceableOnAnswerReady = true
	state.mu.Unlock()

	state.reset()

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.inFlightEnforceable || state.enforceableOnAnswerReady {
		t.Fatalf("reset retained enforceability provenance: in_flight=%v answer_ready=%v", state.inFlightEnforceable, state.enforceableOnAnswerReady)
	}
}

func TestFailedEnforceableRouteClearsEnforceabilityProvenance(t *testing.T) {
	const symbol = "repo/file.go::Wrapper"
	state := newLocalizationTerminalState()
	state.armRefinementRoutesForTask(
		"find the implementation",
		symbol,
		[]string{symbol},
		map[string]localizationRefinementRoute{symbol: {enforceable: true}},
		testEvidenceDigest(),
	)
	args := map[string]any{"target": map[string]any{"symbol": symbol}}
	if blocked, reserved := state.authorize("read", "source", args); blocked != nil || !reserved {
		t.Fatalf("enforceable refinement read = (%#v, %v)", blocked, reserved)
	}
	state.mu.Lock()
	state.enforceableOnAnswerReady = true // simulate stale provenance defensively
	state.mu.Unlock()

	completion := state.finishReservedRead(false)
	if completion.Enforceable || completion.enforceableOnAnswerReady {
		t.Fatalf("failed route retained enforceability in completion: %#v", completion)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.inFlightEnforceable || state.enforceableOnAnswerReady {
		t.Fatalf("failed route retained enforceability provenance: in_flight=%v answer_ready=%v", state.inFlightEnforceable, state.enforceableOnAnswerReady)
	}
}
