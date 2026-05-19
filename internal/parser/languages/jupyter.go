package languages

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// JupyterExtractor extracts cell-structured notebooks:
//
//   - Jupyter `.ipynb` (nbformat 3 + 4) — a JSON document with a
//     `cells[]` array (4) or `worksheets[].cells[]` array (3). Each
//     cell becomes one graph node: code cells materialise as
//     KindFunction (so agents can search them with kind:function),
//     markdown / raw / heading cells materialise as KindVariable
//     (matching markdown.go's heading-and-codeblock convention).
//     Each cell carries Meta["cell_index"] (0-based), Meta["cell_kind"]
//     ∈ code|markdown|raw|heading, and Meta["cell_language"]. A code
//     cell's language defaults to the notebook's kernelspec, but a
//     leading `%%sql` / `%%scala` / `%%r` / `%%bash` / `%%html` cell
//     magic overrides it.
//
//   - Databricks `.dbc` archives — ZIP files containing one or more
//     notebooks. Each entry is parsed first as the Databricks-native
//     JSON shape (`commands[]`) and, on shape mismatch, as nbformat.
//     Cells emitted from archive members carry Meta["archive_member"].
//
// Databricks source-format notebooks (`.py` / `.scala` / `.sql` /
// `.R` files whose first significant line is a `Databricks notebook
// source` magic comment) share their file extension with regular
// source files, so the JupyterExtractor never owns them directly.
// Instead the host-language extractor (PythonExtractor, ScalaExtractor,
// SQLExtractor, RExtractor) calls MaybeEnrichDatabricks at the end
// of its Extract pass — cell-level nodes ride alongside the host
// extractor's regular symbol nodes without conflicting IDs.
type JupyterExtractor struct{}

// NewJupyterExtractor returns the notebook extractor for `.ipynb`
// and `.dbc` files.
func NewJupyterExtractor() *JupyterExtractor { return &JupyterExtractor{} }

func (e *JupyterExtractor) Language() string     { return "jupyter" }
func (e *JupyterExtractor) Extensions() []string { return []string{".ipynb", ".dbc"} }

// dbcZipMaxBytes caps the uncompressed total we read from a `.dbc`
// archive, mirroring zip-bomb defenses elsewhere in the indexer.
const dbcZipMaxBytes = 50 * 1024 * 1024

// ipynbNotebook is the minimal nbformat shape we need.
type ipynbNotebook struct {
	NBFormat   int                `json:"nbformat"`
	Metadata   map[string]any     `json:"metadata"`
	Cells      []ipynbCell        `json:"cells"`
	Worksheets []ipynbV3Worksheet `json:"worksheets"`
}

type ipynbV3Worksheet struct {
	Cells []ipynbCell `json:"cells"`
}

// ipynbCell is the minimal cell shape we need. The `source` field is
// either a string or a list of strings per nbformat — see stringOrList.
type ipynbCell struct {
	CellType string         `json:"cell_type"`
	Source   stringOrList   `json:"source"`
	Language string         `json:"language"`
	Metadata map[string]any `json:"metadata"`
}

// stringOrList accepts either a JSON string or an array of strings.
// nbformat spec allows either; both Jupyter and Databricks emit the
// array form by default but legacy notebooks use the string form.
type stringOrList string

func (s *stringOrList) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*s = ""
		return nil
	}
	switch b[0] {
	case '"':
		var x string
		if err := json.Unmarshal(b, &x); err != nil {
			return err
		}
		*s = stringOrList(x)
		return nil
	case '[':
		var list []string
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		*s = stringOrList(strings.Join(list, ""))
		return nil
	}
	return fmt.Errorf("ipynb cell source: unexpected JSON shape: %s", string(b))
}

// Extract dispatches on the extension. Free-standing `.ipynb` →
// JSON walk; `.dbc` → ZIP archive walk. Malformed inputs yield only
// the file node so indexing continues for the rest of the repo.
func (e *JupyterExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}
	ext := strings.ToLower(filepath.Ext(filePath))

	endLine := lineCount(src)
	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: endLine,
		Language: jupyterFileLanguage(ext),
	}
	result.Nodes = append(result.Nodes, fileNode)

	switch ext {
	case ".ipynb":
		if len(bytes.TrimSpace(src)) == 0 {
			return result, nil
		}
		notebookLang, cells := parseIPYNB(src)
		emitIPYNBCells(filePath, fileNode.ID, "", notebookLang, cells, result)
	case ".dbc":
		extractDBCArchive(filePath, fileNode.ID, src, result)
	}

	return result, nil
}

