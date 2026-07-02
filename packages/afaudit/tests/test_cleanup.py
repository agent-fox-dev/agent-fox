"""Tests for afaudit.cleanup module — signatures, normal path, and edge cases.

TS-01-27: purge_stale_audit_files and enforce_file_retention signatures
TS-01-28: enforce_file_retention deletes oldest runs beyond max_runs
TS-01-29: stdlib logging with 'afaudit.cleanup' logger
TS-01-E3: Missing audit_dir returns 0 silently
TS-01-E4: Unparseable filenames logged as WARNING and skipped
TS-01-E5: Failed file deletions logged as WARNING, counted correctly
"""

from __future__ import annotations

import inspect
import logging
import tempfile
from pathlib import Path
from unittest.mock import patch

import afaudit.cleanup as cleanup

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
CLEANUP_SOURCE = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "cleanup.py"

# Valid run_id timestamps used for test fixtures, ordered oldest to newest.
_VALID_RUN_IDS = [
    "20240101_100000_aaa001",
    "20240201_100000_bbb002",
    "20240301_100000_ccc003",
    "20240401_100000_ddd004",
]


def _create_run_files(audit_dir: Path, run_id: str) -> None:
    """Create the three audit file types for a single run_id."""
    (audit_dir / f"audit_{run_id}.jsonl").write_text("")
    (audit_dir / f"agent_{run_id}.jsonl").write_text("")
    (audit_dir / f"postmortem_{run_id}.json").write_text("{}")


def _create_audit_dir_with_runs(
    base: Path,
    run_ids: list[str] | None = None,
) -> Path:
    """Create a temp audit directory populated with run file sets."""
    if run_ids is None:
        run_ids = list(_VALID_RUN_IDS)
    audit_dir = base / "audit"
    audit_dir.mkdir(parents=True, exist_ok=True)
    for rid in run_ids:
        _create_run_files(audit_dir, rid)
    return audit_dir


