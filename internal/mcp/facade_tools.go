package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/zzet/gortex/internal/telemetry"
)

var facadeDescriptions = map[string]string{
	"explore":         "Localize a task and return the most relevant code neighborhood.",
	"search":          "Search symbols, text, files, AST, or artifacts. Choose with operation.",
	"read":            "Read a file, symbol, symbol batch, summary, history, or editing context.",
	"relations":       "Query usages, callers, dependencies, implementations, or other symbol relations.",
	"trace":           "Trace call chains, graph paths, data flow, taint, CFG, or an expert graph query.",
	"analyze":         "Run deterministic graph analysis selected by kind.",
	"ask":             "Ask the configured research agent an open-ended codebase question.",
	"change":          "Assess a proposed or existing change without modifying source.",
	"edit":            "Apply a guarded source or file mutation. This tool writes to disk.",
	"refactor":        "Apply a semantic refactor such as rename, move, inline, delete, or code action.",
	"review":          "Build or critique a read-only code review selected by operation.",
	"publish_review":  "Publish a review to an external forge. This tool has external side effects.",
	"pr":              "Inspect pull requests, risk, impact, reviewers, triage, or conflicts.",
	"recall":          "Read session notes, durable memories, or repository notebooks.",
	"remember":        "Persist notes, memories, notebook entries, or review suppressions.",
	"workspace":       "Inspect repository, project, scope, index, graph, or proxy state.",
	"workspace_admin": "Change repository, project, index, scope, workflow, or daemon control state.",
	"session":         "Change volatile agent, planning, workflow, or proxy state for this session.",
	"overlay":         "Change only the current session's speculative overlay state.",
	"response":        "Inspect, slice, grep, or export a buffered Gortex response.",
	"capabilities":    "List public tool operations or return the exact schema for one operation.",
}

func boolPointer(v bool) *bool { return &v }

func facadeAnnotation(name string) mcpgo.ToolAnnotation {
	readOnly := true
	destructive := false
	openWorld := false
	switch name {
	case "ask", "pr", "review":
		openWorld = true
	case "edit", "refactor", "remember", "workspace_admin":
		readOnly = false
		destructive = true
		if name == "workspace_admin" {
			openWorld = true
		}
	case "overlay", "session":
		readOnly = false
	case "publish_review":
		readOnly = false
		destructive = true
		openWorld = true
	}
	return mcpgo.ToolAnnotation{
		ReadOnlyHint:    boolPointer(readOnly),
		DestructiveHint: boolPointer(destructive),
		OpenWorldHint:   boolPointer(openWorld),
	}
}

func facadeTargetProperty() mcpgo.PropertyOption {
	return mcpgo.Properties(map[string]any{
		"file":     map[string]any{"type": "string"},
		"symbol":   map[string]any{"type": "string"},
		"symbols":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"query":    map[string]any{"type": "string"},
		"artifact": map[string]any{"type": "string"},
		"repo":     map[string]any{"type": "string"},
	})
}

func facadeToolDefinition(name string) mcpgo.Tool {
	desc := facadeDescriptions[name]
	annotation := mcpgo.WithToolAnnotation(facadeAnnotation(name))
	freeObject := func(field, description string) mcpgo.ToolOption {
		return mcpgo.WithObject(field, mcpgo.Description(description), mcpgo.AdditionalProperties(true))
	}
	operation := mcpgo.WithString("operation", mcpgo.Description("Operation; omit for a selector default."))
	options := freeObject("options", "Operation-specific options validated by Gortex.")
	output := freeObject("output", "Response shaping.")
	target := mcpgo.WithObject("target", mcpgo.Description("Exactly one primary target selector."), facadeTargetProperty(), mcpgo.AdditionalProperties(false))

	var opts []mcpgo.ToolOption
	switch name {
	case "explore":
		opts = []mcpgo.ToolOption{
			operation,
			mcpgo.WithString("task", mcpgo.Description("Task, bug, or question to localize.")),
			mcpgo.WithString("path"), options, output,
		}
	case "search":
		opts = []mcpgo.ToolOption{operation, mcpgo.WithString("query"), options, output}
	case "read":
		opts = []mcpgo.ToolOption{operation, target, freeObject("context", "Read window or source-context controls."), options, output}
	case "relations", "trace":
		opts = []mcpgo.ToolOption{operation, freeObject("target", "Primary file or symbol target."), freeObject("to", "Optional destination target."), options, output}
	case "analyze":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("kind", mcpgo.Description("Analysis kind or operation; omit to list supported kinds.")),
			freeObject("target", "Optional analysis target."), options, output,
		}
	case "ask":
		opts = []mcpgo.ToolOption{mcpgo.WithString("question", mcpgo.Required()), options, output}
	case "change", "review":
		opts = []mcpgo.ToolOption{operation, freeObject("source", "Diff, working tree, ranges, symbols, or review source."), options, output}
	case "edit":
		opts = []mcpgo.ToolOption{
			operation, target, mcpgo.WithString("match"), mcpgo.WithString("replacement"),
			mcpgo.WithString("content"), freeObject("guard", "Stale-write and occurrence guards."),
			mcpgo.WithArray("changes", mcpgo.Description("Batch file or symbol edits."), mcpgo.Items(map[string]any{"type": "object", "additionalProperties": true})),
			mcpgo.WithBoolean("dry_run"), options, output,
		}
	case "refactor":
		opts = []mcpgo.ToolOption{
			operation, target, mcpgo.WithString("new_name"), mcpgo.WithString("destination"),
			mcpgo.WithBoolean("dry_run"), options, output,
		}
	case "publish_review", "pr", "recall", "remember", "workspace", "workspace_admin", "overlay", "response":
		// Cold domain facades keep only the stable discriminator plus a
		// runtime-validated payload. capabilities returns the exact operation
		// schema on demand without changing tools/list.
		opts = []mcpgo.ToolOption{operation, freeObject("arguments", "Operation arguments.")}
	case "session":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("operation", mcpgo.Description("Session operation; see capabilities. Use subscribe or unsubscribe with channel.")),
			mcpgo.WithString("channel", mcpgo.Description("daemon_health, diagnostics, graph_invalidated, stale_refs, or workspace_readiness")),
			freeObject("arguments", "Optional session arguments."),
		}
	case "capabilities":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("domain", mcpgo.Description("Public tool name; omit to list all tool domains.")),
			mcpgo.WithString("operation", mcpgo.Description("Operation name; omit to list the domain.")),
			mcpgo.WithString("detail", mcpgo.Description("summary or schema")),
		}
	default:
		opts = []mcpgo.ToolOption{operation, target, options, output}
	}
	// Response shaping is universal so the shell mirror can merge --format into
	// the same public request object for every compact tool. Common-domain cases
	// already include output above; reapplying the same property is idempotent.
	opts = append(opts, output)
	opts = append([]mcpgo.ToolOption{mcpgo.WithDescription(desc), annotation}, opts...)
	return mcpgo.NewTool(name, opts...)
}

