"""Tests for Backend Protocol, create_backend() factory, and SDK containment.

Extends the original canonical message type tests with Backend Protocol
isinstance checks, execute() signature inspection, create_backend() factory
tests, and SDK containment property tests.

Test Spec: TS-26-3, TS-26-4, TS-26-P1 (original)
           TS-02-1 through TS-02-11, TS-02-23 through TS-02-27 (new)
Requirements: 26-REQ-1.3, 26-REQ-1.4, 26-REQ-2.4 (original)
              02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.3, 02-REQ-1.4, 02-REQ-1.5,
              02-REQ-2.1, 02-REQ-2.2, 02-REQ-2.3, 02-REQ-2.4, 02-REQ-2.5,
              02-REQ-2.6,
              02-REQ-6.1, 02-REQ-6.2, 02-REQ-6.3, 02-REQ-6.4, 02-REQ-6.5
"""

from __future__ import annotations

import dataclasses
import glob
import inspect
import os
import sys

import pytest

# ---------------------------------------------------------------------------
# TS-26-3: Canonical message types are frozen dataclasses
# Requirement: 26-REQ-1.3
# ---------------------------------------------------------------------------


class TestCanonicalMessagesFrozen:
    """Verify ToolUseMessage, AssistantMessage, ResultMessage are frozen."""

    def test_tool_use_message_frozen(self) -> None:
        from agentfox.session.backends.types import ToolUseMessage

        tm = ToolUseMessage(tool_name="Bash", tool_input={"command": "ls"})
        assert tm.tool_name == "Bash"
        assert tm.tool_input == {"command": "ls"}
        with pytest.raises(dataclasses.FrozenInstanceError):
            tm.tool_name = "other"  # type: ignore[misc]

    def test_assistant_message_frozen(self) -> None:
        from agentfox.session.backends.types import AssistantMessage

        am = AssistantMessage(content="thinking")
        assert am.content == "thinking"
        with pytest.raises(dataclasses.FrozenInstanceError):
            am.content = "other"  # type: ignore[misc]

    def test_result_message_frozen(self) -> None:
        from agentfox.session.backends.types import ResultMessage

        rm = ResultMessage(
            status="completed",
            input_tokens=100,
            output_tokens=200,
            duration_ms=5000,
            error_message=None,
            is_error=False,
        )
        assert rm.input_tokens == 100
        with pytest.raises(dataclasses.FrozenInstanceError):
            rm.status = "failed"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-26-4: ResultMessage carries required fields
# Requirement: 26-REQ-1.4
# ---------------------------------------------------------------------------


class TestResultMessageFields:
    """Verify ResultMessage has all specified fields with correct types."""

    def test_result_message_all_fields(self) -> None:
        from agentfox.session.backends.types import ResultMessage

        rm = ResultMessage(
            status="failed",
            input_tokens=0,
            output_tokens=0,
            duration_ms=0,
            error_message="timeout",
            is_error=True,
        )
        assert rm.status == "failed"
        assert rm.is_error is True
        assert rm.error_message == "timeout"
        assert isinstance(rm.input_tokens, int)
        assert isinstance(rm.output_tokens, int)
        assert isinstance(rm.duration_ms, int)

    def test_result_message_none_error(self) -> None:
        from agentfox.session.backends.types import ResultMessage

        rm = ResultMessage(
            status="completed",
            input_tokens=50,
            output_tokens=100,
            duration_ms=3000,
            error_message=None,
            is_error=False,
        )
        assert rm.error_message is None
        assert rm.is_error is False


# ---------------------------------------------------------------------------
# TS-26-P1: Backend Protocol Isolation (Property)
# Property 1: No module outside claude backend adapter imports claude_agent_sdk
# Validates: 26-REQ-1.1, 26-REQ-2.4
# ---------------------------------------------------------------------------


