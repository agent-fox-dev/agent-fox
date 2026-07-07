"""Tests for GoogleADKBackend adapter.

Test Spec: TS-04-1 through TS-04-14, TS-04-E1 through TS-04-E5
Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3,
              04-REQ-2.1, 04-REQ-2.2, 04-REQ-2.3, 04-REQ-2.4,
              04-REQ-1.E1,
              04-REQ-3.1, 04-REQ-3.2, 04-REQ-3.3, 04-REQ-3.4, 04-REQ-3.5,
              04-REQ-3.E1, 04-REQ-3.E2,
              04-REQ-4.1, 04-REQ-4.2, 04-REQ-4.E1

All tests are guarded with pytest.importorskip('google.adk') so the suite
is skipped cleanly when the google-adk optional dependency is not installed.
"""

from __future__ import annotations

import inspect
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

# Skip the entire module when google-adk is not installed (04-REQ-14.1).
pytest.importorskip("google.adk")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


async def _collect_async(ait):
    """Drain an async iterator into a list."""
    messages = []
    async for msg in ait:
        messages.append(msg)
    return messages


def _mock_session(session_id: str = "sess-1", user_id: str = "user-1"):
    """Return a mock ADK session object."""
    session = SimpleNamespace(id=session_id, user_id=user_id)
    return session


async def _mock_terminal_event_stream(**_kwargs):
    """Async generator yielding a single terminal event with token usage."""
    # The implementation will define how terminal events are represented.
    # This mock produces a simple namespace that the backend should
    # recognise as a terminal/final event and map to a ResultMessage.
    yield SimpleNamespace(
        type="terminal",
        usage_metadata=SimpleNamespace(
            prompt_token_count=100,
            candidates_token_count=50,
        ),
    )


def _make_mock_runner(run_async_side_effect=None):
    """Create a mock Runner whose run_async returns the given async gen."""
    runner = MagicMock()
    if run_async_side_effect is not None:
        runner.run_async = MagicMock(return_value=run_async_side_effect)
    else:
        runner.run_async = MagicMock(
            return_value=_mock_terminal_event_stream(),
        )
    return runner


# ---------------------------------------------------------------------------
# Mock event constructors for event-mapping and max_turns tests
# ---------------------------------------------------------------------------


def _make_function_call_event(
    tool_name: str = "read_file",
    args: dict[str, Any] | None = None,
):
    """Return a mock ADK FunctionCall event."""
    return SimpleNamespace(
        type="function_call",
        tool_name=tool_name,
        args=args or {},
    )


def _make_function_response_event(
    tool_name: str = "read_file",
    result: dict[str, Any] | None = None,
):
    """Return a mock ADK FunctionResponse event."""
    return SimpleNamespace(
        type="function_response",
        tool_name=tool_name,
        result=result or {},
    )


def _make_text_event(text: str = "Hello"):
    """Return a mock ADK text content event."""
    return SimpleNamespace(
        type="text",
        content=text,
    )


def _make_terminal_event(
    input_tokens: int = 100,
    output_tokens: int = 50,
):
    """Return a mock ADK terminal event with token usage."""
    return SimpleNamespace(
        type="terminal",
        usage_metadata=SimpleNamespace(
            prompt_token_count=input_tokens,
            candidates_token_count=output_tokens,
        ),
    )


def _make_unknown_event():
    """Return a mock ADK event of an unrecognised type."""
    return SimpleNamespace(
        type="some_unrecognised_internal_event",
    )


async def _make_event_stream(*events):
    """Create an async generator yielding the given events in order."""
    for event in events:
        yield event


def _patch_adk(run_async_gen=None):
    """Context manager that patches InMemorySessionService, Agent, Runner.

    If *run_async_gen* is provided it is used as the run_async return
    value.  Otherwise a default terminal-event stream is used.
    """
    from contextlib import contextmanager

    @contextmanager
    def _ctx():
        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            if run_async_gen is not None:
                mock_runner_cls.return_value = _make_mock_runner(run_async_gen)
            else:
                mock_runner_cls.return_value = _make_mock_runner()

            yield mock_runner_cls

    return _ctx()


# ---------------------------------------------------------------------------
# TS-04-1: GoogleADKBackend instance satisfies the Backend Protocol
# Requirement: 04-REQ-1.1
# ---------------------------------------------------------------------------


