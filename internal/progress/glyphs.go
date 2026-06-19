package progress

import (
	"os"
	"runtime"

	"github.com/charmbracelet/lipgloss"
)

// glyphSet is the small set of display glyphs that differ between a UTF-8
// terminal and a legacy OEM / ASCII one: the success / failure markers, the
// status dot, the mid-dot separator, and the box-drawing charset. Gortex
// renders more box-drawing than a check/cross-only CLI (the rounded card
// border), so the ASCII fallback has to cover the whole border too.
type glyphSet struct {
	OK     string
	Fail   string
	Dot    string
	Sep    string
	Border lipgloss.Border
}

var (
	unicodeGlyphs = glyphSet{OK: "✓", Fail: "✗", Dot: "●", Sep: "·", Border: lipgloss.RoundedBorder()}
	asciiGlyphs   = glyphSet{OK: "+", Fail: "x", Dot: "*", Sep: "-", Border: lipgloss.ASCIIBorder()}
)

// activeGlyphs returns the glyph set appropriate to the current terminal:
// UTF-8 box-drawing / check glyphs when it can render them, ASCII otherwise.
// Resolved per call so a runtime override (or a test) takes effect immediately;
// the cost is a couple of env reads plus, on Windows only, one cheap codepage
// syscall.
func activeGlyphs() glyphSet {
	if supportsUnicode() {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// supportsUnicode reports whether the active terminal can render the UTF-8
// box-drawing / check glyphs. Explicit env overrides win (GORTEX_ASCII opt-out,
// GORTEX_UNICODE opt-in); a linux virtual console (TERM=linux, CP437-ish) and a
// non-UTF-8 Windows console codepage both fall back to ASCII. Every other
// terminal is assumed UTF-8-capable, the modern default.
func supportsUnicode() bool {
	if envFlag("GORTEX_ASCII") {
		return false
	}
	if envFlag("GORTEX_UNICODE") {
		return true
	}
	if os.Getenv("TERM") == "linux" {
		return false
	}
	if runtime.GOOS == "windows" {
		return windowsConsoleUTF8()
	}
	return true
}

// envFlag reports whether the named env var is set to a truthy value.
func envFlag(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}