class TestPropertyProtocolIsolation:
    """No module outside backends/claude.py should import claude_agent_sdk."""

    def test_prop_protocol_isolation(self) -> None:
        agent_fox_dir = os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "agent_fox")
        agent_fox_dir = os.path.normpath(agent_fox_dir)

        # The only file allowed to import claude_agent_sdk
        allowed = os.path.normpath(os.path.join(agent_fox_dir, "session", "backends", "claude.py"))

        py_files = glob.glob(os.path.join(agent_fox_dir, "**", "*.py"), recursive=True)

        violations = []
        for py_file in py_files:
            normalized = os.path.normpath(py_file)
            if normalized == allowed:
                continue
            with open(py_file, encoding="utf-8") as f:
                content = f.read()
            if "claude_agent_sdk" in content:
                violations.append(os.path.relpath(py_file, agent_fox_dir))

        assert violations == [], f"Files outside backends/claude.py import claude_agent_sdk: {violations}"


# ===========================================================================
# Spec 02: Backend Protocol Tests
# ===========================================================================


# ---------------------------------------------------------------------------
# SDK_CONTAINMENT mapping for containment property tests
# Requirement: 02-REQ-6.3
# ---------------------------------------------------------------------------

# Maps SDK name strings to their designated backend file.
# Adding a new backend requires only a one-line addition to this mapping.
SDK_CONTAINMENT: dict[str, str] = {
    "claude_agent_sdk": "claude.py",
    # Future backends (add one line per backend):
    # "deepagents": "deepagents.py",       # spec 03
    # "google_adk": "google_adk.py",       # spec 04
}


# ---------------------------------------------------------------------------
# TS-02-1: Backend is a runtime-checkable Protocol; ClaudeBackend satisfies it
# Requirement: 02-REQ-1.1, 02-REQ-6.1
# ---------------------------------------------------------------------------


class TestBackendProtocolIsinstance:
    """Verify Backend Protocol and ClaudeBackend isinstance check."""

    def test_isinstance_claude_backend_is_backend(self) -> None:
        """TS-02-1: isinstance(ClaudeBackend(), Backend) returns True."""
        from agentfox.session.backends import Backend, ClaudeBackend

        backend = ClaudeBackend()
        assert isinstance(backend, Backend) is True

    def test_backend_is_runtime_checkable(self) -> None:
        """TS-02-1: Backend has __protocol_attrs__ or equivalent runtime marker."""
        from agentfox.session.backends import Backend

        # runtime_checkable Protocols have _is_runtime_protocol set to True
        assert getattr(Backend, "_is_runtime_protocol", False) is True

    def test_backend_importable_from_session_backends(self) -> None:
        """TS-02-1: Backend is importable from agentfox.session.backends."""
        from agentfox.session.backends import Backend

        assert Backend is not None


# ---------------------------------------------------------------------------
# TS-02-2: Backend.execute() signature inspection
# Requirement: 02-REQ-1.2
# ---------------------------------------------------------------------------


class TestBackendExecuteSignature:
    """Verify Backend.execute() has the exact parameter signature."""

    def test_execute_signature_params(self) -> None:
        """TS-02-2: execute() has correct positional and keyword-only params."""
        from agentfox.session.backends.protocol import Backend

        sig = inspect.signature(Backend.execute)
        params = sig.parameters

        # prompt is positional
        assert "prompt" in params
        assert params["prompt"].kind in (
            inspect.Parameter.POSITIONAL_ONLY,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
        )

        # All keyword-only params with correct defaults
        kw_only = inspect.Parameter.KEYWORD_ONLY
        assert params["system_prompt"].kind == kw_only
        assert params["model"].kind == kw_only
        assert params["cwd"].kind == kw_only

        assert params["permission_callback"].kind == kw_only
        assert params["permission_callback"].default is None

        assert params["activity_callback"].kind == kw_only
        assert params["activity_callback"].default is None

        assert params["tool_error_callback"].kind == kw_only
        assert params["tool_error_callback"].default is None

        assert params["node_id"].kind == kw_only
        assert params["node_id"].default == ""

        assert params["archetype"].kind == kw_only
        assert params["archetype"].default is None

        assert params["max_turns"].kind == kw_only
        assert params["max_turns"].default is None

        assert params["max_budget_usd"].kind == kw_only
        assert params["max_budget_usd"].default is None

        assert params["thinking"].kind == kw_only
        assert params["thinking"].default is None

        assert params["effort"].kind == kw_only
        assert params["effort"].default is None

        assert params["compaction"].kind == kw_only
        assert params["compaction"].default is False

    def test_execute_return_annotation(self) -> None:
        """TS-02-2: execute() return annotation is AsyncIterator[AgentMessage]."""
        from agentfox.session.backends.protocol import Backend

        sig = inspect.signature(Backend.execute)
        ret = sig.return_annotation
        # The return annotation should reference AsyncIterator and AgentMessage
        ret_str = str(ret)
        assert "AsyncIterator" in ret_str
        assert "AgentMessage" in ret_str


