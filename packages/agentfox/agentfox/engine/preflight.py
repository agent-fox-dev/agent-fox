"""Preflight checks."""

from __future__ import annotations

import logging
import subprocess
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


_TEST_TIMEOUT_SECONDS = 300


class PreflightVerdict(StrEnum):
    LAUNCH = "launch"
    SKIP = "skip"


@dataclass(frozen=True)
class PreflightResult:
    """Structured result from a preflight check.

    Captures the verdict and the state of each gate so the coder
    session can skip redundant Quick Triage checks.
    """

    verdict: PreflightVerdict
    checkboxes_done: bool
    has_findings: bool
    tests_passed: bool | None  # None = not run (short-circuited)

    def format_summary(self) -> str:
        """Format a human-readable summary for inclusion in the task prompt."""
        cb = "all complete" if self.checkboxes_done else "incomplete"
        findings = "active critical/major findings" if self.has_findings else "none"
        if self.tests_passed is None:
            tests = "not run (short-circuited)"
        elif self.tests_passed:
            tests = "pass"
        else:
            tests = "fail"
        return (
            "## Preflight State (from orchestrator)\n\n"
            f"- Subtask checkboxes: {cb}\n"
            f"- Active findings: {findings}\n"
            f"- Test baseline: {tests}\n\n"
            "The orchestrator has already verified these gates. "
            "Skip Quick Triage and proceed directly to implementation."
        )


def is_task_group_done_db(
    conn: Any,
    spec_name: str,
    group_number: int,
) -> bool | None:
    """Check plan_nodes DB for task group completion.

    Returns True if the node status is 'completed', False if it exists
    but is not completed, or None if the node is not found in the DB
    (indicating the caller should fall back to tasks.md).
    """
    try:
        row = conn.execute(
            """
            SELECT status
            FROM plan_nodes
            WHERE spec_name = ? AND group_number = ?
            LIMIT 1
            """,
            [spec_name, group_number],
        ).fetchone()
    except Exception:
        logger.debug(
            "Failed to query plan_nodes for %s:%d",
            spec_name,
            group_number,
            exc_info=True,
        )
        return None

    if row is None:
        return None
    return row[0] == "completed"


def is_task_group_done_file(
    specs_dir: Path,
    spec_name: str,
    group_number: int,
) -> bool:
    """Check task group checkbox state for a specific task group.

    Returns True only when the task group exists and has completed=True.
    """
    from agentfox.spec.parser import parse_tasks

    spec_dir = specs_dir / spec_name
    if not spec_dir.is_dir():
        return False
    try:
        groups = parse_tasks(spec_dir)
    except Exception:
        logger.debug(
            "Failed to parse tasks for %s",
            spec_name,
            exc_info=True,
        )
        return False

    for group in groups:
        if group.number == group_number:
            return group.completed
    return False


def is_task_group_done(
    conn: Any | None,
    spec_name: str,
    group_number: int,
    specs_dir: Path,
) -> bool:
    """Check whether a task group is already complete.

    Uses the DB as the source of truth, falling back to tasks.md
    when DB state is unavailable.
    """
    if conn is not None:
        db_result = is_task_group_done_db(conn, spec_name, group_number)
        if db_result is not None:
            return db_result

    return is_task_group_done_file(specs_dir, spec_name, group_number)


def has_active_critical_findings(
    conn: Any | None,
    spec_name: str,
    task_group: int,
) -> bool:
    """Return True if unresolved critical/major findings exist."""
    if conn is None:
        return False
    try:
        from agentfox.knowledge.review_store import query_active_findings

        findings = query_active_findings(conn, spec_name, str(task_group))
        return any(f.severity in ("critical", "major") for f in findings)
    except Exception:
        logger.debug(
            "Failed to query review findings for %s:%d",
            spec_name,
            task_group,
            exc_info=True,
        )
        return False


def do_tests_pass(cwd: Path) -> bool:
    """Run ``make test`` and return True if exit code is 0."""
    try:
        result = subprocess.run(
            ["make", "test"],
            cwd=cwd,
            capture_output=True,
            timeout=_TEST_TIMEOUT_SECONDS,
        )
        return result.returncode == 0
    except subprocess.TimeoutExpired:
        logger.warning("Pre-flight test run timed out after %ds", _TEST_TIMEOUT_SECONDS)
        return False
    except Exception:
        logger.debug("Pre-flight test run failed", exc_info=True)
        return False


def run_preflight(
    spec_name: str,
    group_number: int,
    conn: Any | None,
    specs_dir: Path,
    cwd: Path,
) -> PreflightResult:
    """Run the pre-flight check for a coder session.

    Gates are evaluated in order with short-circuit: if any gate
    fails, the check returns LAUNCH immediately to avoid running
    later (more expensive) gates.

    Returns a ``PreflightResult`` with the verdict and gate states.
    """
    checkboxes_done = is_task_group_done(conn, spec_name, group_number, specs_dir)

    if not checkboxes_done:
        return PreflightResult(
            verdict=PreflightVerdict.LAUNCH,
            checkboxes_done=False,
            has_findings=False,
            tests_passed=None,
        )

    findings = has_active_critical_findings(conn, spec_name, group_number)
    if findings:
        logger.info(
            "Preflight: %s:%d has done checkboxes but active findings, launching coder",
            spec_name,
            group_number,
        )
        return PreflightResult(
            verdict=PreflightVerdict.LAUNCH,
            checkboxes_done=True,
            has_findings=True,
            tests_passed=None,
        )

    tests_ok = do_tests_pass(cwd)
    if not tests_ok:
        logger.info(
            "Preflight: %s:%d has done checkboxes but tests fail, launching coder",
            spec_name,
            group_number,
        )
        return PreflightResult(
            verdict=PreflightVerdict.LAUNCH,
            checkboxes_done=True,
            has_findings=False,
            tests_passed=False,
        )

    logger.info(
        "Preflight: %s:%d is complete — checkboxes done, no findings, tests pass. Skipping coder session.",
        spec_name,
        group_number,
    )
    return PreflightResult(
        verdict=PreflightVerdict.SKIP,
        checkboxes_done=True,
        has_findings=False,
        tests_passed=True,
    )
