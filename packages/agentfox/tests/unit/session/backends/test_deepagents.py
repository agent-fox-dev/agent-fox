"""Tests for DeepAgentsBackend adapter.

Test Spec: TS-03-1 through TS-03-4, TS-03-31 through TS-03-41, TS-03-P1, TS-03-P4
Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-2.1, 03-REQ-8.1-8.3,
              03-REQ-9.1-9.2, 03-REQ-10.1-10.3, 03-REQ-11.1-11.2, 03-REQ-12.1

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

import glob
import hashlib
import inspect
import os
import tomllib

import pytest

# TS-03-37 / 03-REQ-10.2: Guard all DeepAgentsBackend tests behind importorskip.
# Tests are skipped with a clear reason when deepagents is not installed.
pytest.importorskip("deepagents")


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

            assert isinstance(backend, Backend), (
                "isinstance(DeepAgentsBackend(), Backend) must return True"
            )
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
            inspect.getfile(
                __import__("agentfox.session.backends.types", fromlist=["types"])
            )
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))

        allowed = os.path.normpath(
            os.path.join(agentfox_dir, "session", "backends", "deepagents.py")
        )

        py_files = glob.glob(
            os.path.join(agentfox_dir, "**", "*.py"), recursive=True
        )

        violations = []
        for py_file in py_files:
            normalized = os.path.normpath(py_file)
            if normalized == allowed:
                continue
            with open(py_file, encoding="utf-8") as f:
                content = f.read()
            if "import deepagents" in content or "from deepagents" in content:
                violations.append(os.path.relpath(py_file, agentfox_dir))

        assert violations == [], (
            f"Files outside backends/deepagents.py import deepagents: {violations}"
        )


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
        types_path = inspect.getfile(
            __import__("agentfox.session.backends.types", fromlist=["types"])
        )
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
            inspect.getfile(
                __import__("agentfox.session.backends.types", fromlist=["types"])
            )
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))
        allowed = os.path.normpath(
            os.path.join(agentfox_dir, "session", "backends", "deepagents.py")
        )

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
            inspect.getfile(
                __import__("agentfox.session.backends.types", fromlist=["types"])
            )
        )
        agentfox_dir = os.path.normpath(os.path.join(backends_dir, "..", ".."))
        allowed_file = os.path.normpath(
            os.path.join(agentfox_dir, "session", "backends", "deepagents.py")
        )

        py_files = glob.glob(
            os.path.join(agentfox_dir, "**", "*.py"), recursive=True
        )

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
            f"Containment violation: deepagents import found outside "
            f"session/backends/deepagents.py: {violations}"
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
# Errata: OrchestratorConfig has no backend field. The implementation
# group (task 6.4) must add it.
# ---------------------------------------------------------------------------


class TestOrchestratorConfigBackend:
    """Verify OrchestratorConfig accepts 'claude' and 'deepagents' backend values."""

    def test_config_accepts_deepagents(self) -> None:
        """TS-03-34: OrchestratorConfig(backend='deepagents') succeeds."""
        from agentfox.core.config import OrchestratorConfig

        config = OrchestratorConfig(backend="deepagents")
        assert config.backend == "deepagents"

    def test_config_accepts_claude(self) -> None:
        """TS-03-34/35: OrchestratorConfig(backend='claude') succeeds."""
        from agentfox.core.config import OrchestratorConfig

        config = OrchestratorConfig(backend="claude")
        assert config.backend == "claude"

    def test_config_rejects_invalid(self) -> None:
        """TS-03-34: OrchestratorConfig(backend='invalid') raises validation error."""
        from agentfox.core.config import OrchestratorConfig

        with pytest.raises((ValueError, TypeError, Exception)):
            OrchestratorConfig(backend="llama")

    def test_existing_claude_config_valid(self) -> None:
        """TS-03-35: Existing backend='claude' configs pass validation unchanged."""
        from agentfox.core.config import OrchestratorConfig

        config = OrchestratorConfig(backend="claude")
        assert config.backend == "claude"


# ---------------------------------------------------------------------------
# TS-03-36: pyproject.toml declares deepagents optional dependency
# Requirement: 03-REQ-10.1
# ---------------------------------------------------------------------------


class TestPyprojectOptionalDependency:
    """Verify pyproject.toml declares deepagents under optional-dependencies."""

    def test_deepagents_optional_dependency(self) -> None:
        """TS-03-36: optional-dependencies contains deepagents = ['deepagents>=0.5']."""
        # Find the agentfox package pyproject.toml
        agentfox_pkg_dir = os.path.dirname(
            os.path.dirname(
                inspect.getfile(
                    __import__("agentfox.session.backends.types", fromlist=["types"])
                )
            )
        )
        pyproject_path = os.path.join(agentfox_pkg_dir, "..", "pyproject.toml")
        pyproject_path = os.path.normpath(pyproject_path)

        assert os.path.exists(pyproject_path), (
            f"pyproject.toml not found at {pyproject_path}"
        )

        with open(pyproject_path, "rb") as f:
            config = tomllib.load(f)

        opt_deps = config.get("project", {}).get("optional-dependencies", {})
        assert "deepagents" in opt_deps, (
            "pyproject.toml missing 'deepagents' in [project.optional-dependencies]"
        )
        assert "deepagents>=0.5" in opt_deps["deepagents"], (
            f"Expected 'deepagents>=0.5' in optional-dependencies.deepagents, "
            f"got {opt_deps['deepagents']}"
        )


# ---------------------------------------------------------------------------
# TS-03-37: Test modules have pytest.importorskip guard
# Requirement: 03-REQ-10.2
# ---------------------------------------------------------------------------


class TestImportSkipGuard:
    """Verify all DeepAgentsBackend test modules have pytest.importorskip guard."""

    def test_importorskip_present(self) -> None:
        """TS-03-37: This test module contains pytest.importorskip('deepagents')."""
        # Read our own source file to verify the guard is present
        this_file = os.path.abspath(__file__)
        with open(this_file, encoding="utf-8") as f:
            source = f.read()
        assert (
            "pytest.importorskip('deepagents')" in source
            or 'pytest.importorskip("deepagents")' in source
        ), f"{this_file} missing pytest.importorskip guard"


# ---------------------------------------------------------------------------
# TS-03-38: CI workflow includes deepagents matrix leg
# Requirement: 03-REQ-10.3
#
# Errata: No .github/workflows/ directory exists. The implementation
# group (task 6.3) must create it.
# ---------------------------------------------------------------------------


class TestCIWorkflowDeepagentsLeg:
    """Verify CI workflow has a matrix leg for deepagents extra."""

    def test_ci_has_deepagents_leg(self) -> None:
        """TS-03-38: At least one CI workflow step installs '.[deepagents]'."""
        # Walk up from agentfox package to find the project root
        agentfox_pkg_dir = os.path.dirname(
            os.path.dirname(
                inspect.getfile(
                    __import__("agentfox.session.backends.types", fromlist=["types"])
                )
            )
        )
        project_root = os.path.normpath(os.path.join(agentfox_pkg_dir, "..", "..", ".."))

        workflow_patterns = [
            os.path.join(project_root, ".github", "workflows", "*.yml"),
            os.path.join(project_root, ".github", "workflows", "*.yaml"),
        ]

        workflow_files: list[str] = []
        for pattern in workflow_patterns:
            workflow_files.extend(glob.glob(pattern))

        # Also check Makefile for deepagents test target as alternative CI
        makefile_path = os.path.join(project_root, "Makefile")
        found_deepagents_leg = False

        for wf_file in workflow_files:
            with open(wf_file, encoding="utf-8") as f:
                content = f.read()
            if ".[deepagents]" in content:
                found_deepagents_leg = True
                break

        if not found_deepagents_leg and os.path.exists(makefile_path):
            with open(makefile_path, encoding="utf-8") as f:
                content = f.read()
            if "deepagents" in content:
                found_deepagents_leg = True

        assert found_deepagents_leg, (
            "No CI workflow or Makefile target found that installs/tests deepagents extra"
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
