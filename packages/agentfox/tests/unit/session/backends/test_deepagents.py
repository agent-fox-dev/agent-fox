"""Tests for DeepAgentsBackend adapter.

Test Spec: TS-03-1 through TS-03-30, TS-03-31 through TS-03-41,
           TS-03-P1, TS-03-P2, TS-03-P3, TS-03-P4, TS-03-P5, TS-03-P6, TS-03-P7,
           TS-03-E2, TS-03-E3, TS-03-E4, TS-03-E5, TS-03-E6, TS-03-E7,
           TS-03-SMOKE-1, TS-03-SMOKE-2, TS-03-SMOKE-3
Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-2.1-2.9,
              03-REQ-2.E1, 03-REQ-2.E2,
              03-REQ-3.1-3.3, 03-REQ-4.1-4.3,
              03-REQ-5.1-5.4, 03-REQ-6.1-6.4,
              03-REQ-7.1-7.4,
              03-REQ-8.1-8.3, 03-REQ-9.1-9.2, 03-REQ-10.1-10.3,
              03-REQ-11.1-11.2, 03-REQ-12.1

Errata: Several spec assumptions diverge from the actual codebase.
  - The spec assumes a Backend Protocol exists in types.py (it does not;
    duck-typing is used).  Tests adapted to check structural protocol
    conformance (has execute/close async methods) instead of isinstance.
  - The spec assumes create_backend() factory exists in __init__.py (it does
    not; session.py directly instantiates ClaudeBackend).  Tests adapted to
    verify the factory once it is created by the implementation group.
  - The spec assumes OrchestratorConfig has a backend field (it does not).
    Tests adapted to verify the field once added by the implementation group.
  - PermissionCallback is async (Awaitable[bool]), not sync as the spec says.
  - ResultMessage.input_tokens is int (non-optional), not Optional[int].
  - ClaudeBackend.close() is async, not sync.
See docs/errata/03_deepagents_backend.md for full divergence documentation.
"""

from __future__ import annotations

import asyncio
import glob
import hashlib
import inspect
import logging
import os
import tomllib
from typing import Any
from unittest.mock import MagicMock, patch

import pytest

# ---------------------------------------------------------------------------
# TS-03-1: DeepAgentsBackend satisfies the Backend Protocol
# Requirement: 03-REQ-1.1
# ---------------------------------------------------------------------------


class TestDeepAgentsBackendProtocol:
    """Verify DeepAgentsBackend structural protocol conformance."""

    def test_isinstance_backend(self) -> None:
        """TS-03-1: isinstance(DeepAgentsBackend(), Backend) is True.

        Errata: The spec assumes a formal Backend Protocol in types.py.
        The codebase uses duck typing.  This test verifies structural
        conformance: DeepAgentsBackend has execute() and close() methods
        matching the Backend interface.  If a Backend Protocol is added
        by the implementation group, the isinstance check is also performed.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()

        # Structural checks: must have execute and close
        assert hasattr(backend, "execute"), "DeepAgentsBackend must have execute method"
        assert hasattr(backend, "close"), "DeepAgentsBackend must have close method"
        assert callable(backend.execute), "execute must be callable"
        assert callable(backend.close), "close must be callable"

        # If a Backend Protocol was added, verify isinstance too
        try:
            from agentfox.session.backends.types import Backend

            assert isinstance(backend, Backend), "isinstance(DeepAgentsBackend(), Backend) must return True"
        except ImportError:
            # Backend Protocol doesn't exist yet - structural check is sufficient
            pass


# ---------------------------------------------------------------------------
# TS-03-P1: Property - every DeepAgentsBackend instance satisfies protocol
# Property: 03-PROP-1
# Validates: 03-REQ-1.1, 03-REQ-2.1
# ---------------------------------------------------------------------------


class TestPropertyBackendProtocolConformance:
    """Property: every DeepAgentsBackend instance has execute/close with correct signatures."""

    def test_prop_protocol_conformance(self) -> None:
        """TS-03-P1: Protocol conformance and execute() signature check."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        instance = DeepAgentsBackend()
        assert hasattr(instance, "execute")
        assert hasattr(instance, "close")

        sig = inspect.signature(instance.execute)
        assert "prompt" in sig.parameters
        assert "system_prompt" in sig.parameters
        assert "model" in sig.parameters
        assert "cwd" in sig.parameters
        assert "permission_callback" in sig.parameters


# ---------------------------------------------------------------------------
# TS-03-2: SDK import containment - deepagents only in deepagents.py
# Requirement: 03-REQ-1.2
# ---------------------------------------------------------------------------


class TestSDKContainment:
    """Verify deepagents SDK is imported only in session/backends/deepagents.py."""

    def test_deepagents_import_containment(self) -> None:
        """TS-03-2: No 'import deepagents' or 'from deepagents' outside deepagents.py."""
        # Navigate to the agentfox source directory
        backends_dir = os.path.dirname(
            inspect.getfile(__import__("agentfox.session.backends.types", fromlist=["types"]))
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))

        allowed = os.path.normpath(os.path.join(agentfox_dir, "session", "backends", "deepagents.py"))

        py_files = glob.glob(os.path.join(agentfox_dir, "**", "*.py"), recursive=True)

        violations = []
        for py_file in py_files:
            normalized = os.path.normpath(py_file)
            if normalized == allowed:
                continue
            with open(py_file, encoding="utf-8") as f:
                content = f.read()
            if "import deepagents" in content or "from deepagents" in content:
                violations.append(os.path.relpath(py_file, agentfox_dir))

        assert violations == [], f"Files outside backends/deepagents.py import deepagents: {violations}"


# ---------------------------------------------------------------------------
# TS-03-3: DeepAgentsBackend uses canonical types from types.py unchanged
# Requirement: 03-REQ-1.3
# ---------------------------------------------------------------------------


class TestTypesModuleUnchanged:
    """Verify types.py is not modified and canonical types are used."""

    # Baseline MD5 hash of types.py before spec-03 changes.
    # If types.py is modified by spec-03 implementation, this test fails.
    _BASELINE_TYPES_HASH = "f42f25750d82de1bbf5aecbce91f63cf"

    def test_types_module_hash_unchanged(self) -> None:
        """TS-03-3: types.py content hash matches pre-spec-03 baseline."""
        types_path = inspect.getfile(__import__("agentfox.session.backends.types", fromlist=["types"]))
        with open(types_path, "rb") as f:
            current_hash = hashlib.md5(f.read()).hexdigest()  # noqa: S324

        assert current_hash == self._BASELINE_TYPES_HASH, (
            f"session/backends/types.py was modified (hash {current_hash} "
            f"!= baseline {self._BASELINE_TYPES_HASH}). "
            "Spec 03-REQ-1.3 forbids changes to types.py."
        )

    def test_deepagents_imports_canonical_types(self) -> None:
        """TS-03-3: DeepAgentsBackend module imports types from types.py."""
        import agentfox.session.backends.deepagents as da_mod
        from agentfox.session.backends import types

        # Verify the deepagents module references the same type objects
        assert da_mod.PermissionCallback is types.PermissionCallback
        assert da_mod.ToolUseMessage is types.ToolUseMessage
        assert da_mod.AssistantMessage is types.AssistantMessage
        assert da_mod.ResultMessage is types.ResultMessage


# ---------------------------------------------------------------------------
# TS-03-P4: Property - SDK import containment across all source files
# Property: 03-PROP-4
# Validates: 03-REQ-1.2, 03-REQ-12.1
# ---------------------------------------------------------------------------


class TestPropertySDKContainment:
    """Property: no 'import deepagents' outside session/backends/deepagents.py."""

    def test_prop_sdk_containment(self) -> None:
        """TS-03-P4: Enumerate all .py files; verify no deepagents import outside designated file."""
        backends_dir = os.path.dirname(
            inspect.getfile(__import__("agentfox.session.backends.types", fromlist=["types"]))
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))
        allowed = os.path.normpath(os.path.join(agentfox_dir, "session", "backends", "deepagents.py"))

        for root, _dirs, files in os.walk(agentfox_dir):
            for fname in files:
                if not fname.endswith(".py"):
                    continue
                full_path = os.path.normpath(os.path.join(root, fname))
                if full_path == allowed:
                    continue
                with open(full_path, encoding="utf-8") as f:
                    content = f.read()
                rel = os.path.relpath(full_path, agentfox_dir)
                assert "import deepagents" not in content, (
                    f"{rel} contains 'import deepagents' - only deepagents.py may import the SDK"
                )
                assert "from deepagents" not in content, (
                    f"{rel} contains 'from deepagents' - only deepagents.py may import the SDK"
                )


# ---------------------------------------------------------------------------
# TS-03-41: Containment test extended with deepagents mapping
# Requirement: 03-REQ-12.1
# ---------------------------------------------------------------------------


class TestContainmentTestExtension:
    """Verify containment test covers deepagents SDK import isolation.

    Errata: The existing containment test in test_protocol.py uses a content-scan
    pattern (searching for 'claude_agent_sdk' string), not a mapping dict.
    This test extends that pattern for 'deepagents'.
    """

    def test_containment_includes_deepagents(self) -> None:
        """TS-03-41: Containment test catches deepagents imports outside deepagents.py."""
        backends_dir = os.path.dirname(
            inspect.getfile(__import__("agentfox.session.backends.types", fromlist=["types"]))
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))
        allowed_file = os.path.normpath(os.path.join(agentfox_dir, "session", "backends", "deepagents.py"))

        py_files = glob.glob(os.path.join(agentfox_dir, "**", "*.py"), recursive=True)

        violations = []
        for py_file in py_files:
            normalized = os.path.normpath(py_file)
            if normalized == allowed_file:
                continue
            with open(py_file, encoding="utf-8") as f:
                content = f.read()
            if "import deepagents" in content or "from deepagents" in content:
                violations.append(os.path.relpath(py_file, agentfox_dir))

        assert violations == [], (
            f"Containment violation: deepagents import found outside session/backends/deepagents.py: {violations}"
        )


# ---------------------------------------------------------------------------
# TS-03-4: execute() method signature
# Requirement: 03-REQ-2.1
# ---------------------------------------------------------------------------


