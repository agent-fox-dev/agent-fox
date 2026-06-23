"""CLI command for standup report: agent-fox standup.

Generates a daily activity report covering agent work, human
commits, file overlaps, and queued tasks.

Requirements: 04-REQ-2.1, 04-REQ-2.5, 04-REQ-6.2,
              07-REQ-2.1, 07-REQ-3.1, 07-REQ-3.4,
              23-REQ-3.2, 23-REQ-8.2
"""

from __future__ import annotations

import logging
from dataclasses import asdict
from pathlib import Path

import click
from agentfox.core.node_id import DEFAULT_DB_PATH
from agentfox.io import exit_codes, format_table
from agentfox.reporting.formatters import (
    OutputFormat,
    get_formatter,
)
from agentfox.reporting.standup import generate_standup
from rich.console import Console

from af import get_output_manager

logger = logging.getLogger(__name__)


def _build_cost_tables(
    report: object,
    json_mode: bool,
) -> dict:
    """Build cost breakdown tables using format_table.

    Returns a dict with ``cost_by_spec`` and ``cost_by_archetype`` keys,
    each holding the format_table output (list-of-dicts in JSON mode,
    Rich Table in text mode).

    Requirements: 04-REQ-6.2
    """
    cost_by_spec = getattr(report, "cost_by_spec", {}) or {}
    cost_by_archetype = getattr(report, "cost_by_archetype", {}) or {}

    spec_table = format_table(
        headers=["Spec", "Cost"],
        rows=[[spec, f"${cost:.2f}"] for spec, cost in sorted(cost_by_spec.items())],
        json_mode=json_mode,
    )

    archetype_table = format_table(
        headers=["Archetype", "Cost"],
        rows=[[archetype, f"${cost:.2f}"] for archetype, cost in sorted(cost_by_archetype.items())],
        json_mode=json_mode,
    )

    return {"cost_by_spec": spec_table, "cost_by_archetype": archetype_table}


@exit_codes(**{"0": "Success", "1": "Error"})
@click.command("standup")
@click.option(
    "--hours",
    type=int,
    default=24,
    help="Reporting window in hours (default: 24)",
)
@click.pass_context
def standup_cmd(ctx: click.Context, hours: int) -> None:
    """Generate daily activity report."""
    # 04-REQ-2.1, 04-REQ-2.5: retrieve OutputManager from context
    om = get_output_manager(ctx)
    json_mode = om.json_mode
    project_root = Path.cwd()

    db_conn = None
    try:
        import duckdb

        if DEFAULT_DB_PATH.exists():
            db_conn = duckdb.connect(str(DEFAULT_DB_PATH), read_only=True)
    except Exception:
        logger.debug("DuckDB unavailable for standup", exc_info=True)

    try:
        report = generate_standup(
            repo_path=project_root,
            hours=hours,
            db_conn=db_conn,
        )
    finally:
        if db_conn is not None:
            db_conn.close()

    # Build cost tables via format_table (04-REQ-6.2)
    cost_tables = _build_cost_tables(report, json_mode=json_mode)

    if json_mode:
        data = asdict(report)
        data["cost_by_spec"] = cost_tables["cost_by_spec"]
        data["cost_by_archetype"] = cost_tables["cost_by_archetype"]
        om.emit(data)
    else:
        console = Console()
        formatter = get_formatter(OutputFormat.TABLE, console=console)
        content = formatter.format_standup(report)

        console.print(content, end="")
