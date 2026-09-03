"""Tests for GoogleADKBackend adapter.

Test Spec: TS-04-1 through TS-04-14, TS-04-E1 through TS-04-E6,
           TS-04-15 through TS-04-17, TS-04-26 through TS-04-37,
           TS-04-E11 through TS-04-E15
Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3,
              04-REQ-2.1, 04-REQ-2.2, 04-REQ-2.3, 04-REQ-2.4,
              04-REQ-1.E1,
              04-REQ-3.1, 04-REQ-3.2, 04-REQ-3.3, 04-REQ-3.4, 04-REQ-3.5,
              04-REQ-3.E1, 04-REQ-3.E2,
              04-REQ-4.1, 04-REQ-4.2, 04-REQ-4.E1,
              04-REQ-5.1, 04-REQ-5.2, 04-REQ-5.3, 04-REQ-5.E1,
              04-REQ-7.1, 04-REQ-7.2, 04-REQ-7.E1,
              04-REQ-8.1, 04-REQ-8.2, 04-REQ-8.3, 04-REQ-8.E1, 04-REQ-8.E2,
              04-REQ-9.1, 04-REQ-9.2,
              04-REQ-10.1, 04-REQ-10.2, 04-REQ-10.E1,
              04-REQ-11.1, 04-REQ-11.E1,
              04-REQ-12.1,
              04-REQ-13.1

google-adk is a mandatory dependency of the agentfox package.
"""

from __future__ import annotations

import glob
import inspect
import logging
import os
import sys
import tomllib
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

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
        from agentfox.session.backends.protocol import Backend

        backend = GoogleADKBackend()
        assert isinstance(backend, Backend), (
            f"GoogleADKBackend() does not satisfy Backend Protocol: got {type(backend).__name__}"
        )


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


# ===========================================================================
# Group 3: Permission/activity callbacks, retry logic, and ignored parameters
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-15: activity_callback invoked before tool call with tool name and args
# Requirement: 04-REQ-5.1
# ---------------------------------------------------------------------------


class TestActivityCallbackInvocation:
    """Verify activity_callback is invoked before each tool call."""

    async def test_activity_callback_called_with_tool_info(self) -> None:
        """TS-04-15: activity_callback called once with ('read_file', {'path': 'foo.py'})."""
        from agentfox.session.backends.google_adk import GoogleADKBackend

        activity_calls: list[tuple[str, dict[str, Any]]] = []

        def activity_callback(tool_name: str, args: dict[str, Any]) -> None:
            activity_calls.append((tool_name, args))

        stream = _make_event_stream(
            _make_function_call_event("read_file", {"path": "foo.py"}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    activity_callback=activity_callback,
                ),
            )

        assert len(activity_calls) == 1
        assert activity_calls[0] == ("read_file", {"path": "foo.py"})


# ---------------------------------------------------------------------------
# TS-04-16: permission_callback denial blocks tool execution
# Requirement: 04-REQ-5.2
# ---------------------------------------------------------------------------


