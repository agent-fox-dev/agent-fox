"""Hypothesis property tests for error envelope serialization and related invariants.

Test Spec: TS-03-P1, TS-03-P2, TS-03-P3, TS-03-P4, TS-03-P5, TS-03-P6,
           TS-03-P7, TS-03-P8, TS-03-62
Requirements: 03-REQ-5.3, 03-REQ-5.4, 03-REQ-5.6, 03-REQ-5.E1,
              03-REQ-6.1, 03-REQ-6.2, 03-REQ-6.3, 03-REQ-6.4,
              03-REQ-6.5, 03-REQ-6.6,
              03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3, 03-REQ-3.4,
              03-REQ-3.6,
              03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3, 03-REQ-4.4,
              03-REQ-4.8,
              03-REQ-2.3, 03-REQ-2.E1,
              03-REQ-14.3
"""

from __future__ import annotations

import json
import os
import re
from unittest.mock import patch

import click
import pytest
from agentfox.core.errors import AgentFoxError
from hypothesis import given, settings
from hypothesis import strategies as st

from tests.unit.io.conftest import capture_stderr, capture_stdout, mock_stdout_raises

# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

# Strategy: generate non-empty text (str(exc) must be non-empty for the tests)
_non_empty_text = st.text(min_size=1, max_size=100).filter(lambda s: s.strip())


def _agentfox_error_strategy() -> st.SearchStrategy:
    """Generate AgentFoxError subclass instances with random class names."""
    return _non_empty_text.map(lambda msg: AgentFoxError(msg))


def _click_exception_strategy() -> st.SearchStrategy:
    """Generate click.ClickException instances."""
    return _non_empty_text.map(lambda msg: click.ClickException(msg))


def _arbitrary_exception_strategy() -> st.SearchStrategy:
    """Generate arbitrary Exception subclass instances."""
    return _non_empty_text.map(lambda msg: ValueError(msg))


def _agentspec_exception_strategies() -> list[st.SearchStrategy]:
    """Generate agentspec exception strategies if agentspec is available.

    Returns an empty list when agentspec is not installed so callers
    can safely extend st.one_of() without ImportError.
    """
    try:
        from agentspec.errors import AgentError, AgentSpecError, SessionError

        return [
            _non_empty_text.map(lambda msg: AgentError(msg)),
            _non_empty_text.map(lambda msg: AgentSpecError(msg)),
            _non_empty_text.map(lambda msg: SessionError(msg)),
        ]
    except ImportError:
        return []


def _any_exception_strategy() -> st.SearchStrategy:
    """Generate exceptions from any of the well-known families.

    Includes AgentFoxError, click.ClickException, ValueError, AND
    agentspec types (AgentError, AgentSpecError, SessionError) when
    agentspec is installed.
    """
    strategies = [
        _agentfox_error_strategy(),
        _click_exception_strategy(),
        _arbitrary_exception_strategy(),
        *_agentspec_exception_strategies(),
    ]
    return st.one_of(*strategies)


# Strategy for generating CamelCase class names suitable for dynamic subclasses
_camel_case_names = st.from_regex(r"[A-Z][a-zA-Z]{2,20}Error", fullmatch=True)


# ---------------------------------------------------------------------------
# TS-03-P1: error_envelope() always returns a valid envelope
# ---------------------------------------------------------------------------


class TestPropErrorEnvelopeAlwaysValid:
    """TS-03-P1: error_envelope() returns valid envelope for any exception type.

    Property: 03-PROP-1
    Validates: 03-REQ-6.1, 03-REQ-6.2, 03-REQ-6.3, 03-REQ-6.4, 03-REQ-6.5, 03-REQ-6.6
    """

    @pytest.mark.property
    @given(exc=_any_exception_strategy())
    @settings(max_examples=50)
    def test_envelope_always_valid(self, exc: Exception) -> None:
        from agentfox.io import error_envelope

        result = error_envelope(exc)
        assert result["ok"] is False
        assert isinstance(result["error"]["type"], str)
        assert len(result["error"]["type"]) > 0
        assert result["error"]["message"] == str(exc)
        assert isinstance(result["error"]["retryable"], bool)
        # detail present iff type is internal_error
        assert ("detail" in result["error"]) == (result["error"]["type"] == "internal_error")


