"""CLI command for querying review findings.

Implements the `agent-fox findings` command that queries the knowledge
database for review findings and displays them in a formatted table or
as JSON.

Requirements: 04-REQ-2.1, 04-REQ-6.3,
              84-REQ-4.1 through 84-REQ-4.6, 84-REQ-4.E1, 84-REQ-4.E2
"""

from __future__ import annotations

import logging
import sys
from pathlib import Path

import click
from agentfox.core.config import KnowledgeConfig
from agentfox.core.node_id import DEFAULT_DB_PATH as _DEFAULT_DB_PATH
from agentfox.io import exit_codes, format_table
from agentfox.knowledge.db import open_knowledge_store

logger = logging.getLogger(__name__)

DEFAULT_DB_PATH: Path = _DEFAULT_DB_PATH


@exit_codes(**{"0": "Success", "1": "Error"})
@click.command("insights")
@click.option("--spec", default=None, help="Filter by spec name")
@click.option("--severity", default=None, help="Minimum severity level (critical, major, minor, observation)")
@click.option(
    "--archetype",
    default=None,
    help="Filter by archetype (reviewer, reviewer/pre-review, reviewer/drift-review)",
)
@click.option("--run", "run_id", default=None, help="Filter by run ID")
@click.option(
    "--dismiss",
    nargs=2,
    default=None,
    metavar="ID REASON",
    help="Dismiss a finding by ID: --dismiss <finding-id> <reason>",
)
@click.option("--json/--no-json", default=None, help="Enable/disable JSON output mode")
@click.pass_context
def findings_cmd(
    ctx: click.Context,
    spec: str | None,
    severity: str | None,
    archetype: str | None,
    run_id: str | None,
    dismiss: tuple[str, str] | None,
    json: bool | None,
) -> None:
    """Query review findings from the knowledge database.

    Displays active (non-superseded) review findings from the reviewer
    archetype (pre-review and drift-review records). Use filters to narrow
    results.

    To dismiss a stale or false-positive finding, use:

        agent-fox insights --dismiss <finding-id> "reason for dismissal"

    Requirements: 04-REQ-2.1, 84-REQ-4.1 through 84-REQ-4.6,
                  84-REQ-4.E1, 84-REQ-4.E2, 592-AC-3, 592-AC-4
    """
    import os

    from af import get_output_manager

    om = get_output_manager(ctx)
    if json is not None:
        om.json_mode = json
    elif os.environ.get("AF_AGENT") == "1":
        om.json_mode = True

    from agentfox.knowledge.review_store import dismiss_finding_by_id
    from agentfox.reporting.findings import query_findings

    # 84-REQ-4.E1: Handle missing DB gracefully
    if not DEFAULT_DB_PATH.exists():
        click.echo("No knowledge database found")
        return

    # 06-REQ-9.1: open with read_only=False to support --dismiss UPDATE
    try:
        _db = open_knowledge_store(
            KnowledgeConfig(store_path=str(DEFAULT_DB_PATH)),
            read_only=False,
        )
        conn = _db.connection
    except Exception:
        logger.debug("Failed to open knowledge database", exc_info=True)
        click.echo("No knowledge database found")
        return

    dismiss_not_found = False
    dismiss_result: str | None = None
    rows = []

    try:
        if dismiss is not None:
            finding_id, reason = dismiss
            dismiss_result = dismiss_finding_by_id(conn, finding_id, reason)
            if dismiss_result is None:
                dismiss_not_found = True
        else:
            rows = query_findings(
                conn,
                spec=spec,
                severity=severity,
                archetype=archetype,
                run_id=run_id,
                active_only=True,
            )
    finally:
        try:
            _db.close()
        except Exception:
            pass

    if dismiss is not None:
        finding_id, reason = dismiss
        if dismiss_not_found:
            click.echo(f"Finding {finding_id} not found", err=True)
            sys.exit(1)
        click.echo(f"Dismissed: {dismiss_result}")
        click.echo(f"Reason: {reason}")
        return

    # 84-REQ-4.E2: Handle empty results gracefully
    if not rows:
        if om.json_mode:
            om.emit({"findings": []})
        else:
            click.echo("No findings match the given filters")
        return

    # 04-REQ-6.3: Use format_table from agentfox.io for tabular output
    headers = ["Severity", "Archetype", "Spec", "Description", "Created"]
    table_rows = [
        [
            f.severity,
            f.archetype,
            f.spec_name,
            f.description[:80],
            f.created_at.strftime("%Y-%m-%d %H:%M") if f.created_at else "N/A",
        ]
        for f in rows
    ]
    output = format_table(headers=headers, rows=table_rows, json_mode=om.json_mode)

    if om.json_mode:
        om.emit({"findings": output})
    else:
        from rich.console import Console

        Console().print(output)
