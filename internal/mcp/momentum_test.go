package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func momentumTextOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestMomentumNoteFiresOnceAtThreshold(t *testing.T) {
	s := &Server{session: &sessionState{}}
	ctx := WithSessionID(context.Background(), "sess_momentum")

	for i := 1; i < momentumReadThreshold; i++ {
		res := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultText("ok"))
		if strings.Contains(momentumTextOf(res), "Session note") {
			t.Fatalf("note fired early at read %d", i)
		}
	}
	res := s.maybeAttachMomentumNote(ctx, "search_symbols", mcp.NewToolResultText("ok"))
	if !strings.Contains(momentumTextOf(res), "Session note") {
		t.Fatalf("note did not fire at threshold read %d", momentumReadThreshold)
	}
	// One-shot: never again in the same session.
	res = s.maybeAttachMomentumNote(ctx, "read_file", mcp.NewToolResultText("ok"))
	if strings.Contains(momentumTextOf(res), "Session note") {
		t.Fatal("note fired twice in one session")
	}
}

func TestMomentumFacadeReadToolsCountOnce(t *testing.T) {
	for _, tool := range []string{"search", "read", "relations", "trace"} {
		s := &Server{session: &sessionState{}}
		ctx := WithSessionID(context.Background(), "sess_facade_"+tool)
		for i := 1; i < momentumReadThreshold; i++ {
			res := s.maybeAttachMomentumNote(ctx, tool, mcp.NewToolResultText("ok"))
			if strings.Contains(momentumTextOf(res), "Session note") {
				t.Fatalf("%s facade note fired early at read %d", tool, i)
			}
		}
		res := s.maybeAttachMomentumNote(ctx, tool, mcp.NewToolResultText("ok"))
		if !strings.Contains(momentumTextOf(res), "Session note") {
			t.Fatalf("%s facade note did not fire at threshold", tool)
		}
	}
}

func TestMomentumNoteIgnoresNonReadAndErrors(t *testing.T) {
	s := &Server{}
	ctx := WithSessionID(context.Background(), "sess_momentum2")
	for i := 0; i < momentumReadThreshold*2; i++ {
		// Edit tools never count.
		res := s.maybeAttachMomentumNote(ctx, "edit_file", mcp.NewToolResultText("ok"))
		if strings.Contains(momentumTextOf(res), "Session note") {
			t.Fatal("note fired for a non-read tool")
		}
		// Error results never count and are never decorated.
		errRes := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultError("boom"))
		if strings.Contains(momentumTextOf(errRes), "Session note") {
			t.Fatal("note fired on an error result")
		}
	}
}

// escalationMarker is the load-bearing substring of the level-2 note —
// distinct from the level-1 note's text so the tests can tell them apart.
const escalationMarker = "answer NOW"

func TestMomentumEscalationFiresOnceOnGranularStreak(t *testing.T) {
	s := &Server{session: &sessionState{}}
	ctx := WithSessionID(context.Background(), "sess_escalate")

	for i := 1; i < momentumEscalateStreak; i++ {
		res := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultText("ok"))
		if strings.Contains(momentumTextOf(res), escalationMarker) {
			t.Fatalf("escalation fired early at streak %d", i)
		}
	}
	res := s.maybeAttachMomentumNote(ctx, "get_callers", mcp.NewToolResultText("ok"))
	text := momentumTextOf(res)
	if !strings.Contains(text, escalationMarker) {
		t.Fatalf("escalation did not fire at streak %d: %q", momentumEscalateStreak, text)
	}
	// Wording: names the deliverable and, explore being unused, the one-call
	// alternative.
	if !strings.Contains(text, "file and the symbol") {
		t.Errorf("escalation must ask for file+symbol: %q", text)
	}
	if !strings.Contains(text, "explore") {
		t.Errorf("escalation must name explore when it was never used: %q", text)
	}
	// One-shot: a continuing streak never re-fires it.
	res = s.maybeAttachMomentumNote(ctx, "read_file", mcp.NewToolResultText("ok"))
	if strings.Contains(momentumTextOf(res), escalationMarker) {
		t.Fatal("escalation fired twice in one session")
	}
}

func TestMomentumEscalationOmitsExploreWhenAlreadyUsed(t *testing.T) {
	s := &Server{session: &sessionState{}}
	ctx := WithSessionID(context.Background(), "sess_escalate_used")

	// explore ran once; the session then grinds a full granular streak.
	s.maybeAttachMomentumNote(ctx, "explore", mcp.NewToolResultText("ok"))
	var text string
	for i := 0; i < momentumEscalateStreak; i++ {
		res := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultText("ok"))
		text = momentumTextOf(res)
	}
	if !strings.Contains(text, escalationMarker) {
		t.Fatalf("escalation did not fire after a full post-explore streak: %q", text)
	}
	if strings.Contains(text, "explore") {
		t.Errorf("escalation must not re-suggest explore after it was used: %q", text)
	}
}

func TestMomentumEscalationNoFireOnHealthySessions(t *testing.T) {
	// explore (or any action call) breaks the streak, so a session that
	// interleaves one-shot orientation or edits with short read runs is
	// never escalated at.
	for name, breaker := range map[string]string{"explore": "explore", "action": "edit_file"} {
		s := &Server{session: &sessionState{}}
		ctx := WithSessionID(context.Background(), "sess_healthy_"+name)
		for round := 0; round < 3; round++ {
			for i := 0; i < momentumEscalateStreak-1; i++ {
				res := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultText("ok"))
				if strings.Contains(momentumTextOf(res), escalationMarker) {
					t.Fatalf("[%s] escalation fired on a healthy session (round %d read %d)", name, round, i)
				}
			}
			res := s.maybeAttachMomentumNote(ctx, breaker, mcp.NewToolResultText("ok"))
			if strings.Contains(momentumTextOf(res), escalationMarker) {
				t.Fatalf("[%s] escalation fired on the streak-breaking call", name)
			}
		}
	}
}
