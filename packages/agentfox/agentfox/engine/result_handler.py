"""Session result processing: retry decisions, timeout handling.

Extracted from engine.py to reduce the Orchestrator class size. Handles
the outcome of each completed session: marking success, deciding retries,
cascade-blocking on exhaustion, and emitting audit events.

Requirements: 26-REQ-9.3, 40-REQ-9.4, 18-REQ-5.4,
              58-REQ-1.*, 58-REQ-2.*
"""

from __future__ import annotations

import json
import logging
import math
import re
import subprocess
import time
import tomllib
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from agentfox.archetypes import get_archetype
from agentfox.engine.audit_helpers import emit_audit_event
from agentfox.engine.blocking import evaluate_review_blocking
from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.state import ExecutionState, SessionRecord, update_state_with_session
from agentfox.knowledge.audit import AuditEventType
from agentfox.knowledge.sink import SinkDispatcher
from agentfox.ui.progress import TaskCallback, TaskEvent

logger = logging.getLogger(__name__)


_MAX_WORKSPACE_FAILURES = 3
_MAX_WORKSPACE_BACKOFF_SECONDS = 30


@dataclass
class _NodeRetryState:
    timeout_retries: int = 0
    audit_retry_count: int = 0
    max_turns: int | None = None
    has_max_turns: bool = False
    timeout: int | None = None
    original_timeout: int | None = None
    coverage_baseline: Any = field(default=None, repr=False)
    workspace_failures: int = 0
    workspace_next_eligible: float = 0.0


