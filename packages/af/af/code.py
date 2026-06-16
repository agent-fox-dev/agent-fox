"""CLI code command: execute the task plan via the orchestrator.

Thin CLI wrapper that delegates to ``engine.run.run_code()`` for
orchestrator execution, then handles output formatting and exit codes.

Requirements: 16-REQ-1.1 through 16-REQ-5.2, 23-REQ-5.1, 23-REQ-5.E1,
              123-REQ-1.1 through 123-REQ-4.2
"""

from __future__ import annotations

import asyncio
import logging
import sys
from pathlib import Path

import click
from agentfox.core.errors import AgentFoxError
from agentfox.engine.run import InterruptedResult, run_code
from agentfox.engine.state import ExecutionState
from agentfox.graph.persistence import load_plan
from agentfox.knowledge.db import open_knowledge_store
from agentfox.reporting.formatters import format_tokens
from agentfox.spec.discovery import discover_specs

from af import json_io

logger = logging.getLogger(__name__)

# Exit code mapping: run_status -> shell exit code
# 16-REQ-4.1 through 16-REQ-4.5, 16-REQ-4.E1
_EXIT_CODES: dict[str, int] = {
    "completed": 0,
    "stalled": 2,
    "cost_limit": 3,
    "session_limit": 3,
    "interrupted": 130,
}


def _exit_code_for_status(run_status: str) -> int:
    """Map a run status string to a shell exit code.

    Returns the documented exit code for known statuses, or 1 for
    any unrecognized status.

    Requirements: 16-REQ-4.1 through 16-REQ-4.5, 16-REQ-4.E1
    """
    return _EXIT_CODES.get(run_status, 1)


def _count_by_status(node_states: dict[str, str]) -> dict[str, int]:
    """Count tasks grouped by their status value."""
    counts: dict[str, int] = {}
    for status in node_states.values():
        counts[status] = counts.get(status, 0) + 1
    return counts


def _extract_workspace_state_errors(state: ExecutionState) -> list[tuple[str, str]]:
    """Extract workspace-state errors from blocked reasons.

    Returns a list of (node_id, error_message) tuples for nodes blocked
    due to workspace-state errors.

    Requirements: 118-REQ-8.3
    """
    results: list[tuple[str, str]] = []
    for node_id, reason in state.blocked_reasons.items():
        if "workspace-state" in reason:
            results.append((node_id, reason))
    return results


def _print_summary(state: ExecutionState) -> None:
    """Print a compact execution summary.

    Requirements: 16-REQ-3.1, 16-REQ-3.2, 16-REQ-3.E1, 118-REQ-8.3
    """
    total = len(state.node_states)

    # 16-REQ-3.E1: empty plan
    if total == 0:
        click.echo("No tasks to execute.")
        return

    counts = _count_by_status(state.node_states)
    done = counts.get("completed", 0)
    in_progress = counts.get("in_progress", 0)
    pending = counts.get("pending", 0)
    failed = counts.get("failed", 0)
    blocked = counts.get("blocked", 0)

    parts = [f"{done}/{total} done"]
    if in_progress:
        parts.append(f"{in_progress} in progress")
    if pending:
        parts.append(f"{pending} pending")
    if failed:
        parts.append(f"{failed} failed")
    if blocked:
        parts.append(f"{blocked} blocked")

    click.echo(f"Tasks:  {', '.join(parts)}")
    click.echo(f"Tokens: {format_tokens(state.total_input_tokens)} in / {format_tokens(state.total_output_tokens)} out")
    click.echo(f"Cost:   ${state.total_cost:.2f}")
    click.echo(f"Status: {state.run_status}")

    # 126-REQ-6.1, 126-REQ-6.2: Print post-mortem path when present
    if state.postmortem_path:
        click.echo(f"Post-mortem: {state.postmortem_path}")

    # 118-REQ-8.3: when a run stalls/fails due to workspace-state errors,
    # include the root cause classification and the original error message.
    if state.run_status in ("stalled", "failed", "block_limit"):
        ws_errors = _extract_workspace_state_errors(state)
        if ws_errors:
            click.echo("")
            click.echo("Workspace-state errors:")
            for node_id, reason in ws_errors:
                click.echo(f"  [{node_id}] {reason}")


