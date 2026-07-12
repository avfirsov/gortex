package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestLocalizationBudgetExactReturnedReadConsumes(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	id := "repo/main.go::helper"
	targets := []exploreTarget{{node: &graph.Node{ID: id, Name: "helper", Kind: graph.KindFunction, FilePath: "main.go", StartLine: 3}}}
	control := srv.armExploreLocalization(ctx, "find helper function", targets, true)
	require.True(t, control.AnswerNow)
	require.Equal(t, 1, control.FollowupBudget)

	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"id": id})
	require.Nil(t, blocked)
	require.NotNil(t, reservation)

	result := srv.completeLocalizationFacade(ctx, reservation, mcpgo.NewToolResultText("func helper() {}"), nil)
	require.Contains(t, toolResultText(result), "one permitted localization follow-up completed")
	terminal := localizationControlFromResult(t, result)
	require.True(t, terminal.AnswerNow)
	require.Zero(t, terminal.FollowupBudget)
	require.True(t, terminal.Executed)

	reservation, blocked = srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "search", Operation: "text"}, map[string]any{"query": "helper"})
	require.Nil(t, reservation)
	require.NotNil(t, blocked)
	require.Contains(t, toolResultText(blocked), "No further search/read/explore work was run")
	require.Contains(t, toolResultText(blocked), id)
	require.False(t, localizationControlFromResult(t, blocked).Executed)
}

func TestLocalizationBudgetWeakResultAllowsOnlyOneFocusedCall(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	targets := []exploreTarget{{node: &graph.Node{ID: "repo/a.go::candidate", Name: "candidate", Kind: graph.KindFunction, FilePath: "a.go"}}}
	srv.armExploreLocalization(ctx, "diagnose parser timeout", targets, false)

	first, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "search", Operation: "symbols"}, map[string]any{"query": "parser timeout"})
	require.Nil(t, blocked)
	require.NotNil(t, first)

	second, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "search", Operation: "text"}, map[string]any{"query": "timeout"})
	require.Nil(t, second)
	require.NotNil(t, blocked, "a concurrent or second follow-up must not execute")
	require.Zero(t, localizationControlFromResult(t, blocked).FollowupBudget)
}

func TestLocalizationBudgetBlocksBroadReadButNotOtherDomains(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	targets := []exploreTarget{{node: &graph.Node{ID: "repo/main.go::helper", Name: "helper", Kind: graph.KindFunction, FilePath: "main.go"}}}
	srv.armExploreLocalization(ctx, "find helper", targets, true)

	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"path": "main.go"})
	require.Nil(t, reservation)
	require.NotNil(t, blocked)

	reservation, blocked = srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "relations", Operation: "usages"}, map[string]any{})
	require.Nil(t, reservation)
	require.NotNil(t, blocked, "alternate localization domains must not bypass the terminal budget")

	reservation, blocked = srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "change", Operation: "impact"}, map[string]any{})
	require.Nil(t, reservation)
	require.Nil(t, blocked, "localization terminality must not affect change/edit/test workflow calls")
	require.Nil(t, srv.session.localization, "entering the change/edit workflow clears localization-only state")

	reservation, blocked = srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"id": "repo/main.go::helper"})
	require.Nil(t, reservation)
	require.Nil(t, blocked, "post-change verification reads must be allowed")
}

func TestLocalizationBudgetMateriallyDifferentExploreResets(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	targets := []exploreTarget{{node: &graph.Node{ID: "repo/http.go::parse", Name: "parse", Kind: graph.KindFunction, FilePath: "http.go"}}}
	srv.armExploreLocalization(ctx, "fix HTTP parser timeout", targets, true)

	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "explore", Operation: "task"}, map[string]any{"task": "repair database migration checksum"})
	require.Nil(t, reservation)
	require.Nil(t, blocked)
	require.Nil(t, srv.session.localization, "a clearly different task starts with a fresh localization budget")
}

func TestLocalizationControlPreservesExistingStructuredContent(t *testing.T) {
	result := mcpgo.NewToolResultText("ok")
	result.StructuredContent = json.RawMessage(`{"payload":{"value":1}}`)
	result = attachLocalizationControl(result, exploreLocalizationControl(true, 0, true), false, "")

	body, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Contains(t, payload, "payload")
	require.Contains(t, payload, "gortex")
}

func TestLocalizationBudgetConcurrentExploreCannotRearm(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	initial := []exploreTarget{{node: &graph.Node{ID: "repo/a.go::candidate", Name: "candidate", Kind: graph.KindFunction, FilePath: "a.go"}}}
	srv.armExploreLocalization(ctx, "diagnose parser timeout", initial, false)

	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "explore", Operation: "task"}, map[string]any{"task": "parser timeout in decoder"})
	require.Nil(t, blocked)
	require.NotNil(t, reservation)

	parallel, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "explore", Operation: "task"}, map[string]any{"task": "repair database migration checksum"})
	require.Nil(t, parallel)
	require.NotNil(t, blocked)
	require.True(t, srv.session.localization.inFlight, "a parallel block must not release the active reservation")

	refined := []exploreTarget{{node: &graph.Node{ID: "repo/parser.go::decode", Name: "decode", Kind: graph.KindFunction, FilePath: "parser.go"}}}
	srv.armExploreLocalization(ctx, "parser timeout in decoder", refined, true)
	result := srv.completeLocalizationFacade(ctx, reservation, mcpgo.NewToolResultText("refined"), nil)
	require.Zero(t, localizationControlFromResult(t, result).FollowupBudget)
	require.Equal(t, localizationTerminal, srv.session.localization.phase)
	require.Zero(t, srv.session.localization.remaining)
	require.False(t, srv.session.localization.inFlight)
}

