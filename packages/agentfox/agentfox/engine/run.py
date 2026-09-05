"""Backing module for the ``code`` CLI command.

Configures and runs the orchestrator, returning an ``ExecutionState``
(or a lightweight result with ``status`` for interrupted runs).

This module can be called without the Click framework.

Requirements: 59-REQ-4.1, 59-REQ-4.2, 59-REQ-4.3, 59-REQ-4.E1,
              06-REQ-5.2, 06-REQ-6.2, 06-REQ-7.3
"""

from __future__ import annotations

import logging
import warnings
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import TYPE_CHECKING, Any

from afaudit.postmortem import build_postmortem, should_dump, write_postmortem
from afaudit.sink import SinkDispatcher

from agentfox.engine.engine import Orchestrator
from agentfox.engine.state import ExecutionState, RunStatus
from agentfox.knowledge.db import ContextKnowledgeDB, open_knowledge_store
from agentfox.knowledge.duckdb_sink import DuckDBSink
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

if TYPE_CHECKING:
    from agentfox.core.config import AgentFoxConfig, OrchestratorConfig

logger = logging.getLogger(__name__)

# Callback type aliases for progress display integration.
ActivityCallback = Callable[..., Any]
TaskCallback = Callable[..., Any]


@dataclass(frozen=True)
class InterruptedResult:
    """Lightweight result returned when execution is interrupted."""

    status: str = "interrupted"


def _stalled_result() -> ExecutionState:
    """Return a minimal ExecutionState with STALLED status for early aborts."""
    now = datetime.now(UTC).isoformat()
    return ExecutionState(
        plan_hash="",
        node_states={},
        run_status=RunStatus.STALLED,
        started_at=now,
        updated_at=now,
    )


def _apply_overrides(
    config: OrchestratorConfig,
    max_cost: float | None = None,
    max_sessions: int | None = None,
    watch_interval: int | None = None,
    parallel: int | None = None,
) -> OrchestratorConfig:
    """Return a new OrchestratorConfig with CLI overrides applied.

    Only overrides fields that were explicitly provided (not None).
    All non-overridden fields are preserved from the original config.

    Requirements: 16-REQ-2.1, 16-REQ-2.3, 16-REQ-2.4, 16-REQ-2.5,
                  70-REQ-3.3
    """
    from agentfox.core.config import OrchestratorConfig as OC

    overrides: dict[str, object] = {}
    if max_cost is not None:
        overrides["max_cost"] = max_cost
    if max_sessions is not None:
        overrides["max_sessions"] = max_sessions
    if watch_interval is not None:
        overrides["watch_interval"] = watch_interval
    if parallel is not None:
        overrides["parallel"] = parallel
    if overrides:
        merged = config.model_dump()
        merged.update(overrides)
        return OC.model_validate(merged)
    return config


def _setup_infrastructure(
    config: AgentFoxConfig,
    *,
    activity_callback: ActivityCallback | None = None,
) -> dict[str, Any]:
    """Set up knowledge DB, sinks, and other infrastructure.

    Returns a dict of infrastructure components needed by the orchestrator.
    This is separated from run_code so the orchestrator construction can
    be tested independently.

    Requirements: 108-REQ-5.1
    """
    from afaudit.constants import AUDIT_DIR

    from agentfox.engine.session_lifecycle import NodeSessionRunner
    from agentfox.workspace.platform_factory import create_platform_safe

    # Create DuckDB sink for session outcome recording
    sink_dispatcher = SinkDispatcher()
    knowledge_db = open_knowledge_store(config.knowledge, read_only=False)
    sink_dispatcher.add(DuckDBSink(knowledge_db.connection))

    # 06-REQ-7.3: Derive a cursor from the primary connection for context
    # assembly. DuckDB disallows opening a second connection to the same
    # file with a different read_only flag, so we use a cursor instead.
    # Cursors support concurrent SELECT queries without contending with
    # write operations on the primary connection.
    context_knowledge_db = ContextKnowledgeDB(knowledge_db.connection.cursor())

    # Attach agent trace sink unconditionally so that trace-based transcript
    # reconstruction is available for knowledge extraction (113-REQ-1.1).
    from afaudit.trace import AgentTraceSink

    sink_dispatcher.add(AgentTraceSink(AUDIT_DIR, ""))

    # 115-REQ-10.1: Construct FoxKnowledgeProvider with config
    knowledge_provider = FoxKnowledgeProvider(knowledge_db, config.knowledge.provider)

    def session_runner_factory(
        node_id: str,
        *,
        archetype: str = "coder",
        mode: str | None = None,
        instances: int = 1,
        run_id: str = "",
        timeout_override: int | None = None,
        max_turns_override: int | None = None,
    ) -> Any:
        """Create a session runner for the given node."""
        return NodeSessionRunner(
            node_id,
            config,
            archetype=archetype,
            mode=mode,
            instances=instances,
            sink_dispatcher=sink_dispatcher,
            knowledge_db=knowledge_db,
            context_knowledge_db=context_knowledge_db,
            knowledge_provider=knowledge_provider,
            activity_callback=activity_callback,
            run_id=run_id,
            timeout_override=timeout_override,
            max_turns_override=max_turns_override,
            trace_enabled=True,
        )

    # 108-REQ-5.1: Create platform instance (None if not configured)
    platform = None
    try:
        platform = create_platform_safe(config, Path.cwd())
    except Exception:
        logger.debug("create_platform_safe failed; proceeding without platform", exc_info=True)

    anthropic_client = None
    try:
        from agentfox.core.client import create_async_anthropic_client

        anthropic_client = create_async_anthropic_client()
    except Exception:
        logger.debug("Failed to create Anthropic client for complexity assessment", exc_info=True)

    return {
        "sink_dispatcher": sink_dispatcher,
        "knowledge_db": knowledge_db,
        "context_knowledge_db": context_knowledge_db,
        "knowledge_provider": knowledge_provider,
        "session_runner_factory": session_runner_factory,
        "audit_dir": AUDIT_DIR,
        "platform": platform,
        "anthropic_client": anthropic_client,
    }


