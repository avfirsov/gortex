# Temporal Fork Comparison — opencode agent prompt

> **Usage:** paste the contents of this file as an opencode agent prompt, or run:
> ```
> opencode run --file docs/temporal-compare/compare-temporal.md
> ```
> The agent executes the four `gortex analyze` commands, reads the real JSON output,
> and prints a structured comparison. All numbers must come from actual command output —
> never fabricate if a command fails.

---

## SYSTEM CONTEXT

You are evaluating the fork's Temporal resolution against "original" behavior, where
the SAME binary reproduces the original by setting `--temporal off` (the fork's four
Temporal passes are disabled) versus `--temporal on` (fork enhancements active).

**You are running inside a directory that contains ALL of our repositories, each cloned
as a sub-directory** (a Go worker repo, a Java client repo, shared libs, etc.). Temporal
flows cross repository boundaries — a workflow in one repo dispatches an activity defined
in another, and a Java client can drive a Go workflow. Index the **whole tree** so
cross-repo / cross-language edges can form:

```
gortex analyze --kind <kind> --path . --temporal <on|off> --format json
```
(`.` = the parent dir holding all clones. Use `gortex.exe` on Windows.)

Your job has **two parts**:

- **PART A (primary):** *Hunt corner cases yourself.* Search the cloned repos for the code
  shapes that the original resolver provably mishandles and the fork fixes, then for EACH
  one print a side-by-side "original vs fork" comparison grounded in real command output.
  The rules for finding these spots are spelled out below — follow them; do not guess.
- **PART B (rollup):** the aggregate scorecard across the whole tree (orphan deltas,
  synthesizer edge counts, resolution-outcome breakdown, verdict).

**Critical honesty rules:**
- Every claim must come from actual command output. If a command exits non-zero or emits
  no JSON, print its stderr in full and mark that section "COMMAND FAILED — data
  unavailable". Never invent or extrapolate numbers.
- The fork can be WRONG too. For every edge the fork adds, check the target is the CORRECT
  one (read the function), so you measure precision, not just "more edges". Flag any link
  that looks like a false positive.

---

# LLM AXIS — run the hunt in two LLM configurations

Cross the whole investigation with a second axis: **L0 = without our LLM** (pure
structural / BM25) and **L1 = with our LLM** (the OpenAI-compatible model from the opencode
config). Together with the temporal axis this is a 2×2:

```
                 temporal OFF (original)   temporal ON (fork)
  L0 (no LLM)          A1/A2 structural          A1/A2 structural
  L1 (our LLM)         A1/A2 + assist/ask        A1/A2 + assist/ask
```

**What the LLM axis does and does NOT change — read carefully:**
- The structural analyzers (`temporal_orphans`, `synthesizers`, `resolution_outcomes`) are
  **deterministic and LLM-independent**. PART B and the A2 snapshots produce the SAME JSON
  whether or not an LLM is configured. Do **not** claim a difference there; run those once
  and reuse. (Optionally re-run PART B under L1 purely to *confirm* the numbers are
  identical — that invariance is itself a result: the fork's structural gain is real, not
  model-conjured.)
- The LLM axis changes only **PART A discovery + correctness judging**: in L1 you have
  `search_symbols assist:deep` and `ask` to find corner cases that grep/structural patterns
  miss, and to judge whether a fork edge points at the right target. So run **A1+A2 twice**
  and compare the two corner-case result sets.

## LLM SETUP

**L0 — no LLM.** Configure gortex with no provider (or just never use LLM features): pass
`assist:off` to every `search_symbols` call and do not call `ask`. If `ask` is unregistered
(no provider), that already IS L0.

**L1 — our LLM (OpenAI-compatible, from opencode config).**
1. Read the opencode config (`./opencode.json`, `./.opencode/opencode.json`, or
   `~/.config/opencode/opencode.json`) and identify the active model id and its
   OpenAI-compatible endpoint + the env var holding the API key.
