package languages

// lineAt returns the 1-based line number for byte offset pos.
func lineAt(src []byte, pos int) int {
	line := 1
	for i := 0; i < pos && i < len(src); i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}

// findBlockEnd finds the approximate end line of a brace-delimited
// block starting at startLine (1-based). Counts `{` / `}` depth from
// startLine onward and returns the 1-based line where depth first
// drops back to zero — startLine itself when no brace is found.
func findBlockEnd(lines []string, startLine int) int {
	depth := 0
	for i := startLine - 1; i < len(lines); i++ {
		for _, ch := range lines[i] {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth <= 0 {
					return i + 1
				}
			}
		}
	}
	return startLine
}
