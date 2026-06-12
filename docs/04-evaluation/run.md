# How to run

Operational recipe for executing the full methodology against a
tagged build and publishing the results.

## Prerequisites

- `gortex` binary built from the tagged commit
- `python3 -m pip install -r eval/requirements.txt` (Python
  harness deps; existing file)
- API keys: `ANTHROPIC_API_KEY` (for Sonnet 4.6),
  `OPENAI_API_KEY` (for GPT 5.4), and a working `gh copilot`
  installation (for Copilot CLI). At least one is enough for a
  partial run.
- A working corpus checkout (default: the gortex repo itself)

## End-to-end

```sh
# 1) Tag the build — every published result cites this SHA.
git rev-parse HEAD > eval/results/$(date +%Y%m%d)/HEAD.sha

# 2) Run the full matrix: 15 tasks × 3 agents × 2 modes × 2 prompts.
#    Estimated wall clock: ~3-6 hours per agent.
gortex eval run \
    --task-set docs/04-evaluation/task-set.md \
    --judge-prompt docs/04-evaluation/judge-prompt.md \
    --agents sonnet-4.6,gpt-5.4,copilot \
    --corpus . \
    --out eval/results/$(date +%Y%m%d)/ \
    --max-task-tokens 50000 \
    --max-task-seconds 300

# 3) Aggregate per-task scores into the summary table.
python3 eval/scripts/aggregate.py \
    --workdir eval/results/$(date +%Y%m%d)/ \
    --judges sonnet-4.6,gpt-5.4 \
    --out eval/results/$(date +%Y%m%d)/summary.md

# 4) Spot-check 5 random tasks per category by hand BEFORE
#    publishing. The judge is good, not infallible.
python3 eval/scripts/spotcheck.py \
    --workdir eval/results/$(date +%Y%m%d)/ \
    --sample 5 \
    --out eval/results/$(date +%Y%m%d)/spotcheck.md

# 5) Promote into BENCHMARK.md (manual edit; doc owner).
$EDITOR BENCHMARK.md
```

## What lands on disk

```
eval/results/<date>/
├── HEAD.sha                            # tagged commit
├── summary.md                          # the published table
├── spotcheck.md                        # manual review notes
├── disagreement.md                     # judge-vs-judge disagreement
├── per-task/
│   ├── 1.1-indexer-walkthrough/
│   │   ├── sonnet-4.6-with-default.json
│   │   ├── sonnet-4.6-without-default.json
│   │   ├── sonnet-4.6-with-ablation.json
│   │   ├── sonnet-4.6-without-ablation.json
│   │   ├── gpt-5.4-with-default.json
│   │   └── ...
│   ├── 1.2-community-detection/
│   └── ...
└── judge-runs/
    ├── sonnet-4.6-judging/
    └── gpt-5.4-judging/
```

Every per-task JSON contains: task prompt, canonical answer,
agent answer, token cost, wall clock, tools called (count +
list), and (if judged) the judge's label + reasoning + agreement
between judges.

## What to publish

In `BENCHMARK.md`, add a section like:

```markdown
## Agent-graded evaluation

**Last run: 2026-MM-DD** · agents: Sonnet 4.6 / GPT 5.4 /
Copilot CLI · judge: Sonnet 4.6 + GPT 5.4 (agreement 87%)

| Category | (a) gortex helped | (b) no difference | (c) gortex hurt |
|----------|------------------:|------------------:|----------------:|
| Architectural explanation |  6 | 1 | 2 |
| Refactor safety           |  7 | 2 | 0 |
| Bug localization          |  5 | 2 | 2 |
| Impact analysis           |  8 | 1 | 0 |
| Contract extraction       |  6 | 3 | 0 |
| **Total**                 | 32 | 9 | 4 |

- Default-prompt vs ablation-prompt delta:  +2 (a) / 0 (b) / -1 (c)
  — gortex prompt steering helps but isn't load-bearing.
- (c) cases written up in `eval/results/2026-MM-DD/c-cases.md`
  — every loss has a public post-mortem.
```

**Required**: cite the (c) count, link to the (c)
post-mortems, and call out the prompt-bias delta. A publication
that hides (c) results is non-compliant with this methodology
and should not be referenced as a benchmark.

## Cost envelope

A single full run (15 tasks × 3 agents × 2 modes × 2 prompts =
180 agent runs + ~360 judge invocations) costs roughly:

- Anthropic API:  ~$15-30 (Sonnet 4.6 agent + Sonnet 4.6 judge)
- OpenAI API:     ~$10-25 (GPT 5.4 agent + GPT 5.4 judge)
- Copilot CLI:    subscription-included

Total: ~$25-55 per full run. Run quarterly + on every major
version bump.

## Partial-run modes

When you only have one API key:

```sh
# Just Sonnet 4.6 (cheapest path)
gortex eval run --agents sonnet-4.6 --task-set ...

# Just one task category (smoke before the full run)
gortex eval run --agents sonnet-4.6 \
    --task-set docs/04-evaluation/task-set.md \
    --categories "Refactor safety"

# Just the WITH mode (compare absolute quality across agents)
gortex eval run --agents sonnet-4.6,gpt-5.4,copilot \
    --modes with
```

Partial runs are useful for iteration but **don't publish
partial-run numbers as benchmarks** — the methodology requires
the full matrix.
