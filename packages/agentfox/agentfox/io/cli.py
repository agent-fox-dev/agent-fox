"""CLI group and common options for AgentFoxGroup.

Requirements: 03-REQ-3, 03-REQ-9, 03-REQ-15, 04-REQ-5
"""

from __future__ import annotations

import json
import logging
import os
import sys
from typing import Any

import click

logger = logging.getLogger(__name__)


def common_options(fn: Any) -> Any:
    """Add --verbose, --quiet, --trace, and --json/--no-json flags."""
    if isinstance(fn, click.Command) and not isinstance(fn, click.Group):
        raise TypeError(
            "common_options must be applied to the root Click group, not to a subcommand"
        )

    existing_names: set[str] = set()
    if hasattr(fn, "params"):
        existing_names = {p.name for p in fn.params if p.name}

    def _json_callback(ctx, param, value):
        ctx.ensure_object(dict)
        if value is not None:
            ctx.obj["_json_explicit"] = True
        return value

    def _quiet_verbose_callback(ctx, param, value):
        ctx.ensure_object(dict)
        if value is not None and value is not False:
            ctx.obj["_quiet_explicit"] = True
        return value

    if "json" not in existing_names:
        fn = click.option("--json/--no-json", default=None,
            help="Enable/disable JSON output mode",
            callback=_json_callback, expose_value=True, is_eager=False)(fn)
    else:
        logger.debug("Skipping --json/--no-json: name collision with existing flag")

    if "trace" not in existing_names:
        fn = click.option("--trace", is_flag=True, default=False,
            help="Enable trace logging")(fn)
    else:
        logger.debug("Skipping --trace: name collision with existing flag")

    if "quiet" not in existing_names:
        fn = click.option("--quiet", "-q", is_flag=True, default=False,
            help="Suppress info messages", callback=_quiet_verbose_callback,
            expose_value=True, is_eager=False)(fn)
    else:
        logger.debug("Skipping --quiet: name collision with existing flag")

    if "verbose" not in existing_names:
        fn = click.option("--verbose", "-v", is_flag=True, default=False,
            help="Enable debug logging", callback=_quiet_verbose_callback,
            expose_value=True, is_eager=False)(fn)
    else:
        logger.debug("Skipping --verbose: name collision with existing flag")

    return fn


class _CliKeyboardInterrupt(KeyboardInterrupt, Exception):
    """KeyboardInterrupt that is also an Exception subclass."""


class AgentFoxGroup(click.Group):
    """Custom Click group with agent-mode detection and unified error routing."""

    def _resolve_flags(self, ctx):
        obj = ctx.obj if isinstance(ctx.obj, dict) else {}
        af_agent = os.environ.get("AF_AGENT") == "1"

        json_explicit = obj.get("_json_explicit", False)
        if json_explicit:
            json_mode = ctx.params.get("json", False)
            if json_mode is None:
                json_mode = False
        elif af_agent:
            json_mode = True
        else:
            json_mode = ctx.params.get("json", False) or ctx.params.get("json_mode", False)
            if json_mode is None:
                json_mode = False

        quiet_explicit = obj.get("_quiet_explicit", False)
        if quiet_explicit:
            verbose_val = ctx.params.get("verbose", False)
            quiet_val = ctx.params.get("quiet", False)
            if verbose_val:
                quiet = False
            else:
                quiet = quiet_val or False
        elif af_agent:
            quiet = True
        else:
            quiet = ctx.params.get("quiet", False) or False

        verbose = ctx.params.get("verbose", False) or False
        trace = ctx.params.get("trace", False) or False

        return {
            "json_mode": bool(json_mode),
            "quiet": bool(quiet),
            "verbose": bool(verbose),
            "trace": bool(trace),
            "agent_mode": af_agent or bool(json_mode),
        }

    def invoke(self, ctx):
        from agentfox.io.errors import cli_error_handler
        from agentfox.io.output import OutputManager

        try:
            ctx.ensure_object(dict)
        except Exception:
            pass

        if not isinstance(ctx.obj, dict):
            logger.debug(
                "ctx.obj is a non-dict value (%s); falling back to defaults",
                type(ctx.obj).__name__,
            )
            ctx.obj = {}

        flags = self._resolve_flags(ctx)
        ctx.obj["agent_mode"] = flags["agent_mode"]

        om = OutputManager(
            json_mode=flags["json_mode"],
            quiet=flags["quiet"],
            verbose=flags["verbose"],
            trace=flags["trace"],
        )
        ctx.obj["output"] = om

        try:
            from agentfox.core.logging import setup_logging
            setup_logging(verbose=flags["verbose"], quiet=flags["quiet"], trace=flags["trace"])
        except ImportError:
            pass

        # 04-REQ-5.1, 04-REQ-5.3: intercept --json --help for subcommands.
        # When both --json and --help appear in the subcommand args (or
        # --json was parsed as a group-level flag), render a JSON command
        # description instead of Click's standard text help.
        # Use _protected_args to avoid Click 8.3+ deprecation warning on
        # the public protected_args property (removed in Click 9.0).
        _prot = getattr(ctx, "_protected_args", None) or []
        sub_args = list(ctx.args or [])
        all_remaining = list(_prot) + sub_args
        help_in_args = "--help" in all_remaining
        json_in_args = "--json" in all_remaining
        json_from_group = flags.get("json_mode", False)

        if help_in_args and (json_from_group or json_in_args):
            # Find the subcommand name (first non-option token).
            cmd_name: str | None = None
            for tok in all_remaining:
                if not tok.startswith("-"):
                    cmd_name = tok
                    break
            if cmd_name is not None:
                cmd = self.get_command(ctx, cmd_name)
                if cmd is not None:
                    from agentfox.io.help import render_json_help

                    help_data = render_json_help(cmd)
                    click.echo(json.dumps(help_data, indent=2))
                    ctx.exit(0)

        try:
            super().invoke(ctx)
        except KeyboardInterrupt:
            self._pending_keyboard_interrupt = True
            raise
        except SystemExit:
            raise
        except click.exceptions.Exit:
            raise
        except click.ClickException as exc:
            if flags.get("json_mode"):
                from agentfox.io.json import emit as _emit
                _emit({"ok": False, "error": exc.format_message()})
                sys.exit(exc.exit_code)
            raise
        except Exception as exc:
            cli_error_handler(ctx, exc)
            sys.exit(1)

        # 03-REQ-2.4: Detect if a group callback changed ctx.obj to a
        # non-dict value during invocation. This can happen when a parent
        # group callback overwrites ctx.obj after AgentFoxGroup has
        # already constructed and stored the OutputManager.
        # Use logging.debug() (root logger) because setup_logging may
        # have set the 'agentfox' logger to WARNING, which would filter
        # this DEBUG diagnostic from the module logger.
        if not isinstance(ctx.obj, dict):
            logging.debug(
                "ctx.obj was changed to a non-dict value (%s) during invocation",
                type(ctx.obj).__name__,
            )

    def main(self, *args, **kwargs):
        self._pending_keyboard_interrupt = False
        try:
            return super().main(*args, **kwargs)
        except SystemExit:
            if getattr(self, "_pending_keyboard_interrupt", False):
                raise _CliKeyboardInterrupt() from None
            raise
        except KeyboardInterrupt:
            raise