// jupyterFileLanguage returns the language label for the file node.
// Cell-level Meta["cell_language"] is what agents use for per-cell
// routing; the file-level language is the umbrella.
func jupyterFileLanguage(ext string) string {
	if ext == ".dbc" {
		return "databricks"
	}
	return "jupyter"
}

// parseIPYNB decodes a notebook (nbformat 3 or 4+). Malformed
// documents yield an empty result rather than an error so the rest
// of the repo still indexes.
func parseIPYNB(src []byte) (notebookLang string, cells []ipynbCell) {
	var nb ipynbNotebook
	if err := json.Unmarshal(src, &nb); err != nil {
		return "", nil
	}
	notebookLang = ipynbKernelLanguage(nb.Metadata)
	if len(nb.Cells) > 0 {
		return notebookLang, nb.Cells
	}
	for _, ws := range nb.Worksheets {
		cells = append(cells, ws.Cells...)
	}
	return notebookLang, cells
}

// ipynbKernelLanguage pulls the notebook's kernel language from the
// top-level metadata. Falls through several documented locations and
// finally to "python" — the dominant kernel and what nbformat
// notebooks that omit the metadata default to in practice.
func ipynbKernelLanguage(md map[string]any) string {
	if md == nil {
		return "python"
	}
	if ks, ok := md["kernelspec"].(map[string]any); ok {
		if s, ok := ks["language"].(string); ok && s != "" {
			return strings.ToLower(s)
		}
		if s, ok := ks["name"].(string); ok && s != "" {
			return strings.ToLower(s)
		}
	}
	if li, ok := md["language_info"].(map[string]any); ok {
		if s, ok := li["name"].(string); ok && s != "" {
			return strings.ToLower(s)
		}
	}
	return "python"
}

// jupyterCellMagicRe captures the language token from a leading
// `%%lang ...` line in a code cell. Cell magics override the
// notebook's kernel language for that cell.
var jupyterCellMagicRe = regexp.MustCompile(`(?m)\A\s*%%(\w+)`)

// emitIPYNBCells materialises one graph node per cell. archivePath
// is the entry path inside a `.dbc` archive (empty for free-standing
// `.ipynb` files); it disambiguates per-cell IDs across archive
// entries that share an outer file.
func emitIPYNBCells(filePath, fileID, archivePath, notebookLang string, cells []ipynbCell, result *parser.ExtractionResult) {
	for i, c := range cells {
		body := string(c.Source)
		cellKind := strings.ToLower(strings.TrimSpace(c.CellType))
		if cellKind == "" {
			cellKind = "code"
		}
		cellLang := notebookLang
		if c.Language != "" {
			cellLang = strings.ToLower(c.Language)
		}
		switch cellKind {
		case "markdown":
			cellLang = "markdown"
		case "raw":
			cellLang = "raw"
		case "heading":
			cellLang = "markdown"
		case "code":
			if m := jupyterCellMagicRe.FindStringSubmatch(body); m != nil {
				cellLang = mapJupyterMagic(m[1], cellLang)
			}
		}
		emitNotebookCell(filePath, fileID, archivePath, i, cellKind, cellLang, body, result)
	}
}

// mapJupyterMagic normalises an IPython cell-magic name to a Gortex
// cell_language label. Execution magics (%%time, %%capture, etc.)
// keep the surrounding kernel language. Unrecognised magics pass
// through lowercased.
func mapJupyterMagic(magic, defaultLang string) string {
	switch strings.ToLower(magic) {
	case "sql":
		return "sql"
	case "bash", "sh":
		return "bash"
	case "html":
		return "html"
	case "javascript", "js":
		return "javascript"
	case "scala":
		return "scala"
	case "r":
		return "r"
	case "python", "py", "python2", "python3":
		return "python"
	case "writefile", "capture", "time", "timeit", "prun", "matplotlib", "system":
		return defaultLang
	}
	return strings.ToLower(magic)
}

