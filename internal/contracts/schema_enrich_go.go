package contracts

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// -----------------------------------------------------------------------------
// Go — net/http (stdlib) provider
// -----------------------------------------------------------------------------

// Matches:
//
//	json.NewDecoder(r.Body).Decode(&req)
//	jsoniter.NewDecoder(r.Body).Decode(&req)
//	decoder.Decode(&req)
//
// The capture is the target variable; the detector then looks up its
// type inside the handler body or the file-scoped node list.
var goStdlibDecodeRe = regexp.MustCompile(`(?:json|jsoniter|decoder)\.?(?:NewDecoder)?\([^)]*\.Body\)?\.Decode\(\s*&?(\w+)\s*\)`)

// Matches:
//
//	json.Unmarshal(body, &req)  |  json.Unmarshal(data, req)
var goUnmarshalRe = regexp.MustCompile(`json\.Unmarshal\([^,]+,\s*&?(\w+)\s*\)`)

// Matches provider-side response encoders:
//
//	json.NewEncoder(w).Encode(resp)
//	WriteJSON(w, status, resp)
var goStdlibEncodeRe = regexp.MustCompile(`json\.NewEncoder\([^)]+\)\.Encode\(\s*&?(\w+)\s*\)`)

// JSON response helpers. Custom wrappers are the norm in handwritten
// Go servers — `respondJSON`, `writeJSON`, `sendJSON`, `renderJSON`,
// `h.json`, `render.JSON` all converge on the same (w, code, value)
// shape. Matching any of these gets us the status code and response
// value expression in one pass. The name capture is case-insensitive
// only for the leading letter so `WriteJSON` still matches.
var goWriteJSONRe = regexp.MustCompile(`\b(?:[A-Za-z_]\w*\.)?(?:[Rr]espond|[Ww]rite|[Ss]end|[Rr]ender)(?:JSON|Json)\(\s*\w+\s*,\s*([^,]+?)\s*,\s*([^)]+?)\s*\)`)

// (Envelope splitting now uses splitMapLiteralBody — a brace/bracket-
// balanced byte walker — rather than a regex. The regex form
// truncated nested literals like `[]any{evt1, evt2}` at the first
// `}` and produced unusable expr values like `"[]any{"`.)

// r.URL.Query().Get("x"), r.FormValue("x"), r.PostFormValue("x").
var goQueryParamRe = regexp.MustCompile(`\b(?:URL\.Query\(\)\.Get|FormValue|PostFormValue)\(\s*["` + "`" + `]([^"` + "`" + `]+)["` + "`" + `]\s*\)`)

// w.WriteHeader(<expr>) — literal int or http.StatusX.
var goWriteHeaderRe = regexp.MustCompile(`\bWriteHeader\(\s*([^)]+?)\s*\)`)

// Return bare status literal: "return http.StatusBadRequest" in helpers.
var goStatusConstRe = regexp.MustCompile(`\bhttp\.(Status[A-Z]\w+)\b`)

func init() {
	// Provider-side detectors. Each one's regex is narrow enough that
	// running all of them on every Go provider handler doesn't cause
	// cross-framework false positives.
	schemaEnrichers = append(schemaEnrichers,
		schemaEnricher{
			name:      "go-stdlib-provider",
			languages: []string{"go"},
			roles:     []Role{RoleProvider},
			detect:    goNetHTTPDetect,
		},
		schemaEnricher{
			name:      "go-gin-provider",
			languages: []string{"go"},
			roles:     []Role{RoleProvider},
			detect:    goGinDetect,
		},
		schemaEnricher{
			name:      "go-fiber-provider",
			languages: []string{"go"},
			roles:     []Role{RoleProvider},
			detect:    goFiberDetect,
		},
		schemaEnricher{
			name:      "go-echo-provider",
			languages: []string{"go"},
			roles:     []Role{RoleProvider},
			detect:    goEchoDetect,
		},

		// Consumer side — picks up the outgoing payload and the
		// decode target around the call site. Same detector handles
		// all Go HTTP clients (stdlib, resty, etc.) because the
		// surrounding idioms are the same.
		schemaEnricher{
			name:      "go-consumer",
			languages: []string{"go"},
			roles:     []Role{RoleConsumer},
			detect:    goConsumerDetect,
		},
	)
}

// -----------------------------------------------------------------------------
// Go provider detectors
// -----------------------------------------------------------------------------

