// Package version exposes the running build's semver as a structured
// value, parses SemVer 2.0.0 strings, and owns the rendering of the
// canonical form `vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]`.
//
// For Gortex, the build slot always carries the short commit SHA — so
// released binaries identify the exact source they were built from
// even inside a single tag (rare but possible with retag + rebuild).
package version

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a parsed SemVer 2.0.0 value. Pre-release and build labels
// are kept as raw strings because their grammar is identifier-based
// rather than numeric-only.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // e.g. "rc.1", "alpha.2" — no leading '-'
	Build      string // e.g. commit SHA — no leading '+'
}

// String returns the canonical SemVer form with a leading 'v'. Empty
// pre-release / build sections are omitted. A zero-valued Version
// returns "v0.0.0".
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// IsZero reports whether the Version is the zero value. Used by
// rendering code to decide whether to print "dev build (no version)"
// vs a real semver string.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 &&
		v.Prerelease == "" && v.Build == ""
}

// semverRe matches the SemVer 2.0.0 grammar:
//
//   - optional leading 'v'
//   - major.minor.patch (all required, numeric, no leading zeros
//     except literal "0")
//   - optional "-<prerelease>" (dot-separated identifiers, alphanumeric
//     or hyphen; numeric identifiers no leading zeros)
//   - optional "+<build>" (dot-separated identifiers, alphanumeric or
//     hyphen; leading zeros allowed here)
//
// Kept as a single regex because the format is small and the cost of
// a full recursive-descent parser isn't justified.
var semverRe = regexp.MustCompile(
	`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`,
)

// Parse accepts a SemVer string (with or without leading 'v') and
// returns the structured Version. Common non-semver inputs that crop
// up during development — "dev", "", "unknown" — are rejected with a
// clear error rather than silently returning a zero value, so callers
// can decide whether to fall back or fail.
func Parse(s string) (Version, error) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid semver: %q", s)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("parse major: %w", err)
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("parse minor: %w", err)
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return Version{}, fmt.Errorf("parse patch: %w", err)
	}
	return Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: m[4],
		Build:      m[5],
	}, nil
}

// MustParse is Parse with panic-on-error. Intended for compile-time
// constants (tests, internal defaults); do not use on user input.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Compose builds a Version from its components without going through
// the string form. Useful at program startup when main() has the
// ldflag-injected pieces as separate variables.
//
// An empty semver string returns a zero Version with no error — that's
// the "dev build" signal. A malformed semver returns an error so the
// caller can decide whether to panic or fall back.
func Compose(semver, build string) (Version, error) {
	if semver == "" || semver == "dev" || semver == "unknown" {
		return Version{Build: build}, nil
	}
	v, err := Parse(semver)
	if err != nil {
		return Version{}, err
	}
	// Explicit build slot overrides whatever was in the semver string.
	if build != "" {
		v.Build = build
	}
	return v, nil
}
