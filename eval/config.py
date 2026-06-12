"""Config loading, merging, and validation for the eval framework.

Loads model and mode YAML configs from eval/configs/, merges them with
mode overriding model on key conflicts, and validates required fields.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

# Resolve configs directory relative to this file (eval/configs/)
_CONFIGS_DIR = Path(__file__).resolve().parent / "configs"


def load_model_config(name: str) -> dict[str, Any]:
    """Load a model config from configs/models/{name}.yaml."""
    path = _CONFIGS_DIR / "models" / f"{name}.yaml"
    if not path.is_file():
        raise FileNotFoundError(f"Model config not found: {path}")
    with open(path) as f:
        return yaml.safe_load(f) or {}


def load_mode_config(name: str) -> dict[str, Any]:
    """Load a mode config from configs/modes/{name}.yaml."""
    path = _CONFIGS_DIR / "modes" / f"{name}.yaml"
    if not path.is_file():
        raise FileNotFoundError(f"Mode config not found: {path}")
    with open(path) as f:
        return yaml.safe_load(f) or {}


def merge_configs(model_config: dict[str, Any], mode_config: dict[str, Any]) -> dict[str, Any]:
    """Deep-merge two config dicts. Mode values override model on key conflicts.

    For nested dicts, merges recursively. For scalar values, mode wins.
    """
    return _deep_merge(model_config, mode_config)


def _deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    """Recursively merge override into base. Override wins on conflicts."""
    result = dict(base)
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = _deep_merge(result[key], value)
        else:
            result[key] = value
    return result


_REQUIRED_FIELDS = [
    "model.model_name",
    "agent.agent_class",
    "environment.environment_class",
]


def validate_config(merged: dict[str, Any]) -> None:
    """Validate that all required fields are present in the merged config.

    Required fields use dot notation: 'model.model_name' means
    merged["model"]["model_name"].

    Raises ValueError listing all missing fields if any are absent.
    """
    missing = []
    for field in _REQUIRED_FIELDS:
        parts = field.split(".")
        current = merged
        found = True
        for part in parts:
            if not isinstance(current, dict) or part not in current:
                found = False
                break
            current = current[part]
        if not found:
            missing.append(field)

    if missing:
        raise ValueError(f"Missing required config fields: {', '.join(missing)}")


def list_configs() -> dict[str, list[str]]:
    """Discover all YAML config files in configs/models/ and configs/modes/.

    Returns a dict with 'models' and 'modes' keys, each containing a list
    of config names (without the .yaml extension).
    """
    result: dict[str, list[str]] = {"models": [], "modes": []}
    for category in ("models", "modes"):
        directory = _CONFIGS_DIR / category
        if directory.is_dir():
            result[category] = sorted(
                p.stem for p in directory.iterdir() if p.suffix in (".yaml", ".yml") and p.is_file()
            )
    return result
