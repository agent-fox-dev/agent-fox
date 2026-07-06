"""CLI code command: execute the task plan via the orchestrator.

Thin CLI wrapper that delegates to ``engine.run.run_code()`` for
orchestrator execution, then handles output formatting and exit codes.

Requirements: 16-REQ-1.1 through 16-REQ-5.2, 23-REQ-5.1, 23-REQ-5.E1,
              04-REQ-2.1, 123-REQ-1.1 through 123-REQ-4.2
"""

from __future__ import annotations

import asyncio
import logging
import os
import shutil
import sys
from pathlib import Path

import click
from agentfox.core.errors import AgentFoxError
from agentfox.engine.run import InterruptedResult, run_code
from agentfox.engine.state import ExecutionState
from agentfox.graph.persistence import load_plan
from agentfox.io import emit_error, emit_line, exit_codes, read_stdin
from agentfox.knowledge.db import open_knowledge_store
from agentfox.reporting.formatters import format_tokens
from agentfox.spec.discovery import discover_specs

from af import get_output_manager

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


def _spec_breakdown(node_states: dict[str, str]) -> dict[str, dict[str, int]]:
    """Group node_states by spec name, counting statuses per spec.

    Injected nodes (group_number == "0", e.g. ``spec:0:reviewer``) are
    excluded so they do not inflate the numbered-group count.

    Returns a dict mapping spec_name -> status counts dict.  Each inner
    dict always has a ``total`` key plus one key per distinct status seen
    (e.g. ``completed``, ``blocked``, ``pending``, ``in_progress``, ``failed``).
    """
    specs: dict[str, dict[str, int]] = {}
    for node_id, status in node_states.items():
        parts = node_id.split(":")
        if len(parts) < 2:
            continue
        spec_name = parts[0]
        group_part = parts[1]
        # Skip injected nodes (group_number == 0)
        if group_part == "0":
            continue
        if spec_name not in specs:
            specs[spec_name] = {"total": 0}
        specs[spec_name]["total"] += 1
        specs[spec_name][status] = specs[spec_name].get(status, 0) + 1
    return specs


def _format_spec_progress(spec_name: str, counts: dict[str, int]) -> str:
    """Format one spec's group-level progress as a human-readable string.

    Examples::

        "08_session_lifecycle    3/3 groups done"
        "10_knowledge_cleanup    2/4 groups done, 1 blocked, 1 pending"
        "11_enrich_summaries     0/2 groups done (stalled)"
    """
    total = counts["total"]
    done = counts.get("completed", 0)
    blocked = counts.get("blocked", 0)
    pending = counts.get("pending", 0)
    in_progress = counts.get("in_progress", 0)
    failed = counts.get("failed", 0)

    qualifiers: list[str] = []
    if blocked:
        qualifiers.append(f"{blocked} blocked")
    if pending:
        qualifiers.append(f"{pending} pending")
    if in_progress:
        qualifiers.append(f"{in_progress} in progress")
    if failed:
        qualifiers.append(f"{failed} failed")

    summary = f"{done}/{total} groups done"
    if qualifiers:
        summary += ", " + ", ".join(qualifiers)
    elif done == 0:
        summary += " (stalled)"

    return f"{spec_name}    {summary}"


def _completed_spec_names(node_states: dict[str, str]) -> set[str]:
    """Return spec names where all nodes are completed."""
    spec_nodes: dict[str, list[str]] = {}
    for node_id in node_states:
        idx = node_id.find(":")
        spec = node_id[:idx] if idx != -1 else node_id
        spec_nodes.setdefault(spec, []).append(node_id)
    return {spec for spec, nodes in spec_nodes.items() if all(node_states[n] == "completed" for n in nodes)}