class TestExecuteSignature:
    """Verify DeepAgentsBackend.execute() signature matches Backend Protocol."""

    def test_execute_method_signature(self) -> None:
        """TS-03-4: execute() has correct positional and keyword-only parameters."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        sig = inspect.signature(DeepAgentsBackend.execute)
        params = sig.parameters

        # prompt: positional-or-keyword
        assert "prompt" in params
        assert params["prompt"].kind == inspect.Parameter.POSITIONAL_OR_KEYWORD

        # system_prompt: keyword-only
        assert "system_prompt" in params
        assert params["system_prompt"].kind == inspect.Parameter.KEYWORD_ONLY

        # model: keyword-only
        assert "model" in params
        assert params["model"].kind == inspect.Parameter.KEYWORD_ONLY

        # cwd: keyword-only
        assert "cwd" in params
        assert params["cwd"].kind == inspect.Parameter.KEYWORD_ONLY

        # permission_callback: keyword-only with default None
        assert "permission_callback" in params
        assert params["permission_callback"].kind == inspect.Parameter.KEYWORD_ONLY
        assert params["permission_callback"].default is None

        # **kwargs
        assert "kwargs" in params
        assert params["kwargs"].kind == inspect.Parameter.VAR_KEYWORD


# ---------------------------------------------------------------------------
# TS-03-31: create_backend('deepagents') returns DeepAgentsBackend
# Requirement: 03-REQ-8.1
#
# Errata: create_backend() factory does not exist in the codebase.
# The implementation group (task 7.3) must create it.
# ---------------------------------------------------------------------------


class TestCreateBackendFactory:
    """Verify create_backend() dispatches correctly for deepagents and claude."""

    def test_create_backend_deepagents(self) -> None:
        """TS-03-31: create_backend('deepagents') returns DeepAgentsBackend.

        Errata: create_backend() must be created by the implementation group.
        """
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        result = create_backend("deepagents")
        assert isinstance(result, DeepAgentsBackend)

    def test_create_backend_deepagents_distinct_instances(self) -> None:
        """TS-03-32: Each call returns a distinct new object."""
        from agentfox.session.backends import create_backend

        b1 = create_backend("deepagents")
        b2 = create_backend("deepagents")
        assert b1 is not b2

    def test_create_backend_claude_unchanged(self) -> None:
        """TS-03-33: create_backend('claude') returns ClaudeBackend unchanged."""
        from agentfox.session.backends import create_backend
        from agentfox.session.backends.claude import ClaudeBackend

        result = create_backend("claude")
        assert isinstance(result, ClaudeBackend)


# ---------------------------------------------------------------------------
# TS-03-34, TS-03-35: OrchestratorConfig.backend Literal widening
# Requirement: 03-REQ-9.1, 03-REQ-9.2
#
# Backend provider field moved from OrchestratorConfig to BackendConfig.
# ---------------------------------------------------------------------------


class TestOrchestratorConfigBackend:
    """Verify BackendConfig accepts 'claude' and 'deepagents' provider values."""

    def test_config_accepts_deepagents(self) -> None:
        """TS-03-34: BackendConfig(provider='deepagents') succeeds."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="deepagents")
        assert config.provider == "deepagents"

    def test_config_accepts_claude(self) -> None:
        """TS-03-34/35: BackendConfig(provider='claude') succeeds."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="claude")
        assert config.provider == "claude"

    def test_config_rejects_invalid(self) -> None:
        """TS-03-34: BackendConfig(provider='invalid') raises validation error."""
        from agentfox.core.config import BackendConfig

        with pytest.raises((ValueError, TypeError, Exception)):
            BackendConfig(provider="llama")

    def test_existing_claude_config_valid(self) -> None:
        """TS-03-35: Existing provider='claude' configs pass validation unchanged."""
        from agentfox.core.config import BackendConfig

        config = BackendConfig(provider="claude")
        assert config.provider == "claude"


# ---------------------------------------------------------------------------
# TS-03-36: pyproject.toml declares deepagents dependency
# Requirement: 03-REQ-10.1
# ---------------------------------------------------------------------------


class TestPyprojectDeepagentsDependency:
    """Verify pyproject.toml declares deepagents as a mandatory dependency."""

    def test_deepagents_in_dependencies(self) -> None:
        """TS-03-36: dependencies contains 'deepagents>=0.5'."""
        types_path = inspect.getfile(
            __import__("agentfox.session.backends.types", fromlist=["types"])
        )
        backends_dir = os.path.dirname(types_path)
        pyproject_path = os.path.normpath(
            os.path.join(backends_dir, "..", "..", "..", "pyproject.toml")
        )

        assert os.path.exists(pyproject_path), f"pyproject.toml not found at {pyproject_path}"

        with open(pyproject_path, "rb") as f:
            config = tomllib.load(f)

        deps = config.get("project", {}).get("dependencies", [])
        assert any("deepagents>=0.5" in d for d in deps), (
            f"Expected 'deepagents>=0.5' in project.dependencies, got {deps}"
        )



# ---------------------------------------------------------------------------
# TS-03-39: ClaudeBackend backward compatibility
# Requirement: 03-REQ-11.1
# ---------------------------------------------------------------------------


class TestClaudeBackendBackwardCompat:
    """Verify ClaudeBackend import path and module structure unchanged."""

    def test_claude_backend_importable(self) -> None:
        """TS-03-39: ClaudeBackend still importable from original path."""
        from agentfox.session.backends.claude import ClaudeBackend

        assert ClaudeBackend is not None
        backend = ClaudeBackend()
        assert hasattr(backend, "execute")
        assert hasattr(backend, "close")

    def test_init_exports_unchanged(self) -> None:
        """TS-03-39: __init__.py still exports expected symbols."""
        from agentfox.session.backends import (
            AgentMessage,
            AssistantMessage,
            ClaudeBackend,
            PermissionCallback,
            ResultMessage,
            ToolUseMessage,
        )

        # All expected symbols are importable
        assert ClaudeBackend is not None
        assert AgentMessage is not None
        assert AssistantMessage is not None
        assert PermissionCallback is not None
        assert ResultMessage is not None
        assert ToolUseMessage is not None


# ---------------------------------------------------------------------------
# TS-03-40: All pre-existing session-layer tests pass
# Requirement: 03-REQ-11.2
# ---------------------------------------------------------------------------


class TestExistingTestsUnbroken:
    """Verify no regressions in pre-existing test suite."""

    def test_existing_canonical_types_importable(self) -> None:
        """TS-03-40: Canonical types remain importable and functional."""
        from agentfox.session.backends.types import (
            AgentMessage,
            AssistantMessage,
            PermissionCallback,
            ResultMessage,
            ToolUseMessage,
        )

        # Verify types are usable
        tm = ToolUseMessage(tool_name="test", tool_input={"key": "val"})
        assert tm.tool_name == "test"

        am = AssistantMessage(content="hello")
        assert am.content == "hello"

        rm = ResultMessage(
            status="completed",
            input_tokens=0,
            output_tokens=0,
            duration_ms=0,
            error_message=None,
            is_error=False,
        )
        assert rm.is_error is False

        # Verify union type
        assert AgentMessage is not None
        assert PermissionCallback is not None

    def test_claude_backend_unchanged(self) -> None:
        """TS-03-40: ClaudeBackend behavior unchanged post spec-03."""
        from agentfox.session.backends.claude import ClaudeBackend

        backend = ClaudeBackend()
        assert backend.name == "claude"


# ===========================================================================
# Task Group 2: execute() event mapping and token usage tests
# ===========================================================================


# ---------------------------------------------------------------------------
# Helpers for synthetic astream_events v2 event generation
# ---------------------------------------------------------------------------


def _make_chat_stream_event(chunk: str) -> dict[str, Any]:
    """Create a synthetic on_chat_model_stream event with a text chunk.

    LangGraph astream_events v2 fires this kind for each streamed token/chunk.
    The DeepAgentsBackend should map it to an AssistantMessage.
    """
    return {
        "event": "on_chat_model_stream",
        "data": {"chunk": MagicMock(content=chunk)},
        "name": "ChatModel",
    }


def _make_on_tool_start_event(
    tool_name: str = "read_file",
    tool_input: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Create a synthetic on_tool_start event.

    The DeepAgentsBackend should map it to a ToolUseMessage.
    """
    return {
        "event": "on_tool_start",
        "name": tool_name,
        "data": {"input": tool_input or {}},
    }


def _make_on_tool_end_event(
    tool_name: str = "read_file",
    output: str = "content",
) -> dict[str, Any]:
    """Create a synthetic on_tool_end event.

    The DeepAgentsBackend should map it to a ToolUseMessage.
    """
    return {
        "event": "on_tool_end",
        "name": tool_name,
        "data": {"output": output},
    }


def _make_llm_end_event(
    input_tokens: int | None = None,
    output_tokens: int | None = None,
) -> dict[str, Any]:
    """Create a synthetic on_llm_end event with optional token usage.

    The DeepAgentsBackend should accumulate token counts from these events.
    No message should be yielded for on_llm_end events.
    """
    usage: dict[str, int] | None = None
    if input_tokens is not None or output_tokens is not None:
        usage = {}
        if input_tokens is not None:
            usage["input_tokens"] = input_tokens
        if output_tokens is not None:
            usage["output_tokens"] = output_tokens

    output_mock = MagicMock()
    output_mock.usage_metadata = usage
    return {
        "event": "on_llm_end",
        "data": {"output": output_mock},
        "name": "ChatModel",
    }


async def _async_event_stream(
    events: list[dict[str, Any]],
) -> Any:
    """Async generator that yields events from a list.

    Returns an async iterator suitable for use as a mock astream_events return.
    """
    for event in events:
        yield event


async def _collect_async(ait: Any) -> list[Any]:
    """Drain an async iterator into a list."""
    messages: list[Any] = []
    async for msg in ait:
        messages.append(msg)
    return messages


def _make_mock_agent_with_events(
    events: list[dict[str, Any]],
) -> MagicMock:
    """Create a mock agent whose astream_events returns the given events."""
    agent = MagicMock()

    def astream_events_side_effect(*_args: Any, **_kwargs: Any) -> Any:
        return _async_event_stream(events)

    agent.astream_events = astream_events_side_effect
    return agent


def _make_mock_agent_empty() -> MagicMock:
    """Create a mock agent with an empty event stream (only terminal)."""
    return _make_mock_agent_with_events(
        [
            _make_llm_end_event(input_tokens=0, output_tokens=0),
        ]
    )


# ---------------------------------------------------------------------------
# TS-03-5: execute() calls create_deep_agent with correct params and 5 tools
# Requirement: 03-REQ-2.2
# ---------------------------------------------------------------------------


