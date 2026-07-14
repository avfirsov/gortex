package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// Step is one node in a discovered execution flow. Depth preserves the
// call-tree shape so the UI can render branches instead of flattening
// siblings into a false sequence: traceForward emits DFS preorder, and
// the parent of a step is the nearest preceding step with a smaller
// depth. Sibling order in the slice is the child-declaration order of
// the parent function.
type Step struct {
	ID    string `json:"id"`
	Depth int    `json:"depth"`
}

const (
	defaultMaxProcesses         = 50
	defaultMaxProcessDepth      = 15
	defaultMaxStepsPerProcess   = 2048
	defaultMaxTotalProcessSteps = 16384
)

// ProcessLimits bounds both the CPU work and retained result size of process
// discovery. Zero values select the safe defaults.
type ProcessLimits struct {
	MaxProcesses       int
	MaxDepth           int
	MaxStepsPerProcess int
	MaxTotalSteps      int
}

func defaultProcessLimits() ProcessLimits {
	return ProcessLimits{
		MaxProcesses:       defaultMaxProcesses,
		MaxDepth:           defaultMaxProcessDepth,
		MaxStepsPerProcess: defaultMaxStepsPerProcess,
		MaxTotalSteps:      defaultMaxTotalProcessSteps,
	}
}

func (l ProcessLimits) normalized() ProcessLimits {
	d := defaultProcessLimits()
	if l.MaxProcesses > 0 {
		d.MaxProcesses = l.MaxProcesses
	}
	if l.MaxDepth > 0 {
		d.MaxDepth = l.MaxDepth
	}
	if l.MaxStepsPerProcess > 0 {
		d.MaxStepsPerProcess = l.MaxStepsPerProcess
	}
	if l.MaxTotalSteps > 0 {
		d.MaxTotalSteps = l.MaxTotalSteps
	}
	return d
}

