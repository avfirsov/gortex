"""Property-based tests for eval.results module.

Feature: eval-framework
Uses hypothesis to verify result completeness, serialization, and aggregation.
"""

from __future__ import annotations

import json
import tempfile
from pathlib import Path
from typing import Any

import pytest
from hypothesis import given, settings, assume
from hypothesis import strategies as st

from results import InstanceResult, RunSummary, save_run_summary

# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

_MODES = ["baseline", "native", "native_augment"]

_TOOL_NAMES = [
    "search_symbols",
    "smart_context",
    "explain_change_impact",
    "graph_stats",
    "find_usages",
]

_EXIT_STATUSES = ["submitted", "setup_failure", "api_error", "cost_limit", "step_limit"]


def _gortex_tool_calls_st() -> st.SearchStrategy[dict[str, int]]:
    """Strategy for gortex_metrics.tool_calls dict."""
    return st.fixed_dictionaries(
        {name: st.integers(min_value=0, max_value=50) for name in _TOOL_NAMES}
    )


def _gortex_metrics_st(mode: str) -> st.SearchStrategy[dict[str, Any]]:
    """Strategy for gortex_metrics based on mode."""
    if mode == "baseline":
        return st.just({})
    elif mode == "native":
        return st.builds(
            lambda tc: {
                "tool_calls": tc,
                "total_tool_calls": sum(tc.values()),
            },
            tc=_gortex_tool_calls_st(),
        )
    else:  # native_augment
        return st.builds(
            lambda tc, aug_calls, aug_hits, aug_errors, aug_time, idx_time: {
                "tool_calls": tc,
                "total_tool_calls": sum(tc.values()),
                "augmentation_calls": aug_calls,
                "augmentation_hits": aug_hits,
                "augmentation_errors": aug_errors,
                "augmentation_time_seconds": aug_time,
                "index_time_seconds": idx_time,
            },
            tc=_gortex_tool_calls_st(),
            aug_calls=st.integers(min_value=0, max_value=100),
            aug_hits=st.integers(min_value=0, max_value=100),
            aug_errors=st.integers(min_value=0, max_value=20),
            aug_time=st.floats(min_value=0.0, max_value=60.0, allow_nan=False, allow_infinity=False),
            idx_time=st.floats(min_value=0.0, max_value=300.0, allow_nan=False, allow_infinity=False),
        )


@st.composite
def instance_result_st(draw: st.DrawFn, mode: str | None = None) -> InstanceResult:
    """Strategy that generates a valid InstanceResult for a given mode."""
    m = mode if mode is not None else draw(st.sampled_from(_MODES))
    return InstanceResult(
        instance_id=draw(st.text(
            alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
            min_size=1, max_size=30,
        )),
        model=draw(st.text(
            alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
            min_size=1, max_size=20,
        )),
        mode=m,
        exit_status=draw(st.sampled_from(_EXIT_STATUSES)),
        submission=draw(st.text(min_size=0, max_size=200)),
        cost=draw(st.floats(min_value=0.0, max_value=100.0, allow_nan=False, allow_infinity=False)),
        tokens_input=draw(st.integers(min_value=0, max_value=500_000)),
        tokens_output=draw(st.integers(min_value=0, max_value=100_000)),
        n_calls=draw(st.integers(min_value=0, max_value=200)),
        n_steps=draw(st.integers(min_value=0, max_value=100)),
        duration_seconds=draw(st.floats(min_value=0.0, max_value=3600.0, allow_nan=False, allow_infinity=False)),
        gortex_metrics=draw(_gortex_metrics_st(m)),
    )


# ---------------------------------------------------------------------------
# Property 7: Result completeness per mode
# Feature: eval-framework, Property 7: Result completeness per mode
# **Validates: Requirements 6.1, 6.2, 6.3**
# ---------------------------------------------------------------------------


class TestResultCompletenessPerMode:
    """Base fields always present; native/native_augment include gortex tool
    metrics; native_augment includes augmentation metrics."""

    _BASE_FIELDS = {
        "instance_id", "model", "mode", "exit_status", "submission",
        "cost", "tokens_input", "tokens_output", "n_calls", "n_steps",
        "duration_seconds", "gortex_metrics",
    }

    @given(result=instance_result_st())
    @settings(max_examples=100)
    def test_base_fields_always_present(self, result: InstanceResult) -> None:
        """All base metric fields are present regardless of mode."""
        d = result.to_dict()
        for field_name in self._BASE_FIELDS:
            assert field_name in d, f"Missing base field: {field_name}"

    @given(result=instance_result_st(mode="native"))
    @settings(max_examples=100)
    def test_native_mode_has_gortex_tool_metrics(self, result: InstanceResult) -> None:
        """Native mode results include gortex tool call metrics."""
        metrics = result.gortex_metrics
        assert "tool_calls" in metrics, "native mode must have tool_calls"
        assert "total_tool_calls" in metrics, "native mode must have total_tool_calls"
        for tool_name in _TOOL_NAMES:
            assert tool_name in metrics["tool_calls"], f"Missing tool: {tool_name}"

    @given(result=instance_result_st(mode="native_augment"))
    @settings(max_examples=100)
    def test_native_augment_mode_has_augmentation_metrics(self, result: InstanceResult) -> None:
        """native_augment mode results include both tool and augmentation metrics."""
        metrics = result.gortex_metrics
        # Tool metrics
        assert "tool_calls" in metrics
        assert "total_tool_calls" in metrics
        # Augmentation metrics
        assert "augmentation_calls" in metrics, "native_augment must have augmentation_calls"
        assert "augmentation_hits" in metrics, "native_augment must have augmentation_hits"
        assert "augmentation_errors" in metrics, "native_augment must have augmentation_errors"
        assert "augmentation_time_seconds" in metrics, "native_augment must have augmentation_time_seconds"

    @given(result=instance_result_st(mode="baseline"))
    @settings(max_examples=100)
    def test_baseline_mode_has_empty_gortex_metrics(self, result: InstanceResult) -> None:
        """Baseline mode results have empty gortex_metrics."""
        assert result.gortex_metrics == {}


