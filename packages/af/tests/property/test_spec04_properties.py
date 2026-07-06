"""Property tests for af agentic CLI migration (PROP-1..5).

Test Spec: TS-04-P1, TS-04-P2, TS-04-P3, TS-04-P4, TS-04-P5
Requirements: 04-REQ-2.1, 04-REQ-3.5, 04-REQ-4.1, 04-REQ-6.4
"""

from __future__ import annotations

import json
import os
import random
from pathlib import Path

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"

# All subcommand files plus package-level files
_SUBCOMMAND_FILES = [
    "code.py",
    "plan.py",
    "standup.py",
    "findings.py",
]

_ALL_AF_PY_FILES = _SUBCOMMAND_FILES + ["__init__.py", "app.py"]

# Subcommands that support --json for property testing
_SUBCOMMANDS = [
    "code",
    "plan",
    "standup",
    "insights",
]


class TestProp1StdoutStderrSeparation:
    """TS-04-P1: stdout/stderr separation for JSONL streaming commands.

    For any invocation of af code with --json, every
    stdout line is valid JSON and every stderr line is a valid JSONL
    progress event; no cross-contamination occurs.

    Uses hypothesis to generate random task node counts (1-20), then
    validates the stdout/stderr invariant via ProgressDisplay + OutputManager
    directly (bypassing the CLI, which requires infrastructure).
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
        stderr_lines = [line for line in stderr_buf.getvalue().strip().splitlines() if line.strip()]
        assert len(stderr_lines) >= num_nodes, f"Expected at least {num_nodes} JSONL events, got {len(stderr_lines)}"
        for line in stderr_lines:
            obj = json.loads(line)
            assert "event" in obj, f"stderr line missing 'event' key: {line}"
            assert obj["event"] in (
                "task_started",
                "task_completed",
                "task_failed",
            )


class TestProp4JsonModeValidOutput:
    """TS-04-P4: JSON mode produces valid JSON on stdout.

    For any af command invoked with --json, stdout contains only valid
    JSON text and the process exits with code 0 on success.
    """

    @pytest.mark.parametrize(
        "command",
        [
            pytest.param(
                "code",
                marks=pytest.mark.xfail(
                    strict=True,
                    reason="'code' requires plan DB and orchestrator backend",
                ),
            ),
            "plan",
            "standup",
            "insights",
        ],
    )
    def test_json_mode_stdout_is_valid_json(self, cli_runner, command: str) -> None:
        """af <cmd> --json produces valid JSON on stdout and exits 0."""
        from af.app import main

        result = cli_runner.invoke(main, [command, "--json"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


class TestProp2NoJsonIoReferences:
    """TS-04-P2: No af.json_io references in any af/ Python file."""

    @pytest.mark.parametrize("filename", _ALL_AF_PY_FILES)
    def test_no_json_io_in_source(self, filename: str) -> None:
        """'af.json_io' does not appear in the given af/ source file."""
        filepath = _AF_PACKAGE_DIR / filename
        if not filepath.exists():
            pytest.skip(f"{filename} does not exist")
        content = filepath.read_text()
        assert "af.json_io" not in content

    def test_json_io_file_absent(self) -> None:
        """af/json_io.py does not exist on disk."""
        assert not os.path.exists(_AF_PACKAGE_DIR / "json_io.py")


class TestProp3OutputManagerSoleChannel:
    """TS-04-P3: om.emit() is the sole data output channel.

    Verifies two complementary properties for each subcommand file:
    1. Positive: the file retrieves OutputManager and calls om.emit()
       for structured data output.
    2. Negative: click.echo is never used to emit JSON/structured data
       (i.e., no ``click.echo(json.dumps(...))``) patterns).

    Text-mode human output and stderr error messages via click.echo()
    are acceptable per 04-REQ-2.1, which requires om.emit() for
    *data payloads* specifically.
    """

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_om_emit_used_for_data(self, filename: str) -> None:
        """om.emit() is present in each subcommand file."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        has_emit = "om.emit(" in content or "output.emit(" in content
        assert has_emit, f"{filename} missing om.emit() call for data output"

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_click_echo_for_json_data(self, filename: str) -> None:
        """click.echo() is not used to emit JSON data in the given file.

        Checks that click.echo is never called with json.dumps() or
        similar JSON serialization, which would bypass OutputManager.
        """
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        assert "click.echo(json.dumps(" not in content, (
            f"{filename} uses click.echo(json.dumps(...)) — should use om.emit()"
        )
        assert "click.echo(emit" not in content, f"{filename} uses click.echo(emit*()) — should use om.emit()"


class TestProp5FormatTableKeyAlignment:
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
    def test_all_dicts_have_header_keys(self, headers: list[str], num_rows: int) -> None:
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
