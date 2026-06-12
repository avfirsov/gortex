"""Smoke tests for the eval framework.

Feature: eval-framework
Verifies basic sanity of all major components:
- Bridge scripts pass bash -n
- All prompt templates render with sample data
- list_configs discovers YAML files
- gortex eval-server --help exits 0 (skipped if binary unavailable)
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest

import sys
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from config import list_configs
from prompts import VALID_MODES, load_templates, render_instance_prompt

# --- Paths ---
EVAL_DIR = Path(__file__).resolve().parent.parent
BRIDGE_DIR = EVAL_DIR / "bridge"
PROMPTS_DIR = EVAL_DIR / "prompts"


# --- Bridge script bash -n tests ---

BRIDGE_SCRIPTS = sorted(
    p for p in BRIDGE_DIR.iterdir()
    if p.is_file() and not p.name.endswith(".py") and not p.name.startswith("__")
) if BRIDGE_DIR.is_dir() else []


@pytest.mark.parametrize("script", BRIDGE_SCRIPTS, ids=lambda p: p.name)
def test_bridge_script_bash_syntax(script: Path) -> None:
    """Each bridge script must pass bash -n (syntax check)."""
    result = subprocess.run(
        ["bash", "-n", str(script)],
        capture_output=True,
        text=True,
        timeout=10,
    )
    assert result.returncode == 0, (
        f"bash -n failed for {script.name}: {result.stderr}"
    )


# --- Prompt template rendering tests ---

SAMPLE_TASK = (
    "Fix the bug in django/contrib/auth/models.py where "
    "AbstractUser.clean() does not normalize the email address."
)


@pytest.mark.parametrize("mode", VALID_MODES)
def test_prompt_templates_render_without_errors(mode: str) -> None:
    """All prompt templates render without errors with sample data."""
    system_tpl, instance_tpl = load_templates(mode)

    # System template should render (may have no variables)
    system_output = system_tpl.render()
    assert len(system_output) > 0, f"system_{mode}.jinja rendered empty"

    # Instance template should render with task variable
    instance_output = render_instance_prompt(instance_tpl, SAMPLE_TASK)
    assert len(instance_output) > 0, f"instance_{mode}.jinja rendered empty"
    assert SAMPLE_TASK in instance_output, (
        f"instance_{mode}.jinja did not include the task verbatim"
    )


# --- list_configs discovery tests ---

def test_list_configs_discovers_yaml_files() -> None:
    """list_configs discovers all YAML files in configs/."""
    configs = list_configs()

    assert "models" in configs
    assert "modes" in configs

    # We know at least these configs exist
    assert len(configs["models"]) >= 2, (
        f"Expected at least 2 model configs, found: {configs['models']}"
    )
    assert len(configs["modes"]) >= 3, (
        f"Expected at least 3 mode configs, found: {configs['modes']}"
    )

    # Verify known configs are present
    assert "claude-sonnet" in configs["models"]
    assert "claude-haiku" in configs["models"]
    assert "baseline" in configs["modes"]
    assert "native" in configs["modes"]
    assert "native_augment" in configs["modes"]


# --- gortex eval-server --help test ---

# Check if gortex binary is available
_gortex_binary = shutil.which("gortex") or (
    str(EVAL_DIR.parent / "gortex") if (EVAL_DIR.parent / "gortex").is_file() else None
)


@pytest.mark.skipif(
    _gortex_binary is None,
    reason="gortex binary not found in PATH or project root",
)
def test_gortex_eval_server_help_exits_zero() -> None:
    """gortex eval-server --help exits 0."""
    result = subprocess.run(
        [_gortex_binary, "eval-server", "--help"],
        capture_output=True,
        text=True,
        timeout=10,
    )
    assert result.returncode == 0, (
        f"gortex eval-server --help failed:\n"
        f"stdout: {result.stdout}\n"
        f"stderr: {result.stderr}"
    )
    assert "eval-server" in result.stdout.lower() or "eval-server" in result.stderr.lower(), (
        "Help output should mention eval-server"
    )