def _archive_completed_specs(
    node_states: dict[str, str],
    specs_dir: Path,
    *,
    json_mode: bool = False,
) -> list[str]:
    """Move completed spec directories to specs/archive/.

    Returns list of spec names that were archived.
    """
    archive_dir = specs_dir / "archive"
    if not archive_dir.is_dir():
        logger.warning("Archive directory does not exist: %s", archive_dir)
        if not json_mode:
            click.echo(f"Warning: archive directory does not exist: {archive_dir}", err=True)
        return []

    completed = sorted(_completed_spec_names(node_states))
    archived: list[str] = []
    for spec_name in completed:
        src = specs_dir / spec_name
        dst = archive_dir / spec_name
        if not src.is_dir():
            continue
        if dst.exists():
            logger.warning("Spec already archived, skipping: %s", spec_name)
            continue
        shutil.move(str(src), str(dst))
        archived.append(spec_name)
        logger.info("Archived spec: %s", spec_name)

    if archived and not json_mode:
        click.echo(f"Archived {len(archived)} spec(s): {', '.join(archived)}")

    return archived


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

    # Per-spec breakdown (NS-REQ-1, NS-REQ-3, NS-REQ-4, NS-REQ-5)
    breakdown = _spec_breakdown(state.node_states)
    if breakdown:
        sorted_specs = sorted(breakdown.items())
        if len(sorted_specs) == 1:
            # NS-REQ-3: single spec — condensed one-line format
            spec_name, counts = sorted_specs[0]
            click.echo(f"Specs:  {_format_spec_progress(spec_name, counts)}")
        else:
            # NS-REQ-1: multiple specs — indented block
            click.echo("Specs:")
            for spec_name, counts in sorted_specs:
                click.echo(f"  {_format_spec_progress(spec_name, counts)}")

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


