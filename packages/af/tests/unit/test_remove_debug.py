"""Tests for removal of the dead --debug flag from the code command.

Test Spec: TS-131-1, TS-131-2, TS-131-3, TS-131-4, TS-131-7,
           TS-131-8, TS-131-9, TS-131-E1,
           TS-131-SMOKE-1, TS-131-SMOKE-2,
           TS-131-P2, TS-131-P3
Requirements: 131-REQ-1.1 through 131-REQ-3.3, 131-REQ-1.E1
"""

from __future__ import annotations

import inspect
from unittest.mock import AsyncMock, patch

import pytest
from af.app import main
from af.code import _check_dry_run_conflicts
from agentfox.engine.run import _setup_infrastructure, run_code
from agentfox.engine.state import ExecutionState
from agentfox.knowledge.duckdb_sink import DuckDBSink
from agentfox.nightshift.pid import PidStatus
from click.testing import CliRunner


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Prevent the daemon PID check from blocking ``code`` tests."""
    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


def _make_execution_state(
    *,
    run_status: str = "completed",
) -> ExecutionState:
    """Build a minimal ExecutionState for testing."""
    return ExecutionState(
        plan_hash="abc123",
        node_states={"spec_a:1": "completed"},
        run_status=run_status,
        total_input_tokens=100,
        total_output_tokens=50,
        total_cost=1.0,
        total_sessions=1,
        started_at="2026-06-03T00:00:00+00:00",
        updated_at="2026-06-03T01:00:00+00:00",
    )


# ---------------------------------------------------------------------------
# TS-131-1: --debug not in help output
# ---------------------------------------------------------------------------


class TestDebugNotInHelp:
    """TS-131-1: The code command help output does not list --debug.

    Requirement: 131-REQ-1.2
    """

    def test_debug_not_in_help(self, cli_runner: CliRunner) -> None:
        """code --help output does not contain --debug."""
        result = cli_runner.invoke(main, ["code", "--help"])
        assert result.exit_code == 0
        assert "--debug" not in result.output


# ---------------------------------------------------------------------------
# TS-131-2: --debug rejected by Click
# ---------------------------------------------------------------------------


class TestDebugRejectedByClick:
    """TS-131-2: Passing --debug to code produces a Click error.

    Requirements: 131-REQ-1.1, 131-REQ-1.3
    """

    def test_debug_rejected_by_click(self, cli_runner: CliRunner) -> None:
        """code --debug exits with code 2 and mentions --debug."""
        result = cli_runner.invoke(main, ["code", "--debug"])
        assert result.exit_code == 2
        assert "--debug" in result.output


# ---------------------------------------------------------------------------
# TS-131-3: run_code rejects debug keyword
# ---------------------------------------------------------------------------


class TestRunCodeRejectsDebug:
    """TS-131-3: Calling run_code(config, debug=True) raises TypeError.

    Requirement: 131-REQ-2.1
    """

    def test_run_code_rejects_debug(self) -> None:
        """run_code raises TypeError on debug= keyword argument."""
        from agentfox.core.config import AgentFoxConfig

        config = AgentFoxConfig()
        with pytest.raises(TypeError):
            # run_code is async, but TypeError fires at call-time before await
            run_code(config, debug=True)  # type: ignore[call-arg]


# ---------------------------------------------------------------------------
# TS-131-4: _setup_infrastructure rejects debug keyword
# ---------------------------------------------------------------------------


class TestSetupInfraRejectsDebug:
    """TS-131-4: Calling _setup_infrastructure(config, debug=True) raises TypeError.

    Requirement: 131-REQ-2.2
    """

    def test_setup_infra_rejects_debug(self) -> None:
        """_setup_infrastructure raises TypeError on debug= keyword argument."""
        from agentfox.core.config import AgentFoxConfig

        config = AgentFoxConfig()
        with pytest.raises(TypeError):
            _setup_infrastructure(config, debug=True)  # type: ignore[call-arg]


# ---------------------------------------------------------------------------
# TS-131-7: _check_dry_run_conflicts has no debug parameter
# ---------------------------------------------------------------------------


class TestConflictFnNoDebugParam:
    """TS-131-7: _check_dry_run_conflicts signature does not accept debug.

    Requirement: 131-REQ-3.1
    """

    def test_conflict_fn_no_debug_param(self) -> None:
        """'debug' is not in _check_dry_run_conflicts parameter names."""
        sig = inspect.signature(_check_dry_run_conflicts)
        assert "debug" not in sig.parameters


# ---------------------------------------------------------------------------
# TS-131-8: Dry-run + --watch still rejected
# ---------------------------------------------------------------------------


class TestDryRunWatchRejected:
    """TS-131-8: --dry-run --watch still produces an error listing --watch.

    Requirement: 131-REQ-3.2
    """

    def test_dry_run_watch_rejected(self, cli_runner: CliRunner) -> None:
        """--dry-run --watch exits with code 1 and mentions --watch."""
        result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])
        assert result.exit_code == 1
        assert "--watch" in result.output


# ---------------------------------------------------------------------------
# TS-131-9: Dry-run + --force-clean still rejected
# ---------------------------------------------------------------------------


class TestDryRunForceCleanRejected:
    """TS-131-9: --dry-run --force-clean still produces an error.

    Requirement: 131-REQ-3.3
    """

    def test_dry_run_force_clean_rejected(self, cli_runner: CliRunner) -> None:
        """--dry-run --force-clean exits with code 1 and mentions --force-clean."""
        result = cli_runner.invoke(main, ["code", "--dry-run", "--force-clean"])
        assert result.exit_code == 1
        assert "--force-clean" in result.output


# ---------------------------------------------------------------------------
# TS-131-E1: Dry-run alone does not mention debug
# ---------------------------------------------------------------------------


class TestDryRunNoDebugMention:
    """TS-131-E1: --dry-run alone does not produce any debug-related output.

    Requirement: 131-REQ-1.E1
    """

    def test_dry_run_no_debug_mention(self, cli_runner: CliRunner) -> None:
        """code --dry-run output does not contain --debug."""
        from unittest.mock import MagicMock

        from agentfox.graph.types import (
            Node,
            NodeStatus,
            PlanMetadata,
            TaskGraph,
        )
        from agentfox.knowledge.db import KnowledgeDB

        graph = TaskGraph(
            nodes={
                "t:1": Node(
                    id="t:1",
                    spec_name="t",
                    group_number=1,
                    title="Task t:1",
                    optional=False,
                    status=NodeStatus.PENDING,
                ),
            },
            edges=[],
            order=["t:1"],
            metadata=PlanMetadata(created_at="2026-01-01T00:00:00", version="test"),
        )
        mock_db = MagicMock(spec=KnowledgeDB)
        mock_db.connection = MagicMock()

        with (
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch(
                "af.code.open_knowledge_store",
                return_value=mock_db,
            ),
            patch("af.code.load_plan", return_value=graph),
            patch("af.code.discover_specs", return_value=[]),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert "--debug" not in result.output


# ---------------------------------------------------------------------------
# TS-131-SMOKE-1: code invocation without debug in kwargs
# ---------------------------------------------------------------------------


class TestSmokeCodeWithoutDebug:
    """TS-131-SMOKE-1: code command end-to-end without --debug in kwargs.

    Execution Path: Path 1 from design.md.
    Verifies run_code is called without debug keyword argument.
    """

    def test_smoke_code_no_debug_kwarg(self, cli_runner: CliRunner) -> None:
        """run_code is called without 'debug' in its keyword arguments."""
        state = _make_execution_state()
        mock_rc = AsyncMock(return_value=state)

        with (
            patch("af.code.run_code", mock_rc),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "debug" not in mock_rc.call_args.kwargs
        assert result.exit_code == 0


# ---------------------------------------------------------------------------
# TS-131-SMOKE-2: dry-run conflict check without debug
# ---------------------------------------------------------------------------


class TestSmokeConflictCheck:
    """TS-131-SMOKE-2: Dry-run conflict check without --debug.

    Execution Path: Path 2 from design.md.
    Verifies _check_dry_run_conflicts returns only --watch and --force-clean.
    """

    def test_smoke_conflict_check(self) -> None:
        """_check_dry_run_conflicts with both flags returns only --watch and --force-clean."""
        result = _check_dry_run_conflicts(dry_run=True, watch=True, force_clean=True)
        assert result == ["--watch", "--force-clean"]
        assert "--debug" not in result


# ---------------------------------------------------------------------------
# TS-131-P2: CLI flag removal complete (property test)
# ---------------------------------------------------------------------------


class TestPropertyFlagRemovalComplete:
    """TS-131-P2: No downstream function accepts a debug parameter.

    Property: Property 2 from design.md.
    Validates: 131-REQ-1.1, 131-REQ-1.2, 131-REQ-1.3,
               131-REQ-2.1, 131-REQ-2.2, 131-REQ-2.3
    """

    def test_property_flag_removal_complete(self, cli_runner: CliRunner) -> None:
        """No function in the call chain accepts 'debug', and --help hides it."""
        for fn in [run_code, _setup_infrastructure, DuckDBSink.__init__]:
            sig = inspect.signature(fn)
            assert "debug" not in sig.parameters, f"{fn.__qualname__} still accepts 'debug'"

        result = cli_runner.invoke(main, ["code", "--help"])
        assert "--debug" not in result.output


# ---------------------------------------------------------------------------
# TS-131-P3: Dry-run conflict accuracy (property test)
# ---------------------------------------------------------------------------


class TestPropertyConflictNoDebug:
    """TS-131-P3: _check_dry_run_conflicts never returns --debug.

    Property: Property 3 from design.md.
    Validates: 131-REQ-3.1, 131-REQ-3.2, 131-REQ-3.3, 131-REQ-1.E1
    """

    @pytest.mark.parametrize(
        ("watch", "force_clean"),
        [(False, False), (False, True), (True, False), (True, True)],
    )
    def test_property_conflict_no_debug(self, watch: bool, force_clean: bool) -> None:
        """_check_dry_run_conflicts never includes --debug in output."""
        result = _check_dry_run_conflicts(dry_run=True, watch=watch, force_clean=force_clean)
        assert "--debug" not in result