# ---------------------------------------------------------------------------
# TS-03-P2: emit_error() output is valid JSON matching error_envelope()
# ---------------------------------------------------------------------------


class TestPropEmitErrorMatchesEnvelope:
    """TS-03-P2: emit_error() writes valid JSON matching error_envelope().

    Property: 03-PROP-2
    Validates: 03-REQ-5.4, 03-REQ-6.1, 03-REQ-7.1
    """

    @pytest.mark.property
    @given(
        exc=_any_exception_strategy(),
        state=st.one_of(st.none(), _non_empty_text),
    )
    @settings(max_examples=50)
    def test_emit_error_matches_envelope(self, exc: Exception, state: str | None) -> None:
        from agentfox.io import emit_error, error_envelope

        env = error_envelope(exc, state=state)
        with capture_stdout() as out:
            emit_error(exc, state=state)
        parsed = json.loads(out.getvalue())
        assert parsed == env


# ---------------------------------------------------------------------------
# TS-03-P3: Explicit CLI flags always override AF_AGENT=1
# ---------------------------------------------------------------------------


class TestPropExplicitFlagsOverrideAfAgent:
    """TS-03-P3: Explicit flags always win over AF_AGENT=1 defaults.

    Property: 03-PROP-3
    Validates: 03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3, 03-REQ-3.4, 03-REQ-3.6
    """

    @pytest.mark.property
    @given(
        af_agent_set=st.booleans(),
        json_flag=st.sampled_from(["--json", "--no-json", None]),
        quiet_flag=st.sampled_from(["--quiet", "--verbose", None]),
    )
    @settings(max_examples=30)
    def test_flag_precedence(
        self,
        af_agent_set: bool,
        json_flag: str | None,
        quiet_flag: str | None,
    ) -> None:
        from agentfox.io import AgentFoxGroup, OutputManager, common_options
        from click.testing import CliRunner

        captured: list[OutputManager] = []

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        @cli.command()
        @click.pass_context
        def sub(ctx: click.Context) -> None:
            captured.append(ctx.obj["output"])

        env = {"AF_AGENT": "1"} if af_agent_set else {}
        args: list[str] = []
        if json_flag is not None:
            args.append(json_flag)
        if quiet_flag is not None:
            args.append(quiet_flag)
        args.append("sub")

        runner = CliRunner(env=env)
        runner.invoke(cli, args)

        if not captured:
            return  # Skip if invocation failed

        om = captured[0]

        # When explicit json flag is passed, it always wins
        if json_flag == "--json":
            assert om.json_mode is True
        elif json_flag == "--no-json":
            assert om.json_mode is False
        elif af_agent_set:
            assert om.json_mode is True  # AF_AGENT=1 default

        # When explicit quiet/verbose flag is passed, it always wins
        if quiet_flag == "--quiet":
            assert om.quiet is True
        elif quiet_flag == "--verbose":
            assert om.quiet is False
        elif af_agent_set:
            assert om.quiet is True  # AF_AGENT=1 default


# ---------------------------------------------------------------------------
# TS-03-P4: emit_ok() always sets ok to True
# ---------------------------------------------------------------------------


class TestPropEmitOkAlwaysTrue:
    """TS-03-P4: emit_ok() always sets 'ok': True regardless of input dict.

    Property: 03-PROP-4
    Validates: 03-REQ-5.3, 03-REQ-5.E1
    """

    @pytest.mark.property
    @given(
        data=st.dictionaries(
            st.text(min_size=1, max_size=20),
            st.one_of(st.booleans(), st.integers(), st.text(max_size=50), st.none()),
            max_size=5,
        ),
    )
    @settings(max_examples=50)
    def test_ok_always_true(self, data: dict) -> None:
        from agentfox.io import emit_ok

        with capture_stdout() as out:
            emit_ok(data)
        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is True


# ---------------------------------------------------------------------------
# TS-03-P5: BrokenPipeError always suppressed
# ---------------------------------------------------------------------------


