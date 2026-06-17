"""Prior fix attempt context retrieval for the night-shift pipeline.

Queries the session_outcomes table for prior coder sessions on the same
issue and formats them as markdown context for the coder prompt.

Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.3,
              128-REQ-1.E1, 128-REQ-1.E2,
              128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    import duckdb

logger = logging.getLogger(__name__)

_MAX_ERROR_LENGTH = 500


@dataclass(frozen=True)
class PriorAttempt:
    """A single prior fix attempt record.

    Requirements: 128-REQ-1.3
    """

    run_id: str
    created_at: str
    status: str
    error_message: str | None
    model: str | None


def query_prior_attempts(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    current_run_id: str,
    max_results: int = 3,
) -> list[PriorAttempt]:
    """Query prior coder sessions for the given issue, grouped by run.

    Returns at most *max_results* ``PriorAttempt`` records, one per prior
    run (the last coder session in each run, by ``created_at`` descending).
    Sessions belonging to *current_run_id* are excluded so the caller never
    sees its own in-flight sessions.

    On any database error the function logs a warning and returns an empty
    list (fail-open).

    Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.E1, 128-REQ-1.E2
    """
    try:
        result = conn.execute(
            """
            WITH ranked AS (
                SELECT run_id, created_at, status, error_message, model,
                       ROW_NUMBER() OVER (
                           PARTITION BY run_id
                           ORDER BY created_at DESC
                       ) AS rn
                FROM session_outcomes
                WHERE spec_name = ?
                  AND archetype = 'coder'
                  AND run_id != ?
            )
            SELECT run_id, created_at, status, error_message, model
            FROM ranked
            WHERE rn = 1
            ORDER BY created_at DESC
            LIMIT ?
            """,
            [spec_name, current_run_id, max_results],
        ).fetchall()

        return [
            PriorAttempt(
                run_id=row[0],
                created_at=str(row[1]),
                status=row[2],
                error_message=row[3],
                model=row[4],
            )
            for row in result
        ]
    except Exception:
        logger.warning(
            "Failed to query prior attempts for %s — returning empty list",
            spec_name,
            exc_info=True,
        )
        return []


def format_prior_attempts(attempts: list[PriorAttempt]) -> str:
    """Format prior attempts as a markdown context block.

    Returns an empty string when *attempts* is empty, preserving the
    existing prompt when no prior context is available.

    Each entry includes the date (extracted from ``created_at``), the
    outcome status, the model used, and the error message (truncated to
    500 characters with a ``...`` marker when necessary).

    Requirements: 128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3
    """
    if not attempts:
        return ""

    lines: list[str] = ["## Prior Fix Attempts", ""]

    for idx, attempt in enumerate(attempts, start=1):
        # Extract date portion from created_at (handles both ISO 8601 and
        # space-separated datetime formats).
        date_str = attempt.created_at[:10]

        model_part = f", {attempt.model}" if attempt.model else ""

        error_part = ""
        if attempt.error_message:
            msg = attempt.error_message
            if len(msg) > _MAX_ERROR_LENGTH:
                msg = msg[:_MAX_ERROR_LENGTH] + "..."
            error_part = f": {msg}"

        lines.append(f"{idx}. **{date_str}** ({attempt.status}{model_part}){error_part}")

    # Trailing newline for clean markdown
    lines.append("")
    return "\n".join(lines)