// registerFacadeTools installs every facade name directly into the live MCP
// server. Session filtering keeps them out of legacy surfaces, while a
// facade-v1 session receives all names from its first tools/list and never
// depends on deferred promotion or tools/list_changed.
func (s *Server) registerFacadeTools() {
	for _, name := range facadeToolNames() {
		if _, alreadyLegacy := s.facades.legacy(name); alreadyLegacy {
			continue // explore/analyze/review (and ask when configured)
		}
		facade := name
		tool := facadeToolDefinition(facade)
		var handler server.ToolHandlerFunc
		if facade == "capabilities" {
			handler = s.handleCapabilities
		} else {
			handler = func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				return s.handleFacade(ctx, facade, req)
			}
		}
		// Deliberately bypass addTool/lazy routing. The per-session surface
		// filter hides these from legacy clients; facade clients need every
		// dispatcher callable immediately.
		scrubToolText(&tool)
		s.mcpServer.AddTool(tool, s.wrapControlToolHandler(handler))
	}
}

func (s *Server) wrapLegacyFacade(name string, raw server.ToolHandlerFunc) server.ToolHandlerFunc {
	if !isFacadeToolName(name) {
		return raw
	}
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		_, explicitOperation := args["operation"]
		facadeSession := s.effectiveSessionPolicy(ctx).preset == FacadeSurfaceVersion
		if !facadeSession && !explicitOperation {
			return raw(ctx, req)
		}
		if name == "analyze" {
			// Compact calls, including native dispatcher kinds, all pass through
			// the same effect split and capability lookup below.
			return s.handleFacade(ctx, name, req)
		}
		return s.handleFacade(ctx, name, req)
	}
}

func (s *Server) handleFacade(ctx context.Context, facade string, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	started := time.Now()
	operation := normalizeFacadeOperation(req.GetString("operation", ""))
	if facade == "analyze" {
		operation = requestedAnalyzeKind(req.GetArguments())
		if operation == "" {
			operation = "help"
		}
		if analyzeKindRequiresAdmin(operation) {
			result := blockedAnalyzeKindResult(operation)
			s.recordFacadeTelemetry("analyze", operation, facadeOutcomeBlocked, time.Since(started))
			return result, nil
		}
	}
	if facade == "session" && (operation == "subscribe" || operation == "unsubscribe") {
		channel := normalizeFacadeOperation(req.GetString("channel", ""))
		if !validFacadeSessionChannel(channel) {
			result := NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   fmt.Sprintf("unknown session channel %q", channel),
				Data: map[string]any{
					"operation": operation, "valid_channels": facadeSessionChannels,
				},
			})
			s.recordFacadeTelemetry("session", operation, facadeOutcomeInvalidOperation, time.Since(started))
			return result, nil
		}
		operation += "_" + channel
	}
	if operation == "" {
		operation = inferFacadeOperation(facade, req.GetArguments())
	}
	if operation == "" {
		operation = defaultFacadeOperation(facade)
	}
	var spec facadeOperationSpec
	var ok bool
	if facade == "analyze" {
		spec, ok = s.capabilityOperation(facade, operation)
	} else {
		spec, ok = s.facades.operation(facade, operation)
	}
	if !ok {
		valid := make([]string, 0)
		for _, candidate := range s.capabilityOperations(facade) {
			valid = append(valid, candidate.Operation)
		}
		result := NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf("unknown %s operation %q", facade, operation),
			Data:      map[string]any{"facade": facade, "operation": operation, "valid_operations": valid},
		})
		// Never put the caller-provided operation in telemetry. All unresolved
		// values collapse to the fixed sentinel "unknown".
		s.recordFacadeTelemetry(facade, "unknown", facadeOutcomeInvalidOperation, time.Since(started))
		return result, nil
	}
	return s.invokeFacadeSpec(ctx, req, spec)
}

func inferFacadeOperation(facade string, input map[string]any) string {
	target, _ := input["target"].(map[string]any)
	switch facade {
	case "read":
		switch {
		case facadeSelectorPresent(target["file"]):
			return "file"
		case facadeSelectorPresent(target["symbol"]):
			return "source"
		case facadeSelectorPresent(target["symbols"]):
			return "symbols"
		case facadeSelectorPresent(target["artifact"]):
			return "artifact"
		}
	case "edit":
		switch {
		case facadeSelectorPresent(input["changes"]):
			return "batch"
		case facadeSelectorPresent(target["symbol"]):
			return "symbol"
		case facadeSelectorPresent(target["file"]):
			if facadeSelectorPresent(input["content"]) && !facadeSelectorPresent(input["match"]) {
				return "write"
			}
			return "file"
		}
	}
	return ""
}

var facadeSessionChannels = []string{
	"daemon_health", "diagnostics", "graph_invalidated", "stale_refs", "workspace_readiness",
}

func validFacadeSessionChannel(channel string) bool {
	return slices.Contains(facadeSessionChannels, channel)
}

func defaultFacadeOperation(facade string) string {
	switch facade {
	case "explore":
		return "task"
	case "search":
		return "symbols"
	case "read":
		return "source"
	case "relations":
		return "usages"
	case "trace":
		return "call_chain"
	case "analyze":
		return "help"
	case "ask":
		return "research"
	case "change":
		return "contract"
	case "edit":
		return "file"
	case "refactor":
		return "rename"
	case "review":
		return "run"
	case "publish_review":
		return "post"
	case "pr":
		return "list"
	case "recall":
		return "surface"
	case "remember":
		return "memory"
	case "workspace":
		return "info"
	case "response":
		return "stats"
	default:
		return ""
	}
}