def _handle_dry_run(config: object, json_mode: bool, specs_dir: str | None) -> None:
    """Execute the dry-run analysis path.

    Loads the persisted plan from DuckDB (read-only), filters out completed
    nodes, computes analysis (phases, critical path, grouped edges), and
    displays the result as text or JSON.

    Requirements: 123-REQ-1.1, 123-REQ-1.3, 123-REQ-1.E1, 123-REQ-1.E2,
                  123-REQ-1.E3, 123-REQ-3.1, 123-REQ-3.E1, 123-REQ-4.1
    """
    from agentfox.core.config import resolve_spec_root
    from agentfox.core.node_id import DEFAULT_DB_PATH
    from agentfox.graph.analyzer import compute_phases, critical_path, group_edges
    from agentfox.graph.planner import format_plan_analysis
    from agentfox.graph.types import NodeStatus

    from af.plan import _edge_to_dict, _metadata_to_dict, _node_to_dict

    # 123-REQ-1.E1: check DB file exists
    if not DEFAULT_DB_PATH.exists():
        _err_msg = "No plan found. Run `agent-fox plan` first to generate a plan."
        if json_mode:
            json_io.emit_error(_err_msg)
            sys.exit(1)
        click.echo(f"Error: {_err_msg}", err=True)
        sys.exit(1)

    # Load persisted plan from DuckDB (read-only)
    _db = open_knowledge_store(config.knowledge)
    try:
        graph = load_plan(_db.connection)
    finally:
        _db.close()

    # 123-REQ-1.E2: empty plan (no nodes or None)
    if graph is None or not graph.nodes:
        if json_mode:
            json_io.emit(
                {
                    "nodes": {},
                    "edges": [],
                    "order": [],
                    "metadata": {},
                    "phases": [],
                    "critical_path": [],
                    "grouped_edges": {"intra_spec": [], "cross_spec": []},
                }
            )
        else:
            click.echo("No tasks in plan.")
        return

    # 123-REQ-1.3: filter completed nodes
    completed_ids = {nid for nid, node in graph.nodes.items() if node.status == NodeStatus.COMPLETED}

    # 123-REQ-1.E3: all nodes completed
    if completed_ids == set(graph.nodes.keys()):
        if json_mode:
            json_io.emit(
                {
                    "nodes": {},
                    "edges": [],
                    "order": [],
                    "metadata": _metadata_to_dict(graph.metadata),
                    "phases": [],
                    "critical_path": [],
                    "grouped_edges": {"intra_spec": [], "cross_spec": []},
                }
            )
        else:
            click.echo("All tasks completed.")
        return

    if completed_ids:
        graph.nodes = {nid: n for nid, n in graph.nodes.items() if nid not in completed_ids}
        graph.edges = [e for e in graph.edges if e.source not in completed_ids and e.target not in completed_ids]
        graph.order = [nid for nid in graph.order if nid not in completed_ids]

    # Compute analysis
    phases = compute_phases(graph)
    path = critical_path(graph)
    grouped = group_edges(graph)

    # Discover specs for display
    project_root = Path.cwd()
    specs_path = Path(specs_dir) if specs_dir else resolve_spec_root(config, project_root)
    try:
        specs = discover_specs(specs_path)
    except Exception:
        specs = []

    # 123-REQ-3.1: JSON output
    if json_mode:
        json_io.emit(
            {
                "nodes": {nid: _node_to_dict(node) for nid, node in graph.nodes.items()},
                "edges": [_edge_to_dict(e) for e in graph.edges],
                "order": graph.order,
                "metadata": _metadata_to_dict(graph.metadata),
                "phases": [{"number": p.number, "node_ids": p.node_ids} for p in phases],
                "critical_path": path,
                "grouped_edges": {
                    "intra_spec": [_edge_to_dict(e) for e in grouped.intra_spec],
                    "cross_spec": [_edge_to_dict(e) for e in grouped.cross_spec],
                },
            }
        )
        return

    # Text output
    click.echo(format_plan_analysis(graph, phases, path, grouped, specs))


def _check_dry_run_conflicts(
    dry_run: bool,
    watch: bool,
    force_clean: bool,
) -> list[str]:
    """Return list of flag names incompatible with --dry-run, or empty list.

    Requirements: 123-REQ-2.1, 123-REQ-2.E1, 131-REQ-3.1
    """
    if not dry_run:
        return []

    conflicts: list[str] = []
    if watch:
        conflicts.append("--watch")
    if force_clean:
        conflicts.append("--force-clean")
    return conflicts


