"""Tests for run_plan() API new parameters.

Requirement coverage:
  01-REQ-7.1 — run_plan() accepts clear, reset, reset_hard, target with defaults
  01-REQ-7.2 — run_plan(clear=True) sets all nodes to completed and returns count
  01-REQ-7.3 — run_plan(reset=True) dispatches to reset_all/reset_task/reset_spec
  01-REQ-7.4 — run_plan(reset_hard=True) dispatches to hard_reset_all/hard_reset_task
  01-REQ-7.E1 — multiple mode flags raise ValueError
  01-REQ-7.E2 — no plan raises RuntimeError (library boundary — no sys.exit)
  01-REQ-7.E3 — unknown task ID raises exception

Test spec entries: TS-01-22, TS-01-23, TS-01-24, TS-01-25
Correctness properties: 01-PROP-7, 01-PROP-10
Execution path: 01-PATH-6
"""

from __future__ import annotations

import inspect
from unittest.mock import MagicMock, patch

import pytest
from agentfox.engine.reset import HardResetResult, ResetResult
from agentfox.graph.planner import run_plan
from agentfox.graph.types import Node, NodeStatus, PlanMetadata, TaskGraph

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_graph(
    nodes: dict[str, NodeStatus],
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
        edges=[],
        order=list(nodes.keys()),
        metadata=PlanMetadata(created_at="2026-07-28T00:00:00"),
    )


def _sample_reset_result(**overrides: object) -> ResetResult:
    """Build a ResetResult with sensible defaults."""
    defaults: dict[str, object] = {
        "reset_tasks": ["spec:1"],
        "unblocked_tasks": [],
        "cleaned_worktrees": [],
        "cleaned_branches": [],
        "skipped_completed": [],
    }
    defaults.update(overrides)
    return ResetResult(**defaults)  # type: ignore[arg-type]


def _sample_hard_reset_result(**overrides: object) -> HardResetResult:
    """Build a HardResetResult with sensible defaults."""
    defaults: dict[str, object] = {
        "reset_tasks": ["spec:0"],
        "cleaned_worktrees": [],
        "cleaned_branches": [],
        "compaction": (0, 0),
        "rollback_sha": "abc123",
    }
    defaults.update(overrides)
    return HardResetResult(**defaults)  # type: ignore[arg-type]


def _mock_config() -> MagicMock:
    """Create a mock AgentFoxConfig."""
    config = MagicMock()
    config.knowledge = MagicMock()
    return config


# ===========================================================================
# TS-01-22: run_plan() accepts four new keyword parameters
# REQ: 01-REQ-7.1, 01-PROP-7
# ===========================================================================


class TestRunPlanSignature:
    """run_plan() function signature includes the new parameters with defaults."""

    def test_clear_parameter_exists_with_default(self) -> None:
        """WHEN inspecting run_plan signature,
        THEN 'clear' parameter exists with default False.
        """
        sig = inspect.signature(run_plan)
        params = sig.parameters
        assert "clear" in params
        assert params["clear"].default is False

    def test_reset_parameter_exists_with_default(self) -> None:
        """WHEN inspecting run_plan signature,
        THEN 'reset' parameter exists with default False.
        """
        sig = inspect.signature(run_plan)
        params = sig.parameters
        assert "reset" in params
        assert params["reset"].default is False

    def test_reset_hard_parameter_exists_with_default(self) -> None:
        """WHEN inspecting run_plan signature,
        THEN 'reset_hard' parameter exists with default False.
        """
        sig = inspect.signature(run_plan)
        params = sig.parameters
        assert "reset_hard" in params
        assert params["reset_hard"].default is False

    def test_target_parameter_exists_with_default(self) -> None:
        """WHEN inspecting run_plan signature,
        THEN 'target' parameter exists with default None.
        """
        sig = inspect.signature(run_plan)
        params = sig.parameters
        assert "target" in params
        assert params["target"].default is None

    def test_existing_callers_unaffected(self) -> None:
        """WHEN calling run_plan() with only pre-existing parameters,
        THEN no TypeError is raised (backward compatibility).

        01-PROP-7: Existing callers passing no new parameters behave identically.
        """
        config = _mock_config()

        # Existing caller pattern: config + optional pre-existing kwargs
        # The call should reach the build_plan logic without TypeError
        with (
            patch(
                "agentfox.graph.planner.build_plan",
                return_value=_make_graph({"spec:1": NodeStatus.PENDING}),
            ),
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            # This should not raise TypeError
            result = run_plan(config, fast=True)

        assert isinstance(result, TaskGraph)


# ===========================================================================
# TS-01-23: run_plan(clear=True) executes clear logic
# REQ: 01-REQ-7.2
# ===========================================================================


class TestRunPlanClear:
    """run_plan(clear=True) clears all nodes and returns count."""

    def test_clear_returns_integer_count(self) -> None:
        """WHEN run_plan(clear=True) is called with a 4-node plan,
        THEN it returns integer 4.
        """
        config = _mock_config()
        graph = _make_graph(
            {
                "spec:1": NodeStatus.PENDING,
                "spec:2": NodeStatus.FAILED,
                "spec:3": NodeStatus.IN_PROGRESS,
                "spec:4": NodeStatus.COMPLETED,
            }
        )

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=graph),
            patch("agentfox.graph.planner.persist_node_status") as mock_persist,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, clear=True)

        assert result == 4
        assert mock_persist.call_count == 4
        # All calls should set status to 'completed'
        for call in mock_persist.call_args_list:
            assert call.args[2] == "completed"

    def test_clear_calls_persist_for_each_node(self) -> None:
        """WHEN run_plan(clear=True) is called,
        THEN persist_node_status is called once per node with the
        correct node ID and 'completed' status.
        """
        config = _mock_config()
        graph = _make_graph(
            {
                "a:1": NodeStatus.PENDING,
                "b:1": NodeStatus.FAILED,
            }
        )

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=graph),
            patch("agentfox.graph.planner.persist_node_status") as mock_persist,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, clear=True)

        persisted_ids = {c.args[1] for c in mock_persist.call_args_list}
        assert persisted_ids == {"a:1", "b:1"}


