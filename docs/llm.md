# LLM features (optional)

Gortex can delegate code-intelligence work to an LLM. Two features, both **off by default** and gated on configuring a provider:

- **`ask` MCP tool** — a research agent that drives Gortex's own tools (search, callers, contracts, dependencies) to answer an open-ended question and returns a synthesized answer, instead of the calling agent issuing many tool calls itself. `chain: true` traces cross-system call chains.
- **`search_symbols` `assist` arg** — LLM-assisted ranking on `search_symbols`: `auto` (engage on natural-language queries only), `on`, `off`, `deep` (adds a body-grounded verification pass that reads candidate code + callers and honestly drops irrelevant matches).

## Providers

The backend is chosen by the `llm.provider` key. Eight of the nine providers are pure Go — available in any build; only `local` needs a `-tags llama` build (it embeds llama.cpp).

| `llm.provider` | Backend | Needs |
|----------------|---------|-------|
| `local` | in-process llama.cpp | a `-tags llama` build + a `.gguf` model file |
| `anthropic` | Anthropic Messages API | `ANTHROPIC_API_KEY` |
| `openai` | OpenAI Chat Completions | `OPENAI_API_KEY` |
| `ollama` | Ollama daemon | a running Ollama + a pulled model |
| `claudecli` | Claude Code CLI subprocess | the `claude` binary on `$PATH` (signed in once). **No API key — reuses your Claude Code subscription.** |
| `codex` | OpenAI Codex CLI subprocess | the `codex` binary on `$PATH` (signed in once). **No API key — reuses your Codex / ChatGPT sign-in.** |
| `gemini` | Google Gemini `generateContent` REST | `GEMINI_API_KEY` |
| `bedrock` | AWS Bedrock Converse API (SigV4-signed, no AWS SDK) | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` (+ optional `AWS_SESSION_TOKEN`) |
| `deepseek` | DeepSeek Chat Completions (OpenAI-compatible) | `DEEPSEEK_API_KEY` |

## Configuration

The `llm:` block goes in `~/.gortex/config.yaml` or a per-repo `.gortex.yaml` (repo-local wins per field, global fills the rest). Configure only the provider you use:

```yaml
# ~/.gortex/config.yaml (or per-repo .gortex.yaml)
llm:
  provider: local            # local | anthropic | openai | ollama | claudecli | codex | gemini | bedrock | deepseek
  max_steps: 16              # agent tool-loop cap (provider-agnostic)

  local:                     # provider: local — requires a `-tags llama` build
    model: ~/models/qwen2.5-coder-7b-instruct-q4_k_m.gguf
    ctx: 4096                # context window in tokens
    gpu_layers: 999          # layers to offload to GPU (0 = CPU-only)
    template: chatml         # chatml | llama3

  anthropic:                 # provider: anthropic
    model: claude-sonnet-4-6
    api_key_env: ANTHROPIC_API_KEY   # env var holding the key (this is the default)
    # base_url: https://api.anthropic.com

  openai:                    # provider: openai
    model: gpt-4o
    api_key_env: OPENAI_API_KEY

  ollama:                    # provider: ollama
    model: qwen2.5-coder:7b
    host: http://localhost:11434

  claudecli:                 # provider: claudecli — spawns the `claude` CLI per call
    # binary: claude          # binary name or absolute path (resolved via $PATH; default "claude")
    model: sonnet             # optional — forwarded as `--model`; empty = CLI default
    # args: ["--allowed-tools", ""]   # extra args appended after our flags (disable tools, etc.)
    # timeout_seconds: 180    # cap per Complete call; 0 → 120s

  codex:                     # provider: codex — spawns the OpenAI `codex` CLI per call
    # binary: codex           # binary name or absolute path (resolved via $PATH; default "codex")
    model: gpt-5-codex        # optional — forwarded as `--model`; empty = CLI default
    # args: ["--sandbox", "workspace-write"]   # extra args inserted before the prompt
    # timeout_seconds: 180    # cap per Complete call; 0 → 180s

  gemini:                    # provider: gemini — Google Gemini generateContent REST
    model: gemini-2.5-pro
    api_key_env: GEMINI_API_KEY
    # base_url: https://generativelanguage.googleapis.com

  bedrock:                   # provider: bedrock — AWS Bedrock Converse API (SigV4-signed)
    model_id: anthropic.claude-sonnet-4-20250514-v1:0
    region: us-east-1
    # access_key_env: AWS_ACCESS_KEY_ID
    # secret_key_env: AWS_SECRET_ACCESS_KEY
    # session_token_env: AWS_SESSION_TOKEN     # optional — for STS-issued creds
    # base_url: https://bedrock-runtime.us-east-1.amazonaws.com   # override for VPC endpoints

  deepseek:                  # provider: deepseek — OpenAI-compatible Chat Completions
    model: deepseek-chat
    api_key_env: DEEPSEEK_API_KEY
    # base_url: https://api.deepseek.com

  routing:                   # optional — model routing for the `ask` agent
    enabled: false           # off by default; every run uses the provider's model
    simple_model: claude-haiku-4-5    # low-complexity runs (empty = configured model)
    complex_model: claude-opus-4-7    # multi-hop / refactor-scale runs
```

When `llm.routing.enabled` is true, each `ask` run is scored by graph-derived task complexity — chain-tracing mode, multi-hop keywords, and how broad a slice of the multi-repo graph is in scope — and dispatched to `simple_model` or `complex_model` *within the active provider* (a cheap single-hop lookup costs less; a cross-system trace gets the capable model). The chosen `model` and `complexity` ride on the `ask` response. An empty tier model falls back to the provider's configured model.

Env overrides: `GORTEX_LLM_PROVIDER`, `GORTEX_LLM_MODEL` (targets the active provider's model — including `gemini`, `bedrock` (sets `model_id`), `deepseek`, `claudecli`, `codex`), `GORTEX_LLM_MAX_STEPS`, `GORTEX_LLM_CLAUDECLI_BINARY`, `GORTEX_LLM_CODEX_BINARY`, and `GORTEX_LLM_BEDROCK_REGION`. API keys are read from the env var named by `api_key_env` — never stored in the config file.

If the active provider can't be constructed (missing model or API key, `local` without a `-tags llama` build, `claudecli` / `codex` without the `claude` / `codex` binary on `$PATH`, `bedrock` without AWS credentials), the daemon logs a warning and the LLM features stay absent — the rest of Gortex is unaffected. If the `ask` tool isn't in `tools/list`, no provider is configured.

The `assist` prompts are tiered automatically — terser for hosted frontier models, rule-heavy for small local ones. `deep` mode in particular benefits from a 7B-class or hosted model; small local models are unreliable on its disambiguation cases.
