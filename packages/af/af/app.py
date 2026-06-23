"""CLI entry point for agent-fox.

Defines the Click command group with global options (--version,
--verbose, --quiet, --json), banner display when invoked without a
subcommand, and configuration loading. Subcommands are registered at
module level.

Requirements: 01-REQ-1.1, 01-REQ-1.2, 01-REQ-1.3, 01-REQ-1.4,
              01-REQ-1.E1, 01-REQ-4.E1,
              04-REQ-1.1, 04-REQ-1.3,
              23-REQ-1.1, 23-REQ-1.2, 23-REQ-2.1, 23-REQ-6.1,
              23-REQ-6.2, 23-REQ-6.E1
"""

from __future__ import annotations

import logging
from pathlib import Path

import click
from agentfox import __version__
from agentfox.core.config import ThemeConfig, load_config
from agentfox.core.logging import setup_logging
from agentfox.io import AgentFoxGroup, OutputManager
from agentfox.ui.display import create_theme, render_banner

logger = logging.getLogger(__name__)


@click.group(cls=AgentFoxGroup, invoke_without_command=True)
@click.version_option(version=__version__)
@click.option("--verbose", "-v", is_flag=True, help="Enable debug logging")
@click.option("--quiet", "-q", is_flag=True, help="Suppress info messages")
@click.option(
    "--trace",
    is_flag=True,
    default=False,
    help="Enable trace logging (includes bulk AI prompt/response payloads; implies --verbose)",
)
@click.option(
    "--json",
    "json_mode",
    is_flag=True,
    default=False,
    help="Switch to structured JSON I/O mode",
)
@click.pass_context
def main(ctx: click.Context, verbose: bool, quiet: bool, trace: bool, json_mode: bool) -> None:
    """af: autonomous coding-agent orchestrator."""
    ctx.ensure_object(dict)

    # 23-REQ-1.2: store JSON flag so every subcommand can access it
    ctx.obj["json"] = json_mode

    # 04-REQ-2.1: create OutputManager for unified data output dispatch
    ctx.obj["output"] = OutputManager(json_mode=json_mode)

    # In JSON mode, suppress warning-level log output so it doesn't pollute
    # the structured JSON stdout stream. Verbose/trace flags override this.
    effective_quiet = quiet or (json_mode and not verbose and not trace)
    setup_logging(verbose=verbose, quiet=effective_quiet, trace=trace)

    config = load_config(Path(".agent-fox/config.toml"))

    ctx.obj["config"] = config
    ctx.obj["verbose"] = verbose
    ctx.obj["quiet"] = quiet
    ctx.obj["trace"] = trace

    # 14-REQ-4.1: render banner on every invocation (suppressed by --quiet)
    # 23-REQ-2.1: suppress banner in JSON mode
    # 03-REQ-4.8: AgentFoxGroup no longer renders banner in invoke();
    # all banner rendering is consolidated here. Render when not in
    # JSON mode and not quiet.
    if not json_mode and not quiet:
        theme_config = config.theme if config else ThemeConfig()
        theme = create_theme(theme_config)
        render_banner(theme, quiet=quiet)

    # 01-REQ-1.3: show help when invoked without a subcommand
    if ctx.invoked_subcommand is None:
        click.echo(ctx.get_help())


# Import and register subcommands
from af.code import code_cmd  # noqa: E402
from af.findings import findings_cmd  # noqa: E402
from af.init import init_cmd  # noqa: E402
from af.nightshift import night_shift_cmd  # noqa: E402
from af.plan import plan_cmd  # noqa: E402
from af.reset import reset_cmd  # noqa: E402
from af.standup import standup_cmd  # noqa: E402

main.add_command(code_cmd, name="code")
main.add_command(findings_cmd, name="insights")
main.add_command(init_cmd, name="init")
main.add_command(night_shift_cmd, name="night-shift")
main.add_command(plan_cmd, name="plan")
main.add_command(reset_cmd, name="reset")
main.add_command(standup_cmd, name="standup")
