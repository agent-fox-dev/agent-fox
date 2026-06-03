"""Tests for the --dry-run flag on the code CLI command.

Test Spec: TS-123-1 through TS-123-11, TS-123-E1 through TS-123-E5,
           TS-123-P1 through TS-123-P5, TS-123-SMOKE-1 through TS-123-SMOKE-3
Requirements: 123-REQ-1.1 through 123-REQ-4.2
"""

from __future__ import annotations

import json
from itertools import combinations
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from click.testing import CliRunner
from hypothesis import given, settings
from hypothesis import strategies as st

from agent_fox.cli.app import main
from agent_fox.graph.types import Edge, Node, NodeStatus, PlanMetadata, TaskGraph
from agent_fox.knowledge.db import KnowledgeDB
from agent_fox.nightshift.pid import PidStatus
from agent_fox.spec.discovery import SpecInfo

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_graph(
    nodes: dict[str, NodeStatus] | None = None,
    edges: list[tuple[str, str, str]] | None = None,
) -> TaskGraph:
    """Build a TaskGraph with configurable node statuses.

    Args:
        nodes: Mapping of node_id -> NodeStatus.  Each node_id must be
               in the form ``"spec:N"`` (e.g. ``"test:1"``).
        edges: List of (source, target, kind) triples.

    Returns:
        A TaskGraph ready for analysis.
    """
    if nodes is None:
        nodes = {
            "test:1": NodeStatus.PENDING,
            "test:2": NodeStatus.PENDING,
            "test:3": NodeStatus.PENDING,
        }
    if edges is None:
        # Build a simple chain from the provided node order
        ids = list(nodes.keys())
        edges = [(ids[i], ids[i + 1], "intra_spec") for i in range(len(ids) - 1)]

    node_objs: dict[str, Node] = {}
    for nid, status in nodes.items():
        parts = nid.split(":")
        spec_name = parts[0]
        group_number = int(parts[1]) if len(parts) > 1 else 1
        node_objs[nid] = Node(
            id=nid,
            spec_name=spec_name,
            group_number=group_number,
            title=f"Task {nid}",
            optional=False,
            status=status,
        )

    edge_objs = [Edge(source=s, target=t, kind=k) for s, t, k in edges]
    order = list(nodes.keys())

    return TaskGraph(
        nodes=node_objs,
        edges=edge_objs,
        order=order,
        metadata=PlanMetadata(created_at="2026-01-01T00:00:00", version="test"),
    )


def _mock_knowledge_db() -> MagicMock:
    """Create a mock KnowledgeDB with a connection attribute."""
    db = MagicMock(spec=KnowledgeDB)
    db.connection = MagicMock()
    return db


def _mock_spec_info(name: str = "test") -> SpecInfo:
    """Create a minimal SpecInfo for testing."""
    return SpecInfo(
        name=name,
        prefix=1,
        path=Path(f".specs/{name}"),
        has_tasks=True,
        has_prd=True,
    )


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Prevent the daemon PID check from blocking ``code`` tests."""
    monkeypatch.setattr(
        "agent_fox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


# ---------------------------------------------------------------------------
# Acceptance-criterion tests
# ---------------------------------------------------------------------------


class TestDryRunDisplaysAnalysis:
    """TS-123-1: Dry-run displays analysis output.

    Requirement: 123-REQ-1.1
    """

    def test_displays_analysis_sections(self, cli_runner: CliRunner) -> None:
        """code --dry-run loads the plan and displays analysis output."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.PENDING,
                "test:2": NodeStatus.PENDING,
                "test:3": NodeStatus.PENDING,
            },
            edges=[
                ("test:1", "test:2", "intra_spec"),
                ("test:2", "test:3", "intra_spec"),
            ],
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0
        assert "Plan Analysis" in result.output
        assert "Phase 0" in result.output
        assert "Critical Path" in result.output
        assert "Dependency Edges" in result.output


class TestDryRunSkipsOrchestrator:
    """TS-123-2: Dry-run does not invoke run_code.

    Requirement: 123-REQ-1.2
    """

    def test_run_code_not_called(self, cli_runner: CliRunner) -> None:
        """code --dry-run never calls run_code()."""
        graph = _make_graph()
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.cli.code.run_code") as mock_rc,
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            cli_runner.invoke(main, ["code", "--dry-run"])

        assert mock_rc.call_count == 0


