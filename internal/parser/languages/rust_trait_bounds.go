package languages

import (
	"strings"
	"unicode"
)

// rustPositiveTraitBounds returns the positive, top-level trait paths from a
// Rust trait_bounds node. Lifetimes and optional `?Trait` bounds do not define
// supertrait relationships and are intentionally omitted.
func rustPositiveTraitBounds(raw string) []string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ":"))
	parts, ok := splitRustPositiveBounds(raw)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "'") || strings.HasPrefix(part, "?") {
			continue
		}
		part = stripRustPositiveBoundPrefix(part)
		if part == "" || strings.HasPrefix(part, "'") || strings.HasPrefix(part, "?") {
			continue
		}
		path := rustPositiveTraitPath(part)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func splitRustPositiveBounds(raw string) ([]string, bool) {
	var parts []string
	start := 0
	angle, paren, bracket, brace := 0, 0, 0, 0
	for i, r := range raw {
		switch r {
		case '<':
			angle++
		case '>':
			if i > 0 && raw[i-1] == '-' {
				continue
			}
			if angle == 0 {
				return nil, false
			}
			angle--
		case '(':
			paren++
		case ')':
			if paren == 0 {
				return nil, false
			}
			paren--
		case '[':
			bracket++
		case ']':
			if bracket == 0 {
				return nil, false
			}
			bracket--
		case '{':
			brace++
		case '}':
			if brace == 0 {
				return nil, false
			}
			brace--
		case '+':
			if angle == 0 && paren == 0 && bracket == 0 && brace == 0 {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	if angle != 0 || paren != 0 || bracket != 0 || brace != 0 {
		return nil, false
	}
	parts = append(parts, raw[start:])
	return parts, true
}

func stripRustPositiveBoundPrefix(part string) string {
	part = strings.TrimSpace(part)
	if strings.HasPrefix(part, "for") {
		rest := strings.TrimSpace(strings.TrimPrefix(part, "for"))
		if strings.HasPrefix(rest, "<") {
			depth := 0
			end := -1
			for i, r := range rest {
				switch r {
				case '<':
					depth++
				case '>':
					if depth == 0 {
						return ""
					}
					depth--
					if depth == 0 {
						end = i + 1
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				return ""
			}
			part = strings.TrimSpace(rest[end:])
		}
	}
	for {
		before := part
		for _, prefix := range []string{"~const ", "const ", "dyn "} {
			if strings.HasPrefix(part, prefix) {
				part = strings.TrimSpace(strings.TrimPrefix(part, prefix))
				break
			}
		}
		if part == before {
			return part
		}
	}
}

func rustPositiveTraitPath(part string) string {
	end := len(part)
	for i, r := range part {
		switch r {
		case '<', '(', '[', '{', '=', ' ', '\t', '\r', '\n':
			end = i
		}
		if end != len(part) {
			break
		}
	}
	path := strings.Trim(strings.TrimSpace(part[:end]), ":")
	if path == "" || strings.ContainsAny(path, "&*!,;") {
		return ""
	}
	segments := strings.Split(path, "::")
	for i, segment := range segments {
		segment = strings.TrimPrefix(segment, "r#")
		if !rustTraitIdentifier(segment) {
			return ""
		}
		segments[i] = segment
	}
	return strings.Join(segments, "::")
}

func rustTraitIdentifier(segment string) bool {
	if segment == "" {
		return false
	}
	for i, r := range segment {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}
