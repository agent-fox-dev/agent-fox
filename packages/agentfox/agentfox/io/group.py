"""AgentFoxGroup: Click group with agent-mode detection and error routing.

Provides a custom Click group class that handles:
- Agent-mode detection via ``AF_AGENT=1`` environment variable
- Banner suppression in agent mode
- Config loading and theme setup
- Unified error routing through JSON envelopes in agent mode
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import click

from agentfox.io.errors import format_error_envelope
from agentfox.io.output import emit


class AgentFoxGroup(click.Group):
    """Custom Click group with agent-mode detection and unified error routing.

    When ``AF_AGENT=1`` is set in the environment, this group:
    - Suppresses banner rendering
    - Routes all errors through JSON error envelopes on stdout
    - Sets ``ctx.obj["agent_mode"] = True``

    In non-agent mode, it renders the banner and loads config as
    the legacy ``spec`` CLI main callback did.
    """

    def invoke(self, ctx: click.Context) -> None:
        """Invoke the group with agent-mode detection and error routing.

        Agent mode is activated by either ``AF_AGENT=1`` in the environment
        or the ``--json`` flag (parameter name ``json_mode``) on the group.
        In agent mode, errors are routed through JSON error envelopes on
        stdout instead of plain text to stderr.
        """
        ctx.ensure_object(dict)

        env_agent = os.environ.get("AF_AGENT") == "1"
        json_flag = ctx.params.get("json_mode", False)
        agent_mode = env_agent or json_flag
        ctx.obj["agent_mode"] = agent_mode

        # Load config (delegated from individual CLIs)
        from agentfox.core.config import ThemeConfig, load_config

        config = load_config(Path(".agent-fox/config.toml"))
        ctx.obj.setdefault("config", config)

        # Render banner unless in agent/json mode or quiet
        quiet = ctx.params.get("quiet", False)
        if not agent_mode and not quiet:
            from agentfox.ui.display import create_theme, render_banner

            theme_config = config.theme if config else ThemeConfig()
            theme = create_theme(theme_config)
            render_banner(theme, quiet=False)

        try:
            super().invoke(ctx)
        except click.exceptions.Exit:
            raise
        except click.ClickException as exc:
            if agent_mode:
                emit({"ok": False, "error": exc.format_message()})
                sys.exit(exc.exit_code)
            raise
        except Exception as exc:
            if agent_mode:
                envelope = format_error_envelope(exc)
                emit(envelope)
                sys.exit(1)
            click.echo(f"Error: {exc}", err=True)
            sys.exit(1)
