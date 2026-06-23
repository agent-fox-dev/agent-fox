"""Unified JSON serialization functions for CLI output.

Requirements: 03-REQ-5
"""

from __future__ import annotations

import json
import sys
from typing import Any

import click


def emit(data: dict[str, Any]) -> None:
    """Write data as pretty-printed JSON (indent=2) to stdout."""
    try:
        click.echo(json.dumps(data, indent=2, default=str))
    except BrokenPipeError:
        pass


def emit_line(data: dict[str, Any]) -> None:
    """Write data as compact JSONL (no indentation) to stdout."""
    try:
        click.echo(json.dumps(data, default=str))
    except BrokenPipeError:
        pass


def emit_ok(data: dict[str, Any] | None = None, **kwargs: Any) -> None:
    """Merge ok=True into data and write as pretty-printed JSON."""
    if data is None:
        data = kwargs
    merged = {**data, "ok": True}
    try:
        click.echo(json.dumps(merged, indent=2, default=str))
    except BrokenPipeError:
        pass


def emit_error(exc_or_message: Exception | str, *, state: str | None = None) -> None:
    """Write a structured JSON error envelope to stdout."""
    if isinstance(exc_or_message, str):
        envelope: dict[str, Any] = {"error": exc_or_message}
        try:
            click.echo(json.dumps(envelope, default=str))
        except BrokenPipeError:
            pass
        return

    from agentfox.io.errors import error_envelope
    envelope = error_envelope(exc_or_message, state=state)
    try:
        click.echo(json.dumps(envelope, indent=2, default=str))
    except BrokenPipeError:
        pass


def read_stdin() -> dict[str, Any]:
    """Read and parse a JSON object from piped stdin."""
    if sys.stdin.isatty():
        return {}
    text = sys.stdin.read().strip()
    if not text:
        return {}
    return json.loads(text)  # type: ignore[no-any-return]
