"""CLI command for spec validation: agent-fox lint-specs.

Thin CLI handler that delegates to the backing module at
agent_fox.spec.lint. Contains only argument parsing, output
formatting, and exit code mapping.

Requirements: 59-REQ-1.3, 59-REQ-1.4, 59-REQ-9.1, 59-REQ-9.2
"""

from __future__ import annotations

import json
import logging
from pathlib import Path

import click

from agent_fox.core.errors import PlanError
from agent_fox.spec.lint import run_lint_specs
from agent_fox.spec.validators import (
    SEVERITY_ERROR,
    SEVERITY_HINT,
    SEVERITY_WARNING,
    Finding,
)
from agent_fox.ui.display import create_theme
from agent_fox.ui.progress import ProgressDisplay

logger = logging.getLogger(__name__)


_SEVERITY_MARKERS = {
    SEVERITY_ERROR: "✗",  # ✗
    SEVERITY_WARNING: "⚠",  # ⚠
    SEVERITY_HINT: "ℹ",  # ℹ
}


def _build_summary(findings: list[Finding]) -> dict:
    """Build a summary counts dictionary from findings."""
    error_count = sum(1 for f in findings if f.severity == SEVERITY_ERROR)
    warning_count = sum(1 for f in findings if f.severity == SEVERITY_WARNING)
    hint_count = sum(1 for f in findings if f.severity == SEVERITY_HINT)
    return {
        "error": error_count,
        "warning": warning_count,
        "hint": hint_count,
        "total": len(findings),
    }


def _format_table(findings: list[Finding]) -> str:
    """Render findings as compact text lines grouped by spec."""
    if not findings:
        return "No findings.\n"

    lines: list[str] = []
    lines.append(f"Spec Validation — {len(findings)} findings")

    specs_seen: list[str] = []
    grouped: dict[str, list[Finding]] = {}
    for f in findings:
        if f.spec_name not in grouped:
            specs_seen.append(f.spec_name)
            grouped[f.spec_name] = []
        grouped[f.spec_name].append(f)

    for spec_name in specs_seen:
        spec_findings = grouped[spec_name]
        lines.append("")
        lines.append(f"{spec_name} ({len(spec_findings)} findings)")
        for f in spec_findings:
            marker = _SEVERITY_MARKERS.get(f.severity, "?")
            loc = f.file
            if f.line is not None:
                loc = f"{f.file}:{f.line}"
            lines.append(f"  {marker} {loc}  {f.rule} — {f.message}")

    summary = _build_summary(findings)
    parts = []
    if summary["error"] > 0:
        parts.append(f"{summary['error']} error(s)")
    if summary["warning"] > 0:
        parts.append(f"{summary['warning']} warning(s)")
    if summary["hint"] > 0:
        parts.append(f"{summary['hint']} hint(s)")
    lines.append("")
    lines.append(f"Summary: {' | '.join(parts)}")

    return "\n".join(lines) + "\n"


def _findings_to_dicts(findings: list[Finding]) -> list[dict]:
    """Convert a list of Finding instances to plain dictionaries."""
    return [
        {
            "spec_name": f.spec_name,
            "file": f.file,
            "rule": f.rule,
            "severity": f.severity,
            "message": f.message,
            "line": f.line,
        }
        for f in findings
    ]


def _format_json(findings: list[Finding]) -> str:
    """Serialize findings as JSON."""
    data = {
        "findings": _findings_to_dicts(findings),
        "summary": _build_summary(findings),
    }
    return json.dumps(data, indent=2)


@click.command("lint-specs")
@click.option(
    "--ai",
    is_flag=True,
    default=False,
    help="Enable AI-powered semantic analysis of acceptance criteria.",
)
@click.option(
    "--all",
    "lint_all",
    is_flag=True,
    default=False,
    help="Lint all specs, including fully-implemented ones.",
)
@click.pass_context
def lint_specs_cmd(ctx: click.Context, ai: bool, lint_all: bool) -> None:
    """Validate specification files for structural and quality problems."""
    json_mode = ctx.obj.get("json", False)
    output_format = "json" if json_mode else "table"

    from agent_fox.core.config import ThemeConfig, load_config, resolve_spec_root

    project_root = Path.cwd()
    config_path = project_root / ".agent-fox" / "config.toml"
    _config = load_config(config_path if config_path.exists() else None)
    specs_dir = resolve_spec_root(_config, project_root)

    # Progress display: suppressed in JSON or quiet mode (127-REQ-4.1, 127-REQ-4.4)
    quiet = ctx.obj.get("quiet", False) if isinstance(ctx.obj, dict) else False
    theme_config = getattr(_config, "theme", None) or ThemeConfig()
    theme = create_theme(theme_config)
    progress = ProgressDisplay(theme, quiet=quiet or json_mode)
    progress.start()

    try:
        result = run_lint_specs(
            specs_dir,
            ai=ai,
            lint_all=lint_all,
            progress_callback=progress.print_status,
        )
    except PlanError as exc:
        click.echo(f"Error: {exc}", err=True)
        ctx.exit(1)
        return
    finally:
        progress.stop()

    # Output results
    if output_format == "json":
        click.echo(_format_json(result.findings))
    else:
        click.echo(_format_table(result.findings), nl=False)

    ctx.exit(result.exit_code)
