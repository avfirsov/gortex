"""Property-based tests for eval runner (run_eval module).

Feature: eval-framework
Uses hypothesis to verify runner orchestration properties.
"""

from __future__ import annotations

from typing import Any, Dict, List
from unittest.mock import patch, MagicMock

import pytest
from hypothesis import given, settings, assume
from hypothesis import strategies as st

from run_eval import parse_slice, build_matrix_configs, run_configuration


# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

# Bounded integers suitable for slice components.
_slice_int_st = st.integers(min_value=-50, max_value=50)

# Optional slice int (None means omitted).
_opt_slice_int_st = st.one_of(st.none(), _slice_int_st)

# Simple identifier-like strings for model/mode names.
_name_st = st.text(
    alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
    min_size=1,
    max_size=12,
)

# Non-empty lists of unique names.
_name_list_st = st.lists(_name_st, min_size=1, max_size=6, unique=True)


def _make_instance(instance_id: str) -> Dict[str, Any]:
    """Create a minimal fake SWE-bench instance dict."""
    return {
        "instance_id": instance_id,
        "problem_statement": f"Fix {instance_id}",
    }



# ---------------------------------------------------------------------------
# Property 3: Slice parsing correctness
# Feature: eval-framework, Property 3: Slice parsing correctness
# **Validates: Requirements 1.5**
# ---------------------------------------------------------------------------


class TestSliceParsingCorrectness:
    """For any valid slice spec, result matches Python's list[start:end] semantics."""

    @given(start=_opt_slice_int_st, end=_opt_slice_int_st)
    @settings(max_examples=100)
    def test_two_part_slice_matches_python(
        self, start: int | None, end: int | None
    ) -> None:
        """A 'start:end' spec produces the same sublist as list[start:end]."""
        # Build the spec string.
        start_str = "" if start is None else str(start)
        end_str = "" if end is None else str(end)
        spec = f"{start_str}:{end_str}"

        # Reference list large enough to exercise the slice.
        ref = list(range(100))

        parsed = parse_slice(spec)
        assert ref[parsed] == ref[start:end]

    @given(start=_opt_slice_int_st, end=_opt_slice_int_st, step=_slice_int_st)
    @settings(max_examples=100)
    def test_three_part_slice_matches_python(
        self, start: int | None, end: int | None, step: int
    ) -> None:
        """A 'start:end:step' spec produces the same sublist as list[start:end:step]."""
        assume(step != 0)  # step=0 is invalid for Python slices

        start_str = "" if start is None else str(start)
        end_str = "" if end is None else str(end)
        spec = f"{start_str}:{end_str}:{step}"

        ref = list(range(100))

        parsed = parse_slice(spec)
        assert ref[parsed] == ref[start:end:step]

    @given(end=st.integers(min_value=-50, max_value=50))
    @settings(max_examples=100)
    def test_single_value_treated_as_end(self, end: int) -> None:
        """A single integer spec like '5' is treated as slice(None, 5)."""
        spec = str(end)
        ref = list(range(100))

        parsed = parse_slice(spec)
        assert ref[parsed] == ref[:end]

    def test_empty_spec_selects_everything(self) -> None:
        """An empty string selects all elements."""
        ref = list(range(20))
        parsed = parse_slice("")
        assert ref[parsed] == ref



# ---------------------------------------------------------------------------
# Property 1: Instance execution completeness
# Feature: eval-framework, Property 1: Instance execution completeness
# **Validates: Requirements 1.1**
# ---------------------------------------------------------------------------


def _fake_process_instance(instance, config, output_dir, run_id, model_name, mode_name):
    """A mock process_instance that returns a result dict without side effects."""
    return {
        "instance_id": instance["instance_id"],
        "model": model_name,
        "mode": mode_name,
        "exit_status": "submitted",
        "submission": "fake-patch",
        "cost": 0.01,
    }


