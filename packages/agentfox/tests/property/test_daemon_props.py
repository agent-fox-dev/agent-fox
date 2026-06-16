"""Property tests for daemon framework.

Test Spec: TS-85-P1 through TS-85-P4
Properties: 1-4 from design.md
Requirements: 85-REQ-1.4, 85-REQ-1.E1, 85-REQ-2.1, 85-REQ-2.E1,
              85-REQ-3.1, 85-REQ-3.2, 85-REQ-5.1, 85-REQ-5.2, 85-REQ-5.E1
"""

from __future__ import annotations

import asyncio
import os
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_mock_stream(
    name: str = "test",
    interval: int = 1,
    enabled: bool = True,
    fail: bool = False,
) -> MagicMock:
    """Create a mock WorkStream."""
    stream = MagicMock()
    stream.name = name
    stream.interval = interval
    stream.enabled = enabled
    if fail:
        stream.run_once = AsyncMock(side_effect=RuntimeError("test failure"))
    else:
        stream.run_once = AsyncMock()
    stream.shutdown = AsyncMock()
    return stream


def _make_config() -> MagicMock:
    config = MagicMock()
    ns = MagicMock()
    ns.issue_check_interval = 900
    config.night_shift = ns
    return config


# ---------------------------------------------------------------------------
# TS-85-P1: PID mutual exclusion
# Property 1: check_pid_file returns ALIVE only for actually alive processes
# Validates: 85-REQ-2.1, 85-REQ-2.E1, 85-REQ-3.1, 85-REQ-3.2
# ---------------------------------------------------------------------------


class TestPidMutualExclusion:
    """PID file mechanism ensures at most one daemon runs at a time."""

    @given(pid=st.integers(min_value=1, max_value=2**31))
    @settings(
        max_examples=50,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_pid_status_matches_process_liveness(
        self, pid: int, tmp_path: Path
    ) -> None:
        """check_pid_file returns ALIVE for alive PIDs, STALE for dead ones."""
        from agentfox.nightshift.pid import PidStatus, check_pid_file

        pid_path = tmp_path / f"daemon_{pid}.pid"
        pid_path.write_text(str(pid))
        status, read_pid = check_pid_file(pid_path)
        assert read_pid == pid

        # Determine if process is alive
        try:
            os.kill(pid, 0)
            alive = True
        except ProcessLookupError:
            alive = False
        except PermissionError:
            # Process exists but we lack permission -- treat as alive.
            alive = True
        except (OverflowError, OSError):
            # PID out of valid range -- not alive.
            alive = False

        if alive:
            assert status == PidStatus.ALIVE
        else:
            assert status == PidStatus.STALE

    def test_write_then_check_returns_alive(self, tmp_path: Path) -> None:
        """write_pid_file + check_pid_file returns ALIVE for current process."""
        from agentfox.nightshift.pid import (
            PidStatus,
            check_pid_file,
            write_pid_file,
        )

        pid_path = tmp_path / "daemon.pid"
        write_pid_file(pid_path)
        status, pid = check_pid_file(pid_path)
        assert status == PidStatus.ALIVE
        assert pid == os.getpid()


# ---------------------------------------------------------------------------
# TS-85-P2: Cost monotonicity and limit
# Property 2: SharedBudget total cost is monotonically non-decreasing
# Validates: 85-REQ-5.1, 85-REQ-5.2, 85-REQ-5.E1
# ---------------------------------------------------------------------------


class TestCostMonotonicity:
    """SharedBudget total cost is monotonically non-decreasing."""

    @given(
        costs=st.lists(
            st.floats(
                min_value=0.0,
                max_value=100.0,
                allow_nan=False,
                allow_infinity=False,
            ),
            max_size=20,
        ),
        max_cost=st.one_of(
            st.none(),
            st.floats(
                min_value=0.0,
                max_value=1000.0,
                allow_nan=False,
                allow_infinity=False,
            ),
        ),
    )
    @settings(max_examples=100)
    def test_cost_monotonicity_and_exceeded(
        self, costs: list[float], max_cost: float | None
    ) -> None:
        """total_cost equals sum of add_cost calls; exceeded triggers correctly."""
        from agentfox.nightshift.daemon import SharedBudget

        budget = SharedBudget(max_cost=max_cost)
        running_total = 0.0
        for cost in costs:
            budget.add_cost(cost)
            running_total += cost
            assert abs(budget.total_cost - running_total) < 1e-9
            if max_cost is not None:
                assert budget.exceeded == (running_total >= max_cost)
            else:
                assert budget.exceeded is False


# ---------------------------------------------------------------------------
# TS-85-P3: Stream isolation
# Property 3: A failing stream never prevents other streams from running
# Validates: 85-REQ-1.4, 85-REQ-1.E1
# ---------------------------------------------------------------------------


class TestStreamIsolation:
    """A failing stream never prevents other streams from running."""

    @given(
        n=st.integers(min_value=2, max_value=5),
        fail_index=st.integers(min_value=0, max_value=4),
    )
    @settings(
        max_examples=20,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_failing_stream_does_not_block_others(
        self, n: int, fail_index: int, tmp_path: Path
    ) -> None:
        """Non-failing streams run even when one stream always fails."""
        from agentfox.nightshift.daemon import DaemonRunner, SharedBudget

        fail_index = fail_index % n
        streams = []
        for i in range(n):
            streams.append(
                _make_mock_stream(
                    name=f"stream-{i}",
                    fail=(i == fail_index),
                )
            )

        budget = SharedBudget(max_cost=None)
        config = _make_config()
        runner = DaemonRunner(
            config, None, streams, budget, pid_path=tmp_path / "d.pid"
        )  # type: ignore[arg-type]

        async def run_briefly() -> None:
            t = asyncio.create_task(runner.run())
            await asyncio.sleep(0.2)
            runner.request_shutdown()
            await t

        asyncio.run(run_briefly())

        for i, s in enumerate(streams):
            assert s.run_once.call_count >= 1, f"stream-{i} was not called"


# ---------------------------------------------------------------------------
# TS-85-P4: Shutdown completeness
# Property 4: Every registered stream's shutdown() is called
# Validates: 85-REQ-2.2, 85-REQ-2.4, 85-REQ-2.5
# ---------------------------------------------------------------------------


class TestShutdownCompleteness:
    """Every registered stream's shutdown() is called on graceful shutdown."""

    @given(n=st.integers(min_value=1, max_value=8))
    @settings(
        max_examples=20,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_all_streams_shutdown(self, n: int, tmp_path: Path) -> None:
        """After run() returns, shutdown() called on all N streams."""
        from agentfox.nightshift.daemon import DaemonRunner, SharedBudget

        streams = [_make_mock_stream(name=f"s-{i}") for i in range(n)]
        budget = SharedBudget(max_cost=None)
        config = _make_config()
        runner = DaemonRunner(
            config, None, streams, budget, pid_path=tmp_path / "d.pid"
        )  # type: ignore[arg-type]
        runner.request_shutdown()

        async def run_and_check() -> None:
            await runner.run()

        asyncio.run(run_and_check())

        for i, s in enumerate(streams):
            assert s.shutdown.call_count == 1, (
                f"stream s-{i} shutdown not called"
            )
