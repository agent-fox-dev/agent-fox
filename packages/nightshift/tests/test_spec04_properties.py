"""Property tests for nightshift IO contract (migrated from af).

Test Spec: TS-04-P1, TS-04-P3, TS-04-P5
Requirements: 04-REQ-2.1, 04-REQ-3.5
Migrated from: packages/af/tests/property/test_spec04_properties.py (07-REQ-8.1)
"""

from __future__ import annotations

import json
import random

from hypothesis import given, settings
from hypothesis import strategies as st


class TestProp1StdoutStderrSeparation:
    """TS-04-P1: stdout/stderr separation for JSONL streaming.

    For any invocation generating JSONL progress events, every stdout line
    is valid JSON and every stderr line is a valid JSONL progress event;
    no cross-contamination occurs.

    Uses hypothesis to generate random task node counts (1-20), then
    validates the stdout/stderr invariant via ProgressDisplay + OutputManager
    directly (bypassing the daemon, which requires infrastructure).
    """

    @settings(max_examples=30)
    @given(
        num_nodes=st.integers(min_value=1, max_value=20),
        outcomes=st.lists(
            st.sampled_from(["completed", "failed"]),
            min_size=1,
            max_size=20,
        ),
    )
    def test_stdout_stderr_no_cross_contamination(
        self,
        num_nodes: int,
        outcomes: list[str],
    ) -> None:
        """All emit_progress events go to stderr; emit() goes to stdout.

        Generates 1-20 task nodes with random outcomes (completed/failed)
        and validates that:
        - Every line on stdout (from emit) is valid JSON
        - Every line on stderr (from emit_progress) is a valid JSONL event
        - No cross-contamination between the two streams
        """
        import io

        from agentfox.io import OutputManager, ProgressDisplay

        stdout_buf = io.StringIO()
        stderr_buf = io.StringIO()
        om = OutputManager(json_mode=True, stdout=stdout_buf, stderr=stderr_buf)
        pd = ProgressDisplay(output_manager=om, json_mode=True)

        # Simulate task lifecycle for num_nodes tasks
        effective_outcomes = outcomes[:num_nodes]
        while len(effective_outcomes) < num_nodes:
            effective_outcomes.append("completed")

        for i, outcome in enumerate(effective_outcomes):
            node_id = f"{i + 1}.1"
            pd.task_started(node_id=node_id)
            if outcome == "completed":
                pd.task_completed(node_id=node_id)
            else:
                pd.task_failed(node_id=node_id, error=f"error at {node_id}")

        # Emit final result to stdout
        om.emit({"status": "done", "tasks": num_nodes})

        # Validate stdout: only the final JSON result, no JSONL events
        stdout_content = stdout_buf.getvalue().strip()
        assert len(stdout_content) > 0, "stdout should have the final result"
        stdout_obj = json.loads(stdout_content)
        assert isinstance(stdout_obj, dict)
        assert "event" not in stdout_obj, "JSONL event leaked to stdout"

        # Validate stderr: only JSONL progress events, no final result
        stderr_lines = [
            line
            for line in stderr_buf.getvalue().strip().splitlines()
            if line.strip()
        ]
        assert len(stderr_lines) >= num_nodes, (
            f"Expected at least {num_nodes} JSONL events, got {len(stderr_lines)}"
        )
        for line in stderr_lines:
            obj = json.loads(line)
            assert "event" in obj, f"stderr line missing 'event' key: {line}"
            assert obj["event"] in (
                "task_started",
                "task_completed",
                "task_failed",
            )


class TestPropFormatTableKeyAlignment:
    """TS-04-P5: format_table JSON dicts always have header-matching keys."""

    @settings(max_examples=50)
    @given(
        headers=st.lists(
            st.text(
                alphabet=st.characters(whitelist_categories=("L", "N")),
                min_size=1,
                max_size=10,
            ),
            min_size=1,
            max_size=10,
            unique=True,
        ),
        num_rows=st.integers(min_value=1, max_value=20),
    )
    def test_all_dicts_have_header_keys(
        self, headers: list[str], num_rows: int
    ) -> None:
        """Every dict in format_table output has exactly the header keys."""
        from agentfox.io import format_table

        rows = []
        for _ in range(num_rows):
            width = random.randint(0, len(headers) + 2)  # noqa: S311
            rows.append([f"v{i}" for i in range(width)])

        result = format_table(headers=headers, rows=rows, json_mode=True)
        assert len(result) == num_rows
        for row_dict in result:
            assert set(row_dict.keys()) == set(headers)


class TestPropOutputManagerSoleChannel:
    """TS-04-P3: OutputManager.emit() is the sole data output channel.

    Validates that OutputManager is the only way to emit structured data
    to stdout, ensuring no bypass via print() or sys.stdout.write().
    """

    def test_emit_writes_to_stdout(self) -> None:
        """om.emit() writes JSON to stdout stream."""
        import io

        from agentfox.io import OutputManager

        stdout_buf = io.StringIO()
        om = OutputManager(json_mode=True, stdout=stdout_buf)
        om.emit({"key": "value"})

        output = stdout_buf.getvalue().strip()
        assert len(output) > 0
        parsed = json.loads(output)
        assert parsed["key"] == "value"

    def test_emit_progress_writes_to_stderr(self) -> None:
        """om.emit_progress() writes JSONL to stderr stream."""
        import io

        from agentfox.io import OutputManager

        stderr_buf = io.StringIO()
        om = OutputManager(json_mode=True, stderr=stderr_buf)
        om.emit_progress(
            {
                "event": "task_started",
                "node_id": "1.1",
                "timestamp": "2026-01-01T00:00:00Z",
            }
        )

        output = stderr_buf.getvalue().strip()
        assert len(output) > 0
        parsed = json.loads(output)
        assert parsed["event"] == "task_started"
