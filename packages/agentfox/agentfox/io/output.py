"""JSON output helpers for CLI commands.

Provides ``emit``, ``emit_ok``, ``emit_line``, ``emit_error``,
``read_stdin``, and ``format_table`` for writing structured JSON
envelopes to stdout, reading JSON input from stdin, and rendering
tabular data consistently for both human and JSON output modes.

All JSON output in agent-fox CLIs should use these functions rather
than raw ``click.echo(json.dumps(...))``.
"""

from __future__ import annotations

import json
import sys
from typing import Any

import click
from rich.table import Table


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


def emit_line(data: dict[str, Any]) -> None:
    """Write a compact JSON object to stdout (JSONL mode, no indent).

    Each call produces exactly one line of output, suitable for
    streaming / JSONL consumers.

    Args:
        data: Dictionary to serialize as JSON.
    """
    click.echo(json.dumps(data, default=str))


def emit_error(message: str) -> None:
    """Write an error envelope ``{"error": "<message>"}`` to stdout.

    Args:
        message: Human-readable error description.
    """
    click.echo(json.dumps({"error": message}))


def read_stdin() -> dict[str, Any]:
    """Read a JSON object from stdin if input is piped (not a TTY).

    Returns an empty dict when stdin is a TTY (interactive terminal)
    or when piped input is empty, so callers never block.

    Returns:
        Parsed JSON dict, or ``{}`` if no input is available.

    Raises:
        json.JSONDecodeError: If stdin contains invalid JSON.
    """
    if sys.stdin.isatty():
        return {}
    text = sys.stdin.read().strip()
    if not text:
        return {}
    return json.loads(text)  # type: ignore[no-any-return]


def _pad_row(row: list[Any], n: int, fill: Any) -> list[Any]:
    """Pad *row* to length *n* using *fill* for missing positions.

    If *row* is already at least length *n*, return it unchanged
    (extra trailing values are preserved).

    Args:
        row: The original row values.
        n: Desired minimum length.
        fill: Value to use for missing positions.

    Returns:
        A list of at least *n* elements.
    """
    if len(row) >= n:
        return row
    return list(row) + [fill] * (n - len(row))


def format_table(
    headers: list[str],
    rows: list[list[Any]],
    json_mode: bool,
) -> list[dict[str, Any]] | Table:
    """Render tabular data for human or JSON output.

    In ``json_mode`` each row becomes a dict keyed by *headers*.  Rows
    shorter than the header list are padded with ``None``; extra trailing
    values are silently ignored.

    In text mode the function returns a Rich ``Table`` ready for
    rendering by ``Console.print()``.  Short rows are padded with empty
    strings.

    Args:
        headers: Column header strings.
        rows: List of row data (each row is a list of cell values).
        json_mode: ``True`` for structured output; ``False`` for Rich table.

    Returns:
        ``list[dict]`` when *json_mode* is ``True``, or a Rich ``Table``
        when ``False``.

    Requirements: 04-REQ-6.1, 04-REQ-6.4, 04-REQ-6.5,
                  04-REQ-6.E1, 04-REQ-6.E2
    """
    n = len(headers)

    if json_mode:
        result: list[dict[str, Any]] = []
        for row in rows:
            padded = _pad_row(row, n, None)
            result.append(dict(zip(headers, padded[:n])))
        return result

    # Rich table for human-readable mode
    table = Table()
    for h in headers:
        table.add_column(h)
    for row in rows:
        padded = _pad_row(row, n, "")
        table.add_row(*(str(v) for v in padded[:n]))
    return table
