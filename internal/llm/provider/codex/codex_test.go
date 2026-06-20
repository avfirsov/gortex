package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/llm"
)

// fakeCodex writes a shell script that impersonates the `codex` CLI.
// It logs args + stdin to sidecar files for assertions, writes
// opts.lastMessage to whatever path follows --output-last-message
// (mirroring real `codex exec`), echoes opts.stdout as progress noise,
// and exits with opts.exitCode.
//
// A shell-script fake exercises every provider code path — arg
// construction, stdin piping, the --output-last-message sidecar, the
// stdout fallback, exit-status and stderr handling — without a real
// codex install.
func fakeCodex(t *testing.T, dir string, opts fakeOpts) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	script := filepath.Join(dir, "fake-codex.sh")

	argsLog := filepath.Join(dir, "args.txt")
	stdinLog := filepath.Join(dir, "stdin.txt")
	q := func(s string) string { return strings.ReplaceAll(s, "'", "'\\''") }

	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + argsLog + "'\n" +
		"cat > '" + stdinLog + "'\n" +
		"prev=''\n" +
		"outfile=''\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = '--output-last-message' ]; then outfile=\"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n"
	if opts.sleep > 0 {
		body += fmt.Sprintf("sleep %d\n", int(opts.sleep.Seconds()+1))
	}
	if opts.lastMessage != "" {
		body += "if [ -n \"$outfile\" ]; then printf '%s' '" + q(opts.lastMessage) + "' > \"$outfile\"; fi\n"
	}
	if opts.stderr != "" {
		body += "printf '%s' '" + q(opts.stderr) + "' >&2\n"
	}
	if opts.stdout != "" {
		body += "printf '%s' '" + q(opts.stdout) + "'\n"
	}
	if opts.exitCode != 0 {
		body += fmt.Sprintf("exit %d\n", opts.exitCode)
	}

	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return script
}

type fakeOpts struct {
	lastMessage string // written to the --output-last-message sidecar
	stdout      string // emitted on stdout (progress logs)
	stderr      string
	exitCode    int
	sleep       time.Duration
}

func readSidecar(t *testing.T, scriptPath, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(scriptPath), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestNew_BinaryNotFound(t *testing.T) {
	if _, err := New(llm.CodexConfig{Binary: "codex-nonexistent-zzzzz"}); err == nil {
		t.Fatal("expected error when binary is not on PATH")
	}
}

func TestNew_DefaultsBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p, err := New(llm.CodexConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()
	if p.Name() != "codex" {
		t.Errorf("Name()=%q want codex", p.Name())
	}
}

func TestComplete_FreeformSuccess(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{lastMessage: "hello world", stdout: "[progress] thinking…"})

	p, err := New(llm.CodexConfig{Binary: script})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "be terse"},
			{Role: llm.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "hello world" {
		t.Errorf("text=%q want hello world (the sidecar must win over stdout noise)", resp.Text)
	}

	gotArgs := readSidecar(t, script, "args.txt")
	for _, want := range []string{"exec", "--skip-git-repo-check", "--sandbox", "read-only", "--output-last-message", "-"} {
		if !strings.Contains(gotArgs, want) {
			t.Errorf("args missing %q\nargs=\n%s", want, gotArgs)
		}
	}
	stdin := readSidecar(t, script, "stdin.txt")
	if !strings.Contains(stdin, "User: hi") {
		t.Errorf("stdin missing user turn:\n%s", stdin)
	}
	if !strings.Contains(stdin, "System instructions:") || !strings.Contains(stdin, "be terse") {
		t.Errorf("system content must be folded into the prompt:\n%s", stdin)
	}
}

func TestComplete_StdoutFallback(t *testing.T) {
	// No sidecar payload — the provider must fall back to stdout for
	// an older codex build that does not honour --output-last-message.
	script := fakeCodex(t, "", fakeOpts{stdout: "fallback answer"})

	p, _ := New(llm.CodexConfig{Binary: script})
	defer p.Close()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "fallback answer" {
		t.Errorf("text=%q want fallback answer", resp.Text)
	}
}

