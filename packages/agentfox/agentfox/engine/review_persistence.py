"""Post-session review finding persistence.

Extracted from session_lifecycle.py to reduce the NodeSessionRunner
god class. Handles parsing and persisting structured findings from
review archetypes (reviewer with modes, verifier).

Requirements: 53-REQ-1.1, 53-REQ-2.1, 53-REQ-3.1,
              74-REQ-3.*, 74-REQ-4.*, 74-REQ-5.*,
              98-REQ-5.1, 98-REQ-5.2
"""

from __future__ import annotations

import logging
from collections import Counter
from pathlib import Path
from typing import TYPE_CHECKING, Any

from agentfox.core.json_extraction import extract_json_array
from agentfox.engine.audit_helpers import emit_audit_event
from agentfox.knowledge.audit import AuditEventType, AuditSeverity
from agentfox.knowledge.sink import SessionSink, SinkDispatcher

if TYPE_CHECKING:
    from agentfox.knowledge.review_store import ReviewFinding

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Format retry constant
# ---------------------------------------------------------------------------

FORMAT_RETRY_PROMPT: str = (
    "Your previous response could not be parsed as valid JSON. "
    "Please output ONLY the structured JSON block with no surrounding text, "
    "no markdown fences, and no commentary. Use exactly the field names "
    "from the schema provided in your instructions."
)

# Extraction strategy names used in parse failure payloads
_STRATEGY_INITIAL = "bracket_scan"
_STRATEGY_RETRY = "retry"


def _emit_persistence_event(
    sink: SinkDispatcher | SessionSink | None,
    run_id: str,
    archetype: str,
    node_id: str,
    spec_name: str,
    task_group: str,
    records: list[Any],
    count: int,
    *,
    mode: str | None = None,
) -> None:
    """Emit the appropriate persistence audit event after successful insertion.

    Logs a warning and continues if emission fails (84-REQ-2.E1).

    Requirements: 84-REQ-2.1, 84-REQ-2.2, 84-REQ-2.3, 84-REQ-2.E1
    """
    try:
        # Determine the effective dispatch key for reviewer modes
        dispatch_key = archetype
        if archetype == "reviewer" and mode:
            dispatch_key = f"reviewer:{mode}"

        if dispatch_key in ("skeptic", "reviewer:pre-review"):
            severity_summary: dict[str, int] = dict(Counter(r.severity for r in records))
            emit_audit_event(
                sink,
                run_id,
                AuditEventType.REVIEW_FINDINGS_PERSISTED,
                node_id=node_id,
                archetype=archetype,
                payload={
                    "archetype": archetype,
                    "mode": mode,
                    "count": count,
                    "severity_summary": severity_summary,
                    "spec_name": spec_name,
                    "task_group": task_group,
                },
            )
        elif dispatch_key == "verifier":
            pass_count = sum(1 for r in records if r.verdict == "PASS")
            fail_count = sum(1 for r in records if r.verdict == "FAIL")
            emit_audit_event(
                sink,
                run_id,
                AuditEventType.REVIEW_VERDICTS_PERSISTED,
                node_id=node_id,
                archetype=archetype,
                payload={
                    "archetype": archetype,
                    "count": count,
                    "pass_count": pass_count,
                    "fail_count": fail_count,
                    "spec_name": spec_name,
                    "task_group": task_group,
                },
            )
        elif dispatch_key in ("oracle", "reviewer:drift-review"):
            severity_summary = dict(Counter(r.severity for r in records))
            emit_audit_event(
                sink,
                run_id,
                AuditEventType.REVIEW_DRIFT_PERSISTED,
                node_id=node_id,
                archetype=archetype,
                payload={
                    "archetype": archetype,
                    "mode": mode,
                    "count": count,
                    "severity_summary": severity_summary,
                    "spec_name": spec_name,
                    "task_group": task_group,
                },
            )
    except Exception:
        logger.warning(
            "Failed to emit persistence audit event for %s %s",
            archetype,
            node_id,
            exc_info=True,
        )


