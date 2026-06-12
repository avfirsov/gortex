"""Prompt template loader for the eval framework.

Loads Jinja2 system and instance prompt templates per evaluation mode
from the ``eval/prompts/`` directory.
"""

from __future__ import annotations

from pathlib import Path
from typing import Tuple

import jinja2

# Valid evaluation modes
VALID_MODES = ("baseline", "native", "native_augment")

# Prompts directory lives alongside this module
_PROMPTS_DIR = Path(__file__).resolve().parent / "prompts"


def _make_env() -> jinja2.Environment:
    """Create a Jinja2 environment rooted at the prompts directory."""
    return jinja2.Environment(
        loader=jinja2.FileSystemLoader(str(_PROMPTS_DIR)),
        keep_trailing_newline=True,
    )


def load_templates(mode: str) -> Tuple[jinja2.Template, jinja2.Template]:
    """Load system and instance Jinja2 templates for *mode*.

    Parameters
    ----------
    mode:
        One of ``baseline``, ``native``, or ``native_augment``.

    Returns
    -------
    tuple[jinja2.Template, jinja2.Template]
        ``(system_template, instance_template)``

    Raises
    ------
    FileNotFoundError
        If either template file does not exist for the given mode.
    """
    env = _make_env()

    system_name = f"system_{mode}.jinja"
    instance_name = f"instance_{mode}.jinja"

    system_path = _PROMPTS_DIR / system_name
    instance_path = _PROMPTS_DIR / instance_name

    if not system_path.is_file():
        raise FileNotFoundError(f"System template not found: {system_path}")
    if not instance_path.is_file():
        raise FileNotFoundError(f"Instance template not found: {instance_path}")

    system_template = env.get_template(system_name)
    instance_template = env.get_template(instance_name)

    return system_template, instance_template


def render_instance_prompt(template: jinja2.Template, task: str) -> str:
    """Render an instance prompt template with the given *task*.

    Parameters
    ----------
    template:
        A Jinja2 ``Template`` object (typically the instance template
        returned by :func:`load_templates`).
    task:
        The SWE-bench issue description to inject into the template.

    Returns
    -------
    str
        The fully rendered prompt string.
    """
    return template.render(task=task)
