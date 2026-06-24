"""Tests for nightshift CLI wiring to DaemonRunner.

Verifies that cli/nightshift.py uses DaemonRunner instead of
NightShiftEngine.run() for lifecycle management, and that CLI flags
are properly passed through to build_streams().
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

from click.testing import CliRunner
from nightshift.app import main as night_shift_cmd


def _make_platform_mock() -> MagicMock:
    """Create a mock platform whose check_credentials() returns successfully."""
    platform = MagicMock()
    platform.check_credentials = AsyncMock(return_value=None)
    return platform


def _make_config() -> MagicMock:
    """Create a mock config matching expected structure."""
    config = MagicMock()
    config.max_budget_usd = 10.0
    ns = MagicMock()
    ns.issue_check_interval = 900
    ns.push_fix_branch = False
    config.night_shift = ns
    config.platform.type = "github"
    config.theme = None
    config.orchestrator.max_cost = 10.0
    return config


# Patch targets are at their definition sites since they're imported
# inside the function body.
_PATCHES = {
    "validate": "agentfox.nightshift.engine.validate_night_shift_prerequisites",
    "create_platform": "agentfox.nightshift.platform_factory.create_platform",
    "progress_cls": "agentfox.ui.progress.ProgressDisplay",
    "create_theme": "agentfox.ui.display.create_theme",
    "daemon_runner": "agentfox.nightshift.daemon.DaemonRunner",
    "build_streams": "agentfox.nightshift.streams.build_streams",
    "engine_cls": "agentfox.nightshift.engine.NightShiftEngine",
    "shared_budget": "agentfox.nightshift.daemon.SharedBudget",
}


class TestCliUsesDaemonRunner:
    """Verify CLI wires through DaemonRunner, not NightShiftEngine.run()."""

    def test_cli_creates_daemon_runner(self) -> None:
        """The CLI creates a DaemonRunner and calls runner.run()."""
        from agentfox.nightshift.daemon import DaemonState

        mock_state = DaemonState(total_cost=0.5, issues_fixed=1)

        with (
            patch(_PATCHES["validate"]),
            patch(_PATCHES["create_platform"], return_value=_make_platform_mock()),
            patch(_PATCHES["progress_cls"]) as mock_progress_cls,
            patch(_PATCHES["create_theme"]),
            patch(_PATCHES["daemon_runner"]) as mock_runner_cls,
            patch(_PATCHES["build_streams"], return_value=[MagicMock()]),
            patch(_PATCHES["engine_cls"]) as mock_engine_cls,
            patch(_PATCHES["shared_budget"]),
        ):
            mock_progress = MagicMock()
            mock_progress_cls.return_value = mock_progress

            mock_runner = MagicMock()
            mock_runner.run = AsyncMock(return_value=mock_state)
            mock_runner_cls.return_value = mock_runner

            mock_engine = MagicMock()
            mock_engine.state = MagicMock()
            mock_engine.state.issues_fixed = 1
            mock_engine.state.hunt_scans_completed = 0
            mock_engine_cls.return_value = mock_engine

            runner = CliRunner()
            runner.invoke(
                night_shift_cmd,
                [],
                obj={"config": _make_config(), "quiet": False},
                catch_exceptions=False,
            )

            # DaemonRunner was created and run() was called
            mock_runner_cls.assert_called_once()
            mock_runner.run.assert_awaited_once()

    def test_engine_run_not_called(self) -> None:
        """Engine.run() must NOT be called -- DaemonRunner manages lifecycle."""
        from agentfox.nightshift.daemon import DaemonState

        mock_state = DaemonState()

        with (
            patch(_PATCHES["validate"]),
            patch(_PATCHES["create_platform"], return_value=_make_platform_mock()),
            patch(_PATCHES["progress_cls"]) as mock_progress_cls,
            patch(_PATCHES["create_theme"]),
            patch(_PATCHES["daemon_runner"]) as mock_runner_cls,
            patch(_PATCHES["build_streams"], return_value=[MagicMock()]),
            patch(_PATCHES["engine_cls"]) as mock_engine_cls,
            patch(_PATCHES["shared_budget"]),
        ):
            mock_progress = MagicMock()
            mock_progress_cls.return_value = mock_progress

            mock_runner = MagicMock()
            mock_runner.run = AsyncMock(return_value=mock_state)
            mock_runner_cls.return_value = mock_runner

            mock_engine = MagicMock()
            mock_engine.state = MagicMock()
            mock_engine.state.issues_fixed = 0
            mock_engine.state.hunt_scans_completed = 0
            mock_engine.run = AsyncMock()
            mock_engine_cls.return_value = mock_engine

            runner = CliRunner()
            runner.invoke(
                night_shift_cmd,
                [],
                obj={"config": _make_config(), "quiet": False},
                catch_exceptions=False,
            )

            # engine.run() should NOT have been called
            mock_engine.run.assert_not_awaited()
            # runner.run() should have been called
            mock_runner.run.assert_awaited_once()


class TestCliSpinnerCallbackWiring:
    """Verify CLI wires spinner_callback=progress.update_spinner_text to NightShiftEngine."""

    def test_spinner_callback_wired_to_engine(self) -> None:
        """CLI passes progress.update_spinner_text as spinner_callback to NightShiftEngine."""
        from agentfox.nightshift.daemon import DaemonState

        mock_state = DaemonState()

        with (
            patch(_PATCHES["validate"]),
            patch(_PATCHES["create_platform"], return_value=_make_platform_mock()),
            patch(_PATCHES["progress_cls"]) as mock_progress_cls,
            patch(_PATCHES["create_theme"]),
            patch(_PATCHES["daemon_runner"]) as mock_runner_cls,
            patch(_PATCHES["build_streams"], return_value=[MagicMock()]),
            patch(_PATCHES["engine_cls"]) as mock_engine_cls,
            patch(_PATCHES["shared_budget"]),
        ):
            mock_progress = MagicMock()
            mock_progress_cls.return_value = mock_progress

            mock_runner = MagicMock()
            mock_runner.run = AsyncMock(return_value=mock_state)
            mock_runner_cls.return_value = mock_runner

            mock_engine = MagicMock()
            mock_engine.state = MagicMock()
            mock_engine.state.issues_fixed = 0
            mock_engine.state.hunt_scans_completed = 0
            mock_engine_cls.return_value = mock_engine

            runner = CliRunner()
            runner.invoke(
                night_shift_cmd,
                [],
                obj={"config": _make_config(), "quiet": False},
                catch_exceptions=False,
            )

            # Verify NightShiftEngine was created with spinner_callback set
            call_kwargs = mock_engine_cls.call_args.kwargs
            assert "spinner_callback" in call_kwargs, "NightShiftEngine should be constructed with spinner_callback"
            assert call_kwargs["spinner_callback"] is mock_progress.update_spinner_text


class TestCredentialPreflightCheck:
    """Verify CLI performs credential pre-flight check before entering the loop.

    Requirements: 598-AC-2
    """

    def test_exits_1_when_credentials_invalid(self) -> None:
        """AC-2: CLI exits with code 1 when check_credentials() raises IntegrationError."""
        from agentfox.core.errors import IntegrationError

        mock_platform = MagicMock()
        mock_platform.check_credentials = AsyncMock(side_effect=IntegrationError("GitHub issue list failed (401)"))

        with (
            patch(_PATCHES["validate"]),
            patch(_PATCHES["create_platform"], return_value=mock_platform),
            patch(_PATCHES["create_theme"]),
            patch(_PATCHES["progress_cls"]) as mock_progress_cls,
        ):
            mock_progress_cls.return_value = MagicMock()

            runner = CliRunner()
            result = runner.invoke(
                night_shift_cmd,
                [],
                obj={"config": _make_config(), "quiet": False},
            )

        assert result.exit_code == 1
        assert "authentication" in (result.output + (result.stderr if result.stderr else "")).lower()

    def test_proceeds_when_credentials_valid(self) -> None:
        """AC-3: CLI does not exit when check_credentials() returns normally."""
        from agentfox.nightshift.daemon import DaemonState

        mock_state = DaemonState(total_cost=0.0, issues_fixed=0)
        mock_platform = MagicMock()
        mock_platform.check_credentials = AsyncMock(return_value=None)

        with (
            patch(_PATCHES["validate"]),
            patch(_PATCHES["create_platform"], return_value=mock_platform),
            patch(_PATCHES["progress_cls"]) as mock_progress_cls,
            patch(_PATCHES["create_theme"]),
            patch(_PATCHES["daemon_runner"]) as mock_runner_cls,
            patch(_PATCHES["build_streams"], return_value=[MagicMock()]),
            patch(_PATCHES["engine_cls"]) as mock_engine_cls,
            patch(_PATCHES["shared_budget"]),
        ):
            mock_progress_cls.return_value = MagicMock()

            mock_runner = MagicMock()
            mock_runner.run = AsyncMock(return_value=mock_state)
            mock_runner_cls.return_value = mock_runner

            mock_engine = MagicMock()
            mock_engine.state = MagicMock()
            mock_engine.state.issues_fixed = 0
            mock_engine.state.hunt_scans_completed = 0
            mock_engine_cls.return_value = mock_engine

            runner = CliRunner()
            result = runner.invoke(
                night_shift_cmd,
                [],
                obj={"config": _make_config(), "quiet": False},
                catch_exceptions=False,
            )

        # Daemon ran -- check_credentials did not abort startup
        mock_runner.run.assert_awaited_once()
        assert result.exit_code == 0