# ---------------------------------------------------------------------------
# Property 8: Result serialization round-trip
# Feature: eval-framework, Property 8: Result serialization round-trip
# **Validates: Requirements 6.4**
# ---------------------------------------------------------------------------


class TestResultSerializationRoundTrip:
    """Serialize → deserialize produces equivalent object with all fields preserved."""

    @given(result=instance_result_st())
    @settings(max_examples=100)
    def test_instance_result_round_trip(self, result: InstanceResult) -> None:
        """InstanceResult survives to_dict → from_dict round-trip."""
        d = result.to_dict()
        restored = InstanceResult.from_dict(d)
        assert restored.to_dict() == d

    @given(result=instance_result_st())
    @settings(max_examples=100)
    def test_instance_result_json_round_trip(self, result: InstanceResult) -> None:
        """InstanceResult survives to_dict → JSON string → parse → from_dict."""
        d = result.to_dict()
        json_str = json.dumps(d)
        parsed = json.loads(json_str)
        restored = InstanceResult.from_dict(parsed)
        assert restored.to_dict() == d

    @given(
        run_id=st.text(
            alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
            min_size=1, max_size=20,
        ),
        model=st.text(
            alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
            min_size=1, max_size=20,
        ),
        mode=st.sampled_from(_MODES),
    )
    @settings(max_examples=100)
    def test_run_summary_round_trip(self, run_id: str, model: str, mode: str) -> None:
        """RunSummary survives to_dict → from_dict round-trip."""
        summary = RunSummary(
            run_id=run_id,
            model=model,
            mode=mode,
            timestamp=1234567890.0,
            config={"model": {"model_name": model}},
            total_instances=10,
            completed=8,
            patch_rate=0.8,
            total_cost=5.0,
            mean_cost=0.5,
            total_tokens=10000,
            mean_tokens=1000.0,
            total_duration_seconds=100.0,
            mean_duration_seconds=10.0,
        )
        d = summary.to_dict()
        restored = RunSummary.from_dict(d)
        assert restored.to_dict() == d


# ---------------------------------------------------------------------------
# Property 9: Aggregate metric correctness
# Feature: eval-framework, Property 9: Aggregate metric correctness
# **Validates: Requirements 6.5, 7.1, 7.3**
# ---------------------------------------------------------------------------


class TestAggregateMetricCorrectness:
    """patch_rate = patches/total, mean_cost = total_cost/count,
    per-tool aggregations = sum of per-instance counts."""

    @given(results=st.lists(instance_result_st(), min_size=1, max_size=20))
    @settings(max_examples=100)
    def test_patch_rate_equals_patches_over_total(self, results: list[InstanceResult]) -> None:
        """patch_rate = count of results with non-empty submission / total."""
        for r in results:
            r.model = "test-model"
            r.mode = "native"

        with tempfile.TemporaryDirectory() as td:
            summary = save_run_summary(results, "test-run", {}, base_dir=Path(td))

        patches = sum(1 for r in results if r.submission)
        expected_rate = patches / len(results)
        assert abs(summary.patch_rate - expected_rate) < 1e-9

    @given(results=st.lists(instance_result_st(), min_size=1, max_size=20))
    @settings(max_examples=100)
    def test_mean_cost_equals_total_over_count(self, results: list[InstanceResult]) -> None:
        """mean_cost = total_cost / instance count."""
        for r in results:
            r.model = "test-model"
            r.mode = "native"

        with tempfile.TemporaryDirectory() as td:
            summary = save_run_summary(results, "test-run", {}, base_dir=Path(td))

        total_cost = sum(r.cost for r in results)
        expected_mean = total_cost / len(results)
        assert abs(summary.mean_cost - expected_mean) < 1e-9
        assert abs(summary.total_cost - total_cost) < 1e-9

    @given(results=st.lists(instance_result_st(), min_size=1, max_size=20))
    @settings(max_examples=100)
    def test_mean_tokens_equals_total_over_count(self, results: list[InstanceResult]) -> None:
        """mean_tokens = total_tokens / instance count."""
        for r in results:
            r.model = "test-model"
            r.mode = "native"

        with tempfile.TemporaryDirectory() as td:
            summary = save_run_summary(results, "test-run", {}, base_dir=Path(td))

        total_tokens = sum(r.tokens_input + r.tokens_output for r in results)
        expected_mean = total_tokens / len(results)
        assert abs(summary.total_tokens - total_tokens) < 1e-9
        assert abs(summary.mean_tokens - expected_mean) < 1e-9

    @given(results=st.lists(instance_result_st(), min_size=1, max_size=20))
    @settings(max_examples=100)
    def test_mean_duration_equals_total_over_count(self, results: list[InstanceResult]) -> None:
        """mean_duration = total_duration / instance count."""
        for r in results:
            r.model = "test-model"
            r.mode = "native"

        with tempfile.TemporaryDirectory() as td:
            summary = save_run_summary(results, "test-run", {}, base_dir=Path(td))

        total_duration = sum(r.duration_seconds for r in results)
        expected_mean = total_duration / len(results)
        assert abs(summary.total_duration_seconds - total_duration) < 1e-9
        assert abs(summary.mean_duration_seconds - expected_mean) < 1e-9
