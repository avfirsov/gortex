package mcp

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Delta context packing: instead of re-emitting a full smart_context
// pack when only a few symbols changed, return just what was added,
// removed, or changed relative to a pack the agent already holds. The
// agent passes the prior pack's root via delta_from; the server diffs
// the new pack against the cached prior (content-addressed by pack
// root) and returns a compact pack_delta the agent merges into its held
// context — incremental context re-delivery that saves tokens across a
// session as the working set shifts by a symbol or two.

// packSymbol is one symbol's identity within a pack, for diffing. The
// Entry (full serialized symbol) is carried only on the freshly-built
// "current" view so an added/changed symbol can be emitted in full; the
// cached prior view stores only the identity fields it needs.
type packSymbol struct {
	ID         string
	StartLine  int
	SourceHash string
	Entry      map[string]any
}

// packView is the canonical, order-insensitive view of a smart_context
// pack used for delta diffing — the same components computePackRoot
// hashes (selected symbols, files-to-edit, the blast-radius edge layer).
type packView struct {
	Symbols []packSymbol
	Files   []string
	Edges   []string // sorted edge keys ("caller|file|id", "test|file|fn")
}

// extractPackView pulls the canonical components from a smart_context
// result map. withEntries controls whether each symbol's full entry is
// retained (true for the current pack so added/changed symbols can be
// emitted whole; false for the cached prior, which only needs identity).
func extractPackView(result map[string]any, withEntries bool) packView {
	var v packView
	addSym := func(e map[string]any) {
		id, _ := e["id"].(string)
		if id == "" {
			return
		}
		body, _ := e["source"].(string)
		ps := packSymbol{ID: id, StartLine: intFromAny(e["start_line"]), SourceHash: shortHash(body)}
		if withEntries {
			ps.Entry = e
		}
		v.Symbols = append(v.Symbols, ps)
	}
	// Graded packs carry symbols in the manifest; flat packs in
	// relevant_symbols — the same precedence computePackRoot uses, so a
	// symbol is never counted twice.
	if mani, ok := result["context_manifest"].(map[string]any); ok {
		if entries, ok := mani["entries"].([]map[string]any); ok {
			for _, e := range entries {
				addSym(e)
			}
		}
	} else if syms, ok := result["relevant_symbols"].([]map[string]any); ok {
		for _, e := range syms {
			addSym(e)
		}
	}

	if list, ok := result["files_to_edit"].([]string); ok {
		v.Files = append([]string(nil), list...)
		sort.Strings(v.Files)
	}

	// Edge layer: the blast-radius caller groups and covering tests.
	if br, ok := result["blast_radius"].(map[string]any); ok {
		if groups, ok := br["callers_by_file"].([]map[string]any); ok {
			for _, g := range groups {
				file, _ := g["file"].(string)
				if ids, ok := g["callers"].([]string); ok {
					for _, id := range ids {
						v.Edges = append(v.Edges, "caller|"+file+"|"+id)
					}
				}
			}
		}
		if tests, ok := br["covering_tests"].([]map[string]any); ok {
			for _, tr := range tests {
				file, _ := tr["file"].(string)
				fn, _ := tr["function"].(string)
				v.Edges = append(v.Edges, "test|"+file+"|"+fn)
			}
		}
	}
	sort.Strings(v.Edges)
	return v
}

// diffPackViews computes the delta from a prior pack to the current one.
// Identity is by symbol ID; a symbol present in both with a different
// source hash (or start line) is "changed". Returns the pack_delta block
// shape, including token estimates so the caller can decide whether the
// delta is worth sending instead of the full pack.
func diffPackViews(prior, current packView, baseRoot, newRoot string) map[string]any {
	priorByID := make(map[string]packSymbol, len(prior.Symbols))
	for _, s := range prior.Symbols {
		priorByID[s.ID] = s
	}
	currentIDs := make(map[string]struct{}, len(current.Symbols))

	var added, changed []map[string]any
	unchanged := 0
	for _, s := range current.Symbols {
		currentIDs[s.ID] = struct{}{}
		p, ok := priorByID[s.ID]
		switch {
		case !ok:
			added = append(added, s.Entry)
		case p.SourceHash != s.SourceHash || p.StartLine != s.StartLine:
			e := s.Entry
			changed = append(changed, e)
		default:
			unchanged++
		}
	}
	var removed []string
	for _, s := range prior.Symbols {
		if _, ok := currentIDs[s.ID]; !ok {
			removed = append(removed, s.ID)
		}
	}
	sort.Strings(removed)

	addedFiles, removedFiles := diffStringSets(prior.Files, current.Files)
	addedEdgeKeys, removedEdgeKeys := diffStringSets(prior.Edges, current.Edges)

	fullTokens := estimatePackTokens(current)
	deltaTokens := estimateDeltaTokens(added, changed, len(removed), len(addedEdgeKeys)+len(removedEdgeKeys))
	savings := 0.0
	if fullTokens > 0 {
		savings = round4(1.0 - float64(deltaTokens)/float64(fullTokens))
	}

	return map[string]any{
		"base_root":       baseRoot,
		"new_root":        newRoot,
		"added":           added,
		"changed":         changed,
		"removed":         removed,
		"unchanged_count": unchanged,
		"added_files":     addedFiles,
		"removed_files":   removedFiles,
		"added_edges":     parseEdgeKeys(addedEdgeKeys),
		"removed_edges":   parseEdgeKeys(removedEdgeKeys),
		"delta_tokens":    deltaTokens,
		"full_tokens":     fullTokens,
		"savings_percent": round4(savings * 100),
		// worth_it: the delta is materially smaller than the full pack.
		// Mirrors the heuristic that section overhead isn't worth it
		// once the delta exceeds ~60% of the full pack.
		"worth_it": fullTokens > 0 && deltaTokens < (fullTokens*6)/10,
	}
}

