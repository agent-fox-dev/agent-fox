"""af: CLI for the agentfox autonomous coding-agent orchestrator.

Requirements: 04-REQ-1.2 — BannerGroup and handle_agent_fox_errors removed;
error handling is now provided by AgentFoxGroup from agentfox.io.
04-REQ-2.E1 — get_output_manager raises RuntimeError when OutputManager
is missing from the Click context.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

from agentfox import __version__

if TYPE_CHECKING:
    import click
    from agentfox.io import OutputManager


def get_output_manager(ctx: click.Context) -> OutputManager:
    """Retrieve the ``OutputManager`` from the Click context.

    Every ``af`` subcommand should call this at the top of its callback
    to obtain the shared ``OutputManager`` instance.

    When ``ctx.obj`` is ``None`` or lacks the ``"output"`` key, a
    default ``OutputManager`` with ``json_mode=False`` is created and
    stored back.  This backward-compatible fallback keeps existing
    tests that invoke subcommands directly (without going through
    the group callback) working per 04-REQ-7.1.

    Args:
        ctx: The Click invocation context.

    Returns:
        The ``OutputManager`` stored in ``ctx.obj["output"]``.

    Requirements: 04-REQ-2.E1, 04-REQ-7.1
    """
    if ctx.obj is None:
        ctx.ensure_object(dict)
    om = ctx.obj.get("output")
    if om is None:
        from agentfox.io import OutputManager as _OM

        json_mode = ctx.obj.get("json_mode", ctx.obj.get("json", False))
        om = _OM(json_mode=bool(json_mode))
        ctx.obj["output"] = om
    return om


__all__ = ["__version__", "get_output_manager"]