2. Point gortex at the SAME endpoint/model. For a real OpenAI endpoint the fast path is env:
   ```
   GORTEX_LLM_PROVIDER=openai  GORTEX_LLM_MODEL=<model-id>  OPENAI_API_KEY=<key>
   ```
   For any other OpenAI-compatible base_url, register a custom provider (inspect the exact
   flags first — do not guess them):
   ```
   gortex provider add --help        # confirm flag names
   gortex provider add <name> --base-url <url> --model <model> --api-key-env <ENV>
   # then select it:
   GORTEX_LLM_PROVIDER=<name>
   ```
3. Verify the provider constructed: `gortex ask "ping"` should answer (or `gortex` logs that
   `ask` is registered). If it stays unregistered (missing key / bad url), **do not silently
   fall back to L0** — report "L1 unavailable: <reason>" and run L0 only.

Tag every PART A finding with its pass id (`L0` / `L1`) so the cross-summary can diff them.

---

# PART A — Hunt & compare corner cases (primary task)

> Run this whole part **twice**: once as **L0** (no LLM — discovery by `rg`/structural,
> `search_symbols assist:off`, no `ask`) and once as **L1** (our LLM — additionally use
> `search_symbols assist:deep` and `ask "where is activity X dispatched, and what
> implements it?"` to surface and judge candidates). The A2 confirmation snapshots
> (`gortex analyze ... --format json`) are identical across L0/L1 — capture them once and
> reuse in both passes; the LLM only changes which candidates you FIND and how you judge
> correctness.

Goal: find concrete source locations where `--temporal off` (original) loses a Temporal
edge that `--temporal on` (fork) recovers, and print a per-location "original vs fork"
comparison. Work in two moves: **(A1) discover candidates from source patterns**, then
**(A2) confirm each against real off/on analyzer output**.

## A1 — Discovery rules (WHY each rule finds a fork-vs-original gap)

The fork adds exactly four Temporal passes. Each targets a code shape the original cannot
statically resolve. Search the whole tree for these shapes. Prefer `rg` (ripgrep); fall
back to `findstr /s /n` on Windows or `git grep` per repo; if the gortex MCP is connected,
`search_text` / `search_ast` are better than plain grep.

### Rule 1 — String-literal activity / child-workflow dispatch  *(pass: base temporal-stub)*
The original resolves a dispatch only when the target is passed as a **function value**
(`ExecuteActivity(ctx, a.Charge, …)`). When the target is a **string literal**
(`ExecuteActivity(ctx, "Charge", …)`) the original cannot link it to the implementing
function — the fork rebuilds that link from the registration map. So: **dispatch sites
whose name argument is a string literal are prime corner cases.**

```
rg -n --type go \
  -e 'ExecuteActivity\(\s*\w+\s*,\s*"' \
  -e 'ExecuteChildWorkflow\(\s*\w+\s*,\s*"' \
  -e 'SignalExternalWorkflow\(' \
  -e 'workflow\.(ExecuteActivity|ExecuteChildWorkflow)\(\s*\w+\s*,\s*"'
```
Ignore matches where the 2nd arg is an identifier/selector that is itself a func value —
those the original already resolves (not a gap).

### Rule 2 — Activity dispatched by name, registered indirectly / by convention  *(pass: P2 wrapper-by-name)*
A string-name dispatch where there is **no direct `RegisterActivity(<thatFunc>)`** the
resolver can follow, but a function or method exists whose name matches the dispatched
name (or the `<Name>Activity` / `<Name>Workflow` convention). The fork resolves by
func-name convention; the original leaves it dangling.

```
# 1) collect dispatched names (string args from Rule 1)
# 2) list explicit registrations:
rg -n --type go -e 'RegisterActivity(WithOptions)?\(' -e 'RegisterWorkflow(WithOptions)?\('
# 3) a dispatched name with NO matching registration line, but a func of that name:
rg -n --type go -e 'func\s+\w*Charge\w*\('   # substitute each dispatched name
```
Corner case = dispatched name ∉ registrations ∧ a same-named func exists.