class TestDryRunFiltersCompleted:
    """TS-123-3: Dry-run filters completed nodes.

    Requirement: 123-REQ-1.3
    """

    def test_completed_nodes_excluded(self, cli_runner: CliRunner) -> None:
        """Completed nodes are excluded from the analysis output."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.COMPLETED,
                "test:2": NodeStatus.PENDING,
                "test:3": NodeStatus.PENDING,
            },
            edges=[
                ("test:1", "test:2", "intra_spec"),
                ("test:2", "test:3", "intra_spec"),
            ],
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert "Task test:1" not in result.output
        assert "Task test:2" in result.output
        assert "Task test:3" in result.output


class TestNonDryRunUnchanged:
    """TS-123-4: Non-dry-run behavior unchanged.

    Requirement: 123-REQ-1.4
    """

    def test_run_code_called_without_dry_run(self, cli_runner: CliRunner) -> None:
        """code without --dry-run calls run_code() as before."""
        from unittest.mock import AsyncMock

        from agent_fox.engine.state import ExecutionState

        state = ExecutionState(
            plan_hash="abc",
            node_states={"a:1": "completed"},
            run_status="completed",
            total_input_tokens=100,
            total_output_tokens=50,
            total_cost=1.0,
            total_sessions=1,
            started_at="2026-01-01T00:00:00",
            updated_at="2026-01-01T01:00:00",
        )
        mock_rc = AsyncMock(return_value=state)

        with (
            patch("agent_fox.cli.code.run_code", mock_rc),
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
        ):
            mock_db_path.exists.return_value = True
            cli_runner.invoke(main, ["code"])

        assert mock_rc.call_count == 1


class TestMutualExclusionWatch:
    """TS-123-5: Mutual exclusion with --watch.

    Requirement: 123-REQ-2.1
    """

    def test_dry_run_watch_rejected(self, cli_runner: CliRunner) -> None:
        """--dry-run --watch exits with code 1 and error message."""
        result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])

        assert result.exit_code == 1
        assert "--watch" in result.output


class TestMutualExclusionForceClean:
    """TS-123-8: Mutual exclusion with --force-clean.

    Requirement: 123-REQ-2.1
    """

    def test_dry_run_force_clean_rejected(self, cli_runner: CliRunner) -> None:
        """--dry-run --force-clean exits with code 1 and error message."""
        result = cli_runner.invoke(main, ["code", "--dry-run", "--force-clean"])

        assert result.exit_code == 1
        assert "--force-clean" in result.output


class TestJsonOutput:
    """TS-123-9: JSON output.

    Requirement: 123-REQ-3.1
    """

    def test_json_output_has_required_keys(self, cli_runner: CliRunner) -> None:
        """code --dry-run with --json outputs valid JSON with all keys."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.PENDING,
                "test:2": NodeStatus.PENDING,
                "test:3": NodeStatus.PENDING,
            },
            edges=[
                ("test:1", "test:2", "intra_spec"),
                ("test:2", "test:3", "intra_spec"),
            ],
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert "nodes" in data
        assert "edges" in data
        assert "order" in data
        assert "metadata" in data
        assert "phases" in data
        assert "critical_path" in data
        assert "grouped_edges" in data


class TestDaemonGuardBypassed:
    """TS-123-10: Daemon guard bypassed in dry-run.

    Requirement: 123-REQ-4.1
    """

    def test_dry_run_succeeds_with_daemon_alive(self, cli_runner: CliRunner, monkeypatch: pytest.MonkeyPatch) -> None:
        """code --dry-run succeeds even when daemon PID check reports ALIVE."""
        monkeypatch.setattr(
            "agent_fox.nightshift.pid.check_pid_file",
            lambda _path: (PidStatus.ALIVE, 12345),
        )

        graph = _make_graph()
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0
        assert "Plan Analysis" in result.output


class TestDaemonGuardEnforced:
    """TS-123-11: Daemon guard enforced without dry-run.

    Requirement: 123-REQ-4.2
    """

    def test_non_dry_run_blocked_by_daemon(self, cli_runner: CliRunner, monkeypatch: pytest.MonkeyPatch) -> None:
        """code without --dry-run is blocked by active daemon."""
        monkeypatch.setattr(
            "agent_fox.nightshift.pid.check_pid_file",
            lambda _path: (PidStatus.ALIVE, 12345),
        )

        result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 1
        output_lower = result.output.lower()
        assert "daemon" in output_lower or "night-shift" in output_lower


# ---------------------------------------------------------------------------
# Edge-case tests
# ---------------------------------------------------------------------------


class TestMissingDbDryRun:
    """TS-123-E1: Missing DB file in dry-run.

    Requirement: 123-REQ-1.E1
    """

    def test_missing_db_exits_1(self, cli_runner: CliRunner) -> None:
        """code --dry-run with no DB file exits with code 1."""
        with patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path:
            mock_db_path.exists.return_value = False
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 1
        assert "plan" in result.output.lower()


class TestEmptyPlanDryRun:
    """TS-123-E2: Empty plan in dry-run.

    Requirement: 123-REQ-1.E2
    """

    def test_empty_plan_shows_message(self, cli_runner: CliRunner) -> None:
        """code --dry-run with empty persisted plan displays message."""
        graph = _make_graph(nodes={}, edges=[])
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0
        assert "No tasks in plan" in result.output


