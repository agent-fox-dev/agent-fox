"""CLI tests for spec-scoped reset (--spec option).

Test Spec: TS-50-8 through TS-50-11
Requirements: 50-REQ-2.1, 50-REQ-2.2, 50-REQ-3.1, 50-REQ-3.2, 50-REQ-3.4
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

from af.reset import reset_cmd
from agentfox.engine.state import ExecutionState
from click.testing import CliRunner


def _setup_project(
    tmp_path: Path,
    node_states: dict[str, str],
    nodes: dict[str, dict[str, str]] | None = None,
) -> None:
    """Create .agent-fox directory structure (state loaded from DB mock)."""
    agent_dir = tmp_path / ".agent-fox"
    agent_dir.mkdir(parents=True, exist_ok=True)
    (agent_dir / "worktrees").mkdir()


def _make_state(node_states: dict[str, str]) -> ExecutionState:
    """Create an ExecutionState for test mocking."""
    return ExecutionState(
        plan_hash="abc123",
        node_states=node_states,
        started_at="2026-03-01T09:00:00Z",
        updated_at="2026-03-01T10:00:00Z",
    )


# ---------------------------------------------------------------------------
# TS-50-8: Mutual exclusivity with --hard
# Requirement: 50-REQ-2.1
# ---------------------------------------------------------------------------


class TestMutualExclusivityHard:
    """TS-50-8: --spec combined with --hard produces an error."""

    def test_spec_and_hard_error(self, tmp_path: Path) -> None:
        """Non-zero exit and mutually exclusive error message."""
        node_states = {"alpha:1": "completed"}
        _setup_project(tmp_path, node_states)

        runner = CliRunner()
        with (
            patch("af.reset.Path.cwd", return_value=tmp_path),
            patch("af.reset._get_db_conn", return_value=MagicMock()),
            patch("agentfox.engine.reset.load_state_from_db", return_value=_make_state(node_states)),
        ):
            result = runner.invoke(reset_cmd, ["--spec", "alpha", "--hard"], catch_exceptions=False)

        assert result.exit_code != 0
        assert "mutually exclusive" in result.output.lower()


# ---------------------------------------------------------------------------
# TS-50-9: Mutual exclusivity with task_id
# Requirement: 50-REQ-2.2
# ---------------------------------------------------------------------------


class TestMutualExclusivityTaskId:
    """TS-50-9: --spec combined with a positional task_id produces an error."""

    def test_spec_and_task_id_error(self, tmp_path: Path) -> None:
        """Non-zero exit and mutually exclusive error message."""
        node_states = {"alpha:1": "completed"}
        _setup_project(tmp_path, node_states)

        runner = CliRunner()
        with (
            patch("af.reset.Path.cwd", return_value=tmp_path),
            patch("af.reset._get_db_conn", return_value=MagicMock()),
            patch("agentfox.engine.reset.load_state_from_db", return_value=_make_state(node_states)),
        ):
            result = runner.invoke(
                reset_cmd,
                ["--spec", "alpha", "alpha:1"],
                catch_exceptions=False,
            )

        assert result.exit_code != 0
        assert "mutually exclusive" in result.output.lower()


# ---------------------------------------------------------------------------
# TS-50-10: Confirmation required
# Requirement: 50-REQ-3.1, 50-REQ-3.2
# ---------------------------------------------------------------------------


class TestConfirmationRequired:
    """TS-50-10: Without --yes, confirmation is prompted."""

    def test_decline_aborts(self, tmp_path: Path) -> None:
        """Declining confirmation leaves state unchanged."""
        node_states = {"alpha:1": "completed"}
        _setup_project(tmp_path, node_states)
        state = _make_state(node_states)

        runner = CliRunner()
        with (
            patch("af.reset.Path.cwd", return_value=tmp_path),
            patch("af.reset._get_db_conn", return_value=MagicMock()),
            patch("agentfox.engine.reset.load_state_from_db", return_value=state),
        ):
            runner.invoke(reset_cmd, ["--spec", "alpha"], input="n\n", catch_exceptions=False)

        # State should be unchanged (decline aborts before modifying)
        assert state.node_states["alpha:1"] == "completed"