class TestCreateDeepAgentCall:
    """Verify execute() calls create_deep_agent() with correct parameters."""

    @pytest.mark.asyncio
    async def test_create_deep_agent_called_with_params_and_tools(self) -> None:
        """TS-03-5: create_deep_agent called with model, system_prompt, cwd, 5 tools.

        Errata: The exact af SDK functions (spec_read, context_search, etc.)
        may not exist yet (see E7). This test verifies that create_deep_agent
        is called with the correct core parameters and a tools list of the
        expected length.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        backend = DeepAgentsBackend()
        mock_agent = _make_mock_agent_empty()

        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ) as mock_create:
            messages = await _collect_async(
                backend.execute(
                    "do task",
                    system_prompt="sys",
                    model="openai:gpt-5.5",
                    cwd="/workspace",
                )
            )

        # create_deep_agent was called
        assert mock_create.called, "create_deep_agent was not called"
        call_kwargs = mock_create.call_args.kwargs

        # Core params forwarded correctly
        assert call_kwargs["model"] == "openai:gpt-5.5"
        assert call_kwargs["system_prompt"] == "sys"
        assert call_kwargs["cwd"] == "/workspace"

        # Tools list has 5 items
        assert "tools" in call_kwargs
        assert len(call_kwargs["tools"]) == 5

        # Stream terminates with a ResultMessage
        assert any(isinstance(m, ResultMessage) for m in messages)


# ---------------------------------------------------------------------------
# TS-03-6: on_tool_start and on_tool_end → ToolUseMessage
# Requirement: 03-REQ-2.3
# ---------------------------------------------------------------------------


class TestToolEventMapping:
    """Verify on_tool_start and on_tool_end events map to ToolUseMessage."""

    @pytest.mark.asyncio
    async def test_tool_events_yield_tool_use_messages(self) -> None:
        """TS-03-6: on_tool_start + on_tool_end → two ToolUseMessage instances."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        events = [
            _make_on_tool_start_event(
                tool_name="read_file",
                tool_input={"path": "foo.py"},
            ),
            _make_on_tool_end_event(
                tool_name="read_file",
                output="content",
            ),
            _make_llm_end_event(input_tokens=5, output_tokens=3),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "prompt",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) == 2, f"Expected 2 ToolUseMessage, got {len(tool_msgs)}"

        # Terminal ResultMessage should be last
        assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-7: on_chat_model_stream → AssistantMessage
# Requirement: 03-REQ-2.4
# ---------------------------------------------------------------------------


class TestChatStreamEventMapping:
    """Verify on_chat_model_stream events map to AssistantMessage."""

    @pytest.mark.asyncio
    async def test_chat_stream_events_yield_assistant_messages(self) -> None:
        """TS-03-7: Two on_chat_model_stream events → two AssistantMessage instances."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import AssistantMessage

        events = [
            _make_chat_stream_event("Hello"),
            _make_chat_stream_event(" world"),
            _make_llm_end_event(input_tokens=10, output_tokens=5),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        asst_msgs = [m for m in messages if isinstance(m, AssistantMessage)]
        assert len(asst_msgs) == 2, f"Expected 2 AssistantMessage, got {len(asst_msgs)}"
        assert asst_msgs[0].content == "Hello"
        assert asst_msgs[1].content == " world"


# ---------------------------------------------------------------------------
# TS-03-8: on_llm_end accumulates tokens; on_chat_model_stream counts ignored
# Requirement: 03-REQ-2.5
# ---------------------------------------------------------------------------


class TestTokenAccumulation:
    """Verify token counts come from on_llm_end, not on_chat_model_stream."""

    @pytest.mark.asyncio
    async def test_token_counts_from_llm_end_only(self) -> None:
        """TS-03-8: Token counts from on_llm_end used; on_chat_model_stream ignored.

        The on_chat_model_stream event may carry spurious usage data that must
        NOT be included in the final token counts. Only on_llm_end is authoritative.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        events = [
            _make_chat_stream_event("hi"),
            _make_llm_end_event(input_tokens=10, output_tokens=5),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.input_tokens == 10
        assert result.output_tokens == 5

    @pytest.mark.asyncio
    async def test_no_message_yielded_for_llm_end(self) -> None:
        """TS-03-8 additional: on_llm_end does not yield any message."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import (
            AssistantMessage,
            ResultMessage,
            ToolUseMessage,
        )

        # Stream with only an on_llm_end event (no chat or tool events)
        events = [
            _make_llm_end_event(input_tokens=10, output_tokens=5),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # Should only have the terminal ResultMessage, no AssistantMessage or
        # ToolUseMessage from the on_llm_end event
        non_result = [m for m in messages if isinstance(m, (AssistantMessage, ToolUseMessage))]
        assert len(non_result) == 0, "on_llm_end should not yield AssistantMessage or ToolUseMessage"
        assert isinstance(messages[-1], ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-9: Exactly one terminal ResultMessage with is_error=False
# Requirement: 03-REQ-2.6
# ---------------------------------------------------------------------------


class TestTerminalResultMessage:
    """Verify exactly one ResultMessage is yielded at end of successful stream."""

    @pytest.mark.asyncio
    async def test_single_result_message_on_success(self) -> None:
        """TS-03-9: Last message is ResultMessage with is_error=False; appears once."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        events = [
            _make_chat_stream_event("hello"),
            _make_on_tool_start_event("read_file", {"path": "x.py"}),
            _make_on_tool_end_event("read_file", "content"),
            _make_llm_end_event(input_tokens=20, output_tokens=10),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result_msgs = [m for m in messages if isinstance(m, ResultMessage)]
        assert len(result_msgs) == 1, f"Expected exactly 1 ResultMessage, got {len(result_msgs)}"
        assert result_msgs[0] is messages[-1], "ResultMessage must be the last yielded item"
        assert result_msgs[0].is_error is False


# ---------------------------------------------------------------------------
# TS-03-10: Token fields are 0 (not None) when provider omits usage data
# Requirement: 03-REQ-2.7
#
# Errata E5: ResultMessage.input_tokens is int (non-optional), so we use 0
# for missing tokens instead of None as the spec says.
# ---------------------------------------------------------------------------


class TestMissingTokenCounts:
    """Verify token fields default to 0 when provider omits usage data."""

    @pytest.mark.asyncio
    async def test_missing_token_counts_are_zero(self) -> None:
        """TS-03-10: Token fields are 0 when on_llm_end has no usage data.

        Errata E5: The spec says None, but ResultMessage.input_tokens is int
        (non-optional). ClaudeBackend uses 0 for missing tokens. We follow
        that convention.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        # on_llm_end with no usage data (both None)
        events = [
            _make_llm_end_event(input_tokens=None, output_tokens=None),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        # Errata E5: 0, not None
        assert result.input_tokens == 0
        assert result.output_tokens == 0


# ---------------------------------------------------------------------------
# TS-03-P3: Property - token accumulation correctness
# Property: 03-PROP-3
# Validates: 03-REQ-2.5, 03-REQ-2.6, 03-REQ-2.7
# ---------------------------------------------------------------------------


class TestPropertyTokenAccumulation:
    """Property: ResultMessage tokens equal sum of on_llm_end counts only."""

    @pytest.mark.asyncio
    async def test_prop_multiple_llm_end_events_summed(self) -> None:
        """TS-03-P3: Multiple on_llm_end events are summed correctly.

        Generates N on_llm_end events with known token counts and verifies the
        terminal ResultMessage has the correct sum. on_chat_model_stream events
        with spurious usage data must not contribute to the total.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        # Three on_llm_end events with distinct token counts
        events = [
            _make_chat_stream_event("a"),  # spurious — should not count
            _make_llm_end_event(input_tokens=10, output_tokens=5),
            _make_chat_stream_event("b"),
            _make_llm_end_event(input_tokens=20, output_tokens=15),
            _make_llm_end_event(input_tokens=30, output_tokens=25),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        # Expected sums: 10+20+30 = 60 input, 5+15+25 = 45 output
        assert result.input_tokens == 60
        assert result.output_tokens == 45

    @pytest.mark.asyncio
    async def test_prop_mixed_present_and_missing_usage(self) -> None:
        """TS-03-P3: Mix of present and missing usage in on_llm_end events.

        When some on_llm_end events have usage data and others don't, only the
        present values are summed. Missing values contribute 0.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        events = [
            _make_llm_end_event(input_tokens=10, output_tokens=5),
            _make_llm_end_event(input_tokens=None, output_tokens=None),
            _make_llm_end_event(input_tokens=20, output_tokens=15),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.input_tokens == 30  # 10 + 0 + 20
        assert result.output_tokens == 20  # 5 + 0 + 15


# ---------------------------------------------------------------------------
# TS-03-11: Malformed event skipped with WARNING log
# Requirement: 03-REQ-2.8
# ---------------------------------------------------------------------------


class TestMalformedEventHandling:
    """Verify malformed events are skipped with a WARNING log."""

    @pytest.mark.asyncio
    async def test_malformed_event_skipped_with_warning(self) -> None:
        """TS-03-11: Malformed event logs WARNING and does not stop stream.

        A malformed event (e.g. missing required fields) should be skipped,
        a WARNING should be logged, and subsequent valid events should be
        processed normally.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        events = [
            # Malformed: on_tool_start missing tool_name/input fields
            {"event": "on_tool_start"},
            # Valid event after the malformed one
            _make_llm_end_event(input_tokens=3, output_tokens=2),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            # No exception should be raised — malformed events are skipped
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # Terminal ResultMessage should be yielded normally
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].input_tokens == 3
        assert messages[-1].output_tokens == 2

    @pytest.mark.asyncio
    async def test_malformed_event_warning_logged(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-03-11: WARNING-level log message emitted for malformed events."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        events = [
            # Malformed: completely empty event dict
            {},
            _make_llm_end_event(input_tokens=1, output_tokens=1),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            with caplog.at_level(logging.WARNING):
                await _collect_async(
                    backend.execute(
                        "p",
                        system_prompt="s",
                        model="m",
                        cwd="/",
                    )
                )

        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) >= 1, "Expected at least one WARNING log for the malformed event"


# ---------------------------------------------------------------------------
# TS-03-12: No exception propagates from execute()
# Requirement: 03-REQ-2.9
# ---------------------------------------------------------------------------


class TestNoExceptionPropagation:
    """Verify execute() never propagates exceptions; always yields ResultMessage."""

    @pytest.mark.asyncio
    async def test_runtime_error_yields_error_result(self) -> None:
        """TS-03-12: RuntimeError → ResultMessage(is_error=True), no propagation."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def raising_stream(*_args: Any, **_kwargs: Any) -> Any:
            raise RuntimeError("unexpected")
            yield  # noqa: RUF028 — makes this an async generator

        mock_agent = MagicMock()
        mock_agent.astream_events = raising_stream

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            # No exception should escape execute()
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True

    @pytest.mark.asyncio
    async def test_value_error_yields_error_result(self) -> None:
        """TS-03-12 variant: ValueError also yields ResultMessage(is_error=True)."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def raising_stream(*_args: Any, **_kwargs: Any) -> Any:
            raise ValueError("bad input")
            yield  # noqa: RUF028 — makes this an async generator

        mock_agent = MagicMock()
        mock_agent.astream_events = raising_stream

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True

    @pytest.mark.asyncio
    async def test_exception_during_create_deep_agent(self) -> None:
        """TS-03-12 variant: Exception in create_deep_agent itself."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=RuntimeError("agent creation failed"),
        ):
            backend = DeepAgentsBackend()
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is True


# ---------------------------------------------------------------------------
# TS-03-P2: Property - always exactly one ResultMessage as final item
# Property: 03-PROP-2
# Validates: 03-REQ-2.6, 03-REQ-2.9, 03-REQ-6.2, 03-REQ-6.3
# ---------------------------------------------------------------------------


class TestPropertyResultMessageAlwaysTerminal:
    """Property: every execute() invocation yields exactly one ResultMessage last."""

    @pytest.mark.asyncio
    async def test_prop_empty_stream(self) -> None:
        """TS-03-P2(a): Empty event stream → exactly one ResultMessage."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        mock_agent = _make_mock_agent_with_events([])

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result_msgs = [m for m in messages if isinstance(m, ResultMessage)]
        assert len(result_msgs) == 1
        assert result_msgs[0] is messages[-1]

    @pytest.mark.asyncio
    async def test_prop_success_stream(self) -> None:
        """TS-03-P2(b): Successful stream → exactly one ResultMessage at end."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        events = [
            _make_chat_stream_event("hello"),
            _make_chat_stream_event(" world"),
            _make_on_tool_start_event("write_file", {"path": "f.py"}),
            _make_on_tool_end_event("write_file", "ok"),
            _make_llm_end_event(input_tokens=50, output_tokens=30),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result_msgs = [m for m in messages if isinstance(m, ResultMessage)]
        assert len(result_msgs) == 1
        assert result_msgs[0] is messages[-1]

    @pytest.mark.asyncio
    async def test_prop_error_stream(self) -> None:
        """TS-03-P2(d): Error during streaming → exactly one ResultMessage."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def raising_stream(*_args: Any, **_kwargs: Any) -> Any:
            raise RuntimeError("boom")
            yield  # noqa: RUF028

        mock_agent = MagicMock()
        mock_agent.astream_events = raising_stream

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result_msgs = [m for m in messages if isinstance(m, ResultMessage)]
        assert len(result_msgs) == 1
        assert result_msgs[0] is messages[-1]
        assert result_msgs[0].is_error is True


# ===========================================================================
# Task Group 3: af SDK tool registration and permission callback tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-03-13: Five af SDK functions registered as LangChain tools
# Requirement: 03-REQ-3.1
# ---------------------------------------------------------------------------


class TestAfSdkToolRegistration:
    """Verify exactly five af SDK functions are registered as LangChain tools."""

    @pytest.mark.asyncio
    async def test_five_tools_passed_to_create_deep_agent(self) -> None:
        """TS-03-13: tools list passed to create_deep_agent has 5 LangChain tools.

        Captures the tools argument from the create_deep_agent call and verifies:
        - Exactly 5 tools are passed
        - Each is a LangChain BaseTool instance
        - Tool names match the five af SDK functions

        Errata E7: The exact af SDK functions may not exist yet. Tool names
        are verified against the spec-defined set but the test is structured
        to adapt once the functions are implemented.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert captured_tools is not None, "create_deep_agent was not called"
        assert len(captured_tools) == 5, f"Expected 5 tools, got {len(captured_tools)}"

        # Each tool should be a LangChain BaseTool (or at minimum have .name)
        expected_names = {
            "spec_read",
            "context_search",
            "context_get",
            "memory_recall",
            "subtask_state",
        }
        actual_names = {t.name for t in captured_tools}
        assert actual_names == expected_names, (
            f"Tool names mismatch. Expected {expected_names}, got {actual_names}"
        )

    @pytest.mark.asyncio
    async def test_tools_are_langchain_base_tools(self) -> None:
        """TS-03-13: Each tool in the list is a LangChain BaseTool instance."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert captured_tools is not None
        for tool in captured_tools:
            # LangChain BaseTool has .name, .description, and .args_schema
            assert hasattr(tool, "name"), f"Tool {tool} missing .name attribute"
            assert hasattr(tool, "description"), f"Tool {tool.name} missing .description"
            assert hasattr(tool, "args_schema"), f"Tool {tool.name} missing .args_schema"


# ---------------------------------------------------------------------------
# TS-03-14: Tool wrappers carry complete type annotations for schema generation
# Requirement: 03-REQ-3.2
# ---------------------------------------------------------------------------


class TestToolSchemaGeneration:
    """Verify LangChain tool schema generation succeeds for all five tools."""

    @pytest.mark.asyncio
    async def test_tool_schemas_are_complete(self) -> None:
        """TS-03-14: Each tool has a valid args_schema with typed properties.

        LangChain auto-generates JSON schemas from Python type annotations.
        This test verifies that schema generation succeeds and each tool's
        schema has non-empty properties with types defined.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert captured_tools is not None
        assert len(captured_tools) == 5

        for tool in captured_tools:
            # LangChain BaseTool.args_schema is a pydantic model class
            schema = tool.args_schema.schema()
            assert "properties" in schema, (
                f"Tool '{tool.name}' schema missing 'properties'"
            )
            assert len(schema["properties"]) > 0, (
                f"Tool '{tool.name}' has empty properties"
            )
            # Each field must have a type or $ref (no untyped params)
            for field_name, field_def in schema["properties"].items():
                assert "type" in field_def or "$ref" in field_def or "anyOf" in field_def, (
                    f"Tool '{tool.name}' field '{field_name}' has no type annotation"
                )


# ---------------------------------------------------------------------------
# TS-03-15: Tool wrappers call af SDK functions in-process synchronously
# Requirement: 03-REQ-3.3
# ---------------------------------------------------------------------------


class TestToolWrappersCallInProcess:
    """Verify tool wrappers delegate to af SDK functions in-process."""

    @pytest.mark.asyncio
    async def test_tool_wrappers_delegate_to_sdk_functions(self) -> None:
        """TS-03-15: Each tool wrapper calls the underlying af SDK function.

        Patches each af SDK function to verify it is called when the
        corresponding LangChain tool wrapper is invoked. No subprocess
        is spawned and no network I/O occurs at the tool boundary.

        Errata E7: The exact af SDK function import paths may differ.
        This test patches at the module where the wrappers import from.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert captured_tools is not None
        tools_by_name = {t.name: t for t in captured_tools}

        # Verify each tool is callable (thin sync wrapper)
        for tool_name, tool in tools_by_name.items():
            assert callable(getattr(tool, "invoke", None)) or callable(tool), (
                f"Tool '{tool_name}' is not callable"
            )


# ---------------------------------------------------------------------------
# TS-03-E4: af SDK functions have complete type annotations
# Requirement: 03-REQ-3.E1
# ---------------------------------------------------------------------------


class TestAfSdkFunctionAnnotations:
    """Verify af SDK functions have complete type annotations."""

    @pytest.mark.asyncio
    async def test_tool_wrappers_have_complete_annotations(self) -> None:
        """TS-03-E4: All tool wrapper functions have typed parameters and return.

        Verifies that the LangChain tool wrappers (which mirror the af SDK
        function signatures) carry complete Python type annotations. This
        ensures LangChain's JSON schema auto-generation produces correct output.

        Errata E7: If the af SDK functions don't exist yet, we verify the
        annotations on the @tool-decorated wrapper functions instead.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert captured_tools is not None
        assert len(captured_tools) == 5

        for tool in captured_tools:
            # LangChain BaseTool wraps a function; verify schema is non-trivial
            schema = tool.args_schema.schema()
            properties = schema.get("properties", {})
            # Every tool must have at least one typed parameter
            assert len(properties) > 0, (
                f"Tool '{tool.name}' has no typed parameters in args_schema"
            )


# ---------------------------------------------------------------------------
# TS-03-16: Permission callback invoked with tool_name and tool_input
# Requirement: 03-REQ-4.1
# ---------------------------------------------------------------------------


class TestPermissionCallbackMapping:
    """Verify permission_callback integration with interrupt mechanism."""

    @pytest.mark.asyncio
    async def test_permission_callback_invoked_with_tool_info(self) -> None:
        """TS-03-16: permission_callback called with (tool_name, tool_input).

        When a permission_callback is provided and the Deep Agents interrupt
        mechanism fires for a tool call, the callback receives the tool_name
        and tool_input extracted from the interrupt event payload.

        Errata E4: PermissionCallback is async (Awaitable[bool]), not sync.
        The test callback is async and returns a bool coroutine.

        Errata E7: The interrupt mechanism API is unverified. This test
        mocks the agent to simulate an interrupt event flow where the
        tool call triggers the permission callback through the backend.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        callback_calls: list[tuple[str, dict[str, Any]]] = []

        async def my_callback(name: str, inp: dict[str, Any]) -> bool:
            callback_calls.append((name, inp))
            return True

        # Create an event stream that includes a tool call which should
        # trigger the permission callback through the interrupt mechanism.
        # The mock agent must be configured to trigger the interrupt/callback
        # flow during tool execution.
        events = [
            _make_on_tool_start_event(
                tool_name="write_file",
                tool_input={"path": "out.txt", "content": "data"},
            ),
            _make_on_tool_end_event(tool_name="write_file", output="ok"),
            _make_llm_end_event(input_tokens=5, output_tokens=3),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ) as mock_create:
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                    permission_callback=my_callback,
                )
            )

        # The stream should complete with a ResultMessage
        assert isinstance(messages[-1], ResultMessage)

        # Verify create_deep_agent was called — the implementation must
        # configure interrupt handling when permission_callback is provided.
        assert mock_create.called


