"""Integration smoke test: fix-pipeline drain loop end-to-end.

Test Spec: TS-125-SMOKE-1
Requirements: 125-REQ-2.4, 125-REQ-3.3

Verifies the fix-pipeline stream works through the full drain loop from
build_streams -> DaemonRunner -> EngineWorkStream -> NightShiftEngine._drain_issues
-> _run_issue_check -> _process_fix, with a real engine, real streams, and a real
DaemonRunner.  FixPipeline.process_issue is mocked to return FixMetrics.
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_issue(number: int = 42) -> IssueResult:
    return IssueResult(
        number=number,
        title="Fix linter warning",
        html_url=f"https://github.com/test/repo/issues/{number}",
        body="Fix the linter warning in module.py.",
    )


def _make_config() -> MagicMock:
    """Config with generous limits so the drain loop completes naturally."""
    config = MagicMock()
    ns = MagicMock()
    ns.issue_check_interval = 1
    ns.push_fix_branch = False
    config.night_shift = ns
    config.platform.type = "github"
    config.orchestrator.max_cost = 100.0
    config.orchestrator.max_sessions = 100
    return config


def _make_platform(issue: IssueResult) -> AsyncMock:
    """Platform that returns one issue on first call, empty on subsequent."""
    platform = AsyncMock()
    call_count = 0

    async def _list_issues(*args: object, **kwargs: object) -> list[IssueResult]:
        nonlocal call_count
        call_count += 1
        if call_count == 1:
            return [issue]
        return []

    platform.list_issues_by_label = AsyncMock(side_effect=_list_issues)
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    platform.add_issue_comment = AsyncMock()
    return platform


# ---------------------------------------------------------------------------
# TS-125-SMOKE-1: Fix-pipeline drain loop works end-to-end
# ---------------------------------------------------------------------------


class TestSmokeFixPipelineDrainLoop:
    """TS-125-SMOKE-1: Full drain loop from DaemonRunner to _process_fix.

    Uses a real NightShiftEngine (real _drain_issues and _run_issue_check),
    real build_streams (single fix-pipeline stream), and a real DaemonRunner.
    Only FixPipeline.process_issue and cost calculation are mocked.
    """

    @pytest.mark.asyncio
    async def test_drain_loop_e2e(self, tmp_path: Path) -> None:
        """Engine drains one issue, FixPipeline.process_issue is called, state updated."""
        from agentfox.maintenance.daemon import DaemonRunner, SharedBudget
        from agentfox.maintenance.engine import NightShiftEngine
        from agentfox.maintenance.fix_pipeline import FixMetrics
        from agentfox.maintenance.streams import build_streams

        issue = _make_issue(number=42)
        config = _make_config()
        platform = _make_platform(issue)

        # Real engine -- _drain_issues and _run_issue_check execute for real.
        engine = NightShiftEngine(config, platform)

        # Mock FixPipeline so we don't need real archetype sessions.
        mock_metrics = FixMetrics(
            input_tokens=100,
            output_tokens=50,
            sessions_run=1,
        )
        mock_pipeline_instance = MagicMock()
        mock_pipeline_instance.process_issue = AsyncMock(return_value=mock_metrics)

        # Real build_streams -- returns single fix-pipeline stream.
        budget = SharedBudget(max_cost=100.0)
        streams = build_streams(config, engine=engine, budget=budget)

        assert len(streams) == 1, "build_streams must return exactly one stream"
        assert streams[0].name == "fix-pipeline"

        # Real DaemonRunner.
        pid_path = tmp_path / "daemon.pid"
        runner = DaemonRunner(config, platform, streams, budget, pid_path=pid_path)

        with (
            patch(
                "agentfox.maintenance.engine.FixPipeline",
                return_value=mock_pipeline_instance,
            ),
            patch(
                "agentfox.maintenance.engine.NightShiftEngine._calculate_fix_cost",
                return_value=0.50,
            ),
        ):
            # Shutdown after the drain loop should have completed.
            async def _shutdown_after_delay() -> None:
                # Wait long enough for one drain cycle to complete.
                await asyncio.sleep(1.0)
                runner.request_shutdown()

            shutdown_task = asyncio.create_task(_shutdown_after_delay())
            await runner.run()
            await shutdown_task

        # Verify: FixPipeline.process_issue called with the issue.
        assert mock_pipeline_instance.process_issue.call_count >= 1
        call_args = mock_pipeline_instance.process_issue.call_args
        processed_issue = call_args[0][0]  # first positional arg
        assert processed_issue.number == 42

        # Verify: Engine state reflects the fix.
        assert engine.state.issues_fixed >= 1

        # Verify: Drain loop terminated (platform re-poll returned empty).
        # The platform was called at least twice: initial fetch + re-poll.
        assert platform.list_issues_by_label.call_count >= 2