class TestGoogleADKBackendProtocolConformance:
    """Verify GoogleADKBackend conforms to the Backend Protocol."""

    def test_isinstance_check(self) -> None:
        """TS-04-1: isinstance(GoogleADKBackend(), Backend) is True."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        backend = GoogleADKBackend()
        assert backend is not None
        # When the Backend Protocol is defined (spec 02), verify:
        # from agentfox.session.backends.protocol import Backend
        # assert isinstance(backend, Backend)
        # For now, verify the execute method exists with correct signature.
        assert hasattr(backend, "execute")
        assert callable(backend.execute)


# ---------------------------------------------------------------------------
# TS-04-2: execute() is an async generator returning AsyncIterator[AgentMessage]
# Requirement: 04-REQ-1.2
# ---------------------------------------------------------------------------


class TestExecuteIsAsyncGenerator:
    """Verify execute() is an async generator function."""

    async def test_execute_returns_async_generator(self) -> None:
        """TS-04-2: execute() returns an async iterator; last msg is ResultMessage."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        backend = GoogleADKBackend()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc
            mock_runner_cls.return_value = _make_mock_runner()

            result = backend.execute(
                prompt="hello",
                system_prompt="sys",
                model="gemini-2.0-flash",
                cwd="/workspace",
            )
            assert inspect.isasyncgen(result)

            messages = await _collect_async(result)
            assert len(messages) >= 1
            assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-3: execute() accepts all Backend Protocol parameters without TypeError
# Requirement: 04-REQ-1.3
# ---------------------------------------------------------------------------


class TestExecuteAcceptsAllParams:
    """Verify execute() accepts all Backend Protocol parameters."""

    async def test_all_params_accepted(self) -> None:
        """TS-04-3: call execute() with every parameter; no TypeError raised."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        backend = GoogleADKBackend()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc
            mock_runner_cls.return_value = _make_mock_runner()

            try:
                result = backend.execute(
                    "test",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    permission_callback=None,
                    activity_callback=None,
                    tool_error_callback=None,
                    node_id="n1",
                    archetype="coder",
                    max_turns=5,
                    max_budget_usd=1.0,
                    thinking={"enabled": True},
                    effort="high",
                    compaction=True,
                )
                await _collect_async(result)
            except TypeError as exc:
                pytest.fail(f"TypeError raised: {exc}")


# ---------------------------------------------------------------------------
# TS-04-4: Session lifecycle — fresh InMemorySessionService, Agent, Runner
# Requirement: 04-REQ-2.1
# ---------------------------------------------------------------------------


class TestSessionLifecycle:
    """Verify execute() creates ADK session components correctly."""

    async def test_session_creation_and_run_async(self) -> None:
        """TS-04-4: InMemorySessionService, create_session, Agent, Runner, run_async wired."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        mock_session = _mock_session()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ) as mock_agent_cls,
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=mock_session)
            mock_svc_cls.return_value = mock_svc

            mock_runner_cls.return_value = _make_mock_runner()

            backend = GoogleADKBackend()
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

            # InMemorySessionService was instantiated
            mock_svc_cls.assert_called_once()

            # create_session was called with app_name='agent-fox' and a UUID user_id
            mock_svc.create_session.assert_called_once()
            call_kwargs = mock_svc.create_session.call_args
            # Allow positional or keyword arguments
            if call_kwargs.kwargs:
                assert call_kwargs.kwargs.get("app_name") == "agent-fox"
                user_id = call_kwargs.kwargs.get("user_id", "")
            else:
                assert call_kwargs.args[0] == "agent-fox" if call_kwargs.args else True
                user_id = call_kwargs.args[1] if len(call_kwargs.args) > 1 else ""
            # user_id should be a UUID-format string (non-empty)
            assert isinstance(user_id, str)
            assert len(user_id) > 0

            # Agent was constructed with model, name='coder', instruction=system_prompt
            mock_agent_cls.assert_called_once()
            agent_kwargs = mock_agent_cls.call_args.kwargs
            assert agent_kwargs.get("model") == "gemini-2.0-flash"
            assert agent_kwargs.get("name") == "coder"
            assert agent_kwargs.get("instruction") == "sys"

            # Runner was constructed with agent and session_service
            mock_runner_cls.assert_called_once()

            # run_async was called with session.id and session.user_id
            mock_runner_cls.return_value.run_async.assert_called_once()
            ra_kwargs = mock_runner_cls.return_value.run_async.call_args.kwargs
            assert ra_kwargs.get("session_id") == "sess-1"
            assert ra_kwargs.get("user_id") == "user-1"