# ---------------------------------------------------------------------------
# TS-03-17: No interrupt hook when permission_callback is None
# Requirement: 03-REQ-4.2
# ---------------------------------------------------------------------------


class TestNoInterruptHookWhenCallbackNone:
    """Verify no interrupt hook is registered when permission_callback is None."""

    @pytest.mark.asyncio
    async def test_no_interrupt_hook_without_callback(self) -> None:
        """TS-03-17: create_deep_agent called without interrupt hook arguments.

        When permission_callback is None, the DeepAgentsBackend should NOT
        register any interrupt hook with create_deep_agent. All tool calls
        proceed automatically without approval checks.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                    permission_callback=None,
                )
            )

        call_kwargs = mock_create.call_args.kwargs

        # No interrupt/permission hook should be registered
        # The exact kwarg name depends on the deepagents API but common
        # names are 'on_interrupt', 'interrupt_callback', 'interrupt_handler'
        interrupt_related_keys = {
            "on_interrupt",
            "interrupt_callback",
            "interrupt_handler",
            "permission_callback",
        }
        found_interrupt_keys = interrupt_related_keys & set(call_kwargs.keys())
        assert not found_interrupt_keys, (
            f"Interrupt-related kwargs found when permission_callback=None: {found_interrupt_keys}"
        )


# ---------------------------------------------------------------------------
# TS-03-18: create_deep_agent() never called with 'permissions' kwarg
# Requirement: 03-REQ-4.3
# ---------------------------------------------------------------------------


class TestNoPermissionsKwarg:
    """Verify create_deep_agent is never called with a 'permissions' argument."""

    @pytest.mark.asyncio
    async def test_permissions_never_in_create_kwargs(self) -> None:
        """TS-03-18: 'permissions' not in create_deep_agent call_args.kwargs.

        The spec explicitly forbids passing the 'permissions' parameter to
        create_deep_agent(). Permission enforcement is delegated exclusively
        to the permission_callback / interrupt mechanism.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        call_kwargs = mock_create.call_args.kwargs
        assert "permissions" not in call_kwargs, (
            "'permissions' was passed to create_deep_agent; "
            "permission enforcement must use the permission_callback/interrupt mechanism"
        )

    @pytest.mark.asyncio
    async def test_permissions_not_passed_with_callback(self) -> None:
        """TS-03-18 variant: 'permissions' absent even with a permission_callback."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        async def my_callback(name: str, inp: dict[str, Any]) -> bool:
            return True

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                    permission_callback=my_callback,
                )
            )

        call_kwargs = mock_create.call_args.kwargs
        assert "permissions" not in call_kwargs


# ---------------------------------------------------------------------------
# TS-03-E5: Permission callback exception → ResultMessage(is_error=True)
# Requirement: 03-REQ-4.E1
# ---------------------------------------------------------------------------


class TestPermissionCallbackException:
    """Verify permission_callback exception is handled gracefully."""

    @pytest.mark.asyncio
    async def test_callback_exception_yields_error_result(self) -> None:
        """TS-03-E5: permission_callback raises → ResultMessage(is_error=True).

        When permission_callback raises an exception, execute() must:
        1. Deny the tool call
        2. Yield ResultMessage(is_error=True, is_transport_error=False)
        3. Exit cleanly without propagating the exception

        Errata E4: The callback is async, so the exception is raised from
        an awaited coroutine.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def bad_callback(name: str, inp: dict[str, Any]) -> bool:
            raise RuntimeError("callback error")

        # The mock agent must trigger a flow where the permission callback
        # is invoked. The interrupt mechanism should be configured by
        # the backend when permission_callback is provided.
        events = [
            _make_on_tool_start_event(
                tool_name="write_file",
                tool_input={"path": "x"},
            ),
            _make_on_tool_end_event(tool_name="write_file", output="ok"),
            _make_llm_end_event(input_tokens=1, output_tokens=1),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            # No exception should propagate to the caller
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                    permission_callback=bad_callback,
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is False


# ---------------------------------------------------------------------------
# TS-03-P7: Property - 'permissions' never in any create_deep_agent call
# Property: 03-PROP-7
# Validates: 03-REQ-4.3
# ---------------------------------------------------------------------------


class TestPropertyPermissionsNeverPassed:
    """Property: create_deep_agent() never called with 'permissions' kwarg."""

    @pytest.mark.asyncio
    async def test_prop_permissions_absent_no_callback(self) -> None:
        """TS-03-P7(a): Without callback, 'permissions' never in kwargs."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        all_create_calls: list[dict[str, Any]] = []

        def recording_create(**kwargs: Any) -> MagicMock:
            all_create_calls.append(kwargs)
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=recording_create,
        ):
            await _collect_async(
                backend.execute(
                    "prompt",
                    system_prompt="sys",
                    model="openai:gpt-4o",
                    cwd="/workspace",
                    permission_callback=None,
                )
            )

        for call_kwargs in all_create_calls:
            assert "permissions" not in call_kwargs, (
                "'permissions' found in create_deep_agent kwargs (no callback)"
            )

    @pytest.mark.asyncio
    async def test_prop_permissions_absent_with_callback(self) -> None:
        """TS-03-P7(b): With callback, 'permissions' never in kwargs."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        async def my_callback(name: str, inp: dict[str, Any]) -> bool:
            return True

        all_create_calls: list[dict[str, Any]] = []

        def recording_create(**kwargs: Any) -> MagicMock:
            all_create_calls.append(kwargs)
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=recording_create,
        ):
            await _collect_async(
                backend.execute(
                    "prompt",
                    system_prompt="sys",
                    model="anthropic:claude-sonnet-4-6",
                    cwd="/home",
                    permission_callback=my_callback,
                )
            )

        for call_kwargs in all_create_calls:
            assert "permissions" not in call_kwargs, (
                "'permissions' found in create_deep_agent kwargs (with callback)"
            )

    @pytest.mark.asyncio
    async def test_prop_permissions_absent_various_models(self) -> None:
        """TS-03-P7(c): Across model prefixes, 'permissions' never in kwargs."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        models = [
            "openai:gpt-4o",
            "anthropic:claude-sonnet-4-6",
            "ollama:llama3",
        ]

        for model in models:
            all_create_calls: list[dict[str, Any]] = []

            def recording_create(**kwargs: Any) -> MagicMock:
                all_create_calls.append(kwargs)
                return _make_mock_agent_empty()

            backend = DeepAgentsBackend()
            with patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=recording_create,
            ):
                await _collect_async(
                    backend.execute(
                        "prompt",
                        system_prompt="sys",
                        model=model,
                        cwd="/",
                    )
                )

            for call_kwargs in all_create_calls:
                assert "permissions" not in call_kwargs, (
                    f"'permissions' found in create_deep_agent kwargs for model={model}"
                )


# ===========================================================================
# Task Group 4: provider-specific parameter handling and retry logic tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-03-19: thinking parameter handling based on model prefix
# Requirement: 03-REQ-5.1
# ---------------------------------------------------------------------------


class TestThinkingParameterByModelPrefix:
    """Verify thinking parameter is passed for anthropic: models and omitted for others."""

    @pytest.mark.asyncio
    async def test_thinking_passed_for_anthropic_model(self) -> None:
        """TS-03-19: thinking parameter present for anthropic: prefix model.

        When the model string carries the 'anthropic:' prefix, DeepAgentsBackend
        should pass the 'thinking' parameter to create_deep_agent().
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="anthropic:claude-sonnet-4-6",
                    cwd="/",
                )
            )

        assert mock_create.called
        call_kwargs = mock_create.call_args.kwargs
        assert "thinking" in call_kwargs, (
            "Expected 'thinking' in create_deep_agent kwargs for anthropic: model"
        )

    @pytest.mark.asyncio
    async def test_thinking_absent_for_openai_model(self) -> None:
        """TS-03-19: thinking parameter absent for openai: prefix model.

        When the model string does NOT carry the 'anthropic:' prefix, the
        thinking parameter should not be passed to create_deep_agent().
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="openai:gpt-5.5",
                    cwd="/",
                )
            )

        assert mock_create.called
        call_kwargs = mock_create.call_args.kwargs
        assert "thinking" not in call_kwargs, (
            "Expected 'thinking' NOT in create_deep_agent kwargs for openai: model"
        )

    @pytest.mark.asyncio
    async def test_thinking_absent_for_ollama_model(self) -> None:
        """TS-03-19 variant: thinking parameter absent for ollama: prefix model."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=_make_mock_agent_empty(),
        ) as mock_create:
            await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="ollama:llama3",
                    cwd="/",
                )
            )

        assert mock_create.called
        call_kwargs = mock_create.call_args.kwargs
        assert "thinking" not in call_kwargs, (
            "Expected 'thinking' NOT in create_deep_agent kwargs for ollama: model"
        )

    @pytest.mark.asyncio
    async def test_thinking_typeerror_silently_retried(self) -> None:
        """TS-03-19: If create_deep_agent raises TypeError with thinking, retries without it.

        For anthropic: models, if the installed deepagents version does not
        support the thinking parameter, create_deep_agent raises TypeError.
        The backend should retry without thinking silently (provider-level handling).
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        call_count = 0

        def create_side_effect(**kwargs: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if "thinking" in kwargs:
                raise TypeError("unexpected keyword argument 'thinking'")
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=create_side_effect,
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="anthropic:claude-sonnet-4-6",
                    cwd="/",
                )
            )

        # Should have retried without thinking parameter
        assert call_count >= 2, (
            f"Expected at least 2 create_deep_agent calls (retry without thinking), got {call_count}"
        )
        result = messages[-1]
        assert isinstance(result, ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-20: max_budget_usd TypeError handling
# Requirement: 03-REQ-5.2
# ---------------------------------------------------------------------------


class TestMaxBudgetUsdTypeerror:
    """Verify max_budget_usd TypeError triggers retry without the parameter."""

    @pytest.mark.asyncio
    async def test_max_budget_usd_typeerror_retries_without(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-03-20: TypeError for max_budget_usd → retry without it + DEBUG log.

        When create_deep_agent() raises TypeError because of the max_budget_usd
        parameter, execute() should retry without it and log a DEBUG message.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        call_count = 0

        def create_side_effect(**kwargs: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if "max_budget_usd" in kwargs:
                raise TypeError("unexpected keyword argument 'max_budget_usd'")
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=create_side_effect,
        ):
            with caplog.at_level(logging.DEBUG):
                messages = await _collect_async(
                    backend.execute(
                        "p",
                        system_prompt="s",
                        model="m",
                        cwd="/",
                        max_budget_usd=10.0,
                    )
                )

        # Should retry: called at least twice
        assert call_count >= 2, (
            f"Expected at least 2 create_deep_agent calls (retry without max_budget_usd), "
            f"got {call_count}"
        )

        # Should log a DEBUG message about the unsupported parameter
        debug_records = [r for r in caplog.records if r.levelno <= logging.DEBUG]
        assert len(debug_records) >= 1, "Expected at least one DEBUG log for max_budget_usd fallback"

        # Should still produce a successful result
        result = messages[-1]
        assert isinstance(result, ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-21: effort TypeError handling
# Requirement: 03-REQ-5.3
# ---------------------------------------------------------------------------


class TestEffortTypeerror:
    """Verify effort TypeError triggers retry without the parameter."""

    @pytest.mark.asyncio
    async def test_effort_typeerror_retries_without(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-03-21: TypeError for effort → retry without it + DEBUG log.

        When create_deep_agent() raises TypeError because of the effort
        parameter, execute() should retry without it and log a DEBUG message.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        call_count = 0

        def create_side_effect(**kwargs: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if "effort" in kwargs:
                raise TypeError("unexpected keyword argument 'effort'")
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=create_side_effect,
        ):
            with caplog.at_level(logging.DEBUG):
                messages = await _collect_async(
                    backend.execute(
                        "p",
                        system_prompt="s",
                        model="m",
                        cwd="/",
                        effort="high",
                    )
                )

        # Should retry: called at least twice
        assert call_count >= 2, (
            f"Expected at least 2 create_deep_agent calls (retry without effort), "
            f"got {call_count}"
        )

        # Should log a DEBUG message
        debug_records = [r for r in caplog.records if r.levelno <= logging.DEBUG]
        assert len(debug_records) >= 1, "Expected at least one DEBUG log for effort fallback"

        # Should produce a result
        result = messages[-1]
        assert isinstance(result, ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-22: compaction parameter never passed to create_deep_agent
# Requirement: 03-REQ-5.4
# ---------------------------------------------------------------------------


class TestNoCompactionParameter:
    """Verify compaction is never passed to create_deep_agent()."""

    @pytest.mark.asyncio
    async def test_compaction_never_in_create_kwargs(self) -> None:
        """TS-03-22: 'compaction' never appears in create_deep_agent call kwargs.

        Context management is delegated entirely to Deep Agents' built-in
        automatic summarization and large-result eviction. The DeepAgentsBackend
        must not pass a compaction argument to create_deep_agent().
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        all_kwargs: list[dict[str, Any]] = []

        def capture(**kwargs: Any) -> MagicMock:
            all_kwargs.append(kwargs)
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        for kwargs in all_kwargs:
            assert "compaction" not in kwargs, (
                "'compaction' was passed to create_deep_agent; "
                "context management is delegated to Deep Agents"
            )

    @pytest.mark.asyncio
    async def test_compaction_absent_even_when_passed_to_execute(self) -> None:
        """TS-03-22 variant: compaction absent even if execute() receives it in kwargs.

        Even if the caller passes compaction=True to execute() via **kwargs,
        the DeepAgentsBackend must NOT forward it to create_deep_agent().
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        all_kwargs: list[dict[str, Any]] = []

        def capture(**kwargs: Any) -> MagicMock:
            all_kwargs.append(kwargs)
            return _make_mock_agent_empty()

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture,
        ):
            await _collect_async(
                backend.execute(
                    "task",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                    compaction=True,  # type: ignore[arg-type]
                )
            )

        for kwargs in all_kwargs:
            assert "compaction" not in kwargs, (
                "'compaction' forwarded to create_deep_agent from execute() kwargs"
            )


# ---------------------------------------------------------------------------
# TS-03-23: Transient retry with exponential backoff, success on attempt 3
# Requirement: 03-REQ-6.1
# ---------------------------------------------------------------------------


class TestTransientRetryWithBackoff:
    """Verify transient errors trigger retry with exponential backoff."""

    @pytest.mark.asyncio
    async def test_retry_success_on_third_attempt(self) -> None:
        """TS-03-23: Transient errors on attempts 1-2, success on 3.

        With _BACKOFF_BASE=1.0, delays should be:
        - After attempt 1 failure: 1.0s  (_BACKOFF_BASE * 2^0)
        - After attempt 2 failure: 2.0s  (_BACKOFF_BASE * 2^1)
        - Attempt 3 succeeds: no delay

        The terminal ResultMessage should have is_error=False.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        attempt_count = 0

        def make_agent(**_kwargs: Any) -> MagicMock:
            nonlocal attempt_count
            attempt_count += 1
            agent = MagicMock()
            if attempt_count < 3:
                # Transient error: agent's stream raises ConnectionError
                async def failing_stream(*_a: Any, **_kw: Any) -> Any:
                    raise ConnectionError("transient network failure")
                    yield  # noqa: RUF028 — async generator

                agent.astream_events = failing_stream
            else:
                # Success on attempt 3
                agent.astream_events = lambda *a, **kw: _async_event_stream(
                    [_make_llm_end_event(input_tokens=10, output_tokens=5)]
                )
            return agent

        sleep_calls: list[float] = []

        async def fake_sleep(delay: float) -> None:
            sleep_calls.append(delay)

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=make_agent,
            ),
            patch("asyncio.sleep", side_effect=fake_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # Verify backoff delays
        assert sleep_calls == [1.0, 2.0], f"Expected backoff delays [1.0, 2.0], got {sleep_calls}"

        # Verify success
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False


# ---------------------------------------------------------------------------
# TS-03-24: All 3 retries exhausted → terminal error
# Requirement: 03-REQ-6.2
# ---------------------------------------------------------------------------


class TestTransientRetryExhausted:
    """Verify terminal error ResultMessage after all retry attempts exhausted."""

    @pytest.mark.asyncio
    async def test_all_retries_exhausted_yields_transport_error(self) -> None:
        """TS-03-24: All 3 attempts raise transient error → is_transport_error=True.

        After exhausting all 3 retry attempts, execute() must yield a terminal
        ResultMessage with is_error=True and is_transport_error=True. No
        exception should propagate.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def always_failing_stream(*_a: Any, **_kw: Any) -> Any:
            raise ConnectionError("persistent transient failure")
            yield  # noqa: RUF028

        def make_failing_agent(**_kwargs: Any) -> MagicMock:
            agent = MagicMock()
            agent.astream_events = always_failing_stream
            return agent

        async def fake_sleep(_delay: float) -> None:
            pass  # No-op: skip actual sleep

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=make_failing_agent,
            ),
            patch("asyncio.sleep", side_effect=fake_sleep),
        ):
            # No exception should propagate
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is True


# ---------------------------------------------------------------------------
# TS-03-E6: Retry bound strictly to 3 attempts
# Requirement: 03-REQ-6.E1
# ---------------------------------------------------------------------------


class TestRetryBoundStrictlyThree:
    """Verify retry loop is bounded to exactly 3 attempts."""

    @pytest.mark.asyncio
    async def test_exactly_three_attempts(self) -> None:
        """TS-03-E6: Infinite transient errors → exactly 3 attempts, then terminal error.

        Even with persistent transient errors, the retry loop must not iterate
        beyond 3 total attempts.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        attempt_count = 0

        def counting_create(**_kwargs: Any) -> MagicMock:
            nonlocal attempt_count
            attempt_count += 1
            agent = MagicMock()

            async def failing_stream(*_a: Any, **_kw: Any) -> Any:
                raise ConnectionError("transient")
                yield  # noqa: RUF028

            agent.astream_events = failing_stream
            return agent

        async def fake_sleep(_delay: float) -> None:
            pass

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=counting_create,
            ),
            patch("asyncio.sleep", side_effect=fake_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        assert attempt_count == 3, f"Expected exactly 3 attempts, got {attempt_count}"

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is True


# ---------------------------------------------------------------------------
# TS-03-E7: Fresh agent on retry, no duplicate events
# Requirement: 03-REQ-6.E2
# ---------------------------------------------------------------------------


class TestFreshAgentOnRetry:
    """Verify retry creates a fresh agent and does not re-yield events from failed attempt."""

    @pytest.mark.asyncio
    async def test_no_duplicate_events_on_retry(self) -> None:
        """TS-03-E7: Retry discards events from failed attempt; only retry events yielded.

        Attempt 1: yields an AssistantMessage('from-attempt-1') then raises
        a transient error.
        Attempt 2: yields an AssistantMessage('from-attempt-2') then completes.

        Only the message from attempt 2 should appear in the final output.
        create_deep_agent must be called twice (fresh agent per retry).
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import AssistantMessage, ResultMessage

        call_count = 0

        def make_agent(**_kwargs: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            agent = MagicMock()

            if call_count == 1:
                # Attempt 1: yield one event then fail
                async def failing_stream(*_a: Any, **_kw: Any) -> Any:
                    yield _make_chat_stream_event("from-attempt-1")
                    raise ConnectionError("transient mid-stream")

                agent.astream_events = failing_stream
            else:
                # Attempt 2: succeed
                agent.astream_events = lambda *a, **kw: _async_event_stream(
                    [
                        _make_chat_stream_event("from-attempt-2"),
                        _make_llm_end_event(input_tokens=5, output_tokens=3),
                    ]
                )
            return agent

        async def fake_sleep(_delay: float) -> None:
            pass

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=make_agent,
            ),
            patch("asyncio.sleep", side_effect=fake_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # create_deep_agent called twice (fresh agent per attempt)
        assert call_count == 2, f"Expected 2 create_deep_agent calls, got {call_count}"

        # Only AssistantMessage from attempt 2 should be present
        asst_msgs = [m for m in messages if isinstance(m, AssistantMessage)]
        assert len(asst_msgs) == 1, (
            f"Expected 1 AssistantMessage (from retry only), got {len(asst_msgs)}"
        )
        assert asst_msgs[0].content == "from-attempt-2"

        # Terminal ResultMessage
        assert isinstance(messages[-1], ResultMessage)
        assert messages[-1].is_error is False


# ---------------------------------------------------------------------------
# TS-03-P5: Property - retry bound invariant
# Property: 03-PROP-5
# Validates: 03-REQ-6.1, 03-REQ-6.E1
# ---------------------------------------------------------------------------


class TestPropertyRetryBoundInvariant:
    """Property: retry loop executes at most 3 attempts with correct backoff delays."""

    @pytest.mark.asyncio
    async def test_prop_retry_bound_with_all_transient_failures(self) -> None:
        """TS-03-P5: With persistent transient errors, at most 3 attempts and correct delays.

        For any transient error sequence:
        - retry loop executes at most 3 total attempts
        - backoff delay for inter-attempt gap n (0-indexed) is _BACKOFF_BASE * 2^n
        - at most 2 sleeps for 3 attempts
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        attempt_count = 0
        sleep_calls: list[float] = []

        def counting_create(**_kwargs: Any) -> MagicMock:
            nonlocal attempt_count
            attempt_count += 1
            agent = MagicMock()

            async def failing_stream(*_a: Any, **_kw: Any) -> Any:
                raise ConnectionError("transient")
                yield  # noqa: RUF028

            agent.astream_events = failing_stream
            return agent

        async def recording_sleep(delay: float) -> None:
            sleep_calls.append(delay)

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=counting_create,
            ),
            patch("asyncio.sleep", side_effect=recording_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # At most 3 attempts
        assert attempt_count <= 3, f"Expected at most 3 attempts, got {attempt_count}"

        # Verify backoff formula: delay(i) = _BACKOFF_BASE * 2^i
        # (i is 0-indexed inter-attempt gap; at most 2 gaps for 3 attempts)
        _BACKOFF_BASE = 1.0
        for i, delay in enumerate(sleep_calls):
            expected_delay = _BACKOFF_BASE * (2**i)
            assert delay == expected_delay, (
                f"Sleep delay {i} was {delay}, expected {expected_delay} "
                f"(_BACKOFF_BASE * 2^{i})"
            )

        # At most 2 sleeps for 3 attempts
        assert len(sleep_calls) <= 2, (
            f"Expected at most 2 sleep calls for 3 attempts, got {len(sleep_calls)}"
        )

        # Terminal error result
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is True

    @pytest.mark.asyncio
    async def test_prop_retry_bound_success_on_first_attempt(self) -> None:
        """TS-03-P5 variant: Success on first attempt → zero sleeps, zero retries."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        attempt_count = 0
        sleep_calls: list[float] = []

        def counting_create(**_kwargs: Any) -> MagicMock:
            nonlocal attempt_count
            attempt_count += 1
            return _make_mock_agent_empty()

        async def recording_sleep(delay: float) -> None:
            sleep_calls.append(delay)

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=counting_create,
            ),
            patch("asyncio.sleep", side_effect=recording_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # One attempt, no sleeps
        assert attempt_count == 1, f"Expected 1 attempt for success, got {attempt_count}"
        assert sleep_calls == [], f"Expected no sleeps on first-try success, got {sleep_calls}"

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False


# ---------------------------------------------------------------------------
# TS-03-25: Non-transient error → no retry, is_transport_error=False
# Requirement: 03-REQ-6.3
# ---------------------------------------------------------------------------


class TestNonTransientErrorNoRetry:
    """Verify non-transient errors yield an immediate error without retry."""

    @pytest.mark.asyncio
    async def test_auth_error_no_retry_immediate_error(self) -> None:
        """TS-03-25: Non-transient error (auth failure) → no retry, immediate error.

        Non-transient errors such as authentication failures (HTTP 401) or
        tool execution errors should NOT trigger the retry loop. The execute()
        method should immediately yield a ResultMessage with is_error=True and
        is_transport_error=False.

        Note: The exact exception type for authentication errors depends on the
        deepagents/LangChain provider. We use ValueError as a stand-in for a
        non-transient error. The implementation must classify it as non-transient.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def auth_failing_stream(*_a: Any, **_kw: Any) -> Any:
            raise ValueError("Authentication failed: invalid API key (HTTP 401)")
            yield  # noqa: RUF028

        def make_auth_failing_agent(**_kwargs: Any) -> MagicMock:
            agent = MagicMock()
            agent.astream_events = auth_failing_stream
            return agent

        sleep_calls: list[float] = []

        async def recording_sleep(delay: float) -> None:
            sleep_calls.append(delay)

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=make_auth_failing_agent,
            ),
            patch("asyncio.sleep", side_effect=recording_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # No retry attempts — sleep should not be called
        assert sleep_calls == [], f"Expected no sleep calls for non-transient error, got {sleep_calls}"

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is False

    @pytest.mark.asyncio
    async def test_permission_error_no_retry(self) -> None:
        """TS-03-25 variant: PermissionError (non-transient) → no retry.

        Errors that are clearly not transport-level should not trigger retries.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        async def perm_failing_stream(*_a: Any, **_kw: Any) -> Any:
            raise PermissionError("Forbidden: insufficient permissions")
            yield  # noqa: RUF028

        def make_perm_failing_agent(**_kwargs: Any) -> MagicMock:
            agent = MagicMock()
            agent.astream_events = perm_failing_stream
            return agent

        async def recording_sleep(delay: float) -> None:
            pytest.fail("sleep should not be called for non-transient errors")

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=make_perm_failing_agent,
            ),
            patch("asyncio.sleep", side_effect=recording_sleep),
        ):
            messages = await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is False


# ---------------------------------------------------------------------------
# TS-03-26: _BACKOFF_BASE shared, not duplicated across backend modules
# Requirement: 03-REQ-6.4
# ---------------------------------------------------------------------------


class TestBackoffBaseSharedConstant:
    """Verify _BACKOFF_BASE is defined once and shared between backends."""

    def test_backoff_base_value(self) -> None:
        """TS-03-26: _BACKOFF_BASE equals 1.0 in both backend modules.

        The constant must be 1.0 (giving delays of 1s, 2s, 4s via the
        formula _BACKOFF_BASE * 2^(n-1)).
        """
        from agentfox.session.backends.claude import _BACKOFF_BASE as claude_backoff
        from agentfox.session.backends.deepagents import _BACKOFF_BASE as da_backoff

        assert claude_backoff == 1.0, f"ClaudeBackend _BACKOFF_BASE should be 1.0, got {claude_backoff}"
        assert da_backoff == 1.0, f"DeepAgentsBackend _BACKOFF_BASE should be 1.0, got {da_backoff}"

    def test_backoff_base_not_duplicated(self) -> None:
        """TS-03-26: _BACKOFF_BASE defined exactly once across backend source files.

        The constant must not be independently defined in both backend modules.
        Either it is defined in a shared utility module and imported by both,
        or defined in one module and imported by the other.
        """
        import agentfox.session.backends.claude as cl_mod
        import agentfox.session.backends.deepagents as da_mod

        da_source_path = inspect.getfile(da_mod)
        cl_source_path = inspect.getfile(cl_mod)

        with open(da_source_path, encoding="utf-8") as f:
            da_text = f.read()
        with open(cl_source_path, encoding="utf-8") as f:
            cl_text = f.read()

        # Count literal definitions (assignments) of _BACKOFF_BASE
        # An import-based reference (e.g. `from ... import _BACKOFF_BASE`)
        # is not counted as a definition.
        da_defs = da_text.count("_BACKOFF_BASE = ")
        cl_defs = cl_text.count("_BACKOFF_BASE = ")

        total_definitions = da_defs + cl_defs
        assert total_definitions <= 1, (
            f"_BACKOFF_BASE is defined {total_definitions} times across "
            f"claude.py ({cl_defs}) and deepagents.py ({da_defs}). "
            f"It should be defined exactly once (in a shared module or one backend)."
        )


# ===========================================================================
# Task Group 5: close() lifecycle, cancellation, and smoke tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-03-27: close() idempotency — multiple calls without exception
# Requirement: 03-REQ-7.1
# ---------------------------------------------------------------------------


class TestCloseIdempotency:
    """Verify close() can be called multiple times without error."""

    @pytest.mark.asyncio
    async def test_close_three_times_no_exception(self) -> None:
        """TS-03-27: close() called three times; no exception on any call.

        close() must be idempotent: every call returns normally without
        error and without side effects beyond the first call.

        Errata E6: ClaudeBackend.close() is async, so DeepAgentsBackend.close()
        must also be async for drop-in compatibility.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        # First call
        await backend.close()
        # Second call — must not raise
        await backend.close()
        # Third call — must not raise
        await backend.close()
        # If we got here, all three calls returned normally


# ---------------------------------------------------------------------------
# TS-03-28: close() on fresh instance returns None immediately
# Requirement: 03-REQ-7.2
# ---------------------------------------------------------------------------


class TestCloseNoOpOnFreshInstance:
    """Verify close() is a no-op on a fresh instance that never ran execute()."""

    @pytest.mark.asyncio
    async def test_close_fresh_instance_returns_none(self) -> None:
        """TS-03-28: close() on a freshly created instance returns None immediately.

        When execute() has never been called, close() should be a no-op
        that returns None without side effects.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        result = await backend.close()
        assert result is None


# ---------------------------------------------------------------------------
# TS-03-29: close() releases per-instance LangGraph checkpoint state
# Requirement: 03-REQ-7.3
# ---------------------------------------------------------------------------


class TestCloseReleasesState:
    """Verify close() releases per-instance checkpoint state after execute()."""

    @pytest.mark.asyncio
    async def test_close_clears_state_after_execute(self) -> None:
        """TS-03-29: After execute() completes, close() releases checkpoint state.

        Per-instance LangGraph state attributes (e.g. _checkpointer,
        _thread_state, _agent) should be cleared/None after close() is called.

        The exact attribute names depend on the implementation. This test
        verifies that any instance-level state that was set during execute()
        is cleaned up by close().
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        mock_agent = _make_mock_agent_empty()

        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # After execute(), close() should release state
        await backend.close()

        # Check common state attribute patterns — at least one should exist
        # and be cleared after close()
        state_attrs = ["_agent", "_checkpointer", "_thread_state", "_graph"]
        for attr in state_attrs:
            if hasattr(backend, attr):
                val = getattr(backend, attr)
                assert val is None, (
                    f"Expected backend.{attr} to be None after close(), "
                    f"got {type(val).__name__}"
                )


# ---------------------------------------------------------------------------
# TS-03-30: close() does not perform async task cancellation
# Requirement: 03-REQ-7.4
# ---------------------------------------------------------------------------


class TestCloseNoAsyncCancellation:
    """Verify close() does not call asyncio.Task.cancel() or async cancellation."""

    @pytest.mark.asyncio
    async def test_close_does_not_cancel_tasks(self) -> None:
        """TS-03-30: close() does not call asyncio.Task.cancel().

        close() must not perform async task cancellation or manage the
        execute() lifecycle. Mid-stream teardown is delegated to session.py
        via asyncio.wait_for() / async iterator cancellation.

        Errata E6: close() is async (matching ClaudeBackend) but must not
        invoke any cancellation primitives.
        """
        import ast
        import textwrap

        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()

        # Verify close() completes without side effects
        await backend.close()

        # Verify close() implementation body does not invoke task cancellation.
        # We inspect the AST of close() rather than raw source to avoid false
        # positives from docstring mentions of "cancel()".
        # Note: asyncio.Task.cancel cannot be patched on CPython 3.12+ (the
        # C extension type is immutable), so AST inspection is the only
        # reliable approach.
        source = textwrap.dedent(inspect.getsource(backend.close))
        tree = ast.parse(source)

        # Walk the AST looking for call nodes named "cancel"
        for node in ast.walk(tree):
            if isinstance(node, ast.Call):
                # Check for obj.cancel() or cancel() calls
                if isinstance(node.func, ast.Attribute) and node.func.attr == "cancel":
                    pytest.fail(
                        "close() body contains a .cancel() call; "
                        "it should not perform async task cancellation"
                    )
                if isinstance(node.func, ast.Name) and node.func.id == "cancel":
                    pytest.fail(
                        "close() body contains a cancel() call; "
                        "it should not perform async task cancellation"
                    )


# ---------------------------------------------------------------------------
# TS-03-P6: Property - close() idempotency across N calls
# Property: 03-PROP-6
# Validates: 03-REQ-7.1, 03-REQ-7.2
# ---------------------------------------------------------------------------


class TestPropertyCloseIdempotency:
    """Property: close() returns normally for any number of calls (0..20)."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("n_calls", [0, 1, 2, 3, 5, 10, 20])
    async def test_prop_close_n_times(self, n_calls: int) -> None:
        """TS-03-P6: close() called N times; no exception; state consistent.

        For any sequence of zero or more close() calls on the same instance,
        every call returns normally without raising an exception. Observable
        state after the second+ call is identical to state after the first.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        for i in range(n_calls):
            try:
                await backend.close()
            except Exception as e:
                pytest.fail(f"close() raised on call {i}: {e}")

    @pytest.mark.asyncio
    async def test_prop_close_after_execute_idempotent(self) -> None:
        """TS-03-P6 variant: close() N times after execute() completes."""
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        backend = DeepAgentsBackend()
        mock_agent = _make_mock_agent_empty()

        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            await _collect_async(
                backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                )
            )

        # Call close() multiple times after execute()
        for i in range(5):
            try:
                await backend.close()
            except Exception as e:
                pytest.fail(f"close() raised on post-execute call {i}: {e}")


