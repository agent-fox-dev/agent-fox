"""Tests for JSONL streaming progress events (REQ-3).

Test Spec: TS-04-10, TS-04-11, TS-04-12, TS-04-13, TS-04-14,
           TS-04-15, TS-04-16, TS-04-E3, TS-04-E4
Requirements: 04-REQ-3.1, 04-REQ-3.2, 04-REQ-3.3, 04-REQ-3.4,
              04-REQ-3.5, 04-REQ-3.6, 04-REQ-3.7, 04-REQ-3.E1, 04-REQ-3.E2
"""

from __future__ import annotations

import json
import re
from unittest.mock import MagicMock, patch

import pytest


class TestTaskStartedEvent:
    """TS-04-10: ProgressDisplay emits task_started with node_id and timestamp."""

    def test_emit_progress_called_with_task_started(self) -> None:
        """task_started('1.1') calls emit_progress with correct event."""
        from agentfox.io import ProgressDisplay

        om = MagicMock()
        pd = ProgressDisplay(output_manager=om, json_mode=True)
        pd.task_started(node_id="1.1")

        om.emit_progress.assert_called_once()
        call_args = om.emit_progress.call_args
        event_data = call_args[0][0] if call_args[0] else call_args[1]
        assert event_data["event"] == "task_started"
        assert event_data["node_id"] == "1.1"
        assert re.match(r"\d{4}-\d{2}-\d{2}T", event_data["timestamp"])


class TestTaskCompletedEvent:
    """TS-04-11: ProgressDisplay emits task_completed with duration_s."""

    def test_emit_progress_called_with_task_completed(self) -> None:
        """task_completed('1.1') includes duration_s as non-negative float."""
        from agentfox.io import ProgressDisplay

        om = MagicMock()
        pd = ProgressDisplay(output_manager=om, json_mode=True)
        pd.task_started(node_id="1.1")
        pd.task_completed(node_id="1.1")

        calls = om.emit_progress.call_args_list
        last = calls[-1]
        event_data = last[0][0] if last[0] else last[1]
        assert event_data["event"] == "task_completed"
        assert event_data["node_id"] == "1.1"
        assert isinstance(event_data["duration_s"], float)
        assert event_data["duration_s"] >= 0


class TestTaskFailedEvent:
    """TS-04-12: ProgressDisplay emits task_failed with error message."""

    def test_emit_progress_called_with_task_failed(self) -> None:
        """task_failed('1.2', error='...') includes error field."""
        from agentfox.io import ProgressDisplay

        om = MagicMock()
        pd = ProgressDisplay(output_manager=om, json_mode=True)
        pd.task_failed(node_id="1.2", error="something went wrong")

        call = om.emit_progress.call_args
        event_data = call[0][0] if call[0] else call[1]
        assert event_data["event"] == "task_failed"
        assert event_data["node_id"] == "1.2"
        assert event_data["error"] == "something went wrong"


class TestNoEmitProgressInTextMode:
    """TS-04-13: ProgressDisplay does NOT call emit_progress in text mode."""

    def test_emit_progress_not_called_when_json_mode_false(self) -> None:
        """json_mode=False: emit_progress is never called."""
        from agentfox.io import ProgressDisplay

        om = MagicMock()
        om.json_mode = False
        pd = ProgressDisplay(output_manager=om, json_mode=False)
        pd.task_started(node_id="1.1")
        pd.task_completed(node_id="1.1")
        om.emit_progress.assert_not_called()


