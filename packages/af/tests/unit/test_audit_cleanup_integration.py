"""Integration tests for audit file cleanup at startup.

Covers:
- TS-NS-4: af nightshift calls purge_stale_audit_files in _run_daemon
- TS-NS-5: af plan and af standup do NOT call purge_stale_audit_files

Requirements: NS-REQ-4, NS-REQ-5
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest
from af.app import main
from agentfox.nightshift.pid import PidStatus
from click.testing import CliRunner


@pytest.fixture()
def cli_runner() -> CliRunner:
    return CliRunner()


class TestCodeCallsAuditCleanup:
    """TS-NS-1 (integration): af code invokes purge_stale_audit_files at startup."""

    def test_code_calls_purge_on_startup(self, cli_runner: CliRunner) -> None:
        """purge_stale_audit_files is called when af code runs."""
        from agentfox.engine.state import ExecutionState

        state = ExecutionState(
            plan_hash="x",
            node_states={"s:1": "completed"},
            run_status="completed",
            total_input_tokens=0,
            total_output_tokens=0,
            total_cost=0.0,
            total_sessions=0,
            started_at="2026-01-01T00:00:00+00:00",
            updated_at="2026-01-01T01:00:00+00:00",
        )
        with (
            patch("af.code.run_code", AsyncMock(return_value=state)),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH") as mock_db_path,
            patch("agentfox.nightshift.pid.check_pid_file", return_value=(PidStatus.ABSENT, None)),
            patch("agentfox.workspace.audit_cleanup.purge_stale_audit_files") as mock_purge,
        ):
            mock_db_path.exists.return_value = True
            result = cli_runner.invoke(main, ["code"])

        assert result.exit_code == 0, f"Unexpected exit code: {result.output}"
        mock_purge.assert_called_once()


class TestNightshiftCallsAuditCleanup:
    """TS-NS-4: _run_daemon calls purge_stale_audit_files before the work loop."""

    def test_nightshift_app_source_contains_purge_call(self) -> None:
        """nightshift/app.py source references purge_stale_audit_files."""
        import nightshift.app as app_mod

        source = Path(app_mod.__file__).read_text()
        assert "purge_stale_audit_files" in source, (
            "Expected purge_stale_audit_files call in nightshift/app.py"
        )

    def test_nightshift_app_purge_call_follows_merge_lock_cleanup(self) -> None:
        """purge_stale_audit_files appears after cleanup_stale_merge_lock in nightshift/app.py."""
        import nightshift.app as app_mod

        source = Path(app_mod.__file__).read_text()
        idx_merge = source.find("cleanup_stale_merge_lock")
        idx_purge = source.find("purge_stale_audit_files")
        assert idx_merge != -1, "cleanup_stale_merge_lock not found in nightshift/app.py"
        assert idx_purge != -1, "purge_stale_audit_files not found in nightshift/app.py"
        assert idx_purge > idx_merge, (
            "purge_stale_audit_files must appear after cleanup_stale_merge_lock in nightshift/app.py"
        )


class TestReadOnlyCommandsDoNotCleanup:
    """TS-NS-5: af plan and af standup must NOT trigger audit file cleanup."""

    def test_plan_source_does_not_contain_purge_call(self) -> None:
        """af/plan.py source does NOT reference purge_stale_audit_files."""
        import af.plan as plan_mod

        source = Path(plan_mod.__file__).read_text()
        assert "purge_stale_audit_files" not in source, (
            "purge_stale_audit_files must NOT be called from af/plan.py"
        )

    def test_standup_source_does_not_contain_purge_call(self) -> None:
        """af/standup.py source does NOT reference purge_stale_audit_files."""
        import af.standup as standup_mod

        source = Path(standup_mod.__file__).read_text()
        assert "purge_stale_audit_files" not in source, (
            "purge_stale_audit_files must NOT be called from af/standup.py"
        )
