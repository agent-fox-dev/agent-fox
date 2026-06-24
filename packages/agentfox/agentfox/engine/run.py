"""Backing module for the ``code`` CLI command.

Configures and runs the orchestrator, returning an ``ExecutionState``
(or a lightweight result with ``status`` for interrupted runs).

This module can be called without the Click framework.

Requirements: 59-REQ-4.1, 59-REQ-4.2, 59-REQ-4.3, 59-REQ-4.E1,
              06-REQ-5.2, 06-REQ-6.2, 06-REQ-7.3
"""

from __future__ import annotations

import json
import logging
import warnings
from collections import Counter
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import TYPE_CHECKING, Any

from agentfox.engine.engine import Orchestrator
from agentfox.engine.state import ExecutionState, RunStatus
from agentfox.knowledge.db import open_knowledge_store
from agentfox.knowledge.duckdb_sink import DuckDBSink
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider
from agentfox.knowledge.sink import SinkDispatcher

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
    from agentfox.core.node_id import AUDIT_DIR
    from agentfox.engine.session_lifecycle import NodeSessionRunner
    from agentfox.nightshift.platform_factory import create_platform_safe

    # Create DuckDB sink for session outcome recording
    sink_dispatcher = SinkDispatcher()
    knowledge_db = open_knowledge_store(config.knowledge, read_only=False)
    sink_dispatcher.add(DuckDBSink(knowledge_db.connection))

    # 06-REQ-7.3: Open a separate read-only connection for session context
    # assembly.  This ensures assemble_context never holds a write lock and
    # can run concurrently with the orchestrator's write operations.
    # read-only conn for session context assembly; writes done at startup
    context_knowledge_db = None
    try:
        context_knowledge_db = open_knowledge_store(config.knowledge, read_only=True)
    except Exception:
        logger.warning(
            "Failed to open read-only knowledge store for context assembly; "
            "falling back to main connection",
            exc_info=True,
        )

    # Attach agent trace sink unconditionally so that trace-based transcript
    # reconstruction is available for knowledge extraction (113-REQ-1.1).
    from agentfox.knowledge.agent_trace import AgentTraceSink

    sink_dispatcher.add(AgentTraceSink(AUDIT_DIR, ""))

    # 115-REQ-10.1: Construct FoxKnowledgeProvider with config
    knowledge_provider = FoxKnowledgeProvider(knowledge_db, config.knowledge.provider)

    # Determine the read-only connection for context assembly (06-REQ-7.3).
    # Falls back to the main knowledge_db when a separate read-only
    # connection is unavailable (e.g. in-memory databases in tests).
    _context_db = context_knowledge_db if context_knowledge_db is not None else knowledge_db

    def session_runner_factory(
        node_id: str,
        *,
        archetype: str = "coder",
        mode: str | None = None,
        instances: int = 1,
        assessed_tier: Any = None,
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
            context_knowledge_db=_context_db,
            knowledge_provider=knowledge_provider,
            activity_callback=activity_callback,
            assessed_tier=assessed_tier,
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

    return {
        "sink_dispatcher": sink_dispatcher,
        "knowledge_db": knowledge_db,
        "context_knowledge_db": context_knowledge_db,
        "knowledge_provider": knowledge_provider,
        "session_runner_factory": session_runner_factory,
        "audit_dir": AUDIT_DIR,
        "platform": platform,
    }


def _run_startup_migrations(
    knowledge_db: Any,
    specs_path: Path,
    project_root: Path,
) -> None:
    """Run legacy file migrations and errata indexing at orchestrator startup.

    Migrates legacy review.md/verification.md files and indexes errata
    markdown files into DuckDB using the read-write connection, before any
    sessions are dispatched.

    Errors on individual specs or errata indexing are logged and skipped —
    they do not abort the startup sequence.

    Requirements: 06-REQ-5.2, 06-REQ-5.E1, 06-REQ-6.2, 06-REQ-6.E1
    """
    from agentfox.knowledge.errata import index_errata_from_markdown
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

    # Index errata markdown files (06-REQ-6.2)
    try:
        index_errata_from_markdown(conn, project_root)
    except Exception:
        # 06-REQ-6.E1: Log error and continue startup
        logger.warning(
            "Failed to index errata from markdown, continuing",
            exc_info=True,
        )


async def run_code(
    config: AgentFoxConfig,
    *,
    max_cost: float | None = None,
    max_sessions: int | None = None,
    watch: bool = False,
    watch_interval: int | None = None,
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

    # 06-REQ-5.2, 06-REQ-6.2: Run legacy migrations and errata indexing
    # at startup with the read-write connection, before any sessions are
    # dispatched.
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
        # Build orchestrator kwargs — use infra if available
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
        }

        if infra is not None:
            orch_kwargs.update(
                {
                    "session_runner_factory": infra["session_runner_factory"],
                    "sink_dispatcher": infra["sink_dispatcher"],
                    "audit_dir": infra["audit_dir"],
                    "audit_db_conn": infra["knowledge_db"].connection,
                    "knowledge_db_conn": infra["knowledge_db"].connection,
                    "platform": infra.get("platform"),
                    "knowledge_provider": infra.get("knowledge_provider"),
                }
            )

        orchestrator = Orchestrator(orch_config, **orch_kwargs)
        state: ExecutionState = await orchestrator.run()

        # 126-REQ-1.1, 126-REQ-1.E1, 126-REQ-2.E1: Generate post-mortem
        # for non-successful runs. Wrapped in try/except so failures in
        # post-mortem generation never block returning the state.
        try:
            if should_dump(state):
                from agentfox.core.node_id import AUDIT_DIR

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
    # Close the read-only context knowledge DB if it was opened separately
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


# ---------------------------------------------------------------------------
# Post-mortem dump (inlined from engine/postmortem.py)
# ---------------------------------------------------------------------------

SCHEMA_VERSION: int = 1

TRIGGER_STATUSES: frozenset[str] = frozenset({"stalled", "block_limit", "cost_limit", "session_limit"})


def should_dump(state: ExecutionState) -> bool:
    """Return True if the run status should trigger a post-mortem.

    Requirements: 126-REQ-1.1, 126-REQ-1.2, 126-REQ-1.3
    """
    status = state.run_status
    # Normalise StrEnum values to plain strings for comparison.
    if hasattr(status, "value"):
        status = status.value
    return status in TRIGGER_STATUSES


def build_postmortem(state: ExecutionState) -> dict[str, Any]:
    """Build a post-mortem dict from an ExecutionState.

    Requirements: 126-REQ-1.E2, 126-REQ-3.1 through 126-REQ-5.E1
    """
    # 126-REQ-1.E2: Fallback run_id when empty
    run_id = state.run_id
    if not run_id:
        now = datetime.now(UTC)
        run_id = now.strftime("%Y%m%d_%H%M%S_000000")

    run_status = state.run_status
    if hasattr(run_status, "value"):
        run_status = run_status.value

    # 126-REQ-3.3: Task summary counts from node_states
    task_summary = _build_task_summary(state.node_states)

    # 126-REQ-3.4, 126-REQ-5.2: Cost summary from state aggregates
    cost_summary = {
        "total_cost_usd": state.total_cost,
        "total_input_tokens": state.total_input_tokens,
        "total_output_tokens": state.total_output_tokens,
        "total_sessions": state.total_sessions,
    }

    # 126-REQ-4.1, 126-REQ-4.2, 126-REQ-4.E1: Blocked tasks
    blocked_tasks = _build_blocked_tasks(state.node_states, state.blocked_reasons)

    # 126-REQ-5.1: Session history
    session_history = _build_session_history(state.session_history)

    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "run_status": run_status,
        "started_at": state.started_at,
        "completed_at": state.updated_at,
        "task_summary": task_summary,
        "cost_summary": cost_summary,
        "blocked_tasks": blocked_tasks,
        "session_history": session_history,
    }


