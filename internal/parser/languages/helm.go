package languages

import (
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Helm extraction gives Gortex a semantic view of a Helm chart: the
// chart itself (Chart.yaml) with its cross-chart dependencies, the
// named templates defined in `_helpers.tpl` / other `.tpl` files, and
// the include/template call edges between named templates (and from
// render manifests in templates/*.yaml into the helpers they pull in).
//
// Named templates are chart-wide: a `{{ define "x" }}` in any file and
// an `{{ include "x" }}` in any other file refer to the same template,
// so template nodes use a chart-canonical ID keyed only by the template
// name ("helm::template::"+NAME). include/template edges therefore
// resolve across files by ID without a second pass.
var (
	// {{- define "NAME" -}}  /  {{ define "NAME" }} — tolerant of the
	// {{- / -}} whitespace-trim markers and of single/double quotes.
	helmDefineRe = regexp.MustCompile(`\{\{-?\s*define\s+["']([^"']+)["']\s*-?\}\}`)
	// {{ end }} — closes a define (and other block actions). We pair
	// define/end with a depth counter so include calls land on the
	// innermost enclosing define.
	helmEndRe = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	// {{ include "NAME" . }} and {{ template "NAME" . }} — the two ways
	// a template invokes another named template. The trailing context
	// argument and any pipeline after it are ignored.
	helmIncludeRe = regexp.MustCompile(`\{\{-?\s*(?:include|template)\s+["']([^"']+)["']`)
)

// HelmExtractor extracts Helm charts and Go-template named templates.
type HelmExtractor struct{}

func NewHelmExtractor() *HelmExtractor { return &HelmExtractor{} }

func (e *HelmExtractor) Language() string { return "helm" }

// Extensions claims Helm's named-template files (`.tpl`) and the
// `Chart.yaml` basename. `.tpl` is claimed ahead of the forest gotmpl
// grammar (registered earlier in RegisterAll) so the hand-written
// semantic layer wins. `.gotmpl` / `.tmpl` are deliberately left to the
// forest gotmpl grammar — Helm charts use `.tpl`, and those generic
// Go-template extensions are rarely Helm; YAML render templates with
// inline includes are handled by the yaml.go augmentation hook.
func (e *HelmExtractor) Extensions() []string {
	return []string{".tpl", "Chart.yaml"}
}

func (e *HelmExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	if filepath.Base(filePath) == "Chart.yaml" {
		return e.extractChart(filePath, src)
	}
	return e.extractTemplate(filePath, src)
}

// extractChart parses a Chart.yaml manifest: it emits a KindPackage
// chart node and an EdgeDependsOn to each declared dependency chart.
func (e *HelmExtractor) extractChart(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}

	endLine := lineAt(src, len(src))
	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: endLine,
		Language: "helm",
	}
	result.Nodes = append(result.Nodes, fileNode)

	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		// Malformed Chart.yaml: keep the file node so the file still
		// indexes, but emit no chart semantics.
		return result, nil
	}
	root := documentMapping(&doc)
	if root == nil {
		return result, nil
	}

	chartName := scalarOf(mappingGet(root, "name"))
	if chartName == "" {
		return result, nil
	}
	version := scalarOf(mappingGet(root, "version"))

	chartID := helmChartID(chartName, version)
	chartMeta := map[string]any{"helm_kind": "chart"}
	if version != "" {
		chartMeta["version"] = version
	}
	nameLine := 1
	if nameKey := mappingGetKey(root, "name"); nameKey != nil && nameKey.Line > 0 {
		nameLine = nameKey.Line
	}
	result.Nodes = append(result.Nodes, &graph.Node{
		ID: chartID, Kind: graph.KindPackage, Name: chartName,
		FilePath: filePath, StartLine: nameLine, EndLine: endLine,
		Language: "helm",
		Meta:     chartMeta,
	})
	result.Edges = append(result.Edges, &graph.Edge{
		From: fileNode.ID, To: chartID, Kind: graph.EdgeDefines,
		FilePath: filePath, Line: nameLine,
	})

	// dependencies: [ {name, version, repository}, ... ] — emit a
	// cross-chart EdgeDependsOn to each dependency's chart node. The
	// target ID is keyed by name only (no version) so it resolves to a
	// dependency chart regardless of the exact version pinned here.
	for _, dep := range sequenceItems(mappingGet(root, "dependencies")) {
		depName := scalarOf(mappingGet(dep, "name"))
		if depName == "" {
			continue
		}
		depLine := dep.Line
		if depLine <= 0 {
			depLine = nameLine
		}
		depMeta := map[string]any{"helm_kind": "dependency"}
		if depVer := scalarOf(mappingGet(dep, "version")); depVer != "" {
			depMeta["version"] = depVer
		}
		if repo := scalarOf(mappingGet(dep, "repository")); repo != "" {
			depMeta["repository"] = repo
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: chartID, To: "helm::chart::" + depName,
			Kind:     graph.EdgeDependsOn,
			FilePath: filePath, Line: depLine,
			Meta: depMeta,
		})
	}

	return result, nil
}