class TestAllCompletedDryRun:
    """TS-123-E3: All nodes completed in dry-run.

    Requirement: 123-REQ-1.E3
    """

    def test_all_completed_shows_message(self, cli_runner: CliRunner) -> None:
        """code --dry-run with all completed nodes displays message."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.COMPLETED,
                "test:2": NodeStatus.COMPLETED,
                "test:3": NodeStatus.COMPLETED,
            },
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0
        assert "All tasks completed" in result.output


class TestMultipleIncompatibleFlags:
    """TS-123-E4: Multiple incompatible flags.

    Requirement: 123-REQ-2.E1
    """

    def test_multiple_flags_listed(self, cli_runner: CliRunner) -> None:
        """--dry-run --watch --force-clean lists all incompatible flags."""
        result = cli_runner.invoke(main, ["code", "--dry-run", "--watch", "--force-clean"])

        assert result.exit_code == 1
        assert "--watch" in result.output
        assert "--force-clean" in result.output


class TestEmptyPlanJsonDryRun:
    """TS-123-E5: Empty plan JSON output.

    Requirement: 123-REQ-3.E1
    """

    def test_all_completed_json_has_empty_collections(self, cli_runner: CliRunner) -> None:
        """--dry-run --json with all-completed plan outputs valid JSON."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.COMPLETED,
                "test:2": NodeStatus.COMPLETED,
            },
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert data["nodes"] == {}
        assert data["edges"] == []
        assert data["order"] == []


# ---------------------------------------------------------------------------
# Property tests
# ---------------------------------------------------------------------------


def _random_graph_strategy():
    """Hypothesis strategy for generating random TaskGraphs."""
    return st.integers(min_value=0, max_value=20).flatmap(
        lambda n: st.fixed_dictionaries(
            {nid: st.sampled_from(list(NodeStatus)) for nid in [f"s:{i}" for i in range(1, n + 1)]}
        )
    )


class TestPropertyNoOrchestrator:
    """TS-123-P1: No orchestrator invocation.

    Property: code --dry-run never calls run_code() regardless of plan content.
    Validates: 123-REQ-1.1, 123-REQ-1.2
    """

    @settings(max_examples=20)
    @given(node_statuses=_random_graph_strategy())
    def test_run_code_never_called(self, node_statuses: dict[str, NodeStatus]) -> None:
        """run_code is never called when --dry-run is set."""
        runner = CliRunner()
        graph = _make_graph(nodes=node_statuses) if node_statuses else _make_graph(nodes={}, edges=[])
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.cli.code.run_code") as mock_rc,
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
            patch(
                "agent_fox.nightshift.pid.check_pid_file",
                return_value=(PidStatus.ABSENT, None),
            ),
        ):
            mock_db_path.exists.return_value = True
            runner.invoke(main, ["code", "--dry-run"])

        assert mock_rc.call_count == 0


class TestPropertyCompletedExclusion:
    """TS-123-P2: Completed node exclusion.

    Property: Analysis output contains only non-completed node IDs.
    Validates: 123-REQ-1.3
    """

    @settings(max_examples=20)
    @given(
        node_statuses=st.integers(min_value=1, max_value=10).flatmap(
            lambda n: st.fixed_dictionaries({f"s:{i}": st.sampled_from(list(NodeStatus)) for i in range(1, n + 1)})
        )
    )
    def test_completed_ids_not_in_output(self, node_statuses: dict[str, NodeStatus]) -> None:
        """No completed node ID appears in the output text."""
        runner = CliRunner()
        completed_ids = {nid for nid, status in node_statuses.items() if status == NodeStatus.COMPLETED}

        graph = _make_graph(nodes=node_statuses)
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
            patch(
                "agent_fox.nightshift.pid.check_pid_file",
                return_value=(PidStatus.ABSENT, None),
            ),
        ):
            mock_db_path.exists.return_value = True
            result = runner.invoke(main, ["code", "--dry-run"])

        import re

        for nid in completed_ids:
            pattern = re.escape(nid) + r"(?!\d)"
            assert not re.search(pattern, result.output), f"{nid} (completed) found in output"


class TestPropertyMutualExclusion:
    """TS-123-P3: Mutual exclusion enforcement.

    Property: Any combination of --dry-run with execution flags exits 1.
    Validates: 123-REQ-2.1, 123-REQ-2.E1
    """

    @pytest.mark.parametrize(
        "flags",
        [
            combo
            for r in range(1, 3)
            for combo in combinations(
                ["--watch", "--force-clean"],
                r,
            )
        ],
    )
    def test_all_flag_combos_rejected(self, cli_runner: CliRunner, flags: tuple[str, ...]) -> None:
        """Any non-empty subset of execution flags with --dry-run exits 1."""
        args = ["code", "--dry-run"] + list(flags)

        with patch("agent_fox.cli.code.load_plan") as mock_lp:
            result = cli_runner.invoke(main, args)

        assert result.exit_code == 1
        assert mock_lp.call_count == 0


