"""CLI entry point for agent-fox.

Defines the Click command group with global options (--version,
--verbose, --quiet, --json/--no-json), banner display when invoked
without a subcommand, and configuration loading. Subcommands are
registered at module level.

Requirements: 01-REQ-1.1, 01-REQ-1.2, 01-REQ-1.3, 01-REQ-1.4,
              01-REQ-1.E1, 01-REQ-4.E1,
              03-REQ-3, 03-REQ-11.1, 03-REQ-11.2, 03-REQ-11.3,
              04-REQ-1.1, 04-REQ-1.3,
              23-REQ-1.1, 23-REQ-1.2, 23-REQ-2.1, 23-REQ-6.1,
              23-REQ-6.2, 23-REQ-6.E1
"""

from __future__ import annotations

import logging

import click
from agentfox import __version__
from agentfox.core.config import ThemeConfig, load_config
from agentfox.core.logging import setup_logging
from agentfox.io import AgentFoxGroup, OutputManager, common_options
from agentfox.ui.display import create_theme, render_banner

logger = logging.getLogger(__name__)


# --- BannerGroup -> AgentFoxGroup migration audit (2026-06-24) ---
# Covered by AgentFoxGroup (agentfox/io/cli.py):
#   - ctx.obj initialization via ctx.ensure_object(dict)
#   - AF_AGENT=1 environment variable detection for agent-mode defaults
#   - Unified error routing: Exception -> cli_error_handler -> JSON or stderr
#   - SystemExit / KeyboardInterrupt propagation (not caught)
#   - OutputManager construction and storage at ctx.obj["output"]
#   - setup_logging() invocation with base resolved flags
#   - common_options sentinel mechanism: _json_explicit, _quiet_explicit
#     enable explicit --no-json / --verbose to override AF_AGENT=1 defaults
# Covered here in main() callback (app-level behavior):
#   - Banner rendering (render_banner) -- suppressed when json_mode or quiet
#   - Config loading (load_config) and ctx.obj wiring for subcommands
#   - setup_logging with effective_quiet (json_mode implies quiet for logs)
#   - Uses OutputManager from AgentFoxGroup (does not construct a new one)
# Completed in Spec 04:
#   - JSON IO compatibility shim removal (json_io module)
#   - Structured JSON help via --json --help
# ---
@click.group(cls=AgentFoxGroup, invoke_without_command=True)
@click.version_option(version=__version__)
@common_options
@click.pass_context
def main(ctx: click.Context, **kwargs) -> None:  # noqa: ARG001
    """af: autonomous coding-agent orchestrator."""
    ctx.ensure_object(dict)

    # 03-REQ-11.1, 03-REQ-2.2: Use the OutputManager already constructed
    # by AgentFoxGroup.invoke() with properly resolved flags including
    # AF_AGENT=1 support and sentinel-based flag overrides.
    om = ctx.obj.get("output")
    if om is None:
        # Fallback for tests that invoke main() directly without the
        # AgentFoxGroup invoke cycle.
        om = OutputManager(json_mode=bool(kwargs.get("json", False)))
        ctx.obj["output"] = om

    # 23-REQ-1.2: store JSON flag so every subcommand can access it
    ctx.obj["json"] = om.json_mode

    # In JSON mode, suppress warning-level log output so it doesn't pollute
    # the structured JSON stdout stream. Verbose flag overrides this.
    effective_quiet = om.quiet or (om.json_mode and not om.verbose)
    setup_logging(verbose=om.verbose, quiet=effective_quiet)

    config = load_config()

    ctx.obj["config"] = config
    ctx.obj["verbose"] = om.verbose
    ctx.obj["quiet"] = om.quiet

    # 14-REQ-4.1: render banner on every invocation (suppressed by --quiet)
    # 23-REQ-2.1: suppress banner in JSON mode
    # 03-REQ-4.8: AgentFoxGroup no longer renders banner in invoke();
    # all banner rendering is consolidated here. Render when not in
    # JSON mode and not quiet.
    if not om.json_mode and not om.quiet:
        theme_config = config.theme if config else ThemeConfig()
        theme = create_theme(theme_config)
        render_banner(theme, quiet=om.quiet)

    # 01-REQ-1.3: show help when invoked without a subcommand
    if ctx.invoked_subcommand is None:
        click.echo(ctx.get_help())


# Import and register subcommands
from af.code import code_cmd  # noqa: E402
from af.findings import findings_cmd  # noqa: E402
from af.init import init_cmd  # noqa: E402
from af.plan import plan_cmd  # noqa: E402
from af.reset import reset_cmd  # noqa: E402
from af.standup import standup_cmd  # noqa: E402

main.add_command(code_cmd, name="code")
main.add_command(findings_cmd, name="insights")
main.add_command(init_cmd, name="init")
main.add_command(plan_cmd, name="plan")
main.add_command(reset_cmd, name="reset")
main.add_command(standup_cmd, name="standup")
