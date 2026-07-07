"""Audit file cleanup and retention utilities.

Provides :func:`purge_stale_audit_files` for best-effort removal of
ephemeral audit files at startup, and :func:`enforce_file_retention` for
deleting the oldest audit run file sets beyond a configured maximum.

Migrated from ``agentfox.workspace.audit_cleanup`` (purge) and the
file-only half of the former ``enforce_audit_retention`` (retention).
The DB-retention half remains in the agentfox package.
"""

from __future__ import annotations

import logging
import re
from pathlib import Path

logger = logging.getLogger("afaudit.cleanup")

# Glob patterns that match stale ephemeral audit files produced during a run.
# Unrelated files (e.g. ``audit_{spec}.md`` from audit output) are NOT
# matched and are left untouched.
_STALE_PATTERNS: tuple[str, ...] = (
    "agent_*.jsonl",
    "audit_*.jsonl",
    "nightshift_*.json",
    "postmortem_*.json",
)

# Regex to extract the YYYYMMDD_HHMMSS_hex run_id from an audit filename.
# Example: audit_20240101_100000_aaa001.jsonl -> 20240101_100000_aaa001
_RUN_ID_RE = re.compile(r"^audit_(\d{8}_\d{6}_[0-9a-f]+)\.jsonl$")


def purge_stale_audit_files(audit_dir: Path) -> int:
    """Delete stale audit files from *audit_dir*.

    Removes files matching:

    - ``agent_*.jsonl``
    - ``audit_*.jsonl``
    - ``postmortem_*.json``

    Deletion is best-effort: per-file ``OSError`` exceptions are caught,
    logged at WARNING level, and do not abort the cleanup loop.

    Args:
        audit_dir: Path to the audit directory (typically
            ``<repo_root>/.agent-fox/audit``).

    Returns:
        The number of files successfully removed.
    """
    if not audit_dir.is_dir():
        logger.debug("Audit directory does not exist, skipping purge: %s", audit_dir)
        return 0

    candidates: list[Path] = []
    for pattern in _STALE_PATTERNS:
        candidates.extend(audit_dir.glob(pattern))

    removed = 0
    for path in candidates:
        try:
            path.unlink()
            removed += 1
        except OSError as exc:
            logger.warning(
                "Failed to remove stale audit file %s: %s",
                path,
                exc,
            )

    logger.debug(
        "Purged %d stale audit file(s) from %s",
        removed,
        audit_dir,
    )
    return removed


def enforce_file_retention(audit_dir: Path, *, max_runs: int = 20) -> int:
    """Delete the oldest audit file sets beyond *max_runs*.

    Discovers ``run_id`` values from ``audit_*.jsonl`` filenames, parses
    the embedded ``YYYYMMDD_HHMMSS_hex`` timestamp, sorts runs
    chronologically, and deletes all three corresponding files
    (``audit_*.jsonl``, ``agent_*.jsonl``, ``postmortem_*.json``) for each
    run beyond *max_runs*.

    This is a pure filesystem operation with no database interaction.

    Args:
        audit_dir: Path to the audit directory.
        max_runs: Maximum number of audit run file sets to retain.
            Defaults to 20.

    Returns:
        The number of files successfully deleted.
    """
    if not audit_dir.is_dir():
        return 0

    # Discover run_ids from audit_*.jsonl filenames.
    run_ids: list[str] = []
    for path in audit_dir.glob("audit_*.jsonl"):
        m = _RUN_ID_RE.match(path.name)
        if m:
            run_ids.append(m.group(1))
        else:
            logger.warning(
                "Skipping audit file with unparseable timestamp: %s",
                path.name,
            )

    if len(run_ids) <= max_runs:
        return 0

    # Sort chronologically by the run_id string (YYYYMMDD_HHMMSS_hex).
    # The timestamp prefix provides natural chronological ordering.
    run_ids.sort()

    # Runs to delete: the oldest ones beyond the retention limit.
    runs_to_delete = run_ids[: len(run_ids) - max_runs]

    deleted = 0
    for run_id in runs_to_delete:
        for pattern in (
            f"audit_{run_id}.jsonl",
            f"agent_{run_id}.jsonl",
            f"nightshift_{run_id}.json",
            f"postmortem_{run_id}.json",
        ):
            file_path = audit_dir / pattern
            try:
                if file_path.exists():
                    file_path.unlink()
                    deleted += 1
            except OSError as exc:
                logger.warning(
                    "Failed to delete audit file %s: %s",
                    file_path,
                    exc,
                )

    return deleted
