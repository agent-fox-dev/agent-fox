"""Tests for the af plan --reset flag.

Requirement coverage:
  01-REQ-2.1 — --reset performs soft reset of all tasks with confirmation
  01-REQ-2.2 — --reset TASK_ID resets single task without confirmation
  01-REQ-2.3 — --reset --spec NAME resets spec tasks with confirmation
  01-REQ-2.4 — --reset --json emits structured JSON output
  01-REQ-2.E1 — --reset with no plan exits code 1
  01-REQ-2.E2 — --reset with daemon active exits code 1
  01-REQ-2.E3 — declining confirmation aborts without modification
  01-REQ-2.E6 — no resettable tasks returns empty result

Test spec entries: TS-01-5, TS-01-6, TS-01-7, TS-01-8
"""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from agentfox.engine.reset import ResetResult
from agentfox.graph.types import Node, NodeStatus, PlanMetadata, TaskGraph
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Suppress daemon PID guard checks in all tests."""
    from agentfox.nightshift.pid import PidStatus

    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_graph(
    nodes: dict[str, NodeStatus],
) -> TaskGraph:
    """Build a minimal TaskGraph from a node_id -> status mapping.

    Node IDs follow the ``{spec_name}:{group_number}`` convention.
    """
    graph_nodes = {}
    for nid, status in nodes.items():
        parts = nid.split(":")
        spec_name = parts[0]
        group_num = int(parts[1]) if len(parts) > 1 else 1
        graph_nodes[nid] = Node(
            id=nid,
            spec_name=spec_name,
            group_number=group_num,
            title=f"Task {nid}",
            optional=False,
            status=status,
        )
    return TaskGraph(
        nodes=graph_nodes,
        edges=[],
        order=list(nodes.keys()),
        metadata=PlanMetadata(created_at="2026-07-28T00:00:00"),
    )


def _mock_knowledge_store() -> MagicMock:
    """Create a mock KnowledgeDB with a mock connection."""
    mock_db = MagicMock()
    mock_db.connection = MagicMock()
    return mock_db


def _sample_reset_result(**overrides: object) -> ResetResult:
    """Build a ResetResult with sensible defaults, merging *overrides*."""
    defaults: dict[str, object] = {
        "reset_tasks": ["spec:1"],
        "unblocked_tasks": [],
        "cleaned_worktrees": [],
        "cleaned_branches": [],
        "skipped_completed": [],
    }
    defaults.update(overrides)
    return ResetResult(**defaults)  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# TS-01-5: --reset performs soft reset of all tasks with confirmation
# REQ: 01-REQ-2.1, 01-PROP-5, 01-PATH-2
# ---------------------------------------------------------------------------


class TestResetAll:
    """af plan --reset prompts for confirmation and calls run_reset."""

    def test_reset_all_confirmed_calls_run_reset_and_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset and user confirms,
        THEN run_reset is called with soft=True, hard=False, target=None,
        spec=None, and exit code is 0.
        """
        graph = _make_graph(
            {
                "spec:1": NodeStatus.FAILED,
                "spec:2": NodeStatus.IN_PROGRESS,
            }
        )
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec:1", "spec:2"],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(main, ["plan", "--reset"], input="y\n")

        assert result.exit_code == 0, (
            f"Expected exit 0, got {result.exit_code}: {result.output}"
        )
        mock_run.assert_called_once()

        # Verify dispatch args: soft reset, no target, no spec
        _, kwargs = mock_run.call_args
        assert kwargs.get("soft") is True
        assert kwargs.get("hard") is False
        assert kwargs.get("spec") is None

        # First positional arg is target=None
        args = mock_run.call_args.args
        if args:
            assert args[0] is None  # target

    def test_reset_all_output_contains_summary(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN --reset succeeds, THEN stdout contains the reset summary
        with task IDs.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(reset_tasks=["spec:1"])

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(main, ["plan", "--reset"], input="y\n")

        assert result.exit_code == 0
        # Output should reference the reset or the task
        assert "spec:1" in result.output or "reset" in result.output.lower()

    def test_reset_all_with_yes_flag_skips_prompt(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset --yes, THEN no confirmation prompt
        is shown and run_reset is called.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(main, ["plan", "--reset", "--yes"])

        assert result.exit_code == 0
        mock_run.assert_called_once()
        assert "confirm" not in result.output.lower()
        assert "[y/N]" not in result.output


# ---------------------------------------------------------------------------
# 01-REQ-2.E3: Declining confirmation aborts without modification
# ---------------------------------------------------------------------------


class TestResetDeclined:
    """af plan --reset with declined confirmation aborts without modifying state."""

    def test_reset_declined_confirmation_does_not_call_run_reset(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN user declines confirmation prompt for --reset,
        THEN run_reset is not called and exit code is 0.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset") as mock_run,
        ):
            result = cli_runner.invoke(main, ["plan", "--reset"], input="n\n")

        assert result.exit_code == 0
        mock_run.assert_not_called()


# ---------------------------------------------------------------------------
# TS-01-6: --reset TASK_ID resets single task without confirmation
# REQ: 01-REQ-2.2, 01-PATH-4
# ---------------------------------------------------------------------------


class TestResetTaskId:
    """af plan --reset TASK_ID resets a single task without prompting."""

    def test_reset_task_id_calls_run_reset_with_target(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset spec:1,
        THEN run_reset is called with target='spec:1' and no
        confirmation prompt is shown.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED, "spec:2": NodeStatus.PENDING})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec:1"],
            unblocked_tasks=["spec:2"],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(main, ["plan", "--reset", "spec:1"])

        assert result.exit_code == 0, (
            f"Expected exit 0, got {result.exit_code}: {result.output}"
        )
        mock_run.assert_called_once()

        # Verify target was passed
        args, kwargs = mock_run.call_args
        target = args[0] if args else kwargs.get("target")
        assert target == "spec:1"

        # Verify soft reset
        assert kwargs.get("soft") is True
        assert kwargs.get("hard") is False

        # No confirmation prompt should appear
        assert "confirm" not in result.output.lower()

    def test_reset_task_id_prints_summary(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN --reset TASK_ID succeeds,
        THEN stdout contains the reset summary for the task and its
        cascaded dependents.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED, "spec:2": NodeStatus.BLOCKED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec:1"],
            unblocked_tasks=["spec:2"],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(main, ["plan", "--reset", "spec:1"])

        assert result.exit_code == 0
        assert "spec:1" in result.output or "reset" in result.output.lower()


# ---------------------------------------------------------------------------
# TS-01-7: --reset --spec NAME resets tasks for named spec with confirmation
# REQ: 01-REQ-2.3
# ---------------------------------------------------------------------------


class TestResetSpec:
    """af plan --reset --spec NAME resets spec-scoped tasks with confirmation."""

    def test_reset_spec_confirmed_calls_run_reset_with_spec(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset --spec spec_a and user confirms,
        THEN run_reset is called with spec='spec_a' and soft=True.
        """
        graph = _make_graph(
            {
                "spec_a:1": NodeStatus.FAILED,
                "spec_a:2": NodeStatus.IN_PROGRESS,
                "spec_b:1": NodeStatus.FAILED,
            }
        )
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec_a:1", "spec_a:2"],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--spec", "spec_a"], input="y\n"
            )

        assert result.exit_code == 0
        mock_run.assert_called_once()

        _, kwargs = mock_run.call_args
        assert kwargs.get("spec") == "spec_a"
        assert kwargs.get("soft") is True
        assert kwargs.get("hard") is False

    def test_reset_spec_declined_does_not_call_run_reset(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN user declines confirmation for --reset --spec,
        THEN run_reset is not called.
        """
        graph = _make_graph({"spec_a:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset") as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--spec", "spec_a"], input="n\n"
            )

        assert result.exit_code == 0
        mock_run.assert_not_called()


# ---------------------------------------------------------------------------
# TS-01-8: --reset --json emits structured JSON output
# REQ: 01-REQ-2.4
# ---------------------------------------------------------------------------


class TestResetJsonOutput:
    """af plan --reset --json --yes emits a JSON object matching ResetResult."""

    def test_reset_json_contains_reset_result_keys(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset --json --yes,
        THEN stdout is valid JSON with keys: reset_tasks, unblocked_tasks,
        cleaned_worktrees, cleaned_branches.
        """
        graph = _make_graph(
            {
                "spec:0": NodeStatus.FAILED,
                "spec:1": NodeStatus.IN_PROGRESS,
            }
        )
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec:0", "spec:1"],
            unblocked_tasks=[],
            cleaned_worktrees=[],
            cleaned_branches=[],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--json", "--yes"]
            )

        assert result.exit_code == 0, (
            f"Expected exit 0, got {result.exit_code}: {result.output}"
        )
        data = json.loads(result.output)
        assert set(data.keys()) >= {
            "reset_tasks",
            "unblocked_tasks",
            "cleaned_worktrees",
            "cleaned_branches",
        }
        assert data["reset_tasks"] == ["spec:0", "spec:1"]

    def test_reset_json_values_match_result(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN run_reset returns specific worktrees and branches,
        THEN JSON output faithfully reports them.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=["spec:1"],
            unblocked_tasks=["spec:2"],
            cleaned_worktrees=["/tmp/wt1"],
            cleaned_branches=["feature/spec-1"],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--json", "--yes"]
            )

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert data["unblocked_tasks"] == ["spec:2"]
        assert data["cleaned_worktrees"] == ["/tmp/wt1"]
        assert data["cleaned_branches"] == ["feature/spec-1"]


# ---------------------------------------------------------------------------
# Edge cases
# ---------------------------------------------------------------------------


class TestResetNoPlan:
    """af plan --reset with no plan in DB exits code 1.

    Edge case: 01-REQ-2.E1, 01-PROP-4
    """

    def test_reset_no_plan_exits_one(self, cli_runner: CliRunner) -> None:
        """WHEN load_plan returns None (no plan exists),
        THEN exit code is 1 and error message references missing plan.
        """
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=None),
        ):
            result = cli_runner.invoke(main, ["plan", "--reset"])

        assert result.exit_code == 1
        combined = result.output + getattr(result, "stderr", "")
        assert "no plan found" in combined.lower()

    def test_reset_no_plan_does_not_call_run_reset(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN load_plan returns None,
        THEN run_reset is not called.
        """
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=None),
            patch("af.plan.run_reset") as mock_run,
        ):
            result = cli_runner.invoke(main, ["plan", "--reset"])

        assert result.exit_code == 1
        mock_run.assert_not_called()


class TestResetDaemonGuard:
    """af plan --reset refuses to run when daemon is active.

    Edge case: 01-REQ-2.E2
    """

    def test_reset_with_active_daemon_exits_one(
        self, cli_runner: CliRunner, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """WHEN the nightshift daemon PID guard is active,
        THEN exit code is 1 and an error message is shown.
        """
        from agentfox.nightshift.pid import PidStatus

        # Override the autouse _no_daemon fixture
        monkeypatch.setattr(
            "agentfox.nightshift.pid.check_pid_file",
            lambda _path: (PidStatus.ALIVE, 12345),
        )

        result = cli_runner.invoke(main, ["plan", "--reset"])

        assert result.exit_code == 1
        combined = result.output + getattr(result, "stderr", "")
        assert "daemon" in combined.lower()


class TestResetNoResettableTasks:
    """af plan --reset with no resettable tasks completes with empty results.

    Edge case: 01-REQ-2.E6
    """

    def test_reset_empty_result_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN all tasks are in non-resettable statuses,
        THEN exit code is 0 and result lists are empty.
        """
        graph = _make_graph(
            {
                "spec:1": NodeStatus.COMPLETED,
                "spec:2": NodeStatus.PENDING,
            }
        )
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(
            reset_tasks=[],
            unblocked_tasks=[],
            cleaned_worktrees=[],
            cleaned_branches=[],
            skipped_completed=[],
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(main, ["plan", "--reset", "--yes"])

        assert result.exit_code == 0


class TestResetFlagRegistered:
    """af plan --reset flag appears in help output."""

    def test_reset_in_help(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --help, THEN --reset is listed."""
        result = cli_runner.invoke(main, ["plan", "--help"])
        assert "--reset" in result.output
