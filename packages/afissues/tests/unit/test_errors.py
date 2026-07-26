"""Tests for afissues.errors module (TS-03-17 through TS-03-21, TS-03-P3, TS-03-E6).

Verifies the independent error hierarchy: AfIssuesError stores context kwargs,
ConfigError subclasses AfIssuesError (not AgentFoxError), IntegrationError
defaults retryable to True, errors.py has no workspace imports, and
agentfox.core.errors remains unchanged and independent.

Requirements: 03-REQ-5.1, 03-REQ-5.2, 03-REQ-5.3, 03-REQ-5.4, 03-REQ-5.5,
              03-REQ-5.E1
"""

from __future__ import annotations

import sys
from pathlib import Path
from unittest.mock import patch

import pytest

from afissues.errors import AfIssuesError, ConfigError, IntegrationError

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_ERRORS_PY = _WORKSPACE_ROOT / "packages" / "afissues" / "afissues" / "errors.py"


# ── TS-03-17: AfIssuesError stores context ────────────────────────────


class TestAfIssuesError:
    """TS-03-17: AfIssuesError stores **context kwargs in .context attribute."""

    def test_message_and_context(self) -> None:
        """AfIssuesError('msg', key='val', other=42) stores context correctly."""
        err = AfIssuesError("test message", key="val", other=42)
        assert str(err) == "test message"
        assert err.context == {"key": "val", "other": 42}

    def test_empty_context(self) -> None:
        """AfIssuesError() with no kwargs has empty context."""
        err = AfIssuesError()
        assert err.context == {}

    def test_message_only(self) -> None:
        """AfIssuesError('msg') with no context kwargs."""
        err = AfIssuesError("just a message")
        assert str(err) == "just a message"
        assert err.context == {}

    def test_is_exception(self) -> None:
        """AfIssuesError is a subclass of Exception."""
        assert issubclass(AfIssuesError, Exception)

    def test_can_be_raised_and_caught(self) -> None:
        """AfIssuesError can be raised and caught as Exception."""
        with pytest.raises(AfIssuesError):
            raise AfIssuesError("test", key="value")


# ── TS-03-18: ConfigError subclasses AfIssuesError ────────────────────


class TestConfigError:
    """TS-03-18: ConfigError is a subclass of AfIssuesError, not AgentFoxError."""

    def test_is_subclass_of_afissues_error(self) -> None:
        assert issubclass(ConfigError, AfIssuesError)

    def test_is_subclass_of_exception(self) -> None:
        assert issubclass(ConfigError, Exception)

    def test_isinstance_check(self) -> None:
        """isinstance(ConfigError(), AfIssuesError) is True."""
        assert isinstance(ConfigError(), AfIssuesError)

    def test_context_kwargs(self) -> None:
        """ConfigError inherits the **context calling convention."""
        err = ConfigError("bad config", param="x")
        assert err.context == {"param": "x"}

    def test_no_agentfox_in_mro(self) -> None:
        """ConfigError.__mro__ does not include any agentfox.core.errors class."""
        for cls in ConfigError.__mro__:
            assert cls.__module__ != "agentfox.core.errors", (
                f"Should not inherit from agentfox: {cls}"
            )


# ── TS-03-19: IntegrationError defaults retryable ────────────────────


class TestIntegrationError:
    """TS-03-19: IntegrationError defaults retryable=True and stores context."""

    def test_default_retryable_is_true(self) -> None:
        """IntegrationError() has retryable == True by default."""
        err = IntegrationError()
        assert err.retryable is True

    def test_isinstance_afissues_error(self) -> None:
        """IntegrationError is an instance of AfIssuesError."""
        assert isinstance(IntegrationError(), AfIssuesError)

    def test_explicit_retryable_false(self) -> None:
        """IntegrationError('msg', retryable=False) overrides default."""
        err = IntegrationError("failed", retryable=False, repo="owner/repo")
        assert err.retryable is False
        assert err.context == {"repo": "owner/repo"}
        assert str(err) == "failed"

    def test_context_with_default_retryable(self) -> None:
        """IntegrationError with context kwargs keeps retryable=True."""
        err = IntegrationError("err", attempt=3, url="https://example.com")
        assert err.retryable is True
        assert err.context == {"attempt": 3, "url": "https://example.com"}


