package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testEvidenceDigest() *localizationEvidenceDigest {
	return newLocalizationEvidenceDigest(localizationExploreEnvelope{
		Files:   []string{"repo/storage/disk.go", "repo/storage/cloud.go"},
		Symbols: []string{"repo/storage/disk.go::DiskStorage.Load", "repo/storage/cloud.go::CloudStorage.Load"},
		Evidence: []localizationEvidence{
			{Rank: 1, ID: "repo/storage/disk.go::DiskStorage.Load", Name: "Load", File: "repo/storage/disk.go", Line: 42, Signature: "func (s *DiskStorage) Load(key string) ([]byte, error)"},
			{Rank: 2, ID: "repo/storage/cloud.go::CloudStorage.Load", Name: "Load", File: "repo/storage/cloud.go", Line: 17},
		},
	})
}

func TestPostTerminalNavigationReplaysEvidenceAsSuccess(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")

	first, reserved := state.authorize("search", "symbols", nil)
	if reserved {
		t.Fatal("post-terminal navigation must not reserve a read")
	}
	if first == nil || first.IsError {
		t.Fatalf("post-terminal navigation = %+v, want successful replay", first)
	}
	text, ok := singleTextContent(first)
	if !ok {
		t.Fatal("replay result must carry text content")
	}
	if !strings.Contains(text, localizationReplayNotice) || !strings.Contains(text, "repo/storage/disk.go") {
		t.Fatalf("replay must carry neutral retained evidence: %q", text)
	}
	if strings.Contains(text, "answer NOW") || strings.Contains(text, "FILES:") {
		t.Fatalf("replay must not carry an injected-looking answer: %q", text)
	}

	second, _ := state.authorize("relations", "usages", nil)
	firstText, _ := singleTextContent(first)
	secondText, _ := singleTextContent(second)
	if firstText != secondText {
		t.Fatal("post-terminal replay must be idempotent across calls and facades")
	}
}

func TestRepeatLocalizeAgainstTerminalContractReplaysEvidence(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")

	token, blocked := state.beginLocalize("find the storage load implementations again", false)
	if token != 0 {
		t.Fatal("repeat localize must not reserve the handler slot")
	}
	if blocked == nil || blocked.IsError {
		t.Fatalf("repeat localize = %+v, want successful replay", blocked)
	}
	text, _ := singleTextContent(blocked)
	if !strings.Contains(text, "repo/storage/disk.go") || strings.Contains(text, "final_response") {
		t.Fatalf("repeat localize must replay neutral retained evidence: %q", text)
	}
}

func TestRefinementPromotionRetainsDigestForReplay(t *testing.T) {
	state := &localizationTerminalState{}
	candidate := "repo/storage/disk.go::DiskStorage.Load"
	state.armRefinementForTask("find the storage load implementations", candidate, []string{candidate}, testEvidenceDigest())

	args := map[string]any{"target": map[string]any{"symbol": candidate}}
	if blocked, reserved := state.authorize("read", "source", args); blocked != nil || !reserved {
		t.Fatalf("permitted refinement read = (%v, %v), want reservation", blocked, reserved)
	}
	state.finishReservedRead(true)

	replay, reserved := state.authorize("search", "symbols", nil)
	if reserved || replay == nil || replay.IsError {
		t.Fatalf("post-promotion navigation = (%+v, %v), want successful replay", replay, reserved)
	}
	text, _ := singleTextContent(replay)
	if !strings.Contains(text, "repo/storage/cloud.go") {
		t.Fatal("promotion must replay the digest stashed at refinement arm time")
	}
}

func TestDigestLifecycleAndLegacyFallback(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")
	state.keepOpenForTask("new unrelated work")
	if blocked, _ := state.authorize("search", "symbols", nil); blocked != nil {
		t.Fatalf("inactive state must not block, got %+v", blocked)
	}
	if state.digest != nil {
		t.Fatal("keepOpenForTask must clear the retained digest")
	}

	legacy := &localizationTerminalState{}
	legacy.armForTask(newLocalizationCompletion(true, ""), "task without digest")
	blocked, _ := legacy.authorize("search", "symbols", nil)
	if blocked == nil || !blocked.IsError {
		t.Fatal("answer_ready without a digest must keep the error contract")
	}
}

