package graph

import (
	"slices"
	"sync"
	"sync/atomic"
)

// MutationReceiptToken identifies one active graph-mutation receipt. Tokens are
// store-local and opaque to callers.
type MutationReceiptToken uint64

// MutationReceipt is the exact resolution-facing delta observed between
// BeginMutationReceipt and EndMutationReceipt.
//
// Complete is false when the store saw a mutation shape it cannot describe
// exactly. Callers must fall back to a whole-graph pass in that case. A complete
// receipt with ResolutionRelevant false proves that no added definition or
// unresolved reference can change name/import resolution.
type MutationReceipt struct {
	Complete           bool     `json:"complete"`
	ResolutionRelevant bool     `json:"resolution_relevant"`
	ChangedFiles       []string `json:"changed_files,omitempty"`
	DefinitionFiles    []string `json:"definition_files,omitempty"`
	TargetNames        []string `json:"target_names,omitempty"`
	TargetIDs          []string `json:"target_ids,omitempty"`
	ImportCandidates   []string `json:"import_candidates,omitempty"`
}

// ResolutionFiles returns the deduplicated exact file frontier needed by
// Resolver.ResolveFilesAndIncoming.
func (r MutationReceipt) ResolutionFiles() []string {
	seen := make(map[string]struct{}, len(r.ChangedFiles)+len(r.DefinitionFiles))
	out := make([]string, 0, len(seen))
	for _, files := range [][]string{r.ChangedFiles, r.DefinitionFiles} {
		for _, file := range files {
			if file == "" {
				continue
			}
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			out = append(out, file)
		}
	}
	slices.Sort(out)
	return out
}

// MutationReceiptStore is an optional graph-store capability. Backends must not
// advertise it unless every resolution-relevant mutation performed while a
// receipt is active is either represented exactly or marks the receipt
// incomplete. Multiple receipts may overlap; each must observe the mutations
// made during its own lifetime independently.
type MutationReceiptStore interface {
	BeginMutationReceipt() MutationReceiptToken
	EndMutationReceipt(MutationReceiptToken) MutationReceipt
}

type mutationReceiptAccumulator struct {
	complete           bool
	resolutionRelevant bool
	changedFiles       map[string]struct{}
	definitionFiles    map[string]struct{}
	targetNames        map[string]struct{}
	targetIDs          map[string]struct{}
	importCandidates   map[string]struct{}
}

func newMutationReceiptAccumulator() *mutationReceiptAccumulator {
	return &mutationReceiptAccumulator{
		complete:         true,
		changedFiles:     make(map[string]struct{}),
		definitionFiles:  make(map[string]struct{}),
		targetNames:      make(map[string]struct{}),
		targetIDs:        make(map[string]struct{}),
		importCandidates: make(map[string]struct{}),
	}
}

func (a *mutationReceiptAccumulator) receipt() MutationReceipt {
	return MutationReceipt{
		Complete:           a.complete,
		ResolutionRelevant: a.resolutionRelevant,
		ChangedFiles:       sortedReceiptKeys(a.changedFiles),
		DefinitionFiles:    sortedReceiptKeys(a.definitionFiles),
		TargetNames:        sortedReceiptKeys(a.targetNames),
		TargetIDs:          sortedReceiptKeys(a.targetIDs),
		ImportCandidates:   sortedReceiptKeys(a.importCandidates),
	}
}

func sortedReceiptKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

// mutationReceiptState is embedded in the in-memory Graph. Keeping it separate
// makes the optional capability self-contained and avoids widening Store.
type mutationReceiptState struct {
	// activeCount keeps the overwhelmingly common no-receipt mutation path
	// lock- and allocation-free. Begin/End publish it while holding gate
	// exclusively, so a mutation that observes zero can be linearized before
	// an overlapping Begin (or after an overlapping End).
	activeCount atomic.Uint64

	// gate makes active receipt boundaries atomic with graph writes without
	// serialising writers: mutations hold a shared lock only while at least one
	// receipt is active; Begin/End take the exclusive lock while changing the
	// active window set.
	gate   sync.RWMutex
	mu     sync.Mutex
	next   MutationReceiptToken
	active map[MutationReceiptToken]*mutationReceiptAccumulator
}

