package contracts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// TRPCExtractor detects tRPC routers (providers) and client procedure
// calls (consumers) in TypeScript / JavaScript source.
//
// tRPC is a code-first RPC framework: the server declares procedures as
// the keys of a router object (createTRPCRouter / t.router), and the
// client invokes them through a typed proxy chain
// (trpc.<router>.<procedure>.useQuery(), or a createTRPCProxyClient
// chain). There is no IDL or HTTP path, so both sides are detected
// structurally from the source text.
//
// Canonical contract ID: "trpc::<router>.<procedure>".
//   - Provider: <router> is the variable the router object is assigned
//     to (export const userRouter = createTRPCRouter({...})); <procedure>
//     is an object key whose value is a procedure builder.
//   - Consumer: <router> and <procedure> are the two access-chain
//     segments between the client base (the `trpc` proxy or a
//     createTRPCProxyClient variable) and the React-Query hook / proxy
//     method (trpc.userRouter.getUser.useQuery()).
//
// Both sides build the identical string, so contracts.Match pairs a
// provider with its consumer.
//
// Nested routers (v1): only the leaf procedures of each
// createTRPCRouter / t.router literal that is assigned to a variable are
// emitted, under that variable. A sub-router mounted by reference
// (appRouter = createTRPCRouter({ user: userRouter })) or inline
// (user: t.router({...})) is not expanded here — the sub-router's own
// `const userRouter = createTRPCRouter({...})` contributes its leaves.
type TRPCExtractor struct{}

// trpcMarkers is the cheap substring prefilter: a file mentioning none
// of these cannot contain a tRPC router or client call, so the regex
// scan is skipped (mirrors the gRPC / GraphQL marker prefilter).
var trpcMarkers = [][]byte{
	[]byte("createTRPCRouter"),
	[]byte("t.router"),
	[]byte("createTRPCProxyClient"),
	[]byte("createTRPCClient"),
	[]byte("publicProcedure"),
	[]byte("protectedProcedure"),
	[]byte("trpc."),
}

// hasTRPCMarkers reports whether src mentions any tRPC construct.
func hasTRPCMarkers(src []byte) bool { return srcHasAnyMarker(src, trpcMarkers) }

// SupportedLanguages lists the indexer dispatch languages tRPC code
// lives in. The contract dispatch keys on the graph node's language:
// .ts -> "typescript", .js/.jsx -> "javascript", .tsx -> "tsx". List
// every form so a router definition or a React-component (.tsx) consumer
// is seen wherever it lives.
func (e *TRPCExtractor) SupportedLanguages() []string {
	return []string{"typescript", "javascript", "tsx", "jsx"}
}

