"""Tests for idempotency of startup write operations (spec 06_duckdb_reader_writer_split).

TS-06-12: _migrate_legacy_files is idempotent (same record count after 2 calls).
TS-06-P4: Property test for arbitrary N repetitions.

Requirements: 06-REQ-5.3
"""

from __future__ import annotations

import tempfile
from pathlib import Path

import duckdb
from agentfox.session.context import _migrate_legacy_files
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Schema helpers
# ---------------------------------------------------------------------------


def _create_review_schema(conn: duckdb.DuckDBPyConnection) -> None:
    """Create the review_findings table."""
    conn.execute("""
        CREATE TABLE IF NOT EXISTS review_findings (
            id              UUID PRIMARY KEY,
            severity        TEXT NOT NULL,
            description     TEXT NOT NULL,
            requirement_ref TEXT,
            spec_name       TEXT NOT NULL,
            task_group      TEXT NOT NULL,
            session_id      TEXT NOT NULL,
            superseded_by   TEXT,
            created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            category        TEXT
        )
    """)


# ---------------------------------------------------------------------------
# Fixture helpers
# ---------------------------------------------------------------------------


SAMPLE_REVIEW_MD = """\
# Review

- [severity: critical] Database connection leak in module X
- [severity: major] Missing input validation on user endpoint
"""


def _make_spec_dir(tmp_path: Path) -> Path:
    """Create a spec directory with sample legacy files."""
    spec_dir = tmp_path / "specs" / "01_test_spec"
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "review.md").write_text(SAMPLE_REVIEW_MD, encoding="utf-8")
    return spec_dir


# ---------------------------------------------------------------------------
# TS-06-12: _migrate_legacy_files idempotency
# ---------------------------------------------------------------------------


class TestMigrateLegacyFilesIdempotency:
    """TS-06-12: Calling _migrate_legacy_files twice with the same
    (conn, spec_dir, spec_name) produces no duplicate records."""

    def test_findings_idempotent_no_duplicates(self, tmp_path: Path) -> None:
        """First call inserts findings; second call is a no-op."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = _make_spec_dir(tmp_path)
        spec_name = "test_spec"

        _migrate_legacy_files(conn, spec_dir, spec_name)
        count_after_first = conn.execute(
            "SELECT COUNT(*) FROM review_findings WHERE spec_name = ?",
            [spec_name],
        ).fetchone()[0]
        assert count_after_first > 0, "First call should insert findings"

        _migrate_legacy_files(conn, spec_dir, spec_name)
        count_after_second = conn.execute(
            "SELECT COUNT(*) FROM review_findings WHERE spec_name = ?",
            [spec_name],
        ).fetchone()[0]
        assert count_after_first == count_after_second, (
            f"Second call should not insert duplicates: first={count_after_first}, second={count_after_second}"
        )
        conn.close()

    # test_verdicts_idempotent_no_duplicates removed in spec 10.
    # test_combined_record_count_stable removed in spec 10.

    def test_no_error_on_repeated_calls(self, tmp_path: Path) -> None:
        """Repeated calls raise no exceptions."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = _make_spec_dir(tmp_path)
        spec_name = "test_spec"

        # Should not raise on any call
        for _ in range(3):
            _migrate_legacy_files(conn, spec_dir, spec_name)
        conn.close()

    def test_missing_legacy_files_is_noop(self, tmp_path: Path) -> None:
        """Calling with no review.md is a silent no-op."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = tmp_path / "empty_spec"
        spec_dir.mkdir(parents=True, exist_ok=True)

        _migrate_legacy_files(conn, spec_dir, "empty_spec")

        findings = conn.execute("SELECT COUNT(*) FROM review_findings").fetchone()[0]
        assert findings == 0
        conn.close()


# ---------------------------------------------------------------------------
# TS-06-P4: Property test — idempotency for arbitrary N >= 2 repetitions
# ---------------------------------------------------------------------------


class TestIdempotencyProperty:
    """TS-06-P4: For any N >= 2 repeated calls with identical inputs,
    record counts are stable after the first call."""

    @given(n_calls=st.integers(min_value=2, max_value=5))
    @settings(max_examples=5, deadline=10000)
    def test_migrate_legacy_files_stable_for_n_calls(self, n_calls: int) -> None:
        """_migrate_legacy_files record count is stable after N >= 2 calls."""
        with tempfile.TemporaryDirectory() as td:
            tmp_path = Path(td)
            conn = duckdb.connect(":memory:")
            _create_review_schema(conn)
            spec_dir = _make_spec_dir(tmp_path)
            spec_name = "prop_test_spec"

            _migrate_legacy_files(conn, spec_dir, spec_name)
            count_after_first = conn.execute(
                "SELECT COUNT(*) FROM review_findings WHERE spec_name = ?",
                [spec_name],
            ).fetchone()[0]

            for _ in range(n_calls - 1):
                _migrate_legacy_files(conn, spec_dir, spec_name)

            count_after_n = conn.execute(
                "SELECT COUNT(*) FROM review_findings WHERE spec_name = ?",
                [spec_name],
            ).fetchone()[0]
            assert count_after_first == count_after_n
            conn.close()