class SessionResultHandler:
    """Processes session outcomes: success, retry, blocking.

    Extracted from Orchestrator to isolate the retry decision tree
    from the dispatch loop.
    """

    def __init__(
        self,
        *,
        graph_sync: GraphSync,
        max_retries: int,
        task_callback: TaskCallback | None,
        sink: SinkDispatcher | None,
        run_id: str,
        graph: Any | None,
        archetypes_config: Any | None,
        knowledge_db_conn: Any | None,
        block_task_fn: Callable[[str, ExecutionState, str], None],
        check_block_budget_fn: Callable[[ExecutionState], bool],
        max_timeout_retries: int = 2,
        timeout_multiplier: float = 1.5,
        timeout_ceiling_factor: float = 2.0,
        original_session_timeout: int = 45,
    ) -> None:
        self._graph_sync = graph_sync
        self._max_retries = max_retries
        self._task_callback = task_callback
        self._sink = sink
        self._run_id = run_id
        self._graph = graph
        self._archetypes_config = archetypes_config
        self._knowledge_db_conn = knowledge_db_conn
        if knowledge_db_conn is None:
            logger.warning("knowledge_db_conn is None — session outcomes will not be recorded to DB")
        self._block_task = block_task_fn
        self._check_block_budget = check_block_budget_fn

        self._node_retry_states: dict[str, _NodeRetryState] = {}
        self._node_failure_counts: dict[str, int] = {}
        self._coverage_tool: Any = None  # None = not checked, False = no tool
        self._max_timeout_retries: int = max_timeout_retries
        self._timeout_multiplier: float = timeout_multiplier
        self._timeout_ceiling_factor: float = timeout_ceiling_factor
        self._original_session_timeout: int = original_session_timeout

    def _get_node_archetype(self, node_id: str) -> str:
        """Get the archetype name for a node from the task graph."""
        if self._graph is not None and node_id in self._graph.nodes:
            return self._graph.nodes[node_id].archetype
        return "coder"

    def _get_node_mode(self, node_id: str) -> str | None:
        """Get the mode for a node from the task graph."""
        if self._graph is not None and node_id in self._graph.nodes:
            return self._graph.nodes[node_id].mode
        return None

    def _get_node_state(self, node_id: str) -> _NodeRetryState:
        ns = self._node_retry_states.get(node_id)
        if ns is None:
            ns = _NodeRetryState()
            self._node_retry_states[node_id] = ns
        return ns

    def get_timeout_override(self, node_id: str) -> int | None:
        ns = self._node_retry_states.get(node_id)
        return ns.timeout if ns is not None else None

    def get_max_turns_override(self, node_id: str) -> tuple[bool, int | None]:
        ns = self._node_retry_states.get(node_id)
        if ns is None or not ns.has_max_turns:
            return False, None
        return True, ns.max_turns

    def _get_predecessors(self, node_id: str) -> list[str]:
        """Get predecessor node IDs for a given node."""
        return self._graph_sync.predecessors(node_id)

    def _get_coverage_tool(self, cwd: Path) -> Any:
        """Lazy-detect the coverage tool once per run."""
        if self._coverage_tool is None:
            tool = detect_coverage_tool(cwd)
            self._coverage_tool = tool if tool is not None else False
        return self._coverage_tool if self._coverage_tool is not False else None

    def capture_coverage_baseline(self, node_id: str, cwd: Path) -> None:
        """Measure and store baseline coverage before a coder session."""
        tool = self._get_coverage_tool(cwd)
        if tool is None:
            return
        try:
            result = measure_coverage(cwd, tool)
            if result is not None:
                self._get_node_state(node_id).coverage_baseline = result
                logger.debug("Captured coverage baseline for %s (%d files)", node_id, len(result.files))
        except Exception:
            logger.debug("Failed to capture coverage baseline for %s", node_id, exc_info=True)

    def check_coverage_regression(
        self,
        record: SessionRecord,
        state: ExecutionState,
        cwd: Path,
    ) -> str | None:
        """Check for coverage regression after a successful coder session.

        Returns JSON coverage data for storage, or None if no measurement
        was possible. Emits a blocking finding if coverage regressed.
        """
        ns = self._get_node_state(record.node_id)
        baseline = ns.coverage_baseline
        ns.coverage_baseline = None
        if baseline is None:
            return None

        tool = self._get_coverage_tool(cwd)
        if tool is None:
            return None

        try:
            current = measure_coverage(cwd, tool)
            if current is None:
                return None

            modified_files = record.files_touched or []
            regressions = find_regressions(baseline, current, modified_files)

            if regressions:
                self._emit_coverage_regression(record, regressions, state)

            return current.to_json()
        except Exception:
            logger.debug("Coverage regression check failed for %s", record.node_id, exc_info=True)
            return None

    def _emit_coverage_regression(
        self,
        record: SessionRecord,
        regressions: list[Any],
        state: ExecutionState,
    ) -> None:
        """Record a coverage regression finding and block the node."""
        details = "; ".join(
            f"{r.file_path}: {r.baseline_pct:.1f}% → {r.current_pct:.1f}% ({r.delta:+.1f}%)" for r in regressions
        )
        reason = f"Coverage regression on {len(regressions)} file(s): {details}"
        logger.warning("Coverage regression for %s: %s", record.node_id, reason)

        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.TASK_STATUS_CHANGE,
            node_id=record.node_id,
            payload={
                "from_status": "completed",
                "to_status": "blocked",
                "reason": reason,
                "regressions": [
                    {
                        "file": r.file_path,
                        "baseline": r.baseline_pct,
                        "current": r.current_pct,
                        "delta": r.delta,
                    }
                    for r in regressions
                ],
            },
        )

        if self._knowledge_db_conn is not None:
            try:
                from agentfox.core.node_id import parse_node_id

                parsed = parse_node_id(record.node_id)
                self._knowledge_db_conn.execute(
                    """
                    INSERT INTO review_findings
                        (id, severity, description, spec_name, task_group, session_id, category)
                    VALUES
                        (gen_random_uuid(), 'critical', ?, ?, ?, ?, 'coverage_regression')
                    """,
                    [
                        reason,
                        parsed.spec_name,
                        str(parsed.group_number) if parsed.group_number else "1",
                        f"{record.node_id}:{record.attempt}",
                    ],
                )
            except Exception:
                logger.debug("Failed to persist coverage regression finding", exc_info=True)

        self._block_task(record.node_id, state, reason)

    def check_review_blocking(
        self,
        record: SessionRecord,
        state: ExecutionState,
    ) -> bool:
        """Check if review findings should block downstream tasks."""
        decision = evaluate_review_blocking(
            record,
            self._archetypes_config,
            self._knowledge_db_conn,
            mode=self._get_node_mode(record.node_id),
            sink=self._sink,
            run_id=self._run_id,
        )
        if not decision.should_block:
            return False

        node_archetype = self._get_node_archetype(record.node_id)
        node_mode = self._get_node_mode(record.node_id)
        archetype_entry = get_archetype(node_archetype)
        if node_mode is not None:
            from agentfox.archetypes import resolve_effective_config

            archetype_entry = resolve_effective_config(archetype_entry, node_mode)

        if archetype_entry.retry_predecessor:
            return self._retry_on_review_block(record, decision, state, mode=node_mode)

        self._block_task(decision.coder_node_id, state, decision.reason)
        return True

    def _retry_on_review_block(
        self,
        record: SessionRecord,
        decision: Any,
        state: ExecutionState,
        *,
        mode: str | None = None,
    ) -> bool:
        """Convert a review block to a coder retry when retry_predecessor is set.

        Instead of permanently blocking the coder, lets it proceed with review
        findings injected as context.

        For audit-review mode, uses a dedicated per-node counter capped by
        ``ReviewerConfig.audit_max_retries``. For other modes, uses the
        generic failure counter against ``max_retries``.

        Returns True if the coder was permanently blocked (retries exhausted),
        False if converted to a retry.
        """
        coder_node_id = decision.coder_node_id

        if mode == "audit-review":
            return self._retry_on_audit_review_block(record, decision, state, coder_node_id)

        count = self._node_failure_counts.get(coder_node_id, 0) + 1
        self._node_failure_counts[coder_node_id] = count

        if count > self._max_retries:
            logger.warning(
                "Review retry-predecessor exhausted for %s, permanently blocking",
                coder_node_id,
            )
            self._block_task(coder_node_id, state, decision.reason)
            return True

        logger.info(
            "Review blocking converted to retry for %s (findings injected as context)",
            coder_node_id,
        )
        coder_status = self._graph_sync.node_states.get(coder_node_id)
        if coder_status == "completed":
            self._graph_sync._transition(coder_node_id, "pending", reason="retry after review block")
            self._graph_sync._transition(record.node_id, "pending", reason="retry after review block")

        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.TASK_STATUS_CHANGE,
            node_id=record.node_id,
            payload={
                "from_status": "completed",
                "to_status": "retry_predecessor",
                "reason": decision.reason,
                "coder_node_id": coder_node_id,
            },
        )

        if self._task_callback is not None:
            self._task_callback(
                TaskEvent(
                    node_id=record.node_id,
                    status="disagreed",
                    duration_s=0,
                    archetype=self._get_node_archetype(record.node_id),
                    predecessor_node=coder_node_id,
                )
            )

        return False

    def _get_audit_max_retries(self) -> int:
        """Read audit_max_retries from ReviewerConfig, defaulting to 2."""
        if self._archetypes_config is not None:
            return self._archetypes_config.reviewer_config.audit_max_retries
        return 2

    def _retry_on_audit_review_block(
        self,
        record: SessionRecord,
        decision: Any,
        state: ExecutionState,
        coder_node_id: str,
    ) -> bool:
        """Handle audit-review retry using a dedicated counter.

        Uses ``ReviewerConfig.audit_max_retries`` as a separate counter
        from the generic failure counter.

        Returns True if permanently blocked, False if converted to retry.
        """
        max_retries = self._get_audit_max_retries()
        ns = self._get_node_state(coder_node_id)
        count = ns.audit_retry_count

        if count >= max_retries:
            logger.warning(
                "Audit-review retries exhausted for %s (%d/%d), permanently blocking",
                coder_node_id,
                count,
                max_retries,
            )
            self._block_task(coder_node_id, state, decision.reason)
            return True

        ns.audit_retry_count = count + 1

        logger.info(
            "Audit-review blocking converted to retry for %s (%d/%d, findings injected as context)",
            coder_node_id,
            count + 1,
            max_retries,
        )
        coder_status = self._graph_sync.node_states.get(coder_node_id)
        if coder_status == "completed":
            self._graph_sync._transition(coder_node_id, "pending", reason="retry after audit-review block")
            self._graph_sync._transition(record.node_id, "pending", reason="retry after audit-review block")

        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.TASK_STATUS_CHANGE,
            node_id=record.node_id,
            payload={
                "from_status": "completed",
                "to_status": "retry_predecessor",
                "reason": decision.reason,
                "coder_node_id": coder_node_id,
                "audit_retry_count": count + 1,
                "audit_max_retries": max_retries,
            },
        )

        if self._task_callback is not None:
            self._task_callback(
                TaskEvent(
                    node_id=record.node_id,
                    status="disagreed",
                    duration_s=0,
                    archetype=self._get_node_archetype(record.node_id),
                    predecessor_node=coder_node_id,
                )
            )

        return False

    def process(
        self,
        record: SessionRecord,
        attempt: int,
        state: ExecutionState,
        attempt_tracker: dict[str, int],
        error_tracker: dict[str, str | None],
    ) -> None:
        """Process a completed session record and persist state."""
        update_state_with_session(state, record)

        # Run coverage regression gate for successful coder sessions
        if record.status == "completed" and self._get_node_archetype(record.node_id) == "coder":
            self.check_coverage_regression(record, state, Path.cwd())

        # 105-REQ-3.2: Record session outcome to DB (unified single source of truth).
        # 105-REQ-4.3: Accumulate run token/cost totals.
        if self._knowledge_db_conn is not None:
            try:
                import uuid as _uuid  # stdlib first (ruff I001)

                from agentfox.core.node_id import spec_name_of as _spec_name_of
                from agentfox.engine.state import (
                    SessionOutcomeRecord,
                )
                from agentfox.engine.state import (
                    record_session as _record_session_db,
                )
                from agentfox.engine.state import (
                    update_run_totals as _update_run_totals,
                )

                spec_name = _spec_name_of(record.node_id)
                idx = record.node_id.find(":")
                task_group = record.node_id[idx + 1 :] if idx >= 0 else ""
                outcome = SessionOutcomeRecord(
                    id=str(_uuid.uuid4()),
                    spec_name=spec_name,
                    task_group=task_group,
                    node_id=record.node_id,
                    touched_path=",".join(record.files_touched) if record.files_touched else "",
                    status=record.status,
                    input_tokens=record.input_tokens,
                    output_tokens=record.output_tokens,
                    duration_ms=record.duration_ms,
                    created_at=record.timestamp,
                    run_id=self._run_id,
                    attempt=record.attempt,
                    cost=record.cost,
                    model=record.model,
                    archetype=record.archetype,
                    commit_sha=record.commit_sha,
                    error_message=record.error_message,
                    is_transport_error=record.is_transport_error,
                )
                _record_session_db(self._knowledge_db_conn, outcome)
                _update_run_totals(
                    self._knowledge_db_conn,
                    self._run_id,
                    input_tokens=record.input_tokens,
                    output_tokens=record.output_tokens,
                    cost=record.cost,
                    is_workspace_setup_failure=record.is_workspace_setup_failure,
                )
            except Exception:
                logger.warning("Failed to record session to DB", exc_info=True)

        node_id = record.node_id
        self._get_node_state(node_id)

        if record.status == "completed":
            self._handle_success(record, state, error_tracker)
        elif record.status == "timeout":
            # 75-REQ-1.1, 75-REQ-1.3: Route timeout to dedicated handler
            self._handle_timeout(record, attempt, state, attempt_tracker, error_tracker)
        else:
            self._handle_failure(record, attempt, state, attempt_tracker, error_tracker)

        # 105-REQ-2.1: Persist node status per-transition to DB (not batch at end-of-run).
        if self._knowledge_db_conn is not None:
            try:
                from agentfox.engine.state import persist_node_status as _persist_status

                current_status = self._graph_sync.node_states.get(node_id, record.status)
                _persist_status(
                    self._knowledge_db_conn,
                    node_id,
                    current_status,
                    blocked_reason=state.blocked_reasons.get(node_id),
                )
            except Exception:
                logger.warning("Failed to persist node status to DB", exc_info=True)

    def _handle_success(
        self,
        record: SessionRecord,
        state: ExecutionState,
        error_tracker: dict[str, str | None],
    ) -> None:
        """Handle a successful session completion."""
        node_id = record.node_id

        ns = self._node_retry_states.get(node_id)
        if ns is not None:
            ns.workspace_failures = 0

        prev_status = self._graph_sync.node_states.get(node_id, "in_progress")
        self._graph_sync.mark_completed(node_id)

        # 40-REQ-9.4: Emit task.status_change on completion
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.TASK_STATUS_CHANGE,
            node_id=node_id,
            payload={
                "from_status": prev_status,
                "to_status": "completed",
                "reason": "session completed successfully",
            },
        )
        error_tracker.pop(node_id, None)

        # 18-REQ-5.4: Emit task completion event
        if self._task_callback is not None:
            duration_s = (record.duration_ms or 0) / 1000
            self._task_callback(
                TaskEvent(
                    node_id=node_id,
                    status="completed",
                    duration_s=duration_s,
                    archetype=self._get_node_archetype(node_id),
                )
            )

        # Reviewer blocking (pre-review / drift-review / audit-review)
        if self.check_review_blocking(record, state):
            self._check_block_budget(state)

    def _get_original_node_timeout(self, node_id: str) -> int:
        """Return the original session timeout for a node before any extension.

        On first call for a node, captures the current value (from per-node
        override dict or the global original_session_timeout). Subsequent
        calls return the stored original so the ceiling stays fixed.

        Requirements: 75-REQ-3.3, 75-REQ-3.E1
        """
        ns = self._get_node_state(node_id)
        if ns.original_timeout is None:
            ns.original_timeout = ns.timeout if ns.timeout is not None else self._original_session_timeout
        return ns.original_timeout

    def _extend_node_params(self, node_id: str) -> None:
        """Increase max_turns and session_timeout for the node by the multiplier.

        Applies ceiling clamping to session_timeout. Skips max_turns when it
        is None (unlimited). Changes are stored in per-node override dicts.

        Requirements: 75-REQ-3.1, 75-REQ-3.2, 75-REQ-3.3, 75-REQ-3.4,
                      75-REQ-3.5, 75-REQ-3.E1
        """
        ns = self._get_node_state(node_id)
        multiplier = self._timeout_multiplier
        ceiling_factor = self._timeout_ceiling_factor

        # Get original timeout (stored on first extension for stable ceiling)
        original_timeout = self._get_original_node_timeout(node_id)

        # Extend session_timeout, clamped to ceiling (75-REQ-3.2, 75-REQ-3.3)
        current_timeout = ns.timeout if ns.timeout is not None else original_timeout
        ceiling_timeout = math.ceil(original_timeout * ceiling_factor)
        new_timeout = min(
            math.ceil(current_timeout * multiplier),
            ceiling_timeout,
        )
        ns.timeout = new_timeout

        # Extend max_turns if finite (75-REQ-3.1, 75-REQ-3.4)
        if ns.has_max_turns and ns.max_turns is not None:
            ns.max_turns = math.ceil(ns.max_turns * multiplier)

    def _handle_timeout(
        self,
        record: SessionRecord,
        attempt: int,
        state: ExecutionState,
        attempt_tracker: dict[str, int],
        error_tracker: dict[str, str | None],
    ) -> None:
        """Handle a timeout failure: extend params and retry, or fall through.

        When timeout retries are available, increments the per-node timeout
        counter, extends session_timeout and max_turns, resets the node to
        pending, and emits a SESSION_TIMEOUT_RETRY audit event.

        When retries are exhausted, logs a warning and falls through to the
        normal escalation ladder via _handle_failure().

        Requirements: 75-REQ-1.1, 75-REQ-2.2, 75-REQ-2.3, 75-REQ-2.4,
                      75-REQ-5.1, 75-REQ-5.2, 75-REQ-5.3
        """
        node_id = record.node_id
        ns = self._get_node_state(node_id)
        current_retries = ns.timeout_retries

        if current_retries >= self._max_timeout_retries:
            logger.warning(
                "Timeout retries exhausted for %s (%d/%d), falling through to failure handler",
                node_id,
                current_retries,
                self._max_timeout_retries,
            )
            self._handle_failure(record, attempt, state, attempt_tracker, error_tracker)
            return

        # Capture original values before extending for audit payload (75-REQ-5.3)
        original_timeout = self._get_original_node_timeout(node_id)
        original_max_turns = ns.max_turns if ns.has_max_turns else None

        # Increment counter and extend parameters (75-REQ-2.2, 75-REQ-3.1, 75-REQ-3.2)
        ns.timeout_retries = current_retries + 1
        self._extend_node_params(node_id)

        extended_timeout = ns.timeout
        extended_max_turns = ns.max_turns if ns.has_max_turns else None

        # Reset to pending for retry at same tier (75-REQ-2.3, 535-AC-2)
        self._graph_sync.mark_pending(node_id, reason="timeout retry")

        # Emit SESSION_TIMEOUT_RETRY audit event (75-REQ-5.1, 75-REQ-5.3)
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.SESSION_TIMEOUT_RETRY,
            node_id=node_id,
            payload={
                "timeout_retry_count": current_retries + 1,
                "max_timeout_retries": self._max_timeout_retries,
                "original_max_turns": original_max_turns,
                "extended_max_turns": extended_max_turns,
                "original_timeout": original_timeout,
                "extended_timeout": extended_timeout,
            },
        )

    def _handle_non_retryable(
        self,
        record: SessionRecord,
        state: ExecutionState,
    ) -> None:
        """Handle a non-retryable workspace-state error by blocking immediately.

        118-REQ-3.2, 118-REQ-3.3: Non-retryable errors are blocked without
        consuming escalation ladder retries.
        """
        node_id = record.node_id
        logger.warning(
            "Non-retryable workspace-state error for %s, blocking immediately: %s",
            node_id,
            record.error_message,
        )
        self._block_task(
            node_id,
            state,
            f"workspace-state: {record.error_message}",
        )
        self._check_block_budget(state)

    def _handle_budget_exhausted(
        self,
        record: SessionRecord,
        state: ExecutionState,
    ) -> None:
        """Handle budget exhaustion by blocking without retry.

        The session did real work but the SDK terminated it when the
        max-budget-usd cap was reached.  Retrying would just burn the same
        budget again with no progress.
        """
        node_id = record.node_id
        logger.warning(
            "Budget exhausted for %s, blocking without retry: %s",
            node_id,
            record.error_message,
        )
        self._block_task(
            node_id,
            state,
            f"Budget exhausted for {node_id}: {record.error_message}",
        )
        self._check_block_budget(state)

    def _handle_transport_error(
        self,
        record: SessionRecord,
    ) -> None:
        """Handle a transport error by resetting to pending without consuming escalation.

        The ClaudeBackend already retried internally; this path is reached only
        when all transport retries were exhausted.  Reset the node to pending
        so the orchestrator re-dispatches it without touching the ladder.
        """
        node_id = record.node_id
        logger.warning(
            "Transport error for %s (not consuming escalation retry): %s",
            node_id,
            record.error_message,
        )
        self._graph_sync.mark_pending(node_id, reason="transport error retry")

    def is_workspace_backoff_active(self, node_id: str) -> bool:
        """Return True when the node is in workspace-error backoff."""
        ns = self._node_retry_states.get(node_id)
        if ns is None or ns.workspace_failures == 0:
            return False
        return time.monotonic() < ns.workspace_next_eligible

    def _handle_workspace_setup_failure(
        self,
        record: SessionRecord,
        state: ExecutionState,
    ) -> None:
        """Handle a workspace-setup failure with exponential backoff.

        Workspace-setup failures (worktree creation, branch checkout) are
        infrastructure errors that should not consume escalation retries.
        After ``_MAX_WORKSPACE_FAILURES`` consecutive failures for the same
        node, the node is blocked with a diagnostic message.
        """
        node_id = record.node_id
        ns = self._get_node_state(node_id)
        ns.workspace_failures += 1
        count = ns.workspace_failures

        if count >= _MAX_WORKSPACE_FAILURES:
            reason = (
                f"Workspace setup failed {count} times consecutively for {node_id}: "
                f"{record.error_message}. "
                f"Check for stale worktrees (.agent-fox/worktrees/) or lock contention."
            )
            logger.warning("Workspace circuit breaker tripped for %s: %s", node_id, reason)
            self._block_task(node_id, state, reason)
            self._check_block_budget(state)
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.WORKSPACE_SETUP_FAILED,
                node_id=node_id,
                payload={
                    "consecutive_failures": count,
                    "blocked": True,
                    "error": record.error_message,
                },
            )
            return

        delay = min(2**count, _MAX_WORKSPACE_BACKOFF_SECONDS)
        ns.workspace_next_eligible = time.monotonic() + delay

        logger.warning(
            "Workspace setup failed for %s (%d/%d), backing off %ds: %s",
            node_id,
            count,
            _MAX_WORKSPACE_FAILURES,
            delay,
            record.error_message,
        )
        self._graph_sync.mark_pending(node_id, reason="workspace setup retry with backoff")

        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.WORKSPACE_SETUP_FAILED,
            node_id=node_id,
            payload={
                "consecutive_failures": count,
                "blocked": False,
                "backoff_seconds": delay,
                "error": record.error_message,
            },
        )

    def _handle_failure(
        self,
        record: SessionRecord,
        attempt: int,
        state: ExecutionState,
        attempt_tracker: dict[str, int],
        error_tracker: dict[str, str | None],
    ) -> None:
        """Handle a failed session: retry or block."""
        node_id = record.node_id
        error_tracker[node_id] = record.error_message

        # Workspace-setup failures get exponential backoff, not retries.
        if record.is_workspace_setup_failure:
            self._handle_workspace_setup_failure(record, state)
            return

        # Non-retryable errors (workspace-state) are blocked immediately.
        if getattr(record, "is_non_retryable", False):
            self._handle_non_retryable(record, state)
            return

        # Budget exhaustion is not retryable.
        if getattr(record, "is_budget_exhausted", False):
            self._handle_budget_exhausted(record, state)
            return

        # Transport errors are retried without counting.
        if getattr(record, "is_transport_error", False):
            self._handle_transport_error(record)
            return

        # 26-REQ-9.3: Retry-predecessor for archetypes with the flag
        node_archetype = self._get_node_archetype(node_id)
        node_mode = self._get_node_mode(node_id)
        archetype_entry = get_archetype(node_archetype)
        if node_mode is not None:
            from agentfox.archetypes import resolve_effective_config

            archetype_entry = resolve_effective_config(archetype_entry, node_mode)

        count = self._node_failure_counts.get(node_id, 0) + 1
        self._node_failure_counts[node_id] = count
        can_retry = count <= self._max_retries
        exhausted = not can_retry

        # Retry-predecessor: reset predecessor instead of failed node
        if archetype_entry.retry_predecessor and can_retry:
            if self._try_retry_predecessor(node_id, record, attempt, state, error_tracker):
                return

        if exhausted:
            self._handle_exhausted(node_id, record, state, attempt_tracker)
        else:
            self._handle_retry(node_id, record, attempt)

    def _try_retry_predecessor(
        self,
        node_id: str,
        record: SessionRecord,
        attempt: int,
        state: ExecutionState,
        error_tracker: dict[str, str | None],
    ) -> bool:
        """Attempt retry-predecessor logic. Returns True if handled."""
        predecessors = self._get_predecessors(node_id)
        if not predecessors:
            return False

        pred_id = predecessors[0]

        pred_count = self._node_failure_counts.get(pred_id, 0) + 1
        self._node_failure_counts[pred_id] = pred_count

        if pred_count > self._max_retries:
            self._block_task(
                pred_id,
                state,
                f"Predecessor {pred_id} exhausted retries after reviewer {node_id} failures",
            )
            self._check_block_budget(state)
            return True

        logger.info(
            "Retry-predecessor: resetting %s to pending due to %s failure (attempt %d)",
            pred_id,
            node_id,
            attempt,
        )
        if self._task_callback is not None:
            self._task_callback(
                TaskEvent(
                    node_id=node_id,
                    status="disagreed",
                    duration_s=0,
                    archetype=self._get_node_archetype(node_id),
                    predecessor_node=pred_id,
                )
            )
        self._graph_sync._transition(pred_id, "pending", reason="retry predecessor")
        error_tracker[pred_id] = record.error_message
        self._graph_sync.mark_pending(node_id, reason="retry predecessor reset")
        return True

    def _handle_exhausted(
        self,
        node_id: str,
        record: SessionRecord,
        state: ExecutionState,
        attempt_tracker: dict[str, int],
    ) -> None:
        """Handle a node that has exhausted all retries."""
        # 18-REQ-5.4: Emit task failure event
        if self._task_callback is not None:
            duration_s = (record.duration_ms or 0) / 1000
            self._task_callback(
                TaskEvent(
                    node_id=node_id,
                    status="failed",
                    duration_s=duration_s,
                    error_message=record.error_message,
                    archetype=self._get_node_archetype(node_id),
                )
            )
        self._block_task(
            node_id,
            state,
            f"Retries exhausted for {node_id}: {record.error_message}",
        )
        self._check_block_budget(state)

    def _handle_retry(
        self,
        node_id: str,
        record: SessionRecord,
        attempt: int,
    ) -> None:
        """Handle a retry at the same model tier."""
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.SESSION_RETRY,
            node_id=node_id,
            payload={
                "attempt": attempt,
                "reason": record.error_message or "retrying after failure",
            },
        )
        if self._task_callback is not None:
            self._task_callback(
                TaskEvent(
                    node_id=node_id,
                    status="retry",
                    duration_s=0,
                    archetype=self._get_node_archetype(node_id),
                    attempt=attempt + 1,
                )
            )
        self._graph_sync.mark_pending(node_id, reason="retry after failure")