### Rule 3 — Activity name from a struct field / step table  *(pass: P6 executor struct-field)*
The name isn't a literal at the call site — it comes from a **struct field** set elsewhere
(a step table / executor object): `[]Step{{Name:"Charge", Fn:…}}` dispatched via
`ExecuteActivity(ctx, step.Name, …)` or `exec.ActivityName`. The original sees a
non-constant arg and gives up; the fork joins the composite-literal field value to the
dispatch by `(type, field)`.

```
rg -n --type go \
  -e '(Name|ActivityName|WorkflowName)\s*:\s*"' \
  -e 'ExecuteActivity\([^,]+,\s*\w+\.(Name|ActivityName)\b' \
  -e '\[\]\w*Step\w*\{' -e '\bStep\{\s*Name\s*:'
```
Corner case = a dispatch whose name arg is `x.Name`/`x.ActivityName` AND that field is set
to a string literal in a composite literal somewhere in the tree.

### Rule 4 — Java → Go cross-language Temporal  *(pass: #21 cross-language bridge)*
A Java client declares a workflow/activity interface (`@WorkflowInterface`,
`@WorkflowMethod`, `@SignalMethod`, `@QueryMethod`, `@ActivityMethod`), optionally with a
`name = "…"` override, whose canonical name matches a **Go** workflow/activity registered
in another repo. The original treats Java and Go as separate worlds; the fork bridges the
Java consumer to the Go provider. **This only forms when both repos are indexed together —
that is why you index the whole tree.**

```
rg -n --type java -e '@WorkflowInterface' -e '@WorkflowMethod' -e '@SignalMethod' \
                  -e '@QueryMethod' -e '@ActivityMethod' -e 'name\s*='
rg -n --type go   -e 'RegisterWorkflow' -e 'RegisterActivity'   # match by canonical name
```
Corner case = a Java interface/method name (after applying any `name=` override) that
equals a Go-registered workflow/activity name.

### Rule 5 — Dispatch by a name variable sourced from env-with-fallback  *(pass: name resolved through the literal env fallback)*
The name argument is a **local variable**, not a literal or a struct field, and that
variable is assigned from an environment lookup **with a literal fallback default**:

```go
name := os.Getenv("CHARGE_ACTIVITY")
if name == "" {
    name = "ChargeActivity"        // ← the literal fallback
}
workflow.ExecuteActivity(ctx, name, req)
// or: n := envOr("WF_NAME", "PaymentWorkflow"); ExecuteChildWorkflow(ctx, n)
// or: n := cmp.Or(os.Getenv("WF_NAME"), "PaymentWorkflow")
```
The original resolver sees a non-constant variable and links nothing. The fork can recover
the edge through the **fallback literal** — the value the dispatch takes when the env var
is unset — and tie it to the registered workflow/activity. (This rides on the fork's
env-/constant-awareness; treat it as a HYPOTHESIS and let the off/on diff in A2 confirm
whether the fork actually resolves it. If it doesn't, report it as `no-diff` — that is a
real, useful finding about a gap the fork has NOT yet closed.)

```
# dispatch where the name arg is a bare identifier (not a literal, not x.Field):
rg -n --type go -e 'ExecuteActivity\(\s*\w+\s*,\s*\w+\s*[,)]' \
                -e 'ExecuteChildWorkflow\(\s*\w+\s*,\s*\w+\s*[,)]'
# then trace that identifier to an env-getter-with-fallback:
rg -n --type go -e 'os\.Getenv\(' -e '\benvOr\b' -e '\bGetenvDefault\b' \
                -e 'cmp\.Or\(\s*os\.Getenv' -e 'viper\.GetString\('
```
Corner case = dispatch name is a variable whose definition chains to
`os.Getenv(...)` (or an env-or-default helper) carrying a string-literal fallback that
matches a registered workflow/activity name. Use the `reads_env` capability edge (gortex
`analyze kind=env_var_users` or `walk_graph`) to confirm the variable really flows from an
env read.

