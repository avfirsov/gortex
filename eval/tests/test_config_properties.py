"""Property-based tests for eval.config module.

Feature: eval-framework
Uses hypothesis to verify config merge and validation properties.
"""

from __future__ import annotations

import re
from typing import Any

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

from config import merge_configs, validate_config

# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

# Keys: non-empty strings without dots (dots are used as path separators in
# validate_config, so keeping keys simple avoids confusion).
_key_st = st.text(
    alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_", "-")),
    min_size=1,
    max_size=12,
)

# Leaf values: scalars that YAML configs typically hold.
_leaf_st = st.one_of(
    st.text(min_size=0, max_size=30),
    st.integers(min_value=-1000, max_value=1000),
    st.floats(allow_nan=False, allow_infinity=False, min_value=-1e6, max_value=1e6),
    st.booleans(),
    st.none(),
)

# Shallow config dict (1 level deep) — sufficient for merge precedence tests.
_flat_dict_st = st.dictionaries(keys=_key_st, values=_leaf_st, max_size=8)

# Nested config dict (up to 2 levels) — mirrors real YAML configs.
_nested_value_st = st.one_of(
    _leaf_st,
    st.dictionaries(keys=_key_st, values=_leaf_st, max_size=5),
)
_config_dict_st = st.dictionaries(keys=_key_st, values=_nested_value_st, max_size=8)


# Required field paths used by validate_config.
_REQUIRED_FIELDS = [
    "model.model_name",
    "agent.agent_class",
    "environment.environment_class",
]


def _build_full_config() -> dict[str, Any]:
    """Return a config dict that passes validation (all required fields present)."""
    return {
        "model": {"model_name": "test-model"},
        "agent": {"agent_class": "eval.agents.TestAgent"},
        "environment": {"environment_class": "eval.environments.TestEnv"},
    }


def _set_nested(d: dict, dotted_key: str, value: Any) -> None:
    """Set a value in a nested dict using dot notation."""
    parts = dotted_key.split(".")
    current = d
    for part in parts[:-1]:
        current = current.setdefault(part, {})
    current[parts[-1]] = value


# ---------------------------------------------------------------------------
# Property 10: Config merge precedence
# Feature: eval-framework, Property 10: Config merge precedence
# **Validates: Requirements 10.1, 10.2**
# ---------------------------------------------------------------------------


class TestConfigMergePrecedence:
    """For any two config dicts, mode values override model values on shared
    keys; unique keys preserved."""

    @given(model=_config_dict_st, mode=_config_dict_st)
    @settings(max_examples=100)
    def test_mode_overrides_model_on_shared_keys(
        self, model: dict[str, Any], mode: dict[str, Any]
    ) -> None:
        """Shared top-level keys take the mode value (or deep-merged sub-dict)."""
        merged = merge_configs(model, mode)

        for key in mode:
            if key in model:
                model_val = model[key]
                mode_val = mode[key]
                merged_val = merged[key]

                if isinstance(model_val, dict) and isinstance(mode_val, dict):
                    # When both are dicts, mode sub-keys override model sub-keys.
                    for sub_key in mode_val:
                        assert merged_val[sub_key] == mode_val[sub_key]
                else:
                    # Scalar or type mismatch: mode wins entirely.
                    assert merged_val == mode_val

    @given(model=_config_dict_st, mode=_config_dict_st)
    @settings(max_examples=100)
    def test_unique_keys_preserved(
        self, model: dict[str, Any], mode: dict[str, Any]
    ) -> None:
        """Keys present in only one config appear unchanged in the merged result."""
        merged = merge_configs(model, mode)

        # Model-only keys preserved.
        for key in model:
            if key not in mode:
                assert merged[key] == model[key]

        # Mode-only keys preserved.
        for key in mode:
            if key not in model:
                assert merged[key] == mode[key]

    @given(model=_config_dict_st, mode=_config_dict_st)
    @settings(max_examples=100)
    def test_merged_contains_all_keys(
        self, model: dict[str, Any], mode: dict[str, Any]
    ) -> None:
        """The merged dict contains every key from both inputs."""
        merged = merge_configs(model, mode)
        assert set(merged.keys()) == set(model.keys()) | set(mode.keys())


# ---------------------------------------------------------------------------
# Property 11: Config validation catches missing required fields
# Feature: eval-framework, Property 11: Config validation catches missing required fields
# **Validates: Requirements 10.3**
# ---------------------------------------------------------------------------

# Strategy: pick a non-empty subset of required fields to remove.
_required_subsets_st = st.lists(
    st.sampled_from(_REQUIRED_FIELDS),
    min_size=1,
    max_size=len(_REQUIRED_FIELDS),
    unique=True,
)


class TestConfigValidationMissingFields:
    """For any merged config missing one or more of (model_name, agent_class,
    environment_class), validation fails naming the missing field(s)."""

    @given(missing_fields=_required_subsets_st)
    @settings(max_examples=100)
    def test_validation_fails_naming_missing_fields(
        self, missing_fields: list[str]
    ) -> None:
        """Removing any subset of required fields causes ValueError listing them."""
        config = _build_full_config()

        # Remove the selected required fields.
        for field in missing_fields:
            parts = field.split(".")
            section = parts[0]
            key = parts[1]
            if section in config and isinstance(config[section], dict):
                config[section].pop(key, None)

        with pytest.raises(ValueError, match="Missing required config fields") as exc_info:
            validate_config(config)

        error_msg = str(exc_info.value)
        for field in missing_fields:
            assert field in error_msg, (
                f"Expected '{field}' to be named in error but got: {error_msg}"
            )

    @given(missing_fields=_required_subsets_st)
    @settings(max_examples=100)
    def test_validation_only_reports_actually_missing_fields(
        self, missing_fields: list[str]
    ) -> None:
        """Fields that ARE present should NOT appear in the error message."""
        config = _build_full_config()
        present_fields = [f for f in _REQUIRED_FIELDS if f not in missing_fields]

        for field in missing_fields:
            parts = field.split(".")
            section = parts[0]
            key = parts[1]
            if section in config and isinstance(config[section], dict):
                config[section].pop(key, None)

        with pytest.raises(ValueError) as exc_info:
            validate_config(config)

        error_msg = str(exc_info.value)
        for field in present_fields:
            assert field not in error_msg, (
                f"Field '{field}' is present but was reported as missing: {error_msg}"
            )

    @given(st.just(_build_full_config()))
    @settings(max_examples=10)
    def test_complete_config_passes_validation(self, config: dict[str, Any]) -> None:
        """A config with all required fields should pass validation."""
        validate_config(config)  # should not raise
