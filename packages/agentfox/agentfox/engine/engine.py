"""Orchestrator: deterministic execution engine. Zero LLM calls.

Loads the task graph, dispatches sessions in dependency order, manages
retries with error feedback, cascade-blocks failed tasks, persists state
after every session, and handles graceful interruption.

The Orchestrator delegates to three collaborators:
- StateManager  — state loading, initialization, persistence, node status
- DispatchManager — runners, dispatchers, launch preparation, preflight
- ConfigReloader — configuration hot-reload from disk

Requirements: 04-REQ-1.1 through 04-REQ-1.4, 04-REQ-1.E1, 04-REQ-1.E2,
              04-REQ-2.1 through 04-REQ-2.3, 04-REQ-2.E1,
              04-REQ-5.1, 04-REQ-5.2, 04-REQ-5.3,
              04-REQ-6.1, 04-REQ-6.2, 04-REQ-6.3,
              04-REQ-7.1, 04-REQ-7.2, 04-REQ-7.E1,
              04-REQ-8.1, 04-REQ-8.2, 04-REQ-8.3, 04-REQ-8.E1,
              04-REQ-9.1, 04-REQ-9.E1
"""

from __future__ import annotations

import asyncio
import atexit
import logging
import signal
import subprocess
from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from afaudit.emit import emit_audit_event
from afaudit.events import (
    AuditEventType,
    AuditJsonlSink,
    AuditSeverity,
    generate_run_id,
)
from afaudit.sink import SinkDispatcher

from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    CachePolicy,
    OrchestratorConfig,
    PlanningConfig,
    RoutingConfig,
)
from agentfox.core.errors import PlanError
from agentfox.engine.barrier import _count_node_status, run_sync_barrier_sequence
from agentfox.engine.circuit import CircuitBreaker
from agentfox.engine.config_reload import (  # noqa: F401 — ReloadResult, diff_configs re-exported
    ConfigReloader,
    ReloadResult,
    diff_configs,
)
from agentfox.engine.dispatch import (
    DispatchManager,
    ParallelDispatcher,
    SerialDispatcher,
)
from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.hot_load import hot_load_into_graph, should_trigger_barrier
from agentfox.engine.issue_summary import post_issue_summaries
from agentfox.engine.result_handler import SessionResultHandler
from agentfox.engine.state import ExecutionState, RunStatus
from agentfox.engine.state_manager import (
    StateManager,
    build_edges_dict,
    defer_ready_reviews,
    init_error_tracker,
    load_or_init_state,
    reset_blocked_tasks,
    reset_in_progress_tasks,
)
from agentfox.graph.injection import ensure_graph_archetypes
from agentfox.graph.persistence import load_plan, save_plan
from agentfox.graph.types import TaskGraph
from agentfox.knowledge.duckdb_sink import enforce_audit_retention
from agentfox.ui.progress import TaskCallback

logger = logging.getLogger(__name__)

_defer_ready_reviews = defer_ready_reviews


class _SignalHandler:
    """SIGINT/SIGTERM handling for graceful shutdown (04-REQ-8.E1)."""

    def __init__(self) -> None:
        self.interrupted = False
        self._interrupt_count = 0
        self._prev_sigint: Any = None
        self._prev_sigterm: Any = None

    def install(self) -> None:
        def handler(signum: int, frame: Any) -> None:
            self._interrupt_count += 1
            if self._interrupt_count >= 2:
                logger.warning("Double interrupt received, exiting immediately.")
                raise SystemExit(1)
            self.interrupted = True
            sig_name = "SIGTERM" if signum == signal.SIGTERM else "SIGINT"
            logger.info("%s received, shutting down gracefully...", sig_name)

        try:
            self._prev_sigint = signal.getsignal(signal.SIGINT)
            signal.signal(signal.SIGINT, handler)
        except (OSError, ValueError):
            self._prev_sigint = None
        try:
            self._prev_sigterm = signal.getsignal(signal.SIGTERM)
            signal.signal(signal.SIGTERM, handler)
        except (OSError, ValueError):
            self._prev_sigterm = None

    def restore(self) -> None:
        if self._prev_sigint is not None:
            try:
                signal.signal(signal.SIGINT, self._prev_sigint)
            except (OSError, ValueError):
                pass
        if self._prev_sigterm is not None:
            try:
                signal.signal(signal.SIGTERM, self._prev_sigterm)
            except (OSError, ValueError):
                pass


