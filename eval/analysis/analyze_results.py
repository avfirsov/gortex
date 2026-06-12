#!/usr/bin/env python3
"""Results analyzer for the Gortex eval framework.

Reads evaluation results and generates comparative analysis:
- Summary table of patch rate, cost, tokens, duration per (model, mode)
- Side-by-side mode comparison for a specific model
- Gortex tool usage frequency and latency breakdown

Usage:
    python -m eval.analysis.analyze_results summary results/
    python -m eval.analysis.analyze_results compare-modes results/ -m claude-sonnet
    python -m eval.analysis.analyze_results tool-usage results/
    python -m eval.analysis.analyze_results summary results/ --format csv
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import sys
from pathlib import Path
from typing import Any

from tabulate import tabulate

from results import RunSummary


# ---------------------------------------------------------------------------
# Data loading
# ---------------------------------------------------------------------------


def _load_summaries(results_dir: Path) -> list[RunSummary]:
    """Load summaries from *results_dir*, recomputing from per-instance files when available."""
    summaries: list[RunSummary] = []
    if not results_dir.is_dir():
        return summaries
    for run_dir in sorted(results_dir.iterdir()):
        if not run_dir.is_dir():
            continue

        # Collect per-instance result files
        instance_results = []
        for inst_dir in sorted(run_dir.iterdir()):
            if not inst_dir.is_dir():
                continue
            for json_file in inst_dir.glob("*.json"):
                if "_trajectory" in json_file.name:
                    continue
                try:
                    instance_results.append(json.loads(json_file.read_text()))
                except Exception:
                    pass

        if not instance_results:
            # Fall back to summary.json
            summary_path = run_dir / "summary.json"
            if summary_path.exists():
                try:
                    data = json.loads(summary_path.read_text())
                    summaries.append(RunSummary.from_dict(data))
                except Exception:
                    pass
            continue

        # Recompute summary from per-instance data
        total = len(instance_results)
        patches = sum(1 for r in instance_results if r.get("submission"))
        total_cost = sum(r.get("cost", 0) for r in instance_results)
        total_tokens = sum(r.get("tokens_input", 0) + r.get("tokens_output", 0) for r in instance_results)
        total_duration = sum(r.get("duration_seconds", 0) for r in instance_results)
        completed = sum(1 for r in instance_results if r.get("exit_status") not in (None, "error", "setup_failure"))

        model = instance_results[0].get("model", "")
        mode = instance_results[0].get("mode", "")

        summaries.append(RunSummary(
            run_id=run_dir.name,
            model=model,
            mode=mode,
            total_instances=total,
            completed=completed,
            patch_rate=patches / total if total else 0,
            total_cost=total_cost,
            mean_cost=total_cost / total if total else 0,
            total_tokens=total_tokens,
            mean_tokens=total_tokens / total if total else 0,
            total_duration_seconds=total_duration,
            mean_duration_seconds=total_duration / total if total else 0,
        ))

    return summaries


def _load_instance_results(results_dir: Path) -> list[dict[str, Any]]:
    """Load all per-instance JSON result files from *results_dir*."""
    instances: list[dict[str, Any]] = []
    if not results_dir.is_dir():
        return instances
    for run_dir in sorted(results_dir.iterdir()):
        if not run_dir.is_dir():
            continue
        for inst_dir in sorted(run_dir.iterdir()):
            if not inst_dir.is_dir():
                continue
            for json_file in inst_dir.glob("*.json"):
                try:
                    instances.append(json.loads(json_file.read_text()))
                except Exception:
                    pass
    return instances


# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------



def _output(headers: list[str], rows: list[list[Any]], fmt: str) -> None:
    """Print *rows* with *headers* in the requested format."""
    if fmt == "csv":
        buf = io.StringIO()
        writer = csv.writer(buf)
        writer.writerow(headers)
        writer.writerows(rows)
        sys.stdout.write(buf.getvalue())
    else:
        print(tabulate(rows, headers=headers, tablefmt="grid"))


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------


def summary(results_dir: str, fmt: str = "table", swebench_eval: bool = False) -> None:
    """Table of patch rate, mean cost, mean tokens, mean duration per (model, mode)."""
    summaries = _load_summaries(Path(results_dir))
    if not summaries:
        print(f"No results found in {results_dir}")
        return

    if swebench_eval:
        print(
            "NOTE: --swebench-eval is a placeholder. "
            "To run the official SWE-bench test harness, install the swebench "
            "package and invoke:\n"
            "  python -m swebench.harness.run_evaluation "
            "--predictions_path <results>/<run_id>/preds.json "
            "--dataset_name princeton-nlp/SWE-Bench_Lite"
        )
        print()

    headers = ["Model", "Mode", "Instances", "Patch Rate", "Mean Cost", "Mean Tokens", "Mean Duration (s)"]
    rows: list[list[Any]] = []
    for s in summaries:
        rows.append([
            s.model,
            s.mode,
            s.total_instances,
            f"{s.patch_rate:.1%}",
            f"${s.mean_cost:.4f}",
            f"{s.mean_tokens:.0f}",
            f"{s.mean_duration_seconds:.1f}",
        ])

    _output(headers, rows, fmt)


def compare_modes(results_dir: str, model: str, fmt: str = "table") -> None:
    """Side-by-side baseline vs native vs native_augment with deltas for *model*."""
    summaries = _load_summaries(Path(results_dir))
    model_runs = {s.mode: s for s in summaries if s.model == model}

    if not model_runs:
        print(f"No results found for model: {model}")
        return

    mode_order = [m for m in ("baseline", "native", "native_augment") if m in model_runs]
    mode_order += sorted(set(model_runs) - set(mode_order))

    metrics = ["patch_rate", "mean_cost", "mean_tokens", "mean_duration_seconds"]
    metric_labels = ["Patch Rate", "Mean Cost ($)", "Mean Tokens", "Mean Duration (s)"]

    headers = ["Metric"] + mode_order
    # Add delta columns if baseline exists
    baseline = model_runs.get("baseline")
    if baseline:
        for m in mode_order:
            if m != "baseline":
                headers.append(f"Δ {m} vs baseline")

    rows: list[list[Any]] = []
    for label, attr in zip(metric_labels, metrics):
        row: list[Any] = [label]
        values: dict[str, float] = {}
        for mode in mode_order:
            s = model_runs[mode]
            v = getattr(s, attr, 0.0)
            values[mode] = v
            if attr == "patch_rate":
                row.append(f"{v:.1%}")
            elif attr == "mean_cost":
                row.append(f"${v:.4f}")
            else:
                row.append(f"{v:.1f}")

        if baseline:
            bv = values.get("baseline", 0.0)
            for mode in mode_order:
                if mode == "baseline":
                    continue
                mv = values[mode]
                if bv != 0:
                    delta_pct = ((mv - bv) / abs(bv)) * 100
                    row.append(f"{delta_pct:+.1f}%")
                else:
                    row.append("N/A")

        rows.append(row)

    print(f"\nMode comparison for model: {model}\n")
    _output(headers, rows, fmt)


def tool_usage(results_dir: str, fmt: str = "table") -> None:
    """Gortex tool call frequency and latency breakdown per tool name."""
    instances = _load_instance_results(Path(results_dir))
    if not instances:
        print(f"No instance results found in {results_dir}")
        return

    # Aggregate tool calls across all instances
    tool_counts: dict[str, int] = {}
    tool_latencies: dict[str, list[float]] = {}

    for inst in instances:
        gm = inst.get("gortex_metrics", {})
        calls = gm.get("tool_calls", {})
        for tool_name, count in calls.items():
            tool_counts[tool_name] = tool_counts.get(tool_name, 0) + count

        # If per-tool latencies are available
        latencies = gm.get("tool_latencies", {})
        for tool_name, lat in latencies.items():
            tool_latencies.setdefault(tool_name, []).append(lat)

    if not tool_counts:
        print("No Gortex tool usage data found.")
        return

    headers = ["Tool", "Total Calls", "Mean Latency (s)"]
    rows: list[list[Any]] = []
    for tool_name in sorted(tool_counts):
        count = tool_counts[tool_name]
        lats = tool_latencies.get(tool_name, [])
        mean_lat = f"{sum(lats) / len(lats):.3f}" if lats else "N/A"
        rows.append([tool_name, count, mean_lat])

    _output(headers, rows, fmt)


# ---------------------------------------------------------------------------
# CLI (argparse)
# ---------------------------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="analyze_results",
        description="Post-run analysis for Gortex eval results.",
    )
    parser.add_argument(
        "--format",
        choices=["csv", "table"],
        default="table",
        help="Output format (default: table)",
    )

    subparsers = parser.add_subparsers(dest="command", required=True)

    # summary
    sp_summary = subparsers.add_parser("summary", help="Summary table per (model, mode)")
    sp_summary.add_argument("results_dir", help="Path to results directory")
    sp_summary.add_argument(
        "--swebench-eval",
        action="store_true",
        default=False,
        help="Run official SWE-bench test harness on collected patches",
    )

    # compare-modes
    sp_compare = subparsers.add_parser("compare-modes", help="Side-by-side mode comparison")
    sp_compare.add_argument("results_dir", help="Path to results directory")
    sp_compare.add_argument("-m", "--model", required=True, help="Model to compare across modes")

    # tool-usage
    sp_tools = subparsers.add_parser("tool-usage", help="Gortex tool call frequency and latency")
    sp_tools.add_argument("results_dir", help="Path to results directory")

    args = parser.parse_args()

    if args.command == "summary":
        summary(args.results_dir, fmt=args.format, swebench_eval=args.swebench_eval)
    elif args.command == "compare-modes":
        compare_modes(args.results_dir, model=args.model, fmt=args.format)
    elif args.command == "tool-usage":
        tool_usage(args.results_dir, fmt=args.format)


if __name__ == "__main__":
    main()
