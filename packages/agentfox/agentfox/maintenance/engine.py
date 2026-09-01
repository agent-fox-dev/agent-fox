"""Night Shift engine: business logic for the fix pipeline.

Provides the core operations that work streams delegate to.  Lifecycle
management (scheduling, signals, budget) is handled by ``DaemonRunner``.

Requirements: 61-REQ-1.1, 61-REQ-1.E1, 61-REQ-9.3
"""

from __future__ import annotations

import asyncio
import logging
import sys
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING

from afaudit.emit import emit_audit_event as _emit_audit_event
from afaudit.events import AuditEventType, generate_run_id
from afissues.labels import LABEL_FIX, LABEL_FIXED, LABEL_PR

from agentfox.core.config import AgentFoxConfig
from agentfox.maintenance.dep_graph import build_graph, build_parallel_graph, merge_edges
from agentfox.maintenance.fix_pipeline import FixPipeline
from agentfox.maintenance.pr_feedback import process_pr_issue
from agentfox.maintenance.reference_parser import (
    fetch_github_relationships,
    parse_text_references,
)
from agentfox.maintenance.staleness import check_staleness
from agentfox.maintenance.triage import run_batch_triage
from agentfox.ui.progress import ActivityCallback, SpinnerCallback, TaskCallback

if TYPE_CHECKING:
    import duckdb
    from afaudit.sink import SinkDispatcher

    from agentfox.knowledge.fox_provider import KnowledgeProvider

logger = logging.getLogger(__name__)

# Maximum number of af:pr issues processed per poll cycle.
# Issues beyond this cap are deferred to the next cycle.
# Requirements: 07-REQ-3.3
_MAX_PR_CHECKS: int = 5


@dataclass(frozen=True)
class IssueOutcome:
    """Per-issue result captured during a nightshift run."""

    issue_number: int
    title: str
    run_id: str
    outcome: str
    duration_ms: int
    cost_usd: float
    sessions_run: int
    input_tokens: int
    output_tokens: int


@dataclass
class NightShiftState:
    """Runtime state for the daemon.

    Mutable -- fields are updated during the daemon lifecycle.
    Protected by an asyncio.Lock for safe concurrent access from
    parallel issue processing tasks.
    """

    total_cost: float = 0.0
    total_sessions: int = 0
    issues_created: int = 0
    issues_fixed: int = 0
    issue_checks_completed: int = 0
    is_shutting_down: bool = False
    issue_outcomes: list[IssueOutcome] = field(default_factory=list)
    _lock: asyncio.Lock = field(default_factory=asyncio.Lock, repr=False)

    async def add_fix_result(
        self,
        cost: float,
        sessions: int,
        outcome: IssueOutcome,
        *,
        succeeded: bool,
    ) -> None:
        """Thread-safe update of counters after a fix completes."""
        async with self._lock:
            self.total_sessions += sessions
            self.total_cost += cost
            if succeeded:
                self.issues_fixed += 1
            self.issue_outcomes.append(outcome)


def validate_night_shift_prerequisites(config: AgentFoxConfig) -> None:
    """Validate that the platform is configured for night-shift.

    Aborts with exit code 1 if the platform type is 'none' or missing.

    Requirements: 61-REQ-1.E1
    """
    platform_type = getattr(getattr(config, "platform", None), "type", "none")
    if platform_type == "none":
        logger.error("Night-shift requires a configured platform. Set [platform] type = 'github' in your config.")
        sys.exit(1)


