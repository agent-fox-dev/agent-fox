"""Plan CLI command: build and display the execution plan.

Thin CLI wrapper that delegates to ``graph.planner.build_plan()``
for the planning pipeline, then handles persistence and display.
Also provides ``--clear``, ``--reset``, and ``--reset-hard`` flags
that subsume the old ``af reset`` command.

Requirements: 02-REQ-7.1, 02-REQ-7.2, 02-REQ-7.3, 02-REQ-7.4, 02-REQ-7.5,
              04-REQ-2.1, 01-REQ-1, 01-REQ-2, 01-REQ-3, 01-REQ-5, 01-REQ-6
"""

from __future__ import annotations

import os
import sys
from dataclasses import asdict
from pathlib import Path

import click
from agentfox.core.config import load_config
from agentfox.core.errors import PlanError
from agentfox.engine.reset import (
    _SESSION_TABLES_ALL,
    HardResetResult,
    ResetResult,
    run_reset,
)
from agentfox.engine.state import persist_node_status
from agentfox.graph.persistence import load_plan, save_plan
from agentfox.graph.planner import build_plan, format_plan_summary
from agentfox.io import emit_error, exit_codes
from agentfox.knowledge.db import open_knowledge_store
from agentfox.spec.discovery import discover_specs

from af import get_output_manager


def _handle_clear(
    config: object,
    filter_spec: str | None,
    json_mode: bool,
    om: object,
) -> None:
    """Handle --clear: set nodes to completed and truncate session tables.

    Requirements: 01-REQ-1.1, 01-REQ-1.2, 01-REQ-1.3, 01-REQ-1.4
    """
    db = open_knowledge_store(config.knowledge, read_only=False)
    try:
        graph = load_plan(db.connection)
        if graph is None:
            click.echo(
                "Error: No plan found in database. Run 'agent-fox plan' first.",
                err=True,
            )
            sys.exit(1)

        # Determine which nodes to clear
        if filter_spec is not None:
            target_nodes = {nid: node for nid, node in graph.nodes.items() if node.spec_name == filter_spec}
        else:
            target_nodes = graph.nodes

        # Set each target node to completed
        for nid in target_nodes:
            persist_node_status(db.connection, nid, "completed")

        # Truncate session-scoped tables
        for table in _SESSION_TABLES_ALL:
            db.connection.execute(f"DELETE FROM {table}")  # noqa: S608

        count = len(target_nodes)

        if json_mode:
            om.emit({"cleared": count, "spec": filter_spec})
        else:
            click.echo(f"Cleared {count} nodes.")
    finally:
        db.close()


def _handle_reset(
    config: object,
    filter_spec: str | None,
    task_id: str | None,
    yes: bool,
    json_mode: bool,
    om: object,
) -> None:
    """Handle --reset: soft-reset failed/blocked/in-progress tasks.

    Requirements: 01-REQ-2.1, 01-REQ-2.2, 01-REQ-2.3, 01-REQ-2.4
    """
    db = open_knowledge_store(config.knowledge, read_only=False)
    try:
        graph = load_plan(db.connection)
        if graph is None:
            click.echo(
                "Error: No plan found in database. Run 'agent-fox plan' first.",
                err=True,
            )
            sys.exit(1)

        # Single-task reset skips confirmation
        # --yes flag skips confirmation
        needs_confirm = task_id is None and not yes

        if needs_confirm:
            label = f"spec '{filter_spec}'" if filter_spec else "all tasks"
            if not click.confirm(f"Reset {label}? Proceed with reset?"):
                click.echo("Reset cancelled.")
                return

        result = run_reset(
            target=task_id,
            config=config,
            soft=True,
            hard=False,
            spec=filter_spec,
            db_conn=db.connection,
        )

        if json_mode:
            om.emit(asdict(result))
        else:
            _display_reset_result(result)
    finally:
        db.close()


def _handle_reset_hard(
    config: object,
    task_id: str | None,
    yes: bool,
    json_mode: bool,
    om: object,
) -> None:
    """Handle --reset-hard: hard-reset with code rollback.

    Requirements: 01-REQ-3.1, 01-REQ-3.2, 01-REQ-3.3, 01-REQ-3.4
    """
    db = open_knowledge_store(config.knowledge, read_only=False)
    try:
        graph = load_plan(db.connection)
        if graph is None:
            click.echo(
                "Error: No plan found in database. Run 'agent-fox plan' first.",
                err=True,
            )
            sys.exit(1)

        # --reset-hard always requires confirmation unless --yes
        if not yes:
            if task_id:
                msg = f"Hard reset task {task_id} (rolls back code, resets affected tasks)?"
            else:
                msg = "Hard reset ALL tasks (rolls back code, wipes all state)?"
            if not click.confirm(msg):
                click.echo("Hard reset cancelled.")
                return

        result = run_reset(
            target=task_id,
            config=config,
            soft=False,
            hard=True,
            db_conn=db.connection,
        )

        if json_mode:
            om.emit(asdict(result))
        else:
            _display_hard_reset_result(result)
    finally:
        db.close()


