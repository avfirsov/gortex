package languages

import (
	"bytes"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// compiledChunkPattern is a ChunkPattern with its regex compiled once
// at registration time — the spec is rejected wholesale if any pattern
// fails to compile, so Extract never re-validates.
type compiledChunkPattern struct {
	kind      graph.NodeKind
	re        *regexp.Regexp
	nameGroup int
}

// RegexChunkerExtractor is a parser.Extractor that derives a coarse
// symbol outline from regex patterns alone — the documented "register a
// regex fallback chunker for a grammar-less language without writing Go"
// entry point. It emits the file node, then one node per pattern match
// (named by the configured capture group) plus an EdgeDefines from the
// file to each. Nodes are deduplicated by ID.
type RegexChunkerExtractor struct {
	language string
	exts     []string
	patterns []compiledChunkPattern
}

func (e *RegexChunkerExtractor) Language() string     { return e.language }
func (e *RegexChunkerExtractor) Extensions() []string { return e.exts }

// Extract scans src with every compiled pattern, emitting one node per
// match and an EdgeDefines from the file. The file node always comes
// first; per-match nodes are deduplicated by ID. Never returns an error
// — a grammar-less outline degrades silently to just the file node when
// nothing matches.
func (e *RegexChunkerExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}
	fileNode := &graph.Node{
		ID:        filePath,
		Kind:      graph.KindFile,
		Name:      filePath,
		FilePath:  filePath,
		StartLine: 1,
		EndLine:   bytes.Count(src, []byte("\n")) + 1,
		Language:  e.language,
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := map[string]bool{filePath: true}
	for _, p := range e.patterns {
		matches := p.re.FindAllSubmatchIndex(src, -1)
		for _, m := range matches {
			// Submatch index 2*g..2*g+1 brackets capture group g. A
			// negative start means the optional group didn't participate.
			gi := 2 * p.nameGroup
			if gi+1 >= len(m) || m[gi] < 0 {
				continue
			}
			name := string(src[m[gi]:m[gi+1]])
			if name == "" {
				continue
			}
			id := filePath + "::" + name
			if seen[id] {
				continue
			}
			seen[id] = true

			// StartLine is computed from the name-capture byte offset
			// (count of newlines before it) rather than the whole-match
			// start: a leading `\s*` in the pattern can pull the match
			// start onto a preceding blank line, while the name capture
			// always sits on the symbol's own line.
			startLine := bytes.Count(src[:m[gi]], []byte("\n")) + 1
			node := &graph.Node{
				ID:        id,
				Kind:      p.kind,
				Name:      name,
				FilePath:  filePath,
				StartLine: startLine,
				Language:  e.language,
			}
			result.Nodes = append(result.Nodes, node)
			result.Edges = append(result.Edges, &graph.Edge{
				From:     filePath,
				To:       id,
				Kind:     graph.EdgeDefines,
				FilePath: filePath,
				Line:     startLine,
			})
		}
	}
	return result, nil
}

var _ parser.Extractor = (*RegexChunkerExtractor)(nil)

// compileChunkPatterns compiles each pattern of a spec, dropping a
// pattern whose Kind is not a valid graph node kind (logged) and
// returning ok=false when any regex fails to compile — the caller then
// skips the whole spec. A NameGroup of zero defaults to group 1.
func compileChunkPatterns(spec config.FallbackChunkerSpec, log *zap.Logger) ([]compiledChunkPattern, bool) {
	out := make([]compiledChunkPattern, 0, len(spec.Patterns))
	for _, p := range spec.Patterns {
		kind := graph.NodeKind(strings.TrimSpace(p.Kind))
		if !graph.ValidNodeKind(kind) {
			log.Warn("fallback chunker: dropping pattern with invalid kind",
				zap.String("language", spec.Language), zap.String("kind", p.Kind))
			continue
		}
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			log.Warn("fallback chunker: skipped — pattern failed to compile",
				zap.String("language", spec.Language), zap.String("regex", p.Regex), zap.Error(err))
			return nil, false
		}
		group := p.NameGroup
		if group <= 0 {
			group = 1
		}
		out = append(out, compiledChunkPattern{kind: kind, re: re, nameGroup: group})
	}
	return out, true
}

// RegisterFallbackChunkers registers every configured regex fallback
// chunker, mirroring RegisterCustomGrammars: a spec whose language or
// any extension collides with a built-in (or already registered)
// extractor is skipped — built-ins win — as is one that fails to
// validate (no usable pattern, or a bad regex), each with a logged
// warning so a typo never aborts startup.
func RegisterFallbackChunkers(reg *parser.Registry, specs []config.FallbackChunkerSpec, log *zap.Logger) {
	if reg == nil {
		return
	}
	if log == nil {
		log = zap.NewNop()
	}
	for _, spec := range specs {
		lang := strings.TrimSpace(spec.Language)
		exts := normalizeGrammarExtensions(spec.Extensions)
		if lang == "" || len(exts) == 0 || len(spec.Patterns) == 0 {
			log.Warn("fallback chunker: skipped — language, extensions and patterns are all required",
				zap.String("language", spec.Language))
			continue
		}
		if _, exists := reg.GetByLanguage(lang); exists {
			log.Warn("fallback chunker: skipped — language already registered by a built-in extractor",
				zap.String("language", lang))
			continue
		}
		if conflict := firstClaimedExtension(reg, exts); conflict != "" {
			log.Warn("fallback chunker: skipped — extension already claimed by a built-in extractor",
				zap.String("language", lang), zap.String("extension", conflict))
			continue
		}
		patterns, ok := compileChunkPatterns(spec, log)
		if !ok || len(patterns) == 0 {
			log.Warn("fallback chunker: skipped — no usable pattern",
				zap.String("language", lang))
			continue
		}
		reg.Register(&RegexChunkerExtractor{language: lang, exts: exts, patterns: patterns})
		log.Info("fallback chunker registered",
			zap.String("language", lang), zap.Strings("extensions", exts),
			zap.Int("patterns", len(patterns)))
	}
}