class TestPermissionCallbackDenial:
    """Verify permission_callback denial blocks tool execution."""

    async def test_denied_tool_not_executed(self) -> None:
        """TS-04-16: Denied tool yields ToolUseMessage; tool not invoked."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        permission_calls: list[tuple[str, dict[str, Any]]] = []
        tool_was_called: list[bool] = []

        async def denying_callback(
            tool_name: str,
            args: dict[str, Any],
        ) -> bool:
            permission_calls.append((tool_name, args))
            return False  # denial

        stream = _make_event_stream(
            _make_function_call_event("execute", {"command": "rm -rf /"}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            # Patch the execute tool to track if it was called.
            # In the mocked ADK setup, tools are registered with the Agent
            # but never actually invoked by the mock Runner.  We patch
            # adk_tools.make_tools to return a tracking wrapper so we can
            # verify the invariant that the tool function is never called
            # when permission is denied.
            original_make_tools = None
            try:
                from agentfox.session.backends import adk_tools

                original_make_tools = adk_tools.make_tools
            except ImportError:
                pass

            def tracking_make_tools(cwd, **kwargs):
                tools = original_make_tools(cwd, **kwargs) if original_make_tools else []
                for t in tools:
                    if getattr(t, "__name__", "") == "execute":
                        original_fn = t

                        def tracked_execute(**kw):
                            tool_was_called.append(True)
                            return original_fn(**kw)

                        tracked_execute.__name__ = "execute"
                        tools[tools.index(t)] = tracked_execute
                        break
                return tools

            with patch(
                "agentfox.session.backends.google_adk.make_tools",
                side_effect=tracking_make_tools,
            ):
                backend = GoogleADKBackend()
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        permission_callback=denying_callback,
                    ),
                )

        # permission_callback was invoked with the correct tool name and args
        assert len(permission_calls) == 1, f"Expected 1 permission check, got {len(permission_calls)}"
        assert permission_calls[0] == ("execute", {"command": "rm -rf /"})

        # The tool function was never invoked (04-REQ-5.2 core invariant)
        assert len(tool_was_called) == 0, "Tool function was invoked despite permission denial"

        # A ToolUseMessage should still be yielded for the attempted call
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) >= 1
        assert tool_msgs[0].tool_name == "execute"

        # The session should complete with a ResultMessage
        assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-17: permission_callback grants tool call; tool executes normally
# Requirement: 04-REQ-5.3
# ---------------------------------------------------------------------------


class TestPermissionCallbackApproval:
    """Verify permission_callback approval allows tool execution."""

    async def test_approved_tool_executes(self) -> None:
        """TS-04-17: Approved tool call proceeds; session completes normally."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        permission_calls: list[tuple[str, dict[str, Any]]] = []

        async def approving_callback(
            tool_name: str,
            args: dict[str, Any],
        ) -> bool:
            permission_calls.append((tool_name, args))
            return True  # approval

        stream = _make_event_stream(
            _make_function_call_event("list_files", {"path": "."}),
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
                    permission_callback=approving_callback,
                ),
            )

        # permission_callback was invoked
        assert len(permission_calls) == 1
        assert permission_calls[0] == ("list_files", {"path": "."})

        # The session should complete successfully
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False


# ---------------------------------------------------------------------------
# TS-04-E6: activity_callback exception yields ResultMessage(is_error=True)
# Requirement: 04-REQ-5.E1
# ---------------------------------------------------------------------------


class TestCallbackExceptionHandling:
    """Verify callback exceptions yield ResultMessage(is_error=True)."""

    async def test_activity_callback_exception(self) -> None:
        """TS-04-E6: RuntimeError in activity_callback yields error result."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        def bad_activity_callback(
            tool_name: str,
            args: dict[str, Any],
        ) -> None:
            raise RuntimeError("callback error")

        stream = _make_event_stream(
            _make_function_call_event("read_file", {}),
            _make_terminal_event(),
        )

        try:
            with _patch_adk(run_async_gen=stream):
                backend = GoogleADKBackend()
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        activity_callback=bad_activity_callback,
                    ),
                )
        except Exception as exc:
            pytest.fail(f"Exception escaped execute(): {exc}")

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True
        assert messages[-1].is_transport_error is False

    async def test_permission_callback_exception(self) -> None:
        """TS-04-E6 variant: exception in permission_callback also handled."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        async def bad_permission_callback(
            tool_name: str,
            args: dict[str, Any],
        ) -> bool:
            raise RuntimeError("permission callback error")

        stream = _make_event_stream(
            _make_function_call_event("read_file", {}),
            _make_terminal_event(),
        )

        try:
            with _patch_adk(run_async_gen=stream):
                backend = GoogleADKBackend()
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        permission_callback=bad_permission_callback,
                    ),
                )
        except Exception as exc:
            pytest.fail(f"Exception escaped execute(): {exc}")

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True
        assert messages[-1].is_transport_error is False


# ---------------------------------------------------------------------------
# TS-04-28: Module-level constants _MAX_TRANSPORT_RETRIES and _BACKOFF_BASE
# Requirement: 04-REQ-8.1
# ---------------------------------------------------------------------------


class TestRetryConstants:
    """Verify retry constants are defined as module-level values."""

    def test_max_transport_retries_value(self) -> None:
        """TS-04-28: _MAX_TRANSPORT_RETRIES == 3."""
        from agentfox.session.backends import google_adk

        assert google_adk._MAX_TRANSPORT_RETRIES == 3

    def test_backoff_base_value(self) -> None:
        """TS-04-28: _BACKOFF_BASE == 1.0."""
        from agentfox.session.backends import google_adk

        assert google_adk._BACKOFF_BASE == 1.0


# ---------------------------------------------------------------------------
# TS-04-29: ResourceExhausted triggers retry with exponential backoff
# Requirement: 04-REQ-8.2
# ---------------------------------------------------------------------------


