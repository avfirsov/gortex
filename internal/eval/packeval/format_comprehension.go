package packeval

import (
	"fmt"
	"sort"
	"strings"
)

// LLM-format-comprehension: a packed context is only useful if the model
// can actually read it. The same symbols rendered as JSON, GCX1, TOON,
// or Markdown cost different tokens and read differently; this measures
// whether an LLM can still answer a grounded question from each format.
// It is the comprehension half of the packing eval — density without
// comprehension is a false economy.

// Asker runs one prompt against an LLM and returns its raw answer. It is
// injected so the harness stays provider-agnostic: the CLI wires it to
// the configured llm.Service; tests pass a deterministic stub. A nil
// Asker means "no provider" and RunFormatComprehension reports skipped.
type Asker func(prompt string) (string, error)

// FormatRenderer renders a packed context (a list of symbol entries)
// into a wire format's textual form. Keyed by format name.
type FormatRenderer func(entries []ContextEntry) string

// ContextEntry is one symbol in a packed context used for the
// comprehension probe: enough to render and to ground a question.
type ContextEntry struct {
	ID        string
	Name      string
	FilePath  string
	Signature string
	Source    string
}

// ComprehensionQuestion is a grounded Q/A whose answer is derivable from
// the packed context. Correctness is a case-insensitive substring match
// of any Accept string in the model's answer — robust to phrasing.
type ComprehensionQuestion struct {
	Question string
	Accept   []string
}

// FormatResult is one format's comprehension score.
type FormatResult struct {
	Format    string  `json:"format"`
	Asked     int     `json:"asked"`
	Correct   int     `json:"correct"`
	Accuracy  float64 `json:"accuracy"`
	Tokens    int     `json:"tokens"`
	Skipped   string  `json:"skipped,omitempty"`
}

// ComprehensionReport bundles per-format results.
type ComprehensionReport struct {
	Questions int            `json:"questions"`
	Formats   []FormatResult `json:"formats"`
}

// RunFormatComprehension renders the packed context in each format and
// asks the model every question, scoring comprehension per format. A nil
// or failing asker yields a skipped result per format (never an error),
// so the probe degrades cleanly when no LLM provider is configured.
func RunFormatComprehension(
	entries []ContextEntry,
	questions []ComprehensionQuestion,
	renderers map[string]FormatRenderer,
	tokenCount func(string) int,
	ask Asker,
) ComprehensionReport {
	rep := ComprehensionReport{Questions: len(questions)}
	formats := make([]string, 0, len(renderers))
	for f := range renderers {
		formats = append(formats, f)
	}
	sort.Strings(formats)

	for _, format := range formats {
		rendered := renderers[format](entries)
		fr := FormatResult{Format: format}
		if tokenCount != nil {
			fr.Tokens = tokenCount(rendered)
		}
		if ask == nil {
			fr.Skipped = "no LLM provider"
			rep.Formats = append(rep.Formats, fr)
			continue
		}
		for _, q := range questions {
			fr.Asked++
			prompt := buildComprehensionPrompt(format, rendered, q.Question)
			answer, err := ask(prompt)
			if err != nil {
				continue // a provider error counts as an unanswered (incorrect) probe
			}
			if answerAccepts(answer, q.Accept) {
				fr.Correct++
			}
		}
		if fr.Asked > 0 {
			fr.Accuracy = float64(fr.Correct) / float64(fr.Asked)
		}
		rep.Formats = append(rep.Formats, fr)
	}
	return rep
}

func buildComprehensionPrompt(format, rendered, question string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are given a code-context pack in %s format.\n", format)
	b.WriteString("Answer the question using ONLY the pack. Reply with just the answer, no preamble.\n\n")
	b.WriteString("=== CONTEXT PACK ===\n")
	b.WriteString(rendered)
	b.WriteString("\n=== END PACK ===\n\n")
	fmt.Fprintf(&b, "Question: %s\nAnswer:", question)
	return b.String()
}

func answerAccepts(answer string, accept []string) bool {
	a := strings.ToLower(answer)
	for _, want := range accept {
		if want == "" {
			continue
		}
		if strings.Contains(a, strings.ToLower(want)) {
			return true
		}
	}
	return false
}

// ComprehensionMarkdown renders the comprehension report.
func ComprehensionMarkdown(rep ComprehensionReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Format comprehension (%d questions)\n\n", rep.Questions)
	b.WriteString("| format | accuracy | correct/asked | tokens |\n")
	b.WriteString("|--------|----------|---------------|--------|\n")
	for _, f := range rep.Formats {
		if f.Skipped != "" {
			fmt.Fprintf(&b, "| %s | — | — | %d (skipped: %s) |\n", f.Format, f.Tokens, f.Skipped)
			continue
		}
		fmt.Fprintf(&b, "| %s | %5.1f%% | %d/%d | %d |\n",
			f.Format, f.Accuracy*100, f.Correct, f.Asked, f.Tokens)
	}
	return b.String()
}
