# GCX1 wire-format benchmark

Reproducible benchmark comparing the GCX1 compact wire format against
JSON (and, when `JCODEMUNCH=/path/to/jcm` is set, against jCodeMunch
MUNCH) on representative MCP tool responses.

## What it measures

For each fixture case the harness captures, for JSON and GCX:

- **Bytes** — raw UTF-8 byte length.
- **tiktoken (cl100k_base)** — LLM-relevant token count via
  `github.com/pkoukk/tiktoken-go` (same loader Gortex uses at runtime).
  Matches Claude 3 / Opus 4 / Sonnet 4 / Haiku 4.5 / GPT-4o budgets.
- **Claude Opus 4.7 input tokens** — second column populated either
  by scaling cl100k_base (×1.35 empirical inflation factor, the
  default; labeled `estimated`) or by calling Anthropic's
  `messages/count_tokens` endpoint with `--use-api` (requires
  `ANTHROPIC_API_KEY`; results cached to `opus47-counts.json` for
  deterministic reruns; labeled `exact`).
- **gzip** — gzip-compressed byte length, fair comparison when the
  transport would compress anyway.
- **Round-trip integrity** — encode → decode → re-marshal, compare to
  the canonical JSON normalisation. Must be 100 % for the format to
  be considered lossless.

Results land in `scorecard.md` with per-case rows and a summary of
medians / totals.

## Running

```sh
go run ./bench/wire-format -cases ./bench/wire-format/cases -out ./bench/wire-format/scorecard.md
```

Flags:

- `-cases DIR` — directory of fixture case files (default
  `./bench/wire-format/cases`).
- `-out FILE` — output scorecard markdown path (default stdout).
- `-json FILE` — emit raw per-case metrics as JSON too.
- `-tokenizer cl100k|opus47|both` — which token-cost column(s) to
  render (default `both`).
- `-use-api` — call Anthropic `count_tokens` for exact Opus 4.7
  numbers (requires `ANTHROPIC_API_KEY`; degrades to the scalar
  on network failure with a single warning).
- `-opus47-cache PATH` — exact-count sidecar (default
  `bench/wire-format/opus47-counts.json`).
- `-opus47-model NAME` — model id passed to the API
  (default `claude-opus-4-20250514`).

## Fixture format

Each case is a Go struct literal decoded from a YAML file with two
sections:

```yaml
tool: search_symbols
description: 20 search hits on a medium-size repo
input: |
  [{...JSON rows as the tool would return...}]
```

The harness encodes `input` via the canonical Go encoder for the
specified tool and scores the two outputs.

## Target

GCX1 targets **≥20 % token savings vs JSON on the median case**
with **100 % round-trip integrity**. Current baseline (20 cases):

- **Median token savings (cl100k_base): −27.4 %**
- **Median token savings (Opus 4.7, scalar): −27.3 %**
- **Median byte savings:  −26.8 %**
- **Round-trip integrity: 20/20**

Tabular list payloads (`search_symbols`, `analyze_hotspots`,
`find_usages_large`, `smart_context`) hit −30 to −38 %. Small
scalar-heavy records (`graph_stats`, `find_cycles`) can flip
positive — GCX1's header overhead exceeds the savings when there
are fewer than ~5 rows and no repeated field names. Payloads with
long inline bodies (`get_symbol_source`) are roughly neutral — the
source body dominates and neither encoding compresses it.
