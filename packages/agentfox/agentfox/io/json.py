"""Unified JSON serialization functions for CLI output.

Requirements: 03-REQ-5
"""

from __future__ import annotations

import json
import sys
from typing import Any

import click


def emit(data: dict[str, Any]) -> None:
    """Write data as pretty-printed JSON (indent=2) to stdout.

    Uses ``default=str`` so non-serializable values (e.g.
    ``datetime``, ``UUID``, ``Path``) are coerced via ``str()``.

    Requirements: 03-REQ-5.1, 03-REQ-5.6
    """
    try:
        click.echo(json.dumps(data, indent=2, default=str))
    except BrokenPipeError:
        pass


def emit_line(data: dict[str, Any]) -> None:
    """Write data as compact JSONL (no indentation) to stdout.

    Requirements: 03-REQ-5.2, 03-REQ-5.6
    """
    try:
        click.echo(json.dumps(data, default=str))
    except BrokenPipeError:
        pass


def emit_ok(data: dict[str, Any] | None = None, **kwargs: Any) -> None:
    """Merge ``ok=True`` into *data* and write as pretty-printed JSON.

    Always overwrites any existing ``"ok"`` key in *data* with
    ``True`` — a caller-supplied ``"ok": False`` is silently ignored.

    Requirements: 03-REQ-5.3, 03-REQ-5.6, 03-REQ-5.E1
    """
    if data is None:
        data = kwargs
    merged = {**data, "ok": True}
    emit(merged)


def emit_error(exc_or_message: Exception | str, *, state: str | None = None) -> None:
    """Write a structured JSON error envelope to stdout.

    Always writes to stdout regardless of ``json_mode``.  Calls
    ``error_envelope()`` internally to build the envelope dict.

    For backward compatibility, also accepts a plain error-message
    string, which is wrapped in ``{"error": <message>}``.

    Requirements: 03-REQ-5.4, 03-REQ-5.6
    """
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
        click.echo(json.dumps(envelope, default=str))
    except BrokenPipeError:
        pass


def read_stdin() -> dict[str, Any]:
    """Read and parse a JSON object from piped stdin.

    Returns ``{}`` if stdin is an interactive TTY or if the piped
    input is empty.  Blocks until EOF for piped stdin.

    Raises:
        json.JSONDecodeError: If input is valid UTF-8 but not
            parseable as JSON.
        UnicodeDecodeError: If input contains non-UTF-8 bytes.

    Requirements: 03-REQ-5.5, 03-REQ-5.E2, 03-REQ-5.E3
    """
    if sys.stdin.isatty():
        return {}
    text = sys.stdin.read().strip()
    if not text:
        return {}
    return json.loads(text)  # type: ignore[no-any-return]