# ---------------------------------------------------------------------------
# TS-04-5: cwd string is converted to pathlib.Path for adk_tools
# Requirement: 04-REQ-2.2
# ---------------------------------------------------------------------------


class TestCwdConversion:
    """Verify cwd string is converted to pathlib.Path."""

    async def test_cwd_converted_to_path(self) -> None:
        """TS-04-5: cwd passed to adk_tools constructors is a pathlib.Path."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        captured_cwds: list[Any] = []

        def capturing_make_tools(cwd):
            captured_cwds.append(cwd)
            return []

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch(
                "agentfox.session.backends.google_adk.make_tools",
                side_effect=capturing_make_tools,
            ),
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc
            mock_runner_cls.return_value = _make_mock_runner()

            backend = GoogleADKBackend()
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assert len(captured_cwds) >= 1
        assert isinstance(captured_cwds[0], Path)
        assert captured_cwds[0] == Path("/workspace")


# ---------------------------------------------------------------------------
# TS-04-6: model string passed unchanged to Agent(model=...)
# Requirement: 04-REQ-2.3
# ---------------------------------------------------------------------------


class TestModelPassthrough:
    """Verify model string is passed unchanged to the ADK Agent."""

    async def test_model_string_unchanged(self) -> None:
        """TS-04-6: Agent is instantiated with the exact model string supplied."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ) as mock_agent_cls,
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc
            mock_runner_cls.return_value = _make_mock_runner()

            backend = GoogleADKBackend()
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="litellm/openai/gpt-5.5",
                    cwd="/workspace",
                ),
            )

            agent_kwargs = mock_agent_cls.call_args.kwargs
            assert agent_kwargs["model"] == "litellm/openai/gpt-5.5"


# ---------------------------------------------------------------------------
# TS-04-7: system_prompt mapped directly to Agent instruction parameter
# Requirement: 04-REQ-2.4
# ---------------------------------------------------------------------------


