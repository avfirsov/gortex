"""Gortex-Enhanced Agent for SWE-bench Evaluation.

Extends mini-swe-agent's DefaultAgent with Gortex code intelligence:

1. **baseline** — bash only (grep, find, cat, sed). Control group.
2. **native** — bash + Gortex tool bridge scripts via eval-server.
3. **native_augment** — native + automatic grep output augmentation
   with ``[Gortex]`` graph annotations (recommended).

The agent is designed to work standalone even when mini-swe-agent is
not installed — a lightweight base class is used as a fallback.

Heavy lifting lives elsewhere:
- Prompt selection: ``eval.prompts`` (system + instance templates per mode)
- Augmentation pipeline: ``eval.augmentation`` (task 12)
- Metrics persistence: ``eval.results`` (task 15)
"""

from __future__ import annotations

import logging
import re
import time
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, List, Optional, Tuple

logger = logging.getLogger("gortex_agent")

# ---------------------------------------------------------------------------
# Try to import mini-swe-agent; fall back to a lightweight stub.
# ---------------------------------------------------------------------------
try:
    from minisweagent.agents.default import DefaultAgent as _DefaultAgent
except ImportError:  # pragma: no cover
    class _DefaultAgent:  # type: ignore[no-redef]
        """Minimal stand-in when mini-swe-agent is not installed."""

        def __init__(self, **kwargs: Any) -> None:
            self._kwargs = kwargs
            self._step_count = 0

        def run(self, task: str) -> dict:
            raise NotImplementedError(
                "mini-swe-agent is not installed — "
                "install it or override run() in a subclass"
            )


# ---------------------------------------------------------------------------
# Gortex evaluation modes
# ---------------------------------------------------------------------------

class GortexMode(str, Enum):
    """Evaluation modes for Gortex integration."""

    BASELINE = "baseline"
    NATIVE = "native"
    NATIVE_AUGMENT = "native_augment"


# ---------------------------------------------------------------------------
# Tool bridge binaries → metric keys
# ---------------------------------------------------------------------------

# Maps a short metric key to the bash binary name installed in the container.
TOOL_BINARIES: Dict[str, str] = {
    "search_symbols": "gortex-search",
    "smart_context": "gortex-context",
    "explain_change_impact": "gortex-impact",
    "graph_stats": "gortex-overview",
    "find_usages": "gortex-usages",
    "augment": "gortex-augment",
}

TOOL_METRIC_KEYS: List[str] = list(TOOL_BINARIES.keys())


# ---------------------------------------------------------------------------
# Metrics
# ---------------------------------------------------------------------------

@dataclass
class GortexMetrics:
    """Tracks Gortex-specific metrics during an evaluation run."""

    tool_calls: Dict[str, int] = field(default_factory=lambda: {k: 0 for k in TOOL_METRIC_KEYS})
    augmentation_calls: int = 0
    augmentation_hits: int = 0
    augmentation_errors: int = 0
    augmentation_time_seconds: float = 0.0

    @property
    def total_tool_calls(self) -> int:
        return sum(self.tool_calls.values())

    def to_dict(self) -> Dict[str, Any]:
        return {
            "tool_calls": dict(self.tool_calls),
            "total_tool_calls": self.total_tool_calls,
            "augmentation_calls": self.augmentation_calls,
            "augmentation_hits": self.augmentation_hits,
            "augmentation_errors": self.augmentation_errors,
            "augmentation_time_seconds": round(self.augmentation_time_seconds, 2),
        }


# ---------------------------------------------------------------------------
# Pattern extraction (shared with augmentation pipeline)
# ---------------------------------------------------------------------------

_GREP_PATTERNS = [
    # Quoted pattern: grep -rn "pattern" .
    re.compile(r'(?:grep|rg|ag)\s+(?:-[a-zA-Z]*\s+)*["\']([^"\']+)["\']'),
    # Unquoted pattern: grep -rn pattern .
    re.compile(r'(?:grep|rg|ag)\s+(?:-[a-zA-Z]*\s+)*(\S+)'),
]


def extract_search_pattern(command: str) -> Optional[str]:
    """Extract the search pattern from a grep/rg/ag command string.

    Returns ``None`` when no usable pattern can be identified.
    """
    for pat in _GREP_PATTERNS:
        match = pat.search(command)
        if match:
            result = match.group(1)
            # Skip file paths and flags that were mis-captured
            if result.startswith(("/", ".", "-")):
                continue
            return result
    return None


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------