class TestInstanceExecutionCompleteness:
    """For any N instances and worker count W >= 1, runner produces exactly N
    result records."""

    @given(
        n=st.integers(min_value=1, max_value=20),
        workers=st.integers(min_value=1, max_value=4),
    )
    @settings(max_examples=100)
    def test_produces_exactly_n_results(self, n: int, workers: int) -> None:
        """run_configuration returns exactly N results for N instances."""
        instances = [_make_instance(f"test__test-{i}") for i in range(n)]

        with (
            patch("run_eval._build_config", return_value={"agent": {}}),
            patch("run_eval.generate_run_id", return_value="test_run_1"),
            patch("run_eval.process_instance", side_effect=_fake_process_instance),
            patch("pathlib.Path.mkdir"),
            patch("pathlib.Path.write_text"),
        ):
            from pathlib import Path

            results = run_configuration(
                "test-model", "baseline", instances, Path("/tmp/fake"), workers
            )

        assert len(results) == n

    @given(n=st.integers(min_value=1, max_value=15))
    @settings(max_examples=100)
    def test_each_instance_id_present(self, n: int) -> None:
        """Every instance ID appears exactly once in the results."""
        instances = [_make_instance(f"test__test-{i}") for i in range(n)]

        with (
            patch("run_eval._build_config", return_value={"agent": {}}),
            patch("run_eval.generate_run_id", return_value="test_run_1"),
            patch("run_eval.process_instance", side_effect=_fake_process_instance),
            patch("pathlib.Path.mkdir"),
            patch("pathlib.Path.write_text"),
        ):
            from pathlib import Path

            results = run_configuration(
                "test-model", "baseline", instances, Path("/tmp/fake"), 1
            )

        result_ids = [r["instance_id"] for r in results]
        expected_ids = [inst["instance_id"] for inst in instances]
        assert sorted(result_ids) == sorted(expected_ids)



# ---------------------------------------------------------------------------
# Property 2: Matrix cross-product completeness
# Feature: eval-framework, Property 2: Matrix cross-product completeness
# **Validates: Requirements 1.3**
# ---------------------------------------------------------------------------


class TestMatrixCrossProductCompleteness:
    """For M models and K modes, matrix produces exactly M x K unique
    (model, mode) configs."""

    @given(models=_name_list_st, modes=_name_list_st)
    @settings(max_examples=100)
    def test_produces_m_times_k_configs(
        self, models: List[str], modes: List[str]
    ) -> None:
        """build_matrix_configs returns exactly M * K pairs."""
        configs = build_matrix_configs(models, modes)
        assert len(configs) == len(models) * len(modes)

    @given(models=_name_list_st, modes=_name_list_st)
    @settings(max_examples=100)
    def test_all_pairs_unique(
        self, models: List[str], modes: List[str]
    ) -> None:
        """Every (model, mode) pair in the result is unique."""
        configs = build_matrix_configs(models, modes)
        assert len(set(configs)) == len(configs)

    @given(models=_name_list_st, modes=_name_list_st)
    @settings(max_examples=100)
    def test_every_model_mode_combination_present(
        self, models: List[str], modes: List[str]
    ) -> None:
        """Every possible (model, mode) combination appears in the result."""
        configs = build_matrix_configs(models, modes)
        config_set = set(configs)
        for model in models:
            for mode in modes:
                assert (model, mode) in config_set



# ---------------------------------------------------------------------------
# Property 4: Failure isolation
# Feature: eval-framework, Property 4: Failure isolation
# **Validates: Requirements 1.7**
# ---------------------------------------------------------------------------