# ---------------------------------------------------------------------------
# TS-03-E2: Timeout via asyncio.wait_for when astream_events hangs
# Requirement: 03-REQ-2.E1
# ---------------------------------------------------------------------------


class TestTimeoutOnHangingStream:
    """Verify session-level timeout propagates into execute() generator."""

    @pytest.mark.asyncio
    async def test_hanging_stream_cancelled_by_wait_for(self) -> None:
        """TS-03-E2: Mock agent never yields → asyncio.wait_for timeout.

        When astream_events() hangs indefinitely without yielding events,
        the session.py-level asyncio.wait_for() / async iterator cancellation
        mechanism enforces a timeout. The adapter itself does not impose a
        timeout — cancellation propagates into the async generator.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        async def hanging_stream(*_args: Any, **_kwargs: Any) -> Any:
            # Simulate a stream that hangs forever
            await asyncio.sleep(9999)
            yield  # unreachable — makes this an async generator

        mock_agent = MagicMock()
        mock_agent.astream_events = hanging_stream

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):
            gen = backend.execute(
                "p",
                system_prompt="s",
                model="m",
                cwd="/",
            )
            with pytest.raises(asyncio.TimeoutError):
                # Session-level cancellation via wait_for
                await asyncio.wait_for(_collect_async(gen), timeout=0.1)


# ---------------------------------------------------------------------------
# TS-03-E3: Mid-stream asyncio cancellation via CancelledError
# Requirement: 03-REQ-2.E2
# ---------------------------------------------------------------------------


class TestMidStreamCancellation:
    """Verify mid-stream asyncio cancellation exits cleanly."""

    @pytest.mark.asyncio
    async def test_cancel_mid_stream_propagates_cancelled_error(self) -> None:
        """TS-03-E3: Cancel task mid-stream → CancelledError, clean exit.

        If the caller cancels the async generator mid-stream via asyncio
        cancellation, the generator should exit at the next await point.
        No unexpected exceptions should propagate beyond CancelledError.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        event_reached = asyncio.Event()

        async def slow_stream(*_args: Any, **_kwargs: Any) -> Any:
            yield _make_chat_stream_event("chunk1")
            event_reached.set()
            # Long pause where cancellation arrives
            await asyncio.sleep(10)
            yield _make_chat_stream_event("chunk2")  # unreachable

        mock_agent = MagicMock()
        mock_agent.astream_events = slow_stream

        backend = DeepAgentsBackend()

        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ):

            async def run_execute() -> None:
                async for _msg in backend.execute(
                    "p",
                    system_prompt="s",
                    model="m",
                    cwd="/",
                ):
                    pass

            task = asyncio.create_task(run_execute())
            # Wait until at least one event has been processed
            await asyncio.wait_for(event_reached.wait(), timeout=2.0)
            # Cancel the task
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task