def _verify_plan(
    specs_path: Path,
    filter_spec: str | None,
    fast: bool,
    config: object,
    om: object,
) -> None:
    """Cross-check tasks.json states against DB plan_nodes statuses.

    Builds a fresh plan from spec files and compares node statuses
    against the persisted plan in DuckDB. Reports mismatches and
    exits with code 1 if any are found.
    """
    from agentfox.core.node_id import DEFAULT_DB_PATH

    json_mode = om.json_mode

    # Build fresh plan from spec files
    graph = build_plan(specs_path, filter_spec, fast, config)

    # Load persisted plan from DB
    if not DEFAULT_DB_PATH.exists():
        msg = "No database found. Run `agent-fox plan` first."
        if json_mode:
            emit_error(msg)
        else:
            click.echo(f"Error: {msg}", err=True)
        sys.exit(1)

    # read_only=True: verify path only reads plan_nodes for comparison; see spec 06-REQ-3
    db = open_knowledge_store(config.knowledge, read_only=True)
    try:
        persisted = load_plan(db.connection)
    finally:
        db.close()

    if persisted is None:
        msg = "No persisted plan found in database. Run `agent-fox plan` first."
        if json_mode:
            emit_error(msg)
        else:
            click.echo(f"Error: {msg}", err=True)
        sys.exit(1)

    # Compare statuses
    mismatches: list[dict[str, str]] = []
    orphans: list[str] = []
    new_nodes: list[str] = []

    all_node_ids = set(graph.nodes.keys()) | set(persisted.nodes.keys())
    for nid in sorted(all_node_ids):
        in_spec = nid in graph.nodes
        in_db = nid in persisted.nodes

        if in_spec and not in_db:
            new_nodes.append(nid)
            continue
        if in_db and not in_spec:
            orphans.append(nid)
            continue

        spec_status = str(graph.nodes[nid].status)
        db_status = str(persisted.nodes[nid].status)
        if spec_status != db_status:
            mismatches.append(
                {
                    "node_id": nid,
                    "spec_status": spec_status,
                    "db_status": db_status,
                }
            )

    has_issues = bool(mismatches or orphans or new_nodes)

    if json_mode:
        om.emit(
            {
                "verified": not has_issues,
                "mismatches": mismatches,
                "orphans": orphans,
                "new_nodes": new_nodes,
            }
        )
    else:
        if not has_issues:
            click.echo("Plan verified: spec files and database are in sync.")
        else:
            if mismatches:
                click.echo("Status mismatches:")
                for m in mismatches:
                    click.echo(f"  {m['node_id']} — spec: {m['spec_status']}, db: {m['db_status']}")
            if orphans:
                click.echo(f"Orphan nodes (in DB, not in specs): {', '.join(orphans)}")
            if new_nodes:
                click.echo(f"New nodes (in specs, not in DB): {', '.join(new_nodes)}")

    if has_issues:
        sys.exit(1)


def _node_to_dict(node: object) -> dict:
    """Serialize a Node (or duck-typed object) to a JSON-friendly dict."""
    return {
        "id": node.id,
        "spec_name": node.spec_name,
        "group_number": node.group_number,
        "title": node.title,
        "optional": node.optional,
        "status": str(node.status),
        "archetype": node.archetype,
    }


def _edge_to_dict(edge: object) -> dict:
    """Serialize an Edge (or duck-typed object) to a JSON-friendly dict."""
    return {"source": edge.source, "target": edge.target, "kind": edge.kind}


def _metadata_to_dict(meta: object) -> dict:
    """Serialize PlanMetadata (or duck-typed object) to a JSON-friendly dict."""
    return {
        "created_at": meta.created_at,
        "fast_mode": meta.fast_mode,
        "filtered_spec": meta.filtered_spec,
        "version": meta.version,
    }


def _check_mode_exclusivity(
    dry_run: bool,
    verify: bool,
    clear: bool,
    reset: bool,
    reset_hard: bool,
) -> list[str]:
    """Return list of active mutually-exclusive mode flags.

    If two or more flags are active the caller must abort before
    opening the KnowledgeStore.

    Requirements: 01-REQ-6.1, 01-PROP-6
    """
    active: list[str] = []
    if dry_run:
        active.append("--dry-run")
    if verify:
        active.append("--verify")
    if clear:
        active.append("--clear")
    if reset:
        active.append("--reset")
    if reset_hard:
        active.append("--reset-hard")
    return active