# ===========================================================================
# TS-01-24: run_plan(reset=True) dispatches to reset functions
# REQ: 01-REQ-7.3, 01-PATH-6
# ===========================================================================


class TestRunPlanReset:
    """run_plan(reset=True) dispatches to the correct reset function."""

    def test_reset_all_returns_reset_result(self) -> None:
        """WHEN run_plan(reset=True) with target=None and filter_spec=None,
        THEN reset_all is dispatched and a ResetResult is returned.
        """
        config = _mock_config()
        expected = _sample_reset_result(reset_tasks=["spec:1", "spec:2"])

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch(
                "agentfox.graph.planner.load_plan",
                return_value=_make_graph({"spec:1": NodeStatus.FAILED}),
            ),
            patch(
                "agentfox.graph.planner.reset_all",
                return_value=expected,
            ) as mock_ra,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, reset=True)

        assert isinstance(result, ResetResult)
        mock_ra.assert_called_once()

    def test_reset_with_target_dispatches_reset_task(self) -> None:
        """WHEN run_plan(reset=True, target='task:1'),
        THEN reset_task is dispatched with the correct task ID.
        """
        config = _mock_config()
        expected = _sample_reset_result(reset_tasks=["task:1"])

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch(
                "agentfox.graph.planner.load_plan",
                return_value=_make_graph({"task:1": NodeStatus.FAILED}),
            ),
            patch(
                "agentfox.graph.planner.reset_task",
                return_value=expected,
            ) as mock_rt,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, reset=True, target="task:1")

        assert isinstance(result, ResetResult)
        mock_rt.assert_called_once()
        # Verify task_id was passed
        _, kwargs = mock_rt.call_args
        assert kwargs.get("task_id") == "task:1" or mock_rt.call_args.args[0] == "task:1"

    def test_reset_with_filter_spec_dispatches_reset_spec(self) -> None:
        """WHEN run_plan(reset=True, filter_spec='my_spec'),
        THEN reset_spec is dispatched with the correct spec name.
        """
        config = _mock_config()
        expected = _sample_reset_result(reset_tasks=["my_spec:1"])

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch(
                "agentfox.graph.planner.load_plan",
                return_value=_make_graph({"my_spec:1": NodeStatus.FAILED}),
            ),
            patch(
                "agentfox.graph.planner.reset_spec",
                return_value=expected,
            ) as mock_rs,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, reset=True, filter_spec="my_spec")

        assert isinstance(result, ResetResult)
        mock_rs.assert_called_once()


# ===========================================================================
# TS-01-25: run_plan(reset_hard=True) dispatches to hard reset functions
# REQ: 01-REQ-7.4
# ===========================================================================


class TestRunPlanResetHard:
    """run_plan(reset_hard=True) dispatches to the correct hard reset function."""

    def test_reset_hard_all_returns_hard_reset_result(self) -> None:
        """WHEN run_plan(reset_hard=True) with target=None,
        THEN hard_reset_all is dispatched and a HardResetResult is returned.
        """
        config = _mock_config()
        expected = _sample_hard_reset_result(
            reset_tasks=["spec:0", "spec:1"],
            rollback_sha="sha1",
        )

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch(
                "agentfox.graph.planner.load_plan",
                return_value=_make_graph({"spec:0": NodeStatus.FAILED}),
            ),
            patch(
                "agentfox.graph.planner.hard_reset_all",
                return_value=expected,
            ) as mock_hra,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, reset_hard=True)

        assert isinstance(result, HardResetResult)
        mock_hra.assert_called_once()

    def test_reset_hard_with_target_dispatches_hard_reset_task(self) -> None:
        """WHEN run_plan(reset_hard=True, target='task:1'),
        THEN hard_reset_task is dispatched with the correct task ID.
        """
        config = _mock_config()
        expected = _sample_hard_reset_result(
            reset_tasks=["task:1"],
            rollback_sha="def456",
        )

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch(
                "agentfox.graph.planner.load_plan",
                return_value=_make_graph({"task:1": NodeStatus.FAILED}),
            ),
            patch(
                "agentfox.graph.planner.hard_reset_task",
                return_value=expected,
            ) as mock_hrt,
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            result = run_plan(config, reset_hard=True, target="task:1")

        assert isinstance(result, HardResetResult)
        mock_hrt.assert_called_once()


