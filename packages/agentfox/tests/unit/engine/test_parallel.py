"""Parallel runner tests: concurrent dispatch, dependency ordering, state safety.

Test Spec: TS-04-12 (concurrent dispatch), TS-04-13 (respects dependencies),
           TS-04-14 (serialized state writes), TS-04-E5 (parallelism clamped),
           TS-04-E6 (fewer tasks than parallelism)
Requirements: 04-REQ-6.1, 04-REQ-6.2, 04-REQ-6.3, 04-REQ-6.E1
"""

from __future__ import annotations

import asyncio
import time

import pytest
from agentfox.engine.parallel import ParallelRunner
from agentfox.engine.state import SessionRecord

# -- Mock session runner for parallel tests ----------------------------------


class MockParallelSessionRunner:
    """Records dispatch timestamps and supports configurable delays."""

    def __init__(self, delay: float = 0.1) -> None:
        self.dispatch_times: dict[str, float] = {}
        self.complete_times: dict[str, float] = {}
        self._delay = delay
        self._lock = asyncio.Lock()
        self._concurrent_count = 0
        self.max_concurrent = 0

    async def __call__(
        self,
        node_id: str,
        attempt: int,
        previous_error: str | None = None,
    ) -> SessionRecord:
        self.dispatch_times[node_id] = time.monotonic()

        async with self._lock:
            self._concurrent_count += 1
            self.max_concurrent = max(
                self.max_concurrent,
                self._concurrent_count,
            )

        await asyncio.sleep(self._delay)

        async with self._lock:
            self._concurrent_count -= 1

        self.complete_times[node_id] = time.monotonic()
        return SessionRecord(
            node_id=node_id,
            attempt=attempt,
            status="completed",
            input_tokens=100,
            output_tokens=200,
            cost=0.10,
            duration_ms=int(self._delay * 1000),
            error_message=None,
            timestamp="2026-03-01T10:00:00Z",
        )


# -- Tests -------------------------------------------------------------------


class TestParallelismClamped:
    """TS-04-E5: Parallelism clamped to 8.

    Verify parallelism values above 8 are clamped.
    """

    def test_max_parallelism_capped_at_8(self) -> None:
        """ParallelRunner clamps max_parallelism to 8."""
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: MockParallelSessionRunner(),
            max_parallelism=16,
            inter_session_delay=0,
        )

        assert runner._max_parallelism == 8

    def test_max_parallelism_8_unchanged(self) -> None:
        """max_parallelism=8 is not changed."""
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: MockParallelSessionRunner(),
            max_parallelism=8,
            inter_session_delay=0,
        )

        assert runner._max_parallelism == 8

    def test_max_parallelism_under_8_unchanged(self) -> None:
        """max_parallelism < 8 is not clamped."""
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: MockParallelSessionRunner(),
            max_parallelism=4,
            inter_session_delay=0,
        )

        assert runner._max_parallelism == 4


class TestExecuteOne:
    """Tests for the execute_one method used by streaming pool dispatch."""

    @pytest.mark.asyncio
    async def test_execute_one_returns_record(self) -> None:
        """execute_one returns a SessionRecord on success."""
        mock = MockParallelSessionRunner(delay=0.01)
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: mock,
            max_parallelism=4,
            inter_session_delay=0,
        )

        record = await runner.execute_one("A", 1, None)

        assert record.node_id == "A"
        assert record.status == "completed"

    @pytest.mark.asyncio
    async def test_execute_one_handles_exception(self) -> None:
        """execute_one returns a failed record on exception."""

        class FailingRunner:
            async def execute(
                self,
                node_id: str,
                attempt: int,
                previous_error: str | None = None,
            ) -> SessionRecord:
                raise RuntimeError("session crashed")

        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: FailingRunner(),
            max_parallelism=4,
            inter_session_delay=0,
        )

        record = await runner.execute_one("A", 1, None)

        assert record.node_id == "A"
        assert record.status == "failed"
        assert "session crashed" in (record.error_message or "")

    @pytest.mark.asyncio
    async def test_track_tasks_updates_in_flight(self) -> None:
        """track_tasks updates the in-flight task list for cancellation."""
        mock = MockParallelSessionRunner(delay=0.5)
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: mock,
            max_parallelism=4,
            inter_session_delay=0,
        )

        task = asyncio.create_task(runner.execute_one("A", 1, None))
        runner.track_tasks([task])

        assert len(runner._in_flight_tasks) == 1

        await runner.cancel_all()

        assert len(runner._in_flight_tasks) == 0

    @pytest.mark.asyncio
    async def test_max_parallelism_property(self) -> None:
        """max_parallelism property returns the effective value."""
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: MockParallelSessionRunner(),
            max_parallelism=3,
            inter_session_delay=0,
        )
        assert runner.max_parallelism == 3


class TestCancelAllReturnFailureRecords:
    """536-AC-1/AC-2: cancel_all() returns SessionRecords for cancelled tasks.

    When SIGINT cancels in-flight parallel tasks, cancel_all() must return
    synthesised failure SessionRecords so the caller can persist
    session_outcomes rows for every interrupted session.

    Requirements: 536-AC-1, 536-AC-2, 536-AC-3
    """

    @pytest.mark.asyncio
    async def test_cancel_all_returns_failure_records_for_cancelled_tasks(self) -> None:
        """cancel_all() returns one failure SessionRecord per cancelled task."""
        # Use a long-running session so the tasks are in-flight when cancelled.
        mock = MockParallelSessionRunner(delay=10.0)
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: mock,
            max_parallelism=4,
            inter_session_delay=0,
        )

        task_a = asyncio.create_task(
            runner.execute_one("spec:1", 1, None),
            name="parallel-spec:1",
        )
        task_b = asyncio.create_task(
            runner.execute_one("spec:2", 1, None),
            name="parallel-spec:2",
        )
        runner.track_tasks([task_a, task_b])

        records = await runner.cancel_all()

        # Both tasks were cancelled → two failure records returned
        assert len(records) == 2
        node_ids = {r.node_id for r in records}
        assert node_ids == {"spec:1", "spec:2"}
        for record in records:
            assert record.status == "failed"
            assert record.error_message is not None

    @pytest.mark.asyncio
    async def test_cancel_all_returns_empty_when_no_tasks(self) -> None:
        """cancel_all() returns an empty list when there are no in-flight tasks."""
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: MockParallelSessionRunner(),
            max_parallelism=4,
            inter_session_delay=0,
        )

        records = await runner.cancel_all()

        assert records == []

    @pytest.mark.asyncio
    async def test_cancel_all_clears_in_flight_tasks(self) -> None:
        """cancel_all() clears _in_flight_tasks after cancellation."""
        mock = MockParallelSessionRunner(delay=10.0)
        runner = ParallelRunner(
            session_runner_factory=lambda nid, **kw: mock,
            max_parallelism=4,
            inter_session_delay=0,
        )

        task = asyncio.create_task(
            runner.execute_one("spec:1", 1, None),
            name="parallel-spec:1",
        )
        runner.track_tasks([task])
        assert len(runner._in_flight_tasks) == 1

        await runner.cancel_all()

        assert len(runner._in_flight_tasks) == 0
