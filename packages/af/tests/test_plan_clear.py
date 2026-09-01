"""Tests for the af plan --clear flag.

Requirement coverage:
  01-REQ-1.1 — clear sets all nodes to completed, truncates session tables
  01-REQ-1.2 — clear --spec scopes to named spec only
  01-REQ-1.3 — clear --json emits structured output
  01-REQ-1.4 — clear never prompts for confirmation
  01-REQ-1.E1 — clear with no plan exits code 1
  01-REQ-1.E3 — clear with zero-row plan succeeds with count 0
  01-REQ-1.E5 — clear --spec with no matching nodes succeeds with count 0

Test spec entries: TS-01-1, TS-01-2, TS-01-3, TS-01-4
"""

from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from agentfox.graph.types import Node, NodeStatus, PlanMetadata, TaskGraph
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Suppress daemon PID guard checks in all tests."""
    from agentfox.maintenance.pid import PidStatus

    monkeypatch.setattr(
        "agentfox.maintenance.pid.check_pid_file",
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


# ---------------------------------------------------------------------------
# TS-01-1: --clear sets all nodes to completed, clears session tables
# REQ: 01-REQ-1.1, 01-PROP-1, 01-PROP-3, 01-PATH-1
# ---------------------------------------------------------------------------


class TestClearAllNodes:
    """af plan --clear marks every node completed and truncates session tables."""

    def test_clear_sets_all_nodes_completed_and_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --clear on a plan with 3 mixed-status nodes,
        THEN exit code is 0, all nodes are set to completed via
        persist_node_status, and output reports the count.
        """
        graph = _make_graph(
            {
                "spec_a:1": NodeStatus.PENDING,
                "spec_a:2": NodeStatus.IN_PROGRESS,
                "spec_b:1": NodeStatus.FAILED,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status") as mock_persist,
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 0, f"Expected exit 0, got {result.exit_code}: {result.output}"
        assert "3" in result.output

        # Every node should have been set to completed
        assert mock_persist.call_count == 3
        persisted_ids = {c.args[1] for c in mock_persist.call_args_list}
        assert persisted_ids == {"spec_a:1", "spec_a:2", "spec_b:1"}
        for c in mock_persist.call_args_list:
            assert c.args[2] == "completed"

    def test_clear_truncates_session_tables(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear, THEN the four session-scoped tables
        (runs, session_outcomes, review_findings, drift_findings) are
        truncated.
        """
        graph = _make_graph({"spec_a:1": NodeStatus.PENDING})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 0

        # Verify session tables were truncated via the mock connection
        executed_sql = [
            c.args[0].strip()
            for c in mock_db.connection.execute.call_args_list
        ]
        session_tables = {"runs", "session_outcomes", "review_findings", "drift_findings"}
        for table in session_tables:
            assert any(
                table in sql for sql in executed_sql
            ), f"Session table '{table}' was not truncated"


# ---------------------------------------------------------------------------
# TS-01-2: --clear --spec NAME scopes to named spec
# REQ: 01-REQ-1.2, 01-PROP-2
# ---------------------------------------------------------------------------


class TestClearWithSpec:
    """af plan --clear --spec NAME only clears nodes belonging to the named spec."""

    def test_clear_spec_only_updates_matching_nodes(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --clear --spec spec_a on a plan with nodes from
        spec_a and spec_b, THEN only spec_a nodes are set to completed.
        """
        graph = _make_graph(
            {
                "spec_a:1": NodeStatus.PENDING,
                "spec_a:2": NodeStatus.FAILED,
                "spec_b:1": NodeStatus.IN_PROGRESS,
                "spec_b:2": NodeStatus.BLOCKED,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status") as mock_persist,
        ):
            result = cli_runner.invoke(main, ["plan", "--clear", "--spec", "spec_a"])

        assert result.exit_code == 0
        assert "2" in result.output

        # Only spec_a nodes should be persisted as completed
        assert mock_persist.call_count == 2
        persisted_ids = {c.args[1] for c in mock_persist.call_args_list}
        assert persisted_ids == {"spec_a:1", "spec_a:2"}

    def test_clear_spec_no_matching_nodes_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN --clear --spec targets a spec with zero matching nodes,
        THEN exit code is 0 and cleared count is 0.

        Edge case: 01-REQ-1.E5
        """
        graph = _make_graph(
            {
                "spec_b:1": NodeStatus.PENDING,
                "spec_b:2": NodeStatus.FAILED,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status") as mock_persist,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--clear", "--spec", "nonexistent_spec"]
            )

        assert result.exit_code == 0
        assert "0" in result.output
        mock_persist.assert_not_called()


# ---------------------------------------------------------------------------
# TS-01-3: --clear --json emits structured JSON output
# REQ: 01-REQ-1.3
# ---------------------------------------------------------------------------


class TestClearJsonOutput:
    """af plan --clear --json emits structured JSON with 'cleared' and 'spec' keys."""

    def test_clear_json_output_all(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear --json on a 5-node plan,
        THEN stdout is valid JSON: {"cleared": 5, "spec": null}.
        """
        graph = _make_graph(
            {
                "spec_a:1": NodeStatus.PENDING,
                "spec_a:2": NodeStatus.IN_PROGRESS,
                "spec_a:3": NodeStatus.FAILED,
                "spec_b:1": NodeStatus.BLOCKED,
                "spec_b:2": NodeStatus.COMPLETED,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear", "--json"])

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert data == {"cleared": 5, "spec": None}

    def test_clear_json_output_with_spec(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear --spec foo --json,
        THEN stdout is valid JSON: {"cleared": N, "spec": "foo"}.
        """
        graph = _make_graph(
            {
                "foo:1": NodeStatus.PENDING,
                "foo:2": NodeStatus.FAILED,
                "bar:1": NodeStatus.IN_PROGRESS,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(
                main, ["plan", "--clear", "--spec", "foo", "--json"]
            )

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert data == {"cleared": 2, "spec": "foo"}


# ---------------------------------------------------------------------------
# TS-01-4: --clear never prompts for confirmation
# REQ: 01-REQ-1.4
# ---------------------------------------------------------------------------


class TestClearNoConfirmation:
    """af plan --clear never displays a confirmation prompt."""

    def test_clear_does_not_prompt(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear without stdin input,
        THEN exit code is 0 and no confirmation prompt appears.
        """
        graph = _make_graph({"spec_a:1": NodeStatus.PENDING})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"], input=None)

        assert result.exit_code == 0
        assert "confirm" not in result.output.lower()
        assert "y/n" not in result.output.lower()
        assert "[y/N]" not in result.output


# ---------------------------------------------------------------------------
# Edge cases
# ---------------------------------------------------------------------------


class TestClearNoPlan:
    """af plan --clear with no plan in DB exits code 1.

    Edge case: 01-REQ-1.E1, 01-PROP-4
    """

    def test_clear_no_plan_exits_one(self, cli_runner: CliRunner) -> None:
        """WHEN load_plan returns None (no plan exists),
        THEN exit code is 1 and error message references missing plan.
        """
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=None),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 1
        combined = result.output + getattr(result, "stderr", "")
        assert "no plan found" in combined.lower()

    def test_clear_no_plan_does_not_modify_tables(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN load_plan returns None, THEN no tables are modified."""
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=None),
            patch("af.plan.persist_node_status") as mock_persist,
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 1
        mock_persist.assert_not_called()


class TestClearEmptyPlan:
    """af plan --clear with a plan containing zero nodes succeeds.

    Edge case: 01-REQ-1.E3
    """

    def test_clear_empty_plan_exits_zero(self, cli_runner: CliRunner) -> None:
        """WHEN the plan has zero nodes, THEN exit code is 0 and
        cleared count is 0.
        """
        graph = _make_graph({})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status") as mock_persist,
        ):
            result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 0
        assert "0" in result.output
        mock_persist.assert_not_called()


class TestClearDaemonGuard:
    """af plan --clear refuses to run when daemon is active.

    Edge case: 01-REQ-1.E2
    """

    def test_clear_with_active_daemon_exits_one(
        self, cli_runner: CliRunner, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        """WHEN the nightshift daemon PID guard is active,
        THEN exit code is 1 and an error message is shown.
        """
        from agentfox.maintenance.pid import PidStatus

        # Override the autouse _no_daemon fixture
        monkeypatch.setattr(
            "agentfox.maintenance.pid.check_pid_file",
            lambda _path: (PidStatus.ALIVE, 12345),
        )

        result = cli_runner.invoke(main, ["plan", "--clear"])

        assert result.exit_code == 1
        combined = result.output + getattr(result, "stderr", "")
        assert "daemon" in combined.lower()


class TestClearFlagRegistered:
    """af plan --clear flag appears in help output."""

    def test_clear_in_help(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --help, THEN --clear is listed."""
        result = cli_runner.invoke(main, ["plan", "--help"])
        assert "--clear" in result.output