class GortexAgent(_DefaultAgent):
    """LLM agent with optional Gortex code-intelligence augmentation.

    In **baseline** mode the agent behaves like a plain ``DefaultAgent``
    with standard bash tools only.

    In **native** mode the Gortex tool bridge scripts are available as
    additional bash commands (``gortex-search``, ``gortex-context``, …).

    In **native_augment** mode the agent additionally intercepts grep/rg
    output and enriches it with ``[Gortex]`` graph annotations.

    Parameters
    ----------
    config:
        Merged run configuration dict (model + mode YAML).  The agent
        reads ``config["agent"]`` for its own settings.
    """

    def __init__(self, config: Dict[str, Any], model: Any = None, env: Any = None, **kwargs: Any) -> None:
        if model is not None and env is not None:
            super().__init__(model=model, env=env, **kwargs)
        else:
            # Standalone mode (no mini-swe-agent)
            self._kwargs = kwargs
            self._step_count = 0

        agent_cfg = config.get("agent", {})

        # --- mode -----------------------------------------------------------
        raw_mode = agent_cfg.get("gortex_mode", "baseline")
        if isinstance(raw_mode, GortexMode):
            self.mode = raw_mode
        else:
            self.mode = GortexMode(raw_mode)

        # --- limits ----------------------------------------------------------
        self.cost_limit: float = float(agent_cfg.get("cost_limit", 3.0))
        self.step_limit: int = int(agent_cfg.get("step_limit", 30))

        # --- augmentation settings -------------------------------------------
        self.augment_timeout: float = float(agent_cfg.get("augment_timeout", 5.0))
        self.augment_min_pattern_length: int = int(
            agent_cfg.get("augment_min_pattern_length", 3)
        )
        self.track_gortex_usage: bool = bool(
            agent_cfg.get("track_gortex_usage", True)
        )

        # --- prompt templates ------------------------------------------------
        self._system_template = None
        self._instance_template = None
        self._load_prompt_templates()

        # --- metrics ---------------------------------------------------------
        self.metrics = GortexMetrics()

        # --- internal bookkeeping --------------------------------------------
        self._step_count = 0
        self._total_cost: float = 0.0

        logger.info(
            "GortexAgent initialised: mode=%s, step_limit=%d, cost_limit=%.2f",
            self.mode.value,
            self.step_limit,
            self.cost_limit,
        )

    # ------------------------------------------------------------------
    # Prompt loading
    # ------------------------------------------------------------------

    def _load_prompt_templates(self) -> None:
        """Load mode-specific Jinja2 prompt templates via ``eval.prompts``."""
        try:
            from prompts import load_templates

            self._system_template, self._instance_template = load_templates(
                self.mode.value
            )
            logger.debug("Loaded prompt templates for mode=%s", self.mode.value)
        except Exception as exc:
            logger.warning("Could not load prompt templates: %s", exc)

    def render_system_prompt(self) -> str:
        """Render the system prompt for the current mode."""
        if self._system_template is None:
            return ""
        return self._system_template.render()

    def render_instance_prompt(self, task: str) -> str:
        """Render the instance prompt with the given *task* description."""
        if self._instance_template is None:
            return task
        try:
            from prompts import render_instance_prompt

            return render_instance_prompt(self._instance_template, task)
        except Exception:
            return task

    # ------------------------------------------------------------------
    # Execution helpers
    # ------------------------------------------------------------------

    def should_continue(self) -> bool:
        """Return ``False`` when a cost or step limit has been reached."""
        if self._step_count >= self.step_limit:
            logger.info("Step limit reached (%d)", self.step_limit)
            return False
        if self._total_cost >= self.cost_limit:
            logger.info("Cost limit reached ($%.2f)", self.cost_limit)
            return False
        return True

    def record_step(self, cost: float = 0.0) -> None:
        """Record one agent step and its associated API cost."""
        self._step_count += 1
        self._total_cost += cost

    # ------------------------------------------------------------------
    # Tool-usage tracking
    # ------------------------------------------------------------------

    def track_tool_usage(self, command: str) -> None:
        """Inspect *command* and increment the matching tool-call counter."""
        if not self.track_gortex_usage:
            return
        for key, binary in TOOL_BINARIES.items():
            if binary in command:
                self.metrics.tool_calls[key] = self.metrics.tool_calls.get(key, 0) + 1
                break

    # ------------------------------------------------------------------
    # Grep augmentation (native_augment mode)
    # ------------------------------------------------------------------

    def maybe_augment(
        self,
        command: str,
        output: str,
        *,
        execute_fn: Any = None,
    ) -> str:
        """Conditionally augment grep/rg output with Gortex annotations.

        In ``native_augment`` mode, if *command* is a grep/rg invocation
        with a pattern of sufficient length, the augmentation endpoint is
        called and ``[Gortex]`` annotations are appended.

        Parameters
        ----------
        command:
            The bash command that was executed.
        output:
            The raw stdout captured from the command.
        execute_fn:
            A callable ``(cmd: str, timeout: float) -> str`` that runs a
            command inside the container and returns its stdout.  When
            ``None``, augmentation is skipped.

        Returns
        -------
        str
            The (possibly enriched) output.
        """
        if self.mode != GortexMode.NATIVE_AUGMENT:
            return output

        pattern = extract_search_pattern(command)
        if not pattern or len(pattern) < self.augment_min_pattern_length:
            return output

        if execute_fn is None:
            return output

        start = time.time()
        try:
            augment_result = execute_fn(
                f'gortex-augment "{pattern}" 2>&1 || true',
                self.augment_timeout,
            )
            elapsed = time.time() - start
            self.metrics.augmentation_calls += 1
            self.metrics.augmentation_time_seconds += elapsed

            augment_text = (augment_result or "").strip()
            if augment_text and "[Gortex]" in augment_text:
                self.metrics.augmentation_hits += 1
                return f"{output}\n\n{augment_text}"
        except Exception as exc:
            logger.debug("Augmentation failed for pattern '%s': %s", pattern, exc)
            self.metrics.augmentation_errors += 1

        return output

    # ------------------------------------------------------------------
    # Serialization
    # ------------------------------------------------------------------

    def get_metrics(self) -> Dict[str, Any]:
        """Return a dict of Gortex-specific metrics for result storage."""
        return {
            "mode": self.mode.value,
            "step_count": self._step_count,
            "total_cost": round(self._total_cost, 4),
            "gortex_metrics": self.metrics.to_dict(),
        }
