package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/server/hub"
)

// activityBuffer holds the last N graph-change events so the UI can
// backfill its activity feed without waiting for a fresh event.
//
// The buffer is intentionally small (default 100) — it is meant for
// "what just happened" feedback in the dashboard, not durable history.
// Events are preserved across reconnects but are lost on server restart.
type activityBuffer struct {
	mu     sync.RWMutex
	events []indexer.GraphChangeEvent
	cap    int
}

func newActivityBuffer(cap int) *activityBuffer {
	if cap <= 0 {
		cap = 100
	}
	return &activityBuffer{cap: cap, events: make([]indexer.GraphChangeEvent, 0, cap)}
}

func (b *activityBuffer) add(ev indexer.GraphChangeEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, ev)
	if len(b.events) > b.cap {
		// Drop oldest. We want the *last* cap events.
		b.events = b.events[len(b.events)-b.cap:]
	}
}

func (b *activityBuffer) snapshot(limit int) []indexer.GraphChangeEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.events) {
		limit = len(b.events)
	}
	out := make([]indexer.GraphChangeEvent, limit)
	// Reverse-chronological so newest is first.
	for i := 0; i < limit; i++ {
		out[i] = b.events[len(b.events)-1-i]
	}
	return out
}

// startActivityCollector subscribes to the hub and streams events into
// the activity buffer. The goroutine exits when the hub is stopped
// (subscribe channel closes).
func (h *Handler) startActivityCollector(eh *hub.Hub) {
	if eh == nil || h.activity == nil {
		return
	}
	subID, ch := eh.Subscribe()
	go func() {
		defer eh.Unsubscribe(subID)
		for ev := range ch {
			h.activity.add(ev)
		}
	}()
}

// --- /v1/activity ---

func (h *Handler) handleActivity(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if h.activity == nil {
		WriteJSON(w, http.StatusOK, map[string]any{"events": []any{}})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"events": h.activity.snapshot(limit)})
}

// --- /v1/caveats ---
//
// Aggregates findings from analyze (dead_code / hotspots / cycles) and
// check_guards into a single severity-ranked list. Each entry follows
// the shape used by the dashboard caveats card and the Caveats page.