func (s *Server) invokeFacadeSpec(ctx context.Context, req mcpgo.CallToolRequest, spec facadeOperationSpec) (result *mcpgo.CallToolResult, err error) {
	started := time.Now()
	outcome := ""
	defer func() {
		if outcome == "" {
			outcome = classifyFacadeOutcome(result, err)
		}
		s.recordFacadeTelemetry(spec.Facade, spec.Operation, outcome, time.Since(started))
	}()
	legacy, ok := s.facades.legacy(spec.Legacy)
	if !ok {
		outcome = facadeOutcomeUnavailable
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf("%s.%s is unavailable in this server configuration", spec.Facade, spec.Operation),
			Data:      map[string]any{"facade": spec.Facade, "operation": spec.Operation, "legacy_tool": spec.Legacy},
		}), nil
	}
	if invalid := validateFacadeInput(spec, req.GetArguments()); invalid != nil {
		outcome = facadeOutcomeInvalidArgument
		return invalid, nil
	}
	normalized := normalizeFacadeArguments(spec, req.GetArguments())
	if spec.Facade == "analyze" && analyzeKindRequiresAdmin(normalizeFacadeOperation(fmt.Sprint(normalized["kind"]))) {
		kind := normalizeFacadeOperation(fmt.Sprint(normalized["kind"]))
		outcome = facadeOutcomeBlocked
		return blockedAnalyzeKindResult(kind), nil
	}
	if OverlayViewFromContext(ctx) == nil && !facadeLegacyManagesOwnOverlay(spec.Legacy) {
		view, viewErr := s.buildOverlayViewForCtx(ctx)
		if viewErr != nil {
			outcome = facadeOutcomeToolError
			return mcpgo.NewToolResultError(viewErr.Error()), nil
		}
		if view != nil {
			ctx = WithOverlayView(ctx, view)
		}
	}
	forwarded := req
	forwarded.Params.Name = spec.Legacy
	forwarded.Params.Arguments = normalized
	forwarded.Params.RawArguments = nil
	result, err = legacy.handler(ctx, forwarded)
	if err == nil {
		result = s.decorateFacadeFreshness(spec.Legacy, forwarded, result)
	}
	if result != nil {
		if result.Meta == nil {
			result.Meta = &mcpgo.Meta{}
		}
		if result.Meta.AdditionalFields == nil {
			result.Meta.AdditionalFields = make(map[string]any)
		}
		result.Meta.AdditionalFields["gortex_facade"] = map[string]any{
			"surface_version": FacadeSurfaceVersion,
			"facade":          spec.Facade,
			"operation":       spec.Operation,
			"canonical_tool":  spec.Legacy,
		}
	}
	return result, err
}

// requestedAnalyzeKind applies the same argument-container precedence as the
// public dispatcher before choosing an operation. This closes nested bypasses
// such as options.kind=coverage while keeping the wire shape compact.
func requestedAnalyzeKind(input map[string]any) string {
	normalized := normalizeFacadeArguments(facadeOperationSpec{
		Facade: "analyze", Legacy: "analyze", Effect: facadeEffectRead,
	}, input)
	raw, ok := normalized["kind"]
	if !ok || raw == nil {
		return ""
	}
	return normalizeFacadeOperation(fmt.Sprint(raw))
}

func blockedAnalyzeKindResult(kind string) *mcpgo.CallToolResult {
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeToolBlockedByMode,
		Message:   fmt.Sprintf("analyze(kind=%s) changes durable state; use workspace_admin(operation=%s)", kind, kind),
		Data:      map[string]any{"domain": "workspace_admin", "operation": kind},
	})
}

// decorateFacadeFreshness runs the existing legacy freshness policy after a
// facade operation has resolved to its canonical tool and normalized request.
// The outer facade middleware only sees compact names/targets (read,
// relations, target.file, ...), so applying the policy there would miss the
// legacy path/id fields the rider is deliberately keyed to.
func (s *Server) decorateFacadeFreshness(legacy string, req mcpgo.CallToolRequest, result *mcpgo.CallToolResult) *mcpgo.CallToolResult {
	if rider := s.freshnessRiderFor(legacy, req); rider != nil {
		return decorateResultWithFreshness(result, rider)
	}
	if isFreshnessListTool(legacy) {
		return s.decorateListResultWithFreshness(result)
	}
	return result
}

func facadeLegacyManagesOwnOverlay(name string) bool {
	if strings.HasPrefix(name, "overlay_") || strings.HasPrefix(name, "subscribe_") ||
		strings.HasPrefix(name, "unsubscribe_") || strings.HasPrefix(name, "proxy_") {
		return true
	}
	switch name {
	case "preview_edit", "simulate_chain", "compare_with_overlay", "compare_branches", "agent_registry", "set_planning_mode", "workflow":
		return true
	default:
		return false
	}
}

func validateFacadeInput(spec facadeOperationSpec, input map[string]any) *mcpgo.CallToolResult {
	for _, field := range []string{"arguments", "options", "source", "context", "guard", "output"} {
		value, present := input[field]
		if !present || value == nil {
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   fmt.Sprintf("%s must be an object", field),
				Data:      map[string]any{"field": field},
			})
		}
	}
	for _, field := range []string{"target", "to"} {
		if raw, present := input[field]; present && raw != nil {
			if invalid := validateFacadeSelector(field, raw); invalid != nil {
				return invalid
			}
		}
	}
	if spec.Facade == "search" {
		switch spec.Operation {
		case "symbols", "text", "completion":
			query := strings.TrimSpace(fmt.Sprint(input["query"]))
			if query == "" || query == "<nil>" {
				return NewStructuredErrorResult(StructuredError{
					ErrorCode: ErrCodeInvalidArgument,
					Message:   fmt.Sprintf("search.%s requires query", spec.Operation),
					Data:      map[string]any{"field": "query", "operation": spec.Operation},
				})
			}
		}
	}
	task, _ := input["task"].(string)
	if spec.Facade == "explore" && spec.Operation == "task" && strings.TrimSpace(task) == "" {
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: "explore.task requires task",
			Data: map[string]any{"field": "task", "operation": spec.Operation},
		})
	}
	return nil
}

func validateFacadeSelector(field string, raw any) *mcpgo.CallToolResult {
	target, ok := raw.(map[string]any)
	if !ok {
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: field + " must be an object",
			Data: map[string]any{"field": field},
		})
	}
	allowed := map[string]bool{"file": true, "symbol": true, "symbols": true, "query": true, "artifact": true, "repo": true}
	selectors := make([]string, 0, len(target))
	for key, value := range target {
		if !allowed[key] {
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown %s selector %q", field, key),
				Data: map[string]any{"field": field, "valid_selectors": []string{"file", "symbol", "symbols", "query", "artifact", "repo"}},
			})
		}
		if facadeSelectorPresent(value) {
			selectors = append(selectors, key)
		}
	}
	if len(selectors) != 1 {
		sort.Strings(selectors)
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: field + " must contain exactly one selector",
			Data: map[string]any{"field": field, "selectors": selectors},
		})
	}
	return nil
}