def write_postmortem(postmortem: dict[str, Any], audit_dir: Path) -> Path:
    """Write post-mortem JSON to audit_dir. Returns the file path.

    Requirements: 126-REQ-2.1, 126-REQ-2.2, 126-REQ-2.3
    """
    # 126-REQ-2.3: Create audit directory if missing
    audit_dir.mkdir(parents=True, exist_ok=True)

    run_id = postmortem.get("run_id", "unknown")
    filename = f"postmortem_{run_id}.json"
    path = audit_dir / filename

    # 126-REQ-2.2: Write valid JSON
    path.write_text(json.dumps(postmortem, indent=2))
    return path


# -- Internal helpers ---------------------------------------------------------


def _build_task_summary(node_states: dict[str, str]) -> dict[str, int]:
    """Build task_summary dict from node_states.

    Requirements: 126-REQ-3.3
    """
    status_counts = Counter(node_states.values())
    return {
        "total": len(node_states),
        "completed": status_counts.get("completed", 0),
        "pending": status_counts.get("pending", 0),
        "blocked": status_counts.get("blocked", 0),
        "failed": status_counts.get("failed", 0),
        "in_progress": status_counts.get("in_progress", 0),
    }


def _build_blocked_tasks(
    node_states: dict[str, str],
    blocked_reasons: dict[str, str],
) -> list[dict[str, str]]:
    """Build sorted blocked_tasks list.

    Requirements: 126-REQ-4.1, 126-REQ-4.2, 126-REQ-4.E1
    """
    blocked = []
    for node_id, status in node_states.items():
        if status == "blocked":
            # 126-REQ-4.E1: default to "unknown" if reason missing
            reason = blocked_reasons.get(node_id, "unknown")
            blocked.append({"node_id": node_id, "reason": reason})

    # 126-REQ-4.2: sorted by node_id ascending
    blocked.sort(key=lambda entry: entry["node_id"])
    return blocked


def _build_session_history(
    session_records: list[Any],
) -> list[dict[str, Any]]:
    """Serialize SessionRecords into session_history dicts.

    Requirements: 126-REQ-5.1
    """
    history = []
    for record in session_records:
        history.append(
            {
                "node_id": record.node_id,
                "attempt": record.attempt,
                "status": record.status,
                "archetype": record.archetype,
                "model": record.model,
                "duration_ms": record.duration_ms,
                "cost": record.cost,
                "error_message": record.error_message,
                "timestamp": record.timestamp,
                "is_transport_error": record.is_transport_error,
                "is_budget_exhausted": record.is_budget_exhausted,
                "is_non_retryable": record.is_non_retryable,
            }
        )
    return history
