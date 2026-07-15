package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// momentumReadThreshold is the per-session graph-read count after which the
// one-shot momentum note is attached. Matches the documented "one explore /
// smart_context call replaces 5-10 exploration calls" shape: past that many
// granular reads the session has out-read the one-shot phase and is usually
// sitting on enough evidence to act.
const momentumReadThreshold = 6

// momentumEscalateStreak is the CONSECUTIVE granular-read streak — no
// explore / smart_context, no action tool in between — at which the
// stronger escalation note fires. 10 is the documented upper bound of a
// whole exploration phase ("one explore call replaces 5-10 calls"): a
// session that has run past it without once taking the one-shot path or
// acting is grinding, and the failure mode it is heading for is running
// out of turns with the evidence in hand but no answer emitted. Like the
// first note it fires exactly once per session.
const momentumEscalateStreak = 10

// momentumReadTools is the set of read/navigate tools that count toward the
// momentum note. Edit and verify tools never count — the note is about
// evidence-gathering loops, not about acting.
var momentumReadTools = map[string]bool{
	// Stable facade-v1 read/navigation dispatchers. These are counted by the
	// outer middleware exactly once per facade call.
	"explore": true, "search": true, "read": true, "relations": true, "trace": true,
	// Legacy read/navigation tools.
	"search_symbols": true, "search_text": true,
	"get_symbol_source": true, "batch_symbols": true, "get_file_summary": true,
	"get_editing_context": true, "read_file": true, "find_usages": true,
	"get_callers": true, "get_call_chain": true, "find_implementations": true,
	"get_dependencies": true, "get_dependents": true, "find_files": true,
}

// momentumNote is attached ONCE per session, to the response of the
// threshold-crossing read call. Generic turn-economy guidance for any
// budgeted agent: everything already returned is real and citeable, so a
// conclusion can usually be written before fetching more.
func momentumNote(n int) string {
	return fmt.Sprintf(
		"(Session note: graph read #%d. Every location returned so far is real and citeable — "+
			"if you are localizing or answering a question, consider writing your conclusion from "+
			"what you already hold and fetching only what it still lacks. This note appears once.)", n)
}

// momentumEscalationNote is the stronger, second-level note: the session
// has ground past a whole exploration phase of consecutive granular reads
// without taking the one-shot path or acting. The worst outcome for a
// budgeted session is exhausting its turns with the evidence in hand and
// no answer emitted, so the note says to answer now with the current best
// candidate. When explore has not been used this session, it is named as
// the one-call alternative.
func momentumEscalationNote(streak int, exploreUsed bool) string {
	alt := ""
	if !exploreUsed {
		alt = " If one more look is genuinely needed, a single explore call with the request in plain " +
			"words returns the ranked neighborhood — symbols, source, call paths — in one response."
	}
	return fmt.Sprintf(
		"(Session note: %d granular reads in a row. You likely have enough — answer NOW with your "+
			"current best candidate and its evidence: name the file and the symbol, citing the locations "+
			"already returned. A confident partial answer beats an exhausted turn budget with no answer.%s "+
			"This note will not repeat.)", streak, alt)
}

// maybeAttachMomentumNote tracks this session's read-tool calls and attaches
// each momentum note exactly once per session:
//
//   - level 1 (momentumNote) when the TOTAL read count crosses
//     momentumReadThreshold — the gentle "you can conclude from what you
//     hold" reminder;
//   - level 2 (momentumEscalationNote) when a CONSECUTIVE granular-read
//     streak reaches momentumEscalateStreak — explore / smart_context and
//     any non-read (action) call break the streak, so it only fires for a
//     session grinding one symbol at a time toward the turn cap.
//
// Nil-safe pass-through for error results and sessionless contexts; a
// non-read tool call resets the streak and is never decorated.
func (s *Server) maybeAttachMomentumNote(ctx context.Context, toolName string, res *mcp.CallToolResult) *mcp.CallToolResult {
	if res == nil || res.IsError {
		return res
	}
	sess := s.sessionFor(ctx)
	if sess == nil {
		return res
	}
	if !momentumReadTools[toolName] {
		// An action call (edit / verify / workflow — or the smart_context
		// one-shot, which is not a momentum read) breaks the granular
		// streak: the session is acting on evidence, not grinding for more.
		sess.mu.Lock()
		sess.momentumStreak = 0
		sess.mu.Unlock()
		return res
	}
	sess.mu.Lock()
	sess.momentumReads++
	if toolName == "explore" {
		// The one-shot path was taken: record it for the escalation
		// wording and break the granular streak.
		sess.momentumExploreUsed = true
		sess.momentumStreak = 0
	} else {
		sess.momentumStreak++
	}
	fireNudge := !sess.momentumNudged && sess.momentumReads >= momentumReadThreshold
	if fireNudge {
		sess.momentumNudged = true
	}
	fireEscalation := !sess.momentumEscalated && sess.momentumStreak >= momentumEscalateStreak
	if fireEscalation {
		sess.momentumEscalated = true
	}
	n := sess.momentumReads
	streak := sess.momentumStreak
	exploreUsed := sess.momentumExploreUsed
	sess.mu.Unlock()
	if fireNudge {
		res.Content = append(res.Content, mcp.NewTextContent(momentumNote(n)))
	}
	if fireEscalation {
		res.Content = append(res.Content, mcp.NewTextContent(momentumEscalationNote(streak, exploreUsed)))
	}
	return res
}
