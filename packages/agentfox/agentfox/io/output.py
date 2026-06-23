"""OutputManager — central coordinator for CLI output.

Provides the ``OutputManager`` class that controls json_mode, quiet,
verbose, and trace settings, and ``get_output_manager()`` to retrieve
the active instance from Click context or a fallback.

Also re-exports the JSON serialization functions from ``agentfox.io.json``
for backward compatibility, and provides ``format_table`` for tabular
data rendering.

Requirements: 03-REQ-2, 03-REQ-4, 04-REQ-6
"""

from __future__ import annotations

import json
import sys
from typing import TYPE_CHECKING, Any

import click
from rich.console import Console
from rich.table import Table

# Re-export JSON functions for backward compatibility with existing imports
from agentfox.io.json import emit, emit_error, emit_line, emit_ok, read_stdin

if TYPE_CHECKING:
    from collections.abc import Callable

__all__ = [
    "OutputManager",
    "format_table",
    "get_output_manager",
    "emit",
    "emit_error",
    "emit_line",
    "emit_ok",
    "read_stdin",
]


class OutputManager:
    """Central coordinator for all CLI output.

    Controls whether output is written as JSON to stdout or as
    human-readable text, and provides format dispatch methods.

    Attributes:
        json_mode: If True, output is JSON to stdout.
        quiet: If True, suppress banner and status lines on stderr.
        verbose: If True, enable verbose output.
        trace: If True, enable trace-level output.
        console: Rich Console instance for stderr output.

    Requirements: 03-REQ-2.1, 03-REQ-4, 04-REQ-3
    """

    def __init__(
        self,
        *,
        json_mode: bool = False,
        quiet: bool = False,
        verbose: bool = False,
        trace: bool = False,
        stdout: Any = None,
        stderr: Any = None,
    ) -> None:
        self.json_mode = json_mode
        self.quiet = quiet
        self.verbose = verbose
        self.trace = trace
        self._explicit_stdout = stdout
        self._explicit_stderr = stderr
        self.console = Console(stderr=True)

    @property
    def _stdout(self) -> Any:
        """Return the stdout stream: explicit override or current sys.stdout."""
        return self._explicit_stdout if self._explicit_stdout is not None else sys.stdout

    @property
    def _stderr(self) -> Any:
        """Return the stderr stream: explicit override or current sys.stderr."""
        return self._explicit_stderr if self._explicit_stderr is not None else sys.stderr

    def emit_json(self, data: dict[str, Any]) -> None:
        """Write JSON to stdout if json_mode is True; no-op otherwise.

        Requirements: 03-REQ-4.1, 03-REQ-4.2
        """
        if self.json_mode:
            print(json.dumps(data, indent=2, default=str), file=self._stdout)

    def emit_human(self, text: str) -> None:
        """Write plain text to stdout if json_mode is False; no-op otherwise.

        Requirements: 03-REQ-4.3, 03-REQ-4.4
        """
        if not self.json_mode:
            print(text, file=self._stdout)

    def emit(
        self,
        data: dict[str, Any],
        human_fn: Callable[[], None] | None = None,
    ) -> None:
        """Dispatch output to JSON or human format.

        In json_mode, calls ``emit_json(data)``.  Otherwise, calls
        ``human_fn()`` if provided.  Silent no-op when json_mode is
        False and human_fn is None.

        Requirements: 03-REQ-4.5, 03-REQ-4.6, 03-REQ-4.7
        """
        if self.json_mode:
            self.emit_json(data)
        elif human_fn is not None:
            human_fn()

    def banner(self) -> None:
        """Render the themed banner on stderr.

        Suppressed when ``json_mode=True`` or ``quiet=True``.

        Requirements: 03-REQ-4.8
        """
        if self.json_mode or self.quiet:
            return

        try:
            from rich.theme import Theme as RichTheme

            from agentfox.core.config import ThemeConfig
            from agentfox.ui.display import create_theme, render_banner

            theme = create_theme(ThemeConfig())
            # 03-REQ-4.8: Banner must render to stderr, not stdout.
            # AppTheme creates Console() targeting stdout by default.
            # Replace it with a stderr-targeting Console that preserves
            # the same Rich theme styles for colored output.
            cfg = theme.config
            styles: dict[str, str] = {}
            for role in ("header", "success", "error", "warning", "info", "tool", "muted"):
                val = getattr(cfg, role, "")
                if val:
                    styles[role] = val
            theme.console = Console(stderr=True, theme=RichTheme(styles))
            render_banner(theme, quiet=False)
        except Exception:
            pass

    def status(self, message: str) -> None:
        """Write a status message to stderr; suppressed when quiet=True.

        Requirements: 03-REQ-4.9
        """
        if self.quiet:
            return
        self.console.print(message)

    def emit_progress(self, event: dict[str, Any]) -> None:
        """Write a JSONL progress event line to stderr.

        Only writes when ``json_mode`` is ``True``.  IO errors
        (``BrokenPipeError``, ``OSError``) are silently suppressed so
        that a broken pipe on stderr never crashes the main command.

        Args:
            event: A dict with at least ``"event"`` and ``"timestamp"``
                keys.  Serialised as a single JSON line to stderr.

        Requirements: 04-REQ-3.1, 04-REQ-3.5, 04-REQ-3.E1
        """
        if not self.json_mode:
            return
        try:
            line = json.dumps(event, default=str)
            self._stderr.write(line + "\n")
            self._stderr.flush()
        except (BrokenPipeError, OSError):
            pass


def get_output_manager() -> OutputManager:
    """Return the active OutputManager from Click context, or a fallback.

    When a Click context is active and ``ctx.obj["output"]`` exists,
    returns that instance.  Otherwise returns a fallback with all
    flags set to False.

    The ``AF_AGENT`` env var is **not** consulted in the fallback path.

    Requirements: 03-REQ-2.3, 03-REQ-2.E1
    """
    try:
        ctx = click.get_current_context(silent=True)
        if ctx is not None and isinstance(ctx.obj, dict):
            om = ctx.obj.get("output")
            if isinstance(om, OutputManager):
                return om
    except Exception:
        pass

    # Fallback: fixed defaults, AF_AGENT not consulted
    return OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)


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
