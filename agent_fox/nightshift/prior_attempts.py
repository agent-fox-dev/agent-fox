"""Prior fix attempt context retrieval for the night-shift pipeline.

Queries the session_outcomes table for prior coder sessions on the same
issue and formats them as markdown context for the coder prompt.

Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.3,
              128-REQ-1.E1, 128-REQ-1.E2,
              128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3

Stub module — implementation in task group 2.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    import duckdb


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

    Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.E1, 128-REQ-1.E2

    Stub — raises NotImplementedError.
    """
    raise NotImplementedError("query_prior_attempts not yet implemented")


def format_prior_attempts(attempts: list[PriorAttempt]) -> str:
    """Format prior attempts as a markdown context block.

    Requirements: 128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3

    Stub — raises NotImplementedError.
    """
    raise NotImplementedError("format_prior_attempts not yet implemented")