def _display_reset_result(result: ResetResult) -> None:
    """Display a human-readable summary of a soft-reset result."""
    if not result.reset_tasks:
        if result.skipped_completed:
            click.echo("Warning: Completed tasks cannot be reset.", err=True)
        else:
            click.echo("Nothing to reset. All tasks are in a valid state.")
        return

    click.echo(f"Reset {len(result.reset_tasks)} task(s) to pending:")
    for task_id in result.reset_tasks:
        click.echo(f"  - {task_id}")

    if result.unblocked_tasks:
        click.echo(f"\nUnblocked {len(result.unblocked_tasks)} downstream task(s):")
        for task_id in result.unblocked_tasks:
            click.echo(f"  - {task_id}")

    if result.cleaned_worktrees:
        click.echo(f"\nCleaned up {len(result.cleaned_worktrees)} worktree(s).")

    if result.cleaned_branches:
        click.echo(f"Deleted {len(result.cleaned_branches)} branch(es).")


def _display_hard_reset_result(result: HardResetResult) -> None:
    """Display a human-readable summary of a hard-reset result."""
    count = len(result.reset_tasks)
    click.echo(f"Hard reset complete: {count} task(s) reset to pending.")

    if result.reset_tasks:
        for task_id in result.reset_tasks:
            click.echo(f"  - {task_id}")

    if result.cleaned_worktrees:
        click.echo(f"\nCleaned up {len(result.cleaned_worktrees)} worktree(s).")

    if result.cleaned_branches:
        click.echo(f"Deleted {len(result.cleaned_branches)} branch(es).")

    orig, surviving = result.compaction
    click.echo(f"\nKnowledge compaction: {orig} -> {surviving} facts.")

    if result.rollback_sha:
        click.echo(f"Code rolled back to {result.rollback_sha}.")
    else:
        click.echo("Code rollback skipped (no tracked commits).")


