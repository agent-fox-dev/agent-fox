"""Plan CLI command: build and display the execution plan.

Thin CLI wrapper that delegates to ``graph.planner.build_plan()``
for the planning pipeline, then handles persistence and display.

Requirements: 02-REQ-7.1, 02-REQ-7.2, 02-REQ-7.3, 02-REQ-7.4, 02-REQ-7.5,
              04-REQ-2.1
"""

from __future__ import annotations

import sys
from pathlib import Path

import click
from agentfox.core.config import load_config
from agentfox.core.errors import PlanError
from agentfox.graph.persistence import load_plan, save_plan
from agentfox.graph.planner import build_plan, format_plan_summary
from agentfox.io import emit_error, exit_codes
from agentfox.knowledge.db import open_knowledge_store
from agentfox.spec.discovery import discover_specs

from af import get_output_manager


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
@click.pass_context
def plan_cmd(
    ctx: click.Context,
    dry_run: bool,
    fast: bool,
    filter_spec: str | None,
    specs_dir: str | None,
    verify: bool,
) -> None:
    """Build an execution plan from specifications."""
    # 04-REQ-2.1: retrieve OutputManager from context
    om = get_output_manager(ctx)
    json_mode = om.json_mode

    # 85-REQ-3.2: Refuse to run when daemon is active.
    from agentfox.nightshift.pid import PidStatus, check_pid_file

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

    if verify:
        try:
            _verify_plan(specs_path, filter_spec, fast, config, om)
        except PlanError as exc:
            if json_mode:
                emit_error(str(exc))
                ctx.exit(1)
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
            ctx.exit(1)
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
