package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		in   string
		want Version
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"v0.0.1", Version{Patch: 1}},
		{"0.0.0", Version{}},
		{"v1.2.3-rc1", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1"}},
		{"v1.2.3-rc.1", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc.1"}},
		{"v1.2.3+abc1234", Version{Major: 1, Minor: 2, Patch: 3, Build: "abc1234"}},
		{"v1.2.3-alpha.2+abc1234", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: "alpha.2", Build: "abc1234"}},
		{"v10.20.30+build.456", Version{Major: 10, Minor: 20, Patch: 30, Build: "build.456"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	// A spec-tight parser should reject these — they're the shapes we
	// actually see from tooling (dev builds, shell variable not set,
	// cut-off tags) that must not silently produce 0.0.0.
	bad := []string{
		"",
		"dev",
		"unknown",
		"v",
		"1",
		"1.2",
		"1.2.3.4",
		"v01.2.3",      // leading zero
		"v1.2.3-",      // empty prerelease
		"v1.2.3+",      // empty build
		"v1.2.3-01",    // leading-zero numeric prerelease identifier
		"v1.2.3 extra", // trailing garbage
	}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			_, err := Parse(s)
			assert.Error(t, err, "Parse(%q) must fail", s)
		})
	}
}

func TestString_Canonical(t *testing.T) {
	// Round-trip: Parse → String → Parse returns an identical value.
	// Catches regressions where String drops or reorders fields.
	samples := []string{
		"v0.0.0",
		"v1.2.3",
		"v1.2.3-rc.1",
		"v1.2.3+abc1234",
		"v1.2.3-alpha+build.456",
	}
	for _, s := range samples {
		v, err := Parse(s)
		require.NoError(t, err)
		assert.Equal(t, s, v.String(), "round-trip failed for %s", s)

		v2, err := Parse(v.String())
		require.NoError(t, err)
		assert.Equal(t, v, v2, "second parse drifted for %s", s)
	}
}

func TestString_Zero(t *testing.T) {
	var v Version
	assert.Equal(t, "v0.0.0", v.String())
	assert.True(t, v.IsZero())
}

func TestCompose_Semver(t *testing.T) {
	v, err := Compose("v1.2.3", "abc1234")
	require.NoError(t, err)
	assert.Equal(t, Version{Major: 1, Minor: 2, Patch: 3, Build: "abc1234"}, v)
	assert.Equal(t, "v1.2.3+abc1234", v.String())
}

func TestCompose_DevBuild(t *testing.T) {
	// The "dev" / "" / "unknown" sentinels mean "no ldflags injected"
	// (local go build without goreleaser). Compose must not error —
	// callers rely on this to render "(dev build)" cleanly.
	for _, s := range []string{"", "dev", "unknown"} {
		v, err := Compose(s, "abc1234")
		require.NoError(t, err, "Compose(%q, ...) must not error", s)
		assert.True(t, v.IsZero() == (v.Build == ""), "dev-build Version")
		assert.Equal(t, "abc1234", v.Build,
			"dev builds must still surface the commit SHA when one was injected")
	}
}

func TestCompose_BuildSlotOverridesEmbedded(t *testing.T) {
	// If the semver already carries a +build slot but an explicit
	// build arg is provided, the explicit one wins. Lets goreleaser
	// pass ShortCommit unconditionally without worrying about whether
	// the tag author also hand-added one.
	v, err := Compose("v1.2.3+old", "new1234")
	require.NoError(t, err)
	assert.Equal(t, "new1234", v.Build)
}

func TestMustParse_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustParse should panic on invalid input")
		}
	}()
	MustParse("nope")
}