class Orchestrator:
    """Deterministic execution engine. Zero LLM calls.

    Delegates to StateManager, DispatchManager, and ConfigReloader.
    """

    def __init__(
        self,
        config: OrchestratorConfig,
        session_runner_factory: Callable[..., Any],
        *,
        agent_dir: Path | None = None,
        watch: bool = False,
        specs_dir: Path | None = None,
        task_callback: TaskCallback | None = None,
        routing_config: RoutingConfig | None = None,
        archetypes_config: ArchetypesConfig | None = None,
        planning_config: PlanningConfig | None = None,
        sink_dispatcher: SinkDispatcher | None = None,
        audit_dir: Path | None = None,
        audit_db_conn: Any | None = None,
        knowledge_db_conn: Any | None = None,
        config_path: Path | None = None,
        full_config: AgentFoxConfig | None = None,
        platform: Any | None = None,
        knowledge_provider: Any | None = None,
    ) -> None:
        self._config = config
        self._watch = watch
        self._agent_dir = agent_dir or Path(".agent-fox")
        self._circuit = CircuitBreaker(config)
        self._graph_sync: GraphSync | None = None
        self._signal = _SignalHandler()
        self._is_parallel = config.parallel > 1
        self._specs_dir = specs_dir
        self._task_callback = task_callback
        self._graph: TaskGraph | None = None
        self._archetypes_config = archetypes_config
        self._planning_config = planning_config or PlanningConfig()
        self._sink = sink_dispatcher
        self._run_id: str = ""
        self._audit_dir = audit_dir
        self._audit_db_conn = audit_db_conn
        self._knowledge_db_conn = knowledge_db_conn
        self._platform = platform
        self._knowledge_provider = knowledge_provider
        self._issue_summaries_posted: set[str] = set()
        self._atexit_handler: Callable[[], None] | None = None

        self._config_reloader = ConfigReloader(config_path, full_config)

        _rc = routing_config or RoutingConfig()
        self._routing_config = _rc

        self._state_mgr = StateManager(
            knowledge_db_conn=knowledge_db_conn,
            task_callback=task_callback,
            max_blocked_fraction=config.max_blocked_fraction,
        )

        self._dispatch_mgr = DispatchManager(
            session_runner_factory=session_runner_factory,
            inter_session_delay=float(config.inter_session_delay),
            parallel=config.parallel,
            circuit=self._circuit,
            config=config,
            routing_config=_rc,
            specs_dir=specs_dir,
            full_config=lambda: self._full_config,
            knowledge_db_conn=knowledge_db_conn,
            sink=sink_dispatcher,
            task_callback=task_callback,
            planning_config=self._planning_config,
        )

        self._result_handler: SessionResultHandler | None = None
        self._serial_dispatcher = SerialDispatcher(self)
        self._parallel_dispatcher: ParallelDispatcher | None = None
        if self._is_parallel:
            self._parallel_dispatcher = ParallelDispatcher(self)

    @property
    def _parallel_runner(self):  # noqa: ANN202
        return self._dispatch_mgr.parallel_runner

    @property
    def _repo_root(self) -> Path:
        return self._agent_dir.parent

    @property
    def _config_path(self) -> Path | None:
        return self._config_reloader.config_path

    @_config_path.setter
    def _config_path(self, value: Path | None) -> None:
        self._config_reloader._config_path = value

    @property
    def _full_config(self) -> AgentFoxConfig | None:
        return self._config_reloader.full_config

    @_full_config.setter
    def _full_config(self, value: AgentFoxConfig | None) -> None:
        self._config_reloader._full_config = value

    @property
    def _config_hash(self) -> str:
        return self._config_reloader.config_hash

    @_config_hash.setter
    def _config_hash(self, value: str) -> None:
        self._config_reloader.config_hash = value

    def _emit_audit(self, *args: Any, **kwargs: Any) -> None:
        emit_audit_event(self._sink, self._run_id, *args, **kwargs)

    def _emit_watch_poll(self, poll: int, *, new_tasks: bool) -> None:
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.WATCH_POLL,
            payload={"poll_number": poll, "new_tasks_found": new_tasks},
        )

    def _get_node_archetype(self, node_id: str) -> str:
        return self._dispatch_mgr.get_node_archetype(node_id)

    def _get_node_mode(self, node_id: str) -> str | None:
        return self._dispatch_mgr.get_node_mode(node_id)

    def _get_predecessors(self, node_id: str) -> list[str]:
        if self._graph_sync is None:
            return []
        return self._graph_sync.predecessors(node_id)

    def _block_task(self, node_id: str, state: ExecutionState, reason: str) -> None:
        self._state_mgr.block_task(
            node_id,
            state,
            reason,
            graph_sync=self._graph_sync,
            get_archetype_fn=self._get_node_archetype,
        )

    def _check_block_budget(self, state: ExecutionState) -> bool:
        return self._state_mgr.check_block_budget(
            state,
            sink=self._sink,
            run_id=self._run_id,
        )

    def _sync_plan_statuses(self, state: ExecutionState) -> None:
        self._state_mgr.sync_plan_statuses(state, self._graph)

    @property
    def node_states(self) -> dict[str, str]:
        if self._graph_sync is not None:
            return self._graph_sync.node_states
        return {}

    def _load_graph(self) -> TaskGraph:
        if self._knowledge_db_conn is None:
            raise PlanError("No database connection available. Run `agent-fox plan` first.")
        graph = load_plan(self._knowledge_db_conn)
        if graph is None:
            raise PlanError("No plan found in database. Run `agent-fox plan` first to generate a plan.")
        return graph

    def _compute_plan_hash(self) -> str:
        if self._graph is not None:
            try:
                from agentfox.graph.persistence import compute_plan_hash

                return compute_plan_hash(self._graph)
            except Exception:
                pass
        return ""

    # -- Cache policy auto-upgrade -----------------------------------------

    def _maybe_upgrade_cache_policy(self, graph: TaskGraph) -> None:
        """Auto-select EXTENDED cache policy for multi-session runs.

        When the orchestrator detects a multi-session run (>3 graph nodes
        or parallel > 1) and no explicit cache policy was configured by
        the user, upgrade from DEFAULT to EXTENDED to benefit from 1-hour
        TTL cache hits on shared system prompt prefixes.

        Requirements: issue #743
        """
        if self._full_config is None:
            return
        if self._full_config._caching_explicit:
            return
        is_multi_session = len(graph.nodes) > 3 or self._config.parallel > 1
        if not is_multi_session:
            return
        if self._full_config.caching.cache_policy != CachePolicy.DEFAULT:
            return
        self._full_config.caching.cache_policy = CachePolicy.EXTENDED
        logger.info(
            "Auto-selecting EXTENDED cache policy for multi-session run (%d nodes, parallel=%d)",
            len(graph.nodes),
            self._config.parallel,
        )

    # -- Init / Run / Watch / Shutdown --------------------------------------

    def _init_run(
        self,
    ) -> tuple[ExecutionState, dict[str, int], dict[str, str | None]] | ExecutionState:
        self._run_id = generate_run_id()
        logger.debug("Audit run ID: %s", self._run_id)

        if self._audit_dir is not None:
            try:
                from afaudit.cleanup import purge_stale_audit_files

                purge_stale_audit_files(self._audit_dir, exclude_run_id=self._run_id)
            except Exception:
                logger.warning("Failed to purge stale audit files", exc_info=True)

        # Wire run_id to the knowledge provider so summary queries work
        # (120-REQ-1.3).
        if self._knowledge_provider is not None and hasattr(self._knowledge_provider, "set_run_id"):
            self._knowledge_provider.set_run_id(self._run_id)

        if self._audit_dir is not None and self._sink is not None:
            try:
                self._sink.add(AuditJsonlSink(self._audit_dir, self._run_id))
            except Exception:
                logger.warning("Failed to register AuditJsonlSink", exc_info=True)

        if self._audit_dir is not None and self._audit_db_conn is not None:
            try:
                enforce_audit_retention(
                    self._audit_dir,
                    self._audit_db_conn,
                    max_runs=self._config.audit_retention_runs,
                )
            except Exception:
                logger.warning("Failed to enforce audit retention", exc_info=True)

        graph = self._load_graph()

        if ensure_graph_archetypes(graph, self._archetypes_config, self._specs_dir):
            if self._knowledge_db_conn is not None:
                try:
                    save_plan(graph, self._knowledge_db_conn)
                    logger.info("Persisted plan with injected archetype nodes")
                except Exception:
                    logger.warning("Failed to persist plan after archetype injection", exc_info=True)

        if not graph.nodes and not self._watch:
            return ExecutionState(
                plan_hash=self._compute_plan_hash(),
                node_states={},
                run_status=RunStatus.COMPLETED,
                started_at=datetime.now(UTC).isoformat(),
                updated_at=datetime.now(UTC).isoformat(),
                run_id=self._run_id,
            )

        self._graph = graph
        self._dispatch_mgr.set_graph(graph)

        # Auto-upgrade cache policy to EXTENDED for multi-session runs
        # when no explicit user configuration is set (issue #743).
        # Criteria: >3 graph nodes OR parallel > 1.
        self._maybe_upgrade_cache_policy(graph)

        plan_hash = self._compute_plan_hash()
        state = load_or_init_state(self._knowledge_db_conn, plan_hash, graph)
        state.run_id = self._run_id  # 126-REQ-7.2
        is_fresh_start = state.total_sessions == 0 and not state.session_history
        reset_in_progress_tasks(state, self._knowledge_db_conn)
        if not is_fresh_start:
            reset_blocked_tasks(state, self._knowledge_db_conn)
        else:
            for node_id, node in graph.nodes.items():
                if node.status.value == "blocked":
                    state.node_states[node_id] = "blocked"

        if self._knowledge_db_conn is not None:
            try:
                from agentfox.engine.state import cleanup_stale_runs as _cleanup

                cleaned = _cleanup(self._knowledge_db_conn, self._run_id)
                if cleaned:
                    logger.info("Marked %d stale running run(s) as stalled", cleaned)
            except Exception:
                logger.warning("Failed to clean up stale running runs", exc_info=True)

        if self._knowledge_db_conn is not None:
            try:
                from agentfox.engine.state import create_run as _create_run

                _create_run(self._knowledge_db_conn, self._run_id, plan_hash)
            except Exception:
                logger.debug("Failed to create DB run record", exc_info=True)

            # Register atexit handler to transition run to 'stalled' on
            # unexpected process termination (118-REQ-6.2)
            try:
                from agentfox.engine.state import run_cleanup_handler

                _db_conn = self._knowledge_db_conn
                _run_id = self._run_id

                def _atexit_cleanup() -> None:
                    run_cleanup_handler(_run_id, _db_conn)

                self._atexit_handler = _atexit_cleanup
                atexit.register(_atexit_cleanup)
            except Exception:
                logger.warning("Failed to register run cleanup handler", exc_info=True)

        edges_dict = build_edges_dict(graph)
        node_archetypes = {nid: n.archetype for nid, n in graph.nodes.items()}
        self._graph_sync = GraphSync(state.node_states, edges_dict, node_archetypes)
        self._dispatch_mgr.set_graph_sync(self._graph_sync)
        self._dispatch_mgr.set_run_id(self._run_id)
        self._dispatch_mgr.set_callbacks(self._block_task, self._check_block_budget)

        defer_ready_reviews(graph, self._graph_sync, self._knowledge_db_conn)
        self._result_handler = SessionResultHandler(
            graph_sync=self._graph_sync,
            max_retries=self._config.max_retries,
            task_callback=self._task_callback,
            sink=self._sink,
            run_id=self._run_id,
            graph=self._graph,
            archetypes_config=self._archetypes_config,
            knowledge_db_conn=self._knowledge_db_conn,
            block_task_fn=self._block_task,
            check_block_budget_fn=self._check_block_budget,
            max_timeout_retries=self._routing_config.max_timeout_retries,
            timeout_multiplier=self._routing_config.timeout_multiplier,
            timeout_ceiling_factor=self._routing_config.timeout_ceiling_factor,
            original_session_timeout=self._config.session_timeout,
        )

        self._dispatch_mgr.set_result_handler(self._result_handler)
        self._result_handler.init_attempts(state)

        return state, init_error_tracker(state)

    async def run(self) -> ExecutionState:
        """Execute the full orchestration loop."""
        run_start_time = datetime.now(UTC)
        self._watch_poll_count = 0
        result = self._init_run()
        if isinstance(result, ExecutionState):
            return result
        state, error_tracker = result

        self._signal.install()

        # Run-level pre-flight workspace check: prune stale worktrees,
        # check for stale lock files, and test git credentials.
        try:
            from agentfox.workspace.health import run_preflight_workspace_check

            preflight = await run_preflight_workspace_check(self._repo_root)
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.RUN_PREFLIGHT,
                payload={
                    "push_available": preflight.push_available,
                    "worktrees_pruned": preflight.worktrees_pruned,
                    "stale_worktrees_removed": preflight.stale_worktrees_removed,
                    "stale_locks": preflight.stale_locks_found,
                    "issues": preflight.issues_found,
                },
            )
        except Exception:
            logger.warning("Run pre-flight check failed, proceeding", exc_info=True)

        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.RUN_START,
            payload={
                "plan_hash": self._compute_plan_hash(),
                "total_nodes": len(self._graph.nodes) if self._graph else 0,
                "parallel": self._is_parallel,
            },
        )

        first_dispatch = True
        try:
            while True:
                if self._signal.interrupted:
                    await self._shutdown(state, error_tracker=error_tracker)
                    return state
                if state.run_status == RunStatus.BLOCK_LIMIT:
                    return state

                stop_decision = self._circuit.should_stop(state)
                if not stop_decision.allowed:
                    if self._config.max_cost is not None and state.total_cost >= self._config.max_cost:
                        state.run_status = RunStatus.COST_LIMIT
                        limit_type, limit_value = "cost", float(self._config.max_cost)
                    else:
                        state.run_status = RunStatus.SESSION_LIMIT
                        limit_type, limit_value = "sessions", float(self._config.max_sessions or 0)
                    logger.info("Circuit breaker tripped: %s", stop_decision.reason)
                    emit_audit_event(
                        self._sink,
                        self._run_id,
                        AuditEventType.RUN_LIMIT_REACHED,
                        severity=AuditSeverity.WARNING,
                        payload={"limit_type": limit_type, "limit_value": limit_value},
                    )
                    return state

                assert self._graph_sync is not None  # noqa: S101
                ready = self._graph_sync.ready_tasks()

                if self._planning_config.file_conflict_detection and self._is_parallel and len(ready) > 1:
                    ready = self._dispatch_mgr.filter_file_conflicts(ready)

                if not ready:
                    pr = self._dispatch_mgr.parallel_runner
                    max_slots = pr.max_parallelism if pr else 1
                    promoted = self._graph_sync.promote_deferred(limit=max_slots)
                    if promoted:
                        logger.info("Promoted %d deferred review node(s)", len(promoted))
                        ready = self._graph_sync.ready_tasks()

                if not ready:
                    if self._graph_sync.is_stalled(ready=ready):
                        state.run_status = RunStatus.STALLED
                        logger.warning("Execution stalled. Summary: %s", self._graph_sync.summary())
                        return state
                    if await self._try_end_of_run_discovery(state):
                        continue
                    if self._watch:
                        if not self._config.hot_load:
                            logger.warning(
                                "Watch mode is active but hot_load is disabled "
                                "in configuration; terminating with COMPLETED "
                                "status instead of entering watch loop."
                            )
                        else:
                            watch_result = await self._watch_loop(state)
                            if watch_result is None:
                                continue
                            return watch_result
                    if self._post_merge_check_passes():
                        state.run_status = RunStatus.COMPLETED
                    else:
                        state.run_status = RunStatus.COMPLETED_DIRTY
                    return state

                if self._is_parallel and self._dispatch_mgr.parallel_runner is not None:
                    await self._parallel_dispatcher.dispatch(ready, state, error_tracker)
                else:
                    first_dispatch = await self._serial_dispatcher.dispatch(
                        ready,
                        state,
                        error_tracker,
                        first_dispatch,
                    )
        finally:
            await self._finalize_run(state, run_start_time)

    async def _finalize_run(self, state: ExecutionState, run_start_time: datetime) -> None:
        self._signal.restore()
        self._sync_plan_statuses(state)

        try:
            from agentfox.session.auditor_output import cleanup_completed_spec_audits

            if self._graph_sync is not None:
                completed = self._graph_sync.completed_spec_names()
                if completed:
                    cleanup_completed_spec_audits(Path.cwd(), completed)
        except Exception:
            logger.warning("Audit report cleanup failed", exc_info=True)

        if self._platform is not None and self._graph_sync is not None:
            await self._post_issue_summaries()

        # Unregister the atexit cleanup handler — the run is completing
        # normally so we don't want the handler to overwrite the terminal
        # status with 'stalled' (118-REQ-6.3).
        if self._atexit_handler is not None:
            try:
                atexit.unregister(self._atexit_handler)
            except Exception:
                pass
            self._atexit_handler = None

        if self._knowledge_db_conn is not None:
            try:
                from agentfox.engine.state import complete_run as _complete_run

                run_status_val = state.run_status.value if hasattr(state.run_status, "value") else str(state.run_status)
                _complete_run(self._knowledge_db_conn, self._run_id, run_status_val)
            except Exception:
                logger.debug("Failed to complete run in DB", exc_info=True)

        run_duration_ms = int((datetime.now(UTC) - run_start_time).total_seconds() * 1000)
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.RUN_COMPLETE,
            payload={
                "total_sessions": len(state.session_history),
                "total_cost": state.total_cost,
                "duration_ms": run_duration_ms,
                "run_status": state.run_status.value if hasattr(state.run_status, "value") else str(state.run_status),
            },
        )

    async def _post_issue_summaries(self) -> None:
        try:
            completed = self._graph_sync.completed_spec_names()
            newly_completed = completed - self._issue_summaries_posted
            if newly_completed:
                _eff = self._specs_dir
                if _eff is None and self._full_config is not None:
                    from agentfox.core.config import resolve_spec_root as _rsr

                    _eff = _rsr(self._full_config, Path.cwd())
                _branch = "main"
                if self._full_config is not None:
                    _branch = self._full_config.workspace.integration_branch
                posted = await post_issue_summaries(
                    self._platform,
                    _eff or Path(".specs"),
                    newly_completed,
                    self._issue_summaries_posted,
                    Path.cwd(),
                    integration_branch=_branch,
                )
                self._issue_summaries_posted.update(posted)
        except Exception:
            logger.warning("Issue summary posting failed", exc_info=True)

    async def _watch_loop(self, state: ExecutionState) -> ExecutionState | None:
        while True:
            self._watch_poll_count += 1
            poll = self._watch_poll_count

            if self._signal.interrupted:
                self._emit_watch_poll(poll, new_tasks=False)
                state.run_status = RunStatus.INTERRUPTED
                return state

            interval = self._config.watch_interval
            logger.info("Watch poll %d: sleeping %ds", poll, interval)
            await asyncio.sleep(interval)

            if self._signal.interrupted:
                self._emit_watch_poll(poll, new_tasks=False)
                state.run_status = RunStatus.INTERRUPTED
                return state

            stop_decision = self._circuit.should_stop(state)
            if not stop_decision.allowed:
                cost_exceeded = self._config.max_cost is not None and state.total_cost >= self._config.max_cost
                state.run_status = RunStatus.COST_LIMIT if cost_exceeded else RunStatus.SESSION_LIMIT
                return state

            try:
                new_tasks = await self._try_end_of_run_discovery(state)
            except Exception:
                logger.exception("Watch poll %d: barrier error", poll)
                new_tasks = False

            self._emit_watch_poll(poll, new_tasks=new_tasks)
            if new_tasks:
                return None

    async def _run_sync_barrier_if_needed(self, state: ExecutionState) -> bool:
        """Run the sync barrier sequence if needed.

        Returns True if the barrier discovered new specs requiring a
        pool drain, False otherwise (including when no barrier fires).
        """
        effective = self._config.effective_sync_interval
        if effective == 0:
            return False
        completed_count = _count_node_status(state.node_states, "completed")
        if not should_trigger_barrier(completed_count, effective):
            return False
        _ib = "main"
        if self._full_config is not None:
            _ib = self._full_config.workspace.integration_branch
        return await run_sync_barrier_sequence(
            state=state,
            sync_interval=effective,
            repo_root=self._repo_root,
            integration_branch=_ib,
            emit_audit=self._emit_audit,
            specs_dir=self._specs_dir,
            hot_load_enabled=self._config.hot_load,
            hot_load_fn=self._hot_load_new_specs,
            sync_plan_fn=self._sync_plan_statuses,
            barrier_callback=None,
            knowledge_db_conn=self._knowledge_db_conn,
            reload_config_fn=self._reload_config,
        )

    def _post_merge_check_passes(self) -> bool:
        """Run ``make check`` on the merged integration branch.

        Called after all tasks complete to validate the combined result.
        Returns True if the check passes (or no Makefile exists), False
        if the quality suite fails.
        """
        cwd = self._repo_root or Path.cwd()
        makefile = cwd / "Makefile"
        if not makefile.exists():
            logger.debug("No Makefile found, skipping post-merge check")
            return True

        logger.info("Post-merge validation: running make check")
        try:
            result = subprocess.run(
                ["make", "check"],
                cwd=cwd,
                capture_output=True,
                text=True,
                timeout=600,
            )
            if result.returncode == 0:
                logger.info("Post-merge validation passed")
                return True

            # Fall back to make test if make check target doesn't exist
            if "No rule to make target" in (result.stderr or ""):
                result = subprocess.run(
                    ["make", "test"],
                    cwd=cwd,
                    capture_output=True,
                    text=True,
                    timeout=600,
                )
                if result.returncode == 0:
                    logger.info("Post-merge validation passed (make test)")
                    return True

            stderr_tail = (result.stderr or "").strip().splitlines()[-10:]
            stdout_tail = (result.stdout or "").strip().splitlines()[-10:]
            output_summary = "\n".join(stderr_tail or stdout_tail)
            logger.error(
                "Post-merge validation FAILED (exit %d):\n%s",
                result.returncode,
                output_summary,
            )
            return False
        except subprocess.TimeoutExpired:
            logger.error("Post-merge validation timed out after 600s")
            return False
        except Exception:
            logger.warning("Post-merge validation could not run", exc_info=True)
            return True

    async def _try_end_of_run_discovery(self, state: ExecutionState) -> bool:
        if not self._config.hot_load:
            return False
        logger.info("End-of-run discovery: checking for new specs")
        try:
            _ib = "main"
            if self._full_config is not None:
                _ib = self._full_config.workspace.integration_branch
            await run_sync_barrier_sequence(
                state=state,
                sync_interval=self._config.effective_sync_interval,
                repo_root=self._repo_root,
                integration_branch=_ib,
                emit_audit=self._emit_audit,
                specs_dir=self._specs_dir,
                hot_load_enabled=self._config.hot_load,
                hot_load_fn=self._hot_load_new_specs,
                sync_plan_fn=self._sync_plan_statuses,
                barrier_callback=None,
                knowledge_db_conn=self._knowledge_db_conn,
                reload_config_fn=self._reload_config,
            )
        except Exception:
            logger.error("End-of-run discovery barrier failed", exc_info=True)
            return False
        if self._graph_sync is None:
            return False
        ready = self._graph_sync.ready_tasks()
        if ready:
            logger.info("End-of-run discovery found %d new ready task(s)", len(ready))
            return True
        return False

    async def _hot_load_new_specs(self, state: ExecutionState) -> bool:
        """Hot-load new specs into the graph.

        Returns True if new specs were discovered and incorporated
        (graph was mutated), False otherwise.
        """
        assert self._specs_dir is not None  # noqa: S101
        assert self._graph_sync is not None  # noqa: S101
        assert self._graph is not None  # noqa: S101

        _ib = "main"
        if self._full_config is not None:
            _ib = self._full_config.workspace.integration_branch
        prev_node_count = len(self._graph.nodes)
        self._graph, self._graph_sync = await hot_load_into_graph(
            specs_dir=self._specs_dir,
            graph=self._graph,
            graph_sync=self._graph_sync,
            state=state,
            repo_root=self._repo_root,
            integration_branch=_ib,
            knowledge_db_conn=self._knowledge_db_conn,
            archetypes_config=self._archetypes_config,
        )
        self._dispatch_mgr.set_graph(self._graph)
        self._dispatch_mgr.set_graph_sync(self._graph_sync)
        return len(self._graph.nodes) > prev_node_count

    def _reload_config(self) -> None:
        result = self._config_reloader.reload(
            current_config=self._config,
            circuit=self._circuit,
            sink=self._sink,
            run_id=self._run_id,
        )
        if result is None:
            return
        self._config = result.config
        self._circuit = result.circuit
        self._archetypes_config = result.archetypes
        self._planning_config = result.planning

    async def _shutdown(
        self,
        state: ExecutionState,
        error_tracker: dict[str, str | None] | None = None,
    ) -> None:
        if self._dispatch_mgr.parallel_runner is not None:
            unprocessed = await self._dispatch_mgr.parallel_runner.cancel_all()
            if unprocessed and self._result_handler is not None:
                _et = error_tracker or {}
                for record in unprocessed:
                    actual_attempt = self._result_handler.get_attempt_count(record.node_id) or record.attempt
                    if record.attempt != actual_attempt:
                        from dataclasses import replace as _dc_replace

                        record = _dc_replace(record, attempt=actual_attempt)
                    try:
                        self._result_handler.process(record, actual_attempt, state, _et)
                    except Exception:
                        logger.debug(
                            "Failed to persist interrupted session record for %s",
                            record.node_id,
                            exc_info=True,
                        )

        state.run_status = RunStatus.INTERRUPTED
        summary = self._graph_sync.summary() if self._graph_sync else {}
        completed = summary.get("completed", 0)
        total = sum(summary.values()) if summary else 0
        remaining = total - completed
        logger.info(
            "Execution interrupted. %d/%d tasks completed, %d remaining. Resume with: agent-fox code",
            completed,
            total,
            remaining,
        )