class TestTransientErrorRetry:
    """Verify transient errors trigger retry with exponential backoff."""

    async def test_resource_exhausted_retried_and_succeeds(self) -> None:
        """TS-04-29: ResourceExhausted retried; sleep(1.0); success on retry."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage
        from google.api_core.exceptions import ResourceExhausted

        call_count = [0]

        def run_async_effect(**_kwargs):
            call_count[0] += 1
            if call_count[0] == 1:

                async def fail():
                    raise ResourceExhausted("rate limit")
                    yield  # noqa: RUF028 — makes this an async generator

                return fail()

            async def succeed():
                yield _make_terminal_event()

            return succeed()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock) as mock_sleep,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=run_async_effect)
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assert call_count[0] == 2
        mock_sleep.assert_called_once_with(1.0)
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False

    async def test_service_unavailable_retried(self) -> None:
        """TS-04-29 variant: ServiceUnavailable also triggers retry."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage
        from google.api_core.exceptions import ServiceUnavailable

        call_count = [0]

        def run_async_effect(**_kwargs):
            call_count[0] += 1
            if call_count[0] == 1:

                async def fail():
                    raise ServiceUnavailable("503")
                    yield  # noqa: RUF028 — makes this an async generator

                return fail()

            async def succeed():
                yield _make_terminal_event()

            return succeed()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock) as mock_sleep,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=run_async_effect)
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assert call_count[0] == 2
        mock_sleep.assert_called_once_with(1.0)
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False


# ---------------------------------------------------------------------------
# TS-04-30: Non-transient exception yields immediate error without retry
# Requirement: 04-REQ-8.3
# ---------------------------------------------------------------------------


class TestNonTransientErrorNoRetry:
    """Verify non-transient exceptions are not retried."""

    async def test_value_error_no_retry(self) -> None:
        """TS-04-30: ValueError yields immediate ResultMessage(is_error=True)."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        call_count = [0]

        def run_async_effect(**_kwargs):
            call_count[0] += 1

            async def fail():
                raise ValueError("model not found")
                yield  # noqa: RUF028 — makes this an async generator

            return fail()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=run_async_effect)
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assert call_count[0] == 1
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True
        assert messages[-1].is_transport_error is False


# ---------------------------------------------------------------------------
# TS-04-E12: All _MAX_TRANSPORT_RETRIES exhausted yields error result
# Requirement: 04-REQ-8.E1
# ---------------------------------------------------------------------------


class TestRetriesExhausted:
    """Verify exhausted retries yield ResultMessage(is_error=True, is_transport_error=True)."""

    async def test_all_retries_exhausted(self) -> None:
        """TS-04-E12: After _MAX_TRANSPORT_RETRIES+1 calls, transport error result."""
        from agentfox.session.backends.google_adk import (
            _MAX_TRANSPORT_RETRIES,
            GoogleADKBackend,
        )
        from agentfox.session.backends.types import ResultMessage
        from google.api_core.exceptions import ResourceExhausted

        call_count = [0]

        def always_fail(**_kwargs):
            call_count[0] += 1

            async def fail():
                raise ResourceExhausted("quota")
                yield  # noqa: RUF028 — makes this an async generator

            return fail()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock),
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=always_fail)
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

        assert call_count[0] == _MAX_TRANSPORT_RETRIES + 1
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True
        assert messages[-1].is_transport_error is True


# ---------------------------------------------------------------------------
# TS-04-E13: Transient error after yielded messages; retry without duplicates
# Requirement: 04-REQ-8.E2
# ---------------------------------------------------------------------------


class TestRetryAfterPartialMessages:
    """Verify retry after partial messages yields single terminal ResultMessage."""

    async def test_partial_then_retry_succeeds(self) -> None:
        """TS-04-E13: TextEvent then ServiceUnavailable; retry yields single ResultMessage."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage
        from google.api_core.exceptions import ServiceUnavailable

        call_count = [0]

        def partial_then_succeed(**_kwargs):
            call_count[0] += 1
            if call_count[0] == 1:

                async def partial_fail():
                    yield _make_text_event("partial response")
                    raise ServiceUnavailable("503")

                return partial_fail()

            async def succeed():
                yield _make_terminal_event()

            return succeed()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock),
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=partial_then_succeed)
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        assert call_count[0] == 2
        result_msgs = [m for m in messages if isinstance(m, ResultMessage)]
        assert len(result_msgs) == 1
        assert result_msgs[0].is_error is False


# ---------------------------------------------------------------------------
# TS-04-31: max_budget_usd emits debug-level log; execution continues
# Requirement: 04-REQ-9.1
# ---------------------------------------------------------------------------