Build a **candidate list**: `{ rule#, repo, file, line, dispatched_name, snippet }`. Cap at
a sensible number (e.g. 30 strongest) and say so if you truncate — never silently drop.

## A2 — Confirm each candidate (original vs fork) from real output

Index the tree once in each mode and capture the structured payloads (these carry
`from` / `file` / `line` / `name`, so you can match them back to your candidates):

```
gortex analyze --kind temporal_orphans    --path . --temporal off --format json > A_orph_off.json
gortex analyze --kind temporal_orphans    --path . --temporal on  --format json > A_orph_on.json
gortex analyze --kind synthesizers        --path . --temporal on  --format json > A_synth_on.json
gortex analyze --kind resolution_outcomes --path . --temporal off --format json > A_resout_off.json
```

For each candidate with dispatched name *N* at *file:line*, determine:
- **ORIGINAL (off):** is *N* in `broken_dispatch`, or is the activity/workflow in
  `orphan_activity` / `orphan_workflow`, in `A_orph_off.json`? Is there a matching row in
  `A_resout_off.json` (`reason` = `no_definition` / `cross_language_only` / …)? Quote it.
- **FORK (on):** is there a `temporal-stub` `samples` edge in `A_synth_on.json` whose
  `from`/`to` matches this site/target (note `via` and any `cross_language` marker)? And is
  *N* now ABSENT from the orphan arrays in `A_orph_on.json`? Quote it.
- **CORRECTNESS:** open the resolved target function and confirm it is the right
  implementation for *N* (catch false edges).

### Per-location output (print one block per confirmed candidate)

```
### [Rule <n>] <repo>/<path>:<line>
  code   : <the matched line, verbatim>
  name   : "<dispatched name>"
  ORIGINAL (--temporal off):
      <e.g. "Charge" ∈ orphan_activity; dispatch ∈ broken_dispatch
            (from=PaymentWorkflow @ payments/wf.go:42); resolution_outcome=no_definition>
  FORK (--temporal on):
      <e.g. edge PaymentWorkflow → ChargeActivity  via temporal.stub  cross_language=false;
            "Charge" no longer an orphan>
  RESULT : ✅ fork recovers this edge — original loses it
           (or ⚠️ fork links to <X> which looks WRONG — see correctness note
            or ➖ no difference / both unresolved — candidate was a false lead)
```

After the blocks, print a one-line tally:
```
Corner cases found: <N>  |  fork-recovered: <a>  |  no-diff/false-lead: <b>  |  suspected-wrong: <c>
```
The `suspected-wrong` count is as important as `fork-recovered`: it is the fork's
false-positive rate on real corner cases.

---

## A3 — L0 vs L1 cross-summary (LLM axis)

After running PART A in both LLM configs, diff the two corner-case sets:

```
=== LLM AXIS: L0 (no LLM) vs L1 (our LLM) ===
                              | L0  | L1  | delta
corner cases found           | ..  | ..  | ..
  fork-recovered             | ..  | ..  | ..
  no-diff / false-lead       | ..  | ..  | ..
  suspected-wrong (FP)       | ..  | ..  | ..

Found ONLY by L1 (LLM surfaced what structural search missed):
  - <repo/file:line> — <name> — <rule#> — <why grep missed it>
Found ONLY by L0 (LLM dropped/overlooked):
  - <repo/file:line> — ...
Verdict changes between L0 and L1 (same site judged differently):
  - <site>: L0 said <…>, L1 said <…> — likely-correct call: <…>
```

Interpretation guide:
- **L1 finds more & they confirm via A2 →** the LLM improves *recall* of real corner cases;
  the structural rules alone undercount the fork's advantage.
- **L1 finds more but they are `no-diff`/`suspected-wrong` →** LLM adds noise, not signal.
- **L0 == L1 →** the structural rules already capture the corner cases; the LLM is not
  needed for THIS evaluation (a clean, citable result).