# ---------------------------------------------------------------------------
# Test coverage measurement (inlined from engine/coverage.py)
# ---------------------------------------------------------------------------

_COVERAGE_TIMEOUT = 600


@dataclass(frozen=True)
class CoverageTool:
    name: str
    command: list[str]
    result_path: str


@dataclass(frozen=True)
class FileCoverage:
    file_path: str
    covered_lines: int
    total_lines: int

    @property
    def percentage(self) -> float:
        if self.total_lines == 0:
            return 100.0
        return (self.covered_lines / self.total_lines) * 100.0


@dataclass(frozen=True)
class CoverageResult:
    files: dict[str, FileCoverage]

    def coverage_for(self, path: str) -> FileCoverage | None:
        return self.files.get(path)

    def to_json(self) -> str:
        return json.dumps(
            {
                path: {
                    "covered_lines": fc.covered_lines,
                    "total_lines": fc.total_lines,
                }
                for path, fc in self.files.items()
            }
        )

    @staticmethod
    def from_json(raw: str) -> CoverageResult | None:
        if not raw:
            return None
        try:
            data = json.loads(raw)
        except (json.JSONDecodeError, TypeError):
            return None
        files: dict[str, FileCoverage] = {}
        for path, info in data.items():
            files[path] = FileCoverage(
                file_path=path,
                covered_lines=info.get("covered_lines", 0),
                total_lines=info.get("total_lines", 0),
            )
        return CoverageResult(files=files)


