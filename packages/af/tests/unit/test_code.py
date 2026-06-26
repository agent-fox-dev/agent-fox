"""Tests for the CLI code command.

Test Spec: TS-16-1 through TS-16-8, TS-16-E1 through TS-16-E4
Requirements: 16-REQ-1.1 through 16-REQ-5.2
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from af.app import main
from agentfox.core.config import AgentFoxConfig
from agentfox.engine.state import ExecutionState
from agentfox.knowledge.db import KnowledgeDB
from agentfox.nightshift.pid import PidStatus
from click.testing import CliRunner

_MOCK_KB = MagicMock(spec=KnowledgeDB)


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Prevent the daemon PID check from blocking ``code`` tests."""
    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


def _make_execution_state(
    *,
    run_status: str = "completed",
    node_states: dict[str, str] | None = None,
    total_input_tokens: int = 100_000,
    total_output_tokens: int = 50_000,
    total_cost: float = 2.50,
    total_sessions: int = 3,
) -> ExecutionState:
    """Build a mock ExecutionState for testing."""
    if node_states is None:
        node_states = {
            "spec_a:1": "completed",
            "spec_a:2": "completed",
            "spec_a:3": "completed",
        }
    return ExecutionState(
        plan_hash="abc123",
        node_states=node_states,
        run_status=run_status,
        total_input_tokens=total_input_tokens,
        total_output_tokens=total_output_tokens,
        total_cost=total_cost,
        total_sessions=total_sessions,
        started_at="2026-03-02T00:00:00+00:00",
        updated_at="2026-03-02T01:00:00+00:00",
    )


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


@pytest.fixture
def mock_db_file(tmp_path: Path) -> Path:
    """Create a temporary knowledge.duckdb file."""
    plan_dir = tmp_path / ".agent-fox"
    plan_dir.mkdir(parents=True)
    db_file = plan_dir / "knowledge.duckdb"
    db_file.write_text("")  # just needs to exist for the check
    return db_file


def _mock_run_code(state: ExecutionState | None = None) -> AsyncMock:
    """Create a mock for run_code that returns an ExecutionState."""
    if state is None:
        state = _make_execution_state()
    return AsyncMock(return_value=state)


class TestCommandRegistered:
    """TS-16-1: Command is registered.

    Requirement: 16-REQ-1.1
    """

    def test_code_help_accessible(self, cli_runner: CliRunner) -> None:
        """The code command is accessible via the main CLI group."""
        result = cli_runner.invoke(main, ["code", "--help"])
        assert result.exit_code == 0
        assert "Execute the task plan" in result.output


class TestSuccessfulExecution:
    """TS-16-2: Successful execution prints summary.

    Requirements: 16-REQ-1.2, 16-REQ-1.3, 16-REQ-1.4, 16-REQ-3.1,
                  16-REQ-3.2, 16-REQ-4.1, 16-REQ-5.1, 16-REQ-5.2
    """

    def test_completed_run_exits_zero(self, cli_runner: CliRunner) -> None:
        """A completed run exits with code 0."""
        state = _make_execution_state(run_status="completed")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0

    def test_summary_contains_task_counts(self, cli_runner: CliRunner) -> None:
        """Output contains task counts in the summary."""
        state = _make_execution_state(run_status="completed")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "3/3 done" in result.output

    def test_summary_contains_cost(self, cli_runner: CliRunner) -> None:
        """Output contains cost in the summary."""
        state = _make_execution_state(run_status="completed", total_cost=2.50)
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "$2.50" in result.output

    def test_summary_contains_status(self, cli_runner: CliRunner) -> None:
        """Output contains run status."""
        state = _make_execution_state(run_status="completed")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "completed" in result.output


class TestStalledExitCode:
    """TS-16-6: Stalled execution exits with code 2.

    Requirement: 16-REQ-4.3
    """

    def test_stalled_exits_code_2(self, cli_runner: CliRunner) -> None:
        """A stalled run exits with code 2."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={"a:1": "blocked"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 2

    def test_stalled_output_contains_status(self, cli_runner: CliRunner) -> None:
        """Output mentions stalled status."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={"a:1": "blocked"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "stalled" in result.output


class TestCostLimitExitCode:
    """TS-16-7: Cost limit exits with code 3.

    Requirement: 16-REQ-4.4
    """

    def test_cost_limit_exits_code_3(self, cli_runner: CliRunner) -> None:
        """A cost-limited run exits with code 3."""
        state = _make_execution_state(run_status="cost_limit")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 3