class TestPropertyReadOnly:
    """TS-123-P4: Read-only database access.

    Property: code --dry-run never calls save_plan().
    Validates: 123-REQ-1.1, 123-REQ-1.2
    """

    @settings(max_examples=10)
    @given(
        node_statuses=st.integers(min_value=0, max_value=10).flatmap(
            lambda n: st.fixed_dictionaries({f"s:{i}": st.sampled_from(list(NodeStatus)) for i in range(1, n + 1)})
        )
    )
    def test_save_plan_never_called(self, node_statuses: dict[str, NodeStatus]) -> None:
        """save_plan is never called during dry-run."""
        runner = CliRunner()
        graph = _make_graph(nodes=node_statuses) if node_statuses else _make_graph(nodes={}, edges=[])
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch("agent_fox.graph.persistence.save_plan") as mock_sp,
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
            patch(
                "agent_fox.nightshift.pid.check_pid_file",
                return_value=(PidStatus.ABSENT, None),
            ),
        ):
            mock_db_path.exists.return_value = True
            runner.invoke(main, ["code", "--dry-run"])

        assert mock_sp.call_count == 0


class TestPropertyDaemonBypass:
    """TS-123-P5: Daemon guard bypass.

    Property: code --dry-run succeeds regardless of daemon state.
    Validates: 123-REQ-4.1
    """

    @pytest.mark.parametrize(
        "daemon_state",
        [PidStatus.ALIVE, PidStatus.ABSENT, PidStatus.STALE],
    )
    def test_dry_run_succeeds_regardless_of_daemon(
        self,
        cli_runner: CliRunner,
        daemon_state: PidStatus,
        monkeypatch: pytest.MonkeyPatch,
    ) -> None:
        """When --dry-run is set, exit code is not 1 due to daemon."""
        monkeypatch.setattr(
            "agent_fox.nightshift.pid.check_pid_file",
            lambda _path: (
                daemon_state,
                12345 if daemon_state == PidStatus.ALIVE else None,
            ),
        )

        graph = _make_graph()
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0


# ---------------------------------------------------------------------------
# Integration smoke tests
# ---------------------------------------------------------------------------


class TestSmokeTextOutput:
    """TS-123-SMOKE-1: Full dry-run text output.

    Execution Path: Path 1 from design.md.
    Real analyzer functions, mocked DB.
    """

    def test_full_text_analysis(self, cli_runner: CliRunner) -> None:
        """End-to-end dry-run with text output returns complete analysis."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.PENDING,
                "test:2": NodeStatus.PENDING,
                "test:3": NodeStatus.PENDING,
            },
            edges=[
                ("test:1", "test:2", "intra_spec"),
                ("test:2", "test:3", "intra_spec"),
            ],
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code == 0
        assert "Plan Analysis" in result.output
        assert "Phase 0" in result.output
        assert "Critical Path" in result.output
        assert "test:1" in result.output
        assert "test:2" in result.output
        assert "test:3" in result.output


class TestSmokeJsonOutput:
    """TS-123-SMOKE-2: Full dry-run JSON output.

    Execution Path: Path 2 from design.md.
    Real analyzer functions, mocked DB.
    """

    def test_full_json_analysis(self, cli_runner: CliRunner) -> None:
        """End-to-end dry-run with JSON output returns structured data."""
        graph = _make_graph(
            nodes={
                "test:1": NodeStatus.PENDING,
                "test:2": NodeStatus.PENDING,
                "test:3": NodeStatus.PENDING,
            },
            edges=[
                ("test:1", "test:2", "intra_spec"),
                ("test:2", "test:3", "intra_spec"),
            ],
        )
        mock_db = _mock_knowledge_db()

        with (
            patch("agent_fox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agent_fox.cli.code.open_knowledge_store", return_value=mock_db),
            patch("agent_fox.cli.code.load_plan", return_value=graph),
            patch(
                "agent_fox.cli.code.discover_specs",
                return_value=[_mock_spec_info()],
            ),
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])

        assert result.exit_code == 0
        data = json.loads(result.output)
        assert len(data["phases"]) >= 1
        assert len(data["critical_path"]) >= 1


class TestSmokeIncompatibleFlags:
    """TS-123-SMOKE-3: Incompatible flags rejected.

    Execution Path: Path 3 from design.md.
    No mocks needed -- validation happens before DB access.
    """

    def test_watch_rejected_without_db_access(self, cli_runner: CliRunner) -> None:
        """End-to-end validation rejects incompatible flags before DB access."""
        # No mocks needed -- validation should happen before DB access
        result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])

        assert result.exit_code == 1
        assert "--watch" in result.output