class TestMaxBudgetDebugLog:
    """Verify max_budget_usd emits a debug log and execution proceeds."""

    async def test_max_budget_usd_debug_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-04-31: Debug log emitted for max_budget_usd=5.0."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        stream = _make_event_stream(_make_terminal_event())

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            with caplog.at_level(
                logging.DEBUG,
                logger="agentfox.session.backends.google_adk",
            ):
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        max_budget_usd=5.0,
                    ),
                )

        debug_msgs = [r.getMessage() for r in caplog.records if r.levelno == logging.DEBUG]
        assert any("max_budget_usd=5.0 ignored" in m and "GoogleADKBackend" in m for m in debug_msgs), (
            f"Expected debug log about max_budget_usd; got: {debug_msgs}"
        )
        assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-32: Silently ignored parameters — no errors, no log noise
# Requirement: 04-REQ-9.2
# ---------------------------------------------------------------------------


class TestIgnoredParameters:
    """Verify thinking, effort, compaction, etc. are silently ignored."""

    async def test_ignored_params_no_errors_no_warnings(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-04-32: Ignored params produce no errors and no warning logs."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage

        stream = _make_event_stream(_make_terminal_event())

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            with caplog.at_level(
                logging.DEBUG,
                logger="agentfox.session.backends.google_adk",
            ):
                messages = await _collect_async(
                    backend.execute(
                        "task",
                        system_prompt="sys",
                        model="gemini-2.0-flash",
                        cwd="/workspace",
                        thinking={"enabled": True},
                        effort="high",
                        compaction=True,
                        tool_error_callback=lambda e: None,
                        node_id="x",
                        archetype="coder",
                    ),
                )

        # No warning or error log entries should appear
        warn_and_above = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warn_and_above) == 0, f"Unexpected warning/error logs: {[r.getMessage() for r in warn_and_above]}"
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False


# ===========================================================================
# Task Group 4: af SDK tool registration, factory, config, and containment
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-26: af SDK tools registered in Agent's tools list
# Requirement: 04-REQ-7.1
# ---------------------------------------------------------------------------


class TestAfSdkToolRegistration:
    """Verify google_adk.py imports and registers af SDK tools in Agent."""

    async def test_sdk_tools_registered_in_agent(self) -> None:
        """TS-04-26: Agent tools list includes all 5 af SDK tool wrappers."""
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
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

            # Verify Agent was called and extract the tools list
            mock_agent_cls.assert_called_once()
            call_kwargs = mock_agent_cls.call_args
            tools_passed = call_kwargs.kwargs.get(
                "tools",
                call_kwargs.args[3] if len(call_kwargs.args) > 3 else [],
            )
            tool_names = [getattr(t, "__name__", getattr(t, "name", str(t))) for t in tools_passed]

            # All five af SDK tools must be present
            assert "spec_read" in tool_names, f"spec_read not found in tools: {tool_names}"
            assert "context_search" in tool_names, f"context_search not found in tools: {tool_names}"
            assert "context_get" in tool_names, f"context_get not found in tools: {tool_names}"
            assert "memory_recall" in tool_names, f"memory_recall not found in tools: {tool_names}"
            assert "subtask_state" in tool_names, f"subtask_state not found in tools: {tool_names}"


# ---------------------------------------------------------------------------
# TS-04-27: af SDK tool import paths present in google_adk.py source
# Requirement: 04-REQ-7.2
# ---------------------------------------------------------------------------


class TestAfSdkToolImportPaths:
    """Verify google_adk.py imports af SDK tools without DI or registry."""

    def test_sdk_tool_names_in_source(self) -> None:
        """TS-04-27: All 5 af SDK function names appear in google_adk.py source."""
        google_adk_path = Path(__file__).resolve().parents[4] / ("agentfox" / Path("session/backends/google_adk.py"))
        source = google_adk_path.read_text(encoding="utf-8")

        assert "spec_read" in source, "spec_read not found in google_adk.py"
        assert "context_search" in source, "context_search not found in google_adk.py"
        assert "context_get" in source, "context_get not found in google_adk.py"
        assert "memory_recall" in source, "memory_recall not found in google_adk.py"
        assert "subtask_state" in source, "subtask_state not found in google_adk.py"

    def test_no_registry_or_di_pattern(self) -> None:
        """TS-04-27: No DI registry/lookup used for af SDK tool registration."""
        google_adk_path = Path(__file__).resolve().parents[4] / ("agentfox" / Path("session/backends/google_adk.py"))
        source = google_adk_path.read_text(encoding="utf-8")

        # The source should not use a "registry" DI pattern for SDK tools
        source_lower = source.lower()
        # Allow "registry" in comments or docstrings but not as a
        # function call pattern like registry.get() or registry.lookup()
        assert "registry.get(" not in source_lower, "Found registry.get() DI pattern in google_adk.py"
        assert "registry.lookup(" not in source_lower, "Found registry.lookup() DI pattern in google_adk.py"