func TestDigestByteCapShedsEvidenceTail(t *testing.T) {
	envelope := localizationExploreEnvelope{}
	for i := 0; i < 400; i++ {
		envelope.Evidence = append(envelope.Evidence, localizationEvidence{
			Rank:      i + 1,
			ID:        fmt.Sprintf("repo/big/file.go::Sym%03d", i),
			Name:      strings.Repeat("n", 40),
			File:      "repo/big/file.go",
			Line:      i,
			Signature: strings.Repeat("s", 2000),
		})
	}
	digest := newLocalizationEvidenceDigest(envelope)
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if len(encoded) > localizationDigestMaxBytes {
		t.Fatalf("digest = %d bytes, want <= %d", len(encoded), localizationDigestMaxBytes)
	}
	if len(digest.Evidence) == 0 || len(digest.Evidence) >= localizationReplayEvidenceLimit {
		t.Fatalf("byte cap retained %d rows, want a non-empty shed prefix", len(digest.Evidence))
	}
	if !reflect.DeepEqual(digest.Files, []string{"repo/big/file.go"}) {
		t.Fatalf("files were not rebuilt from retained evidence: %#v", digest.Files)
	}
	if len(digest.Symbols) != len(digest.Evidence) {
		t.Fatalf("symbols=%d evidence=%d, want one supported symbol per row", len(digest.Symbols), len(digest.Evidence))
	}
}

