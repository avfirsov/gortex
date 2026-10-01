package indexer

import (
	"bytes"
	"errors"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// utf16DecodeTransform transcodes a UTF-16 source to UTF-8 before
// extraction (#812). A tracked source file encoded UTF-16LE/BE — the
// default some Windows tooling still writes — reached tree-sitter as
// NUL-interleaved bytes: with a BOM the parse failed outright, without
// one the grammar parsed interleaved garbage into a partial-health file
// node and zero symbols. Either way the file vanished from the graph
// with no diagnostic, and absence queries read as authoritative.
//
// The transform is an offset-shifting contentTransform — the pipeline
// contract already accepts those (BOM strip, command rewrites), and
// graph.Node persists only line numbers, which a pure code-unit decode
// preserves 1:1 (a UTF-16 code unit maps to exactly one UTF-8 newline).
// Byte columns describe the decoded text; nothing slices the original
// file by stored byte extents. The coordinate-stable path
// (prepareCoordinateStable) keeps refusing UTF-16, which is correct
// there: rename recovery edits the raw file.
type utf16DecodeTransform struct{}

func (utf16DecodeTransform) name() string        { return "utf16-decode" }
func (utf16DecodeTransform) matches(string) bool { return true }
func (utf16DecodeTransform) asLanguage() string  { return "" }

func (utf16DecodeTransform) apply(_ string, src []byte) ([]byte, error) {
	return decodeUTF16Source(src), nil
}

// utf16SampleBytes bounds the no-BOM sniff. 8 KiB matches the binary
// sniff's window — the prefix a grammar would consume first.
const utf16SampleBytes = 8192

// utf16MinNULs is the minimum alternating-parity NUL count the no-BOM
// heuristic requires before it will claim UTF-16. A short ASCII source
// has no NULs at all; a binary blob's NULs cluster arbitrarily. Only a
// sustained single-parity pattern over a real sample is UTF-16 evidence.
const utf16MinNULs = 64

// decodeUTF16Source transcodes UTF-16LE/BE bytes to UTF-8, or returns
// src unchanged when it is not confidently UTF-16 text. Detection is
// keyed on the BOM; without one, a conservative alternating-NUL
// heuristic decides. The decode is also validated: output must be
// well-formed UTF-8 and NUL-free, so a misdetected binary is passed
// through byte-for-byte rather than half-transcoded.
func decodeUTF16Source(src []byte) []byte {
	le, ok := utf16Endianness(src)
	if !ok {
		return src
	}
	body := src
	if utf16HasBOM(src) {
		body = src[2:]
	}
	if len(body)%2 != 0 {
		return src // a dangling byte is not valid UTF-16
	}
	units := make([]uint16, len(body)/2)
	if le {
		for i := range units {
			units[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
		}
	} else {
		for i := range units {
			units[i] = uint16(body[2*i])<<8 | uint16(body[2*i+1])
		}
	}
	runes := utf16.Decode(units)
	out := make([]byte, 0, len(runes)+utf8.UTFMax)
	var buf [utf8.UTFMax]byte
	for _, r := range runes {
		n := utf8.EncodeRune(buf[:], r)
		out = append(out, buf[:n]...)
	}
	// A UTF-16 text source decodes to clean UTF-8. Anything else — a
	// binary payload whose NULs happened to alternate, a truncated file —
	// goes through unchanged so the existing guards handle it.
	if !utf8.Valid(out) || bytes.IndexByte(out, 0) >= 0 {
		return src
	}
	return out
}

// utf16Endianness reports whether src is confidently UTF-16 text and, if
// so, which byte order. A BOM decides outright. Without one, NUL bytes
// must appear only at a single parity over the bounded sample (odd for
// LE, even for BE — every Latin-1-range code unit carries a zero high or
// low byte), in meaningful volume.
func utf16Endianness(src []byte) (le, ok bool) {
	if utf16HasBOM(src) {
		return src[0] == 0xFF, true
	}
	n := len(src)
	if n > utf16SampleBytes {
		n = utf16SampleBytes
	}
	var evenNULs, oddNULs int
	for i := 0; i < n; i++ {
		if src[i] != 0x00 {
			continue
		}
		if i%2 == 0 {
			evenNULs++
		} else {
			oddNULs++
		}
	}
	switch {
	case oddNULs >= utf16MinNULs && evenNULs == 0:
		return true, true
	case evenNULs >= utf16MinNULs && oddNULs == 0:
		return false, true
	default:
		return false, false
	}
}

func utf16HasBOM(src []byte) bool {
	return len(src) >= 2 && ((src[0] == 0xFF && src[1] == 0xFE) || (src[0] == 0xFE && src[1] == 0xFF))
}

// errSilentZeroExtraction is the ledger diagnostic for an extraction that
// reported success but minted zero symbols from a partial parse.
var errSilentZeroExtraction = errors.New(
	"extraction produced no symbols from a partial parse; the file is absent from symbol discovery (encoding or grammar failure)")

// silentZeroExtraction reports whether an extraction result carries only
// a file node flagged parse_health partial — a parse the grammar could
// not complete minted no symbols, yet reported success (nil error), so
// nothing downstream recorded a failure. The #812 shape: absence from
// the graph with no diagnostic.
func silentZeroExtraction(result *parser.ExtractionResult) bool {
	if result == nil {
		return false
	}
	symbols := 0
	partial := false
	for _, n := range result.Nodes {
		if n == nil {
			continue
		}
		if n.Kind == graph.KindFile {
			if v, ok := n.Meta["parse_health"].(string); ok && v == "partial" {
				partial = true
			}
			continue
		}
		symbols++
	}
	return partial && symbols == 0
}

// noteSilentZeroExtraction queues a silent-zero file for the failure
// ledger. It cannot call noteFileIndexFailure directly: recordFileReadVersion's
// success path clears ledger entries for the file later in the same pass,
// which would erase the note before any flush persisted it. The pending
// set is drained by flushSilentZeroNotes at pass end, after every
// version receipt has been recorded.
func (idx *Indexer) noteSilentZeroExtraction(relPath string) {
	idx.silentZeroMu.Lock()
	defer idx.silentZeroMu.Unlock()
	if idx.silentZero == nil {
		idx.silentZero = make(map[string]struct{})
	}
	idx.silentZero[relPath] = struct{}{}
}

// flushSilentZeroNotes records every queued silent-zero file in the
// failure ledger. Called from flushFileIndexFailures — every pass end —
// after version receipts, so the entries survive the success-path clear.
// A file that indexes cleanly on a later pass is never re-queued, and
// its receipt recording clears the stale row, so the lifecycle needs no
// separate recovery path.
func (idx *Indexer) flushSilentZeroNotes() {
	idx.silentZeroMu.Lock()
	defer idx.silentZeroMu.Unlock()
	for relPath := range idx.silentZero {
		idx.noteFileIndexFailure(relPath, errSilentZeroExtraction)
	}
	clear(idx.silentZero)
}
