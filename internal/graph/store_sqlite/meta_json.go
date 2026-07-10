package store_sqlite

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"encoding/json"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

// Node / edge Meta is a map[string]any persisted in the `meta` column.
// It is stored as JSON, not gob: JSON needs no per-call type-engine
// compilation (the gob hot path recompiled its decoder on every edge,
// which dominated cold-load CPU and allocation), and a JSON document is
// human-readable and free of any custom binary versioning.
//
// JSON has one numeric type, so a naive json.Unmarshal into a
// map[string]any widens every number to float64 and every []T to []any,
// silently corrupting the readers that type-assert .(int) / .(float64) /
// .([]string) / .(*contracts.Shape). decodeMeta therefore routes the
// document through metaWire — a typed DTO whose fields parse each known
// key as its exact Go type — and normalises the open tail (Extra plus
// nested maps) with a small key-type table. The in-memory map a caller
// receives is byte-for-byte type-identical to what the old gob path
// produced, so no reader changes.
//
// Existing on-disk stores still hold gob blobs; decodeMeta sniffs the
// leading byte ('{' => JSON) and falls back to gob for legacy rows, which
// migrate to JSON on their next write. No schema migration is required.

// metaWire is the decode-side DTO. Scalar fields are pointers so an absent
// key (nil) is distinguished from a present zero value — comma-ok readers
// rely on that distinction. Slices, maps and Shape are already nil-able.
type metaWire struct {
	// Symbol-shape keys stamped by language extractors (node).
	Signature  *string `json:"signature,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
	Doc        *string `json:"doc,omitempty"`
	External   *bool   `json:"external,omitempty"`

	// Analyzer / contract scalar keys (node).
	Complexity  *int     `json:"complexity,omitempty"`
	LoopDepth   *int     `json:"loop_depth,omitempty"`
	ParseErrors *int     `json:"parse_errors,omitempty"`
	Position    *int     `json:"position,omitempty"`
	Line        *int     `json:"line,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
	CoveragePct *float64 `json:"coverage_pct,omitempty"`

	// Contract structural keys (node).
	Shape            *contracts.Shape `json:"shape,omitempty"`
	ResponseEnvelope []map[string]any `json:"response_envelope,omitempty"`
	PathParamNames   []string         `json:"path_param_names,omitempty"`
	QueryParams      []string         `json:"query_params,omitempty"`
	StatusCodes      []string         `json:"status_codes,omitempty"`

	// Edge scalar keys.
	CandidateCount *int     `json:"candidate_count,omitempty"`
	Similarity     *float64 `json:"similarity,omitempty"`
	Score          *float64 `json:"score,omitempty"`
	Count          *int     `json:"count,omitempty"`
	CloneTokens    *int     `json:"clone_tokens,omitempty"`

	// Nested enrichment maps (sidecar-primary; the meta map is the
	// un-migrated / in-memory fallback). Decoded as plain maps then
	// normalised via the key-type table so their integer children come
	// back as int / int64 rather than float64.
	Churn        map[string]any `json:"churn,omitempty"`
	Coverage     map[string]any `json:"coverage,omitempty"`
	LastAuthored map[string]any `json:"last_authored,omitempty"`
	ContractMeta map[string]any `json:"contract_meta,omitempty"`

	// Extra captures every key not named above (the open / plugin /
	// per-language tail, overwhelmingly strings and bools).
	Extra map[string]any `json:"-"`
}

// metaWireKnownKeys are the JSON keys consumed by metaWire's typed fields;
// everything else is captured into Extra.
var metaWireKnownKeys = []string{
	"signature", "visibility", "doc", "external",
	"complexity", "loop_depth", "parse_errors", "position", "line",
	"confidence", "coverage_pct",
	"shape", "response_envelope", "path_param_names", "query_params", "status_codes",
	"candidate_count", "similarity", "score", "count", "clone_tokens",
	"churn", "coverage", "last_authored", "contract_meta",
}

// metaFloatKeys are keys whose numeric value must stay float64 even when it
// happens to be integral (e.g. confidence 1.0 marshals as "1"); without
// this they would normalise to int and break a .(float64) reader.
var metaFloatKeys = map[string]bool{
	"confidence": true, "coverage_pct": true, "score": true,
	"similarity": true, "churn_rate": true, "rate": true,
}

