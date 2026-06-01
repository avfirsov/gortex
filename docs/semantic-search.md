# Semantic search

**Default-on.** A baked GloVe-50d table (~3.8 MB embedded in the binary, top 20k tokens) gives every install hybrid BM25 + vector search out of the box — no flag, no model download, no native dependency. Reciprocal Rank Fusion blends the two channels; identifier-shaped queries stay BM25-leaning, natural-language ones shift toward the vector channel.

## Configuration

Switch or tune providers in `.gortex.yaml`:

```yaml
embedding:
  enabled: true                # default — pass `false` to disable
  provider: static             # static | local | api  (default: static)
  api_url: http://localhost:11434
  api_model: nomic-embed-text
  chunk_threshold_lines: 60    # symbols longer than this get split
  chunk_window_lines: 40       # AST-aware window size
  api_concurrency: 4           # bounded worker pool for hosted providers
```

| Provider | Quality | Offline | Native deps | Notes |
|---|---|---|---|---|
| `static` (default) | Good for identifier-shaped queries | Yes | None | Baked GloVe-50d table, CPU-only, zero setup |
| `local` (Hugot MiniLM-L6-v2) | Better for NL queries | After first run | None | Auto-downloads ~90 MB to `~/.gortex/models/` |
| `api` (Ollama / OpenAI) | Best | No | None | Bounded concurrent worker pool — tune via `api_concurrency` |

## AST sub-chunking

Symbols longer than `chunk_threshold_lines` are split into AST-aware windows (block statements, case clauses, field groups) before embedding; each window is vectorised independently and de-duplicated back to the parent symbol at query time, so a large function lands as one hit grounded in the specific chunk that matched — chunk IDs never leak into results.

## Persistent index

The vector index and the chunk → symbol map are persisted in the daemon snapshot; restarts re-warm in milliseconds without re-embedding the graph. Daemon snapshot schema is forward-compatible — older snapshots load with an empty vector layer and rebuild incrementally.

## Vocabulary bridging without an LLM

A curated equivalence table (`auth` ↔ `authentication` ↔ `login`, `delete` ↔ `remove` ↔ `destroy`, …) plus per-repo auto-concept mining from symbol-name token co-occurrence expands queries deterministically — runs alongside (and dedup against) any LLM expansion. Toggle via `search.equivalence_classes`.

## HITS reranking

A hubs-and-authorities pass over the reference/call graph contributes a `hits` signal to the rerank pipeline — heavily-referenced symbols outrank shallow utility nodes, and the hub penalty (`authority / (1 + hub)`) demotes called-by-everything infra so it doesn't drown the result page.

## Keyword-soup defense

Boolean / OR-soup queries (`A OR B OR 'no access' OR …`) defeat embedding retrieval. The query classifier detects soup, skips wasted LLM expansion, and splits the soup into terms fused via the existing BM25 expansion path; a `query_advice` nudge rides on the response. Tune via `search.keyword_soup_rewrite: split | nudge | off`.

## Prose corpus

Markdown headings + section bodies become first-class searchable nodes (`KindDoc`) — `search_symbols corpus: "docs"` returns ranked README / ADR / design-doc sections; `corpus: "all"` mixes them with code hits. Section node IDs are derived from the heading path, so incremental reindex of a touched markdown file produces stable IDs.

## Per-keyword TaskMemory

The combo store now keys symbol associations both on the whole query and per keyword, so a new task with similar keywords inherits learned ranking from prior searches even when the exact phrasing differs. Exact-query matches still dominate; per-keyword evidence is the lower-confidence generalisation.

## Build-tag backends

Opt-in faster local backends via build tags:

```bash
go build -tags embeddings_onnx ./cmd/gortex/   # needs: brew install onnxruntime
go build -tags embeddings_gomlx ./cmd/gortex/  # auto-downloads XLA plugin
```

The legacy `--embeddings` / `--embeddings-url` / `--embeddings-model` CLI flags and the `GORTEX_EMBEDDINGS*` env vars still take precedence over the config block — useful for one-shot overrides without editing `.gortex.yaml`.

## `search_symbols` `assist:` modes

- `auto` (default) — skips LLM for identifier queries, expands NL queries
- `on` — forces expansion + rerank
- `off` — pure BM25
- `deep` — adds a body-grounded verification pass; +1.5–4 s; quality is highly model-dependent — unreliable on 3B local models, fine on 7B+ or hosted

See [llm.md](llm.md) for provider configuration.
