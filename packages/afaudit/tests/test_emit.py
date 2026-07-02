"""Tests for afaudit.emit module — emit_audit_event function and logging.

TS-01-30: emit_audit_event is callable; calculate_session_cost is NOT present
TS-01-31: stdlib logging with 'afaudit.emit' logger
"""

from __future__ import annotations

from pathlib import Path

import afaudit.emit as emit

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
EMIT_SOURCE = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "emit.py"


class TestEmitModule:
    """TS-01-30: afaudit.emit defines emit_audit_event but not calculate_session_cost.

    Requirement: 01-REQ-8.1
    """

    def test_emit_audit_event_is_callable(self) -> None:
        """emit_audit_event must be callable."""
        assert callable(emit.emit_audit_event)

    def test_calculate_session_cost_not_present(self) -> None:
        """calculate_session_cost must NOT be defined in afaudit.emit."""
        assert not hasattr(emit, "calculate_session_cost"), (
            "calculate_session_cost should remain in agentfox.engine.audit_helpers, "
            "not be migrated to afaudit.emit"
        )


class TestEmitLogging:
    """TS-01-31: afaudit.emit uses stdlib logging with 'afaudit.emit' logger.

    Requirement: 01-REQ-8.2
    """

    def test_imports_stdlib_logging(self) -> None:
        """emit.py must import stdlib logging."""
        source = EMIT_SOURCE.read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """emit.py must create a logger named 'afaudit.emit'."""
        source = EMIT_SOURCE.read_text(encoding="utf-8")
        assert "getLogger('afaudit.emit')" in source or 'getLogger("afaudit.emit")' in source

    def test_no_loguru(self) -> None:
        """emit.py must not import loguru."""
        source = EMIT_SOURCE.read_text(encoding="utf-8")
        assert "loguru" not in source

    def test_no_structlog(self) -> None:
        """emit.py must not import structlog."""
        source = EMIT_SOURCE.read_text(encoding="utf-8")
        assert "structlog" not in source