# ---------------------------------------------------------------------------
# TS-02-3: Backend.close() is async, returns None, and is idempotent
# Requirement: 02-REQ-1.3
# ---------------------------------------------------------------------------


class TestBackendCloseIdempotent:
    """Verify close() is idempotent on ClaudeBackend."""

    @pytest.mark.asyncio
    async def test_close_idempotent_three_calls(self) -> None:
        """TS-02-3: Calling close() three times does not raise."""
        from agentfox.session.backends import ClaudeBackend

        backend = ClaudeBackend()
        result1 = await backend.close()
        result2 = await backend.close()
        result3 = await backend.close()
        assert result1 is None
        assert result2 is None
        assert result3 is None


# ---------------------------------------------------------------------------
# TS-02-4: Backend.name property returns non-empty str
# Requirement: 02-REQ-1.4
# ---------------------------------------------------------------------------


class TestBackendNameProperty:
    """Verify ClaudeBackend.name returns 'claude'."""

    def test_name_returns_claude(self) -> None:
        """TS-02-4: ClaudeBackend().name returns 'claude'."""
        from agentfox.session.backends import ClaudeBackend

        backend = ClaudeBackend()
        assert isinstance(backend.name, str)
        assert len(backend.name) > 0
        assert backend.name == "claude"


# ---------------------------------------------------------------------------
# TS-02-5: Importing protocol.py does not import claude_agent_sdk
# Requirement: 02-REQ-1.5
# ---------------------------------------------------------------------------


class TestProtocolImportIsolation:
    """Verify importing protocol does not trigger SDK imports."""

    def test_protocol_import_does_not_load_sdk(self) -> None:
        """TS-02-5: Importing protocol.py doesn't load claude_agent_sdk."""
        modules_before = set(sys.modules.keys())
        import agentfox.session.backends.protocol  # noqa: F401

        modules_after = set(sys.modules.keys())
        new_modules = modules_after - modules_before
        assert "claude_agent_sdk" not in new_modules
        assert "agentfox.session.backends.claude" not in new_modules

        # Verify Backend is importable from the protocol module
        from agentfox.session.backends.protocol import Backend

        assert Backend is not None


# ---------------------------------------------------------------------------
# TS-02-23: test_protocol.py asserts isinstance(ClaudeBackend(), Backend) True
# Requirement: 02-REQ-6.1
# ---------------------------------------------------------------------------


class TestProtocolInstanceofDirect:
    """Directly verify isinstance assertion (redundant with TS-02-1, CI-required)."""

    def test_isinstance_direct(self) -> None:
        """TS-02-23: isinstance(ClaudeBackend(), Backend) returns True."""
        from agentfox.session.backends import Backend, ClaudeBackend

        assert isinstance(ClaudeBackend(), Backend) is True


# ---------------------------------------------------------------------------
# TS-02-6: create_backend('claude') returns a Backend instance
# Requirement: 02-REQ-2.1
# ---------------------------------------------------------------------------


class TestCreateBackendHappyPath:
    """Verify create_backend('claude') returns a valid Backend."""

    def test_create_backend_claude(self) -> None:
        """TS-02-6: create_backend('claude') returns Backend with name 'claude'."""
        from agentfox.session.backends import Backend, create_backend

        result = create_backend("claude")
        assert isinstance(result, Backend) is True
        assert result.name == "claude"


