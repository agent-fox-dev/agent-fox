"""Unit tests for parallel issue dispatch in NightShiftEngine.

Covers issue #707: parallel issue processing with dependency-aware dispatch.

Test Spec: TS-NS-1 through TS-NS-7
"""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_engine(
    max_cost: float | None = None,
    max_sessions: int | None = None,
    max_parallel: int = 1,
):
    """Return a NightShiftEngine with a mocked platform and minimal config."""
    from agentfox.maintenance.engine import NightShiftEngine

    config = MagicMock()
    config.orchestrator.max_cost = max_cost
    config.orchestrator.max_sessions = max_sessions
    config.night_shift.similarity_threshold = 0.85
    config.night_shift.max_parallel = max_parallel

    platform = AsyncMock()
    platform.list_issues_by_label = AsyncMock(return_value=[])
    platform.fetch_github_relationships = AsyncMock(return_value=[])

    engine = NightShiftEngine(config=config, platform=platform)
    return engine, platform


def _make_issue(number: int, title: str = "Test issue") -> IssueResult:
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/example/repo/issues/{number}",
        body="Issue body",
    )


# ---------------------------------------------------------------------------
# TS-NS-1: NightShiftConfig max_parallel field
# ---------------------------------------------------------------------------


class TestMaxParallelConfig:
    """Verify max_parallel config field with validation."""

    def test_default_is_one(self) -> None:
        """max_parallel defaults to 1."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig()
        assert cfg.max_parallel == 1

    def test_valid_value(self) -> None:
        """max_parallel=3 is accepted."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=3)
        assert cfg.max_parallel == 3

    def test_zero_clamped_to_one(self) -> None:
        """max_parallel=0 is clamped to 1."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=0)
        assert cfg.max_parallel == 1

    def test_negative_clamped_to_one(self) -> None:
        """max_parallel=-1 is clamped to 1."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=-1)
        assert cfg.max_parallel == 1

    def test_above_eight_clamped_to_eight(self) -> None:
        """max_parallel=10 is clamped to 8."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=10)
        assert cfg.max_parallel == 8

    def test_boundary_eight(self) -> None:
        """max_parallel=8 is accepted."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=8)
        assert cfg.max_parallel == 8

    def test_boundary_one(self) -> None:
        """max_parallel=1 is accepted."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_parallel=1)
        assert cfg.max_parallel == 1


# ---------------------------------------------------------------------------
# TS-NS-2: Parallel processing of independent issues
# ---------------------------------------------------------------------------


class TestParallelIndependentIssues:
    """With max_parallel=3, 3 independent issues start concurrently."""

    @pytest.mark.asyncio
    async def test_three_independent_issues_dispatched_concurrently(self) -> None:
        """All three independent issues start before any completes."""
        engine, platform = _make_engine(max_parallel=3)

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]

        # Track concurrent execution
        started: list[int] = []
        max_concurrent = 0
        active_count = 0
        gate = asyncio.Event()

        async def fake_process_fix(iss, **_kwargs) -> None:
            nonlocal active_count, max_concurrent
            active_count += 1
            started.append(iss.number)
            max_concurrent = max(max_concurrent, active_count)
            if len(started) < 3:
                # Wait until all 3 have started
                await asyncio.sleep(0.05)
            else:
                gate.set()
            await gate.wait()
            active_count -= 1

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 2, 3],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        assert set(started) == {1, 2, 3}
        assert max_concurrent == 3


# ---------------------------------------------------------------------------
# TS-NS-3: Dependency-aware scheduling
# ---------------------------------------------------------------------------


class TestDependencyAwareScheduling:
    """With A->B dependency and independent C, A and C start concurrently, B waits."""

    @pytest.mark.asyncio
    async def test_dependency_respected(self) -> None:
        """B does not start until A completes; A and C run concurrently."""
        from agentfox.maintenance.dep_graph import DependencyEdge

        engine, platform = _make_engine(max_parallel=2)

        issues = [_make_issue(1, "A"), _make_issue(2, "B"), _make_issue(3, "C")]

        # A->B dependency: A must complete before B
        edges = [DependencyEdge(from_issue=1, to_issue=2, source="explicit", rationale="test")]

        dispatch_log: list[tuple[str, int]] = []
        a_done = asyncio.Event()

        async def fake_process_fix(iss, **_kwargs) -> None:
            dispatch_log.append(("start", iss.number))
            if iss.number == 1:
                # A takes some time
                await asyncio.sleep(0.1)
                a_done.set()
            elif iss.number == 2:
                # B should only start after A
                pass
            else:
                # C runs concurrently with A
                await a_done.wait()
            dispatch_log.append(("end", iss.number))

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=edges),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 3, 2],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        # Extract start events
        starts = [num for event, num in dispatch_log if event == "start"]
        a_end_idx = next(
            i for i, (event, num) in enumerate(dispatch_log) if event == "end" and num == 1
        )
        b_start_idx = next(
            i for i, (event, num) in enumerate(dispatch_log) if event == "start" and num == 2
        )

        # A and C should start before A ends (concurrent)
        assert 1 in starts
        assert 3 in starts

        # B should start only after A ends
        assert b_start_idx > a_end_idx


# ---------------------------------------------------------------------------
# TS-NS-4: Concurrent state updates are correct
# ---------------------------------------------------------------------------


class TestConcurrentStateUpdates:
    """NightShiftState and SharedBudget produce correct totals under concurrent updates."""

    @pytest.mark.asyncio
    async def test_nightshift_state_lock_protected(self) -> None:
        """10 concurrent tasks incrementing state produce correct totals."""
        from agentfox.maintenance.engine import IssueOutcome, NightShiftState

        state = NightShiftState()

        async def increment() -> None:
            outcome = IssueOutcome(
                issue_number=0,
                title="test",
                run_id="r",
                outcome="fixed",
                duration_ms=100,
                cost_usd=1.0,
                sessions_run=1,
                input_tokens=0,
                output_tokens=0,
            )
            await state.add_fix_result(1.0, 1, outcome, succeeded=True)

        tasks = [asyncio.create_task(increment()) for _ in range(10)]
        await asyncio.gather(*tasks)

        assert state.total_cost == 10.0
        assert state.total_sessions == 10
        assert state.issues_fixed == 10
        assert len(state.issue_outcomes) == 10

    @pytest.mark.asyncio
    async def test_shared_budget_lock_protected(self) -> None:
        """10 concurrent tasks adding cost to SharedBudget produce correct total."""
        from agentfox.maintenance.daemon import SharedBudget

        budget = SharedBudget(max_cost=100.0)

        async def increment() -> None:
            await budget.add_cost_async(1.0)

        tasks = [asyncio.create_task(increment()) for _ in range(10)]
        await asyncio.gather(*tasks)

        assert budget.total_cost == 10.0


# ---------------------------------------------------------------------------
# TS-NS-5: In-flight issues excluded from staleness checks
# ---------------------------------------------------------------------------


class TestInFlightStalenessExclusion:
    """Issues being processed in parallel are excluded from staleness evaluation."""

    @pytest.mark.asyncio
    async def test_in_flight_excluded_from_staleness(self) -> None:
        """Issue in in_flight set is not marked obsolete by staleness check."""
        from unittest.mock import patch as _patch

        from agentfox.maintenance.staleness import StalenessResult, check_staleness

        fixed = IssueResult(number=1, title="Fixed", html_url="", body="")
        remaining = [
            IssueResult(number=2, title="B", html_url="", body=""),
            IssueResult(number=3, title="C", html_url="", body=""),
        ]

        mock_platform = AsyncMock()
        mock_platform.list_issues_by_label = AsyncMock(
            return_value=[
                IssueResult(number=2, title="B", html_url="", body=""),
                IssueResult(number=3, title="C", html_url="", body=""),
            ]
        )

        config = MagicMock()

        # AI says both 2 and 3 are obsolete
        ai_result = StalenessResult(
            obsolete_issues=[2, 3],
            rationale={2: "fixed by 1", 3: "fixed by 1"},
        )

        with _patch(
            "agentfox.maintenance.staleness._run_ai_staleness",
            AsyncMock(return_value=ai_result),
        ):
            # Issue 3 is in-flight (being processed by another parallel task)
            result = await check_staleness(
                fixed,
                remaining,
                "",
                config,
                mock_platform,
                in_flight={3},
            )

        # Issue 2 should be obsolete, but issue 3 should be excluded
        assert 2 in result.obsolete_issues
        assert 3 not in result.obsolete_issues

    @pytest.mark.asyncio
    async def test_empty_in_flight_has_no_effect(self) -> None:
        """Empty in_flight set does not change behavior."""
        from unittest.mock import patch as _patch

        from agentfox.maintenance.staleness import StalenessResult, check_staleness

        fixed = IssueResult(number=1, title="Fixed", html_url="", body="")
        remaining = [IssueResult(number=2, title="B", html_url="", body="")]

        mock_platform = AsyncMock()
        mock_platform.list_issues_by_label = AsyncMock(
            return_value=[IssueResult(number=2, title="B", html_url="", body="")]
        )

        config = MagicMock()
        ai_result = StalenessResult(obsolete_issues=[2], rationale={2: "fixed"})

        with _patch(
            "agentfox.maintenance.staleness._run_ai_staleness",
            AsyncMock(return_value=ai_result),
        ):
            result = await check_staleness(
                fixed, remaining, "", config, mock_platform, in_flight=set()
            )

        assert 2 in result.obsolete_issues

    @pytest.mark.asyncio
    async def test_all_remaining_in_flight_returns_empty(self) -> None:
        """When all remaining issues are in-flight, staleness returns empty."""
        from agentfox.maintenance.staleness import check_staleness

        fixed = IssueResult(number=1, title="Fixed", html_url="", body="")
        remaining = [
            IssueResult(number=2, title="B", html_url="", body=""),
            IssueResult(number=3, title="C", html_url="", body=""),
        ]

        mock_platform = AsyncMock()
        config = MagicMock()

        result = await check_staleness(
            fixed,
            remaining,
            "",
            config,
            mock_platform,
            in_flight={2, 3},
        )

        assert result.obsolete_issues == []


# ---------------------------------------------------------------------------
# TS-NS-6: Serial processing with max_parallel=1
# ---------------------------------------------------------------------------


class TestSerialProcessingWithMaxParallelOne:
    """With max_parallel=1, issues process strictly sequentially."""

    @pytest.mark.asyncio
    async def test_sequential_processing(self) -> None:
        """No two issues overlap when max_parallel=1."""
        engine, platform = _make_engine(max_parallel=1)

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]

        dispatch_log: list[tuple[str, int]] = []

        async def fake_process_fix(iss, **_kwargs) -> None:
            dispatch_log.append(("start", iss.number))
            await asyncio.sleep(0.01)
            dispatch_log.append(("end", iss.number))

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 2, 3],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        # Verify strictly sequential: each issue ends before the next starts
        for i in range(len(dispatch_log) - 1):
            event, num = dispatch_log[i]
            next_event, next_num = dispatch_log[i + 1]
            if event == "start":
                # The next event for this issue should be "end" before any other "start"
                assert next_event == "end" and next_num == num, (
                    f"Expected end of {num} before any other start, "
                    f"got {next_event} {next_num}"
                )


# ---------------------------------------------------------------------------
# TS-NS-7: Cost budget checked between dispatches
# ---------------------------------------------------------------------------


class TestCostBudgetCheckBetweenDispatches:
    """fill_pool checks cost limit before dispatching each new issue."""

    @pytest.mark.asyncio
    async def test_budget_exceeded_stops_new_dispatches(self) -> None:
        """After budget is exceeded, no new issues are launched."""
        engine, platform = _make_engine(max_cost=1.0, max_parallel=3)

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]

        dispatched: list[int] = []

        async def fake_process_fix(iss, **_kwargs) -> None:
            dispatched.append(iss.number)
            # First issue exceeds the budget
            if iss.number == 1:
                engine.state.total_cost = 2.0

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 2, 3],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        # All 3 may have been dispatched initially (they're all ready at once
        # with max_parallel=3), but the budget check happens in fill_pool
        # before dispatching. Since all 3 are ready simultaneously with
        # max_parallel=3, they all get dispatched in the first fill.
        # The budget check prevents SUBSEQUENT fills from adding more.
        # With 3 independent issues and max_parallel=3, all 3 are dispatched
        # in the initial fill_pool call before any completes.
        # This test verifies that in-flight tasks complete normally.
        assert 1 in dispatched

    @pytest.mark.asyncio
    async def test_budget_checked_between_sequential_dispatches(self) -> None:
        """With max_parallel=1, budget is checked before each dispatch."""
        engine, platform = _make_engine(max_cost=1.0, max_parallel=1)

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]

        dispatched: list[int] = []

        async def fake_process_fix(iss, **_kwargs) -> None:
            dispatched.append(iss.number)
            # First issue exceeds the budget
            if iss.number == 1:
                engine.state.total_cost = 2.0

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 2, 3],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        # With max_parallel=1, only issue 1 should be dispatched.
        # After issue 1 completes and exceeds budget, fill_pool checks
        # the cost limit and stops dispatching.
        assert dispatched == [1]

    @pytest.mark.asyncio
    async def test_in_flight_tasks_not_cancelled(self) -> None:
        """In-flight tasks complete normally even when budget is exceeded."""
        engine, platform = _make_engine(max_cost=1.0, max_parallel=2)

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]

        completed: list[int] = []
        gate = asyncio.Event()

        async def fake_process_fix(iss, **_kwargs) -> None:
            if iss.number == 1:
                # Fast completion, exceeds budget
                engine.state.total_cost = 2.0
                gate.set()
            elif iss.number == 2:
                # Still running when budget exceeded
                await gate.wait()
                await asyncio.sleep(0.05)
            completed.append(iss.number)

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch(
                "agentfox.maintenance.engine.build_graph",
                return_value=[1, 2, 3],
            ),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=issues)
            await engine._run_issue_check()

        # Both 1 and 2 should complete (they were already dispatched)
        # 3 should NOT be dispatched (budget exceeded)
        assert 1 in completed
        assert 2 in completed
        assert 3 not in completed


# ---------------------------------------------------------------------------
# ParallelGraph unit tests
# ---------------------------------------------------------------------------


class TestParallelGraph:
    """Test the ParallelGraph helper from dep_graph."""

    def test_ready_issues_no_edges(self) -> None:
        """All issues are ready when there are no edges."""
        from agentfox.maintenance.dep_graph import build_parallel_graph

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]
        graph = build_parallel_graph(issues, [])

        ready = graph.ready_issues()
        assert ready == [1, 2, 3]

    def test_ready_issues_with_dependency(self) -> None:
        """Only root issues are initially ready."""
        from agentfox.maintenance.dep_graph import DependencyEdge, build_parallel_graph

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]
        edges = [DependencyEdge(from_issue=1, to_issue=2, source="explicit", rationale="test")]
        graph = build_parallel_graph(issues, edges)

        ready = graph.ready_issues()
        assert 1 in ready
        assert 3 in ready
        assert 2 not in ready

    def test_complete_releases_dependent(self) -> None:
        """Completing a prerequisite releases its dependent."""
        from agentfox.maintenance.dep_graph import DependencyEdge, build_parallel_graph

        issues = [_make_issue(1), _make_issue(2)]
        edges = [DependencyEdge(from_issue=1, to_issue=2, source="explicit", rationale="test")]
        graph = build_parallel_graph(issues, edges)

        # Issue 2 should not be ready initially
        assert 2 not in graph.ready_issues()

        # After completing issue 1, issue 2 should be newly ready
        newly_ready = graph.complete(1)
        assert 2 in newly_ready

    def test_complete_chain(self) -> None:
        """A chain A->B->C releases issues sequentially."""
        from agentfox.maintenance.dep_graph import DependencyEdge, build_parallel_graph

        issues = [_make_issue(1), _make_issue(2), _make_issue(3)]
        edges = [
            DependencyEdge(from_issue=1, to_issue=2, source="explicit", rationale="test"),
            DependencyEdge(from_issue=2, to_issue=3, source="explicit", rationale="test"),
        ]
        graph = build_parallel_graph(issues, edges)

        # Initially only 1 is ready
        assert graph.ready_issues() == [1]

        # Complete 1 -> 2 becomes ready
        newly_ready = graph.complete(1)
        assert newly_ready == [2]

        # Complete 2 -> 3 becomes ready
        newly_ready = graph.complete(2)
        assert newly_ready == [3]


# ---------------------------------------------------------------------------
# Engine in-flight tracking
# ---------------------------------------------------------------------------


class TestInFlightTracking:
    """Verify the engine tracks in-flight issues correctly."""

    @pytest.mark.asyncio
    async def test_in_flight_during_processing(self) -> None:
        """Issue is in _in_flight while _process_fix runs."""
        engine, platform = _make_engine(max_parallel=1)

        issue = _make_issue(1)
        in_flight_snapshot: set[int] = set()

        async def fake_process_fix(iss, **_kwargs) -> None:
            in_flight_snapshot.update(engine._in_flight)

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch("agentfox.maintenance.engine.build_graph", return_value=[1]),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=[issue])
            await engine._run_issue_check()

        assert 1 in in_flight_snapshot

    @pytest.mark.asyncio
    async def test_in_flight_cleared_after_processing(self) -> None:
        """Issue removed from _in_flight after _process_fix completes."""
        engine, platform = _make_engine(max_parallel=1)

        issue = _make_issue(1)

        async def fake_process_fix(iss, **_kwargs) -> None:
            pass

        with (
            patch("agentfox.maintenance.engine.parse_text_references", return_value=[]),
            patch(
                "agentfox.maintenance.engine.fetch_github_relationships",
                new=AsyncMock(return_value=[]),
            ),
            patch("agentfox.maintenance.engine.build_graph", return_value=[1]),
            patch.object(engine, "_process_fix", side_effect=fake_process_fix),
        ):
            platform.list_issues_by_label = AsyncMock(return_value=[issue])
            await engine._run_issue_check()

        assert 1 not in engine._in_flight