// extractTemplate scans a `.tpl` / `.gotmpl` file for `define` blocks
// (KindFunction nodes) and the include/template calls between them.
func (e *HelmExtractor) extractTemplate(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}

	endLine := lineAt(src, len(src))
	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: endLine,
		Language: "helm",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	for _, m := range helmDefineRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if name == "" {
			continue
		}
		id := "helm::template::" + name
		line := lineAt(src, m[0])
		if !seen[id] {
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindFunction, Name: name,
				FilePath: filePath, StartLine: line, EndLine: line,
				Language: "helm",
				Meta:     map[string]any{"helm_kind": "named_template"},
			})
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: line,
		})
	}

	helmScanCalls(filePath, fileNode.ID, src, result)
	return result, nil
}

// helmScanCalls walks the template directives in document order and
// emits an EdgeCalls for every include/template invocation. The caller
// is the innermost enclosing `define` block (resolved by pairing
// define/end with a depth counter); when a call sits outside any define
// the file node is the caller.
func helmScanCalls(filePath, fileID string, src []byte, result *parser.ExtractionResult) {
	// Collect the boundary directives (define-open / end) and the call
	// sites, all keyed by byte offset, then merge them in offset order.
	type marker struct {
		pos  int
		kind int    // 0 = define-open, 1 = end, 2 = call
		name string // template name for define-open and call
	}
	var markers []marker
	for _, m := range helmDefineRe.FindAllSubmatchIndex(src, -1) {
		markers = append(markers, marker{pos: m[0], kind: 0, name: string(src[m[2]:m[3]])})
	}
	for _, m := range helmEndRe.FindAllIndex(src, -1) {
		markers = append(markers, marker{pos: m[0], kind: 1})
	}
	for _, m := range helmIncludeRe.FindAllSubmatchIndex(src, -1) {
		markers = append(markers, marker{pos: m[0], kind: 2, name: string(src[m[2]:m[3]])})
	}
	// Insertion sort by byte offset — the directive spans are disjoint,
	// so offsets never collide and ordering is total.
	for i := 1; i < len(markers); i++ {
		for j := i; j > 0 && markers[j-1].pos > markers[j].pos; j-- {
			markers[j-1], markers[j] = markers[j], markers[j-1]
		}
	}

	// defineStack holds the IDs of the open define blocks. Note: `end`
	// also closes if/range/with blocks, so the stack can underflow
	// relative to defines; we only push for define and pop on any end,
	// which keeps the innermost-define attribution correct for the
	// common, well-formed helper layouts Helm charts use.
	var defineStack []string
	for _, mk := range markers {
		switch mk.kind {
		case 0: // define-open
			defineStack = append(defineStack, "helm::template::"+mk.name)
		case 1: // end
			if n := len(defineStack); n > 0 {
				defineStack = defineStack[:n-1]
			}
		case 2: // include/template call
			if mk.name == "" {
				continue
			}
			from := fileID
			if n := len(defineStack); n > 0 {
				from = defineStack[n-1]
			}
			result.Edges = append(result.Edges, &graph.Edge{
				From: from, To: "helm::template::" + mk.name,
				Kind:     graph.EdgeCalls,
				FilePath: filePath, Line: lineAt(src, mk.pos),
			})
		}
	}
}

// helmTemplateCallsFromYAML scans a render manifest (templates/*.yaml)
// for include/template directives and emits an EdgeCalls from the file
// to each invoked named template. It is additive: the YAML extractor
// calls it before its normal dispatch so the file still gets its
// config-key / resource nodes. Render manifests are never inside a
// `define`, so the caller is always the file node.
func helmTemplateCallsFromYAML(filePath, fileID string, src []byte, result *parser.ExtractionResult) {
	for _, m := range helmIncludeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if name == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: "helm::template::" + name,
			Kind:     graph.EdgeCalls,
			FilePath: filePath, Line: lineAt(src, m[0]),
		})
	}
}

// helmChartID is the canonical ID for a Helm chart node. The version,
// when present, is appended so charts with the same name but different
// versions stay distinct; dependency edges target the version-less form
// ("helm::chart::"+name) so they resolve regardless of the pinned range.
func helmChartID(name, version string) string {
	if version != "" {
		return "helm::chart::" + name + "@" + version
	}
	return "helm::chart::" + name
}

// mappingGetKey returns the *key* node for the given key inside a
// MappingNode (mappingGet returns the value node). Used to anchor the
// chart node to the line of its `name:` key.
func mappingGetKey(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k != nil && k.Value == key {
			return k
		}
	}
	return nil
}

var _ parser.Extractor = (*HelmExtractor)(nil)