# ---------------------------------------------------------------------------
# TS-04-E11: ImportError when af SDK tool source module is unavailable
# Requirement: 04-REQ-7.E1
# ---------------------------------------------------------------------------


class TestAfSdkToolImportFailure:
    """Verify ImportError propagates when af SDK modules are unavailable."""

    def test_import_error_on_missing_sdk_module(self) -> None:
        """TS-04-E11: ImportError raised if af SDK module is unavailable."""
        import importlib

        mod_name = "agentfox.session.backends.google_adk"
        dep_name = "agentfox.session.backends.adk_tools"
        cached = sys.modules.pop(mod_name, None)
        cached_dep = sys.modules.pop(dep_name, None)

        try:
            # Setting a sys.modules entry to None causes ImportError on
            # any attempt to import that module.  When google_adk is
            # re-imported it will fail on ``from ...adk_tools import ...``.
            sys.modules[dep_name] = None  # type: ignore[assignment]

            with pytest.raises(ImportError):
                importlib.import_module(mod_name)
        finally:
            if cached is not None:
                sys.modules[mod_name] = cached
            else:
                sys.modules.pop(mod_name, None)
            if cached_dep is not None:
                sys.modules[dep_name] = cached_dep
            else:
                sys.modules.pop(dep_name, None)


# ---------------------------------------------------------------------------
# TS-04-33: create_backend('google-adk') returns a GoogleADKBackend instance
# Requirement: 04-REQ-10.1
# ---------------------------------------------------------------------------


class TestCreateBackendGoogleAdk:
    """Verify create_backend('google-adk') returns GoogleADKBackend."""

    def test_create_backend_returns_google_adk(self) -> None:
        """TS-04-33: create_backend('google-adk') returns GoogleADKBackend."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.protocol import Backend

        result = create_backend("google-adk")
        assert isinstance(result, GoogleADKBackend), f"Expected GoogleADKBackend, got {type(result).__name__}"
        assert isinstance(result, Backend), "GoogleADKBackend instance does not satisfy Backend Protocol"


# ---------------------------------------------------------------------------
# TS-04-34: create_backend for existing keys ('claude') still works
# Requirement: 04-REQ-10.2
# ---------------------------------------------------------------------------


class TestCreateBackendExistingKeys:
    """Verify create_backend still works for pre-existing backend keys."""

    def test_create_backend_claude_still_works(self) -> None:
        """TS-04-34: create_backend('claude') returns ClaudeBackend."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.claude import ClaudeBackend
        from agentfox.session.backends.protocol import Backend

        result = create_backend("claude")
        assert isinstance(result, ClaudeBackend), f"Expected ClaudeBackend, got {type(result).__name__}"
        assert isinstance(result, Backend)

    def test_create_backend_deepagents_still_works(self) -> None:
        """TS-04-34: create_backend('deepagents') returns DeepAgentsBackend."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.protocol import Backend

        result = create_backend("deepagents")
        assert isinstance(result, DeepAgentsBackend), f"Expected DeepAgentsBackend, got {type(result).__name__}"
        assert isinstance(result, Backend)


# ---------------------------------------------------------------------------
# TS-04-35: OrchestratorConfig accepts 'google-adk' backend value
# Requirement: 04-REQ-11.1
# ---------------------------------------------------------------------------


class TestOrchestratorConfigGoogleAdk:
    """Verify BackendConfig.provider accepts 'google'."""

    def test_google_accepted(self) -> None:
        """TS-04-35: BackendConfig(provider='google') validates."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="google")
        assert config.provider == "google"

    def test_claude_still_accepted(self) -> None:
        """TS-04-35: BackendConfig(provider='claude') still validates."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="claude")
        assert config.provider == "claude"

    def test_deepagents_still_accepted(self) -> None:
        """TS-04-35: BackendConfig(provider='deepagents') still validates."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="deepagents")
        assert config.provider == "deepagents"


# ---------------------------------------------------------------------------
# TS-04-36: pyproject.toml declares google-adk>=2.8.0 dependency
# Requirement: 04-REQ-12.1
# ---------------------------------------------------------------------------


