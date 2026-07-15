package graph

import "strings"

const (
	metaRetrievalSignature  = "search_signature"
	metaRetrievalQualName   = "search_qual_name"
	metaRetrievalDoc        = "search_doc"
	metaRetrievalSuppressed = "search_metadata_suppressed"
	maxRetrievalSignature   = 512
)

// RetrievalMetadata is the search/display projection of parser-owned symbol
// metadata. It is deliberately separate from Node.QualName and the parser's
// signature/doc values so retrieval heuristics cannot alter resolver identity.
type RetrievalMetadata struct {
	Signature string
	QualName  string
	Doc       string
}

// RetrievalMetadata returns normalized retrieval fields, falling back to the
// parser-owned values for graph snapshots created before normalization.
// QualName is empty unless the parser or normalizer had semantic owner evidence.
func (n *Node) RetrievalMetadata() RetrievalMetadata {
	if n == nil {
		return RetrievalMetadata{}
	}
	// Child scopes frequently inherit their owner's source span and parser
	// metadata. Never expose that declaration payload as parameter/local search
	// metadata, including when reading snapshots created before normalization.
	if retrievalMetadataSuppressed(n) {
		return RetrievalMetadata{}
	}
	qualName := firstMetadataString(n.Meta, metaRetrievalQualName, "")
	if qualName == "" {
		qualName = n.QualName
	}
	return RetrievalMetadata{
		Signature: compactRetrievalSignature(firstMetadataString(n.Meta, metaRetrievalSignature, "signature"), n.Language),
		QualName:  qualName,
		Doc:       firstMetadataString(n.Meta, metaRetrievalDoc, "doc"),
	}
}

// SetRetrievalMetadata replaces the retrieval-only projection. Empty values
// remove prior projections; parser-owned metadata and Node.QualName are never
// modified.
func SetRetrievalMetadata(n *Node, metadata RetrievalMetadata) {
	if n == nil {
		return
	}
	if n.Meta == nil {
		if metadata.Signature == "" && metadata.QualName == "" && metadata.Doc == "" {
			return
		}
		n.Meta = make(map[string]any)
	}
	delete(n.Meta, metaRetrievalSuppressed)
	setMetadataString(n.Meta, metaRetrievalSignature, compactRetrievalSignature(metadata.Signature, n.Language))
	setMetadataString(n.Meta, metaRetrievalQualName, metadata.QualName)
	setMetadataString(n.Meta, metaRetrievalDoc, metadata.Doc)
}

// SuppressRetrievalMetadata marks a parser node as intentionally absent from
// search metadata without deleting parser-owned signature, doc, or QualName.
func SuppressRetrievalMetadata(n *Node) {
	if n == nil {
		return
	}
	if n.Meta == nil {
		n.Meta = make(map[string]any)
	}
	n.Meta[metaRetrievalSuppressed] = true
	delete(n.Meta, metaRetrievalSignature)
	delete(n.Meta, metaRetrievalQualName)
	delete(n.Meta, metaRetrievalDoc)
}

func retrievalMetadataSuppressed(n *Node) bool {
	if n == nil {
		return true
	}
	if suppressed, _ := n.Meta[metaRetrievalSuppressed].(bool); suppressed {
		return true
	}
	switch n.Kind {
	case KindParam, KindLocal, KindGenericParam:
		return true
	case KindVariable:
		return legacyVariableMetadataLocal(n.Meta)
	default:
		return false
	}
}

func legacyVariableMetadataLocal(meta map[string]any) bool {
	for _, key := range []string{"local", "is_local"} {
		if value, ok := meta[key].(bool); ok && value {
			return true
		}
	}
	for _, key := range []string{"scope", "scope_kind", "parent_kind", "enclosing_kind", "storage"} {
		value, _ := meta[key].(string)
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "local", "function", "function-local", "method", "closure", "block":
			return true
		}
	}
	return false
}

func compactRetrievalSignature(value, language string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	language = strings.ToLower(strings.TrimSpace(language))
	parenDepth, bracketDepth, angleDepth := 0, 0, 0
	quote := rune(0)
	escaped := false
	cut := len(value)
	for i, r := range value {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'':
			if language == "rust" && retrievalRustLifetimeAt(value, i) {
				continue
			}
			quote = r
		case '"', '`':
			quote = r
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '<':
			if (language == "go" || language == "golang") && i+1 < len(value) && value[i+1] == '-' {
				continue
			}
			angleDepth++
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		case '{', ';':
			if parenDepth == 0 && bracketDepth == 0 && angleDepth == 0 {
				cut = i
			}
		case '\n', '\r':
			if !retrievalLanguageAllowsMultilineSignature(language) && parenDepth == 0 && bracketDepth == 0 && angleDepth == 0 {
				cut = i
			}
		}
		if cut != len(value) {
			break
		}
	}
	return boundedRetrievalString(strings.Join(strings.Fields(value[:cut]), " "), maxRetrievalSignature)
}

func retrievalLanguageAllowsMultilineSignature(language string) bool {
	switch language {
	case "go", "golang", "rust", "java", "kotlin", "typescript", "tsx", "javascript", "jsx", "c", "cpp", "c++", "csharp", "c#", "swift":
		return true
	default:
		return false
	}
}

func retrievalRustLifetimeAt(value string, quote int) bool {
	if quote+1 >= len(value) || !retrievalIdentifierByte(value[quote+1]) {
		return false
	}
	end := quote + 2
	for end < len(value) && retrievalIdentifierByte(value[end]) {
		end++
	}
	return end >= len(value) || value[end] != '\''
}

func retrievalIdentifierByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func boundedRetrievalString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && value[cut]&0xc0 == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}

func firstMetadataString(meta map[string]any, primary, fallback string) string {
	if value, _ := meta[primary].(string); value != "" {
		return value
	}
	if fallback != "" {
		value, _ := meta[fallback].(string)
		return value
	}
	return ""
}

func setMetadataString(meta map[string]any, key, value string) {
	if value == "" {
		delete(meta, key)
		return
	}
	meta[key] = value
}
