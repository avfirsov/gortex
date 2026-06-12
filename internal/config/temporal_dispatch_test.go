package config

import (
	"testing"
)

// PURPOSE — verify tri-state semantics and env-var precedence for
// TemporalDispatchEnabledOrDefault so the flag behaves exactly like
// ExternalCallSynthesisEnabledOrDefault (default-ON, env overrides struct).
// RATIONALE — table tests cover every combination callers can encounter so
// a future refactor can't silently flip the default.
// KEYWORDS — config, temporal, dispatch, tri-state, env-override
func TestTemporalDispatchEnabledOrDefault_TriState(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name  string
		field *bool
		want  bool
	}{
		{"nil returns true (default ON)", nil, true},
		{"false returns false", boolPtr(false), false},
		{"true returns true", boolPtr(true), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := IndexConfig{SynthesizeTemporalDispatch: tc.field}
			if got := cfg.TemporalDispatchEnabledOrDefault(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTemporalDispatchEnabledOrDefault_EnvPrecedence covers the env-override
// table: on/1/true enable, off/0/false disable, env beats struct setting,
// unset env falls through to struct.
func TestTemporalDispatchEnabledOrDefault_EnvPrecedence(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name      string
		envVal    string // empty = unset
		field     *bool
		want      bool
	}{
		{`env "off" disables`, "off", nil, false},
		{`env "on" enables`, "on", nil, true},
		{`env "0" disables`, "0", nil, false},
		{`env "1" enables`, "1", nil, true},
		{`env "false" disables`, "false", nil, false},
		{`env "true" enables`, "true", nil, true},
		{`env "ON" enables (case-insensitive)`, "ON", nil, true},
		{`env "OFF" disables (case-insensitive)`, "OFF", nil, false},
		{`env "on" overrides struct false`, "on", boolPtr(false), true},
		{`env "off" overrides struct true`, "off", boolPtr(true), false},
		{`unset env uses struct false`, "", boolPtr(false), false},
		{`unset env uses struct true`, "", boolPtr(true), true},
		{`unset env + nil uses default true`, "", nil, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVal != "" {
				t.Setenv("GORTEX_TEMPORAL", tc.envVal)
			}
			cfg := IndexConfig{SynthesizeTemporalDispatch: tc.field}
			if got := cfg.TemporalDispatchEnabledOrDefault(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
