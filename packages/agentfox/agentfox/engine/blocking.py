"""Review-blocking evaluation: decides whether review findings block downstream tasks.

Extracted from result_handler.py to isolate blocking decision logic.

Requirements: 26-REQ-9.3, 30-REQ-2.3, 84-REQ-3.1, 84-REQ-3.E1, 554-REQ-1
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from typing import Any

from afaudit.emit import emit_audit_event
from afaudit.events import AuditEventType

from agentfox.core.config import ArchetypesConfig
from agentfox.core.node_id import parse_node_id
from agentfox.engine.state import SessionRecord

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class BlockDecision:
    """Result of evaluating whether a review session should block a task."""

    should_block: bool
    coder_node_id: str = ""
    reason: str = ""


def _format_block_reason(
    archetype: str,
    findings: list[Any],
    threshold: int,
    spec_name: str,
    task_group: str,
) -> str:
    """Format an enriched blocking reason string with finding IDs and descriptions.

    Includes the count of actionable findings (critical + major), up to 3
    finding IDs as `F-<8hex>` short prefixes, truncated descriptions (max 60
    chars each), and "and N more" when there are more than 3 findings.

    Requirements: 84-REQ-3.1, 84-REQ-3.E1
    """
    actionable = [f for f in findings if f.severity.lower() in ("critical", "major")]
    critical_count = sum(1 for f in actionable if f.severity.lower() == "critical")
    major_count = len(actionable) - critical_count

    parts_label = []
    if critical_count:
        parts_label.append(f"{critical_count} critical")
    if major_count:
        parts_label.append(f"{major_count} major")
    count_label = " + ".join(parts_label) if parts_label else "0"

    header = (
        f"{archetype.capitalize()} found {count_label} finding(s) "
        f"(threshold: {threshold}) for {spec_name}:{task_group}"
    )

    n = len(actionable)
    if n == 0:
        return header

    shown = actionable[:3]
    detail_parts = []
    for finding in shown:
        raw_id = finding.id.replace("-", "")[:8]
        short_id = f"F-{raw_id}"
        desc = finding.description[:60]
        if len(finding.description) > 60:
            desc += "…"
        detail_parts.append(f"{short_id}: {desc}")

    detail = ", ".join(detail_parts)
    if n > 3:
        detail += f", and {n - 3} more"

    return f"{header} — {detail}"


def _is_deferred_to_future_group(description: str, current_group: str) -> bool:
    """Return True if the description indicates this test is deferred to a future task group.

    Matches case-insensitive occurrences of 'task group N' where N is an integer
    greater than current_group.  This covers all observed phrasings from the wild:

    - "Integration smoke test. Deferred to task group 4."
    - "Integration smoke test. Assigned to task group 4, not yet started."
    - "End-to-end integration smoke test. … deferred to task group 4."

    Requirements: 572-AC-2, 572-AC-4
    """
    try:
        current = int(current_group)
    except (ValueError, TypeError):
        return False

    for match in re.finditer(r"task\s+group\s+(\d+)", description, re.IGNORECASE):
        if int(match.group(1)) > current:
            return True
    return False


def _evaluate_audit_review_blocking(
    knowledge_db_conn: Any,
    spec_name: str,
    task_group: str,
    coder_node_id: str,
    node_id: str,
) -> BlockDecision:
    """Evaluate audit-review blocking: only active critical audit findings trigger retry.

    Uses ``query_active_findings`` filtered by ``category='audit'`` because the
    session_id format for audit findings (``{spec_name}:audit:{N}``) does not
    match the reviewer node_id format used by ``query_findings_by_session``.

    Findings whose descriptions explicitly defer the test to a **future task group**
    (e.g. "Deferred to task group 4", "Assigned to task group 4, not yet started")
    are excluded from the blocking set.  The coder cannot satisfy these requirements
    because the referenced code will be written in a later group; blocking on them
    creates an unwinnable retry loop.

    Only ``critical`` findings (from MISSING/MISALIGNED verdicts) block.
    ``major`` findings (from WEAK verdicts) are logged as warnings but do not
    halt the pipeline — a later task group may fix the underlying tests,
    and blocking on WEAK findings creates unwinnable retry loops (issue #639).

    Returns a blocking decision when any non-superseded, non-deferred critical
    audit finding exists for (spec_name, task_group).  An empty, deferred-only,
    major-only, or minor-only finding set returns ``should_block=False`` so
    execution proceeds normally.

    Requirements: 554-REQ-1, 572-AC-2, 572-AC-4, 639-AC-1
    """
    try:
        from agentfox.knowledge.review_store import query_active_findings

        audit_findings = [
            f
            for f in query_active_findings(knowledge_db_conn, spec_name, task_group)
            if getattr(f, "category", None) == "audit"
        ]

        if not audit_findings:
            return BlockDecision(should_block=False)

        # Filter out findings whose descriptions explicitly defer the test to a
        # future task group (e.g. "Deferred to task group 4", "Assigned to task
        # group 4, not yet started").  The coder cannot fix these — the required
        # code belongs to a later group — so counting them as blocking findings
        # creates an unwinnable retry loop.  Requirements: 572-AC-2, 572-AC-4
        actionable_findings = [f for f in audit_findings if not _is_deferred_to_future_group(f.description, task_group)]

        if not actionable_findings:
            logger.debug(
                "AUDIT-REVIEW: all %d finding(s) for %s:%s are deferred to a future group — not blocking",
                len(audit_findings),
                spec_name,
                task_group,
            )
            return BlockDecision(should_block=False)

        # Only critical findings block.  Major (WEAK) findings are logged but
        # do not halt the pipeline — they may be resolved by later task groups
        # and blocking on them creates unwinnable retry loops (issue #639).
        blocking_findings = [f for f in actionable_findings if f.severity.lower() == "critical"]

        if not blocking_findings:
            weak_count = len(actionable_findings)
            logger.info(
                "AUDIT-REVIEW: %d WEAK (major) finding(s) for %s:%s — not blocking",
                weak_count,
                spec_name,
                task_group,
            )
            return BlockDecision(should_block=False)

        n = len(blocking_findings)
        shown = blocking_findings[:3]
        detail = ", ".join(
            "F-" + f.id.replace("-", "")[:8] + ": " + f.description[:60] + ("…" if len(f.description) > 60 else "")
            for f in shown
        )
        if n > 3:
            detail += f", and {n - 3} more"
        reason = f"reviewer:audit-review found {n} critical audit finding(s) for {spec_name}:{task_group} — {detail}"
        logger.warning("AUDIT-REVIEW blocking %s: %s", coder_node_id, reason)
        return BlockDecision(
            should_block=True,
            coder_node_id=coder_node_id,
            reason=reason,
        )
    except Exception:
        logger.warning(
            "Failed to evaluate audit-review blocking for %s",
            node_id,
            exc_info=True,
        )
        return BlockDecision(should_block=False)


def _evaluate_drift_review_blocking(
    knowledge_db_conn: Any,
    spec_name: str,
    task_group: str,
    coder_node_id: str,
    node_id: str,
    archetypes_config: ArchetypesConfig | None,
) -> BlockDecision:
    """Evaluate drift-review blocking using the drift_findings table.

    Counts both critical and major drift findings toward the configured
    ``drift_review_block_threshold``.  When the threshold is ``None``
    (advisory mode), returns ``should_block=False`` unconditionally.
    """
    try:
        from agentfox.knowledge.review_store import query_active_drift_findings

        threshold: int | None = None
        if archetypes_config is not None:
            threshold = archetypes_config.reviewer_config.drift_review_block_threshold
        if threshold is None:
            return BlockDecision(should_block=False)

        drift_findings = query_active_drift_findings(
            knowledge_db_conn, spec_name, task_group
        )
        if not drift_findings:
            return BlockDecision(should_block=False)

        actionable = [f for f in drift_findings if f.severity.lower() in ("critical", "major")]
        if len(actionable) < threshold:
            return BlockDecision(should_block=False)

        critical_count = sum(1 for f in actionable if f.severity.lower() == "critical")
        major_count = len(actionable) - critical_count

        parts_label = []
        if critical_count:
            parts_label.append(f"{critical_count} critical")
        if major_count:
            parts_label.append(f"{major_count} major")
        count_label = " + ".join(parts_label)

        header = (
            f"Reviewer:drift-review found {count_label} finding(s) "
            f"(threshold: {threshold}) for {spec_name}:{task_group}"
        )
        shown = actionable[:3]
        detail_parts = []
        for f in shown:
            raw_id = f.id.replace("-", "")[:8]
            short_id = f"F-{raw_id}"
            desc = f.description[:60]
            if len(f.description) > 60:
                desc += "…"
            detail_parts.append(f"{short_id}: {desc}")
        detail = ", ".join(detail_parts)
        if len(actionable) > 3:
            detail += f", and {len(actionable) - 3} more"
        reason = f"{header} — {detail}"

        logger.warning("DRIFT-REVIEW blocking %s: %s", coder_node_id, reason)
        return BlockDecision(
            should_block=True,
            coder_node_id=coder_node_id,
            reason=reason,
        )
    except Exception:
        logger.warning(
            "Failed to evaluate drift-review blocking for %s",
            node_id,
            exc_info=True,
        )
        return BlockDecision(should_block=False)


def evaluate_review_blocking(
    record: SessionRecord,
    archetypes_config: ArchetypesConfig | None,
    knowledge_db_conn: Any | None,
    *,
    mode: str | None = None,
    sink: Any | None = None,
    run_id: str = "",
) -> BlockDecision:
    """Evaluate whether a reviewer session should block its downstream task.

    Supports the consolidated reviewer archetype with modes (pre-review,
    drift-review) as well as legacy archetype names for backward compat.

    Queries persisted review findings from DuckDB, counts critical findings,
    applies the configured (or learned) block threshold.

    Critical findings with category='security' always trigger blocking,
    regardless of the numeric threshold, because security vulnerabilities
    must be remediated before downstream work can proceeded.

    Returns a BlockDecision indicating whether blocking should occur and why.
    """
    archetype = record.archetype

    # Only reviewer pre-review, drift-review, and audit-review modes can block.
    # fix-review does not participate in blocking.
    if archetype == "reviewer":
        if mode not in ("pre-review", "drift-review", "audit-review"):
            return BlockDecision(should_block=False)
    else:
        return BlockDecision(should_block=False)

    if knowledge_db_conn is None:
        return BlockDecision(should_block=False)

    parsed = parse_node_id(record.node_id)
    spec_name = parsed.spec_name
    # Group-0 nodes are auto_pre reviewers; the first coder group is always 1
    task_group = "1" if parsed.group_number == 0 else str(parsed.group_number)
    coder_node_id = f"{spec_name}:{task_group}"

    # Display label for log messages
    display_name = f"reviewer:{mode}" if archetype == "reviewer" and mode else archetype

    # Audit-review uses a distinct blocking path: any active critical/major
    # audit finding for (spec_name, task_group) triggers a retry.  The
    # session_id format used by audit findings (``{spec_name}:audit:{attempt}``)
    # does not match the reviewer node_id format, so we query by
    # spec+task_group filtered by category='audit' rather than by session_id.
    if mode == "audit-review":
        return _evaluate_audit_review_blocking(knowledge_db_conn, spec_name, task_group, coder_node_id, record.node_id)

    if mode == "drift-review":
        return _evaluate_drift_review_blocking(
            knowledge_db_conn, spec_name, task_group, coder_node_id, record.node_id, archetypes_config
        )

    try:
        from agentfox.knowledge.review_store import query_findings_by_session

        session_id = f"{record.node_id}:{record.attempt}"
        findings = query_findings_by_session(knowledge_db_conn, session_id)

        # Scope findings to this task_group only — cross-group findings (e.g. from
        # a spec-wide reviewer session) must not block an unrelated coder group.
        findings = [f for f in findings if f.task_group == task_group]

        actionable_count = sum(1 for f in findings if f.severity.lower() in ("critical", "major"))

        if actionable_count == 0:
            return BlockDecision(should_block=False)

        # Security bypass: critical findings with category='security' always block,
        # regardless of the numeric threshold.
        security_critical = [
            f for f in findings if f.severity.lower() == "critical" and getattr(f, "category", None) == "security"
        ]
        if security_critical:
            shown = security_critical[:3]
            detail = ", ".join(
                f"F-{f.id.replace('-', '')[:8]}: {f.description[:60]}" + ("…" if len(f.description) > 60 else "")
                for f in shown
            )
            reason = (
                f"[SECURITY] {display_name.capitalize()} found {len(security_critical)} critical "
                f"security finding(s) for {spec_name}:{task_group} — {detail}"
            )
            logger.warning("SECURITY blocking %s: %s", coder_node_id, reason)
            emit_audit_event(
                sink,
                run_id,
                AuditEventType.SECURITY_FINDING_BLOCKED,
                node_id=record.node_id,
                session_id=session_id,
                archetype=archetype,
                payload={
                    "spec_name": spec_name,
                    "task_group": task_group,
                    "security_critical_count": len(security_critical),
                    "finding_ids": [str(f.id) for f in security_critical],
                },
            )
            return BlockDecision(
                should_block=True,
                coder_node_id=coder_node_id,
                reason=reason,
            )

        # Resolve threshold from ReviewerConfig by mode
        configured_threshold = 3  # conservative default
        if archetypes_config is not None:
            rc = archetypes_config.reviewer_config
            if archetype == "reviewer" and mode == "pre-review":
                configured_threshold = rc.pre_review_block_threshold

        blocked = actionable_count >= configured_threshold

        if blocked:
            reason = _format_block_reason(
                display_name,
                findings,
                configured_threshold,
                spec_name,
                task_group,
            )
            logger.warning(
                "%s blocking %s: %s",
                display_name.capitalize(),
                coder_node_id,
                reason,
            )
            return BlockDecision(
                should_block=True,
                coder_node_id=coder_node_id,
                reason=reason,
            )

    except Exception:
        logger.warning(
            "Failed to evaluate %s blocking for %s",
            display_name,
            record.node_id,
            exc_info=True,
        )

    return BlockDecision(should_block=False)