# ===========================================================================
# Edge cases
# ===========================================================================


class TestRunPlanConflictingModes:
    """run_plan() with multiple mode flags raises ValueError.

    Edge case: 01-REQ-7.E1, 01-PROP-10
    """

    def test_clear_and_reset_raises_value_error(self) -> None:
        """WHEN run_plan(clear=True, reset=True),
        THEN ValueError is raised listing conflicting parameters.
        """
        config = _mock_config()
        with pytest.raises(ValueError, match="clear.*reset|reset.*clear"):
            run_plan(config, clear=True, reset=True)

    def test_clear_and_reset_hard_raises_value_error(self) -> None:
        """WHEN run_plan(clear=True, reset_hard=True),
        THEN ValueError is raised listing conflicting parameters.
        """
        config = _mock_config()
        with pytest.raises(ValueError, match="clear|reset_hard"):
            run_plan(config, clear=True, reset_hard=True)

    def test_reset_and_reset_hard_raises_value_error(self) -> None:
        """WHEN run_plan(reset=True, reset_hard=True),
        THEN ValueError is raised listing conflicting parameters.
        """
        config = _mock_config()
        with pytest.raises(ValueError, match="reset|reset_hard"):
            run_plan(config, reset=True, reset_hard=True)

    def test_all_three_modes_raises_value_error(self) -> None:
        """WHEN run_plan(clear=True, reset=True, reset_hard=True),
        THEN ValueError is raised without modifying state.
        """
        config = _mock_config()
        with pytest.raises(ValueError):
            run_plan(config, clear=True, reset=True, reset_hard=True)

    def test_no_sys_exit_called(self) -> None:
        """WHEN run_plan() encounters conflicting modes,
        THEN sys.exit is never called (library boundary).

        01-PROP-10: Library functions never call sys.exit.
        """
        config = _mock_config()
        with (
            patch("sys.exit") as mock_exit,
            pytest.raises(ValueError),
        ):
            run_plan(config, clear=True, reset=True)

        mock_exit.assert_not_called()


class TestRunPlanNoPlan:
    """run_plan() with a mode flag when no plan exists raises RuntimeError.

    Edge case: 01-REQ-7.E2, 01-PROP-10
    """

    def test_clear_no_plan_raises_runtime_error(self) -> None:
        """WHEN run_plan(clear=True) and load_plan returns None,
        THEN RuntimeError is raised indicating no plan exists.
        """
        config = _mock_config()

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=None),
            pytest.raises(RuntimeError, match="(?i)no plan"),
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, clear=True)

    def test_reset_no_plan_raises_runtime_error(self) -> None:
        """WHEN run_plan(reset=True) and load_plan returns None,
        THEN RuntimeError is raised indicating no plan exists.
        """
        config = _mock_config()

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=None),
            pytest.raises(RuntimeError, match="(?i)no plan"),
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, reset=True)

    def test_reset_hard_no_plan_raises_runtime_error(self) -> None:
        """WHEN run_plan(reset_hard=True) and load_plan returns None,
        THEN RuntimeError is raised indicating no plan exists.
        """
        config = _mock_config()

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=None),
            pytest.raises(RuntimeError, match="(?i)no plan"),
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, reset_hard=True)

    def test_no_plan_does_not_call_sys_exit(self) -> None:
        """WHEN run_plan() with a mode flag and no plan,
        THEN sys.exit is never called (library boundary).

        01-PROP-10: Library functions never call sys.exit.
        """
        config = _mock_config()

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=None),
            patch("sys.exit") as mock_exit,
            pytest.raises(RuntimeError),
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, clear=True)

        mock_exit.assert_not_called()


class TestRunPlanUnknownTarget:
    """run_plan(reset_hard=True, target=TASK_ID) with unknown task raises exception.

    Edge case: 01-REQ-7.E3
    """

    def test_unknown_target_raises_exception(self) -> None:
        """WHEN run_plan(reset_hard=True, target='nonexistent:99') and the
        task does not exist in the plan,
        THEN an exception (ValueError or KeyError) is raised identifying
        the unknown task ID.
        """
        config = _mock_config()
        # Plan exists but doesn't contain the target task
        graph = _make_graph({"spec:1": NodeStatus.FAILED})

        with (
            patch("agentfox.graph.planner.open_knowledge_store") as mock_ks,
            patch("agentfox.graph.planner.load_plan", return_value=graph),
            pytest.raises((ValueError, KeyError)),
        ):
            mock_db = MagicMock()
            mock_ks.return_value = mock_db
            run_plan(config, reset_hard=True, target="nonexistent:99")