class TestPyprojectGoogleAdkDependency:
    """Verify pyproject.toml declares google-adk as a mandatory dependency."""

    def test_google_adk_in_dependencies(self) -> None:
        """TS-04-36: google-adk>=2.8.0 in [project.dependencies]."""
        pyproject_path = Path(__file__).resolve().parents[4] / "pyproject.toml"
        assert pyproject_path.exists(), f"pyproject.toml not found at {pyproject_path}"

        with open(pyproject_path, "rb") as f:
            data = tomllib.load(f)

        deps = data.get("project", {}).get("dependencies", [])
        assert any("google-adk>=2.8.0" in dep for dep in deps), (
            f"'google-adk>=2.8.0' not found in project.dependencies: {deps}"
        )


# ---------------------------------------------------------------------------
# TS-04-37: Containment test maps 'google.adk' -> 'google_adk.py'
# Requirement: 04-REQ-13.1
# ---------------------------------------------------------------------------


class TestContainmentGoogleAdk:
    """Verify google.adk containment mapping and enforcement."""

    def test_containment_mapping_in_test_protocol(self) -> None:
        """TS-04-37: SDK_CONTAINMENT has 'google.adk' -> 'google_adk.py'."""
        # The containment test is defined in test_protocol.py.
        # Read its source and verify the mapping entry is present
        # (not just as a comment, but as an active dict entry).
        test_protocol_path = Path(__file__).parent / "test_protocol.py"
        source = test_protocol_path.read_text(encoding="utf-8")

        # Verify the mapping entry is present and uncommented
        assert "'google.adk'" in source or '"google.adk"' in source, "google.adk mapping not found in test_protocol.py"
        assert "'google_adk.py'" in source or '"google_adk.py"' in source, (
            "google_adk.py mapping not found in test_protocol.py"
        )

    def test_google_adk_imports_only_in_google_adk_py(self) -> None:
        """TS-04-37: google.adk imports only appear in google_adk.py."""
        agentfox_dir = os.path.join(
            os.path.dirname(__file__),
            "..",
            "..",
            "..",
            "..",
            "agentfox",
        )
        agentfox_dir = os.path.normpath(agentfox_dir)
        assert os.path.isdir(agentfox_dir), f"Production source directory not found: {agentfox_dir}"

        all_files = glob.glob(
            os.path.join(agentfox_dir, "**", "*.py"),
            recursive=True,
        )

        violations = []
        for filepath in all_files:
            if os.path.basename(filepath) == "google_adk.py":
                continue
            with open(filepath, encoding="utf-8") as f:
                contents = f.read()
            # Check for direct google.adk imports
            if "import google.adk" in contents or "from google.adk" in contents:
                violations.append(os.path.relpath(filepath, agentfox_dir))

        assert violations == [], f"google.adk imports found outside google_adk.py: {violations}"


# ---------------------------------------------------------------------------
# TS-04-E15: OrchestratorConfig rejects unknown backend values
# Requirement: 04-REQ-11.E1
# ---------------------------------------------------------------------------


class TestOrchestratorConfigUnknownBackend:
    """Verify BackendConfig rejects unknown provider values."""

    def test_unknown_backend_raises_validation_error(self) -> None:
        """TS-04-E15: BackendConfig(provider='unknown') raises."""
        from agentfox.core.config import BackendConfig
        from pydantic import ValidationError

        with pytest.raises(ValidationError):
            BackendConfig(provider="unknown-backend")


# ===========================================================================
# Task Group 13: Smoke tests and end-to-end wiring verification
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-SMOKE-1: End-to-end coding session via GoogleADKBackend
# Execution Path: 04-PATH-1
# ---------------------------------------------------------------------------