def _try_extract_with_retry(
    transcript: str,
    extract_fn: Any,
    *,
    session_handle: Any,
    sink: SinkDispatcher | SessionSink | None,
    run_id: str,
    node_id: str,
    archetype: str,
) -> tuple[Any | None, bool]:
    """Extract structured data from transcript, retrying once if possible.

    Returns (extracted_result, retry_attempted). The result is None when
    all strategies are exhausted; the caller should bail out.

    Requirements: 74-REQ-3.1, 74-REQ-3.3, 74-REQ-3.5
    """
    result = extract_fn(transcript)
    retry_attempted = False

    if result is not None:
        return result, False

    session_is_alive = session_handle is not None and getattr(session_handle, "is_alive", False)
    if session_is_alive:
        logger.warning(
            "Initial parse failed for %s %s — attempting format retry",
            archetype,
            node_id,
        )
        retry_response = session_handle.append_user_message(FORMAT_RETRY_PROMPT)
        retry_attempted = True
        result = extract_fn(retry_response)

    if result is None:
        strategy_parts = [_STRATEGY_INITIAL]
        if retry_attempted:
            strategy_parts.append(_STRATEGY_RETRY)
        emit_audit_event(
            sink,
            run_id,
            AuditEventType.REVIEW_PARSE_FAILURE,
            node_id=node_id,
            archetype=archetype,
            severity=AuditSeverity.WARNING,
            payload={
                "raw_output": transcript[:2000],
                "retry_attempted": retry_attempted,
                "strategy": ",".join(strategy_parts),
            },
        )
        return None, retry_attempted

    if retry_attempted:
        emit_audit_event(
            sink,
            run_id,
            AuditEventType.REVIEW_PARSE_RETRY_SUCCESS,
            node_id=node_id,
            archetype=archetype,
            severity=AuditSeverity.INFO,
            payload={"archetype": archetype},
        )

    return result, retry_attempted


def _persist_standard_findings(
    json_objects: list[Any],
    dispatch_key: str,
    archetype: str,
    node_id: str,
    session_id: str,
    spec_name: str,
    task_group: str,
    knowledge_db_conn: Any,
    sink: SinkDispatcher | SessionSink | None,
    run_id: str,
    mode: str | None,
    retry_attempted: bool,
    transcript: str,
) -> None:
    """Parse and persist skeptic or oracle findings.

    Requirements: 53-REQ-1.1, 53-REQ-3.1
    """
    from agentfox.knowledge.review_store import (
        insert_drift_findings,
        insert_findings,
    )
    from agentfox.session.review_parser import (
        parse_drift_findings,
        parse_review_findings,
    )

    _review_dispatch: dict[str, tuple[Any, Any, str]] = {
        "skeptic": (parse_review_findings, insert_findings, "review findings"),
        "oracle": (parse_drift_findings, insert_drift_findings, "drift findings"),
    }
    parser, inserter, label = _review_dispatch[dispatch_key]
    records = parser(json_objects, spec_name, task_group, session_id)
    if records:
        count = inserter(knowledge_db_conn, records)
        logger.info("Persisted %d %s for %s", count, label, node_id)
        _emit_persistence_event(
            sink,
            run_id,
            archetype,
            node_id,
            spec_name,
            task_group,
            records,
            count,
            mode=mode,
        )
    else:
        emit_audit_event(
            sink,
            run_id,
            AuditEventType.REVIEW_PARSE_FAILURE,
            node_id=node_id,
            archetype=archetype,
            severity=AuditSeverity.WARNING,
            payload={
                "raw_output": transcript[:2000],
                "retry_attempted": retry_attempted,
                "strategy": _STRATEGY_INITIAL,
            },
        )


def _persist_auditor_findings(
    audit_result: Any,
    node_id: str,
    attempt: int,
    spec_name: str,
    task_group: str,
    knowledge_db_conn: Any,
    specs_dir: Path | None,
) -> None:
    """Persist converged auditor results.

    Requirements: 98-REQ-5.1, 98-REQ-5.2
    """
    from agentfox.session.auditor_output import persist_auditor_results

    if specs_dir is not None:
        spec_dir = specs_dir / spec_name
    else:
        from agentfox.core.config import AgentFoxConfig, resolve_spec_root

        spec_dir = resolve_spec_root(AgentFoxConfig(), Path.cwd()) / spec_name
    persist_auditor_results(
        spec_dir,
        audit_result,
        attempt=attempt,
        project_root=Path.cwd(),
        conn=knowledge_db_conn,
        task_group=task_group,
    )