var (
	// Router definition head: `export const userRouter = createTRPCRouter(`
	// or `const appRouter = t.router(`. Group 1 = router variable name.
	trpcRouterDefRe = regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:createTRPCRouter|t\.router)\s*\(`)

	// Client construction: `const client = createTRPCProxyClient<...>(` or
	// `createTRPCClient<...>(`. Group 1 = client variable name. The proxy
	// client exposes the same <router>.<procedure> chain as the React
	// hooks proxy, so its base identifier joins the consumer scan.
	trpcClientDefRe = regexp.MustCompile(`(?m)(?:const|let|var)\s+(\w+)\s*=\s*(?:createTRPCProxyClient|createTRPCClient)\s*<`)

	// Proxy access chain `<base>.<router>.<procedure>.<method>(`. The base
	// and method are validated after the match (base must be a known tRPC
	// client; method must be a known hook / proxy call) to keep arbitrary
	// four-segment chains from minting false consumers.
	trpcConsumerCallRe = regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*\(`)

	// A procedure value is built from a procedure builder and a resolver
	// method — used to confirm an object key is a leaf procedure.
	trpcProcValueRe = regexp.MustCompile(`\b(?:publicProcedure|protectedProcedure|procedure)\b|\.(?:query|mutation|subscription)\s*\(`)

	// A value that is itself a (sub-)router rather than a leaf procedure:
	// an inline createTRPCRouter / t.router / router(...) call.
	trpcSubRouterValueRe = regexp.MustCompile(`^(?:createTRPCRouter|t\.router|router)\s*\(`)

	// A bare identifier value — a sub-router mounted by reference.
	trpcBareIdentRe = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
)

// trpcConsumerMethods are the React-Query hooks and proxy-client methods
// that terminate a tRPC procedure access chain.
var trpcConsumerMethods = map[string]struct{}{
	"useQuery":         {},
	"useMutation":      {},
	"useSubscription":  {},
	"useInfiniteQuery": {},
	"query":            {},
	"mutate":           {},
	"mutation":         {},
	"subscribe":        {},
}

// Extract returns the tRPC provider and consumer contracts for one file.
func (e *TRPCExtractor) Extract(filePath string, src []byte, nodes []*graph.Node, edges []*graph.Edge) []Contract {
	if !hasTRPCMarkers(src) {
		return nil
	}
	text := string(src)
	lines := strings.Split(text, "\n")
	fileNodes := filterFileNodes(filePath, nodes)
	sort.Slice(fileNodes, func(i, j int) bool {
		return fileNodes[i].StartLine < fileNodes[j].StartLine
	})

	var out []Contract
	out = append(out, e.extractProviders(filePath, text, lines, fileNodes)...)
	out = append(out, e.extractConsumers(filePath, text, lines, fileNodes)...)
	return out
}

// extractProviders emits a Provider contract for every leaf procedure of
// each variable-assigned createTRPCRouter / t.router literal.
func (e *TRPCExtractor) extractProviders(filePath, text string, lines []string, fileNodes []*graph.Node) []Contract {
	var out []Contract
	for _, m := range trpcRouterDefRe.FindAllStringSubmatchIndex(text, -1) {
		routerVar := text[m[2]:m[3]]
		// m[1] is one past the whole match — i.e. just past the `(` that
		// opens the createTRPCRouter / t.router call. Balance-scan from
		// that `(` to its matching `)`, then find the router object
		// literal `{...}` inside the argument span.
		openParen := m[1] - 1
		argStart, argClose := trpcBalancedSpan(text, openParen, '(', ')')
		if argStart < 0 {
			continue
		}
		braceRel := strings.IndexByte(text[argStart:argClose], '{')
		if braceRel < 0 {
			continue
		}
		objStart, objClose := trpcBalancedSpan(text, argStart+braceRel, '{', '}')
		if objStart < 0 {
			continue
		}
		for _, key := range trpcTopLevelKeys(text, objStart, objClose) {
			if trpcSubRouterValueRe.MatchString(key.value) || trpcBareIdentRe.MatchString(key.value) {
				// A nested / referenced sub-router, not a leaf procedure.
				continue
			}
			if !trpcProcValueRe.MatchString(key.value) {
				continue
			}
			line := lineAtOffset(lines, key.offset)
			out = append(out, Contract{
				ID:       fmt.Sprintf("trpc::%s.%s", routerVar, key.name),
				Type:     ContractTRPC,
				Role:     RoleProvider,
				SymbolID: findEnclosingSymbol(fileNodes, line),
				FilePath: filePath,
				Line:     line,
				Meta: map[string]any{
					"framework": "trpc",
					"router":    routerVar,
					"procedure": key.name,
				},
				Confidence: 0.9,
			})
		}
	}
	return out
}

// extractConsumers emits a Consumer contract for every tRPC procedure
// access chain rooted at a known client base.
func (e *TRPCExtractor) extractConsumers(filePath, text string, lines []string, fileNodes []*graph.Node) []Contract {
	// Client bases: the conventional `trpc` proxy plus any
	// createTRPCProxyClient / createTRPCClient variable in this file.
	bases := map[string]struct{}{"trpc": {}}
	for _, m := range trpcClientDefRe.FindAllStringSubmatch(text, -1) {
		bases[m[1]] = struct{}{}
	}

	var out []Contract
	seen := map[string]struct{}{}
	for _, m := range trpcConsumerCallRe.FindAllStringSubmatchIndex(text, -1) {
		base := text[m[2]:m[3]]
		router := text[m[4]:m[5]]
		procedure := text[m[6]:m[7]]
		method := text[m[8]:m[9]]
		if _, ok := bases[base]; !ok {
			continue
		}
		if _, ok := trpcConsumerMethods[method]; !ok {
			continue
		}
		line := lineAtOffset(lines, m[0])
		id := fmt.Sprintf("trpc::%s.%s", router, procedure)
		dedup := fmt.Sprintf("%s@%d", id, line)
		if _, ok := seen[dedup]; ok {
			continue
		}
		seen[dedup] = struct{}{}
		out = append(out, Contract{
			ID:       id,
			Type:     ContractTRPC,
			Role:     RoleConsumer,
			SymbolID: findEnclosingSymbol(fileNodes, line),
			FilePath: filePath,
			Line:     line,
			Meta: map[string]any{
				"framework": "trpc",
				"router":    router,
				"procedure": procedure,
				"method":    method,
			},
			Confidence: 0.85,
		})
	}
	return out
}

// trpcKey is one immediate `key: value` entry of an object literal.
type trpcKey struct {
	name   string
	value  string
	offset int // byte offset of the key name in the original text
}

// trpcTopLevelKeys parses the immediate `key: value` entries of an object
// literal. objStart is the index just after the opening `{`; objClose is
// the index of the matching `}`. Only depth-0 entries are returned;
// nested object / array / call contents are skipped. String literals and
// comments are honoured so their delimiters do not perturb the depth.
func trpcTopLevelKeys(text string, objStart, objClose int) []trpcKey {
	var keys []trpcKey
	depth := 0
	entryStart := objStart
	add := func(end int) {
		if k := trpcParseEntry(text, entryStart, end); k != nil {
			keys = append(keys, *k)
		}
	}
	i := objStart
	for i < objClose {
		c := text[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = trpcSkipString(text, i)
			continue
		case c == '/' && i+1 < objClose && text[i+1] == '/':
			i = trpcSkipLineComment(text, i)
			continue
		case c == '/' && i+1 < objClose && text[i+1] == '*':
			i = trpcSkipBlockComment(text, i)
			continue
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			add(i)
			entryStart = i + 1
		}
		i++
	}
	add(objClose)
	return keys
}

// trpcParseEntry parses one top-level object entry `key: value`. Returns
// nil when the segment is not a plain `identifier: value` (e.g. a spread
// or a shorthand property).
func trpcParseEntry(text string, start, end int) *trpcKey {
	i := trpcSkipLeading(text, start, end)
	if i >= end || !trpcIsIdentStart(text[i]) {
		return nil
	}
	nameStart := i
	for i < end && trpcIsIdentPart(text[i]) {
		i++
	}
	name := text[nameStart:i]
	for i < end && trpcIsSpace(text[i]) {
		i++
	}
	if i >= end || text[i] != ':' {
		return nil
	}
	value := strings.TrimSpace(text[i+1 : end])
	return &trpcKey{name: name, value: value, offset: nameStart}
}

// trpcSkipLeading returns the index of the first non-space, non-comment
// byte at or after start (bounded by end).
func trpcSkipLeading(text string, start, end int) int {
	i := start
	for i < end {
		c := text[i]
		switch {
		case trpcIsSpace(c):
			i++
		case c == '/' && i+1 < end && text[i+1] == '/':
			i = trpcSkipLineComment(text, i)
		case c == '/' && i+1 < end && text[i+1] == '*':
			i = trpcSkipBlockComment(text, i)
		default:
			return i
		}
	}
	return end
}

// trpcBalancedSpan returns (contentStart, closeIdx) for the delimiter pair
// opened at text[openIdx]. contentStart = openIdx+1; closeIdx is the index
// of the matching close delimiter. String literals and comments are
// skipped so their delimiters do not affect the count. Returns (-1, -1)
// when no match is found. open and close must be distinct.
func trpcBalancedSpan(text string, openIdx int, open, close byte) (int, int) {
	depth := 0
	n := len(text)
	for i := openIdx; i < n; {
		c := text[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = trpcSkipString(text, i)
			continue
		case c == '/' && i+1 < n && text[i+1] == '/':
			i = trpcSkipLineComment(text, i)
			continue
		case c == '/' && i+1 < n && text[i+1] == '*':
			i = trpcSkipBlockComment(text, i)
			continue
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return openIdx + 1, i
			}
		}
		i++
	}
	return -1, -1
}

// trpcSkipString returns the index just past the string literal beginning
// at text[i] (a ', ", or ` quote). Escapes are honoured. A backtick
// literal is treated as opaque up to its closing backtick — template
// `${}` interpolation is not parsed, which is adequate for scanning
// router / client object literals.
func trpcSkipString(text string, i int) int {
	n := len(text)
	quote := text[i]
	for i++; i < n; i++ {
		switch text[i] {
		case '\\':
			i++
		case quote:
			return i + 1
		}
	}
	return n
}

// trpcSkipLineComment returns the index of the newline ending the `//`
// comment that begins at text[i] (or len(text) at EOF).
func trpcSkipLineComment(text string, i int) int {
	n := len(text)
	for ; i < n && text[i] != '\n'; i++ {
	}
	return i
}

// trpcSkipBlockComment returns the index just past the `*/` ending the
// block comment that begins at text[i] (or len(text) when unterminated).
func trpcSkipBlockComment(text string, i int) int {
	n := len(text)
	for i += 2; i+1 < n; i++ {
		if text[i] == '*' && text[i+1] == '/' {
			return i + 2
		}
	}
	return n
}

func trpcIsSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func trpcIsIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func trpcIsIdentPart(c byte) bool { return trpcIsIdentStart(c) || (c >= '0' && c <= '9') }

func init() {
	// tRPC procedures are extracted by TRPCExtractor, registered in the
	// indexer's per-file contract-extractor set alongside GraphQL and
	// gRPC. This registration adds "trpc" to the route-framework
	// inventory that `analyze route_frameworks` reports — so the
	// framework is listed (with its procedure count, read off the
	// contract nodes' framework label) like the structural HTTP route
	// frameworks. Extraction stays with TRPCExtractor (run is nil here);
	// Detect lets DetectFrameworks attribute a file to tRPC.
	RegisterFrameworkRoutePass(&routePass{
		name:   "trpc",
		langs:  []string{"typescript", "javascript", "tsx", "jsx"},
		detect: func(_ string, src []byte) bool { return hasTRPCMarkers(src) },
		run:    nil,
	})
}