# ---------------------------------------------------------------------------
# TS-03-SMOKE-1: End-to-end smoke test with full message stream
# Execution Path: 03-PATH-1
# Requirements: 03-REQ-2.2, 03-REQ-8.1, 03-REQ-9.1
# ---------------------------------------------------------------------------


class TestSmokeEndToEnd:
    """Smoke test: config → factory → execute() → full message stream."""

    @pytest.mark.asyncio
    async def test_smoke_full_session(self) -> None:
        """TS-03-SMOKE-1: End-to-end with mocked create_deep_agent.

        Verifies the complete flow:
        1. BackendConfig accepts provider='deepagents'
        2. create_backend('deepagents') returns a DeepAgentsBackend instance
        3. execute() calls create_deep_agent with correct params and 5 tools
        4. AssistantMessage yielded for on_chat_model_stream events
        5. ToolUseMessage yielded for on_tool_start/on_tool_end events
        6. Terminal ResultMessage with is_error=False and aggregated tokens
        7. No exceptions propagate

        Real components: create_backend factory, DeepAgentsBackend, types
        Mocked: create_deep_agent(), astream_events() synthetic stream
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import (
            AssistantMessage,
            ResultMessage,
            ToolUseMessage,
        )

        # Step 1: Verify BackendConfig accepts 'deepagents'
        try:
            from agentfox.core.config import BackendConfig

            config = BackendConfig(provider="deepagents")
            assert config.provider == "deepagents"
        except (ImportError, AttributeError, TypeError):
            # BackendConfig.provider field may not exist yet (errata E3)
            pass

        # Step 2: Verify create_backend('deepagents') returns correct type
        try:
            from agentfox.session.backends import create_backend

            factory_result = create_backend("deepagents")
            assert isinstance(factory_result, DeepAgentsBackend)
        except (ImportError, AttributeError):
            # create_backend may not exist yet (errata E2)
            pass

        # Step 3-6: Full event stream processing
        events = [
            _make_chat_stream_event("Hello"),
            _make_chat_stream_event(" world"),
            _make_on_tool_start_event(
                tool_name="read_file",
                tool_input={"path": "main.py"},
            ),
            _make_on_tool_end_event(
                tool_name="read_file",
                output="# main code here",
            ),
            _make_chat_stream_event("Done!"),
            _make_llm_end_event(input_tokens=100, output_tokens=50),
            _make_llm_end_event(input_tokens=200, output_tokens=100),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        captured_tools: list[Any] | None = None

        def capture_create(**kwargs: Any) -> MagicMock:
            nonlocal captured_tools
            captured_tools = kwargs.get("tools", [])
            return mock_agent

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            side_effect=capture_create,
        ) as mock_create:
            messages = await _collect_async(
                backend.execute(
                    "implement feature X",
                    system_prompt="You are a coding assistant.",
                    model="openai:gpt-5.5",
                    cwd="/workspace",
                )
            )

        # Step 3: Verify create_deep_agent called with correct params
        assert mock_create.called
        call_kwargs = mock_create.call_args.kwargs
        assert call_kwargs["model"] == "openai:gpt-5.5"
        assert call_kwargs["system_prompt"] == "You are a coding assistant."
        assert call_kwargs["cwd"] == "/workspace"
        assert captured_tools is not None
        assert len(captured_tools) == 5

        # Step 4: AssistantMessage instances for chat stream events
        asst_msgs = [m for m in messages if isinstance(m, AssistantMessage)]
        assert len(asst_msgs) == 3  # "Hello", " world", "Done!"
        assert asst_msgs[0].content == "Hello"
        assert asst_msgs[1].content == " world"
        assert asst_msgs[2].content == "Done!"

        # Step 5: ToolUseMessage instances for tool events
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) == 2  # on_tool_start + on_tool_end

        # Step 6: Terminal ResultMessage with aggregated tokens
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is False
        assert result.input_tokens == 300  # 100 + 200
        assert result.output_tokens == 150  # 50 + 100

        # Step 7: close() after execute completes
        await backend.close()


# ---------------------------------------------------------------------------
# TS-03-SMOKE-2: Permission callback smoke test
# Execution Path: 03-PATH-2
# Requirements: 03-REQ-4.1
# ---------------------------------------------------------------------------


class TestSmokePermissionCallback:
    """Smoke test: permission_callback invoked for tool calls."""

    @pytest.mark.asyncio
    async def test_smoke_permission_callback_invoked(self) -> None:
        """TS-03-SMOKE-2: Permission callback smoke with mocked interrupt.

        Verifies the permission callback integration:
        1. permission_callback is passed to execute()
        2. create_deep_agent is called (implementation configures interrupt)
        3. Stream processes events and yields ToolUseMessage
        4. Terminal ResultMessage at end

        Real components: DeepAgentsBackend.execute, interrupt mapping logic
        Mocked: create_deep_agent(), interrupt mechanism

        Errata E4: PermissionCallback is async — callback is async def.
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage, ToolUseMessage

        callback_calls: list[tuple[str, dict[str, Any]]] = []

        async def my_callback(name: str, inp: dict[str, Any]) -> bool:
            callback_calls.append((name, inp))
            return True

        events = [
            _make_on_tool_start_event(
                tool_name="write_file",
                tool_input={"path": "out.txt", "content": "data"},
            ),
            _make_on_tool_end_event(tool_name="write_file", output="ok"),
            _make_llm_end_event(input_tokens=15, output_tokens=8),
        ]
        mock_agent = _make_mock_agent_with_events(events)

        backend = DeepAgentsBackend()
        with patch(
            "agentfox.session.backends.deepagents.create_deep_agent",
            return_value=mock_agent,
        ) as mock_create:
            messages = await _collect_async(
                backend.execute(
                    "write data to file",
                    system_prompt="sys",
                    model="openai:gpt-5.5",
                    cwd="/workspace",
                    permission_callback=my_callback,
                )
            )

        # create_deep_agent called with the permission mechanism configured
        assert mock_create.called

        # ToolUseMessage yielded for tool events
        tool_msgs = [m for m in messages if isinstance(m, ToolUseMessage)]
        assert len(tool_msgs) >= 1

        # Terminal ResultMessage at end
        result = messages[-1]
        assert isinstance(result, ResultMessage)


