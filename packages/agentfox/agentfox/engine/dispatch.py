"""Dispatch strategies: serial and parallel task execution.

Extracted from engine.py to isolate dispatch mechanics from orchestration
control flow. Each dispatcher manages the loop of preparing, launching,
and processing sessions for ready tasks.

DispatchManager is the top-level collaborator that owns runners,
dispatchers, and launch preparation.

Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-2.1
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Callable
from pathlib import Path
from typing import Any

from afaudit.emit import emit_audit_event
from afaudit.events import AuditEventType

from agentfox.engine.graph_sync import _is_auto_pre
from agentfox.engine.preflight import PreflightVerdict, run_preflight
from agentfox.engine.session_lifecycle import _REVIEW_ARCHETYPES
from agentfox.engine.state import SessionRecord
from agentfox.graph.types import get_node_archetype as _get_node_archetype
from agentfox.graph.types import get_node_mode as _get_node_mode
from agentfox.ui.progress import TaskCallback, TaskEvent

logger = logging.getLogger(__name__)


def _resolve_overrides(
    result_handler: Any | None,
    node_id: str,
) -> tuple[int | None, int | None]:
    """Extract timeout and max_turns overrides from the result handler."""
    if result_handler is None:
        return None, None
    timeout = result_handler.get_timeout_override(node_id)
    has_mt, mt_val = result_handler.get_max_turns_override(node_id)
    return timeout, mt_val if has_mt else None


# ---------------------------------------------------------------------------
# Known build-artifact patterns for pre-session workspace auto-remediation
# ---------------------------------------------------------------------------

# Directory names that indicate build/test artifact directories.
# Any file path component matching one of these names is considered
# a known build artifact that can be auto-cleaned before dispatch.
_KNOWN_ARTIFACT_DIRS: frozenset[str] = frozenset(
    {
        "__pycache__",
        ".pytest_cache",
        ".mypy_cache",
        ".ruff_cache",
        ".hypothesis",
    }
)

# File-name suffixes that identify known build/test artifacts.
_KNOWN_ARTIFACT_SUFFIXES: tuple[str, ...] = (
    ".proptest-regressions",
    ".pyc",
    ".pyo",
)


def _is_known_build_artifact(path: str) -> bool:
    """Return True when *path* looks like a known build or test artifact.

    Checks:
    - Any path component is a known artifact directory name.
    - The filename ends with a known artifact suffix.

    Used by :meth:`DispatchManager.prepare_launch` to decide whether to
    attempt auto-remediation instead of immediately blocking the node.

    Requirements: 571-AC-1
    """
    parts = Path(path).parts
    if any(p in _KNOWN_ARTIFACT_DIRS for p in parts):
        return True
    name = parts[-1] if parts else path
    return any(name.endswith(s) for s in _KNOWN_ARTIFACT_SUFFIXES)


# ---------------------------------------------------------------------------
# SerialRunner — executes one session at a time
# ---------------------------------------------------------------------------


class SerialRunner:
    """Runs tasks one at a time with inter-session delay."""

    def __init__(
        self,
        session_runner_factory: Callable[..., Any],
        inter_session_delay: float,
    ) -> None:
        self._session_runner_factory = session_runner_factory
        self._inter_session_delay = inter_session_delay

    async def execute(
        self,
        node_id: str,
        attempt: int,
        previous_error: str | None,
        *,
        archetype: str = "coder",
        mode: str | None = None,
        run_id: str = "",
        timeout_override: int | None = None,
        max_turns_override: int | None = None,
        preflight_summary: str | None = None,
    ) -> SessionRecord:
        """Execute a single session and return the outcome record."""
        from agentfox.engine.state import invoke_runner

        runner = self._session_runner_factory(
            node_id,
            archetype=archetype,
            mode=mode,
            run_id=run_id,
            timeout_override=timeout_override,
            max_turns_override=max_turns_override,
        )
        if preflight_summary is not None:
            runner._preflight_summary = preflight_summary
        return await invoke_runner(runner, node_id, attempt, previous_error)

    async def delay(self) -> None:
        """Wait for the configured inter-session delay."""
        if self._inter_session_delay > 0:
            await asyncio.sleep(self._inter_session_delay)


# ---------------------------------------------------------------------------
# SerialDispatcher / ParallelDispatcher — dispatch strategies
# ---------------------------------------------------------------------------


class SerialDispatcher:
    """Dispatches one ready task at a time with inter-session delay."""

    def __init__(self, orch: Any) -> None:
        self._orch = orch

    async def dispatch(
        self,
        ready: list[str],
        state: Any,
        error_tracker: dict[str, str | None],
        first_dispatch: bool,
    ) -> bool:
        """Dispatch one ready task serially. Returns updated first_dispatch."""
        orch = self._orch
        if orch._graph_sync is None:
            raise RuntimeError("Orchestrator._graph_sync must be initialized before dispatch")

        for node_id in ready:
            if orch._signal.interrupted:
                break

            launch = orch._dispatch_mgr.prepare_launch(
                node_id,
                state,
                error_tracker,
            )
            if asyncio.iscoroutine(launch):
                launch = await launch
            if launch is None:
                continue

            _, attempt, previous_error, node_archetype, node_mode, preflight_summary = launch

            if not first_dispatch:
                await orch._dispatch_mgr.serial_runner.delay()
            first_dispatch = False

            orch._graph_sync.mark_in_progress(node_id)

            if node_archetype == "coder" and orch._result_handler is not None:
                orch._result_handler.capture_coverage_baseline(node_id, Path.cwd())

            timeout_override, max_turns_override = _resolve_overrides(orch._result_handler, node_id)

            record = await orch._dispatch_mgr.serial_runner.execute(
                node_id,
                attempt,
                previous_error,
                archetype=node_archetype,
                mode=node_mode,
                run_id=orch._run_id,
                timeout_override=timeout_override,
                max_turns_override=max_turns_override,
                preflight_summary=preflight_summary,
            )

            if orch._result_handler is None:
                raise RuntimeError("Orchestrator._result_handler must be initialized before dispatch")
            orch._result_handler.process(
                record,
                attempt,
                state,
                error_tracker,
            )

            if record.status == "completed":
                await orch._run_sync_barrier_if_needed(state)

            break

        return first_dispatch


class ParallelDispatcher:
    """Dispatches ready tasks using a streaming pool of concurrent sessions."""

    def __init__(self, orch: Any) -> None:
        self._orch = orch

    async def dispatch(
        self,
        ready: list[str],
        state: Any,
        error_tracker: dict[str, str | None],
    ) -> None:
        """Dispatch ready tasks using a streaming pool."""
        orch = self._orch
        if orch._graph_sync is None:
            raise RuntimeError("Orchestrator._graph_sync must be initialized before dispatch")
        if orch._dispatch_mgr.parallel_runner is None:
            raise RuntimeError("Orchestrator._parallel_runner must be initialized before dispatch")

        graph_sync = orch._graph_sync
        parallel_runner = orch._dispatch_mgr.parallel_runner

        pool: set[asyncio.Task[SessionRecord]] = set()

        await self.fill_pool(pool, ready, state, error_tracker)

        if not pool:
            return

        parallel_runner.track_tasks(list(pool))

        while pool:
            if orch._signal.interrupted:
                break

            done, pool = await asyncio.wait(pool, return_when=asyncio.FIRST_COMPLETED)

            barrier_needed = self.process_completed(
                done,
                state,
                error_tracker,
            )

            if barrier_needed:
                # Run barrier operations (worktree verification, sync,
                # config reload) without draining the in-flight pool.
                # Only drain when hot-load discovers new specs requiring
                # graph mutation (#731).
                needs_drain = await orch._run_sync_barrier_if_needed(state)
                if needs_drain and pool:
                    if orch._signal.interrupted:
                        break
                    logger.info(
                        "Hot-loaded new specs — draining %d in-flight tasks",
                        len(pool),
                    )
                    try:
                        drain_done, pool = await asyncio.wait(pool)
                    except asyncio.CancelledError:
                        break
                    self.process_completed(drain_done, state, error_tracker)

            if not orch._signal.interrupted:
                new_ready = graph_sync.ready_tasks()
                if not new_ready and len(pool) < parallel_runner.max_parallelism:
                    promoted = graph_sync.promote_deferred(
                        parallel_runner.max_parallelism - len(pool),
                    )
                    if promoted:
                        logger.info("Promoted %d deferred review node(s)", len(promoted))
                        new_ready = graph_sync.ready_tasks()
                await self.fill_pool(pool, new_ready, state, error_tracker)

            parallel_runner.track_tasks(list(pool))

    async def fill_pool(
        self,
        pool: set[asyncio.Task[SessionRecord]],
        candidates: list[str],
        state: Any,
        error_tracker: dict[str, str | None],
    ) -> None:
        """Launch candidates into the parallel pool up to max_parallelism."""
        orch = self._orch
        if orch._graph_sync is None:
            raise RuntimeError("Orchestrator._graph_sync must be initialized before dispatch")
        if orch._dispatch_mgr.parallel_runner is None:
            raise RuntimeError("Orchestrator._parallel_runner must be initialized before dispatch")

        max_pool = orch._dispatch_mgr.parallel_runner.max_parallelism
        max_review = max(1, int(max_pool * orch._config.max_review_fraction))

        review_in_pool = 0
        for t in pool:
            name = t.get_name()
            if name.startswith("parallel-"):
                pool_node_id = name[len("parallel-") :]
                if not _is_auto_pre(pool_node_id):
                    pool_archetype = orch._dispatch_mgr.get_node_archetype(pool_node_id)
                    if pool_archetype in _REVIEW_ARCHETYPES:
                        review_in_pool += 1

        for node_id in candidates:
            if len(pool) >= max_pool:
                break
            if orch._signal.interrupted:
                break

            if orch._graph_sync.node_states.get(node_id) == "blocked":
                continue

            candidate_archetype = orch._dispatch_mgr.get_node_archetype(node_id)
            if candidate_archetype in _REVIEW_ARCHETYPES and not _is_auto_pre(node_id) and review_in_pool >= max_review:
                continue

            launch = orch._dispatch_mgr.prepare_launch(
                node_id,
                state,
                error_tracker,
            )
            if asyncio.iscoroutine(launch):
                launch = await launch
            if launch is None:
                continue

            _, attempt, previous_error, archetype, node_mode, preflight_summary = launch

            orch._graph_sync.mark_in_progress(node_id)

            if archetype == "coder" and orch._result_handler is not None:
                orch._result_handler.capture_coverage_baseline(node_id, Path.cwd())

            timeout_override, max_turns_override = _resolve_overrides(orch._result_handler, node_id)

            task = asyncio.create_task(
                orch._dispatch_mgr.parallel_runner.execute_one(
                    node_id,
                    attempt,
                    previous_error,
                    archetype=archetype,
                    mode=node_mode,
                    run_id=orch._run_id,
                    timeout_override=timeout_override,
                    max_turns_override=max_turns_override,
                    preflight_summary=preflight_summary,
                ),
                name=f"parallel-{node_id}",
            )
            pool.add(task)

            if archetype in _REVIEW_ARCHETYPES and not _is_auto_pre(node_id):
                review_in_pool += 1

    def process_completed(
        self,
        done: set[asyncio.Task[SessionRecord]],
        state: Any,
        error_tracker: dict[str, str | None],
    ) -> bool:
        """Process completed parallel tasks. Returns True if a barrier is needed."""
        orch = self._orch
        if orch._result_handler is None:
            raise RuntimeError("Orchestrator._result_handler must be initialized before dispatch")

        barrier_needed = False
        for completed_task in done:
            try:
                record = completed_task.result()
            except Exception as exc:
                logger.error("Parallel task raised: %s", exc)
                continue

            attempt = orch._result_handler.get_attempt_count(record.node_id) or 1
            orch._result_handler.process(
                record,
                attempt,
                state,
                error_tracker,
            )

            if record.status == "completed":
                if orch._dispatch_mgr.should_trigger_barrier(state):
                    barrier_needed = True

        return barrier_needed


# ---------------------------------------------------------------------------
# DispatchManager — top-level dispatch collaborator
# ---------------------------------------------------------------------------


class DispatchManager:
    """Owns runners, dispatchers, and launch preparation logic.

    Extracted from Orchestrator to isolate dispatch concerns from the
    main orchestration loop.
    """

    def __init__(
        self,
        *,
        session_runner_factory: Callable[..., Any],
        inter_session_delay: float,
        parallel: int,
        graph: Any | None = None,
        circuit: Any | None = None,
        config: Any | None = None,
        routing_config: Any | None = None,
        specs_dir: Path | None = None,
        full_config: Any | None = None,
        knowledge_db_conn: Any | None = None,
        sink: Any | None = None,
        task_callback: TaskCallback | None = None,
        planning_config: Any | None = None,
    ) -> None:
        from agentfox.engine.parallel import ParallelRunner

        self._graph = graph
        self._circuit = circuit
        self._config = config
        self._routing_config = routing_config
        self._specs_dir = specs_dir
        self._full_config_ref = full_config
        self._knowledge_db_conn = knowledge_db_conn
        self._sink = sink
        self._run_id = ""
        self._task_callback = task_callback
        self._planning_config = planning_config
        self._result_handler: Any | None = None
        self._preflight_summaries: dict[str, str] = {}

        self.serial_runner = SerialRunner(
            session_runner_factory=session_runner_factory,
            inter_session_delay=inter_session_delay,
        )
        self.parallel_runner: ParallelRunner | None = None
        if parallel > 1:
            self.parallel_runner = ParallelRunner(
                session_runner_factory=session_runner_factory,
                max_parallelism=parallel,
                inter_session_delay=inter_session_delay,
            )

    def get_node(self, node_id: str) -> Any | None:
        """Look up a TaskNode by ID, returning None if graph is unset."""
        if self._graph is not None:
            return self._graph.nodes.get(node_id)
        return None

    def get_node_archetype(self, node_id: str) -> str:
        """Get the archetype name for a node from the task graph."""
        return _get_node_archetype(self._graph, node_id)

    def get_node_mode(self, node_id: str) -> str | None:
        """Get the mode for a node from the task graph (97-REQ-5.3)."""
        return _get_node_mode(self._graph, node_id)

    async def prepare_launch(
        self,
        node_id: str,
        state: Any,
        error_tracker: dict[str, str | None],
    ) -> tuple[str, int, str | None, str, int, str | None, str | None] | None:
        """Check whether a node may launch.

        Performs a pre-session workspace health check before creating the
        worktree. If untracked files are detected, the node is blocked
        with a diagnostic message. Git command errors are treated as
        fail-open (dispatch proceeds).

        Returns a tuple of (verdict, attempt, previous_error, archetype,
        mode, preflight_summary) if the node is allowed to launch, or None
        if it was blocked/limited.
        """
        # 118-REQ-4.1: Pre-session workspace health check
        try:
            from agentfox.workspace.health import (
                HealthReport,
                check_workspace_health,
                force_clean_workspace,
                format_health_diagnostic,
            )

            report = await check_workspace_health(Path.cwd())
            if report.has_issues:
                # 571-AC-1: Auto-remediate when untracked files match known
                # build-artifact patterns (e.g. .proptest-regressions, __pycache__).
                # 571-AC-2: Also remediate when workspace.force_clean is enabled.
                should_remediate = self._is_force_clean_enabled() or any(
                    _is_known_build_artifact(f) for f in report.untracked_files
                )
                if should_remediate:
                    try:
                        logger.warning(
                            "Pre-session health check: auto-remediating workspace for %s",
                            node_id,
                        )
                        report = await force_clean_workspace(Path.cwd(), report)
                    except Exception:
                        # 571-AC-5: Fail-open — log a warning and proceed as if clean.
                        logger.warning(
                            "Pre-session workspace auto-remediation raised an exception for %s; proceeding",
                            node_id,
                            exc_info=True,
                        )
                        report = HealthReport(untracked_files=[], dirty_index_files=[])

                if report.has_issues:
                    # AC-4: Do NOT permanently block the task on workspace-state
                    # failures. A concurrent spec may be about to merge, resolving
                    # the orphan files. Return None to skip this dispatch cycle and
                    # allow re-evaluation on the next cycle without calling
                    # _block_task_fn (which triggers an irreversible mark_blocked).
                    diagnostic = format_health_diagnostic(report)
                    logger.warning(
                        "Pre-session health check failed for %s: %s — "
                        "skipping this dispatch cycle (task remains re-dispatchable)",
                        node_id,
                        diagnostic,
                    )
                    return None
        except Exception:
            # 118-REQ-4.E1: Fail-open on git command errors
            logger.warning(
                "Pre-session health check failed with exception for %s, proceeding",
                node_id,
                exc_info=True,
            )

        if (
            self._result_handler is not None
            and hasattr(self._result_handler, "is_workspace_backoff_active")
            and self._result_handler.is_workspace_backoff_active(node_id)
        ):
            self._result_handler.log_backoff_once(node_id, "workspace")
            return None

        if (
            self._result_handler is not None
            and hasattr(self._result_handler, "is_environment_backoff_active")
            and self._result_handler.is_environment_backoff_active(node_id)
        ):
            self._result_handler.log_backoff_once(node_id, "environment")
            return None

        archetype = self.get_node_archetype(node_id)
        mode = self.get_node_mode(node_id)

        rh = self._result_handler
        attempt = (rh.get_attempt_count(node_id) if rh is not None else 0) + 1
        verdict = self._check_launch(
            node_id,
            attempt,
            state,
            error_tracker,
        )
        if verdict != "allowed":
            return None

        if archetype == "coder" and attempt == 1:
            skip = self._run_preflight(node_id)
            if skip:
                return None

        if rh is not None:
            rh.record_attempt(node_id, attempt)
        previous_error = error_tracker.get(node_id)
        preflight_summary = self._preflight_summaries.pop(node_id, None)

        return (verdict, attempt, previous_error, archetype, mode, preflight_summary)

    def _check_launch(
        self,
        node_id: str,
        attempt: int,
        state: Any,
        error_tracker: dict[str, str | None] | None = None,
    ) -> str:
        """Check whether *node_id* may be launched.

        Returns ``"allowed"``, ``"blocked"``, or ``"limited"``.
        """
        decision = self._circuit.check_launch(node_id, attempt, state)
        if decision.allowed:
            return "allowed"

        if self._config.max_retries is not None and attempt > self._config.max_retries + 1:
            if self._result_handler is not None:
                self._result_handler.record_attempt(node_id, attempt)
            last_error = error_tracker.get(node_id) if error_tracker else None
            reason = f"Retry limit exceeded for {node_id}"
            if last_error:
                reason = f"{reason}: {last_error}"
            self._block_task_fn(
                node_id,
                state,
                reason,
            )
            self._check_block_budget_fn(state)
            return "blocked"
        return "limited"

    def _run_preflight(self, node_id: str) -> bool:
        """Run pre-flight check and skip the session if work is done.

        When the verdict is LAUNCH, stores the preflight summary for
        inclusion in the coder's task prompt (avoids redundant Quick Triage).
        """
        from agentfox.core.config import resolve_spec_root
        from agentfox.core.node_id import parse_node_id

        parsed = parse_node_id(node_id)
        specs_dir = self._specs_dir
        if specs_dir is None and self._full_config_ref is not None:
            fc = self._full_config_ref() if callable(self._full_config_ref) else self._full_config_ref
            if fc is not None:
                specs_dir = resolve_spec_root(fc, Path.cwd())
        if specs_dir is None:
            return False

        result = run_preflight(
            spec_name=parsed.spec_name,
            group_number=parsed.group_number,
            conn=self._knowledge_db_conn,
            specs_dir=specs_dir,
            cwd=Path.cwd(),
        )

        if result.verdict == PreflightVerdict.LAUNCH:
            self._preflight_summaries[node_id] = result.format_summary()

        if result.verdict != PreflightVerdict.SKIP:
            return False

        if self._graph_sync is not None:
            prev_status = self._graph_sync.node_states.get(node_id, "pending")
            self._graph_sync.mark_completed(node_id)
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.PREFLIGHT_SKIP,
                node_id=node_id,
                payload={
                    "from_status": prev_status,
                    "reason": "checkboxes done, no active findings, tests pass",
                },
            )
            if self._knowledge_db_conn is not None:
                try:
                    from agentfox.engine.state import persist_node_status

                    persist_node_status(self._knowledge_db_conn, node_id, "completed")
                except Exception:
                    logger.debug("Failed to persist preflight skip status", exc_info=True)

            if self._task_callback is not None:
                self._task_callback(
                    TaskEvent(
                        node_id=node_id,
                        status="completed",
                        duration_s=0.0,
                        archetype="coder",
                    )
                )

        logger.info("Preflight skip: %s", node_id)
        return True

    def _is_force_clean_enabled(self) -> bool:
        """Return True when workspace.force_clean is enabled in the full config.

        Reads the AgentFoxConfig reference stored at construction time.
        Returns False if the reference is absent or the attribute is missing
        (safe default: no force-clean).

        Requirements: 571-AC-2
        """
        try:
            fc = self._full_config_ref() if callable(self._full_config_ref) else self._full_config_ref
            if fc is not None and hasattr(fc, "workspace"):
                return bool(fc.workspace.force_clean)
        except Exception:
            logger.debug("Could not read workspace.force_clean config", exc_info=True)
        return False

    def filter_file_conflicts(self, ready: list[str]) -> list[str]:
        """Filter conflicting tasks from the ready set.

        Requirements: 39-REQ-9.3
        """
        try:
            from agentfox.graph.file_impacts import (
                FileImpact,
                filter_conflicts_from_dispatch,
            )

            impacts: list[FileImpact] = []
            for node_id in ready:
                node = self.get_node(node_id)
                spec_name = node.spec_name if node else ""
                task_group = node.group_number if node else 1

                if self._specs_dir is not None:
                    spec_dir = self._specs_dir / spec_name
                    if spec_dir.is_dir():
                        from agentfox.graph.file_impacts import extract_file_impacts

                        predicted = extract_file_impacts(spec_dir, task_group)
                        impacts.append(FileImpact(node_id, predicted))
                    else:
                        impacts.append(FileImpact(node_id, set()))
                else:
                    impacts.append(FileImpact(node_id, set()))

            filtered = filter_conflicts_from_dispatch(ready, impacts)
            if len(filtered) < len(ready):
                deferred = set(ready) - set(filtered)
                logger.info(
                    "File conflict detection deferred %d tasks: %s",
                    len(deferred),
                    deferred,
                )
            return filtered
        except Exception:
            logger.warning(
                "File conflict detection failed, dispatching all ready tasks",
                exc_info=True,
            )
            return ready

    def should_trigger_barrier(self, state: Any) -> bool:
        """Check whether a sync barrier should fire (no side effects)."""
        from agentfox.engine.barrier import _count_node_status
        from agentfox.engine.hot_load import should_trigger_barrier

        effective = self._config.effective_sync_interval
        if effective == 0:
            return False
        completed_count = _count_node_status(state.node_states, "completed")
        return should_trigger_barrier(completed_count, effective)

    def set_graph(self, graph: Any) -> None:
        """Update the task graph reference (after hot-loading)."""
        self._graph = graph

    def set_graph_sync(self, graph_sync: Any) -> None:
        """Update the graph_sync reference."""
        self._graph_sync = graph_sync

    def set_run_id(self, run_id: str) -> None:
        """Update the run ID."""
        self._run_id = run_id

    def set_callbacks(
        self,
        block_task_fn: Callable[..., None],
        check_block_budget_fn: Callable[..., bool],
    ) -> None:
        """Set callback functions for blocking tasks."""
        self._block_task_fn = block_task_fn
        self._check_block_budget_fn = check_block_budget_fn

    def set_sink(self, sink: Any) -> None:
        """Update the sink dispatcher reference."""
        self._sink = sink

    def set_result_handler(self, handler: Any) -> None:
        """Update the result handler reference (for workspace backoff checks)."""
        self._result_handler = handler
