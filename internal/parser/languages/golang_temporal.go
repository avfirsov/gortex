// Temporal Go SDK call attribution.
//
// Workflows orchestrate activities through a thin set of dispatch
// helpers exposed by `go.temporal.io/sdk/workflow`:
//
//	workflow.ExecuteActivity(ctx, ActivityFn, args...)
//	workflow.ExecuteLocalActivity(ctx, ActivityFn, args...)
//	workflow.ExecuteChildWorkflow(ctx, WorkflowFn, args...)
//
// and activities / workflows enter the runtime via
// `go.temporal.io/sdk/worker`:
//
//	w.RegisterActivity(MyActivity)
//	w.RegisterActivityWithOptions(MyActivity, activity.RegisterOptions{Name: "..."})
//	w.RegisterWorkflow(MyWorkflow)
//	w.RegisterWorkflowWithOptions(MyWorkflow, workflow.RegisterOptions{Name: "..."})
//
// Tree-sitter sees `workflow.ExecuteActivity(...)` as a selector_expression
// call whose receiver text is "workflow" and method is the helper name;
// `w.RegisterActivity(...)` as a selector call whose method is the
// register helper. Neither shape resolves to anything useful through
// the normal Go call-resolution path (the target lives in an external
// SDK module). The helpers below recognise the call shapes and stamp
// dedicated `via=temporal.stub` / `via=temporal.register` placeholders
// that the resolver's ResolveTemporalCalls pass turns into edges from
// the workflow to the activity (or from one workflow to the child
// workflow) it dispatches.

package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// goTemporalDispatchKind reports whether (receiver, method) names one
// of the Temporal workflow dispatch helpers and, if so, returns the
// canonical kind ("activity" or "workflow") plus whether the call is
// the `LocalActivity` variant. Returns ok=false for everything else.
//
// We require the receiver text to be exactly "workflow" — the
// canonical SDK alias. Users who alias the import (e.g.
// `import wf "go.temporal.io/sdk/workflow"`) won't be detected, which
// matches how the existing gRPC stub detector handles SDK aliasing
// (the canonical alias dominates >99% of real-world code).
func goTemporalDispatchKind(receiver, method string) (kind string, local bool, ok bool) {
	if receiver != "workflow" {
		return "", false, false
	}
	switch method {
	case "ExecuteActivity":
		return "activity", false, true
	case "ExecuteLocalActivity":
		return "activity", true, true
	case "ExecuteChildWorkflow":
		return "workflow", false, true
	}
	return "", false, false
}

// goTemporalRegisterKind reports whether a method name is one of the
// Temporal worker registration helpers and, if so, returns the kind
// ("activity" or "workflow") being registered. The receiver isn't
// required — `RegisterActivity` is distinctive enough across the SDK
// surface that a name match has zero realistic false positives.
//
// `RegisterActivities` (plural — registers every exported method on
// a struct as an activity) is recognised too; the resolver pass will
// promote each method of the struct to a temporal activity.
func goTemporalRegisterKind(method string) (kind string, plural bool, ok bool) {
	switch method {
	case "RegisterActivity", "RegisterActivityWithOptions":
		return "activity", false, true
	case "RegisterWorkflow", "RegisterWorkflowWithOptions":
		return "workflow", false, true
	case "RegisterActivities":
		return "activity", true, true
	}
	return "", false, false
}

// goTemporalSignalQueryOutKind reports whether (receiver, method) names
// an OUTBOUND signal-send or query-call against an already-running
// workflow and, if so, returns the kind ("signal" / "query") plus the
// 1-based position of the signal/query-name argument.
//
//	workflow.SignalExternalWorkflow(ctx, wid, rid, "name", arg)  // wf -> wf
//	client.SignalWorkflow(ctx, wid, rid, "name", arg)           // svc -> wf
//	client.QueryWorkflow(ctx, wid, rid, "name", args...)        // svc -> wf
//
// SignalExternalWorkflow is gated on the canonical "workflow" receiver
// (it is a workflow-package function). SignalWorkflow / QueryWorkflow
// live on the client and are called on an arbitrary client variable, so
// — like the Register* helpers — they are matched by method name alone;
// the string-literal name gate below keeps that high-precision. There is
// deliberately no workflow.QueryWorkflow (querying is client-side) and no
// SignalExternalWorkflowAsync (SignalExternalWorkflow returns a Future).
func goTemporalSignalQueryOutKind(receiver, method string) (kind string, namePos int, ok bool) {
	switch method {
	case "SignalExternalWorkflow":
		if receiver == "workflow" {
			return "signal", 4, true
		}
	case "SignalWorkflow":
		return "signal", 4, true
	case "QueryWorkflow":
		return "query", 4, true
	}
	return "", 0, false
}

