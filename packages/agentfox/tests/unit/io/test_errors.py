"""Unit tests for agentfox.io.errors — error envelope and routing.

Test Spec: TS-03-29, TS-03-30, TS-03-31, TS-03-32, TS-03-33, TS-03-34,
           TS-03-35, TS-03-36, TS-03-37, TS-03-38, TS-03-39, TS-03-40,
           TS-03-E7, TS-03-E8, TS-03-E9
Requirements: 03-REQ-6.1, 03-REQ-6.2, 03-REQ-6.3, 03-REQ-6.4,
              03-REQ-6.5, 03-REQ-6.6, 03-REQ-6.7, 03-REQ-6.8,
              03-REQ-6.E1, 03-REQ-7.1, 03-REQ-7.2, 03-REQ-7.3,
              03-REQ-7.4, 03-REQ-7.E1, 03-REQ-7.E2
"""

from __future__ import annotations

import json
import sys

import click
import pytest
from agentfox.core.errors import AgentFoxError
from click.testing import CliRunner

from tests.unit.io.conftest import capture_stderr, capture_stdout


class TestErrorEnvelopeBasic:
    """TS-03-29: error_envelope() returns valid envelope dict."""

    def test_envelope_has_required_fields(self) -> None:
        """03-REQ-6.1: ok=False, non-empty type, message==str(exc), retryable is bool."""
        from agentfox.io import error_envelope

        exc = ValueError("bad value")
        result = error_envelope(exc)
        assert result["ok"] is False
        assert isinstance(result["error"]["type"], str)
        assert len(result["error"]["type"]) > 0
        assert result["error"]["message"] == str(exc)
        assert isinstance(result["error"]["retryable"], bool)


class TestErrorEnvelopeAgentFoxError:
    """TS-03-30: error_envelope() derives type from AgentFoxError via snake_case."""

    def test_known_subclass_snake_case(self) -> None:
        """03-REQ-6.2: ConfigError -> 'config_error'."""
        from agentfox.io import error_envelope

        class ConfigError(AgentFoxError):
            retryable = False

        result = error_envelope(ConfigError("msg"))
        assert result["error"]["type"] == "config_error"

    def test_dynamic_unknown_subclass_snake_case(self) -> None:
        """03-REQ-6.2: Any AgentFoxError subclass never falls to internal_error."""
        from agentfox.io import error_envelope

        MyError = type("MyCustomFoxError", (AgentFoxError,), {"retryable": False})
        result = error_envelope(MyError("msg"))
        assert result["error"]["type"] == "my_custom_fox_error"
        assert result["error"]["type"] != "internal_error"


class TestErrorEnvelopeAgentError:
    """TS-03-31: error_envelope() maps AgentError using .category when present."""

    def test_with_category(self) -> None:
        """03-REQ-6.3: type=category value when .category present."""
        agentspec_errors = pytest.importorskip("agentspec.errors")
        from agentfox.io import error_envelope

        # CRITICAL: set the PUBLIC attribute .category, NOT the private ._category
        exc = agentspec_errors.AgentError(
            "too many requests",
            category="rate_limit_error",
            retryable=True,
        )
        result = error_envelope(exc)
        assert result["error"]["type"] == "rate_limit_error"
        assert result["error"]["retryable"] is True

    def test_without_explicit_category_uses_default(self) -> None:
        """03-REQ-6.3: AgentError without explicit category -> type='agent_error', retryable=False."""
        agentspec_errors = pytest.importorskip("agentspec.errors")
        from agentfox.io import error_envelope

        exc = agentspec_errors.AgentError("generic agent error")
        result = error_envelope(exc)
        # Assert the EXACT expected type string, not just isinstance(str)
        assert result["error"]["type"] == "agent_error", f"expected 'agent_error', got {result['error']['type']!r}"
        # Assert the EXACT expected retryable value, not just isinstance(bool)
        assert result["error"]["retryable"] is False, f"expected False, got {result['error']['retryable']!r}"


class TestErrorEnvelopeSessionError:
    """TS-03-32: error_envelope() always maps SessionError to session_error."""

    def test_session_error_mapping(self) -> None:
        """03-REQ-6.4: type='session_error' and retryable=False."""
        agentspec_errors = pytest.importorskip("agentspec.errors")
        from agentfox.io import error_envelope

        exc = agentspec_errors.SessionError("session expired")
        result = error_envelope(exc)
        assert result["error"]["type"] == "session_error"
        assert result["error"]["retryable"] is False


