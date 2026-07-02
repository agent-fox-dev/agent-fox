"""Tests for afaudit.sink module and logging discipline across all modules.

TS-01-14: SessionSink Protocol, SinkDispatcher class, SessionOutcome/ToolCall/ToolError
TS-01-15: afaudit.sink uses stdlib logging with 'afaudit.sink' logger
TS-01-46: Every afaudit module uses correct afaudit.<module> logger name
TS-01-47: No afaudit module imports third-party logging libraries
"""

from __future__ import annotations

import dataclasses
from pathlib import Path

import afaudit.sink as sink

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
AFAUDIT_SRC = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit"


class TestSinkModuleTypes:
    """TS-01-14: afaudit.sink defines required types.

    Requirement: 01-REQ-4.1
    """

    def test_session_sink_is_type(self) -> None:
        """SessionSink must be a type (Protocol)."""
        assert isinstance(sink.SessionSink, type)

    def test_sink_dispatcher_is_class(self) -> None:
        """SinkDispatcher must be a class."""
        assert isinstance(sink.SinkDispatcher, type)

    def test_session_outcome_is_dataclass(self) -> None:
        """SessionOutcome must be a dataclass."""
        assert dataclasses.is_dataclass(sink.SessionOutcome)

    def test_tool_call_is_dataclass(self) -> None:
        """ToolCall must be a dataclass."""
        assert dataclasses.is_dataclass(sink.ToolCall)

    def test_tool_error_is_dataclass(self) -> None:
        """ToolError must be a dataclass."""
        assert dataclasses.is_dataclass(sink.ToolError)


class TestSinkLogging:
    """TS-01-15: afaudit.sink uses stdlib logging with 'afaudit.sink' logger.

    Requirement: 01-REQ-4.2
    """

    def test_imports_stdlib_logging(self) -> None:
        """sink.py must import stdlib logging."""
        source = (AFAUDIT_SRC / "sink.py").read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """sink.py must create a logger named 'afaudit.sink'."""
        source = (AFAUDIT_SRC / "sink.py").read_text(encoding="utf-8")
        assert "getLogger('afaudit.sink')" in source or 'getLogger("afaudit.sink")' in source

    def test_no_loguru(self) -> None:
        """sink.py must not import loguru."""
        source = (AFAUDIT_SRC / "sink.py").read_text(encoding="utf-8")
        assert "loguru" not in source


class TestLoggingDiscipline:
    """TS-01-46: Every afaudit module uses correct afaudit.<module> logger name.

    Requirement: 01-REQ-13.1
    """

    MODULE_LOGGER_MAP = {
        "events.py": "afaudit.events",
        "sink.py": "afaudit.sink",
        "trace.py": "afaudit.trace",
        "postmortem.py": "afaudit.postmortem",
        "cleanup.py": "afaudit.cleanup",
        "emit.py": "afaudit.emit",
    }

    def test_each_module_has_correct_logger_name(self) -> None:
        """Each module file must contain getLogger('afaudit.<module>') matching its name."""
        for filename, logger_name in self.MODULE_LOGGER_MAP.items():
            source = (AFAUDIT_SRC / filename).read_text(encoding="utf-8")
            has_logger = f"getLogger('{logger_name}')" in source or f'getLogger("{logger_name}")' in source
            assert has_logger, f"Wrong or missing logger name in {filename}: expected getLogger('{logger_name}')"


class TestNoThirdPartyLogging:
    """TS-01-47: No afaudit module imports third-party logging libraries.

    Requirement: 01-REQ-13.2
    """

    THIRD_PARTY_LOGGERS = ["loguru", "structlog"]

    def test_no_third_party_logging_in_any_module(self) -> None:
        """No afaudit module file may import loguru, structlog, or similar."""
        for py_file in AFAUDIT_SRC.glob("*.py"):
            source = py_file.read_text(encoding="utf-8")
            for lib in self.THIRD_PARTY_LOGGERS:
                assert lib not in source, f"Third-party logging library '{lib}' found in {py_file.name}"