// goTemporalHandlerKind reports whether (receiver, method) names one of
// the Temporal in-workflow handler-declaration helpers and, if so,
// returns the canonical kind ("query" / "signal" / "update").
//
//	workflow.SetQueryHandler(ctx, "name", fn)
//	workflow.SetQueryHandlerWithOptions(ctx, "name", fn, opts)
//	workflow.GetSignalChannel(ctx, "name")
//	workflow.GetSignalChannelWithOptions(ctx, "name", opts)
//	workflow.SetUpdateHandler(ctx, "name", fn)
//	workflow.SetUpdateHandlerWithOptions(ctx, "name", fn, opts)
//
// These mirror the Java SDK's `@QueryMethod` / `@SignalMethod` /
// `@UpdateMethod` annotations: a workflow declares, from inside its
// body, the named query / signal / update channels it serves. As with
// the dispatch helpers we require the receiver text to be exactly the
// canonical "workflow" alias.
func goTemporalHandlerKind(receiver, method string) (kind string, ok bool) {
	if receiver != "workflow" {
		return "", false
	}
	switch method {
	case "SetQueryHandler", "SetQueryHandlerWithOptions":
		return "query", true
	case "GetSignalChannel", "GetSignalChannelWithOptions":
		return "signal", true
	case "SetUpdateHandler", "SetUpdateHandlerWithOptions":
		return "update", true
	}
	return "", false
}

// goTemporalHandlerName extracts the query / signal / update name from a
// handler-declaration call — the second positional argument (after the
// workflow.Context). Unlike dispatch names we accept ONLY a string
// literal: handler names are matched by string at runtime, so a
// non-literal (variable / selector) can't be pinned to a name here and
// is left undetected, keeping the detector high-precision. Returns ""
// when the second argument is missing or is not a string literal.
func goTemporalHandlerName(callNode *sitter.Node, src []byte) string {
	if callNode == nil || callNode.Type() != "call_expression" {
		return ""
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	count := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		count++
		if count == 2 {
			switch c.Type() {
			case "interpreted_string_literal", "raw_string_literal":
				return goTemporalNameFromExpr(c, src)
			}
			return ""
		}
	}
	return ""
}

// goTemporalDispatchArg returns the second positional argument node of a
// dispatch call (`workflow.ExecuteActivity(ctx, X, args...)` → X), or
// nil. X is either a string literal ("MyActivity"), a bare identifier
// (MyActivity), or a selector expression (pkg.MyActivity, recv.Method);
// goTemporalNameFromExpr reduces it to the trailing identifier — the
// name the worker registers under (the bare function name unless
// `RegisterActivityWithOptions` overrides it). Returned as a node, not a
// reduced name, so the env-default refinement can inspect the argument's
// shape (a bare identifier is the only case it tries to resolve to a
// literal default). Returns nil when the call has fewer than two
// positional arguments.
func goTemporalDispatchArg(callNode *sitter.Node) *sitter.Node {
	if callNode == nil || callNode.Type() != "call_expression" {
		return nil
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	count := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		count++
		if count == 2 {
			return c
		}
	}
	return nil
}

// goTemporalNthStringLiteralArg returns the unquoted value of the n-th
// (1-based) positional argument of a call when that argument is a string
// literal, else "". Used to extract the signal/query name from an
// outbound send/call — names are matched by string at runtime, so only a
// literal can be pinned here (a variable / constant is left undetected,
// keeping the detector high-precision).
func goTemporalNthStringLiteralArg(callNode *sitter.Node, n int, src []byte) string {
	if callNode == nil || callNode.Type() != "call_expression" {
		return ""
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	count := 0
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		count++
		if count == n {
			switch c.Type() {
			case "interpreted_string_literal", "raw_string_literal":
				return goTemporalNameFromExpr(c, src)
			}
			return ""
		}
	}
	return ""
}

