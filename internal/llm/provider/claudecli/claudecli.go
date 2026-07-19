// Package claudecli is the Claude Code CLI llm.Provider.
//
// It is pure Go — available in every build, no `-tags llama` needed.
// Inference is delegated to the user's locally installed `claude`
// binary, which reuses the user's Claude Code subscription instead of
// requiring an Anthropic API key. Each Complete call spawns one
// `claude -p` subprocess: the conversation is flattened to text, the
// system prompt is forwarded via --append-system-prompt, and the
// prompt text is fed on stdin so very large contexts don't trip
// ARG_MAX.
//
// Structured output (the expand / rerank / verify shapes and the
// agent tool-call shape) is obtained by appending a JSON-Schema
// instruction to the system prompt and parsing the first valid JSON
// object out of the response — the CLI has no native structured-
// output mechanism. That schema-rider + JSON-extraction logic is
// shared with the `codex` provider; it lives in llm.AppendSchema-
// Instruction / llm.ExtractJSON. The agent tool-loop itself uses the
// *emulated* protocol: tool calls and results travel as plain text
// turns, so a single llm.Message shape works across all providers.
package claudecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zzet/gortex/internal/llm"
	"github.com/zzet/gortex/internal/platform"
)

// defaultTimeout caps one Complete call when the user hasn't set
// claudecli.timeout_seconds in config. Claude Code CLI startup plus
// one model round-trip is comfortably under 120s for the small
// prompts the assist/agent loop emits.
const defaultTimeout = 120 * time.Second

// Provider implements llm.Provider against the `claude` CLI.
type Provider struct {
	binary  string
	model   string
	extra   []string
	timeout time.Duration
}

var _ llm.Provider = (*Provider)(nil)

// New constructs the Claude CLI provider. It verifies the binary is
// reachable on $PATH (or as an absolute path) so misconfiguration
// surfaces at startup, not on the first Complete call.
func New(cfg llm.ClaudeCLIConfig) (llm.Provider, error) {
	bin := strings.TrimSpace(cfg.Binary)
	if bin == "" {
		bin = "claude"
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("claudecli: binary %q not found on PATH: %w", bin, err)
	}
	timeout := defaultTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return &Provider{
		binary:  resolved,
		model:   strings.TrimSpace(cfg.Model),
		extra:   append([]string(nil), cfg.Args...),
		timeout: timeout,
	}, nil
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return "claudecli" }

// Close is a no-op — every Complete spawns a fresh subprocess; there
// is no long-lived connection or model handle to release.
func (p *Provider) Close() error { return nil }

// Complete implements llm.Provider. It runs one `claude -p`
// subprocess: the system messages are joined and forwarded via
// --append-system-prompt, every other message is flattened into a
// chat-style prompt that is piped on stdin, and stdout is captured
// as the model's text. For structured shapes the schema is injected
// into the system prompt and the first balanced JSON object is
// extracted from the response.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	system, prompt := flatten(req.Messages)
	structured := req.Shape != llm.ShapeFreeform
	if structured {
		system = llm.AppendSchemaInstruction(system, req.Shape, req.Tools)
	}

	args := []string{"--print", "--output-format", "text"}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	// --max-turns pins the agent loop inside Claude Code to a single
	// turn — every llm.Provider caller assumes one single-shot
	// response. The per-response token cap (req.MaxTokens) is
	// best-effort: the CLI exposes no equivalent flag, so we lean on
	// the model's own behaviour given a short system prompt.
	args = append(args, "--max-turns", "1")
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	args = append(args, p.extra...)

	runCtx := ctx
	if p.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, p.binary, args...)
	platform.ConfigureBackgroundCommand(cmd)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Distinguish a context-timeout from an exec failure so the
		// agent loop can log something meaningful.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return llm.CompletionResponse{}, fmt.Errorf("claudecli: timed out after %s: %s", p.timeout, llm.Snippet(stderr.Bytes()))
		}
		if msg := llm.Snippet(stderr.Bytes()); msg != "" {
			return llm.CompletionResponse{}, fmt.Errorf("claudecli: %w: %s", err, msg)
		}
		return llm.CompletionResponse{}, fmt.Errorf("claudecli: %w", err)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return llm.CompletionResponse{}, errors.New("claudecli: empty response from CLI")
	}
	if structured {
		extracted, ok := llm.ExtractJSON(text)
		if !ok {
			return llm.CompletionResponse{}, fmt.Errorf("claudecli: response carried no JSON: %s", llm.Snippet([]byte(text)))
		}
		text = extracted
	}
	return llm.CompletionResponse{Text: text}, nil
}

// flatten splits the conversation into a system block (every
// RoleSystem message joined with a blank line) and a chat-style
// prompt (every other message rendered as "User:" / "Assistant:" /
// "Tool result:" turns). The CLI takes the system part via
// --append-system-prompt and reads the prompt part from stdin. Using
// stdin avoids the ARG_MAX ceiling on long contexts.
func flatten(in []llm.Message) (system, prompt string) {
	var sys []string
	var b strings.Builder
	turns := 0
	for _, m := range in {
		switch m.Role {
		case llm.RoleSystem:
			if s := strings.TrimSpace(m.Content); s != "" {
				sys = append(sys, s)
			}
		case llm.RoleAssistant:
			if turns > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("Assistant: ")
			b.WriteString(m.Content)
			turns++
		case llm.RoleTool:
			if turns > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(renderToolResult(m))
			turns++
		default:
			if turns > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("User: ")
			b.WriteString(m.Content)
			turns++
		}
	}
	return strings.Join(sys, "\n\n"), b.String()
}

func renderToolResult(m llm.Message) string {
	if m.ToolName != "" {
		return "Tool result (" + m.ToolName + "):\n" + m.Content
	}
	return "Tool result:\n" + m.Content
}