func TestDigestByteCapRetainsSingleMandatoryRowAfterSheddingOptionalFields(t *testing.T) {
	envelope := localizationExploreEnvelope{Evidence: []localizationEvidence{{
		Rank:      1,
		ID:        "repo/registry.go::Registry.Configure",
		Name:      strings.Repeat("n", 1000),
		QualName:  strings.Repeat("q", 3000),
		Kind:      "method",
		File:      "repo/registry.go",
		Line:      17,
		Signature: strings.Repeat("s", 8000),
		Callers:   []string{strings.Repeat("caller", 1000)},
		Callees:   []string{strings.Repeat("callee", 1000)},
	}}}

	digest := newLocalizationEvidenceDigest(envelope)
	if len(digest.Evidence) != 1 {
		t.Fatalf("mandatory evidence rows = %d, want 1", len(digest.Evidence))
	}
	row := digest.Evidence[0]
	if row.ID != envelope.Evidence[0].ID || row.File != envelope.Evidence[0].File || row.Line != envelope.Evidence[0].Line {
		t.Fatalf("mandatory row identity changed while shedding: %#v", row)
	}
	if row.Signature != "" || row.QualName != "" || len(row.Callers) != 0 || len(row.Callees) != 0 {
		t.Fatalf("largest optional fields were retained after the digest fit: %#v", row)
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if len(encoded) > localizationDigestMaxBytes {
		t.Fatalf("shed digest = %d bytes, want <= %d", len(encoded), localizationDigestMaxBytes)
	}
}

func TestPostTerminalReadsExecuteWhileNavigationReplays(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")

	if blocked, reserved := state.authorize("read", "source", map[string]any{
		"target": map[string]any{"symbol": "repo/storage/disk.go::DiskStorage.Load"},
	}); blocked != nil || reserved {
		t.Fatalf("post-terminal read = (%v, %v), want allowed without reservation", blocked, reserved)
	}
	if replay, _ := state.authorize("search", "symbols", nil); replay == nil || replay.IsError {
		t.Fatal("post-terminal navigation must still replay")
	}
	if note, ok := state.answerReadyDirective(); !ok || !strings.Contains(note, "repo/storage/disk.go") || strings.Contains(note, "FILES:") {
		t.Fatalf("post-terminal read note must carry neutral retained evidence, got %q ok=%v", note, ok)
	}
}

func TestLocalizationDigestKeepsOnlyConcreteBoundedEvidence(t *testing.T) {
	envelope := localizationExploreEnvelope{
		Files:   []string{"pkg/unsupported.go", "pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go", "pkg/f.go"},
		Symbols: []string{"repo/pkg/unsupported.go::Unsupported", "repo/pkg/a.go::A", "repo/pkg/b.go::B", "repo/pkg/c.go::C", "repo/pkg/d.go::D", "repo/pkg/e.go::E", "repo/pkg/f.go::F"},
		Evidence: []localizationEvidence{
			{Rank: 1, ID: "repo/pkg/a.go::A", Name: "A", Kind: "function", File: "pkg/a.go", Line: 10, Callers: []string{"repo/pkg/caller.go::CallA"}},
			{Rank: 2, ID: "repo/pkg/b.go::B", Name: "B", Kind: "method", File: "pkg/b.go", Line: 20},
			{Rank: 3, ID: "repo/pkg/c.go::C", Name: "C", Kind: "method", File: "pkg/c.go", Line: 30},
			{Rank: 4, ID: "repo/pkg/d.go::D", Name: "D", Kind: "method", File: "pkg/d.go", Line: 40},
			{Rank: 5, ID: "repo/pkg/e.go::E", Name: "E", Kind: "method", File: "pkg/e.go", Line: 50},
			{Rank: 6, ID: "repo/pkg/f.go::F", Name: "F", Kind: "method", File: "pkg/f.go", Line: 60},
			{Rank: 7, ID: "repo/pkg/missing.go::Missing", Name: "Missing"},
		},
	}

	digest := newLocalizationEvidenceDigest(envelope)
	if len(digest.Evidence) != localizationReplayEvidenceLimit {
		t.Fatalf("retained evidence = %d, want %d", len(digest.Evidence), localizationReplayEvidenceLimit)
	}
	wantFiles := []string{"pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go"}
	wantSymbols := []string{"repo/pkg/a.go::A", "repo/pkg/b.go::B", "repo/pkg/c.go::C", "repo/pkg/d.go::D", "repo/pkg/e.go::E"}
	if !reflect.DeepEqual(digest.Files, wantFiles) {
		t.Fatalf("digest files = %#v, want %#v", digest.Files, wantFiles)
	}
	if !reflect.DeepEqual(digest.Symbols, wantSymbols) {
		t.Fatalf("digest symbols = %#v, want %#v", digest.Symbols, wantSymbols)
	}
	if got := digest.Evidence[0].Callers; !reflect.DeepEqual(got, []string{"repo/pkg/caller.go::CallA"}) {
		t.Fatalf("causal provenance was dropped: %#v", got)
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if strings.Contains(string(encoded), "final_response") || strings.Contains(string(encoded), "unsupported") {
		t.Fatalf("digest retained an unsupported or prewritten answer field: %s", encoded)
	}
}

func TestLocalizationEvidenceReplaySeparatesVisibleEvidenceFromHostFallback(t *testing.T) {
	digest := &localizationEvidenceDigest{
		Files:   []string{"pkg/handler.go", "pkg/rotating.go"},
		Symbols: []string{"repo/pkg/handler.go::Handler.Write", "repo/pkg/rotating.go::RotatingHandler"},
		Evidence: []localizationDigestRow{
			{Rank: 1, ID: "repo/pkg/handler.go::Handler.Write", File: "pkg/handler.go", Line: 42, Signature: "func (h *Handler) Write(record Record)"},
			{Rank: 2, ID: "repo/pkg/rotating.go::RotatingHandler", File: "pkg/rotating.go", Line: 17, Callers: []string{"repo/pkg/factory.go::NewHandler"}},
		},
	}
	result := localizationEvidenceReplayResult(newLocalizationCompletion(true, ""), digest)
	if result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("replay result = %#v, want one successful visible evidence block", result)
	}
	visible, ok := singleTextContent(result)
	if !ok {
		t.Fatalf("replay has no text content: %#v", result)
	}
	for _, forbidden := range []string{"answer NOW", "verbatim", "final_response", `"directive"`, "FILES:\n", "SYMBOLS:\n"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("model-visible replay contains %q: %s", forbidden, visible)
		}
	}
	for _, required := range []string{"ranked candidates", "candidate presence alone does not prove a change target", "pkg/rotating.go:17", "repo/pkg/rotating.go::RotatingHandler"} {
		if !strings.Contains(visible, required) {
			t.Fatalf("model-visible replay omitted %q: %s", required, visible)
		}
	}

	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content type = %T, want map", result.StructuredContent)
	}
	if structured["replay"] != true || structured["completion"] == nil || structured["evidence_digest"] != digest {
		t.Fatalf("structured replay contract is incomplete: %#v", structured)
	}
	if _, exists := structured["directive"]; exists {
		t.Fatalf("structured content retained a directive: %#v", structured)
	}
	if _, exists := structured["final_response"]; exists {
		t.Fatalf("structured content exposed the host fallback: %#v", structured)
	}

	if result.Meta == nil {
		t.Fatal("replay omitted host metadata")
	}
	fallback, ok := result.Meta.AdditionalFields[localizationHostFallbackMetaKey].(string)
	if !ok || fallback == "" {
		t.Fatalf("host fallback metadata = %#v", result.Meta.AdditionalFields)
	}
	if !strings.Contains(fallback, "pkg/rotating.go:17") || strings.Contains(fallback, "answer NOW") || strings.Contains(fallback, "verbatim") {
		t.Fatalf("host fallback is missing evidence or retained coercive wording: %q", fallback)
	}
}

func TestAnswerReadyDirectiveUsesNeutralRetainedEvidence(t *testing.T) {
	digest := &localizationEvidenceDigest{Evidence: []localizationDigestRow{{
		Rank: 1, ID: "repo/pkg/file.go::Run", File: "pkg/file.go", Line: 12,
	}}}
	state := &localizationTerminalState{state: localizationStateAnswerReady, digest: digest}
	note, ok := state.answerReadyDirective()
	if !ok {
		t.Fatal("answer-ready state did not return retained evidence")
	}
	if !strings.Contains(note, "pkg/file.go:12") || !strings.Contains(note, "candidate presence alone does not prove a change target") {
		t.Fatalf("answer-ready note omitted neutral evidence: %q", note)
	}
	for _, forbidden := range []string{"answer NOW", "verbatim", "final_response", "FILES:", "SYMBOLS:"} {
		if strings.Contains(note, forbidden) {
			t.Fatalf("answer-ready note contains %q: %s", forbidden, note)
		}
	}
}