Remember the invariant: PART B / A2 analyzer numbers are the SAME under L0 and L1. If you
ever see them differ, something is misconfigured (e.g. a stale index) — investigate, don't
report it as an LLM effect.

---

# PART B — Aggregate scorecard (whole-tree rollup)

## STEP 1 — Collect the four snapshots

Run the following commands. Replace `<REPO_PATH>` with the repository root being
analyzed (or `.` if already in the repo). Use `gortex.exe` on Windows, `gortex`
on Linux/macOS.

```
gortex analyze --kind temporal_orphans    --path <REPO_PATH> --temporal off --format json
gortex analyze --kind temporal_orphans    --path <REPO_PATH> --temporal on  --format json
gortex analyze --kind synthesizers        --path <REPO_PATH> --temporal on  --format json
gortex analyze --kind resolution_outcomes --path <REPO_PATH> --temporal off --format json
```

Capture stdout from each command into a separate variable. For each command, also
capture stderr. If exit code != 0, log stderr and mark the section failed.

---

## STEP 2 — Parse orphan totals

From the `temporal_orphans --temporal off` JSON, read the `totals` sub-object.
Expected shape:

```json
{
  "totals": {
    "broken_dispatch":    <int>,
    "signal_no_handler":  <int>,
    "query_no_handler":   <int>,
    "orphan_activity":    <int>,
    "orphan_workflow":    <int>
  }
}
```

Do the same for `temporal_orphans --temporal on`.

**Do not assume field names** — if the actual JSON has different keys, report them
verbatim and proceed with whatever keys are present.

Build and print this table:

```
Category              | OFF | ON  | DELTA (ON - OFF)
----------------------|-----|-----|------------------
broken_dispatch       |  X  |  X  |  X
signal_no_handler     |  X  |  X  |  X
query_no_handler      |  X  |  X  |  X
orphan_activity       |  X  |  X  |  X
orphan_workflow       |  X  |  X  |  X
```

A negative delta means the flag reduced orphans (improvement).

---

## STEP 3 — Report temporal-stub edge count

From the `synthesizers --temporal on` JSON, locate the `synthesizers` array.
Each element has the shape:

```json
{
  "synthesizer": "<name>",
  "provenance":  "<string>",
  "edges":       <int>,
  "by_kind":     { "<edge_kind>": <int>, ... },
  "samples":     [{ "from": "...", "to": "...", "kind": "...", "via": "..." }]
}
```

Find the entry where `synthesizer == "temporal-stub"`. Print:

```
temporal-stub synthesizer: <edges> edges
```

If no entry with `synthesizer == "temporal-stub"` exists, print:
```
temporal-stub synthesizer: NOT FOUND in synthesizers output
```

List up to 5 sample edges from the `samples` field of that entry (columns: from, to, kind).

---

## STEP 4 — Break down resolution_outcomes

From the `resolution_outcomes --temporal off` JSON, read `by_reason` (a string→int map)
and `total`.

Print:

```
resolution_outcomes (temporal=off)
  total unresolved edges: <total>

  by_reason breakdown:
    <reason>: <count>
    ...   (sorted by count descending)
```

Known reason keys (verify against actual output — there may be more):
- `ambiguous_multi_match`
- `candidate_out_of_scope`
- `cross_language_only`
- `stub_only`
- `no_definition`

---

## STEP 5 — Ground-truth caller spot-check

From the `temporal_orphans --temporal on` JSON, look at the `orphan_activity` array.
Pick the **first** entry (index 0) from that array. Note its name/identifier.

Using whatever graph query tool is available (e.g. `gortex search` or the MCP
`find_usages` tool), verify whether that activity has any callers in the repository.

Print:

```
Spot-check activity: <activity name>
  Has callers in graph: YES / NO
  (if YES) Sample caller: <caller symbol or file>
```