// goTemporalCallArgNames reduces the first few positional arguments of a
// call to their "name" form so the Temporal wrapper-following resolver
// can recover a dispatch name passed through a user wrapper
// (`executeActivity(ctx, ao, "Charge", …)` / `(…, constants.Charge, …)`).
// Each element is the string-literal value, the trailing identifier of a
// selector, or a bare identifier; non-name args are "". Returns ok=false
// unless at least one argument is "dispatch-relevant" — a string literal,
// a selector expression, or a Capitalised (exported / const-like)
// identifier — which keeps the meta off the overwhelming majority of
// plain `f(x, y)` calls. Capped at the first 8 positional args.
func goTemporalCallArgNames(callNode *sitter.Node, src []byte) ([]string, bool) {
	if callNode == nil || callNode.Type() != "call_expression" {
		return nil, false
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return nil, false
	}
	const maxArgs = 8
	var out []string
	qualifying := false
	count := 0
	for i := 0; i < int(args.NamedChildCount()) && count < maxArgs; i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		count++
		name := ""
		switch c.Type() {
		case "interpreted_string_literal", "raw_string_literal":
			name = goTemporalNameFromExpr(c, src)
			qualifying = true
		case "selector_expression":
			name = goTemporalNameFromExpr(c, src)
			qualifying = true
		case "identifier":
			name = c.Content(src)
			if name != "" && name[0] >= 'A' && name[0] <= 'Z' {
				qualifying = true
			}
		}
		out = append(out, name)
	}
	if !qualifying {
		return nil, false
	}
	return out, true
}

// attachGoTemporalCallArgNames records the reduced positional argument
// names of a plain call on its EdgeCalls edge (Meta["arg_names"]), so the
// wrapper-following resolver can recover a dispatch name forwarded
// through a user wrapper. No-op when no argument is dispatch-relevant.
func attachGoTemporalCallArgNames(edge *graph.Edge, c goDeferredCall, src []byte) {
	if edge == nil {
		return
	}
	names, ok := goTemporalCallArgNames(c.callNode, src)
	if !ok {
		return
	}
	if edge.Meta == nil {
		edge.Meta = map[string]any{}
	}
	edge.Meta["arg_names"] = names
	// Record the callee name too: the wrapper-following resolver matches
	// wrappers by name, which survives even when cross-package / cross-repo
	// resolution can't land the call edge on the wrapper's node.
	callee := c.callName
	if c.isSelector {
		callee = c.method
	}
	if callee != "" {
		edge.Meta["callee"] = callee
	}
}

// goTemporalRegisterName extracts the registered function name from a
// `worker.RegisterActivity(F)` / `worker.RegisterWorkflow(F)` call —
// the first positional argument, which is the function reference.
// Same expression shapes as the dispatch-name argument.
func goTemporalRegisterName(callNode *sitter.Node, src []byte) string {
	if callNode == nil || callNode.Type() != "call_expression" {
		return ""
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		return goTemporalNameFromExpr(c, src)
	}
	return ""
}

// applyGoTemporalRegisterMeta stamps `via=temporal.register` plus
// `temporal_kind` (activity / workflow) and `temporal_name` (the
// function-reference identifier) onto an EdgeCalls edge derived from
// a Temporal worker-registration call. No-op when c.tempKind isn't
// the "register_*" form set by goTemporalRegisterKind.
//
// The resolver's ResolveTemporalCalls pass walks every edge carrying
// this meta to discover (name → registered function) pairs, then
// stamps `temporal_role` on the registered function nodes and uses
// the map to rewrite matching stub-call placeholders.
func applyGoTemporalRegisterMeta(edge *graph.Edge, c goDeferredCall) {
	if edge == nil || c.tempKind == "" || c.tempName == "" {
		return
	}
	var kind string
	switch c.tempKind {
	case "register_activity":
		kind = "activity"
	case "register_workflow":
		kind = "workflow"
	default:
		return
	}
	if edge.Meta == nil {
		edge.Meta = map[string]any{}
	}
	edge.Meta["via"] = "temporal.register"
	edge.Meta["temporal_kind"] = kind
	edge.Meta["temporal_name"] = c.tempName
}

