"""Unit tests for agentfox.io.json — unified JSON serialization functions.

Test Spec: TS-03-23, TS-03-24, TS-03-25, TS-03-26, TS-03-27, TS-03-28,
           TS-03-E4, TS-03-E5, TS-03-E6
Requirements: 03-REQ-5.1, 03-REQ-5.2, 03-REQ-5.3, 03-REQ-5.4,
              03-REQ-5.5, 03-REQ-5.6, 03-REQ-5.E1, 03-REQ-5.E2, 03-REQ-5.E3
"""

from __future__ import annotations

import json
from datetime import datetime

import pytest

from tests.unit.io.conftest import (
    capture_stdout,
    mock_stdin,
    mock_stdin_as_tty,
    mock_stdin_raw,
    mock_stdout_raises,
)


class TestEmit:
    """TS-03-23: emit() writes pretty-printed JSON with indent=2 and default=str."""

    def test_emit_indent2_default_str(self) -> None:
        """03-REQ-5.1: Valid JSON with 2-space indent; non-serializable via str()."""
        from agentfox.io import emit

        dt = datetime(2026, 1, 1)
        with capture_stdout() as out:
            emit({"dt": dt, "key": "value"})
        parsed = json.loads(out.getvalue())
        assert parsed["key"] == "value"
        assert parsed["dt"] == str(dt)
        assert "  " in out.getvalue()  # indent=2


class TestEmitLine:
    """TS-03-24: emit_line() writes compact JSON with no indentation."""

    def test_emit_line_compact(self) -> None:
        """03-REQ-5.2: Compact JSON with no indentation followed by newline."""
        from agentfox.io import emit_line

        with capture_stdout() as out:
            emit_line({"key": "value"})
        line = out.getvalue().strip()
        parsed = json.loads(line)
        assert parsed == {"key": "value"}
        assert "\n  " not in line  # no indentation


class TestEmitOk:
    """TS-03-25: emit_ok() merges ok=True and writes pretty-printed JSON."""

    def test_emit_ok_merges_ok_true(self) -> None:
        """03-REQ-5.3: JSON with ok=true and caller data with indent=2."""
        from agentfox.io import emit_ok

        with capture_stdout() as out:
            emit_ok({"result": "done"})
        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is True
        assert parsed["result"] == "done"
        assert "  " in out.getvalue()


class TestEmitError:
    """TS-03-26: emit_error() always writes structured JSON error envelope to stdout."""

    def test_emit_error_writes_envelope(self) -> None:
        """03-REQ-5.4: JSON error envelope with ok=false regardless of json_mode."""
        from agentfox.core.errors import AgentFoxError
        from agentfox.io import emit_error

        class ConfigError(AgentFoxError):
            pass

        exc = ConfigError("Config not found")
        with capture_stdout() as out:
            emit_error(exc)
        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is False
        assert parsed["error"]["type"] == "config_error"
        assert parsed["error"]["message"] == "Config not found"
        assert isinstance(parsed["error"]["retryable"], bool)


class TestReadStdin:
    """TS-03-27: read_stdin() returns {} for TTY/empty, parses valid JSON."""

    def test_returns_empty_for_tty(self) -> None:
        """03-REQ-5.5: Returns {} for interactive TTY."""
        from agentfox.io import read_stdin

        with mock_stdin_as_tty():
            result = read_stdin()
        assert result == {}

    def test_returns_empty_for_empty_pipe(self) -> None:
        """03-REQ-5.5: Returns {} for empty piped stdin."""
        from agentfox.io import read_stdin

        with mock_stdin(b""):
            result = read_stdin()
        assert result == {}

    def test_parses_valid_json(self) -> None:
        """03-REQ-5.5: Returns parsed dict for valid piped JSON."""
        from agentfox.io import read_stdin

        with mock_stdin(b'{"key": "val"}'):
            result = read_stdin()
        assert result == {"key": "val"}


class TestBrokenPipeErrorSuppression:
    """TS-03-28: BrokenPipeError is silently suppressed in all four emit functions."""

    def test_emit_suppresses_broken_pipe(self) -> None:
        """03-REQ-5.6: No exception propagated from emit()."""
        from agentfox.io import emit

        with mock_stdout_raises(BrokenPipeError):
            emit({"k": "v"})  # must not raise

    def test_emit_line_suppresses_broken_pipe(self) -> None:
        """03-REQ-5.6: No exception propagated from emit_line()."""
        from agentfox.io import emit_line

        with mock_stdout_raises(BrokenPipeError):
            emit_line({"k": "v"})  # must not raise

    def test_emit_ok_suppresses_broken_pipe(self) -> None:
        """03-REQ-5.6: No exception propagated from emit_ok()."""
        from agentfox.io import emit_ok

        with mock_stdout_raises(BrokenPipeError):
            emit_ok({"k": "v"})  # must not raise

    def test_emit_error_suppresses_broken_pipe(self) -> None:
        """03-REQ-5.6: No exception propagated from emit_error()."""
        from agentfox.io import emit_error

        with mock_stdout_raises(BrokenPipeError):
            emit_error(Exception("err"))  # must not raise


class TestReadStdinMalformedJson:
    """TS-03-E5: read_stdin() raises json.JSONDecodeError for malformed JSON."""

    def test_raises_json_decode_error(self) -> None:
        """03-REQ-5.E2: json.JSONDecodeError propagates to caller."""
        from agentfox.io import read_stdin

        with mock_stdin(b"not valid json"):
            with pytest.raises(json.JSONDecodeError):
                read_stdin()


class TestReadStdinNonUtf8:
    """TS-03-E6: read_stdin() raises UnicodeDecodeError for non-UTF-8 bytes."""

    def test_raises_unicode_decode_error(self) -> None:
        """03-REQ-5.E3: UnicodeDecodeError propagates to caller."""
        from agentfox.io import read_stdin

        with mock_stdin_raw(b"\xff\xfe"):
            with pytest.raises(UnicodeDecodeError):
                read_stdin()


class TestEmitOkOverwritesCallerOk:
    """TS-03-E4: emit_ok() overwrites caller-supplied ok=False with True."""

    def test_overwrites_ok_false(self) -> None:
        """03-REQ-5.E1: Emitted JSON contains ok=true; caller's ok=false overwritten."""
        from agentfox.io import emit_ok

        with capture_stdout() as out:
            emit_ok({"ok": False, "result": "done"})
        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is True
        assert parsed["result"] == "done"
