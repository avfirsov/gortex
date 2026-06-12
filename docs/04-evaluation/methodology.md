# Methodology

The protocol below produces reproducible, agent-graded scoring of
gortex's real-world effect on coding tasks. Sticking to the
protocol means anyone with the harness + a model API key can
reproduce the numbers and dispute them.

## 1. Task categories (5)

| Category | What it measures | Example |
|----------|------------------|---------|
| **Architectural explanation** | "Why does this codebase have N services" — graph / community structure understanding | "Walk me through how the indexer pipeline processes a new file." |
| **Refactor safety** | "Rename / move / extract — what breaks?" — impact-analysis driven | "Rename `Indexer.Index` to `Indexer.IndexRoot` across the repo. List every caller that needs updating." |
| **Bug localization** | "Given this failure, where's the root cause?" — call-chain + dataflow | "A panic in `internal/savings/store.go::flushLocked`. Trace the conditions that reach it." |
| **Impact analysis** | "If I change X, what tests should I run?" — `get_test_targets` + `verify_change` | "I'm about to change the signature of `tokens.Count`. List the test files that need re-running." |
| **Contract extraction** | "What's the public API surface of this package?" — `contracts list` driven | "List every exported function / method / type in `internal/savings/`, with one-line summaries." |

Each task is realistic — drawn from actual sessions, not invented
synthetic prompts. The seed task set in [`task-set.md`](task-set.md)
ships 3 tasks per category × 5 categories = 15 tasks.

## 2. Agent / model matrix

The same task set runs against each of:

1. **Claude Sonnet 4.6** via the Anthropic API (`claude-sonnet-4-20250514`)
2. **GPT 5.4** via OpenAI (`gpt-5-2025-08`)
3. **Copilot CLI** via the GitHub CLI extension

For each agent × task combination, **two runs**:

- **WITH gortex MCP** — the agent has access to the full gortex
  tool surface (`smart_context`, `search_symbols`,
  `get_symbol_source`, `verify_change`, …)
- **WITHOUT gortex MCP** — the agent has only its default tool
  set (typically `Read`, `Grep`, `Bash`)

So per task: 6 runs (3 agents × 2 modes). Per task set: 90 runs.
Per category: 18 runs.

## 3. Bias-of-prompt check

Each WITH-gortex run is executed twice:

- **default prompt** — the system prompt gortex ships in
  `internal/agents/instructions.go` (the same one the production
  `gortex init` writes to every agent's config)
- **ablation prompt** — the same prompt with every "prefer
  gortex tools" steering line removed

If the published headline only shows the default-prompt number,
the methodology is incomplete. Always publish both, and call out
the delta — that's the "we're not just measuring the prompt"
test.

## 4. (a) / (b) / (c) classifier

A judge model (default Claude Sonnet 4.6 — see
[`judge-prompt.md`](judge-prompt.md)) scores each per-task
WITH-vs-WITHOUT comparison with one of three labels:

- **(a) gortex helped** — the WITH run produced a measurably
  better answer (more accurate, more complete, fewer
  hallucinations, or substantially fewer tokens)
- **(b) no measurable difference** — answers are roughly
  equivalent in quality and cost
- **(c) gortex hurt** — the WITH run was worse (less accurate,
  more confused by the tool surface, or noticeably more tokens
  for the same answer)

The published summary MUST report all three counts. A
"(a)=12 / (b)=2 / (c)=1" result is honest; "12 wins" is not.

## 5. Negative-delta requirement

Negative deltas (any (c) result) are **required** in the
published summary, with the per-task breakdown linkable. The
explicit requirement is the methodology's anti-survivorship
mechanism: a methodology that buries (c) cases isn't measuring
real-world quality, it's measuring marketing.

If a published run reports zero (c) results across all 15 tasks,
that's a red flag — either the judge is biased or the seed set
is over-fit. Re-run with a different judge model and at least
3 additional tasks per category before publishing.

## 6. Scoring envelope

Per-task token + wall-clock budgets are the same WITH and
WITHOUT (typically 50k tokens / 5 minutes per task). A run that
exceeds the budget scores as "no answer" — not as (c). This
keeps the comparison about *quality* of answers, not endurance.

Per-task cost is reported separately so a published row can show
"answer (a) at 1.2× the WITHOUT cost"; (a) at 5× the cost is
honestly weaker than (a) at 0.8× the cost.

## 7. What we don't measure here

- **Benchmark NDCG / recall** — covered by `bench/baselines/` and
  `bench/token-efficiency/`. Different axis: those measure
  retrieval quality independently of agent / model; this
  methodology measures real agent behaviour.
- **SWE-bench resolve rate** — covered by `BENCHMARK-SWE.md`.
  Multi-day GPU compute; published separately when an operator
  runs it.
- **Performance** — covered by `bench/perf/`. Indexer / query /
  impact latency, not answer quality.

Together the three axes (retrieval / agent behaviour / system
perf) form gortex's published-quality envelope.