// applyGoTemporalSignalQueryMeta stamps the outbound signal-send /
// query-call meta onto an EdgeCalls edge derived from
// `SignalExternalWorkflow` / `SignalWorkflow` / `QueryWorkflow`:
// `via=temporal.signal-send` or `temporal.query-call`, plus
// `temporal_kind` (signal / query) and `temporal_name` (the literal
// signal/query name). No-op when c.tempOutKind / c.tempName are unset.
//
// These are the consumer side of the signal/query namespaces; the
// provider side is the in-workflow handler (GetSignalChannel /
// SetQueryHandler), tagged via=temporal.handler.
func applyGoTemporalSignalQueryMeta(edge *graph.Edge, c goDeferredCall) {
	if edge == nil || c.tempOutKind == "" || c.tempName == "" {
		return
	}
	var via string
	switch c.tempOutKind {
	case "signal":
		via = "temporal.signal-send"
	case "query":
		via = "temporal.query-call"
	default:
		return
	}
	if edge.Meta == nil {
		edge.Meta = map[string]any{}
	}
	edge.Meta["via"] = via
	edge.Meta["temporal_kind"] = c.tempOutKind
	edge.Meta["temporal_name"] = c.tempName
}

// applyGoTemporalHandlerMeta stamps `via=temporal.handler` plus
// `temporal_kind` (query / signal / update) and `temporal_name` (the
// handler's string name) onto the EdgeCalls edge derived from a
// `workflow.SetQueryHandler` / `GetSignalChannel` / `SetUpdateHandler`
// call. No-op when c.tempHandlerKind / c.tempName are unset.
//
// The edge originates from the enclosing workflow function, so the
// graph records — per workflow — the named query / signal / update
// handlers it exposes, symmetric with the Java side's per-method
// `@QueryMethod` / `@SignalMethod` / `@UpdateMethod` annotation edges.
func applyGoTemporalHandlerMeta(edge *graph.Edge, c goDeferredCall) {
	if edge == nil || c.tempHandlerKind == "" || c.tempName == "" {
		return
	}
	if edge.Meta == nil {
		edge.Meta = map[string]any{}
	}
	edge.Meta["via"] = "temporal.handler"
	edge.Meta["temporal_kind"] = c.tempHandlerKind
	edge.Meta["temporal_name"] = c.tempName
}

// goTemporalNameFromExpr reduces a single argument expression to the
// trailing identifier that names the activity / workflow. Handles
// string literals (`"MyActivity"` and the Go raw-string variant),
// bare identifiers (`MyActivity`), and selector expressions
// (`pkg.MyActivity`, `a.Method`). Returns "" for any other shape
// (function literals, ternary-style expressions, etc.) — keeps the
// detector high-precision rather than guessing.
func goTemporalNameFromExpr(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		text := node.Content(src)
		if len(text) >= 2 && (text[0] == '"' || text[0] == '`') {
			return text[1 : len(text)-1]
		}
		return text
	case "identifier":
		return node.Content(src)
	case "selector_expression":
		if field := node.ChildByFieldName("field"); field != nil {
			return field.Content(src)
		}
	case "unary_expression":
		// `&MyActivity` (rare; mostly seen for struct-method registration)
		if op := node.ChildByFieldName("operand"); op != nil {
			return goTemporalNameFromExpr(op, src)
		}
	}
	return ""
}

// goTemporalFuncCallName reduces a call_expression dispatch argument
// (`workflow.ExecuteActivity(ctx, pkg.GetFooName(), …)`) to the bare name
// of the function being CALLED — the trailing identifier of the call's
// `function` field. The callee is either a selector_expression
// (`pkg.GetFooName` → "GetFooName") or a bare identifier
// (`GetFooName` → "GetFooName"). Returns "" for any other shape
// (method-value call chains, parenthesised expressions, etc.) so the
// detector stays high-precision. Unlike goTemporalNameFromExpr — which
// reduces a function-REFERENCE argument to the registered name — this
// recovers the name of a func whose RETURN VALUE supplies the dispatch
// name; the resolver later joins it to that func's const-return literal.
func goTemporalFuncCallName(node *sitter.Node, src []byte) string {
	if node == nil || node.Type() != "call_expression" {
		return ""
	}
	fn := node.ChildByFieldName("function")
	if fn == nil {
		return ""
	}
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "selector_expression":
		if field := fn.ChildByFieldName("field"); field != nil {
			return field.Content(src)
		}
	}
	return ""
}

