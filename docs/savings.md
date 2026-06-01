# Token savings

Gortex tracks how many tokens it saves compared to naive file reads — per-call, per-session, and cumulative across restarts:

- **Per-call:** `get_symbol_source` and other source-reading tools include a `tokens_saved` field in the response, showing the difference between reading the full file vs the targeted symbol.
- **Session-level:** `graph_stats` returns a `token_savings` object with `calls_counted`, `tokens_returned`, `tokens_saved`, `efficiency_ratio`.
- **Cumulative (cross-session):** `graph_stats` also returns `cumulative_savings` when persistence is wired — includes `first_seen`, `last_updated`, and `cost_avoided_usd` per model (Claude Opus/Sonnet/Haiku, GPT-4o, GPT-4o-mini). Backed by `~/.gortex/cache/savings.json` (top-line totals + per-repo + per-language) and a sibling `~/.gortex/cache/savings.jsonl` event log (one line per call) used to render the windowed buckets and the per-tool breakdown.

`gortex savings` renders a three-bucket dashboard:

```text
Gortex Token Savings
====================
Cost avoided:   $168.69 (claude-opus-4) across 1,878 calls · 11,246,094 tokens saved

Today       ████████░░░░░░░░   50.0%  saved 9,200 / 18,400 tokens   $0.14
Last 7 days ██████████░░░░░░   62.5%  saved 60,100 / 96,200 tokens  $0.90
All time    ███████████████░   93.3%  saved 11,246,094 / 12,050,716 tokens  $168.69
```

```bash
# Three-bucket dashboard with USD on top
gortex savings

# Per-tool breakdown inside each bucket
gortex savings --verbose

# Headline a single model (fuzzy match: "opus" → claude-opus-4)
gortex savings --model opus

# Bucket "Today" by UTC instead of local time
gortex savings --utc

# Machine-readable output (mirrors the dashboard structure: buckets[].per_tool, cost_avoided_usd, etc.)
gortex savings --json

# Wipe cumulative totals and the JSONL event log
gortex savings --reset

# Override pricing (JSON array of {model, usd_per_m_input})
GORTEX_MODEL_PRICING_JSON='[{"model":"mycorp","usd_per_m_input":5}]' gortex savings
```

Token counts use **tiktoken (`cl100k_base`)** — the tokenizer Claude and GPT-4 actually use — via `github.com/pkoukk/tiktoken-go` with an embedded offline BPE loader, so no runtime downloads. The BPE is lazy-loaded on first call. If init fails for any reason, the package falls back to the legacy `chars/4` heuristic so metrics stay usable.
