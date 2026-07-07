"""Tests for GoogleADKBackend adapter.

Test Spec: TS-04-1 through TS-04-7, TS-04-E1
Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3,
              04-REQ-2.1, 04-REQ-2.2, 04-REQ-2.3, 04-REQ-2.4,
              04-REQ-1.E1

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
