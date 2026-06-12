"""Property-based tests for eval.prompts module.

Feature: eval-framework
Uses hypothesis to verify prompt template loading and rendering properties.
"""

from __future__ import annotations

from hypothesis import given, settings
from hypothesis import strategies as st

from prompts import VALID_MODES, load_templates, render_instance_prompt

# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

_mode_st = st.sampled_from(VALID_MODES)

# Non-empty text strings for task descriptions.  Keep them printable and
# reasonably sized so rendered output stays manageable.
_task_st = st.text(
    alphabet=st.characters(whitelist_categories=("L", "N", "P", "Z")),
    min_size=1,
    max_size=200,
)


# ---------------------------------------------------------------------------
# Property 5: Template loading consistency
# Feature: eval-framework, Property 5: Template loading consistency
# **Validates: Requirements 2.4, 11.1**
# ---------------------------------------------------------------------------


class TestTemplateLoadingConsistency:
    """For any valid mode name, the loader returns matching
    ``system_{mode}.jinja`` and ``instance_{mode}.jinja``."""

    @given(mode=_mode_st)
    @settings(max_examples=100)
    def test_load_returns_two_templates(self, mode: str) -> None:
        """load_templates always returns a 2-tuple for every valid mode."""
        result = load_templates(mode)
        assert isinstance(result, tuple)
        assert len(result) == 2

    @given(mode=_mode_st)
    @settings(max_examples=100)
    def test_system_template_matches_mode(self, mode: str) -> None:
        """The system template's filename matches ``system_{mode}.jinja``."""
        system_tpl, _ = load_templates(mode)
        assert system_tpl.name == f"system_{mode}.jinja"

    @given(mode=_mode_st)
    @settings(max_examples=100)
    def test_instance_template_matches_mode(self, mode: str) -> None:
        """The instance template's filename matches ``instance_{mode}.jinja``."""
        _, instance_tpl = load_templates(mode)
        assert instance_tpl.name == f"instance_{mode}.jinja"


# ---------------------------------------------------------------------------
# Property 12: Template rendering includes task
# Feature: eval-framework, Property 12: Template rendering includes task
# **Validates: Requirements 11.2**
# ---------------------------------------------------------------------------


class TestTemplateRenderingIncludesTask:
    """For any non-empty task string, rendered instance prompt contains the
    task verbatim."""

    @given(mode=_mode_st, task=_task_st)
    @settings(max_examples=100)
    def test_rendered_output_contains_task_verbatim(
        self, mode: str, task: str
    ) -> None:
        """The rendered instance prompt must contain the task string as-is."""
        _, instance_tpl = load_templates(mode)
        rendered = render_instance_prompt(instance_tpl, task)
        assert task in rendered, (
            f"Task string not found verbatim in rendered output.\n"
            f"  task:     {task!r}\n"
            f"  rendered: {rendered[:300]!r}..."
        )
