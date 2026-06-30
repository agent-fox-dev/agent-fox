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
import re
import signal
import subprocess
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from agentfox.platform.protocol import PlatformProtocol

from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    OrchestratorConfig,
    PlanningConfig,
    RoutingConfig,
)
from agentfox.core.errors import PlanError
from agentfox.core.models import ModelTier  # noqa: F401 — used by assess_node() implementation (task group 9)
from agentfox.engine.audit_helpers import emit_audit_event
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
from agentfox.engine.result_handler import SessionResultHandler
from agentfox.engine.state import ExecutionState, RunStatus
from agentfox.engine.state_manager import (
    StateManager,
    build_edges_dict,
    defer_ready_reviews,
    init_attempt_tracker,
    init_error_tracker,
    load_or_init_state,
    reset_blocked_tasks,
    reset_in_progress_tasks,
)
from agentfox.graph.injection import ensure_graph_archetypes
from agentfox.graph.persistence import load_plan, save_plan
from agentfox.graph.types import TaskGraph
from agentfox.knowledge.audit import (
    AuditEventType,
    AuditJsonlSink,
    AuditSeverity,
    enforce_audit_retention,
    generate_run_id,
)
from agentfox.knowledge.sink import SinkDispatcher
from agentfox.ui.progress import TaskCallback

logger = logging.getLogger(__name__)

_defer_ready_reviews = defer_ready_reviews