@dataclass(frozen=True)
class CoverageRegression:
    file_path: str
    baseline_pct: float
    current_pct: float
    delta: float


def detect_coverage_tool(project_root: Path) -> CoverageTool | None:
    pyproject = project_root / "pyproject.toml"
    if pyproject.exists():
        try:
            data = tomllib.loads(pyproject.read_text(encoding="utf-8"))
            tool = data.get("tool", {})
            if "pytest" in tool:
                return CoverageTool(
                    name="pytest-cov",
                    command=[
                        "uv",
                        "run",
                        "pytest",
                        "--cov",
                        "--cov-report=json",
                        "-q",
                        "--no-header",
                        "-x",
                    ],
                    result_path="coverage.json",
                )
        except (tomllib.TOMLDecodeError, OSError):
            pass

    cargo = project_root / "Cargo.toml"
    if cargo.exists():
        try:
            data = tomllib.loads(cargo.read_text(encoding="utf-8"))
            if "package" in data:
                return CoverageTool(
                    name="cargo-tarpaulin",
                    command=["cargo", "tarpaulin", "--out", "json", "--output-dir", "."],
                    result_path="tarpaulin-report.json",
                )
        except (tomllib.TOMLDecodeError, OSError):
            pass

    go_mod = project_root / "go.mod"
    if go_mod.exists():
        return CoverageTool(
            name="go-cover",
            command=["go", "test", "-coverprofile=coverage.out", "-covermode=count", "./..."],
            result_path="coverage.out",
        )

    return None