def _handle_dry_run(config: object, om: object, specs_dir: str | None) -> None:
    """Execute the dry-run analysis path.

    Loads the persisted plan from DuckDB (read-only), filters out completed
    nodes, computes analysis (phases, critical path, grouped edges), and
    displays the result as text or JSON via *om*.

    Requirements: 04-REQ-2.1, 123-REQ-1.1, 123-REQ-1.3, 123-REQ-1.E1,
                  123-REQ-1.E2, 123-REQ-1.E3, 123-REQ-3.1, 123-REQ-3.E1,
                  123-REQ-4.1
    """
    from agentfox.core.config import resolve_spec_root
    from agentfox.core.node_id import DEFAULT_DB_PATH
    from agentfox.graph.analyzer import compute_phases, critical_path, group_edges
    from agentfox.graph.planner import format_plan_analysis
    from agentfox.graph.types import NodeStatus

    from af.plan import _edge_to_dict, _metadata_to_dict, _node_to_dict

    json_mode = om.json_mode

    # 123-REQ-1.E1: check DB file exists
    if not DEFAULT_DB_PATH.exists():
        _err_msg = "No plan found. Run `agent-fox plan` first to generate a plan."
        if json_mode:
            emit_error(_err_msg)
            sys.exit(1)
        click.echo(f"Error: {_err_msg}", err=True)
        sys.exit(1)

    # Load persisted plan from DuckDB (read-only); see spec 06-REQ-2
    try:
        _db = open_knowledge_store(config.knowledge, read_only=True)
    except RuntimeError as exc:
        _open_err = f"Failed to open knowledge store: {exc}"
        if json_mode:
            emit_error(_open_err)
            sys.exit(1)
        click.echo(f"Error: {_open_err}", err=True)
        sys.exit(1)
    try:
        graph = load_plan(_db.connection)
    finally:
        _db.close()

    # 123-REQ-1.E2: empty plan (no nodes or None)
    if graph is None or not graph.nodes:
        if json_mode:
            om.emit(
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
            om.emit(
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

    # 123-REQ-3.1: JSON output via OutputManager
    if json_mode:
        om.emit(
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
    archive: bool = False,
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
    if archive:
        conflicts.append("--archive")
    return conflicts


@exit_codes(**{"0": "Success", "1": "Error", "2": "Stalled", "3": "Cost/session limit", "130": "Interrupted"})
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
@click.option(
    "--archive",
    is_flag=True,
    default=False,
    help="Move completed specs to specs/archive/ after execution",
)
@click.option("--json/--no-json", default=None, help="Enable/disable JSON output mode")
@click.pass_context
def code_cmd(
    ctx: click.Context,
    specs_dir: str | None,
    watch: bool,
    watch_interval: int | None,
    force_clean: bool,
    dry_run: bool,
    archive: bool,
    json: bool | None,
) -> None:
    """Execute the task plan."""
    om = get_output_manager(ctx)
    if json is not None:
        om.json_mode = json
    elif os.environ.get("AF_AGENT") == "1":
        om.json_mode = True
    json_mode: bool = om.json_mode

    # 16-REQ-1.2: load config from Click context
    config = ctx.obj["config"]
    quiet: bool = ctx.obj.get("quiet", False)

    # 123-REQ-2.1, 123-REQ-2.E1: mutual exclusion with execution flags
    conflicts = _check_dry_run_conflicts(
        dry_run=dry_run,
        watch=watch,
        force_clean=force_clean,
        archive=archive,
    )
    if conflicts:
        flag_list = ", ".join(conflicts)
        msg = f"Error: --dry-run cannot be combined with execution flags: {flag_list}"
        if json_mode:
            emit_error(msg)
        else:
            click.echo(msg, err=True)
        sys.exit(1)

    # 123-REQ-4.1, 123-REQ-4.2: dry-run bypasses daemon guard
    if dry_run:
        _handle_dry_run(config, om, specs_dir)
        return

    # 118-REQ-2.2: CLI --force-clean flag overrides config value
    if force_clean:
        config = config.model_copy(update={"workspace": config.workspace.model_copy(update={"force_clean": True})})

    # 85-REQ-3.1: Refuse to run when daemon is active.
    from agentfox.nightshift.pid import PidStatus, check_pid_file

    daemon_pid_path = Path.cwd() / ".agent-fox" / "daemon.pid"
    pid_status, _pid = check_pid_file(daemon_pid_path)
    if pid_status == PidStatus.ALIVE:
        msg = f"Error: nightshift daemon is running (PID {_pid}). Stop the daemon before running `code`."
        if json_mode:
            emit_error(msg)
        else:
            click.echo(msg, err=True)
        sys.exit(1)

    # Clean up stale merge lock left by a crashed process.
    from agentfox.workspace.merge_lock import cleanup_stale_merge_lock

    if cleanup_stale_merge_lock(Path.cwd()):
        logger.info("Removed stale merge lock at startup")

    # Purge stale audit files from the previous run (agent_*.jsonl,
    # audit_*.jsonl, postmortem_*.json).  Best-effort — failures are logged
    # as warnings and never abort the startup sequence.
    from afaudit.cleanup import purge_stale_audit_files
    from afaudit.constants import AUDIT_DIR

    purge_stale_audit_files(AUDIT_DIR)

    # 23-REQ-7.1: read stdin JSON when in JSON mode
    if json_mode:
        read_stdin()

    # 16-REQ-1.E1: check plan exists in DB
    from agentfox.core.node_id import DEFAULT_DB_PATH

    if not DEFAULT_DB_PATH.exists():
        _err_msg = "No plan found. Run `agent-fox plan` first to generate a plan."
        if json_mode:
            emit_error(_err_msg)
            sys.exit(1)
        click.echo(f"Error: {_err_msg}", err=True)
        sys.exit(1)

    # 18-REQ-5.1: Create progress display (suppressed in JSON mode)
    from agentfox.ui.display import create_theme
    from agentfox.ui.progress import ProgressDisplay

    theme = create_theme(config.theme)
    progress = ProgressDisplay(theme, quiet=quiet or json_mode)

    # 04-REQ-3.6: JSONL progress events for agent-mode
    jsonl_progress = None
    task_cb = progress.task_callback
    if json_mode:
        from agentfox.io.progress import ProgressDisplay as JsonlProgressDisplay

        jsonl_progress = JsonlProgressDisplay(output_manager=om, json_mode=True)
        _ui_task_cb = progress.task_callback

        def _jsonl_task_callback(event: object) -> None:
            """Bridge UI task events to JSONL progress events."""
            _ui_task_cb(event)
            node_id = getattr(event, "node_id", None)
            status = getattr(event, "status", "")
            if status == "completed":
                jsonl_progress.task_started(node_id=node_id)
                jsonl_progress.task_completed(node_id=node_id)
            elif status == "failed":
                error_msg = getattr(event, "error_message", "") or ""
                jsonl_progress.task_failed(node_id=node_id, error=error_msg)
            else:
                jsonl_progress.task_started(node_id=node_id)

        task_cb = _jsonl_task_callback

    progress.start()
    try:
        result = asyncio.run(
            run_code(
                config,
                watch=watch,
                watch_interval=watch_interval,
                specs_dir=Path(specs_dir) if specs_dir else None,
                activity_callback=progress.activity_callback,
                task_callback=task_cb,
            )
        )
    except KeyboardInterrupt:
        # 23-REQ-5.E1: emit interrupted status in JSON mode
        if json_mode:
            emit_line({"status": "interrupted"})
        sys.exit(130)
    except AgentFoxError:
        raise
    except Exception as exc:
        # 16-REQ-1.E2: unexpected exceptions
        logger.debug("Unexpected error during execution", exc_info=True)
        if json_mode:
            emit_error(str(exc))
            sys.exit(1)
        click.echo(f"Error: unexpected error: {exc}", err=True)
        sys.exit(1)
    finally:
        progress.stop()

    # Handle interrupted result from run_code
    if isinstance(result, InterruptedResult):
        if json_mode:
            emit_line({"status": "interrupted"})
        sys.exit(130)

    state: ExecutionState = result

    # 23-REQ-5.1, 04-REQ-2.1: emit summary via OutputManager
    if json_mode:
        counts = _count_by_status(state.node_states)
        # NS-REQ-2: per-spec breakdown in JSON payload
        breakdown = _spec_breakdown(state.node_states)
        specs_payload = {
            spec_name: {
                "completed": spec_counts.get("completed", 0),
                "total": spec_counts["total"],
                "blocked": spec_counts.get("blocked", 0),
                "pending": spec_counts.get("pending", 0),
                "in_progress": spec_counts.get("in_progress", 0),
                "failed": spec_counts.get("failed", 0),
            }
            for spec_name, spec_counts in breakdown.items()
        }
        summary_payload: dict = {
            "tasks": len(state.node_states),
            "completed": counts.get("completed", 0),
            "failed": counts.get("failed", 0),
            "input_tokens": state.total_input_tokens,
            "output_tokens": state.total_output_tokens,
            "cost": state.total_cost,
            "run_status": state.run_status,
            "specs": specs_payload,
        }
        # 118-REQ-8.3: include workspace-state classification in JSON output
        ws_errors = _extract_workspace_state_errors(state)
        if ws_errors:
            summary_payload["workspace_state_errors"] = [
                {"node_id": nid, "reason": reason} for nid, reason in ws_errors
            ]
        om.emit({"event": "complete", "summary": summary_payload})
    else:
        # 16-REQ-3.1: print summary
        _print_summary(state)

    # Archive completed specs when --archive is set
    if archive and state.run_status in ("completed", "stalled", "cost_limit", "session_limit"):
        from agentfox.core.config import resolve_spec_root

        _specs_path = Path(specs_dir) if specs_dir else resolve_spec_root(config, Path.cwd())
        try:
            _archive_completed_specs(state.node_states, _specs_path, json_mode=json_mode)
        except Exception as exc:
            logger.warning("Archive failed: %s", exc, exc_info=True)
            if not json_mode:
                click.echo(f"Warning: failed to archive specs: {exc}", err=True)

    # 16-REQ-4.*: exit with appropriate code
    exit_code = _exit_code_for_status(state.run_status)
    if exit_code != 0:
        sys.exit(exit_code)
