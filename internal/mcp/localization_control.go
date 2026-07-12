package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/graph"
)

const (
	localizationControlVersion = 1
	localizationStateTTL       = 3 * time.Minute
)

type localizationPhase uint8

const (
	localizationInactive localizationPhase = iota
	localizationOpen
	localizationTerminal
)

type localizationState struct {
	phase       localizationPhase
	generation  uint64
	task        string
	taskTerms   map[string]struct{}
	returnedIDs map[string]struct{}
	draft       string
	remaining   uint8
	inFlight    bool
	answerReady bool
	expiresAt   time.Time
}

type localizationReservation struct {
	state      *localizationState
	generation uint64
}

type localizationControl struct {
	ControlVersion   int      `json:"control_version"`
	Scope            string   `json:"scope"`
	State            string   `json:"state"`
	AnswerNow        bool     `json:"answer_now"`
	FollowupBudget   int      `json:"followup_budget"`
	AllowedFollowups []string `json:"allowed_followups,omitempty"`
	AfterFollowup    string   `json:"after_followup,omitempty"`
	Executed         bool     `json:"executed"`
}

func exploreLocalizationControl(answerReady bool, budget int, executed bool) localizationControl {
	state := "refine_once"
	allowed := []string{
		"read:source:returned-exact-symbol",
		"search:one-focused",
		"explore:one-focused",
	}
	if answerReady {
		state = "answer_ready"
		allowed = []string{"read:source:returned-exact-symbol"}
	}
	if budget <= 0 {
		state = "answer_ready"
		allowed = nil
		answerReady = true
	}
	return localizationControl{
		ControlVersion:   localizationControlVersion,
		Scope:            "localization",
		State:            state,
		AnswerNow:        answerReady,
		FollowupBudget:   budget,
		AllowedFollowups: allowed,
		AfterFollowup:    "answer",
		Executed:         executed,
	}
}

func (s *Server) armExploreLocalization(ctx context.Context, task string, targets []exploreTarget, answerReady bool) localizationControl {
	state := s.sessionFor(ctx)
	if state == nil {
		return exploreLocalizationControl(answerReady, 1, true)
	}
	ids := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.node != nil && target.node.ID != "" {
			ids[target.node.ID] = struct{}{}
		}
	}
	draft := buildLocalizationDraft(targets)
	terms := localizationTaskTerms(task)

	state.mu.Lock()
	defer state.mu.Unlock()
	// A permitted refinement is already reserved by beginLocalizationFacade.
	// Refresh its evidence, but leave the reservation/generation intact; the
	// facade dispatcher closes the budget after the handler succeeds.
	if current := state.localization; current != nil && current.inFlight {
		current.task = task
		current.taskTerms = terms
		current.returnedIDs = ids
		current.draft = draft
		current.answerReady = answerReady
		return exploreLocalizationControl(answerReady, 0, true)
	}
	generation := uint64(1)
	if state.localization != nil {
		generation = state.localization.generation + 1
	}
	state.localization = &localizationState{
		phase:       localizationOpen,
		generation:  generation,
		task:        task,
		taskTerms:   terms,
		returnedIDs: ids,
		draft:       draft,
		remaining:   1,
		answerReady: answerReady,
		expiresAt:   time.Now().Add(localizationStateTTL),
	}
	return exploreLocalizationControl(answerReady, 1, true)
}