# ---------------------------------------------------------------------------
# TS-02-7: create_backend signature: def create_backend(name: str) -> Backend
# Requirement: 02-REQ-2.2
# ---------------------------------------------------------------------------


class TestCreateBackendSignature:
    """Verify create_backend has correct signature."""

    def test_signature_name_str_returns_backend(self) -> None:
        """TS-02-7: Single param `name: str`, return annotation Backend."""
        from agentfox.session.backends import Backend, create_backend

        sig = inspect.signature(create_backend)
        params = sig.parameters
        assert list(params.keys()) == ["name"]
        assert params["name"].annotation is str
        assert sig.return_annotation is Backend


# ---------------------------------------------------------------------------
# TS-02-8: Lazy import isolation — claude_agent_sdk not loaded until factory call
# Requirement: 02-REQ-2.3
# ---------------------------------------------------------------------------


class TestLazyImportIsolation:
    """Verify SDK is not loaded until create_backend() is called."""

    def test_importing_backends_does_not_load_sdk(self) -> None:
        """TS-02-8: Importing agentfox.session.backends doesn't load SDK."""
        import subprocess

        # Run in a subprocess for clean module state
        result = subprocess.run(
            [
                sys.executable,
                "-c",
                (
                    "import sys; "
                    "import agentfox.session.backends; "
                    "assert 'claude_agent_sdk' not in sys.modules, "
                    "'claude_agent_sdk loaded before create_backend()'"
                ),
            ],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0, (
            f"Lazy import isolation failed:\nstdout: {result.stdout}\nstderr: {result.stderr}"
        )


# ---------------------------------------------------------------------------
# TS-02-9: create_backend('foo') raises ConfigError
# Requirement: 02-REQ-2.4
# ---------------------------------------------------------------------------


class TestCreateBackendUnknownName:
    """Verify unknown backend name raises ConfigError."""

    def test_unknown_name_raises_config_error(self) -> None:
        """TS-02-9: create_backend('foo') raises ConfigError with details."""
        from agentfox.core.errors import ConfigError
        from agentfox.session.backends import create_backend

        with pytest.raises(ConfigError) as exc_info:
            create_backend("foo")
        error_msg = str(exc_info.value)
        assert "foo" in error_msg
        assert "claude" in error_msg


# ---------------------------------------------------------------------------
# TS-02-10: Missing SDK raises ConfigError with pip install hint
# Requirement: 02-REQ-2.5
# ---------------------------------------------------------------------------


class TestCreateBackendMissingSdk:
    """Verify missing SDK raises ConfigError with install hint."""

    def test_missing_sdk_raises_config_error_with_hint(self) -> None:
        """TS-02-10: ImportError on SDK raises ConfigError with pip install hint."""
        from unittest.mock import patch as mock_patch

        from agentfox.core.errors import ConfigError
        from agentfox.session.backends import create_backend

        # Store the original __import__ for delegation
        original_import = __builtins__.__import__ if hasattr(__builtins__, "__import__") else __import__

        def raise_import_error(name: str, *args: object, **kwargs: object) -> object:
            if "claude" in name and ("claude_agent_sdk" in name or "backends.claude" in name):
                raise ImportError(f"No module named '{name}'")
            return original_import(name, *args, **kwargs)

        with mock_patch("builtins.__import__", side_effect=raise_import_error):
            with pytest.raises(ConfigError) as exc_info:
                create_backend("claude")
            error_msg = str(exc_info.value)
            assert "pip install" in error_msg or "Install" in error_msg
            assert "claude-agent-sdk" in error_msg


# ---------------------------------------------------------------------------
# TS-02-11: create_backend does not fallback — propagates ConfigError immediately
# Requirement: 02-REQ-2.6
# ---------------------------------------------------------------------------


class TestCreateBackendNoFallback:
    """Verify create_backend does not attempt fallback on error."""

    def test_no_fallback_on_unknown_name(self) -> None:
        """TS-02-11: ConfigError raised immediately, no alternative backend."""
        from agentfox.core.errors import ConfigError
        from agentfox.session.backends import create_backend

        with pytest.raises(ConfigError):
            create_backend("nonexistent")
        # If we get here, ConfigError was raised — no fallback occurred


# ---------------------------------------------------------------------------
# TS-02-24: Containment property test — SDK names only in designated files
# Requirement: 02-REQ-6.2
# ---------------------------------------------------------------------------


class TestSdkContainmentProperty:
    """Verify SDK name strings appear only in designated backend files."""

    def test_sdk_containment_scan(self) -> None:
        """TS-02-24: No non-designated file contains SDK name substrings."""
        agent_fox_dir = os.path.join(
            os.path.dirname(__file__), "..", "..", "..", "..", "agentfox",
        )
        agent_fox_dir = os.path.normpath(agent_fox_dir)
        assert os.path.isdir(agent_fox_dir), (
            f"Production source directory not found: {agent_fox_dir}"
        )

        all_files = glob.glob(
            os.path.join(agent_fox_dir, "**", "*.py"), recursive=True,
        )
        assert len(all_files) > 0, f"No Python files found in {agent_fox_dir}"

        for sdk_name, allowed_filename in SDK_CONTAINMENT.items():
            for filepath in all_files:
                if os.path.basename(filepath) == allowed_filename:
                    continue
                with open(filepath, encoding="utf-8") as f:
                    contents = f.read()
                assert sdk_name not in contents, (
                    f'SDK "{sdk_name}" found in non-designated file: {filepath}'
                )


# ---------------------------------------------------------------------------
# TS-02-25: SDK_CONTAINMENT structure and future-backend comments
# Requirement: 02-REQ-6.3
# ---------------------------------------------------------------------------


class TestSdkContainmentStructure:
    """Verify SDK_CONTAINMENT dict structure and placeholder comments."""

    def test_sdk_containment_has_claude(self) -> None:
        """TS-02-25: SDK_CONTAINMENT has 'claude_agent_sdk' -> 'claude.py'."""
        assert "claude_agent_sdk" in SDK_CONTAINMENT
        assert SDK_CONTAINMENT["claude_agent_sdk"] == "claude.py"

    def test_placeholder_comments_exist(self) -> None:
        """TS-02-25: Source contains placeholder comments for future backends."""
        with open(__file__, encoding="utf-8") as f:
            src = f.read()
        assert "SDK_CONTAINMENT" in src
        assert "claude_agent_sdk" in src
        assert "claude.py" in src
        # Check for future backend placeholder comments
        assert "deepagents" in src
        assert "google" in src


# ---------------------------------------------------------------------------
# TS-02-26: Containment test glob does not reach tests/ directory
# Requirement: 02-REQ-6.4
# ---------------------------------------------------------------------------


class TestContainmentGlobScope:
    """Verify glob targets only production source, not test files."""

    def test_glob_excludes_tests_directory(self) -> None:
        """TS-02-26: No file path under packages/agentfox/tests/."""
        agent_fox_dir = os.path.join(
            os.path.dirname(__file__), "..", "..", "..", "..", "agentfox",
        )
        agent_fox_dir = os.path.normpath(agent_fox_dir)

        all_files = glob.glob(
            os.path.join(agent_fox_dir, "**", "*.py"), recursive=True,
        )
        for filepath in all_files:
            abs_path = os.path.abspath(filepath)
            assert os.sep + "tests" + os.sep not in abs_path, (
                f"Test file incorrectly included in containment scan: {filepath}"
            )


# ---------------------------------------------------------------------------
# TS-02-27: Protocol tests run as required CI checks
# Requirement: 02-REQ-6.5
# ---------------------------------------------------------------------------


class TestProtocolTestsRunnable:
    """Verify the protocol test file is runnable by pytest."""

    def test_protocol_tests_pass(self) -> None:
        """TS-02-27: pytest on this file exits with code 0."""
        import subprocess

        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "pytest",
                __file__,
                "-v",
                "--tb=short",
                "-x",
            ],
            capture_output=True,
            text=True,
            timeout=120,
        )
        assert result.returncode == 0, (
            f"Protocol tests failed:\nstdout: {result.stdout}\nstderr: {result.stderr}"
        )