// emitNotebookCell appends a cell node + EdgeDefines from the file
// node. Code cells → KindFunction (so kind:function search finds
// them); markdown / raw / heading cells → KindVariable.
func emitNotebookCell(filePath, fileID, archivePath string, index int, cellKind, cellLang, body string, result *parser.ExtractionResult) {
	name := fmt.Sprintf("cell_%d", index)
	if cellKind != "" && cellKind != "code" {
		name = fmt.Sprintf("%s_cell_%d", cellKind, index)
	}
	id := filePath + "::" + name
	if archivePath != "" {
		id = filePath + "::" + archivePath + "::" + name
	}
	startLine := 1
	endLine := max(startLine, startLine+notebookBodyLines(body)-1)
	kind := graph.KindVariable
	if cellKind == "code" {
		kind = graph.KindFunction
	}
	meta := map[string]any{
		"cell_index":    index,
		"cell_kind":     cellKind,
		"cell_language": cellLang,
	}
	if archivePath != "" {
		meta["archive_member"] = archivePath
	}
	result.Nodes = append(result.Nodes, &graph.Node{
		ID: id, Kind: kind, Name: name,
		FilePath: filePath, StartLine: startLine, EndLine: endLine,
		Language: cellLang, Meta: meta,
	})
	result.Edges = append(result.Edges, &graph.Edge{
		From: fileID, To: id, Kind: graph.EdgeDefines,
		FilePath: filePath, Line: startLine,
	})
}

// notebookBodyLines counts the \n-delimited lines in a cell body
// with a floor of 1 so single-line and empty cells still get a
// well-defined endLine.
func notebookBodyLines(s string) int {
	if s == "" {
		return 1
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return max(n, 1)
}

// ---------------------------------------------------------------------------
// Databricks source-format notebooks
// ---------------------------------------------------------------------------

// databricksMagicHeaderFor builds a magic-header regex that matches
// only the comment style for one language family. Per language:
//
//	python / r : `# Databricks notebook source`
//	scala      : `// Databricks notebook source`
//	sql        : `-- Databricks notebook source`
//
// Case-insensitive on the marker phrase, strict on the comment prefix
// — a Scala-style `// Databricks notebook source` in a `.py` file
// must NOT be classified as a Databricks notebook (it would crash
// the Python parser otherwise).
func databricksMagicHeaderFor(marker string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(marker) + `\s*Databricks notebook source`)
}

// databricksLangFromExt maps a Databricks source-format extension to
// its host language. Used as the default cell language for code
// cells that don't carry a `%magic` override.
func databricksLangFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".py":
		return "python"
	case ".scala":
		return "scala"
	case ".sql":
		return "sql"
	case ".r":
		return "r"
	}
	return ""
}

// databricksCommentMarker returns the line-comment marker that
// introduces `COMMAND ----------` and `MAGIC %lang` lines for a
// given Databricks source-format extension.
func databricksCommentMarker(ext string) string {
	switch strings.ToLower(ext) {
	case ".py", ".r":
		return "#"
	case ".scala":
		return "//"
	case ".sql":
		return "--"
	}
	return ""
}

// IsDatabricksSourceFile reports whether src is a Databricks
// source-format notebook (its first non-blank line is the Databricks
// magic header for the file's comment style). Cheap; reads only
// the first non-blank line.
func IsDatabricksSourceFile(filePath string, src []byte) bool {
	ext := filepath.Ext(filePath)
	if databricksLangFromExt(ext) == "" {
		return false
	}
	marker := databricksCommentMarker(ext)
	if marker == "" {
		return false
	}
	trimmed := bytes.TrimLeft(src, " \t\r\n")
	if i := bytes.IndexByte(trimmed, '\n'); i >= 0 {
		trimmed = trimmed[:i]
	}
	return databricksMagicHeaderFor(marker).Match(trimmed)
}