def _run_startup_migrations(
    knowledge_db: Any,
    specs_path: Path,
    project_root: Path,
) -> None:
    """Run legacy file migrations at orchestrator startup.

    Migrates legacy review.md/verification.md files into DuckDB using the
    read-write connection, before any sessions are dispatched.

    Errors on individual specs are logged and skipped — they do not abort
    the startup sequence.

    Requirements: 06-REQ-5.2, 06-REQ-5.E1
    """
    from agentfox.session.context import _migrate_legacy_files

    conn = knowledge_db.connection

    # Migrate legacy files for each spec (06-REQ-5.2)
    if specs_path.is_dir():
        for spec_dir in sorted(specs_path.iterdir()):
            if not spec_dir.is_dir():
                continue
            spec_name = spec_dir.name
            try:
                _migrate_legacy_files(conn, spec_dir, spec_name)
            except Exception:
                # 06-REQ-5.E1: Log error with spec context and continue
                logger.warning(
                    "Failed to migrate legacy files for spec %s, continuing",
                    spec_name,
                    exc_info=True,
                )


async def run_code(
    config: AgentFoxConfig,
    *,
    max_cost: float | None = None,
    max_sessions: int | None = None,
    watch: bool = False,
    watch_interval: int | None = None,
    parallel: int | None = None,
    specs_dir: Path | None = None,
    activity_callback: ActivityCallback | None = None,
    task_callback: TaskCallback | None = None,
) -> ExecutionState | InterruptedResult:
    """Configure and run the orchestrator.

    Returns the final ``ExecutionState`` on normal completion, or an
    ``InterruptedResult`` when a ``KeyboardInterrupt`` is caught.

    This function can be called without the Click framework.

    Args:
        config: Loaded AgentFoxConfig.
        watch: Keep running and poll for new specs.
        watch_interval: Seconds between watch polls.
        parallel: Override for parallel session count.
        specs_dir: Path to specs directory (default: .specs).
        activity_callback: Optional callback for tool activity display.
        task_callback: Optional callback for task event display.

    Returns:
        ExecutionState on success, InterruptedResult on interruption.

    Requirements: 59-REQ-4.1, 59-REQ-4.2, 59-REQ-4.3, 59-REQ-4.E1
    """
    # Apply CLI overrides to OrchestratorConfig
    try:
        orch_config = _apply_overrides(
            config.orchestrator,
            max_cost=max_cost,
            max_sessions=max_sessions,
            watch_interval=watch_interval,
            parallel=parallel,
        )
    except Exception:
        orch_config = config.orchestrator

    from agentfox.core.config import resolve_spec_root

    agent_dir = Path(".agent-fox")
    specs_path = Path(specs_dir) if specs_dir else resolve_spec_root(config, Path.cwd())

    # Set up infrastructure (knowledge DB, sinks, fact cache, etc.)
    infra: dict[str, Any] | None = None
    try:
        infra = _setup_infrastructure(
            config,
            activity_callback=activity_callback,
        )
    except Exception:
        logger.warning("Infrastructure setup failed", exc_info=True)

    # 06-REQ-5.2, 06-REQ-6.2: Run legacy migrations at startup with the
    # read-write connection, before any sessions are dispatched.
    if infra is not None:
        try:
            _run_startup_migrations(
                infra["knowledge_db"],
                specs_path,
                Path.cwd(),
            )
        except Exception:
            logger.warning("Startup migrations failed", exc_info=True)

    # Suppress noisy third-party warnings
    warnings.filterwarnings("ignore", module=r"huggingface_hub\..*")
    warnings.filterwarnings("ignore", module=r"sentence_transformers\..*")

    # 118-REQ-1.1, 118-REQ-1.2, 118-REQ-1.3: Pre-run workspace health gate
    try:
        from agentfox.workspace.health import (
            check_workspace_health,
            force_clean_workspace,
            format_health_diagnostic,
        )

        repo_root = agent_dir.parent
        health_report = await check_workspace_health(repo_root)

        if health_report.has_issues:
            if config.workspace.force_clean:
                logger.warning("Pre-run health check found issues; force-clean enabled, cleaning workspace")
                cleaned = await force_clean_workspace(repo_root, health_report)
                if cleaned.has_issues:
                    diag = format_health_diagnostic(cleaned)
                    logger.error("Force-clean could not resolve all issues:\n%s", diag)
                    return _stalled_result()
            else:
                diag = format_health_diagnostic(health_report)
                logger.error("Pre-run workspace health check failed:\n%s", diag)
                return _stalled_result()
        else:
            logger.info("Pre-run workspace health check: clean")
    except Exception:
        # 118-REQ-1.E2: Fail-open on unexpected errors
        logger.warning("Pre-run health gate raised an exception; proceeding", exc_info=True)

    try:
        if infra is None:
            raise RuntimeError("Cannot start orchestrator: infrastructure setup failed")

        # Build orchestrator kwargs
        orch_kwargs: dict[str, Any] = {
            "agent_dir": agent_dir,
            "specs_dir": specs_path,
            "watch": watch,
            "task_callback": task_callback,
            "routing_config": config.routing,
            "archetypes_config": config.archetypes,
            "planning_config": config.planning,
            "config_path": Path(".agent-fox/config.toml"),
            "full_config": config,
            "session_runner_factory": infra["session_runner_factory"],
            "sink_dispatcher": infra["sink_dispatcher"],
            "audit_dir": infra["audit_dir"],
            "audit_db_conn": infra["knowledge_db"].connection,
            "knowledge_db_conn": infra["knowledge_db"].connection,
            "platform": infra.get("platform"),
            "knowledge_provider": infra.get("knowledge_provider"),
            "client": infra.get("anthropic_client"),
        }

        orchestrator = Orchestrator(orch_config, **orch_kwargs)
        state: ExecutionState = await orchestrator.run()

        # 126-REQ-1.1, 126-REQ-1.E1, 126-REQ-2.E1: Generate post-mortem
        # for non-successful runs. Wrapped in try/except so failures in
        # post-mortem generation never block returning the state.
        try:
            if should_dump(state):
                from afaudit.constants import AUDIT_DIR

                pm = build_postmortem(state)
                pm_path = write_postmortem(pm, AUDIT_DIR)
                state.postmortem_path = str(pm_path)
        except Exception:
            logger.warning("Post-mortem generation failed", exc_info=True)

        return state

    except KeyboardInterrupt:
        # 59-REQ-4.E1: Return interrupted result instead of raising
        return InterruptedResult(status="interrupted")
    finally:
        if infra is not None:
            _cleanup_infrastructure(infra, config)


def _cleanup_infrastructure(infra: dict[str, Any], config: Any) -> None:
    """Clean up infrastructure resources."""
    knowledge_db = infra["knowledge_db"]

    # Close sinks and DB
    try:
        infra["sink_dispatcher"].close()
    except Exception:
        logger.warning("Sink dispatcher close failed", exc_info=True)
    # Close the context cursor wrapper if present
    context_knowledge_db = infra.get("context_knowledge_db")
    if context_knowledge_db is not None:
        try:
            context_knowledge_db.close()
        except Exception:
            logger.warning("Context knowledge DB close failed", exc_info=True)
    try:
        knowledge_db.close()
    except Exception:
        logger.warning("Knowledge DB close failed", exc_info=True)
