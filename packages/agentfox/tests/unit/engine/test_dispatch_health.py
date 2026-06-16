"""Pre-session workspace health check tests for dispatch.

Test Spec: TS-118-10 (pre-session check blocks node on dirty workspace)
Requirements: 118-REQ-4.1, 118-REQ-4.2, 118-REQ-4.3
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.workspace.health import HealthReport, check_workspace_health


class TestPreSessionCheckBlocksOnDirty:
    """TS-118-10: pre-session check blocks node when workspace is dirty.

    Requirements: 118-REQ-4.2
    """

    @pytest.mark.asyncio
    async def test_presession_check_detects_dirty(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Pre-session workspace check detects untracked files that would
        block harvest and returns a report with has_issues=True."""
        # Create untracked files in the workspace
        (tmp_worktree_repo / "leftover.py").write_text("leftover\n")

        report = await check_workspace_health(tmp_worktree_repo)

        assert report.has_issues is True
        assert "leftover.py" in report.untracked_files

    @pytest.mark.asyncio
    async def test_presession_check_passes_clean(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Pre-session workspace check passes for a clean workspace,
        allowing dispatch to proceed."""
        report = await check_workspace_health(tmp_worktree_repo)

        assert report.has_issues is False


class TestPreSessionCheckFailsOpen:
    """TS-118-E5: pre-session check proceeds on git command failure.

    Requirements: 118-REQ-4.E1
    """

    @pytest.mark.asyncio
    async def test_presession_git_error_fails_open(
        self,
        tmp_worktree_repo: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """When git commands fail during pre-session check, dispatch proceeds."""
        import logging

        async def failing_run_git(args, **kwargs):
            return (1, "", "fatal: error")

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.health"):
            with patch(
                "agentfox.workspace.health.run_git",
                side_effect=failing_run_git,
            ):
                report = await check_workspace_health(tmp_worktree_repo)

        # Fail-open: report should be clean (no issues detected)
        assert report.has_issues is False


# ---------------------------------------------------------------------------
# AC-4: Workspace-state failures must not permanently block (skip-and-retry)
# ---------------------------------------------------------------------------


class TestWorkspaceStateNoPermBlock:
    """AC-4: Workspace-state preflight failures must allow re-evaluation on
    subsequent dispatch cycles without permanently blocking the task.

    Requirements: AC-4 (issue #600)
    """

    @pytest.mark.asyncio
    async def test_prepare_launch_dirty_skips_without_blocking(self) -> None:
        """AC-4 first call: workspace is dirty → prepare_launch returns None
        WITHOUT calling _block_task_fn, so the task remains re-dispatchable."""
        import types

        from agentfox.engine.dispatch import DispatchManager
        from agentfox.engine.graph_sync import GraphSync

        # Build minimal DispatchManager
        routing = MagicMock()
        routing.assess_node = AsyncMock()
        routing.ladders = {}
        decision = MagicMock()
        decision.allowed = True
        circuit = MagicMock()
        circuit.check_launch = MagicMock(return_value=decision)
        workspace_cfg = types.SimpleNamespace(force_clean=False)
        full_config = types.SimpleNamespace(workspace=workspace_cfg)

        mgr = DispatchManager(
            session_runner_factory=MagicMock(),
            inter_session_delay=0,
            parallel=1,
            routing=routing,
            circuit=circuit,
            config=MagicMock(max_retries=3, sync_interval=0),
            full_config=full_config,
        )
        block_fn = MagicMock()
        mgr.set_callbacks(block_fn, MagicMock(return_value=False))
        graph_sync = GraphSync(node_states={"spec:1": "pending"}, edges={})
        mgr.set_graph_sync(graph_sync)

        dirty_report = HealthReport(
            untracked_files=["orphan_from_other_spec.py"],
            dirty_index_files=[],
        )

        with patch(
            "agentfox.workspace.health.check_workspace_health",
            new=AsyncMock(return_value=dirty_report),
        ):
            result = await mgr.prepare_launch("spec:1", MagicMock(), {}, {})

        # (a) _block_task_fn must NOT be called — task stays re-dispatchable
        block_fn.assert_not_called()
        # (b) Returns None to skip the current dispatch cycle
        assert result is None
        # (c) Task is not in blocked state
        assert graph_sync.node_states.get("spec:1") != "blocked"

    @pytest.mark.asyncio
    async def test_prepare_launch_clean_on_retry_proceeds(self) -> None:
        """AC-4 second call: workspace is now clean → prepare_launch returns
        a non-None tuple and the task launches normally."""
        import types

        from agentfox.engine.dispatch import DispatchManager
        from agentfox.engine.graph_sync import GraphSync

        routing = MagicMock()
        routing.assess_node = AsyncMock()
        routing.ladders = {}
        decision = MagicMock()
        decision.allowed = True
        circuit = MagicMock()
        circuit.check_launch = MagicMock(return_value=decision)
        workspace_cfg = types.SimpleNamespace(force_clean=False)
        full_config = types.SimpleNamespace(workspace=workspace_cfg)

        mgr = DispatchManager(
            session_runner_factory=MagicMock(),
            inter_session_delay=0,
            parallel=1,
            routing=routing,
            circuit=circuit,
            config=MagicMock(max_retries=3, sync_interval=0),
            full_config=full_config,
        )
        block_fn = MagicMock()
        mgr.set_callbacks(block_fn, MagicMock(return_value=False))
        graph_sync = GraphSync(node_states={"spec:1": "pending"}, edges={})
        mgr.set_graph_sync(graph_sync)

        dirty_report = HealthReport(
            untracked_files=["orphan_from_other_spec.py"],
            dirty_index_files=[],
        )
        clean_report = HealthReport(untracked_files=[], dirty_index_files=[])

        state = MagicMock()
        state.node_states = {"spec:1": "pending"}

        # First call: dirty — should skip without blocking
        with patch(
            "agentfox.workspace.health.check_workspace_health",
            new=AsyncMock(return_value=dirty_report),
        ):
            first_result = await mgr.prepare_launch("spec:1", state, {}, {})

        block_fn.assert_not_called()
        assert first_result is None
        # Task is still pending (not blocked) after the first skip
        assert graph_sync.node_states.get("spec:1") != "blocked"

        # Second call: clean — should proceed and return a launch tuple
        with patch(
            "agentfox.workspace.health.check_workspace_health",
            new=AsyncMock(return_value=clean_report),
        ), patch.object(mgr, "_run_preflight", return_value=False):
            second_result = await mgr.prepare_launch("spec:1", state, {}, {})

        # (b) Second call returns non-None, allowing the task to launch
        assert second_result is not None