class TestCleanupFunctionSignatures:
    """TS-01-27: purge_stale_audit_files and enforce_file_retention signatures.

    Requirement: 01-REQ-7.1
    """

    def test_purge_stale_audit_files_is_callable(self) -> None:
        """purge_stale_audit_files must be callable."""
        assert callable(cleanup.purge_stale_audit_files)

    def test_enforce_file_retention_is_callable(self) -> None:
        """enforce_file_retention must be callable."""
        assert callable(cleanup.enforce_file_retention)

    def test_enforce_file_retention_has_audit_dir_param(self) -> None:
        """enforce_file_retention must have an 'audit_dir' parameter."""
        sig = inspect.signature(cleanup.enforce_file_retention)
        assert "audit_dir" in sig.parameters

    def test_enforce_file_retention_max_runs_is_keyword_only(self) -> None:
        """max_runs must be a keyword-only parameter."""
        sig = inspect.signature(cleanup.enforce_file_retention)
        param = sig.parameters["max_runs"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY

    def test_enforce_file_retention_max_runs_default_is_20(self) -> None:
        """max_runs must default to 20."""
        sig = inspect.signature(cleanup.enforce_file_retention)
        assert sig.parameters["max_runs"].default == 20


class TestEnforceFileRetentionNormalPath:
    """TS-01-28: enforce_file_retention deletes oldest run sets beyond max_runs.

    Requirement: 01-REQ-7.2
    """

    def test_deletes_oldest_runs_beyond_max(self) -> None:
        """With 4 run sets and max_runs=2, the 2 oldest should be deleted."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(Path(tmp))

            result = cleanup.enforce_file_retention(audit_dir, max_runs=2)

            # 2 oldest runs × 3 files each = 6 files deleted
            assert result == 6

    def test_newest_runs_remain(self) -> None:
        """After retention, only the newest run sets should remain."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(Path(tmp))

            cleanup.enforce_file_retention(audit_dir, max_runs=2)

            remaining_audit = sorted(audit_dir.glob("audit_*.jsonl"))
            assert len(remaining_audit) == 2

            # The two newest run_ids should be the ones remaining
            remaining_ids = {f.stem.replace("audit_", "") for f in remaining_audit}
            expected_ids = {_VALID_RUN_IDS[2], _VALID_RUN_IDS[3]}
            assert remaining_ids == expected_ids

    def test_deletes_all_three_file_types(self) -> None:
        """All three file types (audit, agent, postmortem) must be deleted."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(Path(tmp))

            cleanup.enforce_file_retention(audit_dir, max_runs=2)

            remaining_agent = list(audit_dir.glob("agent_*.jsonl"))
            remaining_postmortem = list(audit_dir.glob("postmortem_*.json"))
            assert len(remaining_agent) == 2
            assert len(remaining_postmortem) == 2

    def test_no_deletion_when_within_limit(self) -> None:
        """When run count <= max_runs, nothing should be deleted."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(Path(tmp))

            result = cleanup.enforce_file_retention(audit_dir, max_runs=10)

            assert result == 0
            assert len(list(audit_dir.glob("audit_*.jsonl"))) == 4

    def test_returns_int(self) -> None:
        """Return value must be an int."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(Path(tmp))
            result = cleanup.enforce_file_retention(audit_dir, max_runs=2)
            assert isinstance(result, int)


class TestCleanupLogging:
    """TS-01-29: afaudit.cleanup uses stdlib logging with 'afaudit.cleanup'.

    Requirement: 01-REQ-7.3
    """

    def test_imports_stdlib_logging(self) -> None:
        """cleanup.py must import stdlib logging."""
        source = CLEANUP_SOURCE.read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """cleanup.py must create a logger named 'afaudit.cleanup'."""
        source = CLEANUP_SOURCE.read_text(encoding="utf-8")
        assert "getLogger('afaudit.cleanup')" in source or 'getLogger("afaudit.cleanup")' in source

    def test_no_loguru(self) -> None:
        """cleanup.py must not import loguru."""
        source = CLEANUP_SOURCE.read_text(encoding="utf-8")
        assert "loguru" not in source

    def test_no_structlog(self) -> None:
        """cleanup.py must not import structlog."""
        source = CLEANUP_SOURCE.read_text(encoding="utf-8")
        assert "structlog" not in source


class TestEnforceFileRetentionMissingDir:
    """TS-01-E3: Missing audit_dir returns 0 silently.

    Requirement: 01-REQ-7.E1
    """

    def test_returns_zero_for_nonexistent_dir(self) -> None:
        """Must return 0 when audit_dir does not exist."""
        nonexistent = Path(tempfile.mkdtemp()) / "does_not_exist_afaudit_test"
        result = cleanup.enforce_file_retention(nonexistent, max_runs=20)
        assert result == 0

    def test_no_exception_for_nonexistent_dir(self) -> None:
        """Must not raise any exception when audit_dir does not exist."""
        nonexistent = Path(tempfile.mkdtemp()) / "does_not_exist_afaudit_test"
        # If we get past this call without exception, the test passes.
        cleanup.enforce_file_retention(nonexistent, max_runs=20)

    def test_no_log_messages_for_nonexistent_dir(self) -> None:
        """Must not emit any log messages when audit_dir does not exist."""
        nonexistent = Path(tempfile.mkdtemp()) / "does_not_exist_afaudit_test"

        logger = logging.getLogger("afaudit.cleanup")
        with _CaptureHandler(logger) as captured:
            cleanup.enforce_file_retention(nonexistent, max_runs=20)

        assert len(captured.records) == 0, (
            f"Expected no log messages, got {len(captured.records)}: "
            f"{[r.getMessage() for r in captured.records]}"
        )


class TestEnforceFileRetentionUnparseableFilenames:
    """TS-01-E4: Unparseable filenames are skipped with a WARNING log.

    Requirement: 01-REQ-7.E2
    """

    def test_unparseable_file_not_deleted(self) -> None:
        """Files with unparseable timestamp names must not be deleted."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp) / "audit"
            audit_dir.mkdir()
            bad_file = audit_dir / "audit_BADNAME.jsonl"
            bad_file.write_text("")

            cleanup.enforce_file_retention(audit_dir, max_runs=0)

            assert bad_file.exists(), "Unparseable file should not be deleted"

    def test_warning_logged_for_unparseable_file(self) -> None:
        """A WARNING must be logged identifying the unparseable filename."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp) / "audit"
            audit_dir.mkdir()
            (audit_dir / "audit_BADNAME.jsonl").write_text("")

            logger = logging.getLogger("afaudit.cleanup")
            with _CaptureHandler(logger) as captured:
                cleanup.enforce_file_retention(audit_dir, max_runs=0)

            warnings = [r for r in captured.records if r.levelno >= logging.WARNING]
            assert len(warnings) > 0, "Expected at least one WARNING log for unparseable filename"
            # The warning should reference the bad filename
            warning_text = " ".join(r.getMessage() for r in warnings)
            assert "BADNAME" in warning_text, f"WARNING log should mention 'BADNAME', got: {warning_text}"

    def test_valid_files_still_processed(self) -> None:
        """Valid run sets should still be processed even when an unparseable file exists."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp) / "audit"
            audit_dir.mkdir()
            # One unparseable file
            (audit_dir / "audit_BADNAME.jsonl").write_text("")
            # One valid run set
            _create_run_files(audit_dir, _VALID_RUN_IDS[0])

            result = cleanup.enforce_file_retention(audit_dir, max_runs=0)

            # The valid run set (3 files) should be deleted, unparseable left alone
            assert result == 3
            assert (audit_dir / "audit_BADNAME.jsonl").exists()


class TestEnforceFileRetentionDeletionFailure:
    """TS-01-E5: Failed deletions are logged at WARNING and counted correctly.

    Requirement: 01-REQ-7.E3
    """

    def test_counts_only_successful_deletions(self) -> None:
        """When one deletion fails, return value excludes the failed file."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(
                Path(tmp),
                run_ids=_VALID_RUN_IDS[:2],
            )
            # 2 runs × 3 files = 6 files, patch to fail on first unlink
            call_count = [0]
            orig_unlink = Path.unlink

            def mock_unlink(self_path: Path, *args: object, **kwargs: object) -> None:
                call_count[0] += 1
                if call_count[0] == 1:
                    raise PermissionError("denied")
                orig_unlink(self_path, *args, **kwargs)

            with patch.object(Path, "unlink", mock_unlink):
                result = cleanup.enforce_file_retention(audit_dir, max_runs=0)

            assert result == 5, f"Expected 5 successful deletions (6 - 1 failed), got {result}"

    def test_warning_logged_on_deletion_failure(self) -> None:
        """A WARNING must be logged when a file deletion fails."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(
                Path(tmp),
                run_ids=_VALID_RUN_IDS[:1],
            )

            call_count = [0]
            orig_unlink = Path.unlink

            def mock_unlink(self_path: Path, *args: object, **kwargs: object) -> None:
                call_count[0] += 1
                if call_count[0] == 1:
                    raise PermissionError("denied")
                orig_unlink(self_path, *args, **kwargs)

            logger = logging.getLogger("afaudit.cleanup")
            with (
                patch.object(Path, "unlink", mock_unlink),
                _CaptureHandler(logger) as captured,
            ):
                cleanup.enforce_file_retention(audit_dir, max_runs=0)

            warnings = [r for r in captured.records if r.levelno >= logging.WARNING]
            assert len(warnings) > 0, "Expected WARNING log for deletion failure"

    def test_continues_after_failure(self) -> None:
        """Remaining files should still be deleted after one failure."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = _create_audit_dir_with_runs(
                Path(tmp),
                run_ids=_VALID_RUN_IDS[:1],
            )
            # 1 run × 3 files = 3 files, fail on first
            call_count = [0]
            orig_unlink = Path.unlink

            def mock_unlink(self_path: Path, *args: object, **kwargs: object) -> None:
                call_count[0] += 1
                if call_count[0] == 1:
                    raise PermissionError("denied")
                orig_unlink(self_path, *args, **kwargs)

            with patch.object(Path, "unlink", mock_unlink):
                result = cleanup.enforce_file_retention(audit_dir, max_runs=0)

            # 2 files should have been successfully deleted (3 - 1 failed)
            assert result == 2


# ---------------------------------------------------------------------------
# Helper: capture log records from a named logger
# ---------------------------------------------------------------------------


class _CaptureHandler(logging.Handler):
    """Context manager that captures log records from a logger."""

    def __init__(self, target_logger: logging.Logger) -> None:
        super().__init__(level=logging.DEBUG)
        self._logger = target_logger
        self.records: list[logging.LogRecord] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.records.append(record)

    def __enter__(self) -> _CaptureHandler:
        self._logger.addHandler(self)
        self._prev_level = self._logger.level
        self._logger.setLevel(logging.DEBUG)
        return self

    def __exit__(self, *args: object) -> None:
        self._logger.removeHandler(self)
        self._logger.setLevel(self._prev_level)