class NightShiftEngine:
    """Main daemon engine for night-shift.

    Coordinates issue checks and fix sessions on a timed schedule.

    Requirements: 61-REQ-1.1, 61-REQ-1.3, 61-REQ-1.4, 61-REQ-1.E2
    """

    # Maximum drain iterations to prevent infinite loops if issues are
    # created faster than they are fixed.
    _MAX_DRAIN_ITERATIONS: int = 50

    def __init__(
        self,
        config: AgentFoxConfig,
        platform: object,
        *,
        activity_callback: ActivityCallback | None = None,
        task_callback: TaskCallback | None = None,
        status_callback: Callable[[str, str], None] | None = None,
        spinner_callback: SpinnerCallback | None = None,
        sink_dispatcher: SinkDispatcher | None = None,
        conn: duckdb.DuckDBPyConnection | None = None,
        knowledge_provider: KnowledgeProvider | None = None,
    ) -> None:
        self._config = config
        self._platform = platform
        self._activity_callback = activity_callback
        self._task_callback = task_callback
        self._status_callback = status_callback
        self._spinner_callback = spinner_callback
        self._sink = sink_dispatcher
        self._conn = conn
        self._knowledge_provider = knowledge_provider
        self.state = NightShiftState()
        # Track issue numbers processed in this run to guard against
        # re-processing issues that were closed/fixed but still returned
        # by the platform API due to eventual consistency (issue #465).
        self._processed_issues: set[int] = set()
        self._processed_issues_lock = asyncio.Lock()
        # Issues currently being processed in parallel — excluded from
        # staleness evaluation by sibling fixes.
        self._in_flight: set[int] = set()
        self._in_flight_lock = asyncio.Lock()

    def _check_cost_limit(self) -> bool:
        """Check whether the cost limit has been reached.

        Returns True when the remaining budget is less than 50% of
        max_cost.  This conservative threshold prevents overspending
        when individual operations may cost a significant fraction of
        the total budget.

        Requirements: 61-REQ-1.E2, 61-REQ-9.3
        """
        max_cost = getattr(getattr(self._config, "orchestrator", None), "max_cost", None)
        if max_cost is None:
            return False
        remaining = max_cost - self.state.total_cost
        return remaining < max_cost * 0.5

    def _check_session_limit(self) -> bool:
        """Check whether the session limit has been reached.

        Returns True when total_sessions >= max_sessions.

        Requirements: 61-REQ-9.3
        """
        max_sessions = getattr(getattr(self._config, "orchestrator", None), "max_sessions", None)
        if not isinstance(max_sessions, (int, float)):
            return False
        return self.state.total_sessions >= max_sessions

    def _emit_status(self, text: str, style: str = "bold cyan") -> None:
        """Emit a permanent status line via the status_callback.

        If no callback is set, this is a no-op.

        Requirements: 81-REQ-3.1, 81-REQ-3.2, 81-REQ-3.3, 81-REQ-3.4, 81-REQ-3.5
        """
        if self._status_callback is not None:
            try:
                self._status_callback(text, style)
            except Exception:
                logger.debug("Status callback failed", exc_info=True)

    async def _run_issue_check(self, _seen: set[int] | None = None) -> None:
        """Poll platform for af:fix issues and process them.

        Issues are fetched sorted by creation date ascending (oldest first).
        A local sort by issue number is applied as a fallback in case the
        platform ignores the sort parameters (71-REQ-1.E1).

        Triage phase: for batches >= 3, runs AI batch triage to detect
        dependencies and supersession candidates (71-REQ-3.1).

        Staleness phase: after each successful fix, evaluates remaining
        issues for obsolescence (71-REQ-5.1).

        Args:
            _seen: Optional set of issue numbers already processed in this
                   drain session.  Issues present in this set are skipped so
                   that a recently closed issue cannot be re-processed due to
                   platform API propagation delays between drain iterations
                   (fixes #465).

        Requirements: 61-REQ-2.1, 71-REQ-1.1, 71-REQ-1.2, 71-REQ-1.E1,
                      71-REQ-3.1, 71-REQ-3.5, 71-REQ-5.1, 71-REQ-5.E3
        """
        # Use the caller-supplied set so processed issues are remembered
        # across drain iterations; fall back to a local set for direct calls.
        seen: set[int] = _seen if _seen is not None else set()
        self._emit_status("Checking for af:fix issues\u2026")
        try:
            issues = await self._platform.list_issues_by_label(  # type: ignore[attr-defined]
                LABEL_FIX,
                sort="created",
                direction="asc",
            )
        except Exception:
            logger.warning(
                "Issue check failed due to platform API error",
                exc_info=True,
            )
            return

        if not issues:
            self.state.issue_checks_completed += 1
            return

        # Local sort fallback: ensure ascending issue number order
        # even if the platform does not honour the sort parameters (71-REQ-1.E1).
        issues = sorted(issues, key=lambda i: i.number)

        # Skip issues already processed in this drain session or prior runs.
        # The platform API may return recently-closed issues due to eventual
        # consistency (issue #465).  The ``seen`` set covers superseded and
        # staleness-closed issues within the current drain; the instance-level
        # ``_processed_issues`` set covers issues handled in earlier runs.
        issues = [i for i in issues if i.number not in seen and i.number not in self._processed_issues]
        if not issues:
            self.state.issue_checks_completed += 1
            return

        # Build dependency graph from explicit references and GitHub metadata
        explicit_edges = parse_text_references(issues)
        try:
            github_edges = await fetch_github_relationships(self._platform, issues)
        except Exception:
            logger.warning(
                "Failed to fetch GitHub relationships, continuing without",
                exc_info=True,
            )
            github_edges = []

        all_edges = explicit_edges + github_edges

        # AI triage for batches >= 3 (71-REQ-3.1, 71-REQ-3.5)
        issue_check_run_id = generate_run_id()
        supersession_pairs: list[tuple[int, int]] = []
        if len(issues) >= 3:
            try:
                triage = await run_batch_triage(
                    issues, all_edges, self._config, sink=self._sink, run_id=issue_check_run_id
                )
                all_edges = merge_edges(all_edges, triage.edges)
                supersession_pairs = triage.supersession_pairs
            except Exception:
                logger.warning(
                    "AI triage failed, using explicit refs only",
                    exc_info=True,
                )

        # Compute processing order via topological sort (used for logging)
        processing_order = build_graph(issues, all_edges)
        logger.info("Resolved processing order: %s", processing_order)

        issue_map = {i.number: i for i in issues}
        closed: set[int] = set()

        # Close AI-identified superseded issues before processing (71-REQ-3.5)
        for _keep, obsolete in supersession_pairs:
            if obsolete not in issue_map or obsolete in closed:
                continue
            try:
                await self._platform.close_issue(  # type: ignore[attr-defined]
                    obsolete,
                    f"Superseded by #{_keep} (AI triage).",
                )
                closed.add(obsolete)
                seen.add(obsolete)
                _emit_audit_event(
                    self._sink,
                    issue_check_run_id,
                    AuditEventType.ISSUE_SUPERSEDED,
                    payload={"closed_issue": obsolete, "superseded_by": _keep},
                )
                try:
                    await self._platform.assign_label(  # type: ignore[attr-defined]
                        obsolete,
                        LABEL_FIXED,
                    )
                except Exception:
                    logger.warning(
                        "Failed to assign af:fixed label to superseded issue #%d",
                        obsolete,
                        exc_info=True,
                    )
            except Exception:
                logger.warning(
                    "Failed to close superseded issue #%d",
                    obsolete,
                    exc_info=True,
                )

        # Parallel dispatch with dependency-aware scheduling
        _mp = getattr(
            getattr(self._config, "night_shift", None),
            "max_parallel",
            1,
        )
        max_parallel = _mp if isinstance(_mp, int) and _mp >= 1 else 1
        await self._dispatch_parallel(
            issues=issues,
            all_edges=all_edges,
            issue_map=issue_map,
            processing_order=processing_order,
            closed=closed,
            seen=seen,
            max_parallel=max_parallel,
            issue_check_run_id=issue_check_run_id,
        )

        self.state.issue_checks_completed += 1

    async def _dispatch_parallel(
        self,
        *,
        issues: list[object],
        all_edges: list[object],
        issue_map: dict[int, object],
        processing_order: list[int],
        closed: set[int],
        seen: set[int],
        max_parallel: int,
        issue_check_run_id: str,
    ) -> None:
        """Dependency-aware parallel issue dispatcher.

        Launches up to ``max_parallel`` concurrent ``_process_fix`` tasks.
        When a task completes, the dependency graph is updated and newly-
        ready issues are dispatched to fill the pool.

        With ``max_parallel=1`` this behaves identically to the previous
        serial loop — issues are processed one at a time in topological order.
        """
        graph = build_parallel_graph(issues, all_edges)  # type: ignore[arg-type]

        # Remove closed (superseded) issues from the graph
        for c in closed:
            graph.complete(c)

        pool: set[asyncio.Task[tuple[int, bool]]] = set()
        dispatched: set[int] = set()

        async def _run_one(issue_num: int) -> tuple[int, bool]:
            """Process a single issue and return (issue_num, succeeded)."""
            issue = issue_map[issue_num]
            fix_succeeded = False
            try:
                async with self._in_flight_lock:
                    self._in_flight.add(issue_num)
                try:
                    await self._process_fix(issue)
                    fix_succeeded = True
                except Exception:
                    logger.warning(
                        "Fix failed for issue #%d, continuing to next",
                        issue_num,
                        exc_info=True,
                    )
                finally:
                    async with self._in_flight_lock:
                        self._in_flight.discard(issue_num)
                    async with self._processed_issues_lock:
                        self._processed_issues.add(issue_num)
            except Exception:
                logger.warning(
                    "Unexpected error processing issue #%d",
                    issue_num,
                    exc_info=True,
                )
            return (issue_num, fix_succeeded)

        def _fill_pool() -> None:
            """Add ready issues to the pool up to max_parallel."""
            if self.state.is_shutting_down:
                return
            ready = graph.ready_issues()
            for issue_num in ready:
                if len(pool) >= max_parallel:
                    break
                if issue_num in dispatched or issue_num in closed:
                    continue
                if self.state.is_shutting_down:
                    break
                if self._check_cost_limit():
                    logger.info("Cost limit reached, stopping issue dispatch")
                    return
                if self._check_session_limit():
                    logger.info("Session limit reached, stopping issue dispatch")
                    return
                dispatched.add(issue_num)
                task = asyncio.create_task(
                    _run_one(issue_num),
                    name=f"fix-issue-{issue_num}",
                )
                pool.add(task)

        _fill_pool()

        while pool:
            done, pool = await asyncio.wait(pool, return_when=asyncio.FIRST_COMPLETED)
            for task in done:
                issue_num, fix_succeeded = task.result()

                # Update graph: mark complete and find newly-ready issues
                graph.complete(issue_num)

                # Post-fix staleness check (71-REQ-5.1, 71-REQ-5.E3)
                if fix_succeeded:
                    seen.add(issue_num)
                    remaining = [
                        issue_map[n]
                        for n in processing_order
                        if n != issue_num and n not in closed and n not in dispatched
                    ]
                    if remaining:
                        try:
                            async with self._in_flight_lock:
                                current_in_flight = set(self._in_flight)
                            staleness = await check_staleness(
                                issue_map[issue_num],
                                remaining,
                                "",  # diff not available in current implementation
                                self._config,
                                self._platform,
                                sink=self._sink,
                                run_id=issue_check_run_id,
                                in_flight=current_in_flight,
                            )
                            remaining_nums = {i.number for i in remaining}
                            for obsolete_num in staleness.obsolete_issues:
                                if obsolete_num not in remaining_nums:
                                    continue
                                await self._platform.close_issue(  # type: ignore[attr-defined]
                                    obsolete_num,
                                    f"Resolved by fix for #{issue_num}",
                                )
                                closed.add(obsolete_num)
                                seen.add(obsolete_num)
                                graph.complete(obsolete_num)
                                _emit_audit_event(
                                    self._sink,
                                    issue_check_run_id,
                                    AuditEventType.ISSUE_OBSOLETE,
                                    payload={
                                        "closed_issue": obsolete_num,
                                        "fixed_by": issue_num,
                                        "rationale": staleness.rationale.get(obsolete_num, ""),
                                    },
                                )
                                try:
                                    await self._platform.assign_label(  # type: ignore[attr-defined]
                                        obsolete_num,
                                        LABEL_FIXED,
                                    )
                                except Exception:
                                    logger.warning(
                                        "Failed to assign af:fixed label to obsolete issue #%d",
                                        obsolete_num,
                                        exc_info=True,
                                    )
                        except Exception:
                            logger.warning(
                                "Staleness check failed after fix #%d",
                                issue_num,
                                exc_info=True,
                            )

            # Fill pool with newly-ready issues
            _fill_pool()

    def _calculate_fix_cost(self, metrics: object) -> float:
        """Calculate USD cost from FixMetrics token counts."""
        from agentfox.core.config import PricingConfig
        from agentfox.core.models import calculate_cost, resolve_model

        model_id = resolve_model("ADVANCED")
        pricing = getattr(self._config, "pricing", PricingConfig())
        return calculate_cost(
            getattr(metrics, "input_tokens", 0),
            getattr(metrics, "output_tokens", 0),
            model_id,
            pricing,
            cache_read_input_tokens=getattr(metrics, "cache_read_input_tokens", 0),
            cache_creation_input_tokens=getattr(metrics, "cache_creation_input_tokens", 0),
        )

    async def _process_fix(self, issue: object, issue_body: str = "") -> None:
        """Process a single af:fix issue through the fix pipeline.

        Builds an in-memory spec from the issue, runs the full archetype
        pipeline, harvests the branch, and updates the engine state
        including cost and session counters.

        Requirements: 61-REQ-6.1, 61-REQ-6.2, 61-REQ-6.3, 61-REQ-6.4,
                      61-REQ-9.3
        """
        from afissues.protocol import IssueResult

        if not isinstance(issue, IssueResult):
            return

        import time

        fix_run_id = generate_run_id()
        fix_start = time.monotonic()
        self._emit_status(f"Fixing issue #{issue.number}: {issue.title}")

        _emit_audit_event(
            self._sink,
            fix_run_id,
            AuditEventType.FIX_START,
            payload={"issue_number": issue.number, "title": issue.title},
        )

        pipeline = FixPipeline(
            config=self._config,
            platform=self._platform,
            activity_callback=self._activity_callback,
            task_callback=self._task_callback,
            sink_dispatcher=self._sink,
            spinner_callback=self._spinner_callback,
            conn=self._conn,
            knowledge_provider=self._knowledge_provider,
        )

        effective_body = issue_body if issue_body else getattr(issue, "body", "")
        try:
            metrics = await pipeline.process_issue(issue, issue_body=effective_body, run_id=fix_run_id)
            cost = self._calculate_fix_cost(metrics)

            from agentfox.ui.progress import format_duration

            duration_ms = int((time.monotonic() - fix_start) * 1000)
            duration_str = format_duration(duration_ms / 1000)
            self._emit_status(
                f"\u2714 Issue #{issue.number} fixed ({duration_str})",
                "bold green",
            )

            outcome = IssueOutcome(
                issue_number=issue.number,
                title=issue.title,
                run_id=fix_run_id,
                outcome="fixed",
                duration_ms=duration_ms,
                cost_usd=cost,
                sessions_run=getattr(metrics, "sessions_run", 0),
                input_tokens=getattr(metrics, "input_tokens", 0),
                output_tokens=getattr(metrics, "output_tokens", 0),
            )
            await self.state.add_fix_result(
                cost,
                getattr(metrics, "sessions_run", 0),
                outcome,
                succeeded=True,
            )

            _emit_audit_event(
                self._sink,
                fix_run_id,
                AuditEventType.FIX_COMPLETE,
                payload={"issue_number": issue.number},
            )
        except Exception:
            from agentfox.ui.progress import format_duration

            duration_ms = int((time.monotonic() - fix_start) * 1000)
            duration_str = format_duration(duration_ms / 1000)
            self._emit_status(
                f"\u2718 Issue #{issue.number} failed ({duration_str})",
                "bold red",
            )

            outcome = IssueOutcome(
                issue_number=issue.number,
                title=issue.title,
                run_id=fix_run_id,
                outcome="failed",
                duration_ms=duration_ms,
                cost_usd=0.0,
                sessions_run=0,
                input_tokens=0,
                output_tokens=0,
            )
            await self.state.add_fix_result(0.0, 0, outcome, succeeded=False)

            logger.warning(
                "Fix pipeline raised unexpectedly for issue #%d",
                issue.number,
                exc_info=True,
            )
            _emit_audit_event(
                self._sink,
                fix_run_id,
                AuditEventType.FIX_FAILED,
                payload={"issue_number": issue.number},
            )

    async def _drain_issues(self) -> bool:
        """Run issue checks until no open af:fix issues remain.

        Loops calling ``_run_issue_check`` and re-polling the platform until
        zero ``af:fix`` issues are reported.  Respects shutdown, cost, and
        session limits between iterations, and enforces a safety-valve
        maximum iteration count to prevent infinite loops.

        A ``seen`` set is threaded through all iterations so that issues
        already processed (fixed, superseded, or staleness-closed) are never
        re-processed even if the platform still returns them due to API
        propagation delays (fixes #465).

        Returns True when no ``af:fix`` issues remain (drain succeeded),
        False when issues may still exist (limit hit, shutdown, or error).

        Requirements: 81-REQ-1.1, 81-REQ-1.4
        """
        seen: set[int] = set()
        for _ in range(self._MAX_DRAIN_ITERATIONS):
            if self.state.is_shutting_down:
                return False
            if self._check_cost_limit():
                logger.info("Cost limit reached during issue drain")
                return False
            if self._check_session_limit():
                logger.info("Session limit reached during issue drain")
                return False

            await self._run_issue_check(seen)

            # Re-poll to see if any af:fix issues remain.
            # Filter already-processed issues from the re-poll result so that
            # recently-closed issues returned by the platform due to eventual
            # consistency do not cause spurious additional drain iterations
            # (issue #465).
            try:
                remaining = await self._platform.list_issues_by_label(  # type: ignore[attr-defined]
                    LABEL_FIX,
                    sort="created",
                    direction="asc",
                )
            except Exception:
                logger.warning(
                    "Failed to re-poll issues during drain",
                    exc_info=True,
                )
                # Fail-open: if we can't check, assume clear (81-REQ-1.E1)
                return True

            # Filter out issues already handled this session so that a
            # recently-closed issue returned by a stale platform response
            # does not trigger another fix iteration.
            remaining = [r for r in remaining if r.number not in seen and r.number not in self._processed_issues]
            if not remaining:
                return True

        logger.warning("Issue drain safety valve reached after %d iterations", self._MAX_DRAIN_ITERATIONS)
        return False

    async def _check_open_prs(self) -> None:
        """Poll for open af:pr issues and process each sequentially.

        Lists issues labelled ``af:pr`` in oldest-first order, caps the
        batch to ``_MAX_PR_CHECKS``, and awaits ``process_pr_issue()``
        for each one.  ``issue_checks_completed`` is incremented after
        each successful call.

        Requirements: 07-REQ-3.1, 07-REQ-3.2, 07-REQ-3.E1, 07-REQ-3.E2
        """
        issues = await self._platform.list_issues_by_label(LABEL_PR)  # type: ignore[attr-defined]
        issues = issues[:_MAX_PR_CHECKS]

        if not issues:
            return

        pipeline = FixPipeline(
            config=self._config,
            platform=self._platform,
            activity_callback=self._activity_callback,
            task_callback=self._task_callback,
            sink_dispatcher=self._sink,
            spinner_callback=self._spinner_callback,
            conn=self._conn,
            knowledge_provider=self._knowledge_provider,
        )

        for issue in issues:
            await process_pr_issue(
                issue,
                config=self._config,
                platform=self._platform,
                pipeline=pipeline,
            )
            self.state.issue_checks_completed += 1