class TestSystemPromptMapping:
    """Verify system_prompt maps to Agent(instruction=...)."""

    async def test_system_prompt_to_instruction(self) -> None:
        """TS-04-7: Agent is instantiated with instruction=system_prompt."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ) as mock_agent_cls,
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc
            mock_runner_cls.return_value = _make_mock_runner()

            backend = GoogleADKBackend()
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="You are a helpful coding assistant.",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

            agent_kwargs = mock_agent_cls.call_args.kwargs
            assert agent_kwargs["instruction"] == "You are a helpful coding assistant."


# ---------------------------------------------------------------------------
# TS-04-E1: Unhandled exception inside execute() yields ResultMessage(is_error=True)
# Requirement: 04-REQ-1.E1
# ---------------------------------------------------------------------------


class TestNoExceptionPropagation:
    """Verify execute() never propagates exceptions to the caller."""

    async def test_runtime_error_caught(self) -> None:
        """TS-04-E1: RuntimeError in run_async yields ResultMessage(is_error=True)."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        async def raising_run_async(**_kwargs):
            raise RuntimeError("unexpected failure")
            yield  # noqa: RUF028 — makes this an async generator

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(return_value=raising_run_async())
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages: list[Any] = []

            # No exception should escape execute()
            try:
                async for msg in backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ):
                    messages.append(msg)
            except Exception as exc:
                pytest.fail(f"Exception escaped execute(): {exc}")

            assert len(messages) >= 1
            last = messages[-1]
            assert isinstance(last, ResultMessage)
            assert last.is_error is True

    async def test_value_error_caught(self) -> None:
        """TS-04-E1 variant: ValueError also yields ResultMessage(is_error=True)."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        async def raising_run_async(**_kwargs):
            raise ValueError("bad input")
            yield  # noqa: RUF028 — makes this an async generator

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch(
                "agentfox.session.backends.google_adk.Agent",
            ),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(return_value=raising_run_async())
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages: list[Any] = []

            try:
                async for msg in backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ):
                    messages.append(msg)
            except Exception as exc:
                pytest.fail(f"Exception escaped execute(): {exc}")

            assert len(messages) >= 1
            last = messages[-1]
            assert isinstance(last, ResultMessage)
            assert last.is_error is True
            assert last.is_transport_error is False


# ===========================================================================
# Group 2: ADK event mapping and max_turns counter tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-8: FunctionCall event yields ToolUseMessage with tool name and input
# Requirement: 04-REQ-3.1
# ---------------------------------------------------------------------------


class TestFunctionCallYieldsToolUseMessage:
    """Verify FunctionCall events are mapped to ToolUseMessage."""

    async def test_function_call_yields_tool_use_message(self) -> None:
        """TS-04-8: FunctionCall yields ToolUseMessage with correct fields."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ToolUseMessage

        stream = _make_event_stream(
            _make_function_call_event("read_file", {"path": "main.py"}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) >= 1
        assert tool_use_msgs[0].tool_name == "read_file"
        assert tool_use_msgs[0].tool_input == {"path": "main.py"}


# ---------------------------------------------------------------------------
# TS-04-9: FunctionResponse events consumed silently, not yielded
# Requirement: 04-REQ-3.2
# ---------------------------------------------------------------------------


class TestFunctionResponseConsumedSilently:
    """Verify FunctionResponse events do not produce messages."""

    async def test_function_response_not_yielded(self) -> None:
        """TS-04-9: No message corresponding to FunctionResponse appears."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import (
            AssistantMessage,
            ResultMessage,
            ToolUseMessage,
        )

        stream = _make_event_stream(
            _make_function_call_event("read_file", {}),
            _make_function_response_event("read_file", {"content": "hello"}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        # Every yielded message must be one of the canonical types
        for msg in messages:
            assert isinstance(msg, (ToolUseMessage, AssistantMessage, ResultMessage))

        # Specifically, no "FunctionResponseMessage" or similar appears
        msg_type_names = [type(m).__name__ for m in messages]
        assert "FunctionResponseMessage" not in msg_type_names


# ---------------------------------------------------------------------------
# TS-04-10: Text content event yields AssistantMessage
# Requirement: 04-REQ-3.3
# ---------------------------------------------------------------------------


class TestTextEventYieldsAssistantMessage:
    """Verify text content events are mapped to AssistantMessage."""

    async def test_text_event_yields_assistant_message(self) -> None:
        """TS-04-10: Text event yields AssistantMessage with correct content."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import AssistantMessage

        stream = _make_event_stream(
            _make_text_event("Here is your result."),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assistant_msgs = [m for m in messages if isinstance(m, AssistantMessage)]
        assert len(assistant_msgs) >= 1
        assert assistant_msgs[0].content == "Here is your result."


# ---------------------------------------------------------------------------
# TS-04-11: Terminal event yields ResultMessage with token usage
# Requirement: 04-REQ-3.4
# ---------------------------------------------------------------------------


class TestTerminalEventYieldsResultMessage:
    """Verify terminal event maps to ResultMessage with token counts."""

    async def test_terminal_event_result_message(self) -> None:
        """TS-04-11: Terminal event yields ResultMessage(is_error=False) with usage."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        stream = _make_event_stream(
            _make_terminal_event(input_tokens=100, output_tokens=50),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False
        assert result.input_tokens == 100
        assert result.output_tokens == 50


# ---------------------------------------------------------------------------
# TS-04-12: Unrecognised or no-op events silently skipped
# Requirement: 04-REQ-3.5
# ---------------------------------------------------------------------------


class TestUnknownEventsSkipped:
    """Verify unrecognised events are silently skipped."""

    async def test_unknown_event_skipped(self) -> None:
        """TS-04-12: Only ResultMessage yielded; unknown event produces nothing."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        stream = _make_event_stream(
            _make_unknown_event(),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        # Only the terminal event should produce a message (ResultMessage)
        assert len(messages) == 1
        assert isinstance(messages[0], ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-E3: Unrecognised tool name still yields ToolUseMessage
# Requirement: 04-REQ-3.E1
# ---------------------------------------------------------------------------


class TestUnrecognisedToolNameYieldsToolUseMessage:
    """Verify unrecognised tool names still produce ToolUseMessage."""

    async def test_unrecognised_tool_name(self) -> None:
        """TS-04-E3: FunctionCall for unknown tool yields ToolUseMessage."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ToolUseMessage

        stream = _make_event_stream(
            _make_function_call_event("totally_unknown_tool", {"x": 1}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) == 1
        assert tool_use_msgs[0].tool_name == "totally_unknown_tool"


# ---------------------------------------------------------------------------
# TS-04-E4: Exception during stream iteration yields ResultMessage(is_error=True)
# Requirement: 04-REQ-3.E2
# ---------------------------------------------------------------------------


class TestStreamExceptionYieldsErrorResult:
    """Verify exceptions during event iteration are caught gracefully."""

    async def test_connection_error_mid_stream(self) -> None:
        """TS-04-E4: ConnectionError after TextEvent yields ResultMessage(is_error=True)."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        async def flaky_stream(**_kwargs):
            yield _make_text_event("partial")
            raise ConnectionError("dropped")

        with (
            _patch_adk(run_async_gen=flaky_stream()) as _mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock),
        ):
            backend = GoogleADKBackend()
            messages: list[Any] = []

            try:
                async for msg in backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ):
                    messages.append(msg)
            except Exception as exc:
                pytest.fail(f"Exception escaped execute(): {exc}")

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True


# ---------------------------------------------------------------------------
# TS-04-13: max_turns stops the event loop after N round-trips
# Requirement: 04-REQ-4.1
# ---------------------------------------------------------------------------


class TestMaxTurnsStopsLoop:
    """Verify max_turns caps the number of tool-call round-trips."""

    async def test_max_turns_limits_tool_calls(self) -> None:
        """TS-04-13: With max_turns=2, only 2 ToolUseMessages yielded."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        # Create a stream with 5 FunctionCall events — more than max_turns
        events = [_make_function_call_event("read_file", {}) for _ in range(5)]
        stream = _make_event_stream(*events)

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    max_turns=2,
                ),
            )

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) == 2

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False


# ---------------------------------------------------------------------------
# TS-04-14: max_turns=None runs until ADK signals completion
# Requirement: 04-REQ-4.2
# ---------------------------------------------------------------------------


class TestMaxTurnsNoneRunsToCompletion:
    """Verify max_turns=None does not impose a turn limit."""

    async def test_max_turns_none(self) -> None:
        """TS-04-14: With max_turns=None, all 3 FunctionCalls are processed."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        events = [_make_function_call_event("read_file", {}) for _ in range(3)]
        events.append(_make_terminal_event())
        stream = _make_event_stream(*events)

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    max_turns=None,
                ),
            )

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) == 3

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False


