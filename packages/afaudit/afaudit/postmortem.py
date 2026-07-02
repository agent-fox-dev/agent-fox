"""Postmortem writer with Protocol-based inputs.

Provides Protocols for postmortem input data, builder functions to construct
postmortem data structures, and a writer to persist them as JSON files.

Migrated from ``agentfox.engine.run`` (lines 394-541).

Requirements: 01-REQ-6.1, 01-REQ-6.2, 01-REQ-6.3, 01-REQ-6.4, 01-REQ-6.5
"""

from __future__ import annotations

import json
import logging
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Protocol, runtime_checkable

logger = logging.getLogger("afaudit.postmortem")

# ---------------------------------------------------------------------------
# Protocols
# ---------------------------------------------------------------------------


@runtime_checkable
class SessionRecordLike(Protocol):
    """Protocol defining the 12 attributes accessed on each session record.

    Any object with these attributes can be used in postmortem session history.

    Requirements: 01-REQ-6.2
    """

    node_id: str
    attempt: int
    status: str
    archetype: str
    model: str
    duration_ms: int
    cost: float
    error_message: str | None
    timestamp: str
    is_transport_error: bool
    is_budget_exhausted: bool
    is_non_retryable: bool


@runtime_checkable
class PostmortemInput(Protocol):
    """Protocol defining the 11 attributes read from an execution state.

    Any object with these attributes can be passed to ``build_postmortem()``.

    Requirements: 01-REQ-6.1
    """

    run_id: str
    run_status: str
    node_states: dict[str, str]
    total_cost: float
    total_input_tokens: int
    total_output_tokens: int
    total_sessions: int
    blocked_reasons: dict[str, str]
    session_history: list[SessionRecordLike]
    started_at: str
    updated_at: str


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SCHEMA_VERSION: int = 1

TRIGGER_STATUSES: frozenset[str] = frozenset(
    {"stalled", "block_limit", "cost_limit", "session_limit"}
)


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def should_dump(state: PostmortemInput) -> bool:
    """Return True if the run status should trigger a post-mortem.

    Requirements: 126-REQ-1.1, 126-REQ-1.2, 126-REQ-1.3
    """
    status = state.run_status
    # Normalise StrEnum values to plain strings for comparison.
    if hasattr(status, "value"):
        status = status.value
    return status in TRIGGER_STATUSES


def build_postmortem(state: PostmortemInput) -> dict[str, Any]:
    """Build a post-mortem dict from a PostmortemInput-satisfying object.

    Requirements: 126-REQ-1.E2, 126-REQ-3.1 through 126-REQ-5.E1
    """
    # 126-REQ-1.E2: Fallback run_id when empty
    run_id = state.run_id
    if not run_id:
        now = datetime.now(UTC)
        run_id = now.strftime("%Y%m%d_%H%M%S_000000")

    run_status = state.run_status
    if hasattr(run_status, "value"):
        run_status = run_status.value

    # 126-REQ-3.3: Task summary counts from node_states
    task_summary = _build_task_summary(state.node_states)

    # 126-REQ-3.4, 126-REQ-5.2: Cost summary from state aggregates
    cost_summary = {
        "total_cost_usd": state.total_cost,
        "total_input_tokens": state.total_input_tokens,
        "total_output_tokens": state.total_output_tokens,
        "total_sessions": state.total_sessions,
    }

    # 126-REQ-4.1, 126-REQ-4.2, 126-REQ-4.E1: Blocked tasks
    blocked_tasks = _build_blocked_tasks(state.node_states, state.blocked_reasons)

    # 126-REQ-5.1: Session history
    session_history = _build_session_history(state.session_history)

    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "run_status": run_status,
        "started_at": state.started_at,
        "completed_at": state.updated_at,
        "task_summary": task_summary,
        "cost_summary": cost_summary,
        "blocked_tasks": blocked_tasks,
        "session_history": session_history,
    }


def write_postmortem(postmortem: dict[str, Any], audit_dir: Path) -> Path:
    """Write post-mortem JSON to audit_dir. Returns the file path.

    Requirements: 126-REQ-2.1, 126-REQ-2.2, 126-REQ-2.3
    """
    # 126-REQ-2.3: Create audit directory if missing
    audit_dir.mkdir(parents=True, exist_ok=True)

    run_id = postmortem.get("run_id", "unknown")
    filename = f"postmortem_{run_id}.json"
    path = audit_dir / filename

    # 126-REQ-2.2: Write valid JSON
    path.write_text(json.dumps(postmortem, indent=2))
    return path


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _build_task_summary(node_states: dict[str, str]) -> dict[str, int]:
    """Build task_summary dict from node_states.

    Requirements: 126-REQ-3.3
    """
    status_counts = Counter(node_states.values())
    return {
        "total": len(node_states),
        "completed": status_counts.get("completed", 0),
        "pending": status_counts.get("pending", 0),
        "blocked": status_counts.get("blocked", 0),
        "failed": status_counts.get("failed", 0),
        "in_progress": status_counts.get("in_progress", 0),
    }


def _build_blocked_tasks(
    node_states: dict[str, str],
    blocked_reasons: dict[str, str],
) -> list[dict[str, str]]:
    """Build sorted blocked_tasks list.

    Requirements: 126-REQ-4.1, 126-REQ-4.2, 126-REQ-4.E1
    """
    blocked = []
    for node_id, status in node_states.items():
        if status == "blocked":
            # 126-REQ-4.E1: default to "unknown" if reason missing
            reason = blocked_reasons.get(node_id, "unknown")
            blocked.append({"node_id": node_id, "reason": reason})

    # 126-REQ-4.2: sorted by node_id ascending
    blocked.sort(key=lambda entry: entry["node_id"])
    return blocked


def _build_session_history(
    session_records: list[Any],
) -> list[dict[str, Any]]:
    """Serialize SessionRecordLike objects into session_history dicts.

    Requirements: 126-REQ-5.1
    """
    history = []
    for record in session_records:
        history.append(
            {
                "node_id": record.node_id,
                "attempt": record.attempt,
                "status": record.status,
                "archetype": record.archetype,
                "model": record.model,
                "duration_ms": record.duration_ms,
                "cost": record.cost,
                "error_message": record.error_message,
                "timestamp": record.timestamp,
                "is_transport_error": record.is_transport_error,
                "is_budget_exhausted": record.is_budget_exhausted,
                "is_non_retryable": record.is_non_retryable,
            }
        )
    return history
