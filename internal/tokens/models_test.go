package tokens

import (
	"math"
	"testing"
)

func TestSpecForModel_Families(t *testing.T) {
	cases := []struct {
		model    string
		family   string
		encoding string
	}{
		{"", "default", encodingCL100K},
		{"claude-opus-4-7", "claude", encodingCL100K},
		{"claude-sonnet-4-6", "claude", encodingCL100K},
		{"sonnet", "claude", encodingCL100K},
		{"opus", "claude", encodingCL100K},
		{"haiku", "claude", encodingCL100K},
		{"gpt-4o", "openai-o200k", encodingO200K},
		{"gpt-4o-mini", "openai-o200k", encodingO200K},
		{"gpt-4.1", "openai-o200k", encodingO200K},
		{"gpt-5-codex", "openai-o200k", encodingO200K},
		{"o4-mini", "openai-o200k", encodingO200K},
		{"o3", "openai-o200k", encodingO200K},
		{"gpt-4", "openai-cl100k", encodingCL100K},
		{"gpt-3.5-turbo", "openai-cl100k", encodingCL100K},
		{"deepseek-chat", "deepseek", encodingCL100K},
		{"deepseek-reasoner", "deepseek", encodingCL100K},
		{"gemini-2.5-pro", "gemini", encodingO200K},
		{"some-unknown-model", "default", encodingCL100K},
	}
	for _, c := range cases {
		spec := specForModel(c.model)
		if spec.family != c.family {
			t.Errorf("specForModel(%q).family = %q, want %q", c.model, spec.family, c.family)
		}
		if spec.encoding != c.encoding {
			t.Errorf("specForModel(%q).encoding = %q, want %q", c.model, spec.encoding, c.encoding)
		}
		if ModelFamily(c.model) != c.family {
			t.Errorf("ModelFamily(%q) = %q, want %q", c.model, ModelFamily(c.model), c.family)
		}
		if EncodingForModel(c.model) != c.encoding {
			t.Errorf("EncodingForModel(%q) = %q, want %q", c.model, EncodingForModel(c.model), c.encoding)
		}
	}
}

func TestSpecForModel_GPT4oBeatsGPT4(t *testing.T) {
	// "gpt-4o" contains the substring "gpt-4"; the o200k rule must win.
	if got := EncodingForModel("gpt-4o"); got != encodingO200K {
		t.Errorf("gpt-4o must resolve to o200k_base, got %q", got)
	}
}

func TestCountFor_Empty(t *testing.T) {
	if got := CountFor("claude-opus-4-7", ""); got != 0 {
		t.Errorf("CountFor empty text = %d, want 0", got)
	}
}

func TestCountFor_OpenAICl100kMatchesCount(t *testing.T) {
	if !EncoderReady() {
		t.Skip("tiktoken encoder unavailable")
	}
	const text = "func main() { fmt.Println(\"hello\") }"
	if got, want := CountFor("gpt-4", text), Count(text); got != want {
		t.Errorf("CountFor(gpt-4) = %d, want Count = %d (cl100k, ratio 1.0)", got, want)
	}
}

func TestCountFor_ClaudeAppliesCalibration(t *testing.T) {
	if !EncoderReady() {
		t.Skip("tiktoken encoder unavailable")
	}
	const text = "the quick brown fox jumps over the lazy dog, repeatedly and at length"
	raw := Count(text) // cl100k_base raw count
	got := CountFor("claude-opus-4-7", text)
	want := int(math.Round(float64(raw) * claudeRatio))
	if got != want {
		t.Errorf("CountFor(claude) = %d, want %d (cl100k %d × %.2f)", got, want, raw, claudeRatio)
	}
	if got <= raw {
		t.Errorf("Claude calibration must inflate the cl100k count: got %d, raw %d", got, raw)
	}
}

func TestCountFor_O200kProducesPositiveCount(t *testing.T) {
	if !EncoderReady() {
		t.Skip("tiktoken encoder unavailable")
	}
	const text = "package main\n\nfunc add(a, b int) int { return a + b }\n"
	if got := CountFor("gpt-4o", text); got <= 0 {
		t.Errorf("CountFor(gpt-4o) = %d, want a positive o200k count", got)
	}
}

func TestCountFor_UnknownModelMatchesCount(t *testing.T) {
	if !EncoderReady() {
		t.Skip("tiktoken encoder unavailable")
	}
	const text = "arbitrary content for an unrecognised model id"
	if got, want := CountFor("totally-unknown-xyz", text), Count(text); got != want {
		t.Errorf("CountFor(unknown) = %d, want Count = %d", got, want)
	}
}

func TestCountForInt64(t *testing.T) {
	const text = "some text"
	if got, want := CountForInt64("gpt-4", text), int64(CountFor("gpt-4", text)); got != want {
		t.Errorf("CountForInt64 = %d, want %d", got, want)
	}
}

func TestEstimatorFor_MatchesCountFor(t *testing.T) {
	if !EncoderReady() {
		t.Skip("tiktoken encoder unavailable")
	}
	est := EstimatorFor("claude-sonnet-4-6")
	for _, text := range []string{"", "short", "a longer line of representative source code text"} {
		if got, want := est(text), CountFor("claude-sonnet-4-6", text); got != want {
			t.Errorf("EstimatorFor()(%q) = %d, want CountFor = %d", text, got, want)
		}
	}
}

func TestEncoderFor_O200kLoadsOffline(t *testing.T) {
	enc, err := encoderFor(encodingO200K)
	if err != nil {
		t.Skipf("o200k_base encoder unavailable: %v", err)
	}
	if enc == nil {
		t.Fatal("encoderFor(o200k_base) returned nil encoder without error")
	}
	if n := len(enc.EncodeOrdinary("hello world")); n <= 0 {
		t.Errorf("o200k_base encoded 0 tokens for non-empty text")
	}
}