def measure_coverage(project_root: Path, tool: CoverageTool) -> CoverageResult | None:
    try:
        subprocess.run(
            tool.command,
            cwd=project_root,
            capture_output=True,
            text=True,
            timeout=_COVERAGE_TIMEOUT,
        )
    except subprocess.TimeoutExpired:
        logger.warning("Coverage measurement timed out after %ds", _COVERAGE_TIMEOUT)
        return None
    except Exception:
        logger.debug("Coverage measurement failed", exc_info=True)
        return None

    result_path = project_root / tool.result_path
    if not result_path.exists():
        logger.debug("Coverage result file not found: %s", result_path)
        return None

    try:
        if tool.name == "pytest-cov":
            return _parse_pytest_cov(result_path)
        if tool.name == "cargo-tarpaulin":
            return _parse_tarpaulin(result_path)
        if tool.name == "go-cover":
            return _parse_go_cover(result_path)
    except Exception:
        logger.debug("Failed to parse coverage output from %s", tool.name, exc_info=True)
    finally:
        try:
            result_path.unlink(missing_ok=True)
        except Exception:
            pass

    return None


def _parse_pytest_cov(result_path: Path) -> CoverageResult:
    data = json.loads(result_path.read_text())
    files: dict[str, FileCoverage] = {}
    for file_path, info in data.get("files", {}).items():
        rel_path = file_path
        if Path(file_path).is_absolute():
            try:
                rel_path = str(Path(file_path).relative_to(result_path.parent))
            except ValueError:
                pass
        summary = info.get("summary", {})
        files[rel_path] = FileCoverage(
            file_path=rel_path,
            covered_lines=summary.get("covered_lines", 0),
            total_lines=summary.get("num_statements", 0),
        )
    return CoverageResult(files=files)


