"""CLI command for the night-shift autonomous fix daemon.

Runs continuously, polling for issues labelled ``af:fix`` and processing
them through the full archetype pipeline.  A pull request is opened per
fix.

Requirements: 04-REQ-2.1,
              61-REQ-1.1, 61-REQ-1.2, 61-REQ-1.3, 61-REQ-1.4,
              85-REQ-2.1, 85-REQ-4.1, 85-REQ-6.1,
              125-REQ-4.1, 125-REQ-4.2, 125-REQ-4.3, 125-REQ-4.4
"""

from __future__ import annotations

import asyncio
import logging
import signal
import sys
from pathlib import Path

import click
from agentfox.io import exit_codes

from af import get_output_manager

logger = logging.getLogger(__name__)


@exit_codes(**{"0": "Success", "1": "Startup failure", "130": "Immediate abort"})
@click.command("night-shift")
@click.pass_context
def night_shift_cmd(
    ctx: click.Context,
) -> None:
    """Run the night-shift autonomous fix daemon.

    Polls for open issues labelled ``af:fix`` and processes them through
    the archetype pipeline on configurable intervals.  Continues until
    interrupted with Ctrl-C (SIGINT) or until the configured cost limit
    is reached.

    Exit codes:
      0 -- clean shutdown (single SIGINT or cost limit reached)
      1 -- startup failure (platform not configured, etc.)
      130 -- immediate abort (double SIGINT)
    """
    # 04-REQ-2.1: retrieve OutputManager from context
    om = get_output_manager(ctx)

    from agentfox.nightshift.daemon import DaemonRunner, SharedBudget
    from agentfox.nightshift.engine import (
        NightShiftEngine,
        validate_night_shift_prerequisites,
    )
    from agentfox.nightshift.platform_factory import create_platform
    from agentfox.nightshift.streams import build_streams

    config = ctx.obj["config"]
    project_root = Path.cwd()

    # 61-REQ-1.E1: Validate platform is configured before entering the loop.
    validate_night_shift_prerequisites(config)

    # Instantiate the platform from config (exits with code 1 on failure).
    platform = create_platform(config, project_root)

    # Pre-flight credential check: verify token is valid before entering the loop.
    # A 401/403 from GitHub is an immediate, unrecoverable startup error.
    # Requirements: 598-AC-2
    from agentfox.core.errors import IntegrationError

    try:
        asyncio.run(platform.check_credentials())
    except IntegrationError as exc:
        click.echo(f"Error: GitHub authentication failed — {exc}", err=True)
        logger.error("Credential pre-flight check failed: %s", exc)
        sys.exit(1)

    # Create DuckDB-backed SinkDispatcher for audit cost tracking (91-REQ-1.2).
    # If DuckDB cannot be opened, proceed without cost tracking (91-REQ-1.E1).
    _knowledge_db = None
    _sink_dispatcher = None
    try:
        from agentfox.knowledge.db import open_knowledge_store
        from agentfox.knowledge.duckdb_sink import DuckDBSink
        from agentfox.knowledge.sink import SinkDispatcher

        _knowledge_db = open_knowledge_store(config.knowledge)
        _db_sink = DuckDBSink(_knowledge_db.connection)
        _sink_dispatcher = SinkDispatcher([_db_sink])
    except Exception:
        logger.warning(
            "Failed to open knowledge store for night-shift audit — cost tracking will be unavailable for this session",
            exc_info=True,
        )

    # --- ProgressDisplay setup (81-REQ-2.1) ---------------------------------
    from agentfox.core.config import ThemeConfig
    from agentfox.ui.display import create_theme
    from agentfox.ui.progress import ProgressDisplay

    theme_config = getattr(config, "theme", None) or ThemeConfig()
    theme = create_theme(theme_config)
    quiet = ctx.obj.get("quiet", False) if isinstance(ctx.obj, dict) else False
    progress = ProgressDisplay(theme, quiet=quiet or om.json_mode)
    progress.start()

    # 04-REQ-3.7: JSONL progress events for agent-mode
    task_cb = progress.task_callback
    if om.json_mode:
        from agentfox.io.progress import ProgressDisplay as JsonlProgressDisplay

        _jsonl_progress = JsonlProgressDisplay(output_manager=om, json_mode=True)
        _ui_task_cb = progress.task_callback

        def _jsonl_task_callback(event: object) -> None:
            """Bridge UI task events to JSONL progress events."""
            _ui_task_cb(event)
            node_id = getattr(event, "node_id", None)
            status = getattr(event, "status", "")
            if status == "completed":
                _jsonl_progress.task_started(node_id=node_id)
                _jsonl_progress.task_completed(node_id=node_id)
            elif status == "failed":
                error_msg = getattr(event, "error_message", "") or ""
                _jsonl_progress.task_failed(node_id=node_id, error=error_msg)
            else:
                _jsonl_progress.task_started(node_id=node_id)

        task_cb = _jsonl_task_callback
    # -----------------------------------------------------------------------

    # Create the engine for business logic (fix pipeline).
    # Streams delegate to engine methods; the engine is NOT the lifecycle
    # manager.  DaemonRunner handles lifecycle, scheduling, and budget.
    engine = NightShiftEngine(
        config=config,
        platform=platform,
        activity_callback=progress.activity_callback,
        task_callback=task_cb,
        status_callback=progress.print_status,
        spinner_callback=progress.update_spinner_text,
        sink_dispatcher=_sink_dispatcher,
        conn=(_knowledge_db.connection if _knowledge_db is not None else None),
    )

    # Shared cost budget (85-REQ-5.1, 85-REQ-5.2)
    max_cost = getattr(getattr(config, "orchestrator", None), "max_cost", None)
    budget = SharedBudget(max_cost=max_cost)

    # Build work streams with CLI flags (85-REQ-6.1, 125-REQ-3.3)
    streams = build_streams(
        config,
        engine=engine,
        budget=budget,
    )

    # Create the daemon runner (85-REQ-1.2, 85-REQ-2.1, 85-REQ-4.1)
    pid_path = project_root / ".agent-fox" / "daemon.pid"
    runner = DaemonRunner(
        config=config,
        platform=platform,
        streams=streams,
        budget=budget,
        pid_path=pid_path,
        idle_callback=progress.update_spinner_text,
    )

    # --- Signal handling ----------------------------------------------------
    # First SIGINT/SIGTERM: graceful shutdown (current operation completes).
    # Second interrupt: immediate abort with exit code 130.
    # 61-REQ-1.3, 61-REQ-1.4, 85-REQ-2.2, 85-REQ-2.3
    _interrupt_count = 0

    def _signal_handler(signum: int, frame: object) -> None:
        nonlocal _interrupt_count
        _interrupt_count += 1
        sig_name = "SIGTERM" if signum == signal.SIGTERM else "SIGINT"
        if _interrupt_count == 1:
            logger.info(
                "%s received — completing current operation then exiting (send another signal to abort immediately)",
                sig_name,
            )
            runner.request_shutdown()
        else:
            logger.warning("Second interrupt received — aborting immediately")
            sys.exit(130)

    signal.signal(signal.SIGINT, _signal_handler)
    signal.signal(signal.SIGTERM, _signal_handler)
    # -----------------------------------------------------------------------

    click.echo("Night-shift daemon starting. Press Ctrl-C to stop gracefully.")

    try:
        daemon_state = asyncio.run(runner.run())
    except SystemExit:
        raise
    except Exception as exc:
        logger.error("Night-shift daemon failed: %s", exc, exc_info=True)
        click.echo(f"Error: night-shift daemon failed: {exc}", err=True)
        sys.exit(1)
    finally:
        progress.stop()
        # Close platform connection if it supports it
        try:
            if hasattr(platform, "close"):
                asyncio.run(platform.close())
        except Exception:  # noqa: BLE001
            pass
        # Close knowledge store connection used for audit (91-REQ-1.2)
        try:
            if _knowledge_db is not None:
                _knowledge_db.close()
        except Exception:  # noqa: BLE001
            pass

    # Pull detailed stats from the engine state (streams don't track these).
    summary = {
        "status": "stopped",
        "issues_fixed": engine.state.issues_fixed,
        "total_cost": daemon_state.total_cost,
    }
    if om.json_mode:
        om.emit(summary)
    else:
        fixed = engine.state.issues_fixed
        cost = daemon_state.total_cost
        click.echo(f"Night-shift stopped. Issues fixed: {fixed}, Total cost: ${cost:.4f}")
