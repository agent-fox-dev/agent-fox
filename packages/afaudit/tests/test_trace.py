"""Tests for afaudit.trace module — AgentTraceSink, transcript utilities.

TS-01-17: AgentTraceSink is a class, reconstruct_transcript and truncate_tool_input callable
TS-01-18: truncate_tool_input re-exported from top-level afaudit namespace
TS-01-19: stdlib logging with 'afaudit.trace' logger, no third-party logging
"""

from __future__ import annotations

from pathlib import Path

import afaudit
import afaudit.trace as trace

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
TRACE_SOURCE = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "trace.py"


class TestTraceModuleTypes:
    """TS-01-17: afaudit.trace defines required symbols.

    Requirement: 01-REQ-5.1
    """

    def test_agent_trace_sink_is_class(self) -> None:
        """AgentTraceSink must be a class."""
        assert isinstance(trace.AgentTraceSink, type)

    def test_reconstruct_transcript_is_callable(self) -> None:
        """reconstruct_transcript must be callable."""
        assert callable(trace.reconstruct_transcript)

    def test_truncate_tool_input_is_callable(self) -> None:
        """truncate_tool_input must be callable."""
        assert callable(trace.truncate_tool_input)


class TestTruncateToolInputReexport:
    """TS-01-18: truncate_tool_input is part of the public API.

    Requirement: 01-REQ-5.2
    """

    def test_truncate_tool_input_importable_from_afaudit(self) -> None:
        """truncate_tool_input must be importable from top-level afaudit."""
        from afaudit import truncate_tool_input  # noqa: F811

        assert callable(truncate_tool_input)

    def test_truncate_tool_input_same_object(self) -> None:
        """Top-level re-export must be the same object as afaudit.trace version."""
        assert afaudit.truncate_tool_input is trace.truncate_tool_input


class TestTraceLogging:
    """TS-01-19: afaudit.trace uses stdlib logging with 'afaudit.trace' logger.

    Requirement: 01-REQ-5.3
    """

    def test_imports_stdlib_logging(self) -> None:
        """trace.py must import stdlib logging."""
        source = TRACE_SOURCE.read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """trace.py must create a logger named 'afaudit.trace'."""
        source = TRACE_SOURCE.read_text(encoding="utf-8")
        assert "getLogger('afaudit.trace')" in source or 'getLogger("afaudit.trace")' in source

    def test_no_loguru(self) -> None:
        """trace.py must not import loguru."""
        source = TRACE_SOURCE.read_text(encoding="utf-8")
        assert "loguru" not in source

    def test_no_structlog(self) -> None:
        """trace.py must not import structlog."""
        source = TRACE_SOURCE.read_text(encoding="utf-8")
        assert "structlog" not in source