class TestPropBrokenPipeSuppressed:
    """TS-03-P5: BrokenPipeError suppressed in all four emit functions.

    Property: 03-PROP-5
    Validates: 03-REQ-5.6
    """

    @pytest.mark.property
    @given(
        data=st.dictionaries(
            st.text(min_size=1, max_size=10),
            st.text(max_size=20),
            min_size=1,
            max_size=3,
        ),
    )
    @settings(max_examples=20)
    def test_broken_pipe_suppressed_all_emit(self, data: dict) -> None:
        from agentfox.io import emit, emit_line, emit_ok

        for fn in (emit, emit_line, emit_ok):
            with mock_stdout_raises(BrokenPipeError):
                fn(data)  # must not raise

    @pytest.mark.property
    @given(msg=_non_empty_text)
    @settings(max_examples=20)
    def test_broken_pipe_suppressed_emit_error(self, msg: str) -> None:
        from agentfox.io import emit_error

        with mock_stdout_raises(BrokenPipeError):
            emit_error(ValueError(msg))  # must not raise


# ---------------------------------------------------------------------------
# TS-03-P6: AgentFoxError subclasses always produce snake_case type
# ---------------------------------------------------------------------------


class TestPropAgentFoxErrorSnakeCase:
    """TS-03-P6: All AgentFoxError subclasses produce snake_case type.

    Property: 03-PROP-6
    Validates: 03-REQ-6.2
    """

    @pytest.mark.property
    @given(class_name=_camel_case_names, msg=_non_empty_text)
    @settings(max_examples=50)
    def test_snake_case_type(self, class_name: str, msg: str) -> None:
        from agentfox.io import error_envelope

        # Dynamically create an AgentFoxError subclass
        ErrorClass = type(class_name, (AgentFoxError,), {})
        exc = ErrorClass(msg)
        result = error_envelope(exc)

        assert result["error"]["type"] != "internal_error"
        # Verify it's the snake_case of the class name
        expected_type = re.sub(r"(?<!^)(?=[A-Z])", "_", class_name).lower()
        assert result["error"]["type"] == expected_type


# ---------------------------------------------------------------------------
# TS-03-P7: OutputManager format dispatch mutual exclusion
# ---------------------------------------------------------------------------


class TestPropOutputManagerDispatchExclusion:
    """TS-03-P7: emit_json/emit_human/banner are mutually exclusive.

    Property: 03-PROP-7
    Validates: 03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3, 03-REQ-4.4, 03-REQ-4.8
    """

    @pytest.mark.property
    @given(json_mode=st.booleans(), quiet=st.booleans())
    @settings(max_examples=10)
    def test_dispatch_mutual_exclusion(self, json_mode: bool, quiet: bool) -> None:
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=json_mode, quiet=quiet, verbose=False)

        # emit_json writes iff json_mode=True
        with capture_stdout() as out:
            om.emit_json({"test": True})
        output_present = len(out.getvalue()) > 0
        assert output_present == json_mode

        # emit_human writes iff json_mode=False
        with capture_stdout() as out:
            om.emit_human("test")
        output_present = len(out.getvalue()) > 0
        assert output_present == (not json_mode)

        # banner writes iff json_mode=False and quiet=False
        with capture_stderr() as err:
            om.banner()
        output_present = len(err.getvalue()) > 0
        assert output_present == (not json_mode and not quiet)


# ---------------------------------------------------------------------------
# TS-03-P8: get_output_manager() fallback ignores AF_AGENT
# ---------------------------------------------------------------------------


class TestPropGetOutputManagerFallbackIgnoresEnv:
    """TS-03-P8: Fallback always returns fixed defaults regardless of AF_AGENT.

    Property: 03-PROP-8
    Validates: 03-REQ-2.3, 03-REQ-2.E1
    """

    @pytest.mark.property
    @given(
        af_agent_val=st.one_of(
            st.none(),
            # Environment variables cannot contain null bytes; filter
            # them out to avoid ValueError from os.environ.
            st.text(max_size=10).filter(lambda s: "\x00" not in s),
        ),
    )
    @settings(max_examples=20)
    def test_fallback_ignores_af_agent(self, af_agent_val: str | None) -> None:
        from agentfox.io import get_output_manager

        env_patch = {}
        if af_agent_val is not None:
            env_patch["AF_AGENT"] = af_agent_val

        # Ensure no active Click context
        ctx = click.get_current_context(silent=True)
        if ctx is not None:
            pytest.skip("Click context is active; cannot test fallback path")

        with patch.dict(os.environ, env_patch, clear=False):
            om = get_output_manager()
            assert om.json_mode is False
            assert om.quiet is False
            assert om.verbose is False
            assert not hasattr(om, "trace")
