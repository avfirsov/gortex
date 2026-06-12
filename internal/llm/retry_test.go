package llm

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsContextOverflow_Positives(t *testing.T) {
	// One representative error string per provider wording.
	cases := []string{
		"anthropic: 400: prompt is too long: 210000 tokens > 200000 maximum",
		"openai: context_length_exceeded: please reduce the length of the messages",
		"openai: this model's maximum context length is 128000 tokens",
		"bedrock: ValidationException: too many input tokens",
		"gemini: the input token count exceeds the maximum number of tokens allowed",
		"ollama: input is too long for this model's context window",
		"local: requested tokens exceed context window (n_ctx)",
		"deepseek: this model's maximum context length is 65536 tokens",
	}
	for _, c := range cases {
		if !IsContextOverflow(errors.New(c)) {
			t.Errorf("IsContextOverflow(%q) = false, want true", c)
		}
	}
}

func TestIsContextOverflow_Negatives(t *testing.T) {
	cases := []error{
		nil,
		errors.New("anthropic: 401: invalid x-api-key"),
		errors.New("openai: 429: rate limit exceeded"),
		errors.New("connection refused"),
		errors.New("claudecli: timed out after 2m0s"),
		errors.New("codex: response carried no JSON"),
	}
	for _, c := range cases {
		if IsContextOverflow(c) {
			t.Errorf("IsContextOverflow(%v) = true, want false", c)
		}
	}
}

func TestIsContextOverflow_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("step 3 generate: %w", errors.New("prompt is too long"))
	if !IsContextOverflow(wrapped) {
		t.Error("IsContextOverflow must see through a wrapped error (matches on the full message)")
	}
}

func TestIsContextOverflow_CaseInsensitive(t *testing.T) {
	if !IsContextOverflow(errors.New("ERROR: Maximum Context Length Exceeded")) {
		t.Error("IsContextOverflow must match case-insensitively")
	}
}
