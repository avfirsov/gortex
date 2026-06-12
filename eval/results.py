"""Result store for the Gortex eval framework.

Provides dataclasses for per-instance results and run summaries, plus
persistence helpers that write JSON to the results directory.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import Any


@dataclass
class InstanceResult:
    """Per-instance evaluation result with all metric fields."""

    instance_id: str
    model: str
    mode: str
    exit_status: str
    submission: str = ""
    cost: float = 0.0
    tokens_input: int = 0
    tokens_output: int = 0
    n_calls: int = 0
    n_steps: int = 0
    duration_seconds: float = 0.0
    gortex_metrics: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        """Serialize to a plain dict."""
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> InstanceResult:
        """Deserialize from a plain dict."""
        return cls(**{k: v for k, v in data.items() if k in cls.__dataclass_fields__})


@dataclass
class RunSummary:
    """Aggregate summary for an evaluation run."""

    run_id: str
    model: str
    mode: str
    timestamp: float = 0.0
    config: dict[str, Any] = field(default_factory=dict)
    total_instances: int = 0
    completed: int = 0
    patch_rate: float = 0.0
    total_cost: float = 0.0
    mean_cost: float = 0.0
    total_tokens: int = 0
    mean_tokens: float = 0.0
    total_duration_seconds: float = 0.0
    mean_duration_seconds: float = 0.0

    def to_dict(self) -> dict[str, Any]:
        """Serialize to a plain dict."""
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> RunSummary:
        """Deserialize from a plain dict."""
        return cls(**{k: v for k, v in data.items() if k in cls.__dataclass_fields__})


def save_instance_result(result: InstanceResult, run_id: str, base_dir: Path | None = None) -> Path:
    """Write an instance result to ``results/{run_id}/{instance_id}/{instance_id}.json``.

    Returns the path to the written file.
    """
    base = base_dir or Path("results")
    out_dir = base / run_id / result.instance_id
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / f"{result.instance_id}.json"
    out_path.write_text(json.dumps(result.to_dict(), indent=2))
    return out_path


def save_run_summary(
    results: list[InstanceResult],
    run_id: str,
    config: dict[str, Any],
    base_dir: Path | None = None,
) -> RunSummary:
    """Compute aggregate metrics from *results* and write ``results/{run_id}/summary.json``.

    Returns the computed :class:`RunSummary`.
    """
    base = base_dir or Path("results")
    out_dir = base / run_id
    out_dir.mkdir(parents=True, exist_ok=True)

    total = len(results)
    if total == 0:
        summary = RunSummary(
            run_id=run_id,
            model="",
            mode="",
            timestamp=time.time(),
            config=config,
        )
    else:
        patches = sum(1 for r in results if r.submission)
        total_cost = sum(r.cost for r in results)
        total_tokens = sum(r.tokens_input + r.tokens_output for r in results)
        total_duration = sum(r.duration_seconds for r in results)
        completed = sum(1 for r in results if r.exit_status == "submitted")

        summary = RunSummary(
            run_id=run_id,
            model=results[0].model,
            mode=results[0].mode,
            timestamp=time.time(),
            config=config,
            total_instances=total,
            completed=completed,
            patch_rate=patches / total,
            total_cost=total_cost,
            mean_cost=total_cost / total,
            total_tokens=total_tokens,
            mean_tokens=total_tokens / total,
            total_duration_seconds=total_duration,
            mean_duration_seconds=total_duration / total,
        )

    out_path = out_dir / "summary.json"
    out_path.write_text(json.dumps(summary.to_dict(), indent=2))
    return summary


def save_predictions(
    results: list[InstanceResult],
    run_id: str,
    base_dir: Path | None = None,
) -> Path:
    """Write SWE-bench compatible ``preds.json`` to ``results/{run_id}/preds.json``.

    Returns the path to the written file.
    """
    base = base_dir or Path("results")
    out_dir = base / run_id
    out_dir.mkdir(parents=True, exist_ok=True)

    preds: dict[str, dict[str, str]] = {}
    for r in results:
        preds[r.instance_id] = {
            "model_name_or_path": r.model,
            "instance_id": r.instance_id,
            "model_patch": r.submission,
        }

    out_path = out_dir / "preds.json"
    out_path.write_text(json.dumps(preds, indent=2))
    return out_path