class AssessmentManager:
    """Manages escalation ladders for nodes based on archetype default tiers.

    Coordinates complexity assessment for nodes, integrating
    ComplexityAssessor with dispatch logic.

    Requirements: 15-REQ-1.6, 15-REQ-1.7, 15-REQ-4.1, 15-REQ-4.3,
                  15-REQ-4.4, 15-REQ-6.1
    """

    def __init__(
        self,
        retries_before_escalation: int | None = None,
        config: Any = None,
        *,
        client: Any = None,
    ) -> None:
        self.ladders: dict[str, Any] = {}
        self._config = config
        self._assessor: Any = None

        # Resolve retries_before_escalation from config or parameter.
        if retries_before_escalation is not None:
            self.retries_before_escalation = retries_before_escalation
        elif config is not None and hasattr(config, "retries_before_escalation"):
            self.retries_before_escalation = config.retries_before_escalation
        else:
            self.retries_before_escalation = 1

        # 15-REQ-1.7: When client is non-None, instantiate ComplexityAssessor.
        # Handle both AgentFoxConfig (config.routing.assessor_model) and
        # RoutingConfig (config.assessor_model) for assessor configuration.
        if client is not None:
            from agentfox.core.complexity import ComplexityAssessor

            if hasattr(config, "routing"):
                # AgentFoxConfig case: assessor fields live under config.routing
                routing = config.routing
                assessor_model = getattr(routing, "assessor_model", "claude-haiku-4-5")
                confidence_threshold = getattr(routing, "confidence_threshold", 0.6)
            else:
                # RoutingConfig or test mock: fields directly on config
                assessor_model = getattr(config, "assessor_model", "claude-haiku-4-5")
                confidence_threshold = getattr(config, "confidence_threshold", 0.6)
            self._assessor = ComplexityAssessor(
                client=client,
                assessor_model=assessor_model,
                confidence_threshold=confidence_threshold,
            )
        # 15-REQ-1.6 / 15-REQ-12.5: client=None → no assessor, no log

    def _get_base_tier_variant(
        self,
        archetype: str,
        mode: str | None,
    ) -> tuple[str, str | None]:
        """Resolve the archetype registry default tier and variant.

        Returns (base_tier, base_variant) from the ARCHETYPE_REGISTRY.
        Falls back to ('STANDARD', 'standard') if resolution fails.
        """
        try:
            from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

            entry = ARCHETYPE_REGISTRY.get(archetype)
            if entry is not None:
                resolved = resolve_effective_config(entry, mode=mode)
                return (resolved.default_model_tier, "standard")
        except Exception:
            pass
        return ("STANDARD", "standard")

    def _resolve_configured_tier_variant(
        self,
        archetype: str,
        mode: str | None,
        base_tier: str,
        base_variant: str | None,
    ) -> tuple[str, str | None]:
        """Resolve the explicitly configured tier/variant via layers 1-3.

        When AgentFoxConfig is available, uses resolve_model_tier() and
        resolve_model_variant() which include config layers 1-3. Falls back
        to base_tier/base_variant when full config is not available.
        """
        try:
            if hasattr(self._config, "archetypes"):
                from agentfox.engine.sdk_params import resolve_model_tier, resolve_model_variant

                tier = resolve_model_tier(self._config, archetype, mode=mode)
                variant = resolve_model_variant(self._config, archetype, mode=mode)
                return (tier, variant)
        except Exception:
            pass
        return (base_tier, base_variant)

    def is_explicitly_configured(
        self,
        archetype: str,
        mode: str | None,
    ) -> bool:
        """Check whether a tier is explicitly set for archetype/mode.

        Walks config resolution layers 1–3:
          Layer 1: mode-level override (skipped when mode is None)
          Layer 2: per-archetype override
          Layer 3: legacy dict override

        Returns True if any layer returns a non-None value.
        Returns False if all three layers return None (no explicit override).

        Mirrors the layer-resolution logic of resolve_model_tier() in
        engine/sdk_params.py without falling through to registry defaults.

        Requirements: 15-REQ-6.1, 15-REQ-6.2, 15-REQ-6.E1
        """
        config = self._config

        # Need AgentFoxConfig to check archetype overrides.
        # RoutingConfig and other non-full-config objects have no archetypes.
        if not hasattr(config, "archetypes"):
            return False

        archetypes_cfg = config.archetypes
        override = archetypes_cfg.overrides.get(archetype)

        # Layer 1: mode-level override (skipped entirely when mode is None)
        # 15-REQ-6.2: None mode never causes layer 1 to return True
        if mode is not None and override is not None:
            mode_override = override.modes.get(mode)
            if mode_override is not None and mode_override.model_tier:
                # 15-REQ-6.E1: Return True immediately without checking layers 2-3
                return True

        # Layer 2: per-archetype override (unified table)
        if override is not None and override.model_tier:
            return True

        # Layer 3: legacy dict override
        if archetypes_cfg.models.get(archetype):
            return True

        return False

    async def assess_node(
        self,
        node_id: str,
        archetype: str,
        mode: str | None = None,
        node_body: str | None = None,
        previous_failure: str | None = None,
        pre_assessed: Any = None,
    ) -> Any:
        """Assess node complexity and return an EscalationLadder.

        Evaluates assessment eligibility in order:
          1. No assessor → base fallback (no log)
          2. Missing/empty node_body → base fallback with DEBUG log
          3. Explicit config override → configured tier with DEBUG log
          4. pre_assessed non-None → adapter + apply_assessment()
          5. LLM assessment → ComplexityAssessor.assess() + apply_assessment()

        Paths 3-5 raise NotImplementedError until task group 9.

        Requirements: 15-REQ-4.1, 15-REQ-4.3, 15-REQ-4.4, 15-REQ-4.E1,
                      15-REQ-5.1, 15-REQ-12.2, 15-REQ-12.3, 15-REQ-12.5
        """
        from agentfox.core.escalation import EscalationLadder

        # Path 1: No assessor (client=None) → base fallback, no log
        if self._assessor is None:
            base_tier, base_variant = self._get_base_tier_variant(archetype, mode)
            ladder = EscalationLadder(
                starting_tier=ModelTier(base_tier),
                tier_ceiling=ModelTier.ADVANCED,
                retries_before_escalation=self.retries_before_escalation,
                starting_variant=base_variant,
            )
            self.ladders[node_id] = ladder
            return ladder

        # Path 2: Missing/empty node_body → base fallback with DEBUG log
        if not node_body:
            label = "absent" if node_body is None else "empty"
            logger.debug("Node %s has %s body; falling back to base tier", node_id, label)
            base_tier, base_variant = self._get_base_tier_variant(archetype, mode)
            ladder = EscalationLadder(
                starting_tier=ModelTier(base_tier),
                tier_ceiling=ModelTier.ADVANCED,
                retries_before_escalation=self.retries_before_escalation,
                starting_variant=base_variant,
            )
            self.ladders[node_id] = ladder
            return ladder

        # Resolve base tier/variant from the registry (used as floor for all paths).
        base_tier, base_variant = self._get_base_tier_variant(archetype, mode)

        # Path 3: Explicit config override → skip LLM, use resolved tier/variant.
        # 15-REQ-6.3, 15-REQ-12.4
        if self.is_explicitly_configured(archetype, mode):
            # Resolve the configured tier (layers 1-3) via full resolution if possible.
            configured_tier, configured_variant = self._resolve_configured_tier_variant(
                archetype, mode, base_tier, base_variant
            )
            logger.debug(
                "Skipping complexity assessment for node %s: archetype=%s mode=%s "
                "has explicit config override; resolved tier=%s",
                node_id,
                archetype,
                mode,
                configured_tier,
            )
            ladder = EscalationLadder(
                starting_tier=ModelTier(configured_tier),
                tier_ceiling=ModelTier.ADVANCED,
                retries_before_escalation=self.retries_before_escalation,
                starting_variant=configured_variant,
            )
            self.ladders[node_id] = ladder
            return ladder

        # Path 4: pre_assessed non-None → adapter + apply_assessment, no LLM call.
        # Path 5: LLM assessment → ComplexityAssessor.assess() + apply_assessment.
        # 15-REQ-11.3, 15-REQ-11.4
        from agentfox.core.complexity import apply_assessment

        if pre_assessed is not None:
            # 15-REQ-11.3, 15-PROP-8: bypass LLM, use pre_assessed via adapter
            from agentfox.core.complexity import assessed_complexity_to_recommendation

            recommendation = assessed_complexity_to_recommendation(pre_assessed)
            confidence = recommendation.confidence
            rationale = recommendation.rationale
        else:
            # 15-REQ-11.4: Standard LLM assessment path
            recommendation = await self._assessor.assess(
                node_body=node_body,
                archetype=archetype,
                mode=mode,
                base_tier=base_tier,
                base_variant=base_variant,
                previous_failure=previous_failure,
            )
            confidence = recommendation.confidence
            rationale = recommendation.rationale

        effective_tier, effective_variant = apply_assessment(
            recommendation,
            base_tier,
            base_variant,
            self._assessor.confidence_threshold,
        )

        # 15-REQ-12.2: Emit structured DEBUG log on successful assessment.
        logger.debug(
            "Complexity assessment for node %s: archetype=%s mode=%s "
            "effective_tier=%s effective_variant=%s confidence=%s rationale=%s",
            node_id,
            archetype,
            mode,
            effective_tier,
            effective_variant,
            confidence,
            rationale,
        )

        ladder = EscalationLadder(
            starting_tier=ModelTier(effective_tier),
            tier_ceiling=ModelTier.ADVANCED,
            retries_before_escalation=self.retries_before_escalation,
            starting_variant=effective_variant,
        )
        self.ladders[node_id] = ladder
        return ladder


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
        client: Any | None = None,
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
        # 15-REQ-4.2: Pass the configured Anthropic client to AssessmentManager so
        # complexity assessment is enabled when a real client is available.
        self._routing = AssessmentManager(
            retries_before_escalation=self._resolve_retries_before_escalation(_rc),
            config=full_config or AgentFoxConfig(),
            client=client,
        )

        self._state_mgr = StateManager(
            knowledge_db_conn=knowledge_db_conn,
            task_callback=task_callback,
            max_blocked_fraction=config.max_blocked_fraction,
        )

        self._dispatch_mgr = DispatchManager(
            session_runner_factory=session_runner_factory,
            inter_session_delay=float(config.inter_session_delay),
            parallel=config.parallel,
            routing=self._routing,
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

    def _resolve_retries_before_escalation(self, routing_config: RoutingConfig) -> int:
        routing_retries = routing_config.retries_before_escalation
        orch_retries = self._config.max_retries
        if routing_retries != 1:
            return routing_retries
        if orch_retries != 2:
            logger.warning(
                "orchestrator.max_retries is deprecated; use "
                "routing.retries_before_escalation instead. "
                "Using max_retries=%d as fallback.",
                orch_retries,
            )
            return min(orch_retries, 3)
        return routing_retries

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

    # -- Init / Run / Watch / Shutdown --------------------------------------

    def _init_run(
        self,
    ) -> tuple[ExecutionState, dict[str, int], dict[str, str | None]] | ExecutionState:
        self._run_id = generate_run_id()
        logger.debug("Audit run ID: %s", self._run_id)

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
            routing_ladders=self._routing.ladders,
            retries_before_escalation=self._routing.retries_before_escalation,
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

        return state, init_attempt_tracker(state), init_error_tracker(state)

    async def run(self) -> ExecutionState:
        """Execute the full orchestration loop."""
        run_start_time = datetime.now(UTC)
        self._watch_poll_count = 0
        result = self._init_run()
        if isinstance(result, ExecutionState):
            return result
        state, attempt_tracker, error_tracker = result

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
                    await self._shutdown(state, attempt_tracker, error_tracker)
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
                    state.run_status = RunStatus.COMPLETED
                    return state

                if self._is_parallel and self._dispatch_mgr.parallel_runner is not None:
                    await self._parallel_dispatcher.dispatch(ready, state, attempt_tracker, error_tracker)
                else:
                    first_dispatch = await self._serial_dispatcher.dispatch(
                        ready,
                        state,
                        attempt_tracker,
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

    async def _run_sync_barrier_if_needed(self, state: ExecutionState) -> None:
        if self._config.sync_interval == 0:
            return
        completed_count = _count_node_status(state.node_states, "completed")
        if not should_trigger_barrier(completed_count, self._config.sync_interval):
            return
        _ib = "main"
        if self._full_config is not None:
            _ib = self._full_config.workspace.integration_branch
        await run_sync_barrier_sequence(
            state=state,
            sync_interval=self._config.sync_interval,
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
                sync_interval=self._config.sync_interval,
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

    async def _hot_load_new_specs(self, state: ExecutionState) -> None:
        assert self._specs_dir is not None  # noqa: S101
        assert self._graph_sync is not None  # noqa: S101
        assert self._graph is not None  # noqa: S101

        _ib = "main"
        if self._full_config is not None:
            _ib = self._full_config.workspace.integration_branch
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
        attempt_tracker: dict[str, int] | None = None,
        error_tracker: dict[str, str | None] | None = None,
    ) -> None:
        if self._dispatch_mgr.parallel_runner is not None:
            unprocessed = await self._dispatch_mgr.parallel_runner.cancel_all()
            if unprocessed and self._result_handler is not None:
                _at = attempt_tracker or {}
                _et = error_tracker or {}
                for record in unprocessed:
                    actual_attempt = _at.get(record.node_id, record.attempt)
                    if record.attempt != actual_attempt:
                        from dataclasses import replace as _dc_replace

                        record = _dc_replace(record, attempt=actual_attempt)
                    try:
                        self._result_handler.process(record, actual_attempt, state, _at, _et)
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


# ---------------------------------------------------------------------------
# Issue summary posting (inlined from engine/issue_summary.py)
# ---------------------------------------------------------------------------

# GitHub issue URL pattern:
#   https://github.com/{owner}/{repo}/issues/{number}
_GITHUB_ISSUE_RE = re.compile(r"^https://github\.com/(?P<owner>[^/\s]+)/(?P<repo>[^/\s]+)/issues/(?P<number>\d+)\s*$")

# Source line pattern within ## Source section:
#   Source: <value>
_SOURCE_LINE_RE = re.compile(r"^Source:\s*(.+)$")


@dataclass(frozen=True)
class SourceIssue:
    """Parsed issue reference from a prd.md Source section.

    Requirements: 108-REQ-1.2, 108-REQ-1.3
    """

    forge: str  # "github" (extensible to "gitlab", "linear", etc.)
    owner: str  # e.g., "agent-fox-dev"
    repo: str  # e.g., "agent-fox"
    issue_number: int  # e.g., 359


def parse_source_url(prd_path: Path) -> SourceIssue | None:
    """Extract the issue URL from prd.md's ## Source section.

    Returns None (never raises) if:
    - prd.md does not exist
    - ## Source section is missing
    - Source: line is absent within the section
    - Source line value does not match any known issue URL pattern

    Requirements: 108-REQ-1.1, 108-REQ-1.2, 108-REQ-1.3,
                  108-REQ-1.E1, 108-REQ-1.E2, 108-REQ-1.E3
    """
    try:
        # 108-REQ-1.E3: prd.md does not exist
        if not prd_path.exists():
            return None

        text = prd_path.read_text(encoding="utf-8")
        lines = text.splitlines()

        in_source_section = False
        for line in lines:
            stripped = line.strip()

            # Detect the ## Source section heading (exact match)
            if stripped == "## Source":
                in_source_section = True
                continue

            # Stop at the next markdown heading after entering the section
            if in_source_section and stripped.startswith("#"):
                break

            # Look for a "Source: <value>" line within the section
            if in_source_section:
                source_match = _SOURCE_LINE_RE.match(stripped)
                if source_match:
                    url = source_match.group(1).strip()
                    # Try GitHub issue URL pattern
                    gh_match = _GITHUB_ISSUE_RE.match(url)
                    if gh_match:
                        return SourceIssue(
                            forge="github",
                            owner=gh_match.group("owner"),
                            repo=gh_match.group("repo"),
                            issue_number=int(gh_match.group("number")),
                        )
                    # URL present but matches no known forge — return None
                    # 108-REQ-1.E2: non-issue-URL source
                    return None

        # 108-REQ-1.E1: ## Source missing or Source: line absent
        return None

    except Exception:
        # 108-REQ-1.3: Pure function — never propagate exceptions
        logger.debug("parse_source_url encountered an error", exc_info=True)
        return None


def _get_integration_head(repo_root: Path, branch: str) -> str:
    """Return the current integration branch HEAD SHA.

    Runs ``git rev-parse <branch>`` in the given repository root.
    Returns ``"unknown"`` if the command fails for any reason.

    Requirements: 108-REQ-6.1, 108-REQ-6.E1
    """
    try:
        result = subprocess.run(
            ["git", "rev-parse", branch],
            capture_output=True,
            text=True,
            cwd=str(repo_root),
        )
        if result.returncode == 0:
            return result.stdout.strip()
    except Exception:
        logger.debug("git rev-parse %s failed", branch, exc_info=True)
    return "unknown"


def build_summary_comment(
    spec_name: str,
    commit_sha: str,
    tasks_path: Path,
    branch: str = "main",
) -> str:
    """Construct the Markdown comment body for the originating issue.

    Includes the spec name, the integration branch HEAD commit SHA, a bulleted list
    of task group titles derived from tasks.md, and an auto-generated footer.

    Requirements: 108-REQ-3.1, 108-REQ-3.2, 108-REQ-3.3, 108-REQ-3.4
    """
    from agentfox.spec.parser import parse_tasks  # noqa: PLC0415

    # Extract task group titles from spec directory
    group_lines: list[str] = []
    try:
        spec_dir = tasks_path.parent
        if spec_dir.is_dir():
            groups = parse_tasks(spec_dir)
            group_lines = [f"- {g.title}" for g in groups]
    except Exception:
        logger.debug("Failed to parse tasks for summary comment", exc_info=True)

    task_section = "\n".join(group_lines) if group_lines else "*(no task groups found)*"

    return (
        f"## Spec Implemented\n\n"
        f"Spec `{spec_name}` has been fully implemented and merged to `{branch}`.\n\n"
        f"**Commit:** `{commit_sha}`\n\n"
        f"### Task Groups\n\n"
        f"{task_section}\n\n"
        f"---\n"
        f"*Auto-generated by agent-fox.*"
    )


async def post_issue_summaries(
    platform: PlatformProtocol,
    specs_dir: Path,
    completed_specs: set[str],
    already_posted: set[str],
    repo_root: Path,
    integration_branch: str = "main",
) -> set[str]:
    """Post summary comments for newly completed specs.

    Iterates specs that are in ``completed_specs`` but not yet in
    ``already_posted``, extracts the originating issue URL from each
    spec's ``prd.md``, and posts a roll-up summary comment to that issue.

    Skips specs that:
    - have no valid source URL (108-REQ-1.E1, 108-REQ-1.E2)
    - are already in ``already_posted`` (108-REQ-2.E1)
    - have a forge type that doesn't match the platform (108-REQ-4.E2)

    Handles ``add_issue_comment()`` failures gracefully: logs a warning
    and continues without affecting the run status (108-REQ-4.E1).

    Returns:
        Set of spec names for which a comment was successfully posted.

    Requirements: 108-REQ-2.1, 108-REQ-2.2, 108-REQ-2.E1,
                  108-REQ-4.1, 108-REQ-4.E1, 108-REQ-4.E2
    """
    newly_completed = completed_specs - already_posted
    posted: set[str] = set()

    from agentfox.platform.labels import LABEL_IMPLEMENTED

    for spec_name in sorted(newly_completed):
        prd_path = specs_dir / spec_name / "prd.md"
        source_issue = parse_source_url(prd_path)

        if source_issue is None:
            logger.debug("No valid source URL for spec '%s'; skipping", spec_name)
            continue

        # 108-REQ-4.E2: Skip when platform forge type doesn't match source forge
        platform_forge = getattr(platform, "forge_type", None)
        if platform_forge != source_issue.forge:
            logger.info(
                "Skipping issue summary for spec '%s': source forge='%s', platform forge='%s'",
                spec_name,
                source_issue.forge,
                platform_forge,
            )
            continue

        # Issue #648: use af:implemented label as durable dedup marker
        try:
            issue = await platform.get_issue(source_issue.issue_number)
            if LABEL_IMPLEMENTED in issue.labels:
                logger.debug(
                    "Issue #%d already has '%s' label; skipping spec '%s'",
                    source_issue.issue_number,
                    LABEL_IMPLEMENTED,
                    spec_name,
                )
                posted.add(spec_name)
                continue
        except Exception:
            logger.debug(
                "Could not check labels for issue #%d; proceeding with post",
                source_issue.issue_number,
                exc_info=True,
            )

        commit_sha = _get_integration_head(repo_root, integration_branch)
        tasks_path = specs_dir / spec_name / "tasks.md"
        body = build_summary_comment(spec_name, commit_sha, tasks_path, integration_branch)

        try:
            await platform.add_issue_comment(source_issue.issue_number, body)
            posted.add(spec_name)
            logger.info(
                "Posted issue summary for spec '%s' to issue #%d",
                spec_name,
                source_issue.issue_number,
            )
        except Exception:
            # 108-REQ-4.E1: Graceful failure — warn and continue
            logger.warning(
                "Failed to post issue summary for spec '%s'",
                spec_name,
                exc_info=True,
            )
            continue

        # Issue #636: assign af:implemented label after successful comment
        try:
            await platform.assign_label(source_issue.issue_number, LABEL_IMPLEMENTED)
            logger.info(
                "Assigned '%s' label to issue #%d for spec '%s'",
                LABEL_IMPLEMENTED,
                source_issue.issue_number,
                spec_name,
            )
        except Exception:
            logger.warning(
                "Failed to assign '%s' label for spec '%s'",
                LABEL_IMPLEMENTED,
                spec_name,
                exc_info=True,
            )

    return posted