// MaybeEnrichDatabricks adds Databricks cell-level nodes to result
// when filePath is a Databricks source-format notebook. Returns
// true when cells were emitted. The host-language extractor
// (PythonExtractor / ScalaExtractor / SQLExtractor / RExtractor)
// calls this at the end of its Extract pass — the cell nodes ride
// alongside the host extractor's regular symbol nodes.
//
// fileID is the host extractor's file-node ID (typically filePath).
// Cell IDs use a `dbx_cell_<i>` prefix so they can't collide with
// the host extractor's symbol IDs.
func MaybeEnrichDatabricks(filePath, fileID string, src []byte, result *parser.ExtractionResult) bool {
	ext := filepath.Ext(filePath)
	hostLang := databricksLangFromExt(ext)
	if hostLang == "" {
		return false
	}
	if !IsDatabricksSourceFile(filePath, src) {
		return false
	}
	marker := databricksCommentMarker(ext)
	cells := splitDatabricksCells(src, marker)
	for i, cell := range cells {
		cellLang, body := classifyDatabricksCell(cell.body, marker, hostLang)
		cellKind := "code"
		if cellLang == "markdown" {
			cellKind = "markdown"
		}
		id := filePath + "::dbx_cell_" + fmt.Sprint(i)
		endLine := max(cell.startLine, cell.startLine+notebookBodyLines(body)-1)
		kind := graph.KindFunction
		if cellKind != "code" {
			kind = graph.KindVariable
		}
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: fmt.Sprintf("dbx_cell_%d", i),
			FilePath: filePath, StartLine: cell.startLine, EndLine: endLine,
			Language: cellLang,
			Meta: map[string]any{
				"cell_index":    i,
				"cell_kind":     cellKind,
				"cell_language": cellLang,
				"notebook":      "databricks",
				"host_language": hostLang,
			},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: cell.startLine,
		})
	}
	return len(cells) > 0
}

// databricksCell is one logical cell carved out of a Databricks
// source-format notebook.
type databricksCell struct {
	startLine int    // 1-based
	body      string // verbatim cell contents (magic prefixes intact)
}

// splitDatabricksCells splits src on the language-appropriate
// `COMMAND ----------` separator. The first cell starts after the
// magic header line. Blank-only segments are dropped.
func splitDatabricksCells(src []byte, marker string) []databricksCell {
	// The trailing `-+` allows any number of dashes — Databricks
	// canonically emits 10 but older exports vary.
	sep := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(marker) + `\s*COMMAND\s*-+\s*$`)
	lines := strings.Split(string(src), "\n")

	// Walk past blank lines and the first comment-prefixed line —
	// the magic header — so cell 0 starts at the first real content.
	startIdx := 0
	skippedHeader := false
	for startIdx < len(lines) {
		line := strings.TrimSpace(lines[startIdx])
		if line == "" {
			startIdx++
			continue
		}
		if !skippedHeader && strings.HasPrefix(line, marker) {
			skippedHeader = true
			startIdx++
			continue
		}
		break
	}

	var sepIdx []int
	for i := startIdx; i < len(lines); i++ {
		if sep.MatchString(lines[i]) {
			sepIdx = append(sepIdx, i)
		}
	}

	var cells []databricksCell
	cellStart := startIdx
	addCell := func(from, to int) {
		// Skip leading-blank lines so startLine points at content.
		for from < to && strings.TrimSpace(lines[from]) == "" {
			from++
		}
		if from >= to {
			return
		}
		body := strings.TrimRight(strings.Join(lines[from:to], "\n"), "\n")
		if strings.TrimSpace(body) == "" {
			return
		}
		cells = append(cells, databricksCell{startLine: from + 1, body: body})
	}
	for _, idx := range sepIdx {
		addCell(cellStart, idx)
		cellStart = idx + 1
	}
	addCell(cellStart, len(lines))
	return cells
}

// classifyDatabricksCell inspects a cell body for a leading
// `MAGIC %lang ...` block. When present, every `MAGIC` line is
// stripped of its prefix and the cell language switches to the
// declared one. When absent, the cell stays in the host language.
func classifyDatabricksCell(body, marker, hostLang string) (cellLang, cleanBody string) {
	lines := strings.Split(body, "\n")
	magicPrefix := marker + " MAGIC"
	magicPrefixAlt := marker + "MAGIC"
	isMagicLine := func(line string) bool {
		s := strings.TrimLeft(line, " \t")
		return strings.HasPrefix(s, magicPrefix) || strings.HasPrefix(s, magicPrefixAlt)
	}
	stripMagic := func(line string) string {
		s := strings.TrimLeft(line, " \t")
		if rest, ok := strings.CutPrefix(s, magicPrefix); ok {
			s = rest
		} else if rest, ok := strings.CutPrefix(s, magicPrefixAlt); ok {
			s = rest
		}
		return strings.TrimLeft(s, " \t")
	}

	if len(lines) == 0 || !isMagicLine(lines[0]) {
		return hostLang, body
	}

	first := stripMagic(lines[0])
	lang := hostLang
	if token, ok := strings.CutPrefix(first, "%"); ok {
		if i := strings.IndexAny(token, " \t"); i >= 0 {
			token = token[:i]
		}
		lang = strings.ToLower(strings.TrimSpace(token))
	}

	var out []string
	for i, line := range lines {
		if isMagicLine(line) {
			stripped := stripMagic(line)
			if i == 0 && strings.HasPrefix(stripped, "%") {
				// Drop the bare `%lang` directive — it's metadata.
				continue
			}
			out = append(out, stripped)
			continue
		}
		out = append(out, line)
	}

	return databricksMagicLang(lang), strings.Join(out, "\n")
}

