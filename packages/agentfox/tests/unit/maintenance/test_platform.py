"""Unit tests for PlatformProtocol, GitHubPlatform compliance, and factory.

Test Spec: TS-61-23, TS-61-24, TS-61-25, TS-61-E1, TS-61-E11
Requirements: 61-REQ-8.1, 61-REQ-8.2, 61-REQ-8.3, 61-REQ-1.E1, 61-REQ-8.E1,
              598-AC-1, 598-AC-3, 598-AC-4, 598-AC-5
"""

from __future__ import annotations

import pytest

# ---------------------------------------------------------------------------
# TS-61-23: Platform protocol completeness
# Requirement: 61-REQ-8.1
# ---------------------------------------------------------------------------


class TestPlatformProtocolCompleteness:
    """Verify that PlatformProtocol defines all required methods."""

    def test_protocol_has_required_methods(self) -> None:
        """Protocol defines create_issue, list_issues_by_label,
        add_issue_comment, assign_label, close.
        Note: create_pr was removed in spec 65 (65-REQ-4.1)."""
        from afissues.protocol import PlatformProtocol

        methods = {m for m in dir(PlatformProtocol) if not m.startswith("_")}
        required = {
            "create_issue",
            "list_issues_by_label",
            "add_issue_comment",
            "assign_label",
            "close",
        }
        assert required.issubset(methods)


# ---------------------------------------------------------------------------
# TS-61-24: GitHub implements platform protocol
# Requirement: 61-REQ-8.2
# ---------------------------------------------------------------------------


class TestGitHubPlatformProtocol:
    """Verify that GitHubPlatform satisfies PlatformProtocol."""

    def test_isinstance_check(self) -> None:
        """GitHubPlatform is an instance of PlatformProtocol."""
        from afissues.github import GitHubPlatform
        from afissues.protocol import PlatformProtocol

        gh = GitHubPlatform(owner="x", repo="y", token="t")
        assert isinstance(gh, PlatformProtocol)


# ---------------------------------------------------------------------------
# TS-61-25: Platform instantiation from config
# Requirement: 61-REQ-8.3
# ---------------------------------------------------------------------------


class TestPlatformFactory:
    """Verify platform is instantiated from config."""

    def test_github_platform_from_config(self, tmp_path: object) -> None:
        """Config with type='github' returns a GitHubPlatform."""
        from unittest.mock import patch

        from afissues.github import GitHubPlatform
        from agentfox.core.config import AgentFoxConfig
        from agentfox.maintenance.platform_factory import create_platform

        config = AgentFoxConfig()
        config.platform.type = "github"  # type: ignore[misc]

        with patch.dict("os.environ", {"GITHUB_PAT": "test-token"}):
            platform = create_platform(config, tmp_path)  # type: ignore[arg-type]

        assert isinstance(platform, GitHubPlatform)


# ---------------------------------------------------------------------------
# TS-61-E1: Platform not configured
# Requirement: 61-REQ-1.E1
# ---------------------------------------------------------------------------


class TestPlatformNotConfigured:
    """Verify abort when platform is not configured."""

    def test_abort_with_exit_code_1(self) -> None:
        """Raises SystemExit with code 1 when platform type is 'none'."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.maintenance.engine import validate_night_shift_prerequisites

        config = AgentFoxConfig()
        assert config.platform.type == "none"

        with pytest.raises(SystemExit) as exc_info:
            validate_night_shift_prerequisites(config)
        assert exc_info.value.code == 1


# ---------------------------------------------------------------------------
# TS-61-E11: Unknown platform type
# Requirement: 61-REQ-8.E1
# ---------------------------------------------------------------------------


class TestUnknownPlatformType:
    """Verify abort on unknown platform type."""

    def test_abort_with_exit_code_1(self, tmp_path: object) -> None:
        """Raises SystemExit with code 1 for unknown platform type."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.maintenance.platform_factory import create_platform

        config = AgentFoxConfig()
        config.platform.type = "bitbucket"  # type: ignore[misc]

        with pytest.raises(SystemExit) as exc_info:
            create_platform(config, tmp_path)  # type: ignore[arg-type]
        assert exc_info.value.code == 1


# ---------------------------------------------------------------------------
# 598-AC-1, 598-AC-3, 598-AC-4: check_credentials() on GitHubPlatform
# ---------------------------------------------------------------------------


class TestCheckCredentials:
    """Verify GitHubPlatform.check_credentials() raises on 401/403, passes on 200."""

    def _make_platform(self, token: str = "tok") -> object:
        from afissues.github import GitHubPlatform

        return GitHubPlatform(owner="owner", repo="repo", token=token)

    def test_raises_integration_error_on_401(self) -> None:
        """AC-1: check_credentials() raises IntegrationError when API returns 401."""
        import asyncio
        from unittest.mock import AsyncMock, MagicMock, patch

        from afissues.errors import IntegrationError

        platform = self._make_platform(token="bad-token")
        mock_resp = MagicMock()
        mock_resp.status_code = 401

        with patch.object(platform, "_request", AsyncMock(return_value=mock_resp)):  # type: ignore[arg-type]
            with pytest.raises(IntegrationError) as exc_info:
                asyncio.run(platform.check_credentials())  # type: ignore[attr-defined]

        assert "401" in str(exc_info.value)

    def test_raises_integration_error_on_403(self) -> None:
        """AC-4: check_credentials() raises IntegrationError when API returns 403."""
        import asyncio
        from unittest.mock import AsyncMock, MagicMock, patch

        from afissues.errors import IntegrationError

        platform = self._make_platform(token="no-access-token")
        mock_resp = MagicMock()
        mock_resp.status_code = 403

        with patch.object(platform, "_request", AsyncMock(return_value=mock_resp)):  # type: ignore[arg-type]
            with pytest.raises(IntegrationError) as exc_info:
                asyncio.run(platform.check_credentials())  # type: ignore[attr-defined]

        assert "403" in str(exc_info.value)

    def test_no_exception_on_200(self) -> None:
        """AC-3: check_credentials() returns normally when API returns 200."""
        import asyncio
        from unittest.mock import AsyncMock, MagicMock, patch

        platform = self._make_platform(token="valid-token")
        mock_resp = MagicMock()
        mock_resp.status_code = 200

        with patch.object(platform, "_request", AsyncMock(return_value=mock_resp)):  # type: ignore[arg-type]
            # Should not raise
            asyncio.run(platform.check_credentials())  # type: ignore[attr-defined]


# ---------------------------------------------------------------------------
# 598-AC-5: whitespace-only GITHUB_PAT is rejected at create_platform() time
# ---------------------------------------------------------------------------


class TestWhitespaceOnlyPat:
    """Verify that a whitespace-only GITHUB_PAT causes create_platform() to exit."""

    def test_whitespace_only_pat_exits_1(self, tmp_path: object) -> None:
        """AC-5: GITHUB_PAT='   ' triggers sys.exit(1) before any API call."""
        from unittest.mock import patch

        from agentfox.core.config import AgentFoxConfig
        from agentfox.maintenance.platform_factory import create_platform

        config = AgentFoxConfig()
        config.platform.type = "github"  # type: ignore[misc]

        with patch.dict("os.environ", {"GITHUB_PAT": "   "}):
            with pytest.raises(SystemExit) as exc_info:
                create_platform(config, tmp_path)  # type: ignore[arg-type]

        assert exc_info.value.code == 1