func goNetHTTPDetect(body string, fileNodes []*graph.Node) schemaHints {
	var h schemaHints

	if m := goStdlibDecodeRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	} else if m := goUnmarshalRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	}

	if m := goStdlibEncodeRe.FindStringSubmatch(body); len(m) > 1 {
		setResponseType(&h, m[1], body, fileNodes, m[0])
	} else if m := goWriteJSONRe.FindStringSubmatch(body); len(m) > 2 {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
		setResponseType(&h, m[2], body, fileNodes, m[0])
	}

	h.QueryParams = append(h.QueryParams, allSubmatches(body, goQueryParamRe, 1)...)
	for _, m := range goWriteHeaderRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	for _, m := range goStatusConstRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr("http." + m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	return h
}

// Gin: c.BindJSON / c.ShouldBindJSON / c.ShouldBind, c.JSON(status, obj),
// c.Query("x"), c.DefaultQuery("x", ...), c.Param("x").
var (
	ginBindRe   = regexp.MustCompile(`\b(?:ShouldBindJSON|BindJSON|ShouldBind)\(\s*&?(\w+)\s*\)`)
	ginJSONRe   = regexp.MustCompile(`\.JSON\(\s*([^,]+?)\s*,\s*([A-Za-z_][\w\.]*(?:\{[^}]*\})?)\s*\)`)
	ginQueryRe  = regexp.MustCompile(`\b(?:DefaultQuery|Query)\(\s*["` + "`" + `]([^"` + "`" + `]+)["` + "`" + `]`)
	ginStatusRe = regexp.MustCompile(`\.Status\(\s*([^)]+?)\s*\)`)
)

func goGinDetect(body string, fileNodes []*graph.Node) schemaHints {
	var h schemaHints
	if m := ginBindRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	}
	for _, m := range ginJSONRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
		setResponseType(&h, m[2], body, fileNodes, m[0])
	}
	h.QueryParams = append(h.QueryParams, allSubmatches(body, ginQueryRe, 1)...)
	for _, m := range ginStatusRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	// Pick up any bare http.StatusX references too.
	for _, m := range goStatusConstRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr("http." + m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	return h
}

// Fiber: c.BodyParser(&req), c.JSON(obj), c.Status(200), c.Query("x").
var (
	fiberBindRe   = regexp.MustCompile(`\bBodyParser\(\s*&?(\w+)\s*\)`)
	fiberJSONRe   = regexp.MustCompile(`\.JSON\(\s*&?([A-Za-z_][\w\.]*(?:\{[^}]*\})?)\s*\)`)
	fiberQueryRe  = regexp.MustCompile(`\.Query\(\s*["` + "`" + `]([^"` + "`" + `]+)["` + "`" + `]`)
	fiberStatusRe = regexp.MustCompile(`\.Status\(\s*([^)]+?)\s*\)`)
)

func goFiberDetect(body string, fileNodes []*graph.Node) schemaHints {
	var h schemaHints
	if m := fiberBindRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	}
	if m := fiberJSONRe.FindStringSubmatch(body); len(m) > 1 {
		setResponseType(&h, m[1], body, fileNodes, m[0])
	}
	h.QueryParams = append(h.QueryParams, allSubmatches(body, fiberQueryRe, 1)...)
	for _, m := range fiberStatusRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	for _, m := range goStatusConstRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr("http." + m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	return h
}

// Echo: c.Bind(&req), c.JSON(status, obj), c.QueryParam("x"),
// c.Param("x").
var (
	echoBindRe  = regexp.MustCompile(`\bc\.Bind\(\s*&?(\w+)\s*\)`)
	echoQueryRe = regexp.MustCompile(`\bQueryParam\(\s*["` + "`" + `]([^"` + "`" + `]+)["` + "`" + `]`)
)

func goEchoDetect(body string, fileNodes []*graph.Node) schemaHints {
	var h schemaHints
	if m := echoBindRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	}
	// Echo's JSON signature is identical to gin's, so the gin regex
	// also fires here via the shared enricher chain — the driver
	// merges. But we still pull status codes from h.WriteHeader etc.
	for _, m := range ginJSONRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr(m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
		setResponseType(&h, m[2], body, fileNodes, m[0])
	}
	h.QueryParams = append(h.QueryParams, allSubmatches(body, echoQueryRe, 1)...)
	for _, m := range goStatusConstRe.FindAllStringSubmatch(body, -1) {
		if code, ok := parseStatusExpr("http." + m[1]); ok {
			h.StatusCodes = append(h.StatusCodes, code)
		}
	}
	return h
}

