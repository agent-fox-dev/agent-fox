"""Integration tests for audit file cleanup at startup.

Covers:
- TS-NS-1: engine._init_run calls purge_stale_audit_files with exclude_run_id
- TS-NS-5: af plan and af standup do NOT call purge_stale_audit_files

Requirements: NS-REQ-4, NS-REQ-5
"""

from __future__ import annotations

from pathlib import Path


class TestCodeCallsAuditCleanup:
    """TS-NS-1: purge_stale_audit_files is called from engine._init_run, not af/code.py."""

    def test_code_source_does_not_contain_purge_call(self) -> None:
        """af/code.py no longer calls purge_stale_audit_files directly."""
        import af.code as code_mod

        source = Path(code_mod.__file__).read_text()
        assert "purge_stale_audit_files" not in source, (
            "purge_stale_audit_files should be called from engine._init_run, not af/code.py"
        )

    def test_engine_source_contains_purge_call(self) -> None:
        """engine.py calls purge_stale_audit_files in _init_run."""
        import agentfox.engine.engine as engine_mod

        source = Path(engine_mod.__file__).read_text()
        assert "purge_stale_audit_files" in source

    def test_engine_purge_passes_exclude_run_id(self) -> None:
        """engine.py passes exclude_run_id to purge_stale_audit_files."""
        import agentfox.engine.engine as engine_mod

        source = Path(engine_mod.__file__).read_text()
        assert "exclude_run_id" in source


class TestReadOnlyCommandsDoNotCleanup:
    """TS-NS-5: af plan and af standup must NOT trigger audit file cleanup."""

    def test_plan_source_does_not_contain_purge_call(self) -> None:
        """af/plan.py source does NOT reference purge_stale_audit_files."""
        import af.plan as plan_mod

        source = Path(plan_mod.__file__).read_text()
        assert "purge_stale_audit_files" not in source, "purge_stale_audit_files must NOT be called from af/plan.py"

    def test_standup_source_does_not_contain_purge_call(self) -> None:
        """af/standup.py source does NOT reference purge_stale_audit_files."""
        import af.standup as standup_mod

        source = Path(standup_mod.__file__).read_text()
        assert "purge_stale_audit_files" not in source, "purge_stale_audit_files must NOT be called from af/standup.py"
