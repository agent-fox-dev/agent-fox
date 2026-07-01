"""Utility to purge stale audit files at CLI startup.

The ``.agent-fox/audit/`` directory accumulates ``agent_*.jsonl``,
``audit_*.jsonl``, and ``postmortem_*.json`` files across runs.  These
files are only useful during or shortly after the run that produced them;
by the next ``af code`` / ``af nightshift`` invocation they are stale.

This module provides :func:`purge_stale_audit_files` for best-effort
removal of those files at startup.  Deletion failures are logged as
warnings and never propagate to the caller.
"""

from __future__ import annotations

import logging
from pathlib import Path

logger = logging.getLogger(__name__)

# Glob patterns that match stale ephemeral audit files produced during a run.
# Unrelated files (e.g. ``audit_{spec}.md`` from audit output) are NOT
# matched and are left untouched.
_STALE_PATTERNS: tuple[str, ...] = (
    "agent_*.jsonl",
    "audit_*.jsonl",
    "postmortem_*.json",
)


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
