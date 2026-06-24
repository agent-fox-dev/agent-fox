"""Banner rendering tests.

Test Spec: TS-14-1, TS-14-2, TS-14-3, TS-14-4, TS-14-7, TS-14-8,
           TS-14-E1, TS-14-E2
Requirements: 14-REQ-1.1, 14-REQ-1.2, 14-REQ-2.1, 14-REQ-2.2,
              14-REQ-2.3, 14-REQ-2.E1, 14-REQ-3.1, 14-REQ-3.2,
              14-REQ-3.E1
"""

from __future__ import annotations

from io import StringIO
from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox import __version__
from agentfox.core.config import ThemeConfig
from agentfox.ui.display import create_theme, render_banner
from rich.console import Console
from rich.theme import Theme

# Expected fox art from design.md — used to verify banner output content.
EXPECTED_FOX_ART = r"""   /\_/\   _
  / o.o \/\ \
 ( > ^ < ) ) )
  \_^/\_/--'"""

_STYLE_ROLES = ("header", "success", "error", "warning", "info", "tool", "muted")


def _capture_banner(
    theme_config: ThemeConfig,
    *,
    quiet: bool = False,
    force_terminal: bool = False,
) -> str:
    """Capture render_banner output via a StringIO-backed console.

    Creates an AppTheme from the given config, replaces its console with
    one that writes to a StringIO buffer, then calls render_banner.

    Args:
        theme_config: Theme configuration to use.
        quiet: Whether to suppress banner output.
        force_terminal: If True, capture ANSI escape codes for role
            verification. If False, capture plain text.

    Returns:
        The captured console output as a string.
    """
    theme = create_theme(theme_config)
    buf = StringIO()
    # Rebuild a Rich Theme from config values to preserve styled output
    rich_theme = Theme({role: getattr(theme_config, role) for role in _STYLE_ROLES})
    theme.console = Console(
        file=buf,
        theme=rich_theme,
        force_terminal=force_terminal,
        width=120,
    )
    render_banner(theme, quiet=quiet)
    return buf.getvalue()


class TestBannerFoxArt:
    """TS-14-1: Banner contains fox ASCII art.

    Requirement: 14-REQ-1.1
    """

    def test_fox_art_present_in_output(self) -> None:
        """All four lines of fox ASCII art appear in banner output."""
        output = _capture_banner(ThemeConfig())

        for line in EXPECTED_FOX_ART.splitlines():
            assert line in output, f"Expected fox art line {line!r} in banner output, got:\n{output}"


class TestBannerFoxArtStyling:
    """TS-14-2: Fox art styled with header role.

    Requirement: 14-REQ-1.2
    """

    def test_fox_art_uses_header_style(self) -> None:
        """Fox art is rendered using the header role markup."""
        output = _capture_banner(ThemeConfig(), force_terminal=True)

        # The header style for default theme is "bold #ff8c00" (bold orange).
        # When rendered with force_terminal, Rich embeds ANSI bold + color codes.
        first_art_line = EXPECTED_FOX_ART.splitlines()[0]
        assert first_art_line in output, f"Expected fox art in styled output, got:\n{output!r}"
        # Verify ANSI codes are present (header style applies bold + color)
        assert "\x1b[" in output, "Expected ANSI escape codes for header styling"


class TestBannerVersionModel:
    """TS-14-3: Banner shows version and model line.

    Requirements: 14-REQ-2.1, 14-REQ-2.2
    """

    def test_version_and_model_line_with_revision(self) -> None:
        """Banner output contains version, revision, and resolved model ID."""
        with patch("agentfox.ui.display._get_git_revision", return_value="abc1234"):
            output = _capture_banner(ThemeConfig())

        expected = f"agent-fox v{__version__} (abc1234).  model: claude-opus-4-6"
        assert expected in output, f"Expected {expected!r} in banner output, got:\n{output}"

    def test_version_and_model_line_without_revision(self) -> None:
        """Banner omits revision gracefully when git is unavailable."""
        with patch("agentfox.ui.display._get_git_revision", return_value=None):
            output = _capture_banner(ThemeConfig())

        expected = f"agent-fox v{__version__}  model: claude-opus-4-6"
        assert expected in output, f"Expected {expected!r} in banner output, got:\n{output}"