func TestComplete_PassesModel(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{lastMessage: "ok"})

	p, _ := New(llm.CodexConfig{Binary: script, Model: "gpt-5-codex"})
	defer p.Close()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	args := readSidecar(t, script, "args.txt")
	if !strings.Contains(args, "--model\ngpt-5-codex") {
		t.Errorf("args missing --model gpt-5-codex:\n%s", args)
	}
}

func TestComplete_StructuredExtractsJSON(t *testing.T) {
	wrapped := "```json\n{\"terms\":[\"bcrypt\",\"argon2\"]}\n```\n"
	script := fakeCodex(t, "", fakeOpts{lastMessage: wrapped})

	p, _ := New(llm.CodexConfig{Binary: script})
	defer p.Close()

	resp, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "expand 'password hashing'"}},
		Shape:    llm.ShapeExpandTerms,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != `{"terms":["bcrypt","argon2"]}` {
		t.Errorf("text=%q want the unwrapped JSON object", resp.Text)
	}
	stdin := readSidecar(t, script, "stdin.txt")
	if !strings.Contains(stdin, "JSON Schema") {
		t.Errorf("structured request must inject a JSON Schema rider; stdin=\n%s", stdin)
	}
}

func TestComplete_StructuredNoJSONErrors(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{lastMessage: "I cannot help with that."})

	p, _ := New(llm.CodexConfig{Binary: script})
	defer p.Close()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
		Shape:    llm.ShapeExpandTerms,
	}); err == nil {
		t.Fatal("expected error when structured response carried no JSON")
	}
}

func TestComplete_NonZeroExit(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{exitCode: 2, stderr: "not signed in"})

	p, _ := New(llm.CodexConfig{Binary: script})
	defer p.Close()

	_, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("error should include stderr snippet; got: %v", err)
	}
}

func TestComplete_EmptyResponseErrors(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{})

	p, _ := New(llm.CodexConfig{Binary: script})
	defer p.Close()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestComplete_ContextCancellation(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{lastMessage: "late", sleep: 2 * time.Second})

	p, _ := New(llm.CodexConfig{Binary: script, TimeoutSeconds: 1})
	defer p.Close()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestComplete_ExtraArgsForwarded(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{lastMessage: "ok"})

	p, _ := New(llm.CodexConfig{Binary: script, Args: []string{"--sandbox", "workspace-write"}})
	defer p.Close()

	if _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	args := readSidecar(t, script, "args.txt")
	if !strings.Contains(args, "workspace-write") {
		t.Errorf("args missing forwarded extra arg:\n%s", args)
	}
}

func TestBuildPrompt_Roles(t *testing.T) {
	prompt := buildPrompt([]llm.Message{
		{Role: llm.RoleSystem, Content: "rule 1"},
		{Role: llm.RoleSystem, Content: "rule 2"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleTool, Content: "[1,2,3]", ToolName: "search_symbols"},
		{Role: llm.RoleUser, Content: "q2"},
	})
	if !strings.HasPrefix(prompt, "System instructions:\nrule 1\n\nrule 2") {
		t.Errorf("system block must lead the prompt:\n%s", prompt)
	}
	for _, want := range []string{"User: q1", "Assistant: a1", "Tool result (search_symbols)", "User: q2"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt=\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_NoSystem(t *testing.T) {
	prompt := buildPrompt([]llm.Message{{Role: llm.RoleUser, Content: "q"}})
	if strings.Contains(prompt, "System instructions:") {
		t.Errorf("no system turns → no system block:\n%s", prompt)
	}
}

// Sanity check: the test helper itself can run a child process.
func TestHelper_FakeScriptIsExecutable(t *testing.T) {
	script := fakeCodex(t, "", fakeOpts{stdout: "alive"})
	cmd := exec.Command(script, "ping")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("cannot exec fake script: %v", err)
	}
	if !strings.Contains(string(out), "alive") {
		t.Errorf("output=%q", out)
	}
}
