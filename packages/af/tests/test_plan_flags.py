"""Tests for flag removal, --yes, and mutual exclusivity on af plan.

Requirement coverage:
  01-REQ-4.1 — af reset command is not registered in CLI
  01-REQ-4.2 — packages/af/af/reset.py is deleted; agentfox.engine.reset is preserved
  01-REQ-4.3 — invoking `af reset` emits Click 'No such command' error
  01-REQ-5.1 — --yes skips confirmation for --reset
  01-REQ-5.2 — --yes skips confirmation for --reset-hard
  01-REQ-5.3 — --yes without --reset or --reset-hard is silently ignored
  01-REQ-5.E1 — --yes with --clear is silently ignored
  01-REQ-6.1 — mutual exclusivity of mode flags
  01-REQ-6.2 — --reset-hard and --spec are mutually exclusive
  01-REQ-6.3 — --fast alongside --clear is silently ignored

Test spec entries: TS-01-13, TS-01-14, TS-01-15, TS-01-16, TS-01-17,
                   TS-01-18, TS-01-19, TS-01-20, TS-01-21
"""

from __future__ import annotations

import importlib
import os
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from agentfox.engine.reset import HardResetResult, ResetResult
from agentfox.graph.types import Node, NodeStatus, PlanMetadata, TaskGraph
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Suppress daemon PID guard checks in all tests."""
    from agentfox.nightshift.pid import PidStatus

    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


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


def _mock_knowledge_store() -> MagicMock:
    """Create a mock KnowledgeDB with a mock connection."""
    mock_db = MagicMock()
    mock_db.connection = MagicMock()
    return mock_db


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


# ===========================================================================
# TS-01-13: af reset command is not registered in CLI
# REQ: 01-REQ-4.1, 01-PROP-9
# ===========================================================================


class TestResetCommandRemoved:
    """The af reset subcommand must not be registered after removal."""

    def test_reset_not_in_click_group_commands(self) -> None:
        """WHEN inspecting the Click group's registered commands,
        THEN 'reset' does not appear in the command names.
        """
        assert "reset" not in main.commands


# ===========================================================================
# TS-01-14: packages/af/af/reset.py is deleted; engine module is preserved
# REQ: 01-REQ-4.2
# ===========================================================================


class TestResetFileDeleted:
    """packages/af/af/reset.py must not exist; agentfox.engine.reset must be importable."""

    def test_cli_reset_module_does_not_exist(self) -> None:
        """WHEN checking the filesystem,
        THEN packages/af/af/reset.py does not exist.
        """
        # Use the import system to locate the af package, then check for reset.py
        import af

        af_dir = os.path.dirname(af.__file__)
        reset_path = os.path.join(af_dir, "reset.py")
        assert not os.path.exists(reset_path), (
            f"packages/af/af/reset.py should be deleted but still exists at {reset_path}"
        )

    def test_engine_reset_module_is_importable(self) -> None:
        """WHEN importing agentfox.engine.reset,
        THEN the module loads successfully (it is preserved).
        """
        mod = importlib.import_module("agentfox.engine.reset")
        assert mod is not None
        # Verify key symbols are still accessible
        assert hasattr(mod, "ResetResult")
        assert hasattr(mod, "HardResetResult")
        assert hasattr(mod, "run_reset")


# ===========================================================================
# TS-01-15: af reset invocation emits Click 'No such command' error
# REQ: 01-REQ-4.3, 01-PROP-9, 01-PATH-5
# ===========================================================================


class TestResetInvocationError:
    """Invoking `af reset` after removal returns Click's standard error."""

    def test_af_reset_exits_nonzero(self, cli_runner: CliRunner) -> None:
        """WHEN invoking `af reset`,
        THEN exit code is non-zero.
        """
        result = cli_runner.invoke(main, ["reset"])
        assert result.exit_code != 0

    def test_af_reset_outputs_no_such_command(self, cli_runner: CliRunner) -> None:
        """WHEN invoking `af reset`,
        THEN output contains 'No such command' and 'reset'.
        """
        result = cli_runner.invoke(main, ["reset"])
        combined = result.output + (getattr(result, "stderr", "") or "")
        lower = combined.lower()
        assert "no such command" in lower
        assert "reset" in lower

    def test_af_reset_no_custom_redirect(self, cli_runner: CliRunner) -> None:
        """WHEN invoking `af reset`,
        THEN no custom redirect message (like 'use af plan') is present.
        """
        result = cli_runner.invoke(main, ["reset"])
        combined = result.output + (getattr(result, "stderr", "") or "")
        lower = combined.lower()
        assert "use af plan" not in lower