If the `orphan_activity` array is empty under `--temporal on`, print:
```
Spot-check: orphan_activity is empty under --temporal on (nothing to verify)
```

---

## STEP 6 — Verdict

Based on the data collected above, print a YES/NO verdict:

```
=== VERDICT ===
Does --temporal on improve Temporal resolution over --temporal off?

orphan_activity delta : <ON value> - <OFF value> = <delta>
temporal-stub edges   : <edges from Step 3>
broken_dispatch delta : <delta>

ANSWER: YES  — --temporal on reduces orphan_activity by <|delta|> and synthesizes <N> temporal-stub edges
  OR
ANSWER: NO   — --temporal on does not reduce orphan counts (delta >= 0)
  OR
ANSWER: INCONCLUSIVE — one or more commands failed; see COMMAND FAILED sections above
```

Verdict is YES only if:
1. `orphan_activity` delta is strictly negative (ON < OFF), AND
2. `temporal-stub` edge count > 0.

---

## DEEP MODE (opt-in, clearly marked)

> **This section is OPTIONAL.** Run it only when you need a thorough, reproducible
> comparison — e.g. before a release or when the fast path above shows surprising
> numbers. Deep Mode takes significantly longer because it performs two complete
> from-scratch in-process index+analyze cycles.

### What Deep Mode does differently

Each toggle state gets its **own clean in-process index run**. There is no shared
in-memory graph between the two modes. Each run re-indexes the repository from
scratch, then runs all three analyzer kinds against that fresh graph.

### Deep Mode procedure

#### Run A — temporal OFF (clean index)

```
gortex analyze --kind temporal_orphans    --path <REPO_PATH> --temporal off --format json  > deep_off_orphans.json
gortex analyze --kind synthesizers        --path <REPO_PATH> --temporal off --format json  > deep_off_synth.json
gortex analyze --kind resolution_outcomes --path <REPO_PATH> --temporal off --format json  > deep_off_resout.json
```

From the `gortex analyze` stdout header (or the JSON if it carries graph stats), also
capture total node/edge counts if available. If the JSON output does not carry graph
stats, note "graph stats not available in analyze output — run `gortex index --output json`
separately if needed".

Collect from Run A:
- `temporal_orphans` full report (`totals` object + all five arrays)
- `synthesizers` array (all entries, not just temporal-stub)
- `resolution_outcomes`: `total` + `by_reason` map

#### Run B — temporal ON (clean index)

```
gortex analyze --kind temporal_orphans    --path <REPO_PATH> --temporal on --format json  > deep_on_orphans.json
gortex analyze --kind synthesizers        --path <REPO_PATH> --temporal on --format json  > deep_on_synth.json
gortex analyze --kind resolution_outcomes --path <REPO_PATH> --temporal on --format json  > deep_on_resout.json
```

Collect the same fields as Run A.

#### Deep Mode diff

Print a complete diff table:

```
=== DEEP MODE DIFF: temporal OFF vs ON ===

--- Orphan totals ---
Category              | OFF | ON  | DELTA
broken_dispatch       | ... | ... | ...
signal_no_handler     | ... | ... | ...
query_no_handler      | ... | ... | ...
orphan_activity       | ... | ... | ...
orphan_workflow       | ... | ... | ...

--- Synthesizers (all groups) ---
Synthesizer           | OFF edges | ON edges | DELTA
<synthesizer name>    | ...       | ...      | ...
...

--- Resolution outcomes ---
Reason                | OFF count | ON count | DELTA
...

--- Edges present ONLY in temporal ON (from temporal-stub samples) ---
From                  | To                   | Kind
...   (up to 20 rows from the ON temporal-stub `samples` field)

--- Activities that flipped from orphan → linked ---
(activities present in OFF orphan_activity but absent from ON orphan_activity)
<activity name>
...
```

Each rebuild is independent — do not reuse output from a previous run. If either
run fails, mark it FAILED and complete the diff with whatever data is available.

---

*End of prompt. Do not print this line in your output.*
