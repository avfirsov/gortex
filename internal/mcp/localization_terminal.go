package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const (
	localizationStateInactive          = ""
	localizationStateNeedsExactRead    = "needs_exact_read"
	localizationStateNeedsRefinement   = "needs_refinement"
	localizationStateRefineInFlight    = "refinement_in_flight"
	localizationStateExactReadInFlight = "exact_read_in_flight"
	localizationStateAnswerReady       = "answer_ready"
)

// localizationCompletion is the host-neutral terminality contract returned by
// explore(operation:"localize"). Hosts may stop the turn from this payload;
// the server also enforces it for later Gortex navigation calls in the same
// MCP session.
type localizationCompletion struct {
	State            string `json:"state"`
	Scope            string `json:"scope"`
	RequiredAction   string `json:"required_action"`
	AllowedToolCalls int    `json:"allowed_tool_calls"`
	ExactSymbol      string `json:"exact_symbol,omitempty"`

	// refinementSymbols is session-only authorization state. Candidate IDs are
	// already present once in the envelope's symbols field, so they are not
	// serialized again in the completion payload.
	refinementSymbols []string
}

func newLocalizationCompletion(answerReady bool, exactSymbol string) localizationCompletion {
	if answerReady {
		return localizationCompletion{
			State:            localizationStateAnswerReady,
			Scope:            "localization",
			RequiredAction:   "respond",
			AllowedToolCalls: 0,
		}
	}
	return localizationCompletion{
		State:            localizationStateNeedsExactRead,
		Scope:            "localization",
		RequiredAction:   "read_exact",
		AllowedToolCalls: 1,
		ExactSymbol:      exactSymbol,
	}
}

// newLocalizationRefinementCompletion keeps uncertain localization successful
// and bounded. The ranked evidence remains usable, while the server permits
// exactly one source read of a returned candidate instead of allowing another
// broad exploration loop.
func newLocalizationRefinementCompletion() localizationCompletion {
	return localizationCompletion{
		State:            localizationStateNeedsRefinement,
		Scope:            "localization",
		RequiredAction:   "read_one_candidate",
		AllowedToolCalls: 1,
	}
}

// localizationTerminalState is intentionally session-local. It never affects
// mutation, analysis, workspace, or memory tools; it only prevents an agent
// from reopening localization after an explicit localization-only request.
type localizationTerminalState struct {
	mu                sync.Mutex
	state             string
	exactSymbol       string
	refinementSymbols []string
	taskFingerprint   string
	generation        uint64
	nextReservation   uint64
	reservation       *localizationReservation
}

type localizationReservation struct {
	token                  uint64
	generation             uint64
	pendingCompletion      localizationCompletion
	pendingTaskFingerprint string
	staged                 bool
}

func newLocalizationTerminalState() *localizationTerminalState {
	return &localizationTerminalState{}
}

func (s *localizationTerminalState) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.generation++
	s.state = localizationStateInactive
	s.exactSymbol = ""
	s.refinementSymbols = nil
	s.taskFingerprint = ""
	// Keep an in-flight reservation until its owner finishes. Its captured
	// generation is now stale, so finishLocalize cannot commit it, while a
	// second localization cannot race ahead of the still-running handler.
	s.mu.Unlock()
}

func (s *localizationTerminalState) arm(completion localizationCompletion) {
	s.armForTask(completion, "")
}

func (s *localizationTerminalState) armForTask(completion localizationCompletion, task string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fingerprint := localizationTaskFingerprint(task)
	if s.reservation != nil {
		s.reservation.pendingCompletion = completion
		s.reservation.pendingTaskFingerprint = fingerprint
		s.reservation.staged = true
		return
	}
	s.commitLocalizationLocked(completion, fingerprint)
}

// keepOpenForTask transactionally replaces any prior terminal contract with
// inactive navigation state. Under facade dispatch the inactive state is
// staged until the localization response succeeds; direct handlers commit it
// immediately.
func (s *localizationTerminalState) keepOpenForTask(task string) {
	s.armForTask(localizationCompletion{State: localizationStateInactive}, task)
}

func (s *localizationTerminalState) armRefinementForTask(task string, symbols []string) {
	completion := newLocalizationRefinementCompletion()
	seen := make(map[string]struct{}, min(len(symbols), 12))
	completion.refinementSymbols = make([]string, 0, min(len(symbols), 12))
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		completion.refinementSymbols = append(completion.refinementSymbols, symbol)
		if len(completion.refinementSymbols) == 12 {
			break
		}
	}
	if len(completion.refinementSymbols) == 0 {
		s.keepOpenForTask(task)
		return
	}
	s.armForTask(completion, task)
}

func (s *localizationTerminalState) commitLocalizationLocked(completion localizationCompletion, fingerprint string) {
	s.generation++
	s.state = completion.State
	s.exactSymbol = completion.ExactSymbol
	s.refinementSymbols = nil
	if completion.State == localizationStateNeedsRefinement {
		s.refinementSymbols = append([]string(nil), completion.refinementSymbols...)
	}
	s.taskFingerprint = fingerprint
}

func (s *localizationTerminalState) completionLocked() localizationCompletion {
	switch s.state {
	case localizationStateNeedsRefinement, localizationStateRefineInFlight:
		completion := newLocalizationRefinementCompletion()
		if s.state == localizationStateRefineInFlight {
			completion.State = localizationStateRefineInFlight
			completion.AllowedToolCalls = 0
		}
		return completion
	case localizationStateNeedsExactRead, localizationStateExactReadInFlight:
		return newLocalizationCompletion(false, s.exactSymbol)
	default:
		return newLocalizationCompletion(true, "")
	}
}

