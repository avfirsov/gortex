"""Unit tests for eval.config module."""

from __future__ import annotations

import textwrap
from pathlib import Path

import pytest
import yaml

from config import (
    list_configs,
    load_mode_config,
    load_model_config,
    merge_configs,
    validate_config,
)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def tmp_configs(tmp_path, monkeypatch):
    """Create a temporary configs directory and patch _CONFIGS_DIR."""
    models_dir = tmp_path / "models"
    modes_dir = tmp_path / "modes"
    models_dir.mkdir()
    modes_dir.mkdir()

    import config as config_mod

    monkeypatch.setattr(config_mod, "_CONFIGS_DIR", tmp_path)
    return tmp_path


def _write_yaml(path: Path, data: dict) -> None:
    with open(path, "w") as f:
        yaml.safe_dump(data, f)


# ---------------------------------------------------------------------------
# load_model_config
# ---------------------------------------------------------------------------


class TestLoadModelConfig:
    def test_loads_valid_yaml(self, tmp_configs):
        data = {"model": {"model_name": "test-model", "cost_tracking": "ignore_errors"}}
        _write_yaml(tmp_configs / "models" / "test.yaml", data)
        result = load_model_config("test")
        assert result == data

    def test_missing_config_raises(self, tmp_configs):
        with pytest.raises(FileNotFoundError, match="Model config not found"):
            load_model_config("nonexistent")

    def test_empty_yaml_returns_empty_dict(self, tmp_configs):
        (tmp_configs / "models" / "empty.yaml").write_text("")
        result = load_model_config("empty")
        assert result == {}


# ---------------------------------------------------------------------------
# load_mode_config
# ---------------------------------------------------------------------------


class TestLoadModeConfig:
    def test_loads_valid_yaml(self, tmp_configs):
        data = {
            "agent": {"agent_class": "eval.agents.gortex_agent.GortexAgent"},
            "environment": {"environment_class": "docker"},
        }
        _write_yaml(tmp_configs / "modes" / "baseline.yaml", data)
        result = load_mode_config("baseline")
        assert result == data

    def test_missing_config_raises(self, tmp_configs):
        with pytest.raises(FileNotFoundError, match="Mode config not found"):
            load_mode_config("nonexistent")


# ---------------------------------------------------------------------------
# merge_configs
# ---------------------------------------------------------------------------


class TestMergeConfigs:
    def test_mode_overrides_model_on_conflict(self):
        model = {"model": {"model_name": "old"}, "shared": "model_val"}
        mode = {"shared": "mode_val"}
        result = merge_configs(model, mode)
        assert result["shared"] == "mode_val"
        assert result["model"]["model_name"] == "old"

    def test_deep_merge_nested_dicts(self):
        model = {"agent": {"step_limit": 30, "cost_limit": 3.0}}
        mode = {"agent": {"step_limit": 50, "extra": True}}
        result = merge_configs(model, mode)
        assert result["agent"]["step_limit"] == 50
        assert result["agent"]["cost_limit"] == 3.0
        assert result["agent"]["extra"] is True

    def test_unique_keys_preserved(self):
        model = {"model": {"model_name": "test"}}
        mode = {"agent": {"agent_class": "MyAgent"}}
        result = merge_configs(model, mode)
        assert result["model"]["model_name"] == "test"
        assert result["agent"]["agent_class"] == "MyAgent"

    def test_empty_configs(self):
        assert merge_configs({}, {}) == {}
        assert merge_configs({"a": 1}, {}) == {"a": 1}
        assert merge_configs({}, {"b": 2}) == {"b": 2}

    def test_scalar_override_replaces_dict(self):
        model = {"key": {"nested": "value"}}
        mode = {"key": "scalar"}
        result = merge_configs(model, mode)
        assert result["key"] == "scalar"


# ---------------------------------------------------------------------------
# validate_config
# ---------------------------------------------------------------------------


class TestValidateConfig:
    def test_valid_config_passes(self):
        config = {
            "model": {"model_name": "test"},
            "agent": {"agent_class": "MyAgent"},
            "environment": {"environment_class": "docker"},
        }
        validate_config(config)  # should not raise

    def test_missing_single_field(self):
        config = {
            "agent": {"agent_class": "MyAgent"},
            "environment": {"environment_class": "docker"},
        }
        with pytest.raises(ValueError, match="model.model_name"):
            validate_config(config)

    def test_missing_multiple_fields(self):
        with pytest.raises(ValueError) as exc_info:
            validate_config({})
        msg = str(exc_info.value)
        assert "model.model_name" in msg
        assert "agent.agent_class" in msg
        assert "environment.environment_class" in msg

    def test_missing_nested_key(self):
        config = {
            "model": {},  # model_name missing inside model dict
            "agent": {"agent_class": "MyAgent"},
            "environment": {"environment_class": "docker"},
        }
        with pytest.raises(ValueError, match="model.model_name"):
            validate_config(config)


# ---------------------------------------------------------------------------
# list_configs
# ---------------------------------------------------------------------------


class TestListConfigs:
    def test_discovers_yaml_files(self, tmp_configs):
        _write_yaml(tmp_configs / "models" / "claude-sonnet.yaml", {"model": {}})
        _write_yaml(tmp_configs / "models" / "claude-haiku.yaml", {"model": {}})
        _write_yaml(tmp_configs / "modes" / "baseline.yaml", {"agent": {}})
        _write_yaml(tmp_configs / "modes" / "native.yaml", {"agent": {}})

        result = list_configs()
        assert "claude-haiku" in result["models"]
        assert "claude-sonnet" in result["models"]
        assert "baseline" in result["modes"]
        assert "native" in result["modes"]

    def test_excludes_non_yaml_files(self, tmp_configs):
        _write_yaml(tmp_configs / "models" / "valid.yaml", {"model": {}})
        (tmp_configs / "models" / ".gitkeep").write_text("")
        (tmp_configs / "models" / "readme.txt").write_text("not yaml")

        result = list_configs()
        assert result["models"] == ["valid"]

    def test_empty_directories(self, tmp_configs):
        result = list_configs()
        assert result == {"models": [], "modes": []}

    def test_returns_sorted_names(self, tmp_configs):
        for name in ["zebra", "alpha", "middle"]:
            _write_yaml(tmp_configs / "models" / f"{name}.yaml", {})
        result = list_configs()
        assert result["models"] == ["alpha", "middle", "zebra"]