@pytest.mark.skip(reason="af code --json requires a plan DB and mocked orchestrator not yet wired in tests")
class TestStdoutStderrSeparation:
    """TS-04-14: JSONL progress on stderr, final result on stdout.

    Uses ``mix_stderr=False`` to capture stdout and stderr independently
    so the stdout/stderr separation contract can be validated.
    """

    def test_stdout_is_valid_json_stderr_is_jsonl(self, cli_runner_separated) -> None:
        """stdout lines are JSON; stderr lines are JSONL with 'event' key.

        Uses CliRunner(mix_stderr=False) to capture stdout and stderr
        separately, validating no cross-contamination per 04-PROP-1.
        """
        from af.app import main

        result = cli_runner_separated.invoke(main, ["code", "--json"])
        assert result.exit_code == 0

        # All stdout lines must be valid JSON (the final result)
        for line in result.output.strip().splitlines():
            if line.strip():
                json.loads(line)

        # All stderr lines must be valid JSONL progress events
        stderr_text = result.stderr if hasattr(result, "stderr") else ""
        stderr_lines = [line for line in stderr_text.strip().splitlines() if line.strip()]
        assert len(stderr_lines) >= 2, "Expected at least 2 JSONL progress events on stderr"
        for line in stderr_lines:
            obj = json.loads(line)
            assert "event" in obj, f"JSONL line missing 'event' key: {line}"


@pytest.mark.skip(reason="af code --json requires a plan DB and mocked orchestrator not yet wired in tests")
class TestCodeJsonlEvents:
    """TS-04-15: af code --json emits JSONL events on stderr.

    Uses ``mix_stderr=False`` to validate stderr JSONL events independently
    from the stdout JSON result.
    """

    def test_code_emits_progress_events(self, cli_runner_separated) -> None:
        """af code --json stderr has task_started/completed/failed events.

        Uses CliRunner(mix_stderr=False) to validate that stderr contains
        at least one JSONL event with valid event, node_id, and timestamp
        fields.  stdout must contain the final JSON result.
        """
        from af.app import main

        result = cli_runner_separated.invoke(main, ["code", "--json"])
        assert result.exit_code == 0

        # stdout: final JSON result
        final = json.loads(result.output)
        assert isinstance(final, dict)
        # stderr: JSONL progress events
        stderr_text = result.stderr if hasattr(result, "stderr") else ""
        events = [json.loads(line) for line in stderr_text.strip().splitlines() if line.strip()]
        assert len(events) > 0, "Expected at least one JSONL event on stderr"
        for ev in events:
            assert ev["event"] in ("task_started", "task_completed", "task_failed")
            assert "node_id" in ev
            assert "timestamp" in ev


class TestBrokenPipeSuppressed:
    """TS-04-E3: emit_progress suppresses IO errors from stderr."""

    def test_broken_pipe_does_not_raise(self) -> None:
        """BrokenPipeError on stderr write is suppressed."""
        from agentfox.io import OutputManager

        class BrokenStderr:
            def write(self, data: str) -> None:
                raise BrokenPipeError("broken pipe")

            def flush(self) -> None:
                pass

        om = OutputManager(json_mode=True, stderr=BrokenStderr())
        # Should not raise
        om.emit_progress(
            {
                "event": "task_started",
                "node_id": "1.1",
                "timestamp": "2026-01-01T00:00:00Z",
            }
        )


class TestNullNodeId:
    """TS-04-E4: ProgressDisplay emits event with node_id=null for None.

    When node_id is None or empty, the JSONL event is emitted with
    ``node_id=null`` and a warning message is written to stderr.
    """

    def test_null_node_id_emits_with_warning(self) -> None:
        """task_started(node_id=None) emits with node_id=null and warns on stderr."""
        import io

        from agentfox.io import ProgressDisplay

        om = MagicMock()
        stderr_capture = io.StringIO()
        pd = ProgressDisplay(output_manager=om, json_mode=True)

        with patch("sys.stderr", stderr_capture):
            pd.task_started(node_id=None)

        # Verify event emitted with null node_id
        call = om.emit_progress.call_args
        event_data = call[0][0] if call[0] else call[1]
        assert event_data["node_id"] is None

        # Verify warning written to stderr (04-REQ-3.E2)
        warning_output = stderr_capture.getvalue()
        assert "WARNING" in warning_output or "warning" in warning_output.lower(), (
            f"Expected a warning about missing node_id on stderr, got: {warning_output!r}"
        )
        assert "node_id" in warning_output.lower()
