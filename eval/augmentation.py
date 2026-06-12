"""Augmentation pipeline for grep/rg output enrichment.

In ``native_augment`` mode, grep/rg output is post-processed with Gortex
graph annotations (callers, callees, execution flows) via the eval-server's
``/augment`` endpoint.

Feature: eval-framework
"""

from __future__ import annotations

import json
import logging
import urllib.error
import urllib.request
from typing import Any, Dict, Optional

from agents.gortex_agent import extract_search_pattern

logger = logging.getLogger("gortex_augmentation")

# Defaults matching native_augment.yaml
_DEFAULT_TIMEOUT: float = 5.0
_DEFAULT_MIN_PATTERN_LENGTH: int = 3


def augment_grep_output(
    raw_output: str,
    command: str,
    eval_server_url: str,
    config: Optional[Dict[str, Any]] = None,
) -> str:
    """Augment grep/rg output with Gortex graph annotations.

    Parameters
    ----------
    raw_output:
        The raw stdout captured from the grep/rg command.
    command:
        The bash command string that was executed.
    eval_server_url:
        Base URL of the eval-server (e.g. ``http://127.0.0.1:4747``).
    config:
        Optional config dict. Reads ``augment_timeout`` and
        ``augment_min_pattern_length`` from it.

    Returns
    -------
    str
        The (possibly enriched) output. Returns *raw_output* unmodified
        when augmentation is skipped, times out, or returns nothing useful.
    """
    cfg = config or {}
    timeout = float(cfg.get("augment_timeout", _DEFAULT_TIMEOUT))
    min_len = int(cfg.get("augment_min_pattern_length", _DEFAULT_MIN_PATTERN_LENGTH))

    # Extract search pattern from the command string.
    pattern = extract_search_pattern(command)
    if pattern is None or len(pattern) < min_len:
        return raw_output

    # POST to /augment endpoint.
    url = f"{eval_server_url.rstrip('/')}/augment"
    payload = json.dumps({"pattern": pattern}).encode("utf-8")

    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode("utf-8")
    except (urllib.error.URLError, OSError, TimeoutError):
        # Timeout or connection error — return original output.
        return raw_output

    # Parse response and format annotations.
    try:
        data = json.loads(body)
    except (json.JSONDecodeError, ValueError):
        return raw_output

    annotations = _format_annotations(data)
    if not annotations:
        return raw_output

    return f"{raw_output}\n{annotations}"


def _format_annotations(data: Dict[str, Any]) -> str:
    """Format augmentation response data as ``[Gortex]`` annotation lines.

    The eval-server ``/augment`` endpoint returns a dict with optional keys:
    ``callers``, ``callees``, ``flows`` — each a list of
    ``{"name": ..., "location": ...}`` dicts.

    Returns an empty string when there is nothing useful to annotate.
    """
    lines: list[str] = []

    callers = data.get("callers")
    if callers and isinstance(callers, list):
        caller_strs = [_format_ref(c) for c in callers if _format_ref(c)]
        if caller_strs:
            lines.append(f"  [Gortex] callers: {', '.join(caller_strs)}")

    callees = data.get("callees")
    if callees and isinstance(callees, list):
        callee_strs = [_format_ref(c) for c in callees if _format_ref(c)]
        if callee_strs:
            lines.append(f"  [Gortex] callees: {', '.join(callee_strs)}")

    flows = data.get("flows")
    if flows and isinstance(flows, list):
        flow_strs = [_format_ref(f) for f in flows if _format_ref(f)]
        if flow_strs:
            lines.append(f"  [Gortex] flows: {', '.join(flow_strs)}")

    return "\n".join(lines)


def _format_ref(ref: Any) -> str:
    """Format a single caller/callee/flow reference.

    Accepts either a dict with ``name`` and optional ``location`` keys,
    or a plain string.
    """
    if isinstance(ref, str):
        return ref
    if isinstance(ref, dict):
        name = ref.get("name", "")
        location = ref.get("location", "")
        if name and location:
            return f"{name} ({location})"
        return name or location or ""
    return ""
