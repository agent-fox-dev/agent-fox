"""Tests for the plan --verify flag."""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from agentfox.graph.types import Edge, Node, NodeStatus, PlanMetadata, TaskGraph
from agentfox.nightshift.pid import PidStatus
from click.testing import CliRunner


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


def _make_graph(
    nodes: dict[str, NodeStatus],
    edges: list[Edge] | None = None,
) -> TaskGraph:
    """Build a minimal TaskGraph from a node_id -> status mapping."""
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
        edges=edges or [],
        order=list(nodes.keys()),
        metadata=PlanMetadata(created_at="2026-06-24T00:00:00"),
    )


class TestVerifyFlagRegistered:
    def test_verify_in_help(self, cli_runner: CliRunner) -> None:
        result = cli_runner.invoke(main, ["plan", "--help"])
        assert "--verify" in result.output


class TestVerifyMatchingStates:
    def test_matching_states_exits_zero(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """When spec and DB statuses match, exit 0 and print verified."""
        graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "01_foo:2": NodeStatus.PENDING,
            }
        )
        with (
            patch("af.plan.build_plan", return_value=graph),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("af.plan.open_knowledge_store") as mock_ks,
            patch("af.plan.load_plan", return_value=graph),
        ):
            mock_db_path.exists.return_value = True
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(tmp_path)])

        assert result.exit_code == 0
        assert "verified" in result.output.lower()


class TestVerifyMismatchedStates:
    def test_mismatch_exits_one(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """When statuses differ, exit 1 and report the mismatch."""
        spec_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "01_foo:2": NodeStatus.COMPLETED,
            }
        )
        db_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "01_foo:2": NodeStatus.PENDING,
            }
        )
        with (
            patch("af.plan.build_plan", return_value=spec_graph),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("af.plan.open_knowledge_store") as mock_ks,
            patch("af.plan.load_plan", return_value=db_graph),
        ):
            mock_db_path.exists.return_value = True
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(tmp_path)])

        assert result.exit_code == 1
        assert "01_foo:2" in result.output
        assert "completed" in result.output
        assert "pending" in result.output


class TestVerifyNoDatabase:
    def test_no_db_exits_one(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """When no DB exists, exit 1 with error message."""
        spec_graph = _make_graph({"01_foo:1": NodeStatus.PENDING})
        with (
            patch("af.plan.build_plan", return_value=spec_graph),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = False
            result = cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(tmp_path)])

        assert result.exit_code == 1


class TestVerifyOrphanNodes:
    def test_orphan_nodes_reported(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Nodes in DB but not in specs are reported as orphans."""
        spec_graph = _make_graph({"01_foo:1": NodeStatus.COMPLETED})
        db_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "02_bar:1": NodeStatus.PENDING,
            }
        )
        with (
            patch("af.plan.build_plan", return_value=spec_graph),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("af.plan.open_knowledge_store") as mock_ks,
            patch("af.plan.load_plan", return_value=db_graph),
        ):
            mock_db_path.exists.return_value = True
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(tmp_path)])

        assert result.exit_code == 1
        assert "orphan" in result.output.lower()
        assert "02_bar:1" in result.output


class TestVerifyNewNodes:
    def test_new_nodes_reported(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Nodes in specs but not in DB are reported as new."""
        spec_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "02_bar:1": NodeStatus.PENDING,
            }
        )
        db_graph = _make_graph({"01_foo:1": NodeStatus.COMPLETED})
        with (
            patch("af.plan.build_plan", return_value=spec_graph),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("af.plan.open_knowledge_store") as mock_ks,
            patch("af.plan.load_plan", return_value=db_graph),
        ):
            mock_db_path.exists.return_value = True
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(tmp_path)])

        assert result.exit_code == 1
        assert "new" in result.output.lower()
        assert "02_bar:1" in result.output


class TestVerifyJsonOutput:
    def test_json_output_format(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """JSON mode emits structured verify result."""
        import json

        spec_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "01_foo:2": NodeStatus.COMPLETED,
            }
        )
        db_graph = _make_graph(
            {
                "01_foo:1": NodeStatus.COMPLETED,
                "01_foo:2": NodeStatus.PENDING,
            }
        )
        with (
            patch("af.plan.build_plan", return_value=spec_graph),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("af.plan.open_knowledge_store") as mock_ks,
            patch("af.plan.load_plan", return_value=db_graph),
        ):
            mock_db_path.exists.return_value = True
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = cli_runner.invoke(
                main,
                ["--json", "plan", "--verify", "--specs-dir", str(tmp_path)],
            )

        assert result.exit_code == 1
        data = json.loads(result.output)
        assert data["verified"] is False
        assert len(data["mismatches"]) == 1
        assert data["mismatches"][0]["node_id"] == "01_foo:2"
