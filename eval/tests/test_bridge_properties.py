"""Property-based tests for tool bridge output format.

Feature: eval-framework, Property 15: Tool bridge output format
**Validates: Requirements 8.4, 8.5**

For any valid Gortex tool JSON response, formatted output is plain text
(not raw JSON) and contains next-step hints guiding the agent toward
effective tool chaining.

Since bridge scripts require a running eval-server for full execution,
we test the formatting properties by:
1. Verifying scripts contain "Next steps" hints in their output
2. Piping mock JSON through the jq formatting logic extracted from the scripts
3. Using hypothesis to generate various JSON response shapes
"""

from __future__ import annotations

import json
import subprocess
import shutil
from pathlib import Path

import pytest
from hypothesis import given, settings, assume
from hypothesis import strategies as st

BRIDGE_DIR = Path(__file__).resolve().parent.parent / "bridge"

BRIDGE_SCRIPTS = sorted(
    p for p in BRIDGE_DIR.iterdir()
    if p.is_file() and not p.name.endswith(".py") and not p.name.startswith("__")
)

# User-facing scripts (gortex-augment is an internal helper without next-step hints)
USER_FACING_SCRIPTS = [s for s in BRIDGE_SCRIPTS if s.name != "gortex-augment"]

# The jq filter used by bridge scripts to format MCP responses.
# Must check `type` before accessing keys to avoid "Cannot index array" errors.
JQ_FORMAT_FILTER = r'''
if type == "array" then
    .[] | if .id then "\(.id)  \(.kind // "")  \(.file // "")" else tostring end
elif type == "object" and .content then
    .content[] | select(.type == "text") | .text
elif type == "object" and .error then
    "Error: \(.error)"
else
    tostring
end
'''

HAS_JQ = shutil.which("jq") is not None


# ---------------------------------------------------------------------------
# Strategies for generating Gortex-like JSON responses
# ---------------------------------------------------------------------------

_safe_text = st.text(
    alphabet=st.characters(
        whitelist_categories=("L", "N", "P", "Z"),
        blacklist_characters=("\x00", "{", "["),
    ),
    min_size=1,
    max_size=100,
).filter(lambda s: s.strip())

# MCP-style content response: {"content": [{"type": "text", "text": "..."}]}
_mcp_content_st = st.builds(
    lambda texts: {"content": [{"type": "text", "text": t} for t in texts]},
    texts=st.lists(_safe_text, min_size=1, max_size=3),
)

# Array of symbol-like objects
_symbol_obj_st = st.fixed_dictionaries({
    "id": _safe_text,
    "kind": st.sampled_from(["function", "method", "type", "interface", "variable"]),
    "file": _safe_text,
})

_symbol_array_st = st.lists(_symbol_obj_st, min_size=1, max_size=5)

# Error response
_error_response_st = st.builds(
    lambda msg: {"error": msg},
    msg=_safe_text,
)

# Combined strategy for any valid Gortex response shape
_gortex_response_st = st.one_of(
    _mcp_content_st,
    _symbol_array_st,
    _error_response_st,
)


def _run_jq(json_input: str, jq_filter: str) -> subprocess.CompletedProcess:
    """Run jq with the given filter on the input JSON string."""
    return subprocess.run(
        ["jq", "-r", jq_filter],
        input=json_input,
        capture_output=True,
        text=True,
        timeout=10,
    )


# ---------------------------------------------------------------------------
# Property 15: Tool bridge output format
# ---------------------------------------------------------------------------


class TestToolBridgeOutputFormat:
    """For any valid Gortex tool JSON response, formatted output is plain text
    (not raw JSON) and contains next-step hints."""

    def test_all_scripts_contain_next_steps_section(self) -> None:
        """Every user-facing bridge script must include a 'Next steps' section."""
        for script in USER_FACING_SCRIPTS:
            content = script.read_text()
            assert "Next steps" in content, (
                f"{script.name} does not contain 'Next steps' hints"
            )

    def test_all_scripts_contain_gortex_tool_hints(self) -> None:
        """Next-step hints should reference other gortex-* tools for chaining."""
        for script in USER_FACING_SCRIPTS:
            content = script.read_text()
            # Each script should suggest at least one other gortex-* tool
            hint_tools = [
                "gortex-search", "gortex-context", "gortex-impact",
                "gortex-overview", "gortex-usages", "gortex-augment",
            ]
            other_tools = [t for t in hint_tools if t != script.name]
            has_hint = any(tool in content for tool in other_tools)
            assert has_hint, (
                f"{script.name} does not reference any other gortex-* tools in hints"
            )

    @pytest.mark.skipif(not HAS_JQ, reason="jq not installed")
    @given(response=_mcp_content_st)
    @settings(max_examples=100, deadline=None)
    def test_mcp_content_formatted_as_plain_text(self, response: dict) -> None:
        """MCP content responses are formatted as plain text, not raw JSON."""
        json_str = json.dumps(response)
        result = _run_jq(json_str, JQ_FORMAT_FILTER)

        assert result.returncode == 0, f"jq failed: {result.stderr}"
        output = result.stdout.strip()
        assert len(output) > 0, "Formatted output should not be empty"

        # Output should NOT look like raw JSON (no leading { or [)
        assert not output.startswith("{"), (
            f"Output looks like raw JSON object: {output[:80]}"
        )
        assert not output.startswith("["), (
            f"Output looks like raw JSON array: {output[:80]}"
        )

    @pytest.mark.skipif(not HAS_JQ, reason="jq not installed")
    @given(symbols=_symbol_array_st)
    @settings(max_examples=100, deadline=None)
    def test_symbol_array_formatted_as_plain_text(self, symbols: list) -> None:
        """Symbol array responses are formatted as readable lines, not JSON."""
        json_str = json.dumps(symbols)
        result = _run_jq(json_str, JQ_FORMAT_FILTER)

        assert result.returncode == 0, f"jq failed: {result.stderr}"
        output = result.stdout.strip()
        assert len(output) > 0, "Formatted output should not be empty"

        # Each symbol should produce a line with its id
        lines = output.split("\n")
        assert len(lines) >= 1, "Expected at least one output line per symbol"

        # Output should not be raw JSON
        for line in lines:
            stripped = line.strip()
            if stripped:
                assert not stripped.startswith("{"), (
                    f"Line looks like raw JSON: {stripped[:80]}"
                )

    @pytest.mark.skipif(not HAS_JQ, reason="jq not installed")
    @given(response=_error_response_st)
    @settings(max_examples=100, deadline=None)
    def test_error_response_formatted_as_plain_text(self, response: dict) -> None:
        """Error responses are formatted as 'Error: ...' text, not raw JSON."""
        json_str = json.dumps(response)
        result = _run_jq(json_str, JQ_FORMAT_FILTER)

        assert result.returncode == 0, f"jq failed: {result.stderr}"
        output = result.stdout.strip()
        assert output.startswith("Error:"), (
            f"Error response should start with 'Error:' but got: {output[:80]}"
        )

    @pytest.mark.skipif(not HAS_JQ, reason="jq not installed")
    @given(response=_gortex_response_st)
    @settings(max_examples=100, deadline=None)
    def test_any_response_produces_non_empty_output(self, response) -> None:
        """Any valid Gortex response shape produces non-empty formatted output."""
        json_str = json.dumps(response)
        result = _run_jq(json_str, JQ_FORMAT_FILTER)

        assert result.returncode == 0, f"jq failed: {result.stderr}"
        output = result.stdout.strip()
        assert len(output) > 0, (
            f"Expected non-empty output for response: {json_str[:120]}"
        )
