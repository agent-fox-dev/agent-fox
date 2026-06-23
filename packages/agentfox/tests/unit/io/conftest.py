"""Shared test fixtures for agentfox.io unit tests.

Provides context managers and helpers for capturing stdout/stderr,
mocking stdin/TTY state, and creating mock Click contexts with
OutputManager instances.
"""

from __future__ import annotations

import io
import sys
from collections.abc import Generator
from contextlib import contextmanager
from unittest.mock import MagicMock, patch


@contextmanager
def capture_stdout() -> Generator[io.StringIO, None, None]:
    """Context manager that captures stdout writes to a StringIO buffer."""
    buf = io.StringIO()
    with patch.object(sys, "stdout", buf):
        yield buf


@contextmanager
def capture_stderr() -> Generator[io.StringIO, None, None]:
    """Context manager that captures stderr writes to a StringIO buffer."""
    buf = io.StringIO()
    with patch.object(sys, "stderr", buf):
        yield buf


@contextmanager
def mock_stdin(data: bytes) -> Generator[None, None, None]:
    """Context manager that replaces stdin with a BytesIO containing data.

    The data is decoded as UTF-8 and presented as a text-mode stream
    (matching sys.stdin's normal behavior).
    """
    text = data.decode("utf-8")
    fake = io.StringIO(text)
    fake.isatty = lambda: False  # type: ignore[assignment]
    with patch.object(sys, "stdin", fake):
        yield


@contextmanager
def mock_stdin_raw(data: bytes) -> Generator[None, None, None]:
    """Context manager that replaces stdin with raw bytes (may not be valid UTF-8).

    Used for testing UnicodeDecodeError handling.
    """
    fake = io.BytesIO(data)
    fake.isatty = lambda: False  # type: ignore[assignment]
    # Wrap in a TextIOWrapper that will raise UnicodeDecodeError for bad bytes
    wrapper = io.TextIOWrapper(fake, encoding="utf-8", errors="strict")
    with patch.object(sys, "stdin", wrapper):
        yield


@contextmanager
def mock_stdin_as_tty() -> Generator[None, None, None]:
    """Context manager that mocks stdin as an interactive TTY."""
    fake = io.StringIO("")
    fake.isatty = lambda: True  # type: ignore[assignment]
    with patch.object(sys, "stdin", fake):
        yield


@contextmanager
def mock_tty() -> Generator[None, None, None]:
    """Context manager that mocks both stdout and stderr as TTY devices."""
    with (
        patch.object(sys.stdout, "isatty", return_value=True),
        patch.object(sys.stderr, "isatty", return_value=True),
    ):
        yield


@contextmanager
def mock_non_tty() -> Generator[None, None, None]:
    """Context manager that mocks both stdout and stderr as non-TTY devices."""
    with (
        patch.object(sys.stdout, "isatty", return_value=False),
        patch.object(sys.stderr, "isatty", return_value=False),
    ):
        yield


@contextmanager
def mock_stdout_raises(exc_class: type) -> Generator[None, None, None]:
    """Context manager that replaces stdout with a stream whose write() raises.

    Sets ``encoding`` and ``errors`` on the mock so that
    ``click.echo()`` can resolve the text stream wrapper without
    hitting ``codecs.lookup(MagicMock())`` errors.
    """
    mock_out = MagicMock()
    mock_out.write = MagicMock(side_effect=exc_class())
    # click.echo inspects sys.stdout.encoding / .errors to decide
    # whether to wrap the stream.  Provide realistic values so
    # click.echo passes its internal checks and reaches the .write()
    # call where the exception is raised.
    mock_out.encoding = "utf-8"
    mock_out.errors = "strict"
    with patch.object(sys, "stdout", mock_out):
        yield


def make_mock_context(*, json_mode: bool = False) -> MagicMock:
    """Create a mock Click context with an OutputManager-like object.

    Args:
        json_mode: Whether the mock OutputManager should report json_mode=True.

    Returns:
        A MagicMock with ctx.obj['output'].json_mode set appropriately.
    """
    from agentfox.io import OutputManager

    om = OutputManager(
        json_mode=json_mode,
        quiet=False,
        verbose=False,
        trace=False,
    )
    ctx = MagicMock()
    ctx.obj = {"output": om}
    return ctx
