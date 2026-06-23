"""Animated spinner for stderr feedback during long-running operations.

This is the canonical location for ``StatusSpinner``.  All CLI tools
should import it from ``agentfox.io`` rather than maintaining their
own copy.
"""

from __future__ import annotations

import sys

from rich.console import Console
from rich.live import Live
from rich.spinner import Spinner
from rich.text import Text


class StatusSpinner:
    """Animated spinner for stderr feedback during long-running operations.

    Use as a context manager.  Returns itself from ``__enter__`` so the
    caller can call :meth:`update` and :meth:`log` to change the status
    message or print permanent lines above the spinner.

    When *quiet* is ``True`` every method is a silent no-op.  When stderr
    is not a TTY, phase messages are printed as plain text lines without
    animation.
    """

    def __init__(self, message: str, *, quiet: bool = False) -> None:
        self._message = message
        self._quiet = quiet
        self._live: Live | None = None
        self._spinner: Spinner | None = None
        self._console: Console | None = None
        self._is_tty: bool = False

    def __enter__(self) -> StatusSpinner:
        if self._quiet:
            return self

        self._console = Console(
            file=sys.stderr,
            force_terminal=False,
            no_color=False,
        )
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