// goFuncConstReturnLiteral returns the single string-literal a function /
// method body unconditionally returns, or ("", false) for anything else.
// The "single const return" shape is a body whose only statement is
// `return "<literal>"` — exactly the convention temporal codebases use to
// centralise activity / workflow names (`func GetFooName() string {
// return "FooActivity" }`). Anything with branching, multiple statements,
// or a non-literal return value is rejected so the resolver only trusts a
// provably-constant func value. `decl` is the function_declaration /
// method_declaration node.
func goFuncConstReturnLiteral(decl *sitter.Node, src []byte) (string, bool) {
	body := goFuncBody(decl)
	if body == nil {
		return "", false
	}
	// The Go grammar nests a function body's statements inside a
	// `statement_list` child of the `block`; descend to it when present so
	// the single-statement scan sees the real statements.
	stmts := body
	for i := 0; i < int(body.NamedChildCount()); i++ {
		if c := body.NamedChild(i); c != nil && c.Type() == "statement_list" {
			stmts = c
			break
		}
	}
	// The body must contain exactly one statement, a return_statement.
	var ret *sitter.Node
	for i := 0; i < int(stmts.NamedChildCount()); i++ {
		c := stmts.NamedChild(i)
		if c == nil {
			continue
		}
		// Tolerate comment nodes interleaved with the statement.
		if c.Type() == "comment" {
			continue
		}
		if ret != nil {
			return "", false // more than one statement → not a const-return func
		}
		ret = c
	}
	if ret == nil || ret.Type() != "return_statement" {
		return "", false
	}
	// The return must carry exactly one expression, a string literal.
	exprs := ret.NamedChild(0)
	if exprs == nil || exprs.Type() != "expression_list" {
		return "", false
	}
	if exprs.NamedChildCount() != 1 {
		return "", false
	}
	return goStringLiteralValue(exprs.NamedChild(0), src)
}

// goTemporalEnvDefaultName attempts to resolve a bare-identifier dispatch
// name to the string-literal default of an env-var-with-default
// assignment in the enclosing function. Returns the default and true for
// one of these shapes (anchored on a literal os.Getenv / os.LookupEnv
// read so the value is provably env-sourced):
//
//	name := cmp.Or(os.Getenv("KEY"), "Default")   // any call mixing an
//	                                              // os.Getenv read with a
//	                                              // string-literal arg
//	name := os.Getenv("KEY")
//	if name == "" { name = "Default" }            // (or `name, ok := os.LookupEnv(...)`
//	                                              //  followed by a literal assign)
//
// Intra-procedural and literal-only: only assignments lexically before
// the dispatch call are considered, and anything that isn't an
// os.Getenv-anchored literal default returns "", false. This is a
// deliberately narrow data-flow shortcut, not general constant
// propagation — see the speculative tier the resolver lands it at.
func goTemporalEnvDefaultName(callNode *sitter.Node, name string, src []byte) (string, bool) {
	body := goEnclosingFuncBody(callNode)
	if body == nil {
		return "", false
	}
	limit := callNode.StartByte()
	envDeclSeen := false
	var result string
	var found bool
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || found {
			return
		}
		// Only consider assignments lexically before the dispatch call.
		if (n.Type() == "short_var_declaration" || n.Type() == "assignment_statement") &&
			n.StartByte() < limit && goAssignHasTarget(n, name, src) {
			if rhs := goAssignRHSExpr(n); rhs != nil {
				if rhs.Type() == "call_expression" {
					if goIsEnvRead(rhs, src) {
						envDeclSeen = true
					} else if def, ok := goCallEnvDefaultLiteral(rhs, src); ok {
						result, found = def, true
						return
					} else if def, ok := goEnvHelperDefaultLiteral(rhs, src); ok {
						result, found = def, true
						return
					}
				} else if envDeclSeen {
					if lit, ok := goStringLiteralValue(rhs, src); ok {
						result, found = lit, true
						return
					}
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
			if found {
				return
			}
		}
	}
	walk(body)
	return result, found
}

// goEnclosingFuncBody walks up from n to the nearest function-like
// ancestor and returns its body block, or nil.
func goEnclosingFuncBody(n *sitter.Node) *sitter.Node {
	for cur := n; cur != nil; cur = cur.Parent() {
		switch cur.Type() {
		case "function_declaration", "method_declaration", "func_literal":
			return cur.ChildByFieldName("body")
		}
	}
	return nil
}

// goAssignHasTarget reports whether `name` appears among the left-hand
// targets of a short_var_declaration / assignment_statement.
func goAssignHasTarget(assign *sitter.Node, name string, src []byte) bool {
	left := assign.ChildByFieldName("left")
	if left == nil {
		return false
	}
	for i := 0; i < int(left.NamedChildCount()); i++ {
		c := left.NamedChild(i)
		if c != nil && c.Type() == "identifier" && c.Content(src) == name {
			return true
		}
	}
	return false
}

// goAssignRHSExpr returns the first right-hand expression of an
// assignment (the value for a single-target assign, or the lone call for
// a multi-return `a, b := f()`), or nil.
func goAssignRHSExpr(assign *sitter.Node) *sitter.Node {
	right := assign.ChildByFieldName("right")
	if right == nil || right.NamedChildCount() == 0 {
		return nil
	}
	return right.NamedChild(0)
}

// goIsEnvRead reports whether a call_expression is `os.Getenv(...)` or
// `os.LookupEnv(...)`.
func goIsEnvRead(call *sitter.Node, src []byte) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "selector_expression" {
		return false
	}
	op := fn.ChildByFieldName("operand")
	field := fn.ChildByFieldName("field")
	if op == nil || field == nil || op.Content(src) != "os" {
		return false
	}
	switch field.Content(src) {
	case "Getenv", "LookupEnv":
		return true
	}
	return false
}

