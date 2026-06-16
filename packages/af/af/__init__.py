"""af: CLI for the agentfox autonomous coding-agent orchestrator."""

from __future__ import annotations

import functools
from collections.abc import Callable
from typing import Any

import click
from agentfox import __version__
from agentfox.core.errors import AgentFoxError


def handle_agent_fox_errors(fn: Callable[..., Any]) -> Callable[..., Any]:
    """Decorator that catches AgentFoxError and exits with code 1."""

    @functools.wraps(fn)
    def wrapper(ctx: click.Context, *args: Any, **kwargs: Any) -> Any:
        try:
            return fn(ctx, *args, **kwargs)
        except AgentFoxError as exc:
            if ctx.obj and ctx.obj.get("json"):
                from af.json_io import emit_error

                emit_error(str(exc))
                ctx.exit(1)
                return
            click.echo(f"Error: {exc}", err=True)
            ctx.exit(1)

    return wrapper


__all__ = ["__version__", "handle_agent_fox_errors"]