// diffStringSets returns elements added (in cur, not prior) and removed
// (in prior, not cur). Inputs need not be sorted; outputs are sorted.
func diffStringSets(prior, cur []string) (added, removed []string) {
	pset := make(map[string]struct{}, len(prior))
	for _, s := range prior {
		pset[s] = struct{}{}
	}
	cset := make(map[string]struct{}, len(cur))
	for _, s := range cur {
		cset[s] = struct{}{}
		if _, ok := pset[s]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range prior {
		if _, ok := cset[s]; !ok {
			removed = append(removed, s)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// parseEdgeKeys turns "kind|file|id" edge keys into structured objects.
func parseEdgeKeys(keys []string) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 3)
		row := map[string]any{"kind": parts[0]}
		if len(parts) > 1 {
			row["file"] = parts[1]
		}
		if len(parts) > 2 {
			row["target"] = parts[2]
		}
		out = append(out, row)
	}
	return out
}

// estimatePackTokens approximates the token cost of a full pack from its
// serialized symbol entries (~4 chars/token) plus a small overhead.
func estimatePackTokens(v packView) int {
	tokens := 12 // top-level structure overhead
	for _, s := range v.Symbols {
		tokens += estimateEntryTokens(s.Entry)
	}
	tokens += len(v.Edges) * 5
	tokens += len(v.Files) * 6
	return tokens
}

// estimateDeltaTokens approximates the token cost of the delta encoding.
func estimateDeltaTokens(added, changed []map[string]any, removed, edges int) int {
	tokens := 16 // delta header + section markers
	for _, e := range added {
		tokens += estimateEntryTokens(e)
	}
	for _, e := range changed {
		tokens += estimateEntryTokens(e)
	}
	tokens += removed * 8 // a removed symbol is just its ID
	tokens += edges * 5
	return tokens
}

// estimateEntryTokens estimates a symbol entry's token cost. Uses the
// serialized length when available, else a default for a stub entry.
func estimateEntryTokens(e map[string]any) int {
	if e == nil {
		return 12
	}
	b, err := json.Marshal(e)
	if err != nil {
		return 24
	}
	return len(b)/4 + 1
}

func shortHash(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// packDeltaCache is a bounded LRU of pack views keyed by pack root, so a
// later smart_context call with delta_from=<root> can diff against the
// pack the agent already received. Content-addressed (the key is the
// pack root), so it is safe to share across sessions: an identical pack
// produced by two sessions resolves to the same entry.
type packDeltaCache struct {
	mu  sync.Mutex
	ll  *list.List
	m   map[string]*list.Element
	cap int
}

type packCacheEntry struct {
	root string
	view packView
}

func newPackDeltaCache() *packDeltaCache {
	return &packDeltaCache{ll: list.New(), m: make(map[string]*list.Element), cap: 32}
}

func (c *packDeltaCache) get(root string) (packView, bool) {
	if c == nil || root == "" {
		return packView{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[root]
	if !ok {
		return packView{}, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*packCacheEntry).view, true
}

func (c *packDeltaCache) put(root string, v packView) {
	if c == nil || root == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[root]; ok {
		el.Value.(*packCacheEntry).view = v
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&packCacheEntry{root: root, view: v})
	c.m[root] = el
	for c.ll.Len() > c.cap {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.m, back.Value.(*packCacheEntry).root)
	}
}