# ---------------------------------------------------------------------------
# TS-04-E2: max_turns prevents unbounded iteration (infinite stream)
# Requirement: 04-REQ-2.E1 / 04-REQ-4.E1
# ---------------------------------------------------------------------------


class TestMaxTurnsPreventsInfiniteLoop:
    """Verify max_turns bounds an infinite event stream."""

    async def test_infinite_stream_bounded_by_max_turns(self) -> None:
        """TS-04-E2: With max_turns=3, infinite stream stops after 3 round-trips."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        async def infinite_stream(**_kwargs):
            while True:
                yield _make_function_call_event("read_file", {})

        with _patch_adk(run_async_gen=infinite_stream()):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    max_turns=3,
                ),
            )

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) == 3

        assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-E5: max_turns=1 exits cleanly without exception
# Requirement: 04-REQ-4.E1
# ---------------------------------------------------------------------------


class TestMaxTurnsOneExitsCleanly:
    """Verify max_turns=1 exits cleanly with ResultMessage(is_error=False)."""

    async def test_max_turns_one_no_exception(self) -> None:
        """TS-04-E5: max_turns=1 yields 1 ToolUseMessage, ResultMessage(is_error=False)."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        async def infinite_stream(**_kwargs):
            while True:
                yield _make_function_call_event("read_file", {})

        try:
            with _patch_adk(run_async_gen=infinite_stream()):
                backend = GoogleADKBackend()
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        max_turns=1,
                    ),
                )
        except Exception as exc:
            pytest.fail(f"Exception escaped execute(): {exc}")

        tool_use_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_use_msgs) == 1

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False