// metaInt64Keys are keys whose numeric value must be int64 (unix
// timestamps), matching readers that assert .(int64).
var metaInt64Keys = map[string]bool{
	"timestamp": true, "ts": true,
}

// metaStringSliceKeys are keys whose array value must be []string (JSON
// arrays decode to []any); readers assert .([]string).
var metaStringSliceKeys = map[string]bool{
	"path_param_names": true, "query_params": true, "status_codes": true,
	"notes": true, "methods": true, "arg_names": true, "repos": true,
}

// metaMapSliceKeys are keys whose array value must be []map[string]any.
var metaMapSliceKeys = map[string]bool{
	"response_envelope": true,
}

// UnmarshalJSON decodes the typed fields and captures every other key into
// Extra (with UseNumber so the tail keeps int/float fidelity).
func (w *metaWire) UnmarshalJSON(b []byte) error {
	type alias metaWire
	if err := json.Unmarshal(b, (*alias)(w)); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	for _, k := range metaWireKnownKeys {
		delete(raw, k)
	}
	if len(raw) > 0 {
		w.Extra = make(map[string]any, len(raw))
		for k, v := range raw {
			w.Extra[k] = normalizeMetaValue(k, v)
		}
	}
	return nil
}

// toMap rebuilds the in-memory map[string]any with exact Go types.
func (w *metaWire) toMap() map[string]any {
	m := make(map[string]any, len(metaWireKnownKeys)+len(w.Extra))
	putString(m, "signature", w.Signature)
	putString(m, "visibility", w.Visibility)
	putString(m, "doc", w.Doc)
	putBool(m, "external", w.External)
	putInt(m, "complexity", w.Complexity)
	putInt(m, "loop_depth", w.LoopDepth)
	putInt(m, "parse_errors", w.ParseErrors)
	putInt(m, "position", w.Position)
	putInt(m, "line", w.Line)
	putFloat(m, "confidence", w.Confidence)
	putFloat(m, "coverage_pct", w.CoveragePct)
	if w.Shape != nil {
		m["shape"] = w.Shape
	}
	if w.ResponseEnvelope != nil {
		m["response_envelope"] = w.ResponseEnvelope
	}
	if w.PathParamNames != nil {
		m["path_param_names"] = w.PathParamNames
	}
	if w.QueryParams != nil {
		m["query_params"] = w.QueryParams
	}
	if w.StatusCodes != nil {
		m["status_codes"] = w.StatusCodes
	}
	putInt(m, "candidate_count", w.CandidateCount)
	putFloat(m, "similarity", w.Similarity)
	putFloat(m, "score", w.Score)
	putInt(m, "count", w.Count)
	putInt(m, "clone_tokens", w.CloneTokens)
	putNestedMap(m, "churn", w.Churn)
	putNestedMap(m, "coverage", w.Coverage)
	putNestedMap(m, "last_authored", w.LastAuthored)
	putNestedMap(m, "contract_meta", w.ContractMeta)
	for k, v := range w.Extra {
		m[k] = v
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func putString(m map[string]any, k string, v *string) {
	if v != nil {
		m[k] = *v
	}
}

func putBool(m map[string]any, k string, v *bool) {
	if v != nil {
		m[k] = *v
	}
}

func putInt(m map[string]any, k string, v *int) {
	if v != nil {
		m[k] = *v
	}
}

func putFloat(m map[string]any, k string, v *float64) {
	if v != nil {
		m[k] = *v
	}
}

// putNestedMap normalises a nested enrichment map (decoded by the standard
// json path, so its numbers are float64) into exact Go types.
func putNestedMap(m map[string]any, k string, nested map[string]any) {
	if nested == nil {
		return
	}
	out := make(map[string]any, len(nested))
	for nk, nv := range nested {
		out[nk] = normalizeMetaValue(nk, nv)
	}
	m[k] = out
}

// normalizeMetaValue coerces a json-decoded value to the exact Go type the
// readers expect, recursing through nested maps and slices. It accepts both
// json.Number (the Extra path uses UseNumber) and float64 (the typed-field
// path decodes nested maps with standard json), so it is correct for both.
func normalizeMetaValue(key string, v any) any {
	switch vv := v.(type) {
	case json.Number:
		return normalizeNumber(key, numberToFloat(vv), &vv)
	case float64:
		return normalizeNumber(key, vv, nil)
	case []any:
		return normalizeSlice(key, vv)
	case map[string]any:
		out := make(map[string]any, len(vv))
		for nk, nv := range vv {
			out[nk] = normalizeMetaValue(nk, nv)
		}
		return out
	default:
		return v
	}
}

func numberToFloat(n json.Number) float64 {
	f, _ := n.Float64()
	return f
}

// normalizeNumber picks the Go numeric type for key. num is the float view;
// jn (may be nil) is the original json.Number for exact integer recovery.
func normalizeNumber(key string, num float64, jn *json.Number) any {
	if metaFloatKeys[key] {
		return num
	}
	if metaInt64Keys[key] {
		if jn != nil {
			if i, err := jn.Int64(); err == nil {
				return i
			}
		}
		return int64(num)
	}
	if num == float64(int64(num)) {
		if jn != nil {
			if i, err := jn.Int64(); err == nil {
				return int(i)
			}
		}
		return int(num)
	}
	return num
}

func normalizeSlice(key string, s []any) any {
	if metaStringSliceKeys[key] {
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	if metaMapSliceKeys[key] {
		out := make([]map[string]any, 0, len(s))
		for _, e := range s {
			if mm, ok := e.(map[string]any); ok {
				norm := make(map[string]any, len(mm))
				for nk, nv := range mm {
					norm[nk] = normalizeMetaValue(nk, nv)
				}
				out = append(out, norm)
			}
		}
		return out
	}
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = normalizeMetaValue(key, e)
	}
	return out
}

// encodeMeta serialises Meta to JSON. nil / empty Meta stores as NULL.
func encodeMeta(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// decodeMeta reads a meta blob. New rows are JSON (routed through metaWire
// for exact types); legacy rows are gob and decode through the fallback.
func decodeMeta(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if isJSONObject(b) {
		var w metaWire
		if err := json.Unmarshal(b, &w); err != nil {
			// A gob blob whose first byte is '{' would land here; fall
			// back rather than fail the row.
			return decodeMetaGob(b)
		}
		return w.toMap(), nil
	}
	return decodeMetaGob(b)
}

// isJSONObject reports whether b looks like a JSON object (the only shape
// encodeMeta ever produces). Leading whitespace is tolerated.
func isJSONObject(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func decodeMetaGob(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// -- promoted node columns ------------------------------------------------
//
// signature / visibility / doc / external are universal, hot-read node
// keys. They are lifted into dedicated nullable columns: stripped from the
// JSON blob on write (extractPromotedMeta) and restored into Meta on read
// (restorePromotedMeta), so the in-memory map is unchanged while the keys
// become queryable and the common blob shrinks.

var promotedMetaColumns = []struct {
	name string
	ddl  string
}{
	{"signature", "signature TEXT"},
	{"visibility", "visibility TEXT"},
	{"doc", "doc TEXT"},
	{"external", "external INTEGER"},
	{"return_type", "return_type TEXT"},
	{"is_async", "is_async INTEGER"},
	{"is_static", "is_static INTEGER"},
	{"is_abstract", "is_abstract INTEGER"},
	{"is_exported", "is_exported INTEGER"},
	{"updated_at", "updated_at INTEGER"},
}

// structNodeColumns are typed nodes columns read and written directly from
// Node struct fields (not the Meta blob): the source column offsets. They are
// NOT NULL DEFAULT 0 like start_line / end_line, so an ALTER on an existing DB
// backfills 0.
var structNodeColumns = []struct {
	name string
	ddl  string
}{
	{"start_column", "start_column INTEGER NOT NULL DEFAULT 0"},
	{"end_column", "end_column INTEGER NOT NULL DEFAULT 0"},
}

// promotedNodeMeta holds the typed column values lifted out of a node's Meta
// blob. A NULL (invalid) field means the key was absent (or had an unexpected
// type and stayed in the blob).
type promotedNodeMeta struct {
	sig, vis, doc, returnType                           sql.NullString
	external, isAsync, isStatic, isAbstract, isExported sql.NullBool
	updatedAt                                           sql.NullInt64
}

// ensureNodeColumns adds the promoted + struct columns to a nodes table
// created before they existed. A fresh DB already has them from the DDL, so
// this is a no-op; an older DB is altered in place.
func ensureNodeColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	add := func(name, ddl string) error {
		if existing[name] {
			return nil
		}
		_, err := db.Exec(`ALTER TABLE nodes ADD COLUMN ` + ddl)
		return err
	}
	for _, c := range structNodeColumns {
		if err := add(c.name, c.ddl); err != nil {
			return err
		}
	}
	for _, c := range promotedMetaColumns {
		if err := add(c.name, c.ddl); err != nil {
			return err
		}
	}
	return nil
}

// extractPromotedMeta splits the promoted keys out of m into typed column
// values and returns the remaining map destined for the JSON blob. m is
// not mutated; a copy is made only when a promoted key is present and has
// the expected type (otherwise the value stays in the blob).
func extractPromotedMeta(m map[string]any) (p promotedNodeMeta, rest map[string]any) {
	rest = m
	if len(m) == 0 {
		return
	}
	has := false
	for _, c := range promotedMetaColumns {
		if _, ok := m[c.name]; ok {
			has = true
			break
		}
	}
	if !has {
		return
	}
	rest = make(map[string]any, len(m))
	str := func(v any) (sql.NullString, bool) {
		if s, ok := v.(string); ok {
			return sql.NullString{String: s, Valid: true}, true
		}
		return sql.NullString{}, false
	}
	boolean := func(v any) (sql.NullBool, bool) {
		if b, ok := v.(bool); ok {
			return sql.NullBool{Bool: b, Valid: true}, true
		}
		return sql.NullBool{}, false
	}
	for k, v := range m {
		var promoted bool
		switch k {
		case "signature":
			if nv, ok := str(v); ok {
				p.sig, promoted = nv, true
			}
		case "visibility":
			if nv, ok := str(v); ok {
				p.vis, promoted = nv, true
			}
		case "doc":
			if nv, ok := str(v); ok {
				p.doc, promoted = nv, true
			}
		case "return_type":
			if nv, ok := str(v); ok {
				p.returnType, promoted = nv, true
			}
		case "external":
			if nv, ok := boolean(v); ok {
				p.external, promoted = nv, true
			}
		case "is_async":
			if nv, ok := boolean(v); ok {
				p.isAsync, promoted = nv, true
			}
		case "is_static":
			if nv, ok := boolean(v); ok {
				p.isStatic, promoted = nv, true
			}
		case "is_abstract":
			if nv, ok := boolean(v); ok {
				p.isAbstract, promoted = nv, true
			}
		case "is_exported":
			if nv, ok := boolean(v); ok {
				p.isExported, promoted = nv, true
			}
		case "updated_at":
			if i, ok := metaToInt64(v); ok {
				p.updatedAt, promoted = sql.NullInt64{Int64: i, Valid: true}, true
			}
		}
		if !promoted {
			// Absent / wrong type: keep the value in the JSON blob.
			rest[k] = v
		}
	}
	return
}

// metaToInt64 coerces a meta numeric value (int / int64 / float64 / json.Number)
// to int64 for a promoted timestamp column.
func metaToInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
	}
	return 0, false
}

// restorePromotedMeta writes the non-NULL promoted columns back into the
// node's Meta. A NULL column is left alone so a legacy gob row's blob value
// survives.
func restorePromotedMeta(n *graph.Node, p promotedNodeMeta) {
	if !p.sig.Valid && !p.vis.Valid && !p.doc.Valid && !p.returnType.Valid &&
		!p.external.Valid && !p.isAsync.Valid && !p.isStatic.Valid &&
		!p.isAbstract.Valid && !p.isExported.Valid && !p.updatedAt.Valid {
		return
	}
	if n.Meta == nil {
		n.Meta = make(map[string]any, 8)
	}
	if p.sig.Valid {
		n.Meta["signature"] = p.sig.String
	}
	if p.vis.Valid {
		n.Meta["visibility"] = p.vis.String
	}
	if p.doc.Valid {
		n.Meta["doc"] = p.doc.String
	}
	if p.returnType.Valid {
		n.Meta["return_type"] = p.returnType.String
	}
	if p.external.Valid {
		n.Meta["external"] = p.external.Bool
	}
	if p.isAsync.Valid {
		n.Meta["is_async"] = p.isAsync.Bool
	}
	if p.isStatic.Valid {
		n.Meta["is_static"] = p.isStatic.Bool
	}
	if p.isAbstract.Valid {
		n.Meta["is_abstract"] = p.isAbstract.Bool
	}
	if p.isExported.Valid {
		n.Meta["is_exported"] = p.isExported.Bool
	}
	if p.updatedAt.Valid {
		n.Meta["updated_at"] = p.updatedAt.Int64
	}
}

// -- promoted edge columns -------------------------------------------------
//
// resolve_terminal / resolve_terminal_reason (see resolver/terminal.go) are
// the edge-side sibling of promotedMetaColumns above. A generated column
// deriving them from the meta blob via json_extract was tried first and
// abandoned: encodeMeta's common case is a custom flat binary codec (see
// "flat binary meta codec" below in this file), not JSON — JSON is only a
// fallback for value shapes the flat codec can't model, and a bool/string
// pair like these two always fits the flat codec. json_extract/json_valid
// against a real store's meta blobs therefore either throws ("malformed
// JSON") or, once gated by json_valid, silently evaluates to NULL for
// effectively every row. Promoting the keys out of the blob into typed
// columns (exactly the node-side pattern) sidesteps the encoding entirely:
// extractPromotedEdgeMeta/restorePromotedEdgeMeta operate on the already-
// decoded map[string]any, not the raw bytes, so they work identically
// regardless of which codec wrote the blob.

// promotedEdgeMeta holds the typed column values lifted out of an edge's
// Meta blob. A NULL (invalid) field means the key was absent (or had an
// unexpected type and stayed in the blob).
type promotedEdgeMeta struct {
	resolveTerminal       sql.NullBool
	resolveTerminalReason sql.NullString
}

// extractPromotedEdgeMeta splits resolve_terminal / resolve_terminal_reason
// out of m into typed column values and returns the remaining map destined
// for the meta blob. m is not mutated; a copy is made only when one of the
// promoted keys is present with the expected type.
func extractPromotedEdgeMeta(m map[string]any) (p promotedEdgeMeta, rest map[string]any) {
	rest = m
	if len(m) == 0 {
		return
	}
	_, hasTerminal := m["resolve_terminal"]
	_, hasReason := m["resolve_terminal_reason"]
	if !hasTerminal && !hasReason {
		return
	}
	rest = make(map[string]any, len(m))
	for k, v := range m {
		var promoted bool
		switch k {
		case "resolve_terminal":
			if b, ok := v.(bool); ok {
				p.resolveTerminal, promoted = sql.NullBool{Bool: b, Valid: true}, true
			}
		case "resolve_terminal_reason":
			if s, ok := v.(string); ok {
				p.resolveTerminalReason, promoted = sql.NullString{String: s, Valid: true}, true
			}
		}
		if !promoted {
			rest[k] = v
		}
	}
	return
}

// restorePromotedEdgeMeta writes the non-NULL promoted columns back into
// the edge's Meta. A NULL column is left alone so a pre-promotion row's
// blob-carried value (if any) survives.
func restorePromotedEdgeMeta(e *graph.Edge, p promotedEdgeMeta) {
	if !p.resolveTerminal.Valid && !p.resolveTerminalReason.Valid {
		return
	}
	if e.Meta == nil {
		e.Meta = make(map[string]any, 2)
	}
	if p.resolveTerminal.Valid {
		e.Meta["resolve_terminal"] = p.resolveTerminal.Bool
	}
	if p.resolveTerminalReason.Valid {
		e.Meta["resolve_terminal_reason"] = p.resolveTerminalReason.String
	}
}