@click.command("code")
@click.option(
    "--specs-dir",
    type=click.Path(),
    default=None,
    help="Path to specs directory (default: from config, or .agent-fox/specs)",
)
@click.option(
    "--watch",
    is_flag=True,
    default=False,
    help="Keep running and poll for new specs after all tasks complete",
)
@click.option(
    "--watch-interval",
    type=int,
    default=None,
    help="Seconds between watch polls (default: 60, minimum: 10)",
)
@click.option(
    "--force-clean",
    is_flag=True,
    default=False,
    help="Automatically remove untracked files and reset dirty index before dispatch",
)
@click.option(
    "--dry-run",
    is_flag=True,
    default=False,
    help="Show plan analysis without running the orchestrator",
)
@click.pass_context
def code_cmd(
    ctx: click.Context,
    specs_dir: str | None,
    watch: bool,
    watch_interval: int | None,
    force_clean: bool,
    dry_run: bool,
) -> None:
    """Execute the task plan."""
    # 16-REQ-1.2: load config from Click context
    config = ctx.obj["config"]
    quiet: bool = ctx.obj.get("quiet", False)
    json_mode: bool = ctx.obj.get("json", False)

    # 123-REQ-2.1, 123-REQ-2.E1: mutual exclusion with execution flags
    conflicts = _check_dry_run_conflicts(
        dry_run=dry_run,
        watch=watch,
        force_clean=force_clean,
    )
    if conflicts:
        flag_list = ", ".join(conflicts)
        msg = f"Error: --dry-run cannot be combined with execution flags: {flag_list}"
        if json_mode:
            json_io.emit_error(msg)
        else:
            click.echo(msg, err=True)
        sys.exit(1)

    # 123-REQ-4.1, 123-REQ-4.2: dry-run bypasses daemon guard
    if dry_run:
        _handle_dry_run(config, json_mode, specs_dir)
        return

    # 118-REQ-2.2: CLI --force-clean flag overrides config value
    if force_clean:
        config = config.model_copy(update={"workspace": config.workspace.model_copy(update={"force_clean": True})})

    # 85-REQ-3.1: Refuse to run when daemon is active.
    from agentfox.nightshift.pid import PidStatus, check_pid_file

    daemon_pid_path = Path.cwd() / ".agent-fox" / "daemon.pid"
    pid_status, _pid = check_pid_file(daemon_pid_path)
    if pid_status == PidStatus.ALIVE:
        msg = f"Error: night-shift daemon is running (PID {_pid}). Stop the daemon before running `code`."
        if json_mode:
            json_io.emit_error(msg)
        else:
            click.echo(msg, err=True)
        sys.exit(1)

    # 23-REQ-7.1: read stdin JSON when in JSON mode
    if json_mode:
        json_io.read_stdin()

    # 16-REQ-1.E1: check plan exists in DB
    from agentfox.core.node_id import DEFAULT_DB_PATH

    if not DEFAULT_DB_PATH.exists():
        _err_msg = "No plan found. Run `agent-fox plan` first to generate a plan."
        if json_mode:
            json_io.emit_error(_err_msg)
            sys.exit(1)
        click.echo(f"Error: {_err_msg}", err=True)
        sys.exit(1)

    # 18-REQ-5.1: Create progress display (suppressed in JSON mode)
    from agentfox.ui.display import create_theme
    from agentfox.ui.progress import ProgressDisplay

    theme = create_theme(config.theme)
    progress = ProgressDisplay(theme, quiet=quiet or json_mode)

    progress.start()
    try:
        result = asyncio.run(
            run_code(
                config,
                watch=watch,
                watch_interval=watch_interval,
                specs_dir=Path(specs_dir) if specs_dir else None,
                activity_callback=progress.activity_callback,
                task_callback=progress.task_callback,
            )
        )
    except KeyboardInterrupt:
        # 23-REQ-5.E1: emit interrupted status in JSON mode
        if json_mode:
            json_io.emit_line({"status": "interrupted"})
        sys.exit(130)
    except AgentFoxError:
        raise
    except Exception as exc:
        # 16-REQ-1.E2: unexpected exceptions
        logger.debug("Unexpected error during execution", exc_info=True)
        if json_mode:
            json_io.emit_error(str(exc))
            sys.exit(1)
        click.echo(f"Error: unexpected error: {exc}", err=True)
        sys.exit(1)
    finally:
        progress.stop()

    # Handle interrupted result from run_code
    if isinstance(result, InterruptedResult):
        if json_mode:
            json_io.emit_line({"status": "interrupted"})
        sys.exit(130)

    state: ExecutionState = result

    # 23-REQ-5.1: emit JSONL summary in JSON mode
    if json_mode:
        counts = _count_by_status(state.node_states)
        summary_payload: dict = {
            "tasks": len(state.node_states),
            "completed": counts.get("completed", 0),
            "failed": counts.get("failed", 0),
            "input_tokens": state.total_input_tokens,
            "output_tokens": state.total_output_tokens,
            "cost": state.total_cost,
            "run_status": state.run_status,
        }
        # 118-REQ-8.3: include workspace-state classification in JSON output
        ws_errors = _extract_workspace_state_errors(state)
        if ws_errors:
            summary_payload["workspace_state_errors"] = [
                {"node_id": nid, "reason": reason} for nid, reason in ws_errors
            ]
        json_io.emit_line({"event": "complete", "summary": summary_payload})
    else:
        # 16-REQ-3.1: print summary
        _print_summary(state)

    # 16-REQ-4.*: exit with appropriate code
    exit_code = _exit_code_for_status(state.run_status)
    if exit_code != 0:
        sys.exit(exit_code)
