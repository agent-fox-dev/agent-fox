"""Unified error envelope and CLI error routing.

Converts exceptions into structured JSON error envelopes conforming
to the unified schema, and provides routing helpers for CLI commands.

``error_envelope()`` builds the envelope dict; ``cli_error_handler()``
routes it to stdout (JSON mode) or stderr (human mode);
``handle_cli_errors`` is a no-arg decorator for command functions.

Requirements: 03-REQ-6, 03-REQ-7
"""

from __future__ import annotations

import logging
import re
import sys
from functools import wraps
from typing import Any

import click

from agentfox.core.errors import AgentFoxError

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Snake-case conversion
# ---------------------------------------------------------------------------


def _to_snake_case(name: str) -> str:
    """Convert a CamelCase class name to snake_case.

    Example: ``ConfigError`` -> ``config_error``
    """
    return re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()


# ---------------------------------------------------------------------------
# Error envelope
# ---------------------------------------------------------------------------


def error_envelope(exc: Exception, *, state: str | None = None) -> dict[str, Any]:
    """Build a structured error envelope dict from any exception.

    Returns a dictionary with ``ok: False`` and an ``error`` sub-dict
    containing ``type``, ``message``, and ``retryable`` fields.

    The ``detail`` field is included **only** when the exception maps
    to ``internal_error`` (i.e. is not a well-known type).

    Args:
        exc: The exception to convert.
        state: Optional workflow phase string (e.g. ``"planning"``).

    Returns:
        Error envelope dict.

    Requirements: 03-REQ-6.1 through 03-REQ-6.8
    """
    error_type: str
    retryable: bool
    include_detail = False

    if isinstance(exc, AgentFoxError):
        # 03-REQ-6.2: snake_case of the class name
        error_type = _to_snake_case(type(exc).__name__)
        retryable = getattr(exc, "retryable", False)

    elif isinstance(exc, click.ClickException):
        # 03-REQ-6.5: click.ClickException -> input_error
        error_type = "input_error"
        retryable = False

    else:
        # 03-REQ-6.6: unknown -> internal_error with detail
        error_type = "internal_error"
        retryable = False
        include_detail = True

    error_info: dict[str, Any] = {
        "type": error_type,
        "message": str(exc),
        "retryable": retryable,
    }

    if include_detail:
        error_info["detail"] = type(exc).__name__

    envelope: dict[str, Any] = {
        "ok": False,
        "error": error_info,
    }

    # 03-REQ-6.7: include state only when non-None
    if state is not None:
        envelope["state"] = state

    return envelope


# ---------------------------------------------------------------------------
# CLI error handler
# ---------------------------------------------------------------------------


def cli_error_handler(ctx: click.Context | Any, exc: Exception) -> None:
    """Route an exception to JSON stdout or plain-text stderr.

    When ``json_mode`` is active on the current ``OutputManager``,
    writes a structured JSON error envelope to stdout via ``emit_error``.
    Otherwise writes the exception message as plain text to stderr.

    Args:
        ctx: Click context (or mock with ``ctx.obj["output"]``).
        exc: The exception to handle.

    Requirements: 03-REQ-7.1, 03-REQ-7.2
    """
    from agentfox.io.json import emit_error

    # Try to get json_mode from OutputManager
    json_mode = False
    try:
        output = ctx.obj["output"]
        json_mode = output.json_mode
    except (TypeError, KeyError, AttributeError):
        # Fall back to get_output_manager
        from agentfox.io.output import get_output_manager

        om = get_output_manager()
        json_mode = om.json_mode

    if json_mode:
        emit_error(exc)
    else:
        click.echo(f"Error: {exc}", err=True)


# ---------------------------------------------------------------------------
# handle_cli_errors decorator
# ---------------------------------------------------------------------------


def handle_cli_errors(fn):  # noqa: ANN001, ANN201
    """No-argument decorator that catches ``Exception`` and routes errors.

    Wraps the decorated function so that:
    - ``Exception`` subclasses are caught, routed through
      ``cli_error_handler()``, and result in ``sys.exit(1)``.
    - ``SystemExit`` and ``KeyboardInterrupt`` propagate without
      being caught.

    The ``json_mode`` is resolved dynamically at call time via
    ``get_output_manager()`` (not at decoration time).

    Applied as ``@handle_cli_errors`` without parentheses.

    Requirements: 03-REQ-7.3
    """

    @wraps(fn)
    def wrapper(*args: Any, **kwargs: Any) -> Any:
        try:
            return fn(*args, **kwargs)
        except (SystemExit, KeyboardInterrupt):
            raise
        except Exception as exc:
            from agentfox.io.output import get_output_manager

            om = get_output_manager()
            # Create a minimal context-like object for cli_error_handler
            ctx_like = type("_Ctx", (), {"obj": {"output": om}})()
            cli_error_handler(ctx_like, exc)
            sys.exit(1)

    return wrapper
