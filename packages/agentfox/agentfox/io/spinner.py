"""Animated spinner for stderr feedback during long-running operations.

This is the canonical location for ``StatusSpinner``.  All CLI tools
should import it from ``agentfox.io`` rather than maintaining their
own copy.

Requirements: 03-REQ-8.1, 03-REQ-8.2, 03-REQ-8.3, 03-REQ-8.4,
              03-REQ-8.5, 03-REQ-8.E1
"""

from __future__ import annotations

import sys
from typing import TYPE_CHECKING

from rich.console import Console
from rich.live import Live
from rich.spinner import Spinner
from rich.text import Text

if TYPE_CHECKING:
    from agentfox.ui.display import AppTheme


class StatusSpinner:
    """Animated spinner for stderr feedback during long-running operations.

    Use as a context manager.  Returns itself from ``__enter__`` so the
    caller can call :meth:`update` and :meth:`log` to change the status
    message or print permanent lines above the spinner.

    When *quiet* is ``True`` every method is a silent no-op.  When stderr
    is not a TTY, phase messages are printed as plain text lines without
    animation.

    Parameters
    ----------
    message:
        Initial spinner text.
    quiet:
        When ``True``, all methods become silent no-ops.
    theme:
        Optional ``AppTheme`` whose ``console`` is used for styled
        output.  When ``None`` a plain ``Console(stderr=True)`` is
        created as fallback (03-REQ-8.E1).

    Thread safety
    -------------
    ``__exit__`` must be called from the same thread as ``__enter__``.
    Concurrent ``update()`` and ``log()`` calls are serialized by
    Rich ``Live``'s internal lock (03-REQ-8.5).
    """

    def __init__(
        self,
        message: str,
        *,
        quiet: bool = False,
        theme: AppTheme | None = None,
    ) -> None:
        self._message = message
        self._quiet = quiet
        self._theme = theme
        self._live: Live | None = None
        self._spinner: Spinner | None = None
        self._console: Console | None = None
        self._is_tty: bool = False

    def _resolve_console(self) -> Console:
        """Return the console to use for output.

        Uses ``theme.console`` when a theme is provided (03-REQ-8.3),
        otherwise creates a plain ``Console(stderr=True)`` with no
        styling (03-REQ-8.E1).
        """
        if self._theme is not None:
            return self._theme.console
        return Console(
            file=sys.stderr,
            force_terminal=False,
            no_color=False,
        )

    def __enter__(self) -> StatusSpinner:
        if self._quiet:
            return self

        self._console = self._resolve_console()
        self._is_tty = self._console.is_terminal

        if self._is_tty:
            self._spinner = Spinner("dots", text=Text(self._message))
            self._live = Live(
                self._spinner,
                console=self._console,
                transient=True,
                refresh_per_second=10,
            )
            self._live.start()
        else:
            self._console.print(self._message, highlight=False)

        return self

    def __exit__(self, *exc: object) -> None:
        if self._quiet:
            return

        if self._live is not None:
            self._live.stop()
            self._live = None
            self._spinner = None

        self._console = None

    def update(self, message: str) -> None:
        """Change the spinner's status message."""
        if self._quiet:
            return

        self._message = message

        if self._is_tty and self._spinner is not None:
            self._spinner.update(text=Text(message))
        elif not self._is_tty and self._console is not None:
            self._console.print(message, highlight=False)

    def log(self, message: str) -> None:
        """Print a permanent line above the spinner."""
        if self._quiet:
            return

        if self._is_tty and self._live is not None:
            self._live.console.print(message, highlight=False)
        elif not self._is_tty and self._console is not None:
            self._console.print(message, highlight=False)
