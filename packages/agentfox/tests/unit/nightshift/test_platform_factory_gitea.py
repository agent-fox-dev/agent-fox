"""Integration tests for platform_factory Gitea routing.

Test Spec: TS-05-50, TS-05-59
Requirements: 05-REQ-18.1, 05-REQ-18.2, 05-REQ-20.7
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

# Patch target for _resolve_remote in the factory module.
_RESOLVE_REMOTE = "agentfox.nightshift.platform_factory._resolve_remote"
# Patch target for the SSRF guard called inside the GiteaPlatform constructor.
_VALIDATE_URL = "afissues.gitea._validate_github_url"


class TestPlatformFactoryGitea:
    """Verify platform_factory constructs GiteaPlatform for type='gitea'."""

    def test_create_platform_returns_gitea_platform(self, tmp_path: Path) -> None:
        """TS-05-50/TS-05-59: create_platform with type='gitea' returns GiteaPlatform.

        Requirements: 05-REQ-18.1, 05-REQ-20.7
        """
        from afissues.gitea import GiteaPlatform
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.corp.com"

        with (
            patch.dict("os.environ", {"GITEA_TOKEN": "tok"}, clear=False),
            patch(_RESOLVE_REMOTE, return_value=("org", "repo")),
            patch(_VALIDATE_URL),  # bypass SSRF guard in constructor
        ):
            result = create_platform(config, tmp_path)

        assert isinstance(result, GiteaPlatform)
        assert result.forge_type == "gitea"

    def test_create_platform_safe_returns_gitea_platform(self, tmp_path: Path) -> None:
        """TS-05-59 variant: create_platform_safe with type='gitea' returns GiteaPlatform.

        Requirements: 05-REQ-18.1
        """
        from afissues.gitea import GiteaPlatform
        from agentfox.nightshift.platform_factory import create_platform_safe

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.corp.com"

        with (
            patch.dict("os.environ", {"GITEA_TOKEN": "tok"}, clear=False),
            patch(_RESOLVE_REMOTE, return_value=("org", "repo")),
            patch(_VALIDATE_URL),  # bypass SSRF guard in constructor
        ):
            result = create_platform_safe(config, tmp_path)

        assert result is not None
        assert isinstance(result, GiteaPlatform)
        assert result.forge_type == "gitea"

    def test_create_platform_safe_returns_none_without_gitea_token(
        self, tmp_path: Path
    ) -> None:
        """create_platform_safe returns None when GITEA_TOKEN is missing.

        Requirements: 05-REQ-18.1
        """
        from agentfox.nightshift.platform_factory import create_platform_safe

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.corp.com"

        with patch.dict("os.environ", {}, clear=True):
            import os

            os.environ.pop("GITEA_TOKEN", None)
            result = create_platform_safe(config, tmp_path)

        assert result is None

    def test_create_platform_exits_without_gitea_token(self, tmp_path: Path) -> None:
        """create_platform calls sys.exit(1) when GITEA_TOKEN is missing.

        Requirements: 05-REQ-18.1
        """
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.corp.com"

        with (
            patch.dict("os.environ", {}, clear=True),
            pytest.raises(SystemExit) as exc_info,
        ):
            import os

            os.environ.pop("GITEA_TOKEN", None)
            create_platform(config, tmp_path)

        assert exc_info.value.code == 1

    def test_create_platform_exits_without_url(self, tmp_path: Path) -> None:
        """create_platform calls sys.exit(1) when url is not configured for Gitea.

        Gitea is self-hosted and has no default URL, so url must be provided.
        """
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = ""

        with (
            patch.dict("os.environ", {"GITEA_TOKEN": "tok"}, clear=False),
            patch(_RESOLVE_REMOTE, return_value=("org", "repo")),
            pytest.raises(SystemExit) as exc_info,
        ):
            create_platform(config, tmp_path)

        assert exc_info.value.code == 1

    def test_gitea_import_no_guard(self) -> None:
        """TS-05-51: GiteaPlatform is importable without NotImplementedError.

        Requirements: 05-REQ-18.2
        """
        from afissues.gitea import GiteaPlatform
        from afissues.gitea import parse_remote as parse_gitea_remote

        assert GiteaPlatform is not None
        assert parse_gitea_remote is not None
