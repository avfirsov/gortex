package languages

import (
	"strings"

	sitter "github.com/zzet/gortex/internal/parser/tsitter"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/sql"
)

// goSQLExecMethods is the set of Go database-driver method names
// whose first string-literal argument is treated as SQL. Covers
// database/sql (Query, Exec, QueryRow, Prepare and their *Context
// variants), sqlx (Get, Select, NamedExec, NamedQuery, MustExec),
// pgx (Query, Exec, QueryRow on a pool/conn). Other Go SQL
// libraries that share these method names land transparently —
// the heuristic is shape-driven, not import-driven, which keeps
// the detector free of per-library plumbing.
//
// Methods with name collisions outside SQL contexts (e.g. Get on a
// cache, Query on a search index) are accepted as a known false-
// positive surface — the spec recommends the gate stays default-off
// for exactly this reason. Users opt in via
// .gortex.yaml::index.coverage.sql.enabled.
var goSQLExecMethods = map[string]struct{}{
	// database/sql
	"Query":           {},
	"QueryContext":    {},
	"QueryRow":        {},
	"QueryRowContext": {},
	"Exec":            {},
	"ExecContext":     {},
	"Prepare":         {},
	"PrepareContext": {},
	// sqlx
	"Get":         {},
	"GetContext":  {},
	"Select":      {},
	"SelectContext": {},
	"NamedExec":   {},
	"NamedQuery":  {},
	"MustExec":    {},
	// pgx
	"QueryRowContextScan": {},
}

// goSQLEvent is a deferred record of one detected SQL call site.
// Mirrors the goObservabilityEvent / goFlagEvent shape so the
// post-pass emit step can match the same patterns.
type goSQLEvent struct {
	method string
	tables []sql.TableRef
	line   int
}

// detectGoSQLCall returns the table refs extracted from a callm.expr
// capture when the method name matches the SQL exec set and the
// call's first argument is a string literal. ok=false on any other
// shape — non-SQL methods, dynamic queries, no string argument.
func detectGoSQLCall(callExpr *sitter.Node, method string, src []byte) ([]sql.TableRef, bool) {
	if callExpr == nil {
		return nil, false
	}
	if _, hit := goSQLExecMethods[method]; !hit {
		return nil, false
	}
	args := callExpr.ChildByFieldName("arguments")
	if args == nil {
		return nil, false
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		t := c.Type()
		if t != "interpreted_string_literal" && t != "raw_string_literal" {
			continue
		}
		query := strings.Trim(c.Content(src), "\"`")
		refs := sql.ExtractTables(query)
		if len(refs) == 0 {
			return nil, false
		}
		return refs, true
	}
	return nil, false
}

// emitGoSQLEvents turns deferred SQL records into KindTable nodes
// plus EdgeQueries edges. Tables share IDs across files in a repo
// — the same `users` table referenced from multiple call sites
// produces a single node that every caller links to.
func emitGoSQLEvents(events []goSQLEvent, callerLookup func(line int) string, filePath string, result *parser.ExtractionResult) {
	if len(events) == 0 {
		return
	}
	seenNodes := make(map[string]struct{})
	for _, e := range events {
		callerID := callerLookup(e.line)
		if callerID == "" {
			continue
		}
		for _, ref := range e.tables {
			tableID := sql.TableNodeID("generic", ref.Schema, ref.Table)
			if _, ok := seenNodes[tableID]; !ok {
				seenNodes[tableID] = struct{}{}
				meta := map[string]any{
					"table":   ref.Table,
					"dialect": "generic",
				}
				if ref.Schema != "" {
					meta["schema"] = ref.Schema
				}
				result.Nodes = append(result.Nodes, &graph.Node{
					ID:       tableID,
					Kind:     graph.KindTable,
					Name:     ref.Table,
					FilePath: filePath, // first sighting; not authoritative
					Language: "go",
					Meta:     meta,
				})
			}
			result.Edges = append(result.Edges, &graph.Edge{
				From:     callerID,
				To:       tableID,
				Kind:     graph.EdgeQueries,
				FilePath: filePath,
				Line:     e.line,
				Origin:   graph.OriginTextMatched,
				Meta: map[string]any{
					"op":     ref.Op,
					"method": e.method,
				},
			})
		}
	}
}