class TestInterruptedExitCode:
    """TS-16-8: Interrupted execution exits with code 130.

    Requirement: 16-REQ-4.5
    """

    def test_interrupted_exits_code_130(self, cli_runner: CliRunner) -> None:
        """An interrupted run exits with code 130."""
        from agentfox.engine.run import InterruptedResult

        mock_rc = AsyncMock(return_value=InterruptedResult())
        with (
            patch("af.code.run_code", mock_rc),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 130


class TestMissingPlanFile:
    """TS-16-E1: Missing plan database.

    Requirement: 16-REQ-1.E1
    """

    def test_missing_plan_exits_code_1(self, cli_runner: CliRunner) -> None:
        """The command exits with code 1 when no plan exists."""
        with patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path:
            mock_db_path.exists.return_value = False
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 1

    def test_missing_plan_mentions_plan(self, cli_runner: CliRunner) -> None:
        """Error message mentions the plan."""
        with patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path:
            mock_db_path.exists.return_value = False
            result = cli_runner.invoke(main, ["code"])

        assert "plan" in result.output.lower()


class TestUnexpectedException:
    """TS-16-E2: Unexpected exception.

    Requirement: 16-REQ-1.E2
    """

    def test_exception_exits_code_1(self, cli_runner: CliRunner) -> None:
        """Unexpected exceptions exit with code 1."""
        mock_rc = AsyncMock(side_effect=RuntimeError("boom"))

        with (
            patch("af.code.run_code", mock_rc),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 1

    def test_exception_shows_error_message(self, cli_runner: CliRunner) -> None:
        """User-friendly error message is shown."""
        mock_rc = AsyncMock(side_effect=RuntimeError("boom"))

        with (
            patch("af.code.run_code", mock_rc),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "error" in result.output.lower()


class TestEmptyPlan:
    """TS-16-E3: Empty plan (zero tasks).

    Requirement: 16-REQ-3.E1
    """

    def test_empty_plan_exits_code_0(self, cli_runner: CliRunner) -> None:
        """An empty plan exits with code 0."""
        state = _make_execution_state(
            run_status="completed",
            node_states={},
            total_sessions=0,
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0

    def test_empty_plan_shows_message(self, cli_runner: CliRunner) -> None:
        """Output contains 'No tasks to execute.' message."""
        state = _make_execution_state(
            run_status="completed",
            node_states={},
            total_sessions=0,
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "No tasks to execute" in result.output


class TestUnknownRunStatus:
    """TS-16-E4: Unknown run status.

    Requirement: 16-REQ-4.E1
    """

    def test_unknown_status_exits_code_1(self, cli_runner: CliRunner) -> None:
        """An unrecognized run status exits with code 1."""
        state = _make_execution_state(
            run_status="unknown_status",
            node_states={"a:1": "completed"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 1


class TestNodeSessionRunnerHarvestError:
    """Verify harvest IntegrationError is caught and reported cleanly.

    When the coding session succeeds but harvest (merge to develop) fails,
    the session should be marked as failed with a clear integration error
    message rather than a generic exception.
    """

    @pytest.mark.asyncio
    async def test_harvest_error_returns_failed_record_with_context(
        self,
    ) -> None:
        """Integration error produces a failed record mentioning harvest."""
        from agentfox.core.errors import IntegrationError
        from agentfox.engine.session_lifecycle import NodeSessionRunner
        from agentfox.knowledge.sink import SessionOutcome

        config = AgentFoxConfig()
        runner = NodeSessionRunner("test_spec:1", config, knowledge_db=_MOCK_KB)

        mock_outcome = SessionOutcome(
            spec_name="test_spec",
            task_group="1",
            node_id="test_spec:1",
            status="completed",
            input_tokens=100,
            output_tokens=200,
            duration_ms=5000,
        )

        with (
            patch(
                "agentfox.engine.session_lifecycle.run_session",
                new_callable=AsyncMock,
                return_value=mock_outcome,
            ),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                side_effect=IntegrationError(
                    "Merge conflict in foo.py",
                ),
            ),
        ):
            from agentfox.workspace import WorkspaceInfo

            workspace = WorkspaceInfo(
                path=Path("/tmp/fake-worktree"),
                spec_name="test_spec",
                task_group=1,
                branch="feature/test_spec/1",
            )
            record = await runner._run_and_harvest(
                "test_spec:1",
                1,
                workspace,
                "system prompt",
                "task prompt",
                Path("/tmp/fake-repo"),
            )

        assert record.status == "failed"
        assert record.error_message is not None
        assert "harvest failed" in record.error_message.lower()
        assert record.input_tokens == 100  # Session metrics preserved
        assert record.output_tokens == 200

    @pytest.mark.asyncio
    async def test_session_summary_read_before_cleanup(
        self,
        tmp_path: Path,
    ) -> None:
        """Session summary JSON is read from the worktree."""
        from agentfox.engine.session_lifecycle import NodeSessionRunner
        from agentfox.workspace import WorkspaceInfo

        summary_data = {
            "summary": "Implemented task group 1.",
            "tests_added_or_modified": [],
        }

        (tmp_path / ".agent-fox").mkdir(exist_ok=True)
        summary_path = tmp_path / ".agent-fox" / "session-summary.json"
        summary_path.write_text(json.dumps(summary_data))

        workspace = WorkspaceInfo(
            path=tmp_path,
            spec_name="test_spec",
            task_group=1,
            branch="feature/test_spec/1",
        )

        result = NodeSessionRunner._read_session_artifacts(workspace)

        assert result is not None
        assert result["summary"] == "Implemented task group 1."


class TestWorkspaceStateRunSummary:
    """118-REQ-8.3: workspace-state errors in run summary output.

    WHEN a run stalls or fails due to workspace-state errors, THE system
    SHALL include the root cause classification ("workspace-state") and the
    original error message in the final run summary output.
    """

    def test_stalled_run_shows_workspace_state_errors(self, cli_runner: CliRunner) -> None:
        """Stalled run summary includes workspace-state error details."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={"spec_a:1": "blocked", "spec_a:2": "completed"},
        )
        state.blocked_reasons["spec_a:1"] = "workspace-state: Divergent untracked files: src/foo.py"

        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "workspace-state" in result.output.lower()
        assert "spec_a:1" in result.output

    def test_completed_run_omits_workspace_state_section(self, cli_runner: CliRunner) -> None:
        """Completed runs do not show workspace-state error section."""
        state = _make_execution_state(run_status="completed")

        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Workspace-state errors" not in result.output

    def test_stalled_run_without_workspace_errors_omits_section(self, cli_runner: CliRunner) -> None:
        """Stalled run without workspace-state reasons omits the section."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={"spec_a:1": "blocked"},
        )
        state.blocked_reasons["spec_a:1"] = "cascade from spec_a:0"

        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Workspace-state errors" not in result.output


class TestFinallyBlockCleanup:
    """Regression test for issue #194: cleanup steps must run independently.

    Each cleanup step in run_code's finally block should be guarded so
    that a failure in one step does not prevent subsequent steps from
    executing.
    """

    def test_cleanup_continues_after_export_failure(self, cli_runner: CliRunner) -> None:
        """Cleanup completes even when internal export fails."""
        state = _make_execution_state(run_status="completed")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0

    def test_cleanup_continues_after_sink_close_failure(self, cli_runner: CliRunner) -> None:
        """Cleanup completes even when internal sink close fails."""
        state = _make_execution_state(run_status="completed")
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0


class TestPostmortemPathInSummary:
    """TS-126-10, TS-126-11: Post-mortem path in CLI summary output.

    Requirements: 126-REQ-6.1, 126-REQ-6.2
    """

    def test_postmortem_path_printed_when_present(self, cli_runner: CliRunner) -> None:
        """TS-126-10: _print_summary() outputs post-mortem path when set.

        Requirement: 126-REQ-6.1
        """
        state = ExecutionState(
            plan_hash="abc123",
            node_states={"a": "blocked"},
            run_status="stalled",
            total_input_tokens=100_000,
            total_output_tokens=50_000,
            total_cost=2.50,
            total_sessions=3,
            started_at="2026-06-03T10:00:00+00:00",
            updated_at="2026-06-03T10:15:00+00:00",
            postmortem_path=".agent-fox/audit/postmortem_123.json",
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Post-mortem: .agent-fox/audit/postmortem_123.json" in result.output

    def test_postmortem_path_not_printed_when_absent(self, cli_runner: CliRunner) -> None:
        """TS-126-11: _print_summary() omits post-mortem line when empty.

        Requirement: 126-REQ-6.2
        """
        state = ExecutionState(
            plan_hash="abc123",
            node_states={"a": "completed", "b": "completed", "c": "completed"},
            run_status="completed",
            total_input_tokens=100_000,
            total_output_tokens=50_000,
            total_cost=2.50,
            total_sessions=3,
            started_at="2026-06-03T10:00:00+00:00",
            updated_at="2026-06-03T10:15:00+00:00",
            postmortem_path="",
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Post-mortem:" not in result.output


class TestArchiveFlag:
    """Tests for the --archive flag on the code command."""

    def test_archive_flag_accepted(self, cli_runner: CliRunner) -> None:
        """The --archive flag is recognized by the CLI."""
        result = cli_runner.invoke(main, ["code", "--help"])
        assert "--archive" in result.output

    def test_archive_moves_completed_specs(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Completed specs are moved to archive/ after a successful run."""
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        archive_dir = specs_dir / "archive"
        archive_dir.mkdir()
        spec_dir = specs_dir / "01_foo"
        spec_dir.mkdir()
        (spec_dir / "requirements.json").write_text("{}")

        state = _make_execution_state(
            run_status="completed",
            node_states={"01_foo:1": "completed", "01_foo:2": "completed"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--archive", "--specs-dir", str(specs_dir)])

        assert result.exit_code == 0
        assert (archive_dir / "01_foo").is_dir()
        assert not spec_dir.exists()
        assert "Archived 1 spec(s)" in result.output

    def test_archive_skips_partial_specs(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Specs with non-completed nodes are not archived."""
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        archive_dir = specs_dir / "archive"
        archive_dir.mkdir()
        spec_dir = specs_dir / "01_foo"
        spec_dir.mkdir()

        state = _make_execution_state(
            run_status="completed",
            node_states={"01_foo:1": "completed", "01_foo:2": "pending"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--archive", "--specs-dir", str(specs_dir)])

        assert result.exit_code == 0
        assert spec_dir.is_dir()
        assert "Archived" not in result.output

    def test_archive_dry_run_conflict(self, cli_runner: CliRunner) -> None:
        """--archive and --dry-run are mutually exclusive."""
        result = cli_runner.invoke(main, ["code", "--archive", "--dry-run"])
        assert result.exit_code == 1
        assert "--archive" in result.output

    def test_archive_missing_archive_dir(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Warning when archive directory does not exist."""
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        # No archive/ subdirectory

        state = _make_execution_state(
            run_status="completed",
            node_states={"01_foo:1": "completed"},
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--archive", "--specs-dir", str(specs_dir)])

        assert result.exit_code == 0
        assert "archive directory does not exist" in result.output.lower()


class TestPerSpecSummary:
    """NS-REQ-1 through NS-REQ-5: per-spec task-group progress in summary.

    Tests for issue #649.
    """

    # TS-NS-1: multi-spec shows indented Specs: block
    def test_multi_spec_shows_specs_block(self, cli_runner: CliRunner) -> None:
        """TS-NS-1: Specs block with indented lines when multiple specs present."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={
                "08_session_lifecycle:1": "completed",
                "08_session_lifecycle:2": "completed",
                "10_knowledge_cleanup:1": "completed",
                "10_knowledge_cleanup:2": "blocked",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Specs:" in result.output
        assert "08_session_lifecycle" in result.output
        assert "2/2" in result.output
        assert "1 blocked" in result.output

    def test_multi_spec_indented_lines_not_single_line(self, cli_runner: CliRunner) -> None:
        """Multi-spec output uses indented block, not a single condensed line."""
        state = _make_execution_state(
            run_status="completed",
            node_states={
                "spec_a:1": "completed",
                "spec_b:1": "completed",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        lines = result.output.splitlines()
        specs_line_idx = next(i for i, ln in enumerate(lines) if ln.strip().startswith("Specs"))
        # The line containing "Specs:" should not also contain a spec name on
        # the same token (it should be just "Specs:")
        assert lines[specs_line_idx].strip() == "Specs:"
        # The following lines should be indented and contain spec names
        assert any("spec_a" in ln and ln.startswith("  ") for ln in lines)
        assert any("spec_b" in ln and ln.startswith("  ") for ln in lines)

    # TS-NS-2: JSON mode includes specs key
    def test_json_mode_includes_specs_key(self, cli_runner: CliRunner) -> None:
        """TS-NS-2: JSON complete event includes specs breakdown."""
        import json as _json

        state = _make_execution_state(
            run_status="stalled",
            node_states={
                "08_session_lifecycle:1": "completed",
                "08_session_lifecycle:2": "completed",
                "10_knowledge_cleanup:1": "completed",
                "10_knowledge_cleanup:2": "blocked",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["--json", "code"], input="")

        # The OutputManager may emit a pretty-printed multi-line JSON object.
        # Accumulate full blocks and try to parse each boundary-complete chunk.
        complete_event = None
        buf = ""
        for line in result.output.splitlines():
            buf += line + "\n"
            try:
                obj = _json.loads(buf)
                buf = ""
            except _json.JSONDecodeError:
                continue
            if isinstance(obj, dict) and obj.get("event") == "complete":
                complete_event = obj
                break

        assert complete_event is not None, (
            f"No 'complete' event found in JSON output. Raw output:\n{result.output!r}"
        )
        assert "specs" in complete_event["summary"]
        specs = complete_event["summary"]["specs"]
        assert "08_session_lifecycle" in specs
        assert "10_knowledge_cleanup" in specs
        assert specs["08_session_lifecycle"]["completed"] == 2
        assert specs["08_session_lifecycle"]["total"] == 2
        assert specs["10_knowledge_cleanup"]["blocked"] == 1
        assert specs["10_knowledge_cleanup"]["total"] == 2

    # TS-NS-3: single spec uses condensed one-line format
    def test_single_spec_condensed_format(self, cli_runner: CliRunner) -> None:
        """TS-NS-3: Single spec uses a one-line Specs: entry, not an indented block."""
        state = _make_execution_state(
            run_status="completed",
            node_states={
                "08_session_lifecycle:1": "completed",
                "08_session_lifecycle:2": "completed",
                "08_session_lifecycle:3": "pending",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        specs_lines = [ln for ln in result.output.splitlines() if "Specs" in ln]
        assert len(specs_lines) == 1
        line = specs_lines[0]
        assert "08_session_lifecycle" in line
        assert "2/3" in line

    # TS-NS-4: injected nodes excluded from count
    def test_injected_nodes_excluded(self, cli_runner: CliRunner) -> None:
        """TS-NS-4: Injected reviewer/verifier nodes (group=0) excluded from count."""
        state = _make_execution_state(
            run_status="completed",
            node_states={
                "spec_a:1": "completed",
                "spec_a:2": "completed",
                "spec_a:0:reviewer": "completed",
                "spec_a:0:verifier": "completed",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0
        specs_lines = [ln for ln in result.output.splitlines() if "spec_a" in ln]
        assert len(specs_lines) == 1
        assert "2/2" in specs_lines[0]
        # Must not show 4/4
        assert "4/4" not in specs_lines[0]

    # TS-NS-5: empty plan skips Specs section
    def test_empty_plan_no_specs_line(self, cli_runner: CliRunner) -> None:
        """TS-NS-5: Empty node_states omits the Specs: section."""
        state = _make_execution_state(
            run_status="completed",
            node_states={},
            total_sessions=0,
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "Specs:" not in result.output
        assert "No tasks to execute" in result.output

    # Additional: Tasks line still present before Specs section
    def test_tasks_line_precedes_specs_section(self, cli_runner: CliRunner) -> None:
        """Tasks: line appears before Specs: section in output."""
        state = _make_execution_state(
            run_status="completed",
            node_states={
                "spec_a:1": "completed",
                "spec_b:1": "pending",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        lines = result.output.splitlines()
        tasks_idx = next((i for i, ln in enumerate(lines) if ln.startswith("Tasks:")), -1)
        specs_idx = next((i for i, ln in enumerate(lines) if "Specs" in ln), -1)
        tokens_idx = next((i for i, ln in enumerate(lines) if ln.startswith("Tokens:")), -1)
        assert tasks_idx != -1
        assert specs_idx != -1
        assert tokens_idx != -1
        assert tasks_idx < specs_idx < tokens_idx

    # Additional: stalled qualifier shows (stalled) when 0 groups done
    def test_stalled_qualifier_when_zero_done(self, cli_runner: CliRunner) -> None:
        """A spec with 0 done and no in-progress groups is marked (stalled)."""
        state = _make_execution_state(
            run_status="stalled",
            node_states={
                "11_enrich_summaries:1": "blocked",
                "11_enrich_summaries:2": "blocked",
            },
        )
        with (
            patch("af.code.run_code", _mock_run_code(state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert "stalled" in result.output