class TestErrorEnvelopeClickException:
    """TS-03-33: error_envelope() maps click.ClickException to input_error."""

    def test_click_exception_mapping(self) -> None:
        """03-REQ-6.5: type='input_error' and retryable=False."""
        from agentfox.io import error_envelope

        exc = click.ClickException("bad input")
        result = error_envelope(exc)
        assert result["error"]["type"] == "input_error"
        assert result["error"]["retryable"] is False


class TestErrorEnvelopeUnknownException:
    """TS-03-34: Unknown exceptions map to internal_error with detail field."""

    def test_unknown_exception_internal_error(self) -> None:
        """03-REQ-6.6: type='internal_error', detail='RuntimeError'."""
        from agentfox.io import error_envelope

        result = error_envelope(RuntimeError("unexpected"))
        assert result["error"]["type"] == "internal_error"
        assert result["error"]["retryable"] is False
        assert result["error"]["detail"] == "RuntimeError"

    def test_well_typed_exception_omits_detail(self) -> None:
        """03-REQ-6.6: Well-typed exceptions omit 'detail' key."""
        from agentfox.io import error_envelope

        class ConfigError(AgentFoxError):
            retryable = False

        result = error_envelope(ConfigError("msg"))
        assert "detail" not in result["error"]


class TestErrorEnvelopeStateParam:
    """TS-03-35: error_envelope() includes/omits state based on argument."""

    def test_state_included_when_non_none(self) -> None:
        """03-REQ-6.7: state='executing' appears as top-level key."""
        from agentfox.io import error_envelope

        result = error_envelope(ValueError("err"), state="executing")
        assert result.get("state") == "executing"

    def test_state_omitted_when_none(self) -> None:
        """03-REQ-6.7: No 'state' key when state=None."""
        from agentfox.io import error_envelope

        result = error_envelope(ValueError("err"), state=None)
        assert "state" not in result


class TestErrorsLazyImport:
    """TS-03-36: agentfox/io/errors.py imports agentspec lazily without ImportError."""

    def test_module_loads_without_agentspec(self) -> None:
        """03-REQ-6.8: Module imports successfully even if agentspec absent."""
        # This test just verifies the module is importable —
        # agentspec may or may not be installed in the test environment.
        import agentfox.io.errors

        assert hasattr(agentfox.io.errors, "error_envelope")


class TestCliErrorHandlerJsonMode:
    """TS-03-37: cli_error_handler() calls emit_error() for JSON envelope when json_mode=True."""

    def test_json_mode_writes_envelope_to_stdout(self) -> None:
        """03-REQ-7.1: stdout contains valid JSON error envelope."""
        from agentfox.io.errors import cli_error_handler

        from tests.unit.io.conftest import make_mock_context

        ctx = make_mock_context(json_mode=True)
        exc = ValueError("fail")
        with capture_stdout() as out:
            cli_error_handler(ctx, exc)
        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is False
        assert parsed["error"]["type"] == "internal_error"


class TestCliErrorHandlerHumanMode:
    """TS-03-38: cli_error_handler() writes plain text to stderr when json_mode=False."""

    def test_human_mode_writes_to_stderr(self) -> None:
        """03-REQ-7.2: Plain text written to stderr; nothing to stdout."""
        from agentfox.io.errors import cli_error_handler

        from tests.unit.io.conftest import make_mock_context

        ctx = make_mock_context(json_mode=False)
        exc = ValueError("fail")
        with capture_stdout() as out, capture_stderr() as err:
            cli_error_handler(ctx, exc)
        assert "fail" in err.getvalue()
        assert out.getvalue() == ""