func facadeSelectorPresent(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	default:
		return fmt.Sprint(value) != ""
	}
}

const (
	facadeOutcomeSuccess          = "success"
	facadeOutcomeInvalidOperation = "invalid_operation"
	facadeOutcomeInvalidArgument  = "invalid_argument"
	facadeOutcomeBlocked          = "blocked"
	facadeOutcomeUnavailable      = "unavailable"
	facadeOutcomeToolError        = "tool_error"
	facadeOutcomeHandlerError     = "handler_error"
	facadeOutcomeEmptyResult      = "empty_result"
)

func facadeTelemetryDimension(spec facadeOperationSpec) string {
	return boundedFacadeTelemetryDimension(spec.Facade, spec.Operation)
}

// boundedFacadeTelemetryDimension joins fixed, low-cardinality tokens and
// deterministically folds long combinations under telemetry's 32-byte guard.
// Callers must pass registry values or fixed sentinels, never request values.
func boundedFacadeTelemetryDimension(parts ...string) string {
	dim := strings.Join(parts, ".")
	if len(dim) <= 32 {
		return dim
	}
	sum := sha256.Sum256([]byte(dim))
	return dim[:23] + "." + hex.EncodeToString(sum[:4])
}

func classifyFacadeOutcome(result *mcpgo.CallToolResult, err error) string {
	if err != nil {
		return facadeOutcomeHandlerError
	}
	if result == nil {
		return facadeOutcomeEmptyResult
	}
	if !result.IsError {
		return facadeOutcomeSuccess
	}
	body, ok := singleTextContent(result)
	if !ok {
		return facadeOutcomeToolError
	}
	var structured struct {
		ErrorCode ErrorCode `json:"error_code"`
	}
	if json.Unmarshal([]byte(body), &structured) != nil {
		return facadeOutcomeToolError
	}
	switch structured.ErrorCode {
	case ErrCodeInvalidArgument:
		return facadeOutcomeInvalidArgument
	case ErrCodeToolBlockedByMode, ErrCodeToolOutOfPhase:
		return facadeOutcomeBlocked
	case ErrCodeWorkspaceUnknown, ErrCodeProjectUnknown, ErrCodeRepoNotTracked, ErrCodeRouteUnresolved:
		return facadeOutcomeUnavailable
	default:
		return facadeOutcomeToolError
	}
}

func validFacadeOutcome(outcome string) string {
	switch outcome {
	case facadeOutcomeSuccess, facadeOutcomeInvalidOperation, facadeOutcomeInvalidArgument,
		facadeOutcomeBlocked, facadeOutcomeUnavailable, facadeOutcomeToolError,
		facadeOutcomeHandlerError, facadeOutcomeEmptyResult:
		return outcome
	default:
		return facadeOutcomeToolError
	}
}

// facadeTelemetryIdentity admits only registry-backed operations and four
// fixed capabilities buckets. This is the privacy boundary that prevents a
// caller-provided operation/domain from becoming even a hashed dimension.
func (s *Server) facadeTelemetryIdentity(facade, operation string) (string, string) {
	if !isFacadeToolName(facade) {
		return "unknown", "unknown"
	}
	if facade == "capabilities" {
		switch operation {
		case "list", "domain", "operation", "unknown":
			return facade, operation
		default:
			return facade, "unknown"
		}
	}
	if operation == "unknown" {
		return facade, operation
	}
	if _, ok := s.capabilityOperation(facade, operation); ok {
		return facade, operation
	}
	// Admin-only analyze kinds are rejected before capability dispatch, but
	// remain a fixed low-cardinality vocabulary worth measuring directly.
	if facade == "analyze" && analyzeKindRequiresAdmin(operation) {
		return facade, operation
	}
	return facade, "unknown"
}

func (s *Server) recordFacadeTelemetry(facade, operation, outcome string, elapsed time.Duration) {
	facade, operation = s.facadeTelemetryIdentity(facade, operation)
	outcome = validFacadeOutcome(outcome)
	status := "error"
	if outcome == facadeOutcomeSuccess {
		status = "ok"
	}
	s.recorder.Record("mcp_facade_call", boundedFacadeTelemetryDimension(facade, operation))
	s.recorder.Record("mcp_facade_status", boundedFacadeTelemetryDimension(facade, operation, status))
	s.recorder.Record("mcp_facade_outcome", boundedFacadeTelemetryDimension(facade, operation, outcome))
	s.recorder.Record("mcp_facade_latency", boundedFacadeTelemetryDimension(facade, operation, telemetry.BucketDuration(elapsed)))
	if outcome == facadeOutcomeInvalidOperation || outcome == facadeOutcomeInvalidArgument {
		s.recorder.Record("mcp_facade_invalid", boundedFacadeTelemetryDimension(facade, operation, string(ErrCodeInvalidArgument)))
	}
}

func normalizeFacadeArguments(spec facadeOperationSpec, input map[string]any) map[string]any {
	out := make(map[string]any)
	mergeFacadeObject(out, input["arguments"])
	mergeFacadeObject(out, input["options"])
	mergeFacadeObject(out, input["source"])
	mergeFacadeObject(out, input["context"])
	mergeFacadeObject(out, input["guard"])
	mergeFacadeObject(out, input["output"])
	for key, value := range input {
		switch key {
		case "operation", "arguments", "options", "source", "context", "guard", "output", "target", "to":
			continue
		}
		out[key] = value
	}
	if target, ok := input["target"].(map[string]any); ok {
		applyFacadeTarget(spec.Legacy, out, target)
	}
	if to, ok := input["to"].(map[string]any); ok {
		for key, value := range to {
			out["to_"+key] = value
		}
	}
	// Friendly edit aliases become the exact legacy vocabulary.
	if match, ok := out["match"]; ok {
		if spec.Legacy == "edit_symbol" {
			out["old_source"] = match
		} else {
			out["old_string"] = match
		}
		delete(out, "match")
	}
	if replacement, ok := out["replacement"]; ok {
		if spec.Legacy == "edit_symbol" {
			out["new_source"] = replacement
		} else {
			out["new_string"] = replacement
		}
		delete(out, "replacement")
	}
	normalizeFacadeAliases(spec, input, out)
	for key, value := range spec.Fixed {
		out[key] = value
	}
	return out
}