// -----------------------------------------------------------------------------
// Go consumer detector — best-effort extraction of the outgoing payload
// and any decode target near the call site.
// -----------------------------------------------------------------------------

var (
	// Outbound body: json.Marshal(<expr>) within the call window.
	goMarshalRe = regexp.MustCompile(`json\.Marshal\(\s*&?(\w+)\s*\)`)
	// Decode target: json.NewDecoder(resp.Body).Decode(&result) or
	// json.Unmarshal(body, &result).
	goDecodeRespRe = regexp.MustCompile(`json\.NewDecoder\([^)]+\.Body\)\.Decode\(\s*&?(\w+)\s*\)`)
)

func goConsumerDetect(body string, fileNodes []*graph.Node) schemaHints {
	var h schemaHints
	if m := goMarshalRe.FindStringSubmatch(body); len(m) > 1 {
		setRequestType(&h, m[1], body, fileNodes, m[0])
	}
	if m := goDecodeRespRe.FindStringSubmatch(body); len(m) > 1 {
		setResponseType(&h, m[1], body, fileNodes, m[0])
	} else if m := goUnmarshalRe.FindStringSubmatch(body); len(m) > 1 {
		setResponseType(&h, m[1], body, fileNodes, m[0])
	}
	return h
}

// -----------------------------------------------------------------------------
// Shared helpers for Go detectors
// -----------------------------------------------------------------------------

// setRequestType resolves an argument identifier to a type name and
// records it on hints. If the identifier doesn't resolve, we store the
// source expression (the matched substring) so the UI at least points
// at the binding call.
func setRequestType(h *schemaHints, ident, body string, fileNodes []*graph.Node, matchText string) {
	if t := findVarType(body, ident); t != "" {
		h.RequestType = resolveTypeInFile(t, fileNodes)
		return
	}
	// Identifier itself might be a type — happens with anonymous
	// zero-value literals like `Decode(&Request{})`.
	if looksLikeType(ident) {
		h.RequestType = resolveTypeInFile(ident, fileNodes)
		return
	}
	if h.RequestExpr == "" {
		h.RequestExpr = strings.TrimSpace(matchText)
	}
}

func setResponseType(h *schemaHints, ident, body string, fileNodes []*graph.Node, matchText string) {
	// Envelope unwrap: `map[string]any{"data": workspaces, ...}` —
	// surface every key as a structured envelope field so the
	// dashboard can render the actual response shape instead of a
	// chunk of source. A common idiom in handwritten Go servers; the
	// outer HasPrefix guard avoids misfiring on struct literals that
	// happen to include a quoted tag.
	trimmed := strings.TrimSpace(ident)
	if strings.HasPrefix(trimmed, "map[") {
		if fields := parseMapEnvelopeFields(trimmed, body, fileNodes); len(fields) > 0 {
			h.ResponseEnvelope = fields
			// Don't surface the raw `map[string]any{...}` literal —
			// the envelope is the JSON shape, the Go construction
			// helper is implementation detail and only confuses the
			// UI ("why is my JSON response a map[string]any?"). The
			// envelope rows carry every field separately.
			//
			// Promote a single-field envelope's resolved type so
			// downstream "is the response type known" checks light up
			// just like a bare struct return would.
			if len(fields) == 1 && fields[0].Type != "" {
				h.ResponseType = fields[0].Type
			}
			return
		}
	}
	if t := findVarType(body, ident); t != "" {
		h.ResponseType = resolveTypeInFile(t, fileNodes)
		return
	}
	if looksLikeType(ident) {
		h.ResponseType = resolveTypeInFile(ident, fileNodes)
		return
	}
	// No syntactic type on the variable's binding line. Fall back to
	// recording the value expression — the indexer's
	// resolveCallReturnTypes post-pass (graph-aware) picks the bare
	// identifier back out and traces it to the method/builtin that
	// bound it, reading the real return type from the method's
	// signature or the literal shape. Prefer the response value
	// (`ident`) over the wider matchText: an identifier (`result`)
	// or compound literal (`Foo{...}`) carries the meaningful info,
	// while matchText is the surrounding helper-call boilerplate the
	// user already saw on the Source tab.
	if h.ResponseExpr == "" {
		switch {
		case isCompoundExpr(trimmed):
			h.ResponseExpr = trimmed
		case isBareIdent(trimmed):
			h.ResponseExpr = trimmed
		default:
			h.ResponseExpr = strings.TrimSpace(matchText)
		}
	}
}