def _parse_tarpaulin(result_path: Path) -> CoverageResult:
    data = json.loads(result_path.read_text())
    files: dict[str, FileCoverage] = {}
    for entry in data.get("files", []):
        path = entry.get("path", "")
        traces = entry.get("traces", [])
        total = len(traces)
        covered = sum(1 for t in traces if t.get("stats", {}).get("Line", 0) > 0)
        files[path] = FileCoverage(
            file_path=path,
            covered_lines=covered,
            total_lines=total,
        )
    return CoverageResult(files=files)


def _parse_go_cover(result_path: Path) -> CoverageResult:
    content = result_path.read_text()
    file_stats: dict[str, tuple[int, int]] = {}
    for line in content.splitlines():
        if line.startswith("mode:"):
            continue
        match = re.match(r"^(.+?):(\d+)\.\d+,(\d+)\.\d+\s+(\d+)\s+(\d+)$", line)
        if match:
            path = match.group(1)
            num_statements = int(match.group(4))
            count = int(match.group(5))
            prev_c, prev_t = file_stats.get(path, (0, 0))
            file_stats[path] = (
                prev_c + (num_statements if count > 0 else 0),
                prev_t + num_statements,
            )
    files = {path: FileCoverage(file_path=path, covered_lines=c, total_lines=t) for path, (c, t) in file_stats.items()}
    return CoverageResult(files=files)


def find_regressions(
    baseline: CoverageResult,
    current: CoverageResult,
    modified_files: list[str],
) -> list[CoverageRegression]:
    regressions: list[CoverageRegression] = []
    for file_path in modified_files:
        base = baseline.coverage_for(file_path)
        curr = current.coverage_for(file_path)
        if base is None or curr is None:
            continue
        if base.total_lines == 0:
            continue
        delta = curr.percentage - base.percentage
        if delta < 0:
            regressions.append(
                CoverageRegression(
                    file_path=file_path,
                    baseline_pct=base.percentage,
                    current_pct=curr.percentage,
                    delta=delta,
                )
            )
    return regressions