# ── TS-03-20: errors.py has no workspace imports ─────────────────────


class TestErrorsNoWorkspaceImports:
    """TS-03-20: afissues/errors.py contains zero imports from workspace packages."""

    def test_no_workspace_package_references(self) -> None:
        """errors.py does not reference agentfox, afspec, afaudit, or nightshift."""
        source = _ERRORS_PY.read_text()
        for pkg in ["agentfox", "afspec", "afaudit", "nightshift"]:
            assert pkg not in source, f"Found workspace reference '{pkg}' in errors.py"


# ── TS-03-21: agentfox.core.errors hierarchy unchanged ────────────────


class TestAgentfoxErrorsIndependence:
    """TS-03-21: agentfox.core.errors still defines its own hierarchy."""

    def test_agentfox_config_error_is_agentfox_error(self) -> None:
        """agentfox ConfigError subclasses AgentFoxError."""
        from agentfox.core.errors import AgentFoxError
        from agentfox.core.errors import ConfigError as AgentfoxConfigError

        assert issubclass(AgentfoxConfigError, AgentFoxError)

    def test_agentfox_integration_error_is_agentfox_error(self) -> None:
        """agentfox IntegrationError subclasses AgentFoxError."""
        from agentfox.core.errors import AgentFoxError
        from agentfox.core.errors import IntegrationError as AgentfoxIntegrationError

        assert issubclass(AgentfoxIntegrationError, AgentFoxError)

    def test_agentfox_config_error_not_afissues_error(self) -> None:
        """agentfox ConfigError is NOT a subclass of AfIssuesError."""
        from agentfox.core.errors import ConfigError as AgentfoxConfigError

        assert not issubclass(AgentfoxConfigError, AfIssuesError)

    def test_agentfox_integration_error_not_afissues_error(self) -> None:
        """agentfox IntegrationError is NOT a subclass of AfIssuesError."""
        from agentfox.core.errors import IntegrationError as AgentfoxIntegrationError

        assert not issubclass(AgentfoxIntegrationError, AfIssuesError)

    def test_agentfox_integration_error_retryable_default(self) -> None:
        """agentfox IntegrationError also defaults retryable=True."""
        from agentfox.core.errors import IntegrationError as AgentfoxIntegrationError

        err = AgentfoxIntegrationError("workspace error")
        assert err.retryable is True


# ── TS-03-P3: Property — error hierarchy independence ─────────────────


class TestErrorHierarchyProperty:
    """TS-03-P3: Every afissues error class is AfIssuesError, not AgentFoxError."""

    @pytest.mark.parametrize("cls", [AfIssuesError, ConfigError, IntegrationError])
    def test_subclass_of_afissues_error(self, cls: type) -> None:
        """Each error class is a subclass of AfIssuesError."""
        assert issubclass(cls, AfIssuesError)

    @pytest.mark.parametrize("cls", [AfIssuesError, ConfigError, IntegrationError])
    def test_no_agentfox_module_in_mro(self, cls: type) -> None:
        """No class in the MRO has module agentfox.core.errors."""
        for mro_cls in cls.__mro__:
            assert mro_cls.__module__ != "agentfox.core.errors", (
                f"{cls.__name__} MRO includes {mro_cls} from agentfox.core.errors"
            )


# ── TS-03-E6: Library raises AfIssuesError, never calls sys.exit ──────


class TestLibraryNeverExits:
    """TS-03-E6: afissues raises AfIssuesError subclasses, never calls sys.exit()."""

    def test_validate_github_url_raises_config_error(self) -> None:
        """_validate_github_url with a private IP raises ConfigError."""
        from afissues.github import _validate_github_url

        with pytest.raises(AfIssuesError):
            _validate_github_url("169.254.169.254")

    def test_sys_exit_not_called(self) -> None:
        """sys.exit is not called by library code on error."""
        from afissues.github import _validate_github_url

        sys_exit_called = False

        def mock_exit(*args: object) -> None:
            nonlocal sys_exit_called
            sys_exit_called = True

        with patch.object(sys, "exit", mock_exit):
            try:
                _validate_github_url("169.254.169.254")
            except AfIssuesError:
                pass  # expected

        assert not sys_exit_called, "sys.exit should not be called by library code"
