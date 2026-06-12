"""Bash syntax validation tests for tool bridge scripts.

Feature: eval-framework
Verifies all bridge scripts in eval/bridge/ pass `bash -n` (syntax check).
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

# All bridge scripts in eval/bridge/ (no .py files, no __pycache__)
BRIDGE_DIR = Path(__file__).resolve().parent.parent / "bridge"

BRIDGE_SCRIPTS = sorted(
    p for p in BRIDGE_DIR.iterdir()
    if p.is_file() and not p.name.endswith(".py") and not p.name.startswith("__")
)


@pytest.fixture(params=BRIDGE_SCRIPTS, ids=lambda p: p.name)
def bridge_script(request: pytest.FixtureRequest) -> Path:
    return request.param


def test_bridge_scripts_discovered() -> None:
    """Sanity check: we found at least the 6 expected bridge scripts."""
    assert len(BRIDGE_SCRIPTS) >= 6, (
        f"Expected at least 6 bridge scripts, found {len(BRIDGE_SCRIPTS)}: "
        f"{[p.name for p in BRIDGE_SCRIPTS]}"
    )


def test_bash_syntax_valid(bridge_script: Path) -> None:
    """Each bridge script must pass bash -n (syntax check)."""
    result = subprocess.run(
        ["bash", "-n", str(bridge_script)],
        capture_output=True,
        text=True,
        timeout=10,
    )
    assert result.returncode == 0, (
        f"bash -n failed for {bridge_script.name}:\n"
        f"stderr: {result.stderr}\n"
        f"stdout: {result.stdout}"
    )