class TestSmoke1EndToEndSession:
    """Smoke test tracing create_backend -> execute -> event stream."""

    async def test_full_coding_session(self) -> None:
        """TS-04-SMOKE-1: create_backend -> execute -> tool/text/result messages."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.protocol import Backend
        from agentfox.session.backends.types import (
            AssistantMessage,
            ResultMessage,
            ToolUseMessage,
        )

        # Step 1: create_backend('google-adk') returns a GoogleADKBackend
        backend = create_backend("google-adk")
        assert isinstance(backend, GoogleADKBackend)
        assert isinstance(backend, Backend)

        # Step 2: Set up mock ADK components — the ADK event stream includes
        # a FunctionCall, FunctionResponse (consumed silently), TextEvent,
        # and TerminalEvent with usage metadata.
        mock_session = _mock_session()
        stream = _make_event_stream(
            _make_function_call_event("write_file", {"path": "hello.py", "content": "print('hi')"}),
            _make_function_response_event("write_file", {"ok": True}),
            _make_text_event("Done"),
            _make_terminal_event(input_tokens=200, output_tokens=80),
        )

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
            mock_runner_cls.return_value = _make_mock_runner(stream)

            # Step 3: Execute the session
            messages = await _collect_async(
                backend.execute(
                    prompt="Write a hello world function",
                    system_prompt="You are a coder",
                    model="gemini-2.0-flash",
                    cwd="/tmp/workspace",
                ),
            )

        # Step 4: Verify the full wiring chain

        # InMemorySessionService.create_session called with correct args
        mock_svc.create_session.assert_called_once()
        cs_kwargs = mock_svc.create_session.call_args.kwargs
        assert cs_kwargs.get("app_name") == "agent-fox"
        assert isinstance(cs_kwargs.get("user_id"), str)
        assert len(cs_kwargs["user_id"]) > 0

        # Agent constructed with correct params
        mock_agent_cls.assert_called_once()
        agent_kwargs = mock_agent_cls.call_args.kwargs
        assert agent_kwargs["model"] == "gemini-2.0-flash"
        assert agent_kwargs["name"] == "coder"
        assert agent_kwargs["instruction"] == "You are a coder"

        # Runner.run_async called with session.id and session.user_id
        mock_runner_cls.return_value.run_async.assert_called_once()
        ra_kwargs = mock_runner_cls.return_value.run_async.call_args.kwargs
        assert ra_kwargs["session_id"] == mock_session.id
        assert ra_kwargs["user_id"] == mock_session.user_id

        # ToolUseMessage for 'write_file' was yielded
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) == 1
        assert tool_msgs[0].tool_name == "write_file"

        # AssistantMessage with 'Done' was yielded
        assistant_msgs = [m for m in messages if isinstance(m, AssistantMessage)]
        assert len(assistant_msgs) == 1
        assert assistant_msgs[0].content == "Done"

        # Final ResultMessage with correct token usage
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False
        assert result.input_tokens == 200
        assert result.output_tokens == 80


# ---------------------------------------------------------------------------
# TS-04-SMOKE-2: Transient error retry path
# Execution Path: 04-PATH-2
# ---------------------------------------------------------------------------


class TestSmoke2TransientRetry:
    """Smoke test tracing the retry path for transient errors."""

    async def test_retry_path_succeeds_on_second_attempt(self) -> None:
        """TS-04-SMOKE-2: ResourceExhausted on attempt 1, success on attempt 2."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage
        from google.api_core.exceptions import ResourceExhausted

        call_count = [0]

        def run_async_effect(**_kwargs):
            call_count[0] += 1
            if call_count[0] == 1:

                async def fail():
                    raise ResourceExhausted("rate limit")
                    yield  # noqa: RUF028

                return fail()

            async def succeed():
                yield _make_terminal_event(input_tokens=150, output_tokens=60)

            return succeed()

        with (
            patch(
                "agentfox.session.backends.google_adk.InMemorySessionService",
            ) as mock_svc_cls,
            patch("agentfox.session.backends.google_adk.Agent"),
            patch(
                "agentfox.session.backends.google_adk.Runner",
            ) as mock_runner_cls,
            patch("asyncio.sleep", new_callable=AsyncMock) as mock_sleep,
        ):
            mock_svc = AsyncMock()
            mock_svc.create_session = AsyncMock(return_value=_mock_session())
            mock_svc_cls.return_value = mock_svc

            runner = MagicMock()
            runner.run_async = MagicMock(side_effect=run_async_effect)
            mock_runner_cls.return_value = runner

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    prompt="Retry test",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                ),
            )

        # Verify wiring: run_async called exactly twice
        assert call_count[0] == 2

        # Exponential backoff: sleep(1.0) on first retry
        mock_sleep.assert_called_once_with(1.0)

        # Final message is successful ResultMessage
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False

        # No exception propagated to the caller
        # (implicit: we reached this point without try/except)


# ---------------------------------------------------------------------------
# TS-04-SMOKE-3: Tool permission denial path
# Execution Path: 04-PATH-3
# ---------------------------------------------------------------------------


