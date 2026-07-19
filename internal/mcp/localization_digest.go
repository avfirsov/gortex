package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Terminal evidence retention.
//
// The localize handler builds a byte-budgeted evidence envelope once and
// retains a compact projection for post-terminal calls. Replaying that evidence
// as a successful result keeps non-adapted hosts out of error-recovery loops,
// but the model-visible response must remain evidence rather than masquerade as
// a prewritten answer.

const (
	// localizationDigestMaxBytes bounds retained session state independently of
	// the original envelope budget.
	localizationDigestMaxBytes = 4096
	// localizationReplayEvidenceLimit prevents a broad localization envelope
	// from becoming an exhaustive, implicitly endorsed answer during replay.
	// Five keeps the promoted structural/literal candidates reserved by the
	// envelope builder while bounding repeat-turn cost.
	localizationReplayEvidenceLimit = 5
	// localizationHostFallbackMetaKey is deliberately carried in MCP _meta,
	// which is available to an adapting host without duplicating a prewritten
	// answer in model-visible TextContent or structuredContent.
	localizationHostFallbackMetaKey = "gortex/localization-fallback"
)

// localizationEvidenceDigest is the compact, session-retained projection of
// an answer envelope: ranked candidate evidence without source bodies.
type localizationEvidenceDigest struct {
	Files    []string                `json:"files,omitempty"`
	Symbols  []string                `json:"symbols,omitempty"`
	Evidence []localizationDigestRow `json:"evidence,omitempty"`
}

type localizationDigestRow struct {
	Rank      int      `json:"rank,omitempty"`
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	QualName  string   `json:"qual_name,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	File      string   `json:"file,omitempty"`
	Line      int      `json:"line,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Callers   []string `json:"callers,omitempty"`
	Callees   []string `json:"callees,omitempty"`
}

// newLocalizationEvidenceDigest retains only concrete ranked evidence rows.
// Files and Symbols are rebuilt from those rows, so an item that was shed by
// the replay limit or byte budget cannot survive as an unsupported answer
// candidate. The upstream ordering already reserves the strongest direct,
// exact, literal, and promoted structural targets before lower-ranked fan-out.
func newLocalizationEvidenceDigest(envelope localizationExploreEnvelope) *localizationEvidenceDigest {
	digest := &localizationEvidenceDigest{}
	seen := make(map[string]struct{}, localizationReplayEvidenceLimit)
	for _, row := range envelope.Evidence {
		if len(digest.Evidence) >= localizationReplayEvidenceLimit {
			break
		}
		if row.ID == "" || row.File == "" {
			continue
		}
		if _, exists := seen[row.ID]; exists {
			continue
		}
		seen[row.ID] = struct{}{}
		digest.Evidence = append(digest.Evidence, localizationDigestRow{
			Rank:      row.Rank,
			ID:        row.ID,
			Name:      row.Name,
			QualName:  row.QualName,
			Kind:      row.Kind,
			File:      row.File,
			Line:      row.Line,
			Signature: row.Signature,
			Callers:   append([]string(nil), row.Callers...),
			Callees:   append([]string(nil), row.Callees...),
		})
	}
	for {
		rebuildLocalizationDigestSkeleton(digest)
		encoded, err := json.Marshal(digest)
		if err == nil && len(encoded) <= localizationDigestMaxBytes {
			return digest
		}
		if len(digest.Evidence) == 0 {
			return digest
		}
		last := len(digest.Evidence) - 1
		if shedLocalizationDigestRowOptionalFields(&digest.Evidence[last]) {
			continue
		}
		if last == 0 {
			// ID and file are the irreducible replay contract. They are bounded by
			// filesystem and symbol extraction limits in production, so retain the
			// mandatory row rather than returning an empty terminal replay.
			return digest
		}
		digest.Evidence = digest.Evidence[:last]
	}
}

func shedLocalizationDigestRowOptionalFields(row *localizationDigestRow) bool {
	if row == nil {
		return false
	}
	if len(row.Callers) > 0 || len(row.Callees) > 0 {
		row.Callers = nil
		row.Callees = nil
		return true
	}
	if row.Signature != "" {
		row.Signature = ""
		return true
	}
	if row.QualName != "" {
		row.QualName = ""
		return true
	}
	if row.Name != "" || row.Kind != "" {
		row.Name = ""
		row.Kind = ""
		return true
	}
	return false
}

