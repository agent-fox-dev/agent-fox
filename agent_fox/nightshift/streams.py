"""Work stream protocol and concrete implementations for the daemon framework.

Defines the ``WorkStream`` protocol and provides the fix-pipeline stream
via ``EngineWorkStream``, plus a ``build_streams()`` factory that applies
CLI flags, config, and platform degradation rules.

Requirements: 85-REQ-1.1, 85-REQ-6.1, 85-REQ-6.2, 85-REQ-6.3,
              85-REQ-7.1, 85-REQ-7.E1, 125-REQ-3.1, 125-REQ-3.2,
              125-REQ-3.3, 125-REQ-3.4, 125-REQ-3.E1
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Protocol, runtime_checkable

if TYPE_CHECKING:
    from agent_fox.nightshift.daemon import SharedBudget


# ---------------------------------------------------------------------------
# WorkStream protocol
# ---------------------------------------------------------------------------


@runtime_checkable
class WorkStream(Protocol):
    """Protocol for daemon work streams.

    Each work stream has a name, polling interval, enabled flag, and
    async methods for running one cycle and shutting down gracefully.

    Requirements: 85-REQ-1.1
    """

    @property
    def name(self) -> str:
        """Unique name identifying this work stream."""
        ...

    @property
    def interval(self) -> int:
        """Seconds between run_once() invocations."""
        ...

    @property
    def enabled(self) -> bool:
        """Whether this stream should be scheduled for execution."""
        ...

    async def run_once(self) -> None:
        """Execute one cycle of this work stream's logic."""
        ...

    async def shutdown(self) -> None:
        """Clean up resources before daemon exit."""
        ...


logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# EngineWorkStream — unified wrapper for engine-method-based streams
# ---------------------------------------------------------------------------


class EngineWorkStream:
    """Wraps a NightShiftEngine method as a work stream.

    Requirements: 85-REQ-1.1, 85-REQ-6.3
    """

    def __init__(
        self,
        stream_name: str,
        engine: object,
        method_name: str,
        budget: SharedBudget,
        *,
        enabled: bool = True,
        interval: int = 900,
    ) -> None:
        self._name = stream_name
        self._engine = engine
        self._method_name = method_name
        self._budget = budget
        self._enabled = enabled
        self._interval = interval

    @property
    def name(self) -> str:
        return self._name

    @property
    def interval(self) -> int:
        return self._interval

    @property
    def enabled(self) -> bool:
        return self._enabled

    @enabled.setter
    def enabled(self, value: bool) -> None:
        self._enabled = value

    async def run_once(self) -> None:
        """Run one cycle via the configured engine method and report cost delta."""
        cost_before = getattr(getattr(self._engine, "state", None), "total_cost", 0.0)
        method = getattr(self._engine, self._method_name)
        await method()
        cost_after = getattr(getattr(self._engine, "state", None), "total_cost", 0.0)
        delta = cost_after - cost_before
        if delta > 0:
            self._budget.add_cost(delta)

    async def shutdown(self) -> None:
        """No resources to clean up."""


# ---------------------------------------------------------------------------
# build_streams() factory
# ---------------------------------------------------------------------------


def build_streams(
    config: object,
    *,
    engine: object | None = None,
    budget: SharedBudget | None = None,
) -> list[WorkStream]:
    """Build work streams with proper enabled/disabled state.

    Returns a list containing exactly one ``WorkStream`` — the fix-pipeline
    stream.  The stream is disabled when the platform type is ``"none"``.

    Requirements: 85-REQ-6.1, 85-REQ-7.1, 125-REQ-3.3, 125-REQ-3.4,
                  125-REQ-3.E1
    """
    from agent_fox.nightshift.daemon import SharedBudget as _SharedBudget

    if budget is None:
        budget = _SharedBudget(max_cost=None)

    ns = getattr(config, "night_shift", None)

    # Platform degradation (85-REQ-7.1): platform.type="none" disables
    # the fix-pipeline stream.
    platform_type = getattr(getattr(config, "platform", None), "type", "github")
    fixes_enabled = True
    if platform_type == "none":
        logger.warning("Platform type is 'none'; disabling fix-pipeline stream")
        fixes_enabled = False

    # Get interval from config
    issue_check_interval = getattr(ns, "issue_check_interval", 900)

    # Build streams — only fix-pipeline (125-REQ-3.3)
    streams: list[WorkStream] = []

    streams.append(
        EngineWorkStream(
            stream_name="fix-pipeline",
            engine=engine,
            method_name="_drain_issues",
            budget=budget,
            enabled=fixes_enabled,
            interval=issue_check_interval,
        )
    )

    return streams