func normalizeFacadeAliases(spec facadeOperationSpec, input, out map[string]any) {
	alias := func(from, to string) {
		if value, ok := out[from]; ok {
			out[to] = value
			if from != to {
				delete(out, from)
			}
		}
	}
	jsonString := func(key string) {
		value, ok := out[key]
		if !ok {
			return
		}
		if _, already := value.(string); already {
			return
		}
		if raw, err := json.Marshal(value); err == nil {
			out[key] = string(raw)
		}
	}
	commaString := func(from, to string) {
		value, ok := out[from]
		if !ok {
			return
		}
		switch values := value.(type) {
		case []any:
			parts := make([]string, 0, len(values))
			for _, item := range values {
				parts = append(parts, fmt.Sprint(item))
			}
			out[to] = strings.Join(parts, ",")
		case []string:
			out[to] = strings.Join(values, ",")
		default:
			out[to] = value
		}
		if from != to {
			delete(out, from)
		}
	}
	flattenRange := func() {
		raw, ok := out["range"]
		if !ok {
			return
		}
		if fields, ok := raw.(map[string]any); ok {
			for _, key := range []string{"start_line", "start_char", "end_line", "end_char"} {
				if value, exists := fields[key]; exists {
					out[key] = value
				}
			}
		}
		delete(out, "range")
	}
	switch spec.Facade + "." + spec.Operation {
	case "search.ast":
		alias("query", "pattern")
	case "search.winnow":
		alias("query", "text_match")
	case "relations.declaration":
		alias("query", "use_site")
	case "edit.batch":
		alias("changes", "edits")
	case "refactor.move":
		alias("destination", "target_file")
	case "change.impact", "change.edit_plan", "change.guards", "change.tests":
		commaString("symbols", "ids")
	case "change.pattern":
		// suggest_pattern accepts one anchor. Preserve an explicit id; when the
		// public source carries a one-element symbols list, lower its first item.
		if _, exists := out["id"]; !exists {
			switch values := out["symbols"].(type) {
			case []any:
				if len(values) > 0 {
					out["id"] = fmt.Sprint(values[0])
				}
			case []string:
				if len(values) > 0 {
					out["id"] = values[0]
				}
			case string:
				out["id"] = values
			}
		}
		delete(out, "symbols")
	case "change.verify":
		jsonString("changes")
	case "change.diagnostics", "change.code_actions":
		alias("file", "path")
		flattenRange()
	case "change.ranges":
		alias("file", "path")
		flattenRange()
		jsonString("ranges")
	case "change.preview":
		jsonString("workspace_edit")
	case "change.simulate":
		jsonString("steps")
	case "change.contract":
		commaString("symbols", "symbols")
		jsonString("ranges")
		jsonString("workspace_edit")
	case "trace.flow", "trace.path":
		if source := facadeSelector(input["target"], "symbol", "query"); source != nil {
			out["source_id"] = source
		}
		if sink := facadeSelector(input["to"], "symbol", "query"); sink != nil {
			out["sink_id"] = sink
		}
		delete(out, "id")
	case "trace.taint":
		if source := facadeSelector(input["target"], "query", "symbol"); source != nil {
			out["source_pattern"] = source
		}
		if sink := facadeSelector(input["to"], "query", "symbol"); sink != nil {
			out["sink_pattern"] = sink
		}
		delete(out, "id")
	}
}

func facadeSelector(raw any, keys ...string) any {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range keys {
		if value, exists := obj[key]; exists && value != nil && fmt.Sprint(value) != "" {
			return value
		}
	}
	return nil
}

func mergeFacadeObject(dst map[string]any, raw any) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for key, value := range obj {
		dst[key] = value
	}
}

func applyFacadeTarget(legacy string, out, target map[string]any) {
	set := func(key string, value any) {
		if value != nil {
			out[key] = value
		}
	}
	if file := target["file"]; file != nil {
		key := "path"
		switch legacy {
		case "find_co_changing_symbols":
			key = "file_path"
		}
		set(key, file)
	}
	if symbol := target["symbol"]; symbol != nil {
		key := "id"
		switch legacy {
		case "check_references", "find_co_changing_symbols":
			key = "symbol_id"
		case "find_import_path":
			key = "name"
		}
		set(key, symbol)
	}
	if symbols := target["symbols"]; symbols != nil {
		if values, ok := symbols.([]any); ok {
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, fmt.Sprint(value))
			}
			set("ids", strings.Join(parts, ","))
		} else if values, ok := symbols.([]string); ok {
			set("ids", strings.Join(values, ","))
		} else {
			set("ids", symbols)
		}
	}
	if query := target["query"]; query != nil {
		set("query", query)
	}
	if artifact := target["artifact"]; artifact != nil {
		set("id", artifact)
	}
	if repo := target["repo"]; repo != nil {
		set("repo", repo)
	}
}

func (s *Server) handleCapabilities(_ context.Context, req mcpgo.CallToolRequest) (result *mcpgo.CallToolResult, err error) {
	started := time.Now()
	telemetryOperation := "list"
	outcome := ""
	defer func() {
		if outcome == "" {
			outcome = classifyFacadeOutcome(result, err)
		}
		s.recordFacadeTelemetry("capabilities", telemetryOperation, outcome, time.Since(started))
	}()
	domain := normalizeFacadeOperation(req.GetString("domain", ""))
	operation := normalizeFacadeOperation(req.GetString("operation", ""))
	detail := normalizeFacadeOperation(req.GetString("detail", "summary"))
	if domain == "" {
		domains := make([]map[string]any, 0, len(facadeToolNames()))
		for _, name := range facadeToolNames() {
			domains = append(domains, map[string]any{
				"name": name, "description": facadeDescriptions[name], "operations": len(s.capabilityOperations(name)),
			})
		}
		return mcpgo.NewToolResultJSON(map[string]any{
			"surface_version": FacadeSurfaceVersion, "domains": domains,
		})
	}
	telemetryOperation = "domain"
	if !isFacadeToolName(domain) {
		telemetryOperation = "unknown"
		outcome = facadeOutcomeInvalidOperation
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown tool domain %q", domain),
			Data: map[string]any{"valid_domains": facadeToolNames()},
		}), nil
	}
	if operation != "" {
		telemetryOperation = "operation"
		spec, ok := s.capabilityOperation(domain, operation)
		if !ok {
			telemetryOperation = "unknown"
			outcome = facadeOutcomeInvalidOperation
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown %s operation %q", domain, operation),
			}), nil
		}
		return mcpgo.NewToolResultJSON(s.facadeCapability(spec, detail == "schema"))
	}
	ops := make([]map[string]any, 0)
	for _, spec := range s.capabilityOperations(domain) {
		ops = append(ops, s.facadeCapability(spec, detail == "schema"))
	}
	return mcpgo.NewToolResultJSON(map[string]any{
		"surface_version": FacadeSurfaceVersion, "domain": domain, "operations": ops,
	})
}