// goCallEnvDefaultLiteral inspects a call's arguments for the
// env-or-default shape `f(os.Getenv("KEY"), "Default")`: at least one
// argument is an os.Getenv / os.LookupEnv read AND at least one is a
// string literal. Returns the last string-literal argument and true on a
// match.
func goCallEnvDefaultLiteral(call *sitter.Node, src []byte) (string, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return "", false
	}
	hasEnvRead := false
	lastLiteral := ""
	haveLiteral := false
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() == "call_expression" && goIsEnvRead(c, src) {
			hasEnvRead = true
			continue
		}
		if lit, ok := goStringLiteralValue(c, src); ok {
			lastLiteral, haveLiteral = lit, true
		}
	}
	if hasEnvRead && haveLiteral {
		return lastLiteral, true
	}
	return "", false
}

// goEnvHelperNames is the tight, deliberately small allow-list of
// project-local "read env var with a literal fallback" helper functions
// whose 2nd argument is, by near-universal convention, the default value.
// Matching is on the function/method NAME only (case-insensitive): the
// helper body almost always lives in another package and is therefore
// invisible at extract time, so we cannot prove the env-read shape from
// the call site — we recognise the well-known names instead. The set is
// intentionally narrow (precision over recall): a wrong guess mints a
// speculative edge onto the wrong activity, so we only admit names that
// unambiguously mean "env-or-default" across the ecosystems we've seen.
var goEnvHelperNames = []string{
	"GetEnvOrDefault",
	"EnvOr",
	"GetenvDefault",
	"GetEnvDefault",
}

// goEnvHelperDefaultLiteral recognises a call to a project-local
// env-or-default helper by name — `wfutils.GetEnvOrDefault(KEY, "Default")`
// or the bare `EnvOr(KEY, "Default")` — and returns the string-literal 2nd
// argument as the default. The callee name is taken from a bare identifier
// or, for a selector_expression, its trailing `field`; it is compared
// case-insensitively (strings.EqualFold) against goEnvHelperNames. Returns
// ("", false) for any non-matching name or a non-string-literal 2nd arg.
func goEnvHelperDefaultLiteral(call *sitter.Node, src []byte) (string, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return "", false
	}
	var callee string
	switch fn.Type() {
	case "identifier":
		callee = fn.Content(src)
	case "selector_expression":
		if field := fn.ChildByFieldName("field"); field != nil {
			callee = field.Content(src)
		}
	}
	if callee == "" {
		return "", false
	}
	matched := false
	for _, name := range goEnvHelperNames {
		if strings.EqualFold(callee, name) {
			matched = true
			break
		}
	}
	if !matched {
		return "", false
	}
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return "", false
	}
	return goStringLiteralValue(args.NamedChild(1), src)
}

// goStringLiteralValue returns the unquoted value of a Go string literal
// node, or ("", false) for any other node type.
func goStringLiteralValue(n *sitter.Node, src []byte) (string, bool) {
	if n == nil {
		return "", false
	}
	switch n.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		return goTemporalNameFromExpr(n, src), true
	}
	return "", false
}