def persist_review_findings(
    transcript: str,
    node_id: str,
    attempt: int,
    *,
    archetype: str,
    spec_name: str,
    task_group: int | str,
    knowledge_db_conn: Any,
    sink: SinkDispatcher | SessionSink | None,
    run_id: str,
    session_handle: Any = None,
    mode: str | None = None,
    specs_dir: Path | None = None,
) -> None:
    """Parse and persist structured findings from review archetypes.

    Routes to the correct handler based on archetype and mode.
    Non-review archetypes (coder, etc.) are silently skipped.

    Requirements: 53-REQ-1.1, 53-REQ-2.1, 53-REQ-3.1,
                  74-REQ-3.1, 74-REQ-3.2, 74-REQ-3.3, 74-REQ-3.4,
                  74-REQ-3.5, 74-REQ-3.E1, 74-REQ-3.E2,
                  74-REQ-5.1, 74-REQ-5.2, 74-REQ-5.3,
                  98-REQ-5.1, 98-REQ-5.2
    """
    if archetype not in ("skeptic", "oracle", "auditor", "reviewer"):
        return

    if archetype == "reviewer":
        if mode == "audit-review":
            archetype = "auditor"
        elif mode in ("pre-review", "drift-review"):
            pass
        elif mode == "fix-review":
            return
        else:
            return

    tg = str(task_group)
    session_id = f"{node_id}:{attempt}"

    dispatch_key = archetype
    if archetype == "reviewer" and mode:
        if mode == "pre-review":
            dispatch_key = "skeptic"
        elif mode == "drift-review":
            dispatch_key = "oracle"

    retry_kwargs = dict(
        session_handle=session_handle,
        sink=sink,
        run_id=run_id,
        node_id=node_id,
        archetype=archetype,
    )

    try:
        if dispatch_key in ("skeptic", "verifier", "oracle"):
            json_objects, retry_attempted = _try_extract_with_retry(
                transcript,
                extract_json_array,
                **retry_kwargs,
            )
            if json_objects is None:
                return
            _persist_standard_findings(
                json_objects,
                dispatch_key,
                archetype,
                node_id,
                session_id,
                spec_name,
                tg,
                knowledge_db_conn,
                sink,
                run_id,
                mode,
                retry_attempted,
                transcript,
            )
        elif archetype == "auditor":
            from agentfox.session.review_parser import parse_auditor_output

            audit_result, _ = _try_extract_with_retry(
                transcript,
                parse_auditor_output,
                **retry_kwargs,
            )
            if audit_result is None:
                return
            _persist_auditor_findings(
                audit_result,
                node_id,
                attempt,
                spec_name,
                tg,
                knowledge_db_conn,
                specs_dir,
            )
    except Exception:
        logger.warning(
            "Failed to persist %s findings for %s, continuing",
            archetype,
            node_id,
            exc_info=True,
        )


# ---------------------------------------------------------------------------
# Partial convergence helpers (74-REQ-4.*)
# ---------------------------------------------------------------------------


def warn_failed_parse_instances(
    raw_results: list[Any],
    archetype: str,
    run_id: str,
) -> None:
    """Log a warning for each instance that failed to produce parseable output.

    Requirements: 74-REQ-4.5
    """
    for i, result in enumerate(raw_results):
        if result is None:
            logger.warning(
                "Instance %d of archetype '%s' failed to parse (run_id=%s)",
                i,
                archetype,
                run_id,
            )


def converge_multi_instance_skeptic(
    raw_results: list[list[ReviewFinding] | None],
    *,
    sink: Any,
    run_id: str,
    node_id: str,
    block_threshold: int,
) -> list[ReviewFinding] | tuple[list[ReviewFinding], bool]:
    """Converge multi-instance skeptic results, filtering failed instances.

    Filters out None results (parse failures), logs warnings for each,
    and passes remaining results to converge_skeptic_records. Emits
    REVIEW_PARSE_FAILURE if all instances failed.

    Requirements: 74-REQ-4.1, 74-REQ-4.4, 74-REQ-4.5, 74-REQ-4.E1,
                  74-REQ-4.E2
    """
    from agentfox.session.convergence import converge_skeptic_records

    # Log warnings for failed instances
    warn_failed_parse_instances(raw_results, archetype="skeptic", run_id=run_id)

    filtered = [r for r in raw_results if r is not None]

    if not filtered:
        # 74-REQ-4.E1: All instances failed
        emit_audit_event(
            sink,
            run_id,
            AuditEventType.REVIEW_PARSE_FAILURE,
            node_id=node_id,
            archetype="skeptic",
            severity=AuditSeverity.WARNING,
            payload={"raw_output": "", "all_instances_failed": True},
        )
        return []

    merged, blocked = converge_skeptic_records(filtered, block_threshold)
    return merged, blocked