// capabilityOperation includes the native analyze(kind=...) catalogue without
// duplicating every kind in the legacy-to-public migration registry. Mutating
// dispatcher kinds are available only through workspace_admin.
func (s *Server) capabilityOperation(domain, operation string) (facadeOperationSpec, bool) {
	if domain == "session" {
		switch operation {
		case "subscribe":
			return facadeOperationSpec{Facade: "session", Operation: operation, Legacy: "subscribe_diagnostics", Effect: facadeEffectSessionWrite}, true
		case "unsubscribe":
			return facadeOperationSpec{Facade: "session", Operation: operation, Legacy: "unsubscribe_diagnostics", Effect: facadeEffectSessionWrite}, true
		}
		if strings.HasPrefix(operation, "subscribe_") || strings.HasPrefix(operation, "unsubscribe_") {
			return facadeOperationSpec{}, false
		}
	}
	if spec, ok := s.facades.operation(domain, operation); ok {
		return spec, true
	}
	if domain == "analyze" && !analyzeKindRequiresAdmin(operation) && AnalyzeKindDescription(operation) != "" {
		return facadeOperationSpec{
			Facade: "analyze", Operation: operation, Legacy: "analyze", Effect: facadeEffectRead,
			Fixed: publicAnalyzeFixedArguments(operation),
		}, true
	}
	return facadeOperationSpec{}, false
}

