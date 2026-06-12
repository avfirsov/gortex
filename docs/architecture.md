# Architecture & graph schema

## Component diagram

```
gortex binary
  CLI (cobra)    ──> MultiIndexer ──> In-Memory Graph (shared, per-repo indexed)
  MCP (stdio)    ──────────────────> Query Engine (repo/project/ref scoping)
  HTTP /v1/*     ──────────────────> same tools + /v1/graph + /v1/events (SSE)
  Daemon (unix)  ──────────────────> shared graph for every MCP client, session isolation
  MCP Prompts    ──────────────────> (pre_commit, orientation, safe_to_change)
  MCP Resources  ──────────────────> (16 read-only URIs — bootstrap state + analyzer rollups)
                   MultiWatcher <── filesystem events (fsnotify, per-repo)
                   CrossRepoResolver ──> cross-repo edge creation (type-aware)
                   Persistence ──> gob+gzip snapshot (pluggable backend)
```

## Data flow

1. On startup, loads cached graph snapshot if available; otherwise performs full indexing.
2. MultiIndexer walks each repo directory concurrently, dispatches files to language-specific extractors (tree-sitter).
3. Extractors produce nodes (files, functions, types, etc.) and edges (calls, imports, defines, etc.) with type environment metadata.
4. In multi-repo mode, nodes get `RepoPrefix` and IDs become `<repo_prefix>/<path>::<Symbol>`.
5. Resolver links cross-file references with type-aware method matching; CrossRepoResolver links cross-repo references with same-repo preference.
6. Query Engine answers traversal queries with optional repo/project/ref scoping.
7. MultiWatcher detects changes per-repo and surgically patches the graph (debounced per-file), then re-resolves cross-repo edges.
8. On shutdown, persists graph snapshot for fast restart.

## Graph schema

**Node kinds:**

- Code structure: `file`, `package`, `function`, `method`, `type`, `interface`, `field`, `variable`, `constant`, `import`, `contract`, `param`, `closure`, `enum_member`, `generic_param`
- Coverage extensions: `module`, `table`, `column`, `config_key`, `flag`, `event`, `migration`, `fixture`, `todo`, `team`, `license`, `release`
- Infrastructure: `resource` (K8s manifest), `kustomization` (Kustomize overlay), `image` (Dockerfile FROM / K8s `container.image`)

**Edge kinds:**

- Calls / structure: `calls`, `imports`, `defines`, `implements`, `extends`, `overrides`, `references`, `member_of`, `instantiates`, `provides`, `consumes`, `composes`, `aliases`, `typed_as`, `returns`, `captures`, `param_of`
- Concurrency / mutation: `spawns`, `sends`, `recvs`, `reads`, `writes`, `reads_config`, `writes_config`
- Dataflow (CPG-lite): `value_flow`, `arg_of`, `returns_to`
- Metadata: `annotated`, `emits`, `throws`, `queries`, `reads_col`, `writes_col`, `toggles_flag`, `depends_on_module`, `matches`, `generated_by`, `tests`, `covered_by`, `owns`, `authored`, `licensed_as`
- Framework / infrastructure: `handles_route`, `models_table`, `renders_child`, `configures`, `mounts`, `exposes`, `depends_on`, `uses_env`
- Similarity: `similar_to` (MinHash + LSH near-duplicate clones; `Meta["similarity"]` carries the estimated Jaccard score), `semantically_related` (graph-diffusion smoothing — transitively blends clone-similarity scores so indirectly-related symbols connect)
- Workspace: `workspace_member` — links a package-manager workspace root (npm / pnpm / Cargo) to each of its members
- Cross-repo: `cross_repo_calls` / `cross_repo_implements` / `cross_repo_extends` — materialised whenever a `calls` / `implements` / `extends` edge's endpoints live in different repos

**Multi-repo fields:** Nodes carry `repo_prefix` (empty in single-repo mode). Edges carry `cross_repo` (true when connecting nodes in different repos). Node IDs use `<repo_prefix>/<path>::<Symbol>` format in multi-repo mode.

**Test taxonomy:** functions and methods in test files carry `Meta["is_test"]` + `Meta["test_role"]` (`test` / `benchmark` / `fuzz` / `example`) + `Meta["test_runner"]`. The runner identifier is one of `gotest` / `pytest` / `unittest` / `rspec` / `minitest` / `test-unit` / `jest` / `vitest` / `mocha` / `bun-test` / `node-test` / `playwright` / `cypress`, resolved from parser-stamped imports (JS / TS) with a Mocha-TDD `suite()` byte fallback and language-default fill-in (Go is always `gotest`, Python defaults to `pytest`, Ruby uses the `_spec.rb` / `_test.rb` suffix). The owning `KindFile` also gets the same `test_runner` stamp so file-level queries can group tests by runner without walking functions.

## Graph persistence

Gortex snapshots the graph to disk on shutdown and restores it on startup, with incremental re-indexing of only changed files:

```bash
# Default cache directory: ~/.gortex/cache/
gortex mcp --index /path/to/repo

# Custom cache directory
gortex mcp --index /path/to/repo --cache-dir /tmp/gortex-cache

# Disable caching
gortex mcp --index /path/to/repo --no-cache
```

The persistence layer uses a pluggable backend interface (`persistence.Store`). The default backend serializes as gob+gzip. Cache is keyed by repo path + git commit hash, with version validation to invalidate on binary upgrades.

**XDG base directories.** Gortex honors the XDG Base Directory spec — `XDG_CONFIG_HOME` for configuration, `XDG_DATA_HOME` for durable data (memories, notes, feedback), and `XDG_CACHE_HOME` for the graph snapshot, token cache, and savings ledger. When set to an absolute path, the variable wins on every platform (Linux / macOS / Windows alike). When unset, every path resolves to its prior default — so upgrading never orphans an existing install's config, memories, or cache. `--cache-dir` still overrides the cache location explicitly.

## Scale — battle-tested on large repos

Measured on an Apple Silicon laptop with the default CGO build:

| Repository | Files | Nodes | Edges | Index time | Throughput | Peak heap |
| ---------- | ----: | ----: | ----: | ---------: | ---------: | --------: |
| [torvalds/linux](https://github.com/torvalds/linux) | 70,333 | 1,690,174 | 6,239,570 | ~3 min | 300 files/s | 5.07 GB |
| [microsoft/vscode](https://github.com/microsoft/vscode) | 10,762 | 204,501 | 808,902 | ~1 min | 143 files/s | 580 MB |
| zzet/gortex (self) | 430 | 5,583 | 53,830 | 3.4s | 127 files/s | 52 MB |

Parsing dominates wall time (65–80%); reference resolution and search-index build scale sub-linearly. Everything runs in-process — no external services, no database, no network.
