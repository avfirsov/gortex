"""Property-based tests for eval.augmentation module.

Feature: eval-framework
Uses hypothesis to verify augmentation triggering and timeout properties.
"""

from __future__ import annotations

import json
from typing import Any, Dict
from unittest.mock import MagicMock, patch

import pytest
from hypothesis import given, settings, assume
from hypothesis import strategies as st

from augmentation import augment_grep_output


# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

# Printable text that could appear in grep output lines.
_grep_line_st = st.text(
    alphabet=st.characters(whitelist_categories=("L", "N", "P", "Z"), whitelist_characters=(":", "/", ".", "_", "-")),
    min_size=1,
    max_size=80,
)

# Multi-line grep output.
_grep_output_st = st.lists(_grep_line_st, min_size=1, max_size=10).map("\n".join)

# Search patterns: printable, no quotes, no leading special chars.
_pattern_st = st.text(
    alphabet=st.characters(whitelist_categories=("L", "N"), whitelist_characters=("_",)),
    min_size=1,
    max_size=30,
).filter(lambda s: not s.startswith(("/", ".", "-")))

# Minimum pattern length config values.
_min_len_st = st.integers(min_value=1, max_value=20)


def _make_grep_command(pattern: str) -> str:
    """Build a grep command string with the given pattern."""
    return f'grep -rn "{pattern}" .'


# ---------------------------------------------------------------------------
# Property 13: Augmentation triggering rules
# Feature: eval-framework, Property 13: Augmentation triggering rules
# **Validates: Requirements 9.1, 9.4**
# ---------------------------------------------------------------------------


class TestAugmentationTriggeringRules:
    """For any grep/rg command in native_augment mode, if the extracted search
    pattern has length >= the configured minimum, augmentation SHALL be
    attempted. If the pattern length is below the minimum, augmentation SHALL
    be skipped and the original output returned."""

    @given(pattern=_pattern_st, min_len=_min_len_st, raw_output=_grep_output_st)
    @settings(max_examples=100)
    def test_short_pattern_skips_augmentation(
        self, pattern: str, min_len: int, raw_output: str
    ) -> None:
        """Pattern shorter than minimum → augmentation skipped, original output returned."""
        assume(len(pattern) < min_len)

        command = _make_grep_command(pattern)
        config: Dict[str, Any] = {"augment_min_pattern_length": min_len}

        # urlopen should never be called when pattern is too short.
        with patch("augmentation.urllib.request.urlopen") as mock_urlopen:
            result = augment_grep_output(
                raw_output, command, "http://127.0.0.1:4747", config
            )

        mock_urlopen.assert_not_called()
        assert result == raw_output

    @given(pattern=_pattern_st, min_len=_min_len_st, raw_output=_grep_output_st)
    @settings(max_examples=100)
    def test_long_pattern_attempts_augmentation(
        self, pattern: str, min_len: int, raw_output: str
    ) -> None:
        """Pattern >= minimum length → augmentation attempted (HTTP call made)."""
        assume(len(pattern) >= min_len)

        command = _make_grep_command(pattern)
        config: Dict[str, Any] = {"augment_min_pattern_length": min_len}

        # Mock urlopen to return a valid augmentation response.
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({
            "callers": [{"name": "Caller", "location": "file.go:1"}],
        }).encode("utf-8")
        mock_response.__enter__ = MagicMock(return_value=mock_response)
        mock_response.__exit__ = MagicMock(return_value=False)

        with patch("augmentation.urllib.request.urlopen", return_value=mock_response) as mock_urlopen:
            result = augment_grep_output(
                raw_output, command, "http://127.0.0.1:4747", config
            )

        mock_urlopen.assert_called_once()
        # Result should contain the Gortex annotation.
        assert "[Gortex]" in result


# ---------------------------------------------------------------------------
# Property 14: Augmentation timeout preserves output
# Feature: eval-framework, Property 14: Augmentation timeout preserves output
# **Validates: Requirements 9.3**
# ---------------------------------------------------------------------------


class TestAugmentationTimeoutPreservesOutput:
    """For any grep output where augmentation times out or returns nothing,
    returned output is identical to original."""

    @given(pattern=_pattern_st, raw_output=_grep_output_st)
    @settings(max_examples=100)
    def test_timeout_returns_original(
        self, pattern: str, raw_output: str
    ) -> None:
        """When the augmentation endpoint times out, original output is returned."""
        assume(len(pattern) >= 3)

        command = _make_grep_command(pattern)
        config: Dict[str, Any] = {"augment_min_pattern_length": 1}

        with patch(
            "eval.augmentation.urllib.request.urlopen",
            side_effect=TimeoutError("timed out"),
        ):
            result = augment_grep_output(
                raw_output, command, "http://127.0.0.1:4747", config
            )

        assert result == raw_output

    @given(pattern=_pattern_st, raw_output=_grep_output_st)
    @settings(max_examples=100)
    def test_connection_error_returns_original(
        self, pattern: str, raw_output: str
    ) -> None:
        """When the augmentation endpoint is unreachable, original output is returned."""
        assume(len(pattern) >= 3)

        command = _make_grep_command(pattern)
        config: Dict[str, Any] = {"augment_min_pattern_length": 1}

        import urllib.error
        with patch(
            "eval.augmentation.urllib.request.urlopen",
            side_effect=urllib.error.URLError("connection refused"),
        ):
            result = augment_grep_output(
                raw_output, command, "http://127.0.0.1:4747", config
            )

        assert result == raw_output

    @given(pattern=_pattern_st, raw_output=_grep_output_st)
    @settings(max_examples=100)
    def test_empty_response_returns_original(
        self, pattern: str, raw_output: str
    ) -> None:
        """When augmentation returns no useful context, original output is returned."""
        assume(len(pattern) >= 3)

        command = _make_grep_command(pattern)
        config: Dict[str, Any] = {"augment_min_pattern_length": 1}

        # Return empty annotations (no callers/callees/flows).
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({}).encode("utf-8")
        mock_response.__enter__ = MagicMock(return_value=mock_response)
        mock_response.__exit__ = MagicMock(return_value=False)

        with patch(
            "eval.augmentation.urllib.request.urlopen",
            return_value=mock_response,
        ):
            result = augment_grep_output(
                raw_output, command, "http://127.0.0.1:4747", config
            )

        assert result == raw_output