func (s *Server) preflightLocalizationRead(ctx context.Context, spec facadeOperationSpec, args map[string]any) *mcpgo.CallToolResult {
	if spec.Facade != "read" {
		return nil
	}
	state := s.sessionFor(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	current := state.localization
	if current != nil && !current.inFlight && !current.expiresAt.IsZero() && time.Now().After(current.expiresAt) {
		state.localization = nil
		return nil
	}
	if current == nil || current.phase == localizationInactive {
		return nil
	}
	if current.phase == localizationTerminal || current.remaining == 0 || current.inFlight {
		return localizationAnswerNowResult(current.draft, false)
	}
	if exactReturnedSymbolRead(spec, args, current.returnedIDs) {
		return nil
	}
	current.phase = localizationTerminal
	current.remaining = 0
	return localizationAnswerNowResult(current.draft, false)
}

func (s *Server) beginLocalizationFacade(ctx context.Context, spec facadeOperationSpec, args map[string]any) (*localizationReservation, *mcpgo.CallToolResult) {
	state := s.sessionFor(ctx)
	if state == nil {
		return nil, nil
	}
	if localizationPhaseTransitionFacade(spec.Facade) {
		state.mu.Lock()
		if current := state.localization; current != nil && current.inFlight {
			// Leave the reserved pointer in place until its handler returns so
			// it cannot re-arm localization after the implementation phase began.
			current.phase = localizationInactive
			current.remaining = 0
		} else {
			state.localization = nil
		}
		state.mu.Unlock()
		return nil, nil
	}
	if !localizationFacade(spec.Facade) {
		return nil, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	current := state.localization
	if current != nil && !current.inFlight && !current.expiresAt.IsZero() && time.Now().After(current.expiresAt) {
		state.localization = nil
		return nil, nil
	}
	if current == nil || current.phase == localizationInactive {
		return nil, nil
	}

	if spec.Facade == "explore" && !current.inFlight {
		nextTask := strings.TrimSpace(fmt.Sprint(args["task"]))
		if nextTask != "" && localizationTasksMateriallyDifferent(current.taskTerms, localizationTaskTerms(nextTask)) {
			state.localization = nil
			return nil, nil
		}
	}

	block := func() (*localizationReservation, *mcpgo.CallToolResult) {
		// Never release another call's reservation. A parallel request gets
		// the terminal snapshot while the original handler retains ownership.
		if !current.inFlight {
			current.phase = localizationTerminal
			current.remaining = 0
		}
		return nil, localizationAnswerNowResult(current.draft, false)
	}
	if current.phase == localizationTerminal || current.remaining == 0 || current.inFlight {
		return block()
	}

	allowed := false
	if current.answerReady {
		allowed = exactReturnedSymbolRead(spec, args, current.returnedIDs) || localizationAnalyticFollowup(spec)
	} else {
		allowed = exactReturnedSymbolRead(spec, args, current.returnedIDs) ||
			spec.Facade == "search" || focusedExploreRefinement(spec, args, current.task) ||
			localizationAnalyticFollowup(spec)
	}
	if !allowed {
		return block()
	}

	current.remaining--
	current.inFlight = true
	current.generation++
	return &localizationReservation{state: current, generation: current.generation}, nil
}

func (s *Server) completeLocalizationFacade(ctx context.Context, reservation *localizationReservation, result *mcpgo.CallToolResult, callErr error) *mcpgo.CallToolResult {
	if reservation == nil {
		return result
	}
	state := s.sessionFor(ctx)
	if state == nil {
		return result
	}
	state.mu.Lock()
	current := state.localization
	if current != reservation.state || current == nil || current.generation != reservation.generation {
		state.mu.Unlock()
		return result
	}
	if current.phase == localizationInactive {
		state.localization = nil
		state.mu.Unlock()
		return result
	}
	if callErr != nil || result == nil || result.IsError {
		current.inFlight = false
		current.remaining = 1
		state.mu.Unlock()
		return result
	}
	current.inFlight = false
	current.remaining = 0
	current.phase = localizationTerminal
	draft := current.draft
	state.mu.Unlock()

	return attachLocalizationControl(result, exploreLocalizationControl(true, 0, true), true, draft)
}

func localizationFacade(facade string) bool {
	switch facade {
	case "explore", "search", "read", "relations", "trace", "analyze", "ask":
		return true
	default:
		return false
	}
}

func localizationAnalyticFollowup(spec facadeOperationSpec) bool {
	switch spec.Facade {
	case "relations", "trace", "analyze", "ask":
		return true
	default:
		return false
	}
}

func localizationPhaseTransitionFacade(facade string) bool {
	switch facade {
	case "change", "edit", "refactor", "review", "pr", "publish_review", "overlay", "workspace_admin", "session":
		return true
	default:
		return false
	}
}

func exactReturnedSymbolRead(spec facadeOperationSpec, args map[string]any, returned map[string]struct{}) bool {
	if spec.Facade != "read" || spec.Operation != "source" || len(returned) == 0 {
		return false
	}
	id := strings.TrimSpace(fmt.Sprint(args["id"]))
	if id == "" || id == "<nil>" || strings.Contains(id, ",") {
		return false
	}
	_, ok := returned[id]
	return ok
}

func focusedExploreRefinement(spec facadeOperationSpec, args map[string]any, original string) bool {
	if spec.Facade != "explore" {
		return false
	}
	next := strings.TrimSpace(fmt.Sprint(args["task"]))
	if next == "" || next == "<nil>" || strings.EqualFold(strings.TrimSpace(original), next) {
		return false
	}
	oldTerms := localizationTaskTerms(original)
	newTerms := localizationTaskTerms(next)
	for term := range newTerms {
		if _, ok := oldTerms[term]; ok {
			return true
		}
	}
	return false
}

func localizationTaskTerms(task string) map[string]struct{} {
	return exploreTerminalTerms(stripLeadingExploreDirective(shapeExploreQuery(task)))
}

func localizationTasksMateriallyDifferent(a, b map[string]struct{}) bool {
	if len(a) < 2 || len(b) < 2 {
		return false
	}
	for term := range a {
		if _, ok := b[term]; ok {
			return false
		}
	}
	return true
}

func buildLocalizationDraft(targets []exploreTarget) string {
	var b strings.Builder
	b.WriteString("FILES / SYMBOLS / EVIDENCE draft (copy or trim this in the final answer):\n")
	fileSeen := make(map[string]struct{})
	fileOrder := make([]string, 0, 8)
	addFile := func(node *graph.Node) {
		if node == nil || len(fileOrder) >= 8 {
			return
		}
		path := nodeDisplayPath(node)
		if path == "" {
			return
		}
		if _, seen := fileSeen[path]; seen {
			return
		}
		fileSeen[path] = struct{}{}
		fileOrder = append(fileOrder, path)
	}
	neighborSeen := make(map[string]struct{})
	for i, target := range targets {
		if target.node == nil {
			continue
		}
		addFile(target.node)
		if i < 5 {
			fmt.Fprintf(&b, "- %s — %s\n", target.node.ID, nodeLoc(target.node))
		}
		for _, adjacent := range [][]*graph.Node{target.callers, target.callees} {
			for _, node := range adjacent {
				if node == nil || node.ID == "" || len(neighborSeen) >= 6 {
					continue
				}
				if _, seen := neighborSeen[node.ID]; seen {
					continue
				}
				neighborSeen[node.ID] = struct{}{}
				addFile(node)
			}
		}
	}
	if len(fileOrder) > 0 {
		b.WriteString("Files represented by ranked and graph-neighbor evidence:")
		for _, path := range fileOrder {
			fmt.Fprintf(&b, " %s", path)
		}
		b.WriteByte('\n')
	}
	return truncateLocalizationDraft(b.String(), 2400)
}

func truncateLocalizationDraft(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "…\n"
}

func localizationAnswerNowResult(draft string, executed bool) *mcpgo.CallToolResult {
	text := "ANSWER-READY: localization follow-up budget is exhausted. No further search/read/explore work was run. Answer now from the evidence already returned."
	if strings.TrimSpace(draft) != "" {
		text += "\n\n" + strings.TrimSpace(draft)
	}
	return attachLocalizationControl(mcpgo.NewToolResultText(text), exploreLocalizationControl(true, 0, executed), false, "")
}

func attachLocalizationControl(result *mcpgo.CallToolResult, control localizationControl, appendAnswerNow bool, draft string) *mcpgo.CallToolResult {
	if result == nil {
		return result
	}
	payload := make(map[string]any)
	canReplaceStructured := true
	if result.StructuredContent != nil {
		body, err := json.Marshal(result.StructuredContent)
		if err != nil {
			canReplaceStructured = false
		} else if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
			payload = map[string]any{"data": json.RawMessage(body)}
		}
	}
	if previous, exists := payload["gortex"]; exists {
		payload["gortex_previous"] = previous
	}
	payload["gortex"] = control
	if canReplaceStructured {
		if body, err := json.Marshal(payload); err == nil {
			result.StructuredContent = json.RawMessage(body)
		}
	}
	if result.Meta == nil {
		result.Meta = &mcpgo.Meta{}
	}
	if result.Meta.AdditionalFields == nil {
		result.Meta.AdditionalFields = make(map[string]any)
	}
	result.Meta.AdditionalFields["gortex_control"] = control
	if appendAnswerNow {
		text := "ANSWER-READY: the one permitted localization follow-up completed. Answer now; do not call search/read/explore again."
		if strings.TrimSpace(draft) != "" {
			text += "\n\n" + strings.TrimSpace(draft)
		}
		result.Content = append(result.Content, mcpgo.TextContent{Type: "text", Text: text})
	}
	return result
}