type caveatEntry struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // risk | hot | cycle | unowned | deprecated | boundary
	Symbol   string `json:"symbol"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Owner    string `json:"owner"`
	Age      string `json:"age"`
}

func (h *Handler) handleCaveats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]caveatEntry, 0, 32)

	// 1. Hotspots → severity:hot
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "hotspots", "limit": 20}); raw != "" {
		out = append(out, parseHotspots(raw)...)
	}

	// 2. Dead code → severity:deprecated (likely stale / removable)
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "dead_code", "limit": 20}); raw != "" {
		out = append(out, parseDeadCode(raw)...)
	}

	// 3. Cycles → severity:cycle
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "cycles", "limit": 20}); raw != "" {
		out = append(out, parseCycles(raw)...)
	}

	// 4. Guard violations → severity:boundary / risk
	if raw := h.CallTool(ctx, "check_guards", map[string]any{}); raw != "" {
		out = append(out, parseGuards(raw)...)
	}

	// Order: risk → hot → cycle → boundary → unowned → deprecated.
	severityRank := map[string]int{
		"risk":       0,
		"hot":        1,
		"cycle":      2,
		"boundary":   3,
		"unowned":    4,
		"deprecated": 5,
	}
	sortByRank(out, severityRank)
	WriteJSON(w, http.StatusOK, map[string]any{"caveats": out})
}

func sortByRank(in []caveatEntry, rank map[string]int) {
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && rank[in[j-1].Severity] > rank[in[j].Severity] {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
}

// parseHotspots accepts either GCX text or JSON output from analyze.
// Falls back to empty when the format isn't recognised so the endpoint
// stays robust as analyze evolves.
func parseHotspots(raw string) []caveatEntry {
	type hotspot struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		FanIn int    `json:"fan_in"`
	}
	type wrap struct {
		Hotspots []hotspot `json:"hotspots"`
	}
	var w wrap
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w.Hotspots) == 0 {
		return nil
	}
	out := make([]caveatEntry, 0, len(w.Hotspots))
	for i, h := range w.Hotspots {
		if i >= 10 {
			break
		}
		out = append(out, caveatEntry{
			ID:       "hs-" + h.ID,
			Severity: "hot",
			Symbol:   h.ID,
			Title:    "Hot path · " + h.Name,
			Desc:     "Fan-in " + strconv.Itoa(h.FanIn) + " — touched by many call sites.",
			Owner:    "",
			Age:      "ongoing",
		})
	}
	return out
}

func parseDeadCode(raw string) []caveatEntry {
	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type wrap struct {
		DeadCode []entry `json:"dead_code"`
	}
	var w wrap
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w.DeadCode) == 0 {
		return nil
	}
	out := make([]caveatEntry, 0, len(w.DeadCode))
	for i, e := range w.DeadCode {
		if i >= 10 {
			break
		}
		out = append(out, caveatEntry{
			ID:       "dc-" + e.ID,
			Severity: "deprecated",
			Symbol:   e.ID,
			Title:    "Likely unreachable · " + e.Name,
			Desc:     "No incoming references in the indexed graph.",
		})
	}
	return out
}

func parseCycles(raw string) []caveatEntry {
	type cycle struct {
		ID      string   `json:"id"`
		Members []string `json:"members"`
	}
	type wrap struct {
		Cycles []cycle `json:"cycles"`
	}
	var w wrap
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w.Cycles) == 0 {
		return nil
	}
	out := make([]caveatEntry, 0, len(w.Cycles))
	for i, c := range w.Cycles {
		if i >= 10 {
			break
		}
		title := "Circular dependency"
		if len(c.Members) > 0 {
			title = "Cycle: " + c.Members[0]
		}
		desc := strconv.Itoa(len(c.Members)) + " symbols form a cycle."
		out = append(out, caveatEntry{
			ID:       "cy-" + c.ID,
			Severity: "cycle",
			Symbol:   firstOr(c.Members, c.ID),
			Title:    title,
			Desc:     desc,
		})
	}
	return out
}

func parseGuards(raw string) []caveatEntry {
	type violation struct {
		Rule    string `json:"rule"`
		Kind    string `json:"kind"`
		Symbol  string `json:"symbol"`
		Message string `json:"message"`
	}
	type wrap struct {
		Violations []violation `json:"violations"`
	}
	var w wrap
	if err := json.Unmarshal([]byte(raw), &w); err != nil || len(w.Violations) == 0 {
		return nil
	}
	out := make([]caveatEntry, 0, len(w.Violations))
	for i, v := range w.Violations {
		if i >= 10 {
			break
		}
		sev := "boundary"
		switch v.Kind {
		case "ownership":
			sev = "unowned"
		case "cycle":
			sev = "cycle"
		case "co-change", "contract":
			sev = "boundary"
		default:
			sev = "risk"
		}
		out = append(out, caveatEntry{
			ID:       "g-" + v.Rule,
			Severity: sev,
			Symbol:   v.Symbol,
			Title:    "Guard violated: " + v.Rule,
			Desc:     v.Message,
		})
	}
	return out
}

func firstOr(s []string, fallback string) string {
	if len(s) > 0 {
		return s[0]
	}
	return fallback
}

// dashboardSnapshot bundles the most-requested top-of-page numbers in a
// single call so the dashboard can render in one round-trip.
type dashboardSnapshot struct {
	Stats struct {
		TotalNodes int            `json:"total_nodes"`
		TotalEdges int            `json:"total_edges"`
		ByKind     map[string]int `json:"by_kind"`
		ByLanguage map[string]int `json:"by_language"`
	} `json:"stats"`
	Caveats  int `json:"caveats"`
	Activity int `json:"activity"`
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats := h.graph.Stats()
	snap := dashboardSnapshot{}
	snap.Stats.TotalNodes = stats.TotalNodes
	snap.Stats.TotalEdges = stats.TotalEdges
	snap.Stats.ByKind = stats.ByKind
	snap.Stats.ByLanguage = stats.ByLanguage
	if h.activity != nil {
		snap.Activity = len(h.activity.snapshot(0))
	}
	// Caveats count is a cheap derived signal — we run analyze
	// hotspots+dead_code+cycles synchronously; if any tool is absent
	// (e.g. server started without those handlers), it contributes 0.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	count := 0
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "hotspots", "limit": 50}); raw != "" {
		count += jsonArrayLen(raw, "hotspots")
	}
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "dead_code", "limit": 50}); raw != "" {
		count += jsonArrayLen(raw, "dead_code")
	}
	if raw := h.CallTool(ctx, "analyze", map[string]any{"kind": "cycles", "limit": 50}); raw != "" {
		count += jsonArrayLen(raw, "cycles")
	}
	snap.Caveats = count
	WriteJSON(w, http.StatusOK, snap)
}

func jsonArrayLen(raw, key string) int {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(v, &arr); err != nil {
		return 0
	}
	return len(arr)
}