@exit_codes(**{"0": "Success", "1": "Error"})
@click.command("plan")
@click.option("--dry-run", is_flag=True, help="Show plan analysis without persisting to database")
@click.option("--fast", is_flag=True, help="Exclude optional tasks")
@click.option("--spec", "filter_spec", default=None, help="Plan a single spec")
@click.option(
    "--specs-dir",
    type=click.Path(),
    default=None,
    help="Path to specs directory (default: from config, or .agent-fox/specs)",
)
@click.option(
    "--verify",
    is_flag=True,
    default=False,
    help="Cross-check spec files against database plan states",
)
@click.option(
    "--clear",
    is_flag=True,
    default=False,
    help="Mark all nodes completed (non-destructive)",
)
@click.option(
    "--reset",
    is_flag=True,
    default=False,
    help="Soft-reset failed/blocked/in-progress tasks to pending",
)
@click.option(
    "--reset-hard",
    is_flag=True,
    default=False,
    help="Hard-reset all tasks including code rollback",
)
@click.option(
    "--yes",
    "-y",
    is_flag=True,
    default=False,
    help="Skip confirmation prompts for reset operations",
)
@click.argument("task_id", required=False, default=None)
@click.option("--json/--no-json", default=None, help="Enable/disable JSON output mode")
@click.pass_context
def plan_cmd(
    ctx: click.Context,
    dry_run: bool,
    fast: bool,
    filter_spec: str | None,
    specs_dir: str | None,
    verify: bool,
    clear: bool,
    reset: bool,
    reset_hard: bool,
    yes: bool,
    task_id: str | None,
    json: bool | None,
) -> None:
    """Build an execution plan from specifications.

    When invoked with --clear, --reset, or --reset-hard the command
    operates on existing plan state instead of building a new plan.
    """
    om = get_output_manager(ctx)
    if json is not None:
        om.json_mode = json
    elif os.environ.get("AF_AGENT") == "1":
        om.json_mode = True
    json_mode = om.json_mode

    # 01-REQ-6.1: Mutual exclusivity — check BEFORE opening DB
    active_modes = _check_mode_exclusivity(dry_run, verify, clear, reset, reset_hard)
    if len(active_modes) > 1:
        click.echo(
            f"Error: mutually exclusive flags provided: {', '.join(active_modes)}",
            err=True,
        )
        sys.exit(1)

    # 01-REQ-6.2: --reset-hard and --spec are mutually exclusive
    if reset_hard and filter_spec is not None:
        click.echo(
            "Error: --reset-hard and --spec cannot be combined.",
            err=True,
        )
        sys.exit(1)

    # 85-REQ-3.2: Refuse to run when daemon is active.
    from agentfox.maintenance.pid import PidStatus, check_pid_file

    daemon_pid_path = Path.cwd() / ".agent-fox" / "daemon.pid"
    pid_status, _pid = check_pid_file(daemon_pid_path)
    if pid_status == PidStatus.ALIVE:
        click.echo(
            f"Error: nightshift daemon is running (PID {_pid}). Stop the daemon before running `plan`.",
            err=True,
        )
        sys.exit(1)

    # Determine project paths
    project_root = Path.cwd()

    # Load config for archetypes
    config_path = project_root / ".agent-fox" / "config.toml"
    config = load_config(config_path if config_path.exists() else None)

    # Resolve spec root from config with backward compatibility
    from agentfox.core.config import resolve_spec_root

    specs_path: Path = Path(specs_dir) if specs_dir else resolve_spec_root(config, project_root)

    # --- Handle --clear mode (01-REQ-1) ---
    if clear:
        _handle_clear(config, filter_spec, json_mode, om)
        return

    # --- Handle --reset mode (01-REQ-2) ---
    if reset:
        _handle_reset(config, filter_spec, task_id, yes, json_mode, om)
        return

    # --- Handle --reset-hard mode (01-REQ-3) ---
    if reset_hard:
        _handle_reset_hard(config, task_id, yes, json_mode, om)
        return

    if verify:
        try:
            _verify_plan(specs_path, filter_spec, fast, config, om)
        except PlanError as exc:
            if json_mode:
                emit_error(str(exc))
                ctx.exit(0)
                return
            click.echo(f"Error: {exc}", err=True)
            ctx.exit(1)
        return

    from agentfox.ui.progress import PlanSpinner

    spinner = PlanSpinner("Planning...")
    if not json_mode:
        spinner.start()
    try:
        graph = build_plan(specs_path, filter_spec, fast, config)
    except PlanError as exc:
        spinner.stop()
        if json_mode:
            emit_error(str(exc))
            ctx.exit(0)
            return
        click.echo(f"Error: {exc}", err=True)
        ctx.exit(1)
        return
    finally:
        spinner.stop()

    # 122-REQ-1.1: dry-run skips persistence and shows analysis
    if dry_run:
        from agentfox.graph.analyzer import compute_phases, critical_path, group_edges
        from agentfox.graph.planner import format_plan_analysis
        from agentfox.graph.types import NodeStatus

        # 122-REQ-1.4: merge persisted statuses and filter completed nodes
        # read_only=True: dry-run only reads persisted plan for comparison
        try:
            _db = open_knowledge_store(config.knowledge, read_only=True)
            try:
                persisted = load_plan(_db.connection)
            finally:
                _db.close()
        except Exception:
            persisted = None

        if persisted:
            for nid, node in graph.nodes.items():
                if nid in persisted.nodes:
                    node.status = persisted.nodes[nid].status

        completed_ids = {nid for nid, node in graph.nodes.items() if node.status == NodeStatus.COMPLETED}
        if completed_ids:
            graph.nodes = {nid: n for nid, n in graph.nodes.items() if nid not in completed_ids}
            graph.edges = [e for e in graph.edges if e.source not in completed_ids and e.target not in completed_ids]
            graph.order = [nid for nid in graph.order if nid not in completed_ids]

        phases = compute_phases(graph)
        path = critical_path(graph)
        grouped = group_edges(graph)

        try:
            specs = discover_specs(specs_path, filter_spec=filter_spec)
        except PlanError:
            specs = []

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

        click.echo(format_plan_analysis(graph, phases, path, grouped, specs))
        return

    # Persist the plan to DuckDB (105-REQ-5.2)
    # read_only=False: save path performs DELETE + INSERT on plan tables
    _knowledge_db = open_knowledge_store(config.knowledge, read_only=False)
    try:
        save_plan(graph, _knowledge_db.connection)
    finally:
        _knowledge_db.close()

    # Re-discover specs for summary display
    try:
        specs = discover_specs(specs_path, filter_spec=filter_spec)
    except PlanError:
        specs = []

    # 23-REQ-3.4, 04-REQ-2.1: JSON output via OutputManager
    if json_mode:
        from dataclasses import asdict

        om.emit(
            {
                "nodes": {nid: asdict(node) for nid, node in graph.nodes.items()},
                "edges": [asdict(e) for e in graph.edges],
                "order": graph.order,
                "metadata": asdict(graph.metadata),
            }
        )
        return

    click.echo(format_plan_summary(graph, specs))