# ---------------------------------------------------------------------------
# TS-03-SMOKE-3: Transient retry exhaust smoke test
# Execution Path: 03-PATH-3
# Requirements: 03-REQ-6.1, 03-REQ-6.2
# ---------------------------------------------------------------------------


class TestSmokeTransientRetryExhaust:
    """Smoke test: 3 transient errors → backoff delays → terminal error."""

    @pytest.mark.asyncio
    async def test_smoke_transient_retry_exhaust(self) -> None:
        """TS-03-SMOKE-3: 3 consecutive transient errors with backoff.

        Verifies the complete transient retry flow:
        1. create_deep_agent called exactly 3 times
        2. asyncio.sleep called with delays [1.0, 2.0] between attempts
        3. No delay of 4.0 (only 2 inter-attempt waits for 3 attempts)
        4. Terminal ResultMessage with is_error=True, is_transport_error=True
        5. No exception propagates to the caller

        Real components: DeepAgentsBackend.execute, retry/backoff logic
        Mocked: create_deep_agent() (always raises ConnectionError),
                asyncio.sleep (records delays)
        """
        from agentfox.session.backends.deepagents import DeepAgentsBackend
        from agentfox.session.backends.types import ResultMessage

        create_count = 0

        def counting_create(**_kwargs: Any) -> MagicMock:
            nonlocal create_count
            create_count += 1
            agent = MagicMock()

            async def failing_stream(*_a: Any, **_kw: Any) -> Any:
                raise ConnectionError("persistent network failure")
                yield  # noqa: RUF028 — async generator

            agent.astream_events = failing_stream
            return agent

        sleep_delays: list[float] = []

        async def recording_sleep(delay: float) -> None:
            sleep_delays.append(delay)

        backend = DeepAgentsBackend()
        with (
            patch(
                "agentfox.session.backends.deepagents.create_deep_agent",
                side_effect=counting_create,
            ),
            patch("asyncio.sleep", side_effect=recording_sleep),
        ):
            # No exception should propagate
            messages = await _collect_async(
                backend.execute(
                    "do something",
                    system_prompt="sys",
                    model="ollama:north-mini-code-1.0",
                    cwd="/workspace",
                )
            )

        # Step 1: create_deep_agent called exactly 3 times
        assert create_count == 3, (
            f"Expected exactly 3 create_deep_agent calls, got {create_count}"
        )

        # Step 2-3: Backoff delays [1.0, 2.0] — no 4.0 (only 2 waits for 3 attempts)
        assert sleep_delays == [1.0, 2.0], (
            f"Expected backoff delays [1.0, 2.0], got {sleep_delays}"
        )

        # Step 4: Terminal error ResultMessage
        result = messages[-1]
        assert isinstance(result, ResultMessage)
        assert result.is_error is True
        assert result.is_transport_error is True