// parseMapEnvelopeFields walks every "key": value pair inside a
// `map[string]any{...}` (or `map[string]interface{}{...}`) literal.
// For each value it tries to resolve a concrete type via findVarType
// and falls back to the trimmed source expression. Returns nil when
// the literal can't be brace-balanced or has no keys.
func parseMapEnvelopeFields(literal, body string, fileNodes []*graph.Node) []envelopeField {
	// The body's opening `{` is the LAST `{` before the first quoted
	// key. Anchoring on the first quote skips over the type-side
	// `{}` pair in `map[string]interface{}{...}`, which a plain
	// brace-balance from index 0 would otherwise treat as the body.
	// Empty envelopes (`map[string]any{}`) have no key and return nil.
	firstQuote := strings.Index(literal, `"`)
	if firstQuote < 0 {
		return nil
	}
	open := strings.LastIndex(literal[:firstQuote], "{")
	if open < 0 {
		return nil
	}
	closeIdx := strings.LastIndex(literal, "}")
	if closeIdx <= open {
		return nil
	}
	inner := literal[open+1 : closeIdx]

	var out []envelopeField
	for _, kv := range splitMapLiteralBody(inner) {
		f := envelopeField{Name: kv.key, Expr: kv.value}
		// Three resolution paths in priority order:
		//   1. Inline composite literal: `[]Foo{...}` / `Foo{...}` /
		//      `&Foo{...}` / `make([]Foo, ...)` — the type is in the
		//      expression itself, no body lookup needed.
		//   2. Bare identifier with a typed declaration in the body
		//      (`var x Foo` / `x := Foo{}`) — findVarType sees it.
		//   3. Bare identifier whose value is a method call —
		//      handled by the indexer's graph-aware post-pass.
		if t, repeated := typeOfInlineExpr(kv.value); t != "" {
			f.Type = resolveTypeInFile(t, fileNodes)
			f.Repeated = repeated
			out = append(out, f)
			continue
		}
		bare := strings.TrimPrefix(strings.TrimPrefix(kv.value, "&"), "*")
		if isBareIdent(bare) {
			if t := findVarType(body, bare); t != "" {
				f.Type = resolveTypeInFile(t, fileNodes)
			}
		} else if looksLikeType(bare) {
			f.Type = resolveTypeInFile(bare, fileNodes)
		}
		out = append(out, f)
	}
	return out
}

// envelopeKV is one parsed "key": value entry from a Go map literal.
// key is the unquoted JSON name; value is the trimmed source
// expression with all nested braces/brackets intact.
type envelopeKV struct{ key, value string }

// splitMapLiteralBody walks the body of a map literal — the text
// between the outer `{` and `}` — and returns one envelopeKV per
// top-level "key": value entry. Tracks `{` `[` `(` nesting and
// string/rune literals so nested composite literals don't trick the
// walker into closing the entry early. Replaces the previous regex
// approach that truncated `[]any{a, b}` at the first `}` it saw.
func splitMapLiteralBody(inner string) []envelopeKV {
	var out []envelopeKV
	parts := splitTopLevel(inner, ',')
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Find the colon separating "key" from value. A key is a
		// double-quoted string; everything after it (up to the colon)
		// is whitespace, then the colon. Skip non-string-keyed
		// elements (Go map[K]V allows non-string keys, but JSON
		// envelopes always use strings).
		if part[0] != '"' {
			continue
		}
		end := indexUnescapedQuote(part[1:])
		if end < 0 {
			continue
		}
		key := part[1 : 1+end]
		rest := strings.TrimSpace(part[1+end+1:])
		if rest == "" || rest[0] != ':' {
			continue
		}
		value := strings.TrimSpace(rest[1:])
		// Strip a single trailing comma if present (the splitter only
		// strips top-level separators; whitespace-padded entries are
		// safe but defensive cleanup keeps round-trips clean).
		value = strings.TrimRight(value, " \t,")
		if value == "" {
			continue
		}
		out = append(out, envelopeKV{key: key, value: value})
	}
	return out
}

// splitTopLevel splits s on `sep` runes that appear at nesting depth
// zero — i.e. not inside `{}` / `[]` / `()` and not inside string or
// rune literals. Used by splitMapLiteralBody.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	inStr := false
	inRune := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			if c == '"' && (i == 0 || s[i-1] != '\\') {
				inStr = false
			}
		case inRune:
			if c == '\'' && (i == 0 || s[i-1] != '\\') {
				inRune = false
			}
		case c == '"':
			inStr = true
		case c == '\'':
			inRune = true
		case c == '{', c == '[', c == '(':
			depth++
		case c == '}', c == ']', c == ')':
			if depth > 0 {
				depth--
			}
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}

