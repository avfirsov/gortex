package languages

import (
	"strconv"
	"strings"

	"github.com/zzet/gortex/internal/parser"
)

// delegateInlineScriptSlice runs delegate over a carved inline-script slice and
// merges its symbols/edges into result, rebased into the host file's coordinate
// space. content is the raw script body; lineOffset is the 0-based line in the
// host file where the slice begins. Every delegated node/edge line is shifted by
// lineOffset, the synthetic file node is dropped, file-defines edges are
// repointed at fileID, nodes are relabeled to the host filePath (and language,
// when non-empty), and Meta["inline_script"]=true is stamped so downstream
// passes can tell a carved symbol from a natively-parsed one.
//
// It is the shared spine behind every markup extractor that embeds another
// language (HTML/Vue/Svelte/Astro <script>, Razor @code), so the error-prone
// offset-rebase math lives — and is tested — in exactly one place.
func delegateInlineScriptSlice(delegate parser.Extractor, content []byte, lineOffset int, filePath, fileID, language string, result *parser.ExtractionResult) {
	if delegate == nil || result == nil || strings.TrimSpace(string(content)) == "" {
		return
	}
	virtual := filePath + "#script:" + strconv.Itoa(lineOffset+1)
	sub, err := delegate.Extract(virtual, content)
	if err != nil || sub == nil {
		return
	}
	for _, n := range sub.Nodes {
		if n == nil || n.ID == virtual { // drop the synthetic file node
			continue
		}
		n.FilePath = filePath
		if language != "" {
			n.Language = language
		}
		if n.StartLine > 0 {
			n.StartLine += lineOffset
		}
		if n.EndLine > 0 {
			n.EndLine += lineOffset
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta["inline_script"] = true
		result.Nodes = append(result.Nodes, n)
	}
	for _, ed := range sub.Edges {
		if ed == nil {
			continue
		}
		if ed.From == virtual { // "file defines symbol" → the host file owns it
			ed.From = fileID
		}
		ed.FilePath = filePath
		if ed.Line > 0 {
			ed.Line += lineOffset
		}
		result.Edges = append(result.Edges, ed)
	}
}
