"""JSON output helpers for CLI commands.

Provides ``emit`` and ``emit_ok`` for writing structured JSON envelopes
to stdout.  All JSON output in agent-fox CLIs should use these functions
rather than raw ``click.echo(json.dumps(...))``.
"""

from __future__ import annotations

import json
from typing import Any

import click


def emit(data: dict[str, Any]) -> None:
    """Write a single JSON object to stdout, followed by newline.

    Uses indented (pretty-printed) format for readability.
    Non-serializable values are converted via ``str()``.

    Args:
        data: Dictionary to serialize as JSON.
    """
    click.echo(json.dumps(data, indent=2, default=str))


def emit_ok(**kwargs: Any) -> None:
    """Write a successful JSON response envelope to stdout.

    Wraps the keyword arguments in ``{"ok": True, ...}`` and writes
    the result as a JSON object to stdout.

    Args:
        **kwargs: Additional fields to include in the envelope.
    """
    emit({"ok": True, **kwargs})