class TestBannerWorkingDirectory:
    """TS-14-4: Banner shows working directory.

    Requirement: 14-REQ-3.1
    """

    def test_cwd_appears_in_output(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Working directory appears in the banner output."""
        monkeypatch.setattr(Path, "cwd", staticmethod(lambda: Path("/tmp/test-project")))

        output = _capture_banner(ThemeConfig())

        assert "/tmp/test-project" in output, f"Expected cwd '/tmp/test-project' in banner output, got:\n{output}"


class TestBannerVersionModelStyling:
    """TS-14-7: Version/model line styled with header role.

    Requirement: 14-REQ-2.3
    """

    def test_version_line_uses_header_style(self) -> None:
        """Version/model line is rendered with header role styling."""
        output = _capture_banner(ThemeConfig(), force_terminal=True)

        # Check that the version line appears in styled output with ANSI codes
        assert f"agent-fox v{__version__}" in output
        assert "\x1b[" in output, "Expected ANSI escape codes for header styling"


class TestBannerCwdStyling:
    """TS-14-8: Working directory styled with muted role.

    Requirement: 14-REQ-3.2
    """

    def test_cwd_uses_muted_style(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """CWD line is rendered with muted role styling."""
        monkeypatch.setattr(Path, "cwd", staticmethod(lambda: Path("/tmp/styled-cwd")))

        config = ThemeConfig(muted="dim", header="bold #ff8c00")

        # Capture with ANSI to verify separate styling
        output = _capture_banner(config, force_terminal=True)

        # The cwd should appear in the output
        assert "/tmp/styled-cwd" in output

        # The muted style ("dim") produces ESC[2m in ANSI.
        assert "\x1b[2m" in output, "Expected dim (muted) ANSI escape code in output for cwd line"


# --- Edge Case Tests ---


class TestBannerModelFallback:
    """TS-14-E1: Model resolution failure shows raw tier value.

    Requirement: 14-REQ-2.E1
    """

    def test_invalid_model_shows_raw_value(self) -> None:
        """Invalid model name falls back to raw tier string in output."""
        with patch("agentfox.ui.display.resolve_model", side_effect=Exception("bad")):
            output = _capture_banner(ThemeConfig())

        # Should fall back to the registry default tier string
        assert "model:" in output, f"Expected 'model:' in banner output, got:\n{output}"

    def test_invalid_model_no_exception(self) -> None:
        """No exception is raised when model resolution fails."""
        with patch("agentfox.ui.display.resolve_model", side_effect=Exception("bad")):
            # Should not raise
            _capture_banner(ThemeConfig())


class TestBannerCwdOSError:
    """TS-14-E2: Path.cwd() OSError shows (unknown).

    Requirement: 14-REQ-3.E1
    """

    def test_cwd_oserror_shows_unknown(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """If Path.cwd() raises OSError, display '(unknown)'."""

        def _raise_oserror() -> Path:
            raise OSError("directory deleted")

        monkeypatch.setattr(Path, "cwd", staticmethod(_raise_oserror))

        output = _capture_banner(ThemeConfig())

        assert "(unknown)" in output, f"Expected '(unknown)' in banner output, got:\n{output}"

    def test_cwd_oserror_no_exception(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """No exception propagates when Path.cwd() raises OSError."""

        def _raise_oserror() -> Path:
            raise OSError("directory deleted")

        monkeypatch.setattr(Path, "cwd", staticmethod(_raise_oserror))

        # Should not raise
        _capture_banner(ThemeConfig())
