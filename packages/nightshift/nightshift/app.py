"""Standalone nightshift CLI — delegates to agentfox.nightshift."""

from __future__ import annotations

import asyncio
import logging
import os
import signal
import sys
from pathlib import Path

import click
from agentfox.core.config import ThemeConfig, load_config
from agentfox.core.logging import setup_logging
from agentfox.io import AgentFoxGroup, OutputManager, common_options, exit_codes
from agentfox.ui.display import create_theme, render_banner

logger = logging.getLogger(__name__)


@exit_codes(**{"0": "Success", "1": "Startup failure", "130": "Immediate abort"})
@click.group(cls=AgentFoxGroup, invoke_without_command=True)
@click.version_option(version=None, package_name="nightshift")
@click.option("--json/--no-json", "json_flag", default=None, help="Enable/disable JSON output mode")
@common_options
@click.pass_context
def main(ctx: click.Context, json_flag: bool | None = None, **kwargs) -> None:  # noqa: ARG001
    """Run the nightshift autonomous fix daemon.

    Polls for issues labelled ``af:fix`` and processes them through the
    archetype pipeline until interrupted (Ctrl-C) or the cost limit is hit.
    """
    ctx.ensure_object(dict)
    om = ctx.obj.get("output")
    if om is None:
        om = OutputManager(json_mode=False)
        ctx.obj["output"] = om
    if json_flag is not None:
        om.json_mode = json_flag
    elif os.environ.get("AF_AGENT") == "1":
        om.json_mode = True
    effective_quiet = om.quiet or (om.json_mode and not om.verbose)
    setup_logging(verbose=om.verbose, quiet=effective_quiet)
    config = load_config()
    ctx.obj.update(config=config, verbose=om.verbose, quiet=om.quiet)
    if not om.json_mode and not om.quiet:
        render_banner(create_theme(getattr(config, "theme", None) or ThemeConfig()), quiet=om.quiet)
    if ctx.invoked_subcommand is None:
        _run_daemon(ctx, om, config)


def _run_daemon(ctx, om, config):  # noqa: C901
    """Assemble and run the daemon from agentfox.nightshift modules."""
    from agentfox.core.errors import IntegrationError
    from agentfox.nightshift.daemon import DaemonRunner, SharedBudget
    from agentfox.nightshift.engine import (
        NightShiftEngine,
        validate_night_shift_prerequisites,
    )
    from agentfox.nightshift.platform_factory import create_platform
    from agentfox.nightshift.streams import build_streams
    from agentfox.ui.progress import ProgressDisplay
    from agentfox.workspace.merge_lock import cleanup_stale_merge_lock

    from nightshift._startup import init_knowledge, wrap_task_callback

    root = Path.cwd()
    validate_night_shift_prerequisites(config)
    platform = create_platform(config, root)
    try:
        asyncio.run(platform.check_credentials())
    except IntegrationError as exc:
        click.echo(f"Error: GitHub authentication failed — {exc}", err=True)
        sys.exit(1)
    if cleanup_stale_merge_lock(root):
        logger.info("Removed stale merge lock at startup")

    # Purge stale audit files from the previous run (agent_*.jsonl,
    # audit_*.jsonl, postmortem_*.json).  Best-effort — failures are logged
    # as warnings and never abort the startup sequence.
    from afaudit.cleanup import purge_stale_audit_files
    from afaudit.constants import AUDIT_DIR

    purge_stale_audit_files(AUDIT_DIR)

    kdb, sink, kprov = init_knowledge(config, root)
    quiet = ctx.obj.get("quiet", False) if isinstance(ctx.obj, dict) else False
    progress = ProgressDisplay(
        create_theme(getattr(config, "theme", None) or ThemeConfig()),
        quiet=quiet or om.json_mode,
    )
    progress.start()
    task_cb = wrap_task_callback(progress, om)

    engine = NightShiftEngine(
        config=config,
        platform=platform,
        activity_callback=progress.activity_callback,
        task_callback=task_cb,
        status_callback=progress.print_status,
        spinner_callback=progress.update_spinner_text,
        sink_dispatcher=sink,
        conn=(kdb.connection if kdb else None),
        knowledge_provider=kprov,
    )
    budget = SharedBudget(
        max_cost=getattr(getattr(config, "orchestrator", None), "max_cost", None),
    )
    runner = DaemonRunner(
        config=config,
        platform=platform,
        streams=build_streams(config, engine=engine, budget=budget),
        budget=budget,
        pid_path=root / ".agent-fox" / "daemon.pid",
        idle_callback=progress.update_spinner_text,
    )

    _n = {"c": 0}

    def _sig(signum, _frame):  # noqa: ARG001
        _n["c"] += 1
        if _n["c"] == 1:
            logger.info("Signal received — graceful shutdown")
            runner.request_shutdown()
        else:
            sys.exit(130)

    signal.signal(signal.SIGINT, _sig)
    signal.signal(signal.SIGTERM, _sig)
    click.echo("Nightshift daemon starting. Press Ctrl-C to stop gracefully.")
    try:
        ds = asyncio.run(runner.run())
    except SystemExit:
        raise
    except Exception as exc:
        logger.error("Night-shift daemon failed: %s", exc, exc_info=True)
        click.echo(f"Error: nightshift daemon failed: {exc}", err=True)
        sys.exit(1)
    finally:
        progress.stop()
        for fn in [
            lambda: asyncio.run(platform.close()) if hasattr(platform, "close") else None,
            lambda: kdb.close() if kdb else None,
        ]:
            try:
                fn()
            except Exception:  # noqa: BLE001, S110
                pass
    fixed, cost = engine.state.issues_fixed, ds.total_cost
    if om.json_mode:
        om.emit({"status": "stopped", "issues_fixed": fixed, "total_cost": cost})
    else:
        click.echo(f"Nightshift stopped. Issues fixed: {fixed}, Total cost: ${cost:.2f}")