func (s *localizationTerminalState) refinementAllowsLocked(symbol string) bool {
	if symbol == "" {
		return false
	}
	for _, candidate := range s.refinementSymbols {
		if symbol == candidate {
			return true
		}
	}
	return false
}

// beginLocalize reserves the only localization handler slot for this session.
// An inactive session admits its first localization without a boundary flag.
// Once a contract exists, only the first localize call for a genuinely new user
// request may cross it, and the caller must say so explicitly. The old contract
// remains live until finishLocalize commits the successful replacement.
func (s *localizationTerminalState) beginLocalize(task string, newUserTask bool) (uint64, *mcpgo.CallToolResult) {
	if s == nil {
		return 0, nil
	}
	fingerprint := localizationTaskFingerprint(task)
	if fingerprint == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservation != nil {
		return 0, localizationInProgressResult()
	}
	if s.state != localizationStateInactive && !newUserTask {
		completion := s.completionLocked()
		return 0, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeLocalizationComplete,
			Message:   "this user request already has a localization completion contract; follow it instead of starting another localize call",
			Data: map[string]any{
				"completion": completion,
				"facade":     "explore",
				"operation":  "localize",
			},
		})
	}
	s.nextReservation++
	if s.nextReservation == 0 {
		s.nextReservation++
	}
	token := s.nextReservation
	s.reservation = &localizationReservation{token: token, generation: s.generation}
	return token, nil
}

// finishLocalize commits only the completion staged by the matching reservation
// and only if no reset changed its generation. Errors and panics pass success=false
// and leave the prior contract untouched. A stale finisher can never clear or
// overwrite a newer reservation.
func (s *localizationTerminalState) finishLocalize(token uint64, success bool) bool {
	if s == nil || token == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reservation := s.reservation
	if reservation == nil || reservation.token != token {
		return false
	}
	s.reservation = nil
	if !success || !reservation.staged || reservation.generation != s.generation {
		return false
	}
	s.commitLocalizationLocked(reservation.pendingCompletion, reservation.pendingTaskFingerprint)
	return true
}

func localizationInProgressResult() *mcpgo.CallToolResult {
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeLocalizationComplete,
		Message:   "a localization request is already in progress for this session",
		Data: map[string]any{
			"completion": map[string]any{
				"state": "localization_in_progress", "scope": "localization",
				"required_action": "wait", "allowed_tool_calls": 0,
			},
			"facade": "explore", "operation": "localize",
		},
	})
}

func localizationTaskFingerprint(task string) string {
	return strings.Join(strings.Fields(task), " ")
}

// authorize checks a navigation call and reserves the single permitted
// localization read when applicable. The caller must finish the reservation
// after invocation so a failed read restores the allowance instead of silently
// consuming it.
func (s *localizationTerminalState) authorize(facade, operation string, arguments map[string]any) (*mcpgo.CallToolResult, bool) {
	if s == nil || !localizationNavigationFacade(facade) {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservation != nil {
		return localizationInProgressResult(), false
	}
	if s.state == localizationStateInactive {
		return nil, false
	}
	if s.state == localizationStateNeedsExactRead && facade == "read" && operation == "source" && exactLocalizationSymbol(arguments) == s.exactSymbol {
		s.state = localizationStateExactReadInFlight
		return nil, true
	}
	if s.state == localizationStateNeedsRefinement && facade == "read" && operation == "source" && s.refinementAllowsLocked(exactLocalizationSymbol(arguments)) {
		s.state = localizationStateRefineInFlight
		return nil, true
	}

	completion := s.completionLocked()
	message := "localization is complete; return the existing evidence without another Gortex navigation call"
	switch s.state {
	case localizationStateNeedsExactRead:
		message = fmt.Sprintf("localization needs exactly one read(operation:\"source\") for %q; other navigation calls are blocked", s.exactSymbol)
	case localizationStateExactReadInFlight:
		message = "the permitted exact localization read is already in progress"
	case localizationStateNeedsRefinement:
		message = "localization permits exactly one read(operation:\"source\") of a returned candidate symbol; other navigation calls are blocked"
	case localizationStateRefineInFlight:
		message = "the permitted localization refinement read is already in progress"
	}
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeLocalizationComplete,
		Message:   message,
		Data: map[string]any{
			"completion": completion,
			"facade":     facade,
			"operation":  operation,
		},
	}), false
}

func (s *localizationTerminalState) finishReservedRead(success bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case localizationStateExactReadInFlight:
		if success {
			s.state = localizationStateAnswerReady
			s.exactSymbol = ""
			return
		}
		s.state = localizationStateNeedsExactRead
	case localizationStateRefineInFlight:
		if success {
			s.state = localizationStateAnswerReady
			s.refinementSymbols = nil
			return
		}
		s.state = localizationStateNeedsRefinement
	}
}

// block is retained for direct state checks; production dispatch uses
// authorize so it can finish a reserved exact read after handler completion.
func (s *localizationTerminalState) block(facade, operation string, arguments map[string]any) *mcpgo.CallToolResult {
	blocked, _ := s.authorize(facade, operation, arguments)
	return blocked
}

func localizationNavigationFacade(facade string) bool {
	switch facade {
	case "explore", "search", "read", "relations", "trace":
		return true
	default:
		return false
	}
}

func exactLocalizationSymbol(arguments map[string]any) string {
	if target, ok := arguments["target"].(map[string]any); ok {
		return strings.TrimSpace(fmt.Sprint(target["symbol"]))
	}
	return strings.TrimSpace(fmt.Sprint(arguments["symbol"]))
}

func (s *Server) localizationFor(ctx context.Context) *localizationTerminalState {
	id := SessionIDFromContext(ctx)
	if id == "" || s.sessions == nil {
		return s.localization
	}
	return s.sessions.get(id).localization
}
