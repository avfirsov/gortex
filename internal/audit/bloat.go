package audit

import "strings"

const (
	bloatLineSoft = 600  // > this begins to cost
	bloatLineHard = 1500 // > this is maxed out
	longLineChars = 200
)

// scoreBloat inspects the file's lines and returns a BloatMetrics with a 0-100
// score. The score is a weighted sum of: file length, long lines, duplicate
// bullets, and list nesting depth.
func scoreBloat(lines []string) BloatMetrics {
	m := BloatMetrics{Lines: len(lines)}

	seenBullets := make(map[string]int)
	inFence := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			m.CodeBlocks++
			continue
		}
		if inFence {
			continue
		}

		if len(line) > longLineChars {
			m.LongLines++
		}

		if bulletBody, depth, ok := parseBullet(line); ok {
			m.Bullets++
			if depth > m.MaxDepth {
				m.MaxDepth = depth
			}
			key := strings.ToLower(bulletBody)
			seenBullets[key]++
		}
	}

	// Code blocks are counted as open+close; collapse to block count.
	m.CodeBlocks = m.CodeBlocks / 2

	for _, n := range seenBullets {
		if n > 1 {
			m.Duplicates += n - 1
		}
	}

	m.Score = computeScore(m)
	return m
}

// parseBullet returns (body, indentDepth, true) for markdown list lines.
func parseBullet(line string) (string, int, bool) {
	leading := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			leading++
		case '\t':
			leading += 4
		default:
			goto done
		}
	}
done:
	rest := line[min(leading, len(line)):]
	// Handle "- ", "* ", "+ ", and "1. "-style.
	switch {
	case strings.HasPrefix(rest, "- "):
		return strings.TrimSpace(rest[2:]), leading/2 + 1, true
	case strings.HasPrefix(rest, "* "):
		return strings.TrimSpace(rest[2:]), leading/2 + 1, true
	case strings.HasPrefix(rest, "+ "):
		return strings.TrimSpace(rest[2:]), leading/2 + 1, true
	}
	// Numeric: up to 3 digits + ". "
	for i := 0; i < len(rest) && i < 3; i++ {
		if rest[i] < '0' || rest[i] > '9' {
			if i > 0 && strings.HasPrefix(rest[i:], ". ") {
				return strings.TrimSpace(rest[i+2:]), leading/2 + 1, true
			}
			break
		}
	}
	return "", 0, false
}

// computeScore combines the bloat signals into a 0-100 score.
func computeScore(m BloatMetrics) int {
	score := 0

	// File length: 0 at <= soft, 50 at >= hard, linear between.
	switch {
	case m.Lines >= bloatLineHard:
		score += 50
	case m.Lines > bloatLineSoft:
		score += (m.Lines - bloatLineSoft) * 50 / (bloatLineHard - bloatLineSoft)
	}

	// Long lines: up to 20 points when 20%+ of bullets are long.
	if m.Bullets > 0 {
		ratio := float64(m.LongLines) / float64(m.Bullets)
		if ratio > 1 {
			ratio = 1
		}
		score += int(ratio * 20)
	} else if m.LongLines > 5 {
		score += 10
	}

	// Duplicates: up to 20 points.
	if m.Bullets > 0 {
		dupRatio := float64(m.Duplicates) / float64(m.Bullets)
		if dupRatio > 1 {
			dupRatio = 1
		}
		score += int(dupRatio * 20)
	}

	// Deep nesting: 10 points if >= 5.
	if m.MaxDepth >= 5 {
		score += 10
	} else if m.MaxDepth >= 4 {
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score
}

// aggregateBloat averages the per-file bloat scores, weighting by file length.
func aggregateBloat(files []FileReport) int {
	if len(files) == 0 {
		return 0
	}
	totalLines := 0
	weighted := 0
	for _, f := range files {
		lines := f.Bloat.Lines
		if lines == 0 {
			lines = 1
		}
		totalLines += lines
		weighted += f.Bloat.Score * lines
	}
	if totalLines == 0 {
		return 0
	}
	return weighted / totalLines
}