func TestLocalizationBudgetPhaseTransitionCancelsInFlightRearm(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	targets := []exploreTarget{{node: &graph.Node{ID: "repo/a.go::candidate", Name: "candidate", Kind: graph.KindFunction, FilePath: "a.go"}}}
	srv.armExploreLocalization(ctx, "diagnose parser timeout", targets, false)
	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "search", Operation: "text"}, map[string]any{"query": "timeout"})
	require.Nil(t, blocked)
	require.NotNil(t, reservation)

	_, blocked = srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "change", Operation: "impact"}, map[string]any{})
	require.Nil(t, blocked)
	require.Equal(t, localizationInactive, srv.session.localization.phase)

	result := srv.completeLocalizationFacade(ctx, reservation, mcpgo.NewToolResultText("search result"), nil)
	require.Equal(t, "search result", toolResultText(result))
	require.Nil(t, srv.session.localization)
}

func TestLocalizationBudgetExpires(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	targets := []exploreTarget{{node: &graph.Node{ID: "repo/a.go::candidate", Name: "candidate", Kind: graph.KindFunction, FilePath: "a.go"}}}
	srv.armExploreLocalization(ctx, "diagnose parser timeout", targets, true)
	srv.session.localization.expiresAt = time.Now().Add(-time.Second)

	reservation, blocked := srv.beginLocalizationFacade(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"id": "unrelated"})
	require.Nil(t, reservation)
	require.Nil(t, blocked)
	require.Nil(t, srv.session.localization)
}

func TestLocalizationReadPreflightRejectsShorthandBeforeResolution(t *testing.T) {
	srv := &Server{session: newSessionState()}
	ctx := context.Background()
	id := "repo/main.go::helper"
	targets := []exploreTarget{{node: &graph.Node{ID: id, Name: "helper", Kind: graph.KindFunction, FilePath: "main.go"}}}
	srv.armExploreLocalization(ctx, "find helper", targets, true)

	require.NotNil(t, srv.preflightLocalizationRead(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"id": "helper"}))

	srv.armExploreLocalization(ctx, "find helper", targets, true)
	require.Nil(t, srv.preflightLocalizationRead(ctx,
		facadeOperationSpec{Facade: "read", Operation: "source"}, map[string]any{"id": id}))
}

func TestLocalizationControlPreservesScalarAndCollision(t *testing.T) {
	result := mcpgo.NewToolResultText("ok")
	result.StructuredContent = "scalar"
	result = attachLocalizationControl(result, exploreLocalizationControl(true, 0, true), false, "")
	body, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var scalar map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &scalar))
	require.JSONEq(t, `"scalar"`, string(scalar["data"]))

	result.StructuredContent = map[string]any{"gortex": map[string]any{"old": true}}
	result = attachLocalizationControl(result, exploreLocalizationControl(true, 0, true), false, "")
	body, err = json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var collision map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &collision))
	require.Contains(t, collision, "gortex")
	require.Contains(t, collision, "gortex_previous")
}

func TestLocalizationControlKeepsUnserializableStructuredContent(t *testing.T) {
	result := mcpgo.NewToolResultText("ok")
	original := make(chan int)
	result.StructuredContent = original
	result = attachLocalizationControl(result, exploreLocalizationControl(true, 0, true), false, "")
	require.Equal(t, original, result.StructuredContent)
	require.NotNil(t, result.Meta)
	require.Contains(t, result.Meta.AdditionalFields, "gortex_control")
}

func localizationControlFromResult(t *testing.T, result *mcpgo.CallToolResult) localizationControl {
	t.Helper()
	require.NotNil(t, result)
	require.NotEmpty(t, result.StructuredContent)
	body, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &envelope))
	raw, ok := envelope["gortex"]
	require.True(t, ok)
	var control localizationControl
	require.NoError(t, json.Unmarshal(raw, &control))
	require.Equal(t, localizationControlVersion, control.ControlVersion)
	require.Equal(t, "localization", control.Scope)
	return control
}

func TestLocalizationDraftIsBoundedAndRecallOriented(t *testing.T) {
	targets := make([]exploreTarget, 0, 20)
	for i := 0; i < 20; i++ {
		id := strings.Repeat("symbol", 30) + string(rune('A'+i))
		targets = append(targets, exploreTarget{node: &graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: id + ".go"}})
	}
	draft := buildLocalizationDraft(targets)
	require.LessOrEqual(t, len(draft), 2404)
	require.Contains(t, draft, "FILES / SYMBOLS / EVIDENCE")
}
