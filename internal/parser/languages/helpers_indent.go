package languages

// findIndentedBlockEnd returns the 1-based line number of the last
// line belonging to an indentation-delimited block that starts at
// startLine. It scans forward from the first content line inside the
// block, records its indent, and returns the line before the first
// subsequent non-empty line whose indent drops to or below the
// starting line's indent.
//
// Used by regex-based extractors for languages without brace
// delimiters: Verse, GDScript, Fortran (as a fallback), Nix
// `let ... in` scopes, Python (when tree-sitter is unavailable).
//
// Falls back to startLine when no content lines follow so callers
// always get a sensible range.
func findIndentedBlockEnd(lines []string, startLine int) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	startIndent := leadingIndent(lines[startLine-1])
	// Walk forward; first non-empty line sets the block indent.
	blockIndent := -1
	lastContent := startLine
	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		if trimmed(line) == "" {
			continue
		}
		indent := leadingIndent(line)
		if blockIndent == -1 {
			if indent <= startIndent {
				// No deeper block — single-line definition.
				return startLine
			}
			blockIndent = indent
			lastContent = i + 1
			continue
		}
		if indent < blockIndent || (indent <= startIndent && indent <= blockIndent) {
			return lastContent
		}
		lastContent = i + 1
	}
	return lastContent
}

func leadingIndent(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// findKeywordBlockEnd scans forward from startLine for the first line
// whose trimmed prefix matches any of the given end-keywords
// (case-insensitive). Used for languages with explicit block
// terminators like Fortran's `end subroutine` / `end function` /
// `end module`, SQL's `end;`, and VB-ish `End Sub`.
//
// Returns startLine when no terminator is found, matching the
// convention of findBlockEnd.
func findKeywordBlockEnd(lines []string, startLine int, endKeywords ...string) int {
	if startLine < 1 || startLine > len(lines) {
		return startLine
	}
	for i := startLine; i < len(lines); i++ {
		line := trimmed(lines[i])
		if line == "" {
			continue
		}
		lower := toLower(line)
		for _, kw := range endKeywords {
			if hasPrefixWord(lower, kw) {
				return i + 1
			}
		}
	}
	return startLine
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// hasPrefixWord reports whether s begins with kw followed by end-of-
// string, whitespace, or punctuation. Avoids matching `enddo` when
// looking for `end`.
func hasPrefixWord(s, kw string) bool {
	if len(s) < len(kw) {
		return false
	}
	if s[:len(kw)] != kw {
		return false
	}
	if len(s) == len(kw) {
		return true
	}
	c := s[len(kw)]
	return c == ' ' || c == '\t' || c == ';' || c == '(' || c == ':'
}

func trimmed(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
