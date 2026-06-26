"""Unit tests for purge_stale_audit_files.

Test Spec: TS-NS-1, TS-NS-2, TS-NS-3
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox.workspace.audit_cleanup import purge_stale_audit_files


@pytest.fixture()
def audit_dir(tmp_path: Path) -> Path:
    """Return a fresh temporary audit directory."""
    d = tmp_path / ".agent-fox" / "audit"
    d.mkdir(parents=True)
    return d


class TestPurgeStaleAuditFiles:
    """TS-NS-1: Stale audit files are removed; unrelated files are preserved."""

    def test_removes_agent_jsonl(self, audit_dir: Path) -> None:
        """agent_*.jsonl files are deleted."""
        f = audit_dir / "agent_abc.jsonl"
        f.write_text("{}")
        purge_stale_audit_files(audit_dir)
        assert not f.exists()

    def test_removes_audit_jsonl(self, audit_dir: Path) -> None:
        """audit_*.jsonl files are deleted."""
        f = audit_dir / "audit_xyz.jsonl"
        f.write_text("{}")
        purge_stale_audit_files(audit_dir)
        assert not f.exists()

    def test_removes_postmortem_json(self, audit_dir: Path) -> None:
        """postmortem_*.json files are deleted."""
        f = audit_dir / "postmortem_123.json"
        f.write_text("{}")
        purge_stale_audit_files(audit_dir)
        assert not f.exists()

    def test_preserves_unrelated_files(self, audit_dir: Path) -> None:
        """Files that do not match any stale pattern are left untouched."""
        unrelated = audit_dir / "other.txt"
        unrelated.write_text("keep me")
        purge_stale_audit_files(audit_dir)
        assert unrelated.exists()

    def test_removes_all_matching_files_preserves_unrelated(self, audit_dir: Path) -> None:
        """All three patterns are cleaned up; unrelated file survives."""
        agent_f = audit_dir / "agent_abc.jsonl"
        audit_f = audit_dir / "audit_xyz.jsonl"
        pm_f = audit_dir / "postmortem_123.json"
        other_f = audit_dir / "other.txt"
        for f in (agent_f, audit_f, pm_f, other_f):
            f.write_text("data")

        purge_stale_audit_files(audit_dir)

        assert not agent_f.exists()
        assert not audit_f.exists()
        assert not pm_f.exists()
        assert other_f.exists()

    def test_returns_count_of_removed_files(self, audit_dir: Path) -> None:
        """Return value equals number of files successfully removed."""
        for name in ("agent_1.jsonl", "audit_2.jsonl", "postmortem_3.json"):
            (audit_dir / name).write_text("{}")

        count = purge_stale_audit_files(audit_dir)

        assert count == 3

    def test_returns_zero_when_no_matching_files(self, audit_dir: Path) -> None:
        """Returns 0 when the audit dir is empty or has no stale files."""
        count = purge_stale_audit_files(audit_dir)
        assert count == 0

    def test_audit_dir_missing_returns_zero(self, tmp_path: Path) -> None:
        """When the audit dir does not exist, returns 0 without error."""
        missing = tmp_path / "nonexistent" / "audit"
        count = purge_stale_audit_files(missing)
        assert count == 0


class TestDebugLogging:
    """TS-NS-2: Purge count is logged at DEBUG level."""

    def test_debug_message_contains_count(self, audit_dir: Path, caplog: pytest.LogCaptureFixture) -> None:
        """A DEBUG log line reports how many files were removed."""
        (audit_dir / "agent_foo.jsonl").write_text("{}")
        (audit_dir / "audit_bar.jsonl").write_text("{}")

        with caplog.at_level(logging.DEBUG, logger="agentfox.workspace.audit_cleanup"):
            purge_stale_audit_files(audit_dir)

        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        assert any("2" in m for m in debug_msgs), f"Expected count '2' in DEBUG messages: {debug_msgs}"

    def test_debug_message_references_audit_dir(self, audit_dir: Path, caplog: pytest.LogCaptureFixture) -> None:
        """The DEBUG log message includes the audit directory path."""
        with caplog.at_level(logging.DEBUG, logger="agentfox.workspace.audit_cleanup"):
            purge_stale_audit_files(audit_dir)

        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        assert any(str(audit_dir) in m for m in debug_msgs), (
            f"Expected audit dir path in DEBUG messages: {debug_msgs}"
        )


class TestErrorHandling:
    """TS-NS-3: Deletion failures are caught; WARNING logged; no exception propagates."""

    def test_oserror_does_not_propagate(self, audit_dir: Path) -> None:
        """OSError during unlink is swallowed - no exception raised."""
        (audit_dir / "agent_fail.jsonl").write_text("{}")

        with patch.object(Path, "unlink", side_effect=OSError("permission denied")):
            # Must not raise
            purge_stale_audit_files(audit_dir)

    def test_oserror_logged_as_warning(self, audit_dir: Path, caplog: pytest.LogCaptureFixture) -> None:
        """A WARNING is emitted when a file cannot be deleted."""
        (audit_dir / "agent_fail.jsonl").write_text("{}")

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.audit_cleanup"):
            with patch.object(Path, "unlink", side_effect=OSError("permission denied")):
                purge_stale_audit_files(audit_dir)

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert warn_msgs, "Expected at least one WARNING log entry on deletion failure"

    def test_continues_after_one_failure(self, audit_dir: Path, caplog: pytest.LogCaptureFixture) -> None:
        """After one file fails to delete, the loop continues and removes the rest."""
        fail_file = audit_dir / "agent_fail.jsonl"
        ok_file = audit_dir / "audit_ok.jsonl"
        fail_file.write_text("{}")
        ok_file.write_text("{}")

        original_unlink = Path.unlink

        def _selective_unlink(self, missing_ok: bool = False) -> None:  # noqa: FBT001
            if self.name == "agent_fail.jsonl":
                raise OSError("simulated failure")
            original_unlink(self, missing_ok=missing_ok)

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.audit_cleanup"):
            with patch.object(Path, "unlink", _selective_unlink):
                count = purge_stale_audit_files(audit_dir)

        # ok_file was removed, fail_file was not
        assert not ok_file.exists()
        assert fail_file.exists()
        assert count == 1
        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert warn_msgs, "Expected a WARNING for the failed deletion"