func rebuildLocalizationDigestSkeleton(digest *localizationEvidenceDigest) {
	digest.Files = digest.Files[:0]
	digest.Symbols = digest.Symbols[:0]
	seenFiles := make(map[string]struct{}, len(digest.Evidence))
	seenSymbols := make(map[string]struct{}, len(digest.Evidence))
	for _, row := range digest.Evidence {
		if _, exists := seenFiles[row.File]; !exists {
			seenFiles[row.File] = struct{}{}
			digest.Files = append(digest.Files, row.File)
		}
		if _, exists := seenSymbols[row.ID]; !exists {
			seenSymbols[row.ID] = struct{}{}
			digest.Symbols = append(digest.Symbols, row.ID)
		}
	}
}

const localizationReplayNotice = "The completed localization retained the ranked candidates below. " +
	"Additional navigation repeats this evidence. Use the strongest supported rows when composing the final " +
	"file-and-symbol answer; candidate presence alone does not prove a change target."

// renderLocalizationReplayEvidence is model-visible. It presents provenance
// and ranking without claiming every candidate is a target or asking the model
// to repeat a prewritten answer.
func renderLocalizationReplayEvidence(digest *localizationEvidenceDigest) string {
	var b strings.Builder
	b.WriteString(localizationReplayNotice)
	if digest == nil || len(digest.Evidence) == 0 {
		b.WriteString("\nNo retained candidate rows are available; use the original localization result.")
		return b.String()
	}
	for index, row := range digest.Evidence {
		rank := row.Rank
		if rank <= 0 {
			rank = index + 1
		}
		fmt.Fprintf(&b, "\n%d. %s:%d — %s", rank, row.File, row.Line, row.ID)
		if row.Signature != "" {
			fmt.Fprintf(&b, " (%s)", row.Signature)
		}
		if len(row.Callers) > 0 || len(row.Callees) > 0 {
			fmt.Fprintf(&b, " [graph: %d caller(s), %d callee(s)]", len(row.Callers), len(row.Callees))
		}
	}
	return b.String()
}

// buildLocalizationHostFallback provides a deterministic compact summary for
// hosts that explicitly implement a no-inference fallback. It stays in MCP
// _meta and is never concatenated into model-visible tool content.
func buildLocalizationHostFallback(digest *localizationEvidenceDigest) string {
	var b strings.Builder
	b.WriteString("Localization candidates:\n")
	if digest == nil || len(digest.Evidence) == 0 {
		b.WriteString("- no retained candidate evidence")
		return b.String()
	}
	for _, row := range digest.Evidence {
		fmt.Fprintf(&b, "- %s:%d — %s", row.File, row.Line, row.ID)
		if row.Signature != "" {
			fmt.Fprintf(&b, " (%s)", row.Signature)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// answerReadyDirective returns a neutral evidence reminder for a post-terminal
// READ. Reads remain executable because starving them produced empty finals;
// the appended note makes the completion boundary explicit without presenting
// an injected-looking answer script.
func (s *localizationTerminalState) answerReadyDirective() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != localizationStateAnswerReady || s.digest == nil {
		return "", false
	}
	return renderLocalizationReplayEvidence(s.digest), true
}

// localizationEvidenceReplayResult is the successful, idempotent response to
// post-terminal navigation. Model-visible text contains neutral ranked evidence;
// structuredContent carries the machine-readable completion and digest; an
// adapting host can use the deterministic fallback from _meta.
func localizationEvidenceReplayResult(completion localizationCompletion, digest *localizationEvidenceDigest) *mcpgo.CallToolResult {
	result := mcpgo.NewToolResultText(renderLocalizationReplayEvidence(digest))
	result.StructuredContent = map[string]any{
		"completion":      completion,
		"replay":          true,
		"evidence_digest": digest,
	}
	result.Meta = mcpgo.NewMetaFromMap(map[string]any{
		localizationHostFallbackMetaKey: buildLocalizationHostFallback(digest),
	})
	return result
}