class TestSmoke3PermissionDenial:
    """Smoke test tracing the permission denial path."""

    async def test_permission_denial_wiring(self) -> None:
        """TS-04-SMOKE-3: Denied tool yields ToolUseMessage, tool not executed."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        permission_calls: list[tuple[str, dict[str, Any]]] = []

        async def denying_callback(
            tool_name: str,
            args: dict[str, Any],
        ) -> bool:
            permission_calls.append((tool_name, args))
            return False  # denial

        stream = _make_event_stream(
            _make_function_call_event("execute", {"command": "rm -rf /"}),
            _make_terminal_event(),
        )

        with _patch_adk(run_async_gen=stream):
            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    prompt="Delete everything",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd="/workspace",
                    permission_callback=denying_callback,
                ),
            )

        # permission_callback was invoked with tool_name='execute'
        assert len(permission_calls) == 1
        assert permission_calls[0][0] == "execute"

        # ToolUseMessage was yielded for the attempted call
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) >= 1
        assert tool_msgs[0].tool_name == "execute"

        # Session completes with a ResultMessage
        result = messages[-1]
        assert isinstance(result, ResultMessage)


# ---------------------------------------------------------------------------
# TS-04-SMOKE-4: Path containment violation via coding tool
# Execution Path: 04-PATH-4
# ---------------------------------------------------------------------------


class TestSmoke4PathContainment:
    """Smoke test tracing path containment from backend through adk_tools."""

    async def test_path_containment_wiring(self, tmp_path: Path) -> None:
        """TS-04-SMOKE-4: Path traversal rejected by real adk_tools.read_file."""
        from agentfox.session.backends.adk_tools import make_tools
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        # Step 1: Verify real adk_tools enforce path containment
        tools = make_tools(tmp_path)
        read_file = next(t for t in tools if t.__name__ == "read_file")

        # Path traversal attempt
        result = read_file(path="../../etc/passwd")
        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }, f"Expected path_not_allowed, got: {result}"

        # Step 2: Verify GoogleADKBackend creates tools and passes them to Agent
        stream = _make_event_stream(
            _make_function_call_event("read_file", {"path": "../../etc/passwd"}),
            _make_terminal_event(),
        )

        captured_tools: list[list[Any]] = []

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
            mock_runner_cls.return_value = _make_mock_runner(stream)

            backend = GoogleADKBackend()
            messages = await _collect_async(
                backend.execute(
                    prompt="Read /etc/passwd",
                    system_prompt="sys",
                    model="gemini-2.0-flash",
                    cwd=str(tmp_path),
                ),
            )

            # Capture the tools registered with Agent
            agent_call_kwargs = mock_agent_cls.call_args.kwargs
            captured_tools.append(agent_call_kwargs.get("tools", []))

        # Tools were registered with Agent, including read_file
        registered_tools = captured_tools[0]
        tool_names = [getattr(t, "__name__", str(t)) for t in registered_tools]
        assert "read_file" in tool_names, f"read_file not in registered tools: {tool_names}"

        # A ToolUseMessage for 'read_file' was yielded
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) == 1
        assert tool_msgs[0].tool_name == "read_file"

        # Session completed with ResultMessage
        assert isinstance(messages[-1], ResultMessage)

        # Step 3: Confirm no actual file was read (path containment enforced)
        # This is verified by step 1 above — the real read_file tool
        # rejects the path traversal and returns the error dict.
        # In the mocked ADK setup, tool execution is managed by the
        # mock Runner, so the real tool was not called during execute().
        # The key verification is that make_tools(cwd) correctly
        # constructs tools with path containment bound to cwd.


# ---------------------------------------------------------------------------
# Cross-spec entry point verification (subtask 13.5)
# ---------------------------------------------------------------------------


class TestCrossSpecEntryPointVerification:
    """Verify integration points across spec boundaries."""

    def test_create_backend_production_wiring(self) -> None:
        """13.5: create_backend lazily imports GoogleADKBackend in production."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.protocol import Backend

        backend = create_backend("google-adk")
        assert isinstance(backend, GoogleADKBackend)
        assert isinstance(backend, Backend)

    def test_isinstance_backend_protocol(self) -> None:
        """13.5: GoogleADKBackend() satisfies Backend Protocol at runtime."""
        from agentfox.session.backends.google_adk import GoogleADKBackend
        from agentfox.session.backends.protocol import Backend

        backend = GoogleADKBackend()
        assert isinstance(backend, Backend)

    def test_backend_config_google(self) -> None:
        """13.5: BackendConfig(provider='google') validates."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="google")
        assert config.provider == "google"

    def test_all_three_backends_validate_and_create(self) -> None:
        """13.5: All three backend keys validate in config and create_backend."""
        from agentfox.core.config import BackendConfig
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.protocol import Backend

        for backend_name in ("claude", "deepagents", "google"):
            # Config validation
            config = BackendConfig(provider=backend_name)
            assert config.provider == backend_name

            # Factory instantiation
            backend = create_backend(backend_name)
            assert isinstance(backend, Backend), f"create_backend('{backend_name}') does not satisfy Backend Protocol"