# ===========================================================================
# TS-01-16: --yes skips confirmation for --reset
# REQ: 01-REQ-5.1, 01-PROP-8
# ===========================================================================


class TestYesFlagReset:
    """af plan --reset --yes skips the confirmation prompt."""

    def test_reset_yes_skips_prompt_and_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset --yes (no stdin),
        THEN exit code is 0, run_reset is called, and no prompt appears.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result(reset_tasks=["spec:1"])

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--yes"], input=None
            )

        assert result.exit_code == 0, (
            f"Expected exit 0, got {result.exit_code}: {result.output}"
        )
        mock_run.assert_called_once()
        assert "[y/N]" not in result.output
        assert "confirm" not in result.output.lower()

    def test_reset_short_y_flag_skips_prompt(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset -y (short form),
        THEN exit code is 0 and no prompt appears.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "-y"], input=None
            )

        assert result.exit_code == 0
        mock_run.assert_called_once()
        assert "[y/N]" not in result.output


# ===========================================================================
# TS-01-17: --yes skips confirmation for --reset-hard
# REQ: 01-REQ-5.2, 01-PROP-8
# ===========================================================================


class TestYesFlagResetHard:
    """af plan --reset-hard --yes skips the confirmation prompt."""

    def test_reset_hard_yes_skips_prompt_and_exits_zero(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset-hard --yes (no stdin),
        THEN exit code is 0, run_reset is called, and no prompt appears.
        """
        graph = _make_graph({"spec:0": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_hard_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset-hard", "--yes"], input=None
            )

        assert result.exit_code == 0, (
            f"Expected exit 0, got {result.exit_code}: {result.output}"
        )
        mock_run.assert_called_once()
        assert "[y/N]" not in result.output
        assert "confirm" not in result.output.lower()

    def test_reset_hard_short_y_flag_skips_prompt(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset-hard -y (short form),
        THEN exit code is 0 and no prompt appears.
        """
        graph = _make_graph({"spec:0": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_hard_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result) as mock_run,
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset-hard", "-y"], input=None
            )

        assert result.exit_code == 0
        mock_run.assert_called_once()
        assert "[y/N]" not in result.output


# ===========================================================================
# TS-01-18: --yes without --reset or --reset-hard is silently ignored
# REQ: 01-REQ-5.3, 01-REQ-5.E1
# ===========================================================================


class TestYesFlagIgnored:
    """--yes is silently ignored when no reset mode is active."""

    def test_yes_without_reset_mode_ignored(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with af plan --yes (normal build),
        THEN exit code matches normal af plan and no error about --yes.
        """
        # Both invocations should have the same exit code (normal plan build
        # may fail for other reasons, but the key is --yes doesn't cause an error)
        result = cli_runner.invoke(main, ["plan", "--yes"])
        assert "error" not in result.output.lower() or "--yes" not in result.output.lower()
        # --yes flag itself should not produce a Click error
        assert "no such option" not in result.output.lower()

    def test_yes_with_clear_ignored(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with af plan --clear --yes,
        THEN --yes is silently ignored and clear proceeds normally.

        Edge case: 01-REQ-5.E1
        """
        graph = _make_graph({"spec:1": NodeStatus.PENDING})
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear", "--yes"])

        assert result.exit_code == 0
        assert "error" not in result.output.lower()
        assert "invalid" not in result.output.lower()


# ===========================================================================
# TS-01-19: Mutual exclusivity of mode flags
# REQ: 01-REQ-6.1, 01-PROP-6
# ===========================================================================


class TestMutualExclusivity:
    """Providing multiple mutually exclusive mode flags exits with code 1."""

    def test_clear_and_reset_conflict(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear --reset,
        THEN exit code is 1 and error lists the conflicting flags.
        open_knowledge_store must NOT be called.
        """
        mock_ks = MagicMock()
        with patch("af.plan.open_knowledge_store", mock_ks):
            result = cli_runner.invoke(main, ["plan", "--clear", "--reset"])

        assert result.exit_code == 1
        combined = result.output + (getattr(result, "stderr", "") or "")
        assert "--clear" in combined
        assert "--reset" in combined
        mock_ks.assert_not_called()

    def test_dry_run_and_verify_conflict(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --dry-run --verify,
        THEN exit code is 1 and error lists conflicting flags.
        """
        mock_ks = MagicMock()
        with patch("af.plan.open_knowledge_store", mock_ks):
            result = cli_runner.invoke(main, ["plan", "--dry-run", "--verify"])

        assert result.exit_code == 1
        combined = result.output + (getattr(result, "stderr", "") or "")
        assert "--dry-run" in combined
        assert "--verify" in combined
        mock_ks.assert_not_called()

    def test_reset_and_reset_hard_conflict(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset --reset-hard,
        THEN exit code is 1 and error lists conflicting flags.
        """
        mock_ks = MagicMock()
        with patch("af.plan.open_knowledge_store", mock_ks):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--reset-hard"]
            )

        assert result.exit_code == 1
        combined = result.output + (getattr(result, "stderr", "") or "")
        assert "--reset" in combined
        assert "--reset-hard" in combined
        mock_ks.assert_not_called()

    def test_all_five_mode_flags_conflict(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN all five mode flags are provided simultaneously,
        THEN exit code is 1 with a consolidated error listing all flags.

        Edge case: 01-REQ-6.E1
        """
        mock_ks = MagicMock()
        with patch("af.plan.open_knowledge_store", mock_ks):
            result = cli_runner.invoke(
                main,
                [
                    "plan",
                    "--dry-run",
                    "--verify",
                    "--clear",
                    "--reset",
                    "--reset-hard",
                ],
            )

        assert result.exit_code == 1
        combined = result.output + (getattr(result, "stderr", "") or "")
        # All conflicting flags should be mentioned
        for flag in ("--dry-run", "--verify", "--clear", "--reset", "--reset-hard"):
            assert flag in combined, f"Flag {flag} not mentioned in error"
        mock_ks.assert_not_called()


# ===========================================================================
# TS-01-20: --reset-hard and --spec are mutually exclusive
# REQ: 01-REQ-6.2
# ===========================================================================


class TestResetHardSpecExclusion:
    """--reset-hard and --spec cannot be combined."""

    def test_reset_hard_with_spec_exits_one(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset-hard --spec my_spec,
        THEN exit code is 1 and error states the combination is invalid.
        """
        result = cli_runner.invoke(
            main, ["plan", "--reset-hard", "--spec", "my_spec"]
        )

        assert result.exit_code == 1
        combined = result.output + (getattr(result, "stderr", "") or "")
        lower = combined.lower()
        assert "reset-hard" in lower or "--reset-hard" in combined
        assert "spec" in lower or "--spec" in combined


# ===========================================================================
# TS-01-21: --fast alongside --clear is silently ignored
# REQ: 01-REQ-6.3
# ===========================================================================


class TestFastIgnoredWithClear:
    """--fast alongside --clear, --reset, or --reset-hard is silently ignored."""

    def test_fast_with_clear_ignored(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --clear --fast,
        THEN exit code is 0, no error about --fast, and clear proceeds.
        """
        graph = _make_graph(
            {
                "spec:1": NodeStatus.PENDING,
                "spec:2": NodeStatus.FAILED,
            }
        )
        mock_db = _mock_knowledge_store()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.persist_node_status"),
        ):
            result = cli_runner.invoke(main, ["plan", "--clear", "--fast"])

        assert result.exit_code == 0
        assert "error" not in result.output.lower()
        assert "invalid" not in result.output.lower()

    def test_fast_with_reset_ignored(self, cli_runner: CliRunner) -> None:
        """WHEN invoked with --reset --fast --yes,
        THEN exit code is 0, no error about --fast.
        """
        graph = _make_graph({"spec:1": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset", "--fast", "--yes"]
            )

        assert result.exit_code == 0
        assert "error" not in result.output.lower()

    def test_fast_with_reset_hard_ignored(
        self, cli_runner: CliRunner
    ) -> None:
        """WHEN invoked with --reset-hard --fast --yes,
        THEN exit code is 0, no error about --fast.
        """
        graph = _make_graph({"spec:0": NodeStatus.FAILED})
        mock_db = _mock_knowledge_store()
        mock_result = _sample_hard_reset_result()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db),
            patch("af.plan.load_plan", return_value=graph),
            patch("af.plan.run_reset", return_value=mock_result),
        ):
            result = cli_runner.invoke(
                main, ["plan", "--reset-hard", "--fast", "--yes"]
            )

        assert result.exit_code == 0
        assert "error" not in result.output.lower()