class TestFailureIsolation:
    """For N instances where K fail, runner still produces results for all
    N-K non-failing instances plus K failure entries.

    The parallel path (workers >= 2) in run_configuration catches exceptions
    from process_instance and records them as error entries. We test with
    workers=2 to exercise this failure isolation logic.
    """

    @given(
        n=st.integers(min_value=2, max_value=15),
        data=st.data(),
    )
    @settings(max_examples=100)
    def test_failure_isolation_produces_n_results(
        self, n: int, data: st.DataObject
    ) -> None:
        """Even when K instances fail, we get exactly N total result records."""
        k = data.draw(st.integers(min_value=0, max_value=n - 1))
        instances = [_make_instance(f"test__test-{i}") for i in range(n)]
        failing_ids = {inst["instance_id"] for inst in instances[:k]}

        def _mock_process(instance, config, output_dir, run_id, model_name, mode_name):
            iid = instance["instance_id"]
            if iid in failing_ids:
                raise RuntimeError(f"Simulated failure for {iid}")
            return {
                "instance_id": iid,
                "model": model_name,
                "mode": mode_name,
                "exit_status": "submitted",
                "submission": "patch",
                "cost": 0.01,
            }

        with (
            patch("run_eval._build_config", return_value={"agent": {}}),
            patch("run_eval.generate_run_id", return_value="test_run_1"),
            patch("run_eval.process_instance", side_effect=_mock_process),
            patch("pathlib.Path.mkdir"),
            patch("pathlib.Path.write_text"),
        ):
            from pathlib import Path

            results = run_configuration(
                "test-model", "baseline", instances, Path("/tmp/fake"), workers=2
            )

        assert len(results) == n

    @given(
        n=st.integers(min_value=2, max_value=15),
        data=st.data(),
    )
    @settings(max_examples=100)
    def test_non_failing_instances_have_results(
        self, n: int, data: st.DataObject
    ) -> None:
        """Non-failing instances produce normal result records."""
        k = data.draw(st.integers(min_value=1, max_value=n - 1))
        instances = [_make_instance(f"test__test-{i}") for i in range(n)]
        failing_ids = {inst["instance_id"] for inst in instances[:k]}

        def _mock_process(instance, config, output_dir, run_id, model_name, mode_name):
            iid = instance["instance_id"]
            if iid in failing_ids:
                raise RuntimeError(f"Simulated failure for {iid}")
            return {
                "instance_id": iid,
                "model": model_name,
                "mode": mode_name,
                "exit_status": "submitted",
                "submission": "patch",
                "cost": 0.01,
            }

        with (
            patch("run_eval._build_config", return_value={"agent": {}}),
            patch("run_eval.generate_run_id", return_value="test_run_1"),
            patch("run_eval.process_instance", side_effect=_mock_process),
            patch("pathlib.Path.mkdir"),
            patch("pathlib.Path.write_text"),
        ):
            from pathlib import Path

            results = run_configuration(
                "test-model", "baseline", instances, Path("/tmp/fake"), workers=2
            )

        # Non-failing instances should have "submitted" status.
        non_failing_results = [
            r for r in results if r["instance_id"] not in failing_ids
        ]
        assert len(non_failing_results) == n - k
        for r in non_failing_results:
            assert r["exit_status"] == "submitted"

    @given(
        n=st.integers(min_value=2, max_value=15),
        data=st.data(),
    )
    @settings(max_examples=100)
    def test_failing_instances_recorded_as_errors(
        self, n: int, data: st.DataObject
    ) -> None:
        """Failing instances are recorded with error status."""
        k = data.draw(st.integers(min_value=1, max_value=n - 1))
        instances = [_make_instance(f"test__test-{i}") for i in range(n)]
        failing_ids = {inst["instance_id"] for inst in instances[:k]}

        def _mock_process(instance, config, output_dir, run_id, model_name, mode_name):
            iid = instance["instance_id"]
            if iid in failing_ids:
                raise RuntimeError(f"Simulated failure for {iid}")
            return {
                "instance_id": iid,
                "model": model_name,
                "mode": mode_name,
                "exit_status": "submitted",
                "submission": "patch",
                "cost": 0.01,
            }

        with (
            patch("run_eval._build_config", return_value={"agent": {}}),
            patch("run_eval.generate_run_id", return_value="test_run_1"),
            patch("run_eval.process_instance", side_effect=_mock_process),
            patch("pathlib.Path.mkdir"),
            patch("pathlib.Path.write_text"),
        ):
            from pathlib import Path

            results = run_configuration(
                "test-model", "baseline", instances, Path("/tmp/fake"), workers=2
            )

        # Failing instances should have "error" status.
        error_results = [
            r for r in results if r["instance_id"] in failing_ids
        ]
        assert len(error_results) == k
        for r in error_results:
            assert r["exit_status"] == "error"