func (s *Server) capabilityOperations(domain string) []facadeOperationSpec {
	ops := s.facades.operations(domain)
	if domain == "session" {
		public := make([]facadeOperationSpec, 0, len(ops)+2)
		for _, spec := range ops {
			if strings.HasPrefix(spec.Operation, "subscribe_") || strings.HasPrefix(spec.Operation, "unsubscribe_") {
				continue
			}
			public = append(public, spec)
		}
		public = append(public,
			facadeOperationSpec{Facade: "session", Operation: "subscribe", Legacy: "subscribe_diagnostics", Effect: facadeEffectSessionWrite},
			facadeOperationSpec{Facade: "session", Operation: "unsubscribe", Legacy: "unsubscribe_diagnostics", Effect: facadeEffectSessionWrite},
		)
		sort.Slice(public, func(i, j int) bool { return public[i].Operation < public[j].Operation })
		return public
	}
	if domain != "analyze" {
		return ops
	}
	seen := make(map[string]bool, len(ops))
	for _, spec := range ops {
		seen[spec.Operation] = true
	}
	for _, kind := range AnalyzeKinds() {
		if analyzeKindRequiresAdmin(kind) || seen[kind] {
			continue
		}
		ops = append(ops, facadeOperationSpec{
			Facade: "analyze", Operation: kind, Legacy: "analyze", Effect: facadeEffectRead,
			Fixed: publicAnalyzeFixedArguments(kind),
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Operation < ops[j].Operation })
	return ops
}

// publicAnalyzeFixedArguments keeps the read-only analyze boundary free of
// optional external effects. Explicit legacy calls retain their historical
// behavior.
func publicAnalyzeFixedArguments(kind string) map[string]any {
	fixed := map[string]any{"kind": kind}
	switch kind {
	case "concepts":
		fixed["use_llm"] = false
	case "impact":
		fixed["refresh_cochange"] = false
	case "sql_call_sites":
		fixed["materialize"] = false
	}
	return fixed
}

func (s *Server) facadeCapability(spec facadeOperationSpec, includeSchema bool) map[string]any {
	legacy, available := s.facades.legacy(spec.Legacy)
	out := map[string]any{
		"surface_version": FacadeSurfaceVersion, "domain": spec.Facade, "operation": spec.Operation,
		"effect": spec.Effect, "available": available,
	}
	if len(spec.Fixed) > 0 {
		out["fixed_arguments"] = spec.Fixed
	}
	if available {
		if spec.Facade == "analyze" && spec.Operation == "help" {
			out["summary"] = "List supported analysis kinds."
		} else if summary := AnalyzeKindDescription(spec.Operation); spec.Legacy == "analyze" && summary != "" {
			out["summary"] = summary
		} else {
			out["summary"] = firstSentence(legacy.tool.Description)
		}
		if includeSchema {
			inputSchema := any(legacy.tool.InputSchema)
			properties := legacy.tool.InputSchema.Properties
			required := legacy.tool.InputSchema.Required
			if spec.Facade == "analyze" || (spec.Facade == "workspace_admin" && spec.Legacy == "analyze") {
				inputSchema, properties, required = analyzeFacadeCapabilitySchema(spec, properties, required)
			} else if spec.Facade == "session" && (spec.Operation == "subscribe" || spec.Operation == "unsubscribe") {
				inputSchema = map[string]any{
					"type": "object",
					"properties": map[string]any{
						"channel":   map[string]any{"type": "string", "enum": facadeSessionChannels},
						"arguments": map[string]any{"type": "object", "additionalProperties": true},
					},
					"required": []string{"channel"},
				}
				properties = map[string]any{"channel": map[string]any{"type": "string"}}
				required = []string{"channel"}
			}
			out["input_schema"] = inputSchema
			out["request_shape"] = facadeRequestShape(spec, properties, required)
			if raw, err := json.Marshal(inputSchema); err == nil {
				sum := sha256.Sum256(raw)
				out["schema_hash"] = hex.EncodeToString(sum[:])
			}
		}
	}
	return out
}

// analyzeFacadeCapabilitySchema turns the legacy unified dispatcher schema
// into the public operation-specific contract. Agents see only fields relevant
// to the selected kind, fixed safety arguments disappear, and conditional
// requirements become ordinary JSON Schema requirements.
func analyzeFacadeCapabilitySchema(spec facadeOperationSpec, legacyProperties map[string]any, legacyRequired []string) (map[string]any, map[string]any, []string) {
	options := make(map[string]any)
	output := make(map[string]any)
	for field, property := range legacyProperties {
		if field == "kind" {
			continue
		}
		if _, fixed := spec.Fixed[field]; fixed {
			continue
		}
		if !analyzeFieldApplies(spec.Operation, field, property) {
			continue
		}
		switch field {
		case "format", "max_bytes", "cursor", "fields", "compact", "limit":
			output[field] = property
		default:
			options[field] = property
		}
	}

	requiredFields := append([]string(nil), analyzeRequiredFields(spec.Operation)...)
	for _, field := range legacyRequired {
		if field == "kind" {
			continue
		}
		if _, fixed := spec.Fixed[field]; fixed {
			continue
		}
		if _, available := options[field]; available && !slices.Contains(requiredFields, field) {
			requiredFields = append(requiredFields, field)
		}
	}
	if spec.Facade == "workspace_admin" {
		arguments := map[string]any{
			"type":                 "object",
			"properties":           options,
			"additionalProperties": false,
		}
		if len(requiredFields) > 0 {
			arguments["required"] = requiredFields
		}
		properties := map[string]any{
			"operation": map[string]any{"type": "string", "const": spec.Operation},
			"arguments": arguments,
		}
		if len(output) > 0 {
			properties["output"] = map[string]any{"type": "object", "properties": output, "additionalProperties": false}
		}
		return map[string]any{
			"type": "object", "properties": properties,
			"required": []string{"operation", "arguments"}, "additionalProperties": false,
		}, mergeAnalyzeSchemaProperties(options, output), requiredFields
	}

	properties := map[string]any{
		"kind": map[string]any{"type": "string", "const": spec.Operation},
		"options": map[string]any{
			"type":                 "object",
			"properties":           options,
			"additionalProperties": false,
		},
	}
	topRequired := []string{"kind"}
	if len(requiredFields) > 0 {
		properties["options"].(map[string]any)["required"] = requiredFields
		topRequired = append(topRequired, "options")
	}
	if len(output) > 0 {
		properties["output"] = map[string]any{"type": "object", "properties": output, "additionalProperties": false}
	}
	if spec.Operation == "def_use" || spec.Operation == "co_change" {
		targetProperties := map[string]any{"symbol": map[string]any{"type": "string"}}
		if spec.Operation == "co_change" {
			targetProperties["file"] = map[string]any{"type": "string"}
		}
		properties["target"] = map[string]any{
			"type": "object", "properties": targetProperties,
			"minProperties": 1, "maxProperties": 1, "additionalProperties": false,
		}
		topRequired = append(topRequired, "target")
	}
	return map[string]any{
		"type": "object", "properties": properties,
		"required": topRequired, "additionalProperties": false,
	}, mergeAnalyzeSchemaProperties(options, output), requiredFields
}

func mergeAnalyzeSchemaProperties(options, output map[string]any) map[string]any {
	merged := make(map[string]any, len(options)+len(output))
	for key, value := range options {
		merged[key] = value
	}
	for key, value := range output {
		merged[key] = value
	}
	return merged
}

func analyzeRequiredFields(kind string) []string {
	switch kind {
	case "coverage":
		return []string{"profile"}
	case "would_create_cycle":
		return []string{"from_id", "to_id"}
	default:
		return nil
	}
}

// analyzeFieldApplies filters the legacy dispatcher's annotated field list.
// Kind-specific descriptions start with one or more parenthesized kind groups;
// unannotated fields are shared. A few handlers predate complete annotations
// and are covered by the explicit additions below.
func analyzeFieldApplies(kind, field string, raw any) bool {
	if kind == "help" {
		return false
	}
	property, _ := raw.(map[string]any)
	description, _ := property["description"].(string)
	description = strings.TrimSpace(description)
	if strings.HasPrefix(description, "(") {
		matched := false
		remaining := description
		for {
			start := strings.IndexByte(remaining, '(')
			if start < 0 {
				break
			}
			remaining = remaining[start+1:]
			end := strings.IndexByte(remaining, ')')
			if end < 0 {
				break
			}
			for _, candidate := range strings.Split(remaining[:end], ",") {
				if normalizeFacadeOperation(candidate) == kind {
					matched = true
				}
			}
			remaining = strings.TrimSpace(remaining[end+1:])
		}
		if !matched {
			switch kind + "." + field {
			case "impact.ids", "impact.path_prefix", "impact.kinds", "impact.min_score", "impact.max_score", "impact.limit",
				"def_use.id", "def_use.ids":
				return true
			default:
				return false
			}
		}
	}
	return true
}

// facadeRequestShape makes capabilities actionable without teaching callers
// canonical handler names. input_schema describes the operation-specific
// fields; request_shape shows where those fields belong in the stable public
// envelope and which target selector to use.
func facadeRequestShape(spec facadeOperationSpec, properties map[string]any, required []string) map[string]any {
	args := map[string]any{"operation": spec.Operation}
	placeholder := func(key string) map[string]any { return map[string]any{key: "<" + key + ">"} }
	hasLegacyField := func(key string) bool {
		_, ok := properties[key]
		return ok
	}

	switch spec.Facade {
	case "explore":
		switch spec.Operation {
		case "task", "context":
			args["task"] = "<task>"
		case "closure":
			args["options"] = map[string]any{"files": "<file>"}
		default:
			args["options"] = map[string]any{}
		}
	case "search":
		args["query"] = "<query>"
		args["options"] = map[string]any{}
	case "read":
		switch spec.Operation {
		case "file", "editing_context", "summary":
			args["target"] = placeholder("file")
		case "symbols":
			args["target"] = map[string]any{"symbols": []string{"<symbol>"}}
		case "artifact":
			args["target"] = placeholder("artifact")
		default:
			args["target"] = placeholder("symbol")
		}
		args["options"] = map[string]any{}
	case "relations":
		if spec.Operation == "declaration" {
			args["target"] = placeholder("query")
		} else {
			args["target"] = placeholder("symbol")
		}
		args["options"] = map[string]any{}
	case "trace":
		switch spec.Operation {
		case "flow", "path":
			args["target"] = placeholder("symbol")
			args["to"] = placeholder("symbol")
		case "taint":
			args["target"] = placeholder("query")
			args["to"] = placeholder("query")
		case "graph":
			args["options"] = map[string]any{"query": "<graph query>"}
		default:
			args["target"] = placeholder("symbol")
		}
		if _, ok := args["options"]; !ok {
			args["options"] = map[string]any{}
		}
	case "analyze":
		delete(args, "operation")
		args["kind"] = spec.Operation
		args["options"] = map[string]any{}
		switch spec.Operation {
		case "citation":
			args["options"] = map[string]any{"span": "<verbatim code>", "file_path": "<file>"}
		case "co_change":
			args["target"] = placeholder("symbol")
		case "def_use":
			args["target"] = placeholder("symbol")
		case "would_create_cycle":
			args["options"] = map[string]any{"from_id": "<source symbol>", "to_id": "<target symbol>"}
		}
	case "ask":
		delete(args, "operation")
		args["question"] = "<question>"
	case "change":
		source := map[string]any{}
		switch spec.Operation {
		case "api_impact":
			source["file"] = "<file>"
		case "impact", "edit_plan", "guards", "pattern", "tests":
			source["symbols"] = []string{"<symbol>"}
		case "verify":
			source["changes"] = []map[string]any{{"symbol_id": "<symbol>", "new_signature": "<signature>"}}
		case "diagnostics", "code_actions", "ranges":
			source["file"] = "<file>"
			if spec.Operation == "ranges" {
				source["ranges"] = []map[string]any{{"file": "<file>", "start_line": 1, "end_line": 1}}
			}
		case "detect":
			source["scope"] = "unstaged"
		case "preview":
			source["workspace_edit"] = "<WorkspaceEdit JSON>"
		case "simulate":
			source["steps"] = "<WorkspaceEdit JSON array>"
		case "contract":
			source["source"] = "symbols"
			source["symbols"] = []string{"<symbol>"}
		}
		args["source"] = source
	case "review":
		args["source"] = map[string]any{}
	case "edit":
		switch spec.Operation {
		case "file":
			args["target"] = placeholder("file")
			args["match"] = "<existing text>"
			args["replacement"] = "<replacement text>"
		case "write":
			args["target"] = placeholder("file")
			args["content"] = "<file content>"
		case "symbol":
			args["target"] = placeholder("symbol")
			args["match"] = "<existing source>"
			args["replacement"] = "<replacement source>"
		case "batch":
			args["changes"] = []map[string]any{{
				"op": "edit_file", "path": "<file>",
				"old_string": "<existing text>", "new_string": "<replacement text>",
			}}
		default:
			args["options"] = map[string]any{}
		}
		if spec.Operation == "skill" {
			args["options"] = map[string]any{"directory": "<directory>"}
		}
		if hasLegacyField("dry_run") {
			args["dry_run"] = true
		}
	case "refactor":
		switch spec.Operation {
		case "fix_all", "apply_code_action":
			args["target"] = placeholder("file")
		case "rename":
			args["target"] = placeholder("symbol")
			args["new_name"] = "<new name>"
		case "move":
			args["target"] = placeholder("symbol")
			args["destination"] = "<destination file>"
		default:
			args["target"] = placeholder("symbol")
		}
		args["options"] = map[string]any{}
		if hasLegacyField("dry_run") {
			args["dry_run"] = true
		}
	case "session":
		args["arguments"] = map[string]any{}
		if spec.Operation == "subscribe" || spec.Operation == "unsubscribe" {
			args["channel"] = "<channel>"
		}
	case "capabilities":
		delete(args, "operation")
		args["domain"] = "<tool>"
	case "remember":
		args["arguments"] = map[string]any{}
		if spec.Operation == "risk_ack" {
			args["arguments"] = map[string]any{"source": "symbols", "symbols": "<symbol>"}
		}
	default:
		args["arguments"] = map[string]any{}
	}
	if spec.Facade == "workspace_admin" && spec.Operation == "coverage" {
		args["arguments"] = map[string]any{"profile": "<cover profile>"}
	}

	// Manual aliases above cover common intent-oriented and conditional fields;
	// remaining schema-required legacy fields stay operation-specific under
	// options/arguments. Handler data preconditions may still apply.
	lowered := normalizeFacadeArguments(spec, args)
	var extras map[string]any
	for _, field := range required {
		if _, fixedOrLowered := lowered[field]; fixedOrLowered {
			continue
		}
		if extras == nil {
			container := "options"
			switch spec.Facade {
			case "publish_review", "pr", "recall", "remember", "workspace", "workspace_admin", "overlay", "response", "session":
				container = "arguments"
			}
			if existing, ok := args[container].(map[string]any); ok {
				extras = existing
			} else {
				extras = map[string]any{}
				args[container] = extras
			}
		}
		extras[field] = facadeSchemaPlaceholder(field, properties[field])
	}
	return map[string]any{"tool": spec.Facade, "arguments": args}
}

func facadeSchemaPlaceholder(field string, raw any) any {
	property, _ := raw.(map[string]any)
	switch property["type"] {
	case "boolean":
		return false
	case "integer", "number":
		return 1
	case "array":
		if item, ok := property["items"]; ok {
			return []any{facadeSchemaPlaceholder(field, item)}
		}
		return []any{"<" + field + ">"}
	case "object":
		return map[string]any{}
	default:
		return "<" + field + ">"
	}
}

// applyFacadeSurface provides session-level surface negotiation. Legacy
// clients never see the new dedicated facade names. facade-v1 clients see
// exactly the 21 compact definitions, including reused names whose global
// registration still carries a legacy schema.
func (s *Server) applyFacadeSurface(ctx context.Context, tools []mcpgo.Tool) []mcpgo.Tool {
	p := s.effectiveSessionPolicy(ctx)
	if p == nil || p.preset != FacadeSurfaceVersion {
		out := tools[:0]
		for _, tool := range tools {
			if isDedicatedFacadeTool(tool.Name) {
				continue
			}
			if tool.Name == "ask" {
				if _, available := s.facades.legacy("ask"); !available {
					continue
				}
			}
			out = append(out, tool)
		}
		return out
	}
	byName := make(map[string]mcpgo.Tool, len(facadeToolNames()))
	for _, tool := range tools {
		if isFacadeToolName(tool.Name) {
			byName[tool.Name] = facadeToolDefinition(tool.Name)
		}
	}
	out := make([]mcpgo.Tool, 0, len(facadeToolNames()))
	for _, name := range facadeToolNames() {
		if tool, ok := byName[name]; ok {
			out = append(out, tool)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