class TestHandleCliErrorsDecorator:
    """TS-03-39: handle_cli_errors catches Exception, propagates SystemExit/KeyboardInterrupt."""

    def test_catches_exception(self) -> None:
        """03-REQ-7.3: Exception is caught and routed."""
        from agentfox.io.errors import handle_cli_errors

        @handle_cli_errors
        def raises_exception() -> None:
            raise ValueError("caught")

        # ValueError should be caught; handler may call sys.exit(1)
        try:
            raises_exception()
        except SystemExit:
            pass  # expected from handler's sys.exit(1)
        except ValueError:
            pytest.fail("ValueError should be caught by handle_cli_errors")

    def test_propagates_system_exit(self) -> None:
        """03-REQ-7.3: SystemExit propagates without being caught."""
        from agentfox.io.errors import handle_cli_errors

        @handle_cli_errors
        def raises_system_exit() -> None:
            raise SystemExit(0)

        with pytest.raises(SystemExit) as exc_info:
            raises_system_exit()
        assert exc_info.value.code == 0

    def test_propagates_keyboard_interrupt(self) -> None:
        """03-REQ-7.3: KeyboardInterrupt propagates without being caught."""
        from agentfox.io.errors import handle_cli_errors

        @handle_cli_errors
        def raises_keyboard_interrupt() -> None:
            raise KeyboardInterrupt()

        with pytest.raises(KeyboardInterrupt):
            raises_keyboard_interrupt()


class TestAgentFoxGroupErrorRouting:
    """TS-03-40: AgentFoxGroup.invoke() routes errors correctly."""

    def test_exception_results_in_exit_code_1(self) -> None:
        """03-REQ-7.4: ValueError results in exit code 1."""
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def cli() -> None:
            pass

        @cli.command()
        def fail() -> None:
            raise ValueError("oops")

        runner = CliRunner()
        result = runner.invoke(cli, ["fail"])
        assert result.exit_code == 1

    def test_system_exit_0_results_in_exit_code_0(self) -> None:
        """03-REQ-7.4: SystemExit(0) results in exit code 0."""
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def cli() -> None:
            pass

        @cli.command()
        def succeed() -> None:
            raise SystemExit(0)

        runner = CliRunner()
        result = runner.invoke(cli, ["succeed"])
        assert result.exit_code == 0


class TestErrorEnvelopeFallbackWithoutAgentspec:
    """TS-03-E7: Without agentspec, exceptions fall through to internal_error."""

    def test_fake_agent_error_falls_through(self) -> None:
        """03-REQ-6.E1: Valid envelope with type='internal_error' and detail field."""
        from agentfox.io import error_envelope

        # Create a mock that looks like AgentError but is not the real one
        class FakeAgentError(Exception):
            pass

        exc = FakeAgentError("agent error message")
        result = error_envelope(exc)
        assert result["error"]["type"] == "internal_error"
        assert result["error"]["retryable"] is False
        assert "detail" in result["error"]


class TestAgentFoxGroupSystemExit0:
    """TS-03-E8: AgentFoxGroup re-raises SystemExit(0) without routing."""

    def test_system_exit_0_no_error_envelope(self) -> None:
        """03-REQ-7.E1: Exit code 0; no error envelope emitted."""
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def cli() -> None:
            pass

        @cli.command()
        def sub() -> None:
            sys.exit(0)

        runner = CliRunner()
        result = runner.invoke(cli, ["sub"])
        assert result.exit_code == 0
        assert result.output.strip() == ""  # no error envelope


class TestAgentFoxGroupKeyboardInterrupt:
    """TS-03-E9: AgentFoxGroup re-raises KeyboardInterrupt without routing."""

    def test_keyboard_interrupt_propagates(self) -> None:
        """03-REQ-7.E2: KeyboardInterrupt propagates; no error envelope."""
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def cli() -> None:
            pass

        @cli.command()
        def sub() -> None:
            raise KeyboardInterrupt()

        runner = CliRunner()
        result = runner.invoke(cli, ["sub"])

        # 1. KeyboardInterrupt must propagate — CliRunner stores it in result.exception
        assert isinstance(result.exception, KeyboardInterrupt), (
            f"expected KeyboardInterrupt to propagate, got {type(result.exception).__name__}"
        )

        # 2. AgentFoxGroup must NOT emit an error envelope for KeyboardInterrupt
        assert result.output.strip() == "" or not _is_json_error_envelope(result.output), (
            "AgentFoxGroup must not emit an error envelope for KeyboardInterrupt"
        )


def _is_json_error_envelope(text: str) -> bool:
    """Check if text is a JSON error envelope with ok=False."""
    try:
        parsed = json.loads(text)
        return parsed.get("ok") is False and "error" in parsed
    except (json.JSONDecodeError, TypeError):
        return False
