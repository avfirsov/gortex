# Judge prompt

The exact prompt the judge model receives. Reproducibility:
if you change a word, bump the `judge_prompt_revision` field in
the published results.

**Current revision: 1 (2026-05-18)**

## System prompt

```
You are evaluating two answers to the same software-engineering
task. Both answers were produced by the same coding agent
working on the same codebase, but one had access to the gortex
MCP tool surface ("WITH") and the other did not ("WITHOUT").

Your job is to assign ONE of three labels:

  (a) WITH was measurably better than WITHOUT
      — more accurate factually, more complete (covers the
        relevant code paths the task asks about), fewer
        hallucinations (no claims about symbols that don't exist
        / behaviours that aren't in the code), OR substantially
        fewer tokens to reach the same answer quality

  (b) WITH and WITHOUT were roughly equivalent
      — no meaningful difference in accuracy, completeness, or
        cost; either both are good or both are mediocre

  (c) WITH was measurably worse than WITHOUT
      — less accurate, more confused (e.g. tool noise drowned
        the answer), OR noticeably more tokens for the same
        answer quality

Always pick exactly one label. If you are uncertain between (a)
and (b), or between (c) and (b), default to (b) — uncertainty is
not a marketing argument.

You will be given:

  - The task prompt (what the user asked)
  - The canonical answer (what an expert engineer would say)
  - The WITH answer
  - The WITHOUT answer
  - The token cost and wall-clock time of each run

Output JSON:

  {
    "label": "(a)|(b)|(c)",
    "reasoning": "<1-3 sentences explaining the label>",
    "facts_correct_with":    "<count|fraction|brief>",
    "facts_correct_without": "<count|fraction|brief>",
    "hallucinations_with":    "<count|fraction|brief>",
    "hallucinations_without": "<count|fraction|brief>",
    "cost_ratio": "<with_tokens / without_tokens, e.g. 0.6 or 1.4>",
    "uncertainty": "low|medium|high"
  }

Be terse in `reasoning`. The scoring summary aggregates labels,
not narrative; reasoning exists for spot-checks, not for
attempting to influence the headline.
```

## User-message template

```
TASK:
{task_prompt}

CANONICAL ANSWER (for reference):
{canonical_answer}

WITH (gortex MCP available):
[{with_token_cost} tokens · {with_wall_clock}]
{with_answer}

WITHOUT (no gortex MCP):
[{without_token_cost} tokens · {without_wall_clock}]
{without_answer}

Label this comparison.
```

## Judge model selection

The default judge is **Claude Sonnet 4.6**
(`claude-sonnet-4-20250514`). Two reasons:

1. **Different family from the WITH agent.** Sonnet 4.6 judging
   Sonnet 4.6's own answers is self-eval bias. Run the judge as
   a DIFFERENT model from the agent under test (e.g. GPT 5.4
   judging Sonnet 4.6's answers and vice versa).

2. **Cheap enough to re-run.** A 15-task × 6-runs comparison is
   90 judge invocations per session; Sonnet 4.6 makes the run
   cost negligible compared to the agent runs themselves.

For the published results we run the judge twice with two
different models (Sonnet + GPT 5.4) and report agreement /
disagreement counts. Disagreement >20% is the methodology
trigger: re-curate the seed tasks or pick a third judge.

## Anti-gaming notes

- The judge **never sees the agent identity** (no "WITH agent
  was Claude Sonnet 4.6"). Identity bias is real; the WITH /
  WITHOUT split is the only signal we want.
- Token counts are computed before the judge sees them
  (`internal/tokens` cl100k_base) so the agent can't lie about
  cost.
- Canonical answers are written by a human expert PER TASK
  before any agent runs. Writing them after seeing agent
  outputs is methodology fraud.
- Per-task token / wall-clock budgets are identical WITH and
  WITHOUT. A run that exceeds the budget is scored as "no
  answer" (not (c)).