func indexUnescapedQuote(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '"' && (i == 0 || s[i-1] != '\\') {
			return i
		}
	}
	return -1
}

// typeOfInlineExpr recognises composite literals and builtin
// constructors and reports the bare type plus whether it's a slice.
// Examples:
//
//	"[]Foo{a, b}"      → ("Foo",   true)
//	"Foo{ID: 1}"       → ("Foo",   false)
//	"&Foo{}"           → ("Foo",   false)
//	"make([]Foo, 0)"   → ("Foo",   true)
//	"42" / "id" / ""   → ("",      false)
//
// Returns ("", false) when the expression isn't a syntactically
// recognisable typed literal — the caller falls back to
// findVarType on the surrounding body.
func typeOfInlineExpr(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", false
	}
	// Pointer prefix.
	if strings.HasPrefix(expr, "&") {
		expr = strings.TrimSpace(expr[1:])
	}
	// `make([]T, …)` / `make([]T)`.
	if strings.HasPrefix(expr, "make([]") {
		rest := expr[len("make([]"):]
		t := readTypeIdent(rest)
		if t != "" {
			return t, true
		}
	}
	// `make(map[K]V, …)` — return the whole map type so the
	// dashboard renders it verbatim.
	if strings.HasPrefix(expr, "make(map[") {
		rest := expr[len("make("):]
		// Walk until we hit the matching `]` then the value type.
		if i := strings.Index(rest, "]"); i >= 0 && i+1 < len(rest) {
			vt := readTypeIdent(rest[i+1:])
			if vt != "" {
				return "map[" + readUntil(rest[len("map["):], ']') + "]" + vt, false
			}
		}
	}
	// `[]Foo{…}` — slice composite literal.
	if strings.HasPrefix(expr, "[]") {
		t := readTypeIdent(expr[2:])
		if t != "" && hasComposite(expr) {
			return t, true
		}
	}
	// `Foo{…}` — struct composite literal. Must start with an upper-
	// case letter to avoid eating `for x := range m {` and similar.
	if hasComposite(expr) {
		t := readTypeIdent(expr)
		if t != "" && t[0] >= 'A' && t[0] <= 'Z' {
			return t, false
		}
	}
	return "", false
}

// readTypeIdent reads a Go type identifier (with optional package
// qualifier and generic arguments) from the start of s, stopping at
// the first character that can't be part of an ident.
func readTypeIdent(s string) string {
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || c == '.' {
			end++
			continue
		}
		if end > 0 && c >= '0' && c <= '9' {
			end++
			continue
		}
		break
	}
	return s[:end]
}

func readUntil(s string, c byte) string {
	if i := strings.IndexByte(s, c); i >= 0 {
		return s[:i]
	}
	return s
}

func hasComposite(s string) bool {
	// A composite literal has an `{` somewhere after the type prefix
	// and before any `(` (which would suggest a call). Cheap check.
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.Index(s, "("); j >= 0 && j < i {
			return false
		}
		return true
	}
	return false
}

func isCompoundExpr(s string) bool {
	return strings.ContainsAny(s, "{[(")
}

func isBareIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		// Allow dotted paths (pkg.Foo) but no other punctuation.
		if r == '.' && i > 0 {
			continue
		}
		return false
	}
	return true
}

// looksLikeType is a quick heuristic: starts with an uppercase letter,
// contains only identifier-ish characters. Filters out things like
// "err" or "nil" while keeping "LoginRequest" and "pkg.User".
func looksLikeType(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "&")
	s = strings.TrimPrefix(s, "*")
	if s == "" {
		return false
	}
	// Drop trailing literal `{}` so `Foo{}` still counts as a type.
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[:i]
	}
	first := rune(s[0])
	if first < 'A' || first > 'Z' {
		// `pkg.User` — check the part after the last dot too.
		if i := strings.LastIndex(s, "."); i >= 0 {
			return looksLikeType(s[i+1:])
		}
		return false
	}
	return true
}

func allSubmatches(body string, re *regexp.Regexp, grp int) []string {
	ms := re.FindAllStringSubmatch(body, -1)
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if grp < len(m) {
			out = append(out, m[grp])
		}
	}
	return out
}
