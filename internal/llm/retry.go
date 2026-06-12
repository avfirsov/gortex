// Package llm — context-overflow classification for adaptive retry.
//
// When a request's prompt exceeds the model's context window, every
// provider fails it — but each phrases the error differently (the
// HTTP providers relay their API's wording, the local llama.cpp
// provider its own, the CLI providers whatever their binary printed).
// IsContextOverflow recognises that failure mode across all of them so
// the assist layer can react to it (see the svc package's adaptive
// chunk-bisection retry) instead of surfacing a hard error.
package llm

import "strings"

// contextOverflowMarkers are lowercase substrings that appear in a
// provider error when a request exceeds the model's context window.
// The set is deliberately specific — generic words like "context" or
// "tokens" alone would misclassify unrelated failures. Covers the
// Anthropic / OpenAI / Ollama / Gemini / Bedrock / DeepSeek API
// wordings and the local llama.cpp `n_ctx` message.
var contextOverflowMarkers = []string{
	"context length",
	"context window",
	"context_length_exceeded",
	"maximum context",
	"exceeds the maximum",
	"exceeds the context",
	"exceed context",
	"too many tokens",
	"too many input tokens",
	"prompt is too long",
	"input is too long",
	"input too long",
	"reduce the length",
	"please reduce",
	"token limit",
	"n_ctx",
}

// IsContextOverflow reports whether err looks like a model
// context-window overflow — the prompt was too large for the model to
// accept. It is a best-effort string classifier: providers expose no
// structured error code for this, so the assist layer treats a true
// result as "retry smaller" and a false result as a genuine failure.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range contextOverflowMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
