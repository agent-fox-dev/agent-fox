"""JSONL-aware progress display for agent-mode CLI output.

Emits structured JSONL progress events (task_started, task_completed,
task_failed) to stderr when ``json_mode`` is True.  In text mode the
class is a silent no-op — the Rich-based ``agentfox.ui.progress``
handles human-readable display.

Requirements: 04-REQ-3.1, 04-REQ-3.2, 04-REQ-3.3, 04-REQ-3.4,
              04-REQ-3.E2
"""

from __future__ import annotations

import logging
import sys
import time
from datetime import UTC, datetime
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from agentfox.io.output import OutputManager

logger = logging.getLogger(__name__)


class ProgressDisplay:
    """JSONL progress display for agent-mode CLI output.

    In ``json_mode`` each task lifecycle transition is emitted as a
    single JSONL line on stderr via ``OutputManager.emit_progress()``.
    In text mode all methods are silent no-ops.
    """

    def __init__(
        self,
        output_manager: OutputManager,
        *,
        json_mode: bool = False,
    ) -> None:
        self._om = output_manager
        self._json_mode = json_mode
        self._start_times: dict[str | None, float] = {}

    def task_started(self, node_id: str | None) -> None:
        """Record start time and emit ``task_started`` JSONL event."""
        self._start_times[node_id] = time.monotonic()
        if not self._json_mode:
            return
        if node_id is None or node_id == "":
            self._warn_null_node_id()
        event = {
            "event": "task_started",
            "node_id": node_id if node_id else None,
            "timestamp": _iso_timestamp(),
        }
        self._om.emit_progress(event)

    def task_completed(self, node_id: str | None) -> None:
        """Emit ``task_completed`` JSONL event with duration."""
        duration_s = self._compute_duration(node_id)
        if not self._json_mode:
            return
        if node_id is None or node_id == "":
            self._warn_null_node_id()
        event = {
            "event": "task_completed",
            "node_id": node_id if node_id else None,
            "duration_s": duration_s,
            "timestamp": _iso_timestamp(),
        }
        self._om.emit_progress(event)

    def task_failed(self, node_id: str | None, *, error: str = "") -> None:
        """Emit ``task_failed`` JSONL event with error message."""
        if not self._json_mode:
            return
        if node_id is None or node_id == "":
            self._warn_null_node_id()
        event = {
            "event": "task_failed",
            "node_id": node_id if node_id else None,
            "error": error,
            "timestamp": _iso_timestamp(),
        }
        self._om.emit_progress(event)

    def _compute_duration(self, node_id: str | None) -> float:
        """Pop start time for *node_id* and return elapsed seconds."""
        start = self._start_times.pop(node_id, None)
        if start is None:
            return 0.0
        return time.monotonic() - start

    @staticmethod
    def _warn_null_node_id() -> None:
        """Write a warning to stderr when node_id is missing."""
        try:
            sys.stderr.write(
                "WARNING: node_id is missing for progress event\n",
            )
            sys.stderr.flush()
        except (BrokenPipeError, OSError):
            pass


def _iso_timestamp() -> str:
    """Return the current UTC time as an ISO-8601 string."""
    return datetime.now(UTC).isoformat()