// Process represents a discovered execution flow in the codebase.
type Process struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`        // human-readable name
	EntryPoint string   `json:"entry_point"` // node ID of the entry function
	Steps      []Step   `json:"steps"`       // bounded DFS preorder with call-tree depth
	StepCount  int      `json:"step_count"`
	Files      []string `json:"files"` // unique files touched
	Score      float64  `json:"score"` // entry point confidence score
	Truncated  bool     `json:"truncated,omitempty"`
}

// ProcessResult is the output of process discovery.
type ProcessResult struct {
	Processes        []Process           `json:"processes"`
	NodeToProcs      map[string][]string `json:"node_to_processes"` // nodeID → process IDs
	Truncated        bool                `json:"truncated,omitempty"`
	TruncationReason string              `json:"truncation_reason,omitempty"`
}

// DiscoverProcesses finds execution flows using fixed, conservative limits so
// a connected multi-repository graph cannot leave O(processes*nodes) retained
// in a long-lived daemon.
func DiscoverProcesses(g graph.Store) *ProcessResult {
	return DiscoverProcessesWithLimits(g, defaultProcessLimits())
}

// DiscoverProcessesWithLimits is DiscoverProcesses with explicit bounds. It is
// primarily useful to tests and specialized callers that need a smaller result.
func DiscoverProcessesWithLimits(g graph.Store, limits ProcessLimits) *ProcessResult {
	return discoverProcesses(g.AllNodes(), graph.EdgesForKindsLight(g, graph.EdgeCalls), limits)
}

func discoverProcesses(nodes []*graph.Node, edges []*graph.Edge, limits ProcessLimits) *ProcessResult {
	limits = limits.normalized()

	callees := make(map[string][]string)
	callers := make(map[string][]string)
	for _, e := range edges {
		if e != nil && e.Kind == graph.EdgeCalls {
			callees[e.From] = append(callees[e.From], e.To)
			callers[e.To] = append(callers[e.To], e.From)
		}
	}
	// SQLite and in-memory stores do not promise edge scan order. Stable child
	// order makes a bounded prefix reproducible across runs and backends.
	for id := range callees {
		sort.Strings(callees[id])
	}

	type scored struct {
		node  *graph.Node
		score float64
	}
	var candidates []scored
	nodeMap := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		nodeMap[n.ID] = n
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		score := scoreEntryPoint(n, len(callees[n.ID]), len(callers[n.ID]))
		if score > 0.5 {
			candidates = append(candidates, scored{node: n, score: score})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].node.ID < candidates[j].node.ID
		}
		return candidates[i].score > candidates[j].score
	})

	result := &ProcessResult{NodeToProcs: make(map[string][]string)}
	seen := make(map[string]bool)
	totalSteps := 0
	stepLimited := false
	processLimited := false
	for i, c := range candidates {
		if i >= limits.MaxProcesses {
			processLimited = true
			break
		}
		if seen[c.node.ID] {
			continue
		}
		remaining := limits.MaxTotalSteps - totalSteps
		if remaining < 2 {
			stepLimited = true
			break
		}
		stepCap := limits.MaxStepsPerProcess
		if remaining < stepCap {
			stepCap = remaining
		}
		steps, truncated := traceForwardBounded(c.node.ID, callees, limits.MaxDepth, stepCap)
		if len(steps) < 2 {
			continue
		}
		seen[c.node.ID] = true
		totalSteps += len(steps)
		if truncated {
			stepLimited = true
		}

		fileSet := make(map[string]bool)
		for _, step := range steps {
			if n, ok := nodeMap[step.ID]; ok && n.FilePath != "" {
				fileSet[n.FilePath] = true
			}
		}
		files := make([]string, 0, len(fileSet))
		for file := range fileSet {
			files = append(files, file)
		}
		sort.Strings(files)

		procID := fmt.Sprintf("process-%d", len(result.Processes))
		result.Processes = append(result.Processes, Process{
			ID:         procID,
			Name:       inferProcessName(c.node),
			EntryPoint: c.node.ID,
			Steps:      steps,
			StepCount:  len(steps),
			Files:      files,
			Score:      c.score,
			Truncated:  truncated,
		})
		for _, step := range steps {
			result.NodeToProcs[step.ID] = append(result.NodeToProcs[step.ID], procID)
		}
	}

	result.Truncated = stepLimited || processLimited
	switch {
	case stepLimited && processLimited:
		result.TruncationReason = "step_limit,process_limit"
	case stepLimited:
		result.TruncationReason = "step_limit"
	case processLimited:
		result.TruncationReason = "process_limit"
	}
	return result
}

func scoreEntryPoint(n *graph.Node, calleeCount, callerCount int) float64 {
	if calleeCount == 0 {
		return 0 // leaf functions are not entry points
	}

	base := float64(calleeCount) / (float64(callerCount) + 1.0)
	nameMult := namePatternMultiplier(n.Name, n.Language)
	exportMult := 1.0
	if isExportedForProcess(n) {
		exportMult = 1.5
	}
	callerMult := 1.0
	if callerCount == 0 {
		callerMult = 2.0
	} else if callerCount <= 2 {
		callerMult = 1.3
	}
	entryMult := 1.0
	if ep, _ := n.Meta["entry_point"].(bool); ep {
		if kind, _ := n.Meta["entry_point_kind"].(string); !strings.HasPrefix(kind, "junit:") {
			entryMult = 2.0
		}
	}
	return base * nameMult * exportMult * callerMult * entryMult
}

// isExportedForProcess mirrors the dead-code visibility logic.
func isExportedForProcess(n *graph.Node) bool {
	if n.Language == "java" {
		if v, ok := n.Meta["visibility"].(string); ok && v != "" {
			return v == "public" || v == "protected"
		}
	}
	return isExported(n.Name, n.Language)
}

func namePatternMultiplier(name, lang string) float64 {
	lower := strings.ToLower(name)
	entryPatterns := []string{
		"main", "init", "run", "start", "serve", "listen",
		"handle", "handler", "controller", "middleware",
		"route", "endpoint", "dispatch",
	}
	for _, p := range entryPatterns {
		if strings.HasPrefix(lower, p) || strings.HasSuffix(lower, p) {
			return 1.5
		}
	}
	if lang == "go" {
		if strings.HasPrefix(name, "New") || strings.HasPrefix(name, "Serve") {
			return 1.3
		}
		if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") {
			return 0.3
		}
	}
	utilPatterns := []string{
		"get", "set", "is", "has", "to", "from", "parse",
		"format", "validate", "helper", "util", "string",
	}
	for _, p := range utilPatterns {
		if strings.HasPrefix(lower, p) {
			return 0.5
		}
	}
	return 1.0
}

func isExported(name, lang string) bool {
	if lang == "go" {
		return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
	}
	return !strings.HasPrefix(name, "_")
}

func traceForwardBounded(startID string, callees map[string][]string, maxDepth, maxSteps int) ([]Step, bool) {
	if maxSteps <= 0 {
		return nil, true
	}
	result := make([]Step, 0, min(maxSteps, 64))
	visited := make(map[string]bool, min(maxSteps, 64))
	truncated := false

	var dfs func(id string, depth int)
	dfs = func(id string, depth int) {
		if truncated || visited[id] || depth > maxDepth {
			return
		}
		if len(result) >= maxSteps {
			truncated = true
			return
		}
		visited[id] = true
		result = append(result, Step{ID: id, Depth: depth})
		for _, callee := range callees[id] {
			if visited[callee] {
				continue
			}
			if len(result) >= maxSteps {
				truncated = true
				return
			}
			dfs(callee, depth+1)
			if truncated {
				return
			}
		}
	}
	dfs(startID, 0)
	return result, truncated
}

func inferProcessName(n *graph.Node) string {
	name := n.Name
	lower := strings.ToLower(name)

	// Try to extract a descriptive name
	if lower == "main" {
		return "main execution"
	}
	if strings.HasPrefix(lower, "handle") {
		subject := strings.TrimPrefix(name, "Handle")
		subject = strings.TrimPrefix(subject, "handle")
		if subject != "" {
			return strings.ToLower(subject[:1]) + subject[1:] + " handling"
		}
	}
	if strings.HasPrefix(lower, "serve") {
		return name + " flow"
	}
	if strings.HasPrefix(name, "New") {
		return strings.TrimPrefix(name, "New") + " initialization"
	}
	if strings.HasPrefix(name, "Test") {
		return name
	}

	return name + " flow"
}