// databricksMagicLang normalises a Databricks `%magic` token to a
// Gortex cell_language label. Unrecognised tokens pass through
// lowercased so non-canonical magics still surface.
func databricksMagicLang(s string) string {
	switch s {
	case "py", "python":
		return "python"
	case "scala":
		return "scala"
	case "r":
		return "r"
	case "sql":
		return "sql"
	case "md", "markdown":
		return "markdown"
	case "sh", "bash":
		return "bash"
	case "fs":
		return "fs"
	case "run":
		return "run"
	}
	return strings.ToLower(s)
}

// ---------------------------------------------------------------------------
// .dbc archive extraction
// ---------------------------------------------------------------------------

// extractDBCArchive opens src as a ZIP archive and emits cells for
// every notebook entry inside. Supports `.ipynb` (nbformat) and
// Databricks-native JSON (`commands[]`). Non-notebook entries are
// ignored. Total uncompressed bytes are capped at dbcZipMaxBytes.
func extractDBCArchive(filePath, fileID string, src []byte, result *parser.ExtractionResult) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return
	}
	var consumed int64
	for _, f := range zr.File {
		if consumed >= dbcZipMaxBytes {
			return
		}
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext != ".ipynb" && ext != ".json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(rc, dbcZipMaxBytes-consumed))
		_ = rc.Close()
		if err != nil {
			continue
		}
		consumed += int64(len(body))
		if cells, lang, ok := parseDBCNotebookJSON(body); ok {
			emitIPYNBCells(filePath, fileID, f.Name, lang, cells, result)
			continue
		}
		notebookLang, ipycells := parseIPYNB(body)
		if len(ipycells) == 0 {
			continue
		}
		emitIPYNBCells(filePath, fileID, f.Name, notebookLang, ipycells, result)
	}
}

// dbcNotebook is the minimal Databricks-native notebook shape we
// extract. The format is undocumented but observable in any
// Databricks export: a top-level object with `language` and a
// `commands[]` array.
type dbcNotebook struct {
	Language string       `json:"language"`
	Commands []dbcCommand `json:"commands"`
}

// dbcCommand is one cell in the Databricks-native format.
type dbcCommand struct {
	Command  string `json:"command"`
	Language string `json:"language"`
	SubType  string `json:"subtype"`
}

// parseDBCNotebookJSON decodes a Databricks-native notebook JSON.
// Returns ok=false when the shape doesn't match so the caller can
// fall back to nbformat.
func parseDBCNotebookJSON(src []byte) (cells []ipynbCell, notebookLang string, ok bool) {
	var nb dbcNotebook
	if err := json.Unmarshal(src, &nb); err != nil {
		return nil, "", false
	}
	if len(nb.Commands) == 0 {
		return nil, "", false
	}
	notebookLang = strings.ToLower(nb.Language)
	cells = make([]ipynbCell, 0, len(nb.Commands))
	for _, c := range nb.Commands {
		cellLang := strings.ToLower(c.Language)
		if cellLang == "" {
			cellLang = notebookLang
		}
		kind := "code"
		if strings.EqualFold(c.SubType, "markdownCommand") {
			kind = "markdown"
			cellLang = "markdown"
		}
		cells = append(cells, ipynbCell{
			CellType: kind,
			Source:   stringOrList(c.Command),
			Language: cellLang,
		})
	}
	return cells, notebookLang, true
}

var _ parser.Extractor = (*JupyterExtractor)(nil)
