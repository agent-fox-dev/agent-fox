"""Audit retention enforcement (DuckDB + file).

Event model, enums, serialization helpers, and AuditJsonlSink have been
migrated to the ``afaudit`` package.  Import them from ``afaudit.events``.

This module retains only ``enforce_audit_retention`` until it is split
(file-only half -> afaudit.cleanup, DB half -> duckdb_sink) in a later
task group.
"""

from __future__ import annotations

import logging
from pathlib import Path

logger = logging.getLogger("agentfox.knowledge.audit")


def enforce_audit_retention(
    audit_dir: Path,
    conn: object,
    *,
    max_runs: int = 20,
) -> None:
    """Delete audit data for runs beyond the retention limit."""
    import duckdb as _duckdb

    if not isinstance(conn, _duckdb.DuckDBPyConnection):
        return

    # 1. Query distinct run_ids ordered by oldest timestamp
    rows = conn.execute(
        """
        SELECT run_id, MIN(timestamp) AS earliest
        FROM audit_events
        GROUP BY run_id
        ORDER BY earliest ASC
        """
    ).fetchall()

    if len(rows) <= max_runs:
        return

    # 2. Identify runs to delete (oldest beyond retention limit)
    runs_to_delete = [row[0] for row in rows[: len(rows) - max_runs]]

    # 3. Delete from DuckDB
    for run_id in runs_to_delete:
        conn.execute("DELETE FROM audit_events WHERE run_id = ?", [run_id])

    # 4. Delete JSONL files
    for run_id in runs_to_delete:
        jsonl_path = audit_dir / f"audit_{run_id}.jsonl"
        try:
            if jsonl_path.exists():
                jsonl_path.unlink()
        except OSError:
            logger.warning("Failed to delete audit JSONL file: %s", jsonl_path)

    logger.info(
        "Audit retention: deleted %d old run(s), kept %d",
        len(runs_to_delete),
        max_runs,
    )