// BeginMutationReceipt starts an independent mutation observation window.
func (g *Graph) BeginMutationReceipt() MutationReceiptToken {
	g.mutationReceipts.gate.Lock()
	defer g.mutationReceipts.gate.Unlock()
	g.mutationReceipts.mu.Lock()
	defer g.mutationReceipts.mu.Unlock()
	g.mutationReceipts.next++
	if g.mutationReceipts.next == 0 {
		g.mutationReceipts.next++
	}
	if g.mutationReceipts.active == nil {
		g.mutationReceipts.active = make(map[MutationReceiptToken]*mutationReceiptAccumulator)
	}
	token := g.mutationReceipts.next
	g.mutationReceipts.active[token] = newMutationReceiptAccumulator()
	g.mutationReceipts.activeCount.Store(uint64(len(g.mutationReceipts.active)))
	return token
}

// EndMutationReceipt closes one observation window. An unknown/already-ended
// token fails closed so consumers never mistake a bookkeeping error for a
// proven empty delta.
func (g *Graph) EndMutationReceipt(token MutationReceiptToken) MutationReceipt {
	g.mutationReceipts.gate.Lock()
	defer g.mutationReceipts.gate.Unlock()
	g.mutationReceipts.mu.Lock()
	defer g.mutationReceipts.mu.Unlock()
	acc := g.mutationReceipts.active[token]
	if acc == nil {
		return MutationReceipt{Complete: false}
	}
	delete(g.mutationReceipts.active, token)
	g.mutationReceipts.activeCount.Store(uint64(len(g.mutationReceipts.active)))
	return acc.receipt()
}

// beginReceiptMutation enters the receipt gate only when a window is active.
// A mutation that observes zero overlaps any concurrent Begin and is
// linearizable immediately before it; an active mutation holds the shared gate
// through recording so End cannot retire its accumulator too early.
func (g *Graph) beginReceiptMutation() bool {
	if g.mutationReceipts.activeCount.Load() == 0 {
		return false
	}
	g.mutationReceipts.gate.RLock()
	return true
}

func (g *Graph) endReceiptMutation() {
	g.mutationReceipts.gate.RUnlock()
}

func (g *Graph) recordAddedNodeForReceipts(n *Node, definition, exact bool) {
	if n == nil || g.mutationReceipts.activeCount.Load() == 0 {
		return
	}
	g.mutationReceipts.mu.Lock()
	defer g.mutationReceipts.mu.Unlock()
	for _, acc := range g.mutationReceipts.active {
		if n.ID != "" {
			acc.targetIDs[n.ID] = struct{}{}
		}
		if n.Name != "" {
			acc.targetNames[n.Name] = struct{}{}
		}
		if n.QualName != "" {
			acc.targetNames[n.QualName] = struct{}{}
		}
		if n.FilePath != "" {
			acc.changedFiles[n.FilePath] = struct{}{}
		}
		if !definition {
			continue
		}
		acc.resolutionRelevant = true
		if n.FilePath != "" {
			acc.definitionFiles[n.FilePath] = struct{}{}
		}
		if !exact || n.FilePath == "" {
			acc.complete = false
		}
	}
}

func (g *Graph) recordAddedEdgeForReceipts(e *Edge, exactFile string) {
	if e == nil || g.mutationReceipts.activeCount.Load() == 0 {
		return
	}
	g.mutationReceipts.mu.Lock()
	defer g.mutationReceipts.mu.Unlock()
	for _, acc := range g.mutationReceipts.active {
		if e.To != "" {
			acc.targetIDs[e.To] = struct{}{}
		}
		if name := UnresolvedName(e.To); name != "" {
			acc.targetNames[name] = struct{}{}
		}
		if e.Kind == EdgeImports {
			if name := UnresolvedName(e.To); name != "" {
				acc.importCandidates[name] = struct{}{}
			} else if e.To != "" {
				acc.importCandidates[e.To] = struct{}{}
			}
			if e.Alias != "" {
				acc.importCandidates[e.Alias] = struct{}{}
			}
		}
		if exactFile != "" {
			acc.changedFiles[exactFile] = struct{}{}
		}
		if !IsUnresolvedTarget(e.To) {
			continue
		}
		acc.resolutionRelevant = true
		if exactFile == "" {
			acc.complete = false
		}
	}
}

func (g *Graph) markMutationReceiptsIncomplete() {
	if g.mutationReceipts.activeCount.Load() == 0 {
		return
	}
	g.mutationReceipts.mu.Lock()
	defer g.mutationReceipts.mu.Unlock()
	for _, acc := range g.mutationReceipts.active {
		acc.complete = false
	}
}

var _ MutationReceiptStore = (*Graph)(nil)
