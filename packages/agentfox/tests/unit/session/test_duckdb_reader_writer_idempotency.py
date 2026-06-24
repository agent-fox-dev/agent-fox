"""Tests for idempotency of startup write operations (spec 06_duckdb_reader_writer_split).

TS-06-12: _migrate_legacy_files is idempotent (same record count after 2 calls).
TS-06-15: index_errata_from_markdown is idempotent (same errata count after 2 calls).
TS-06-P4: Property test for arbitrary N repetitions.

Requirements: 06-REQ-5.3, 06-REQ-6.3
"""

from __future__ import annotations

import tempfile
from pathlib import Path

import duckdb
from agentfox.knowledge.errata import index_errata_from_markdown
from agentfox.session.context import _migrate_legacy_files
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Schema helpers
# ---------------------------------------------------------------------------


def _create_review_schema(conn: duckdb.DuckDBPyConnection) -> None:
    """Create the review_findings and verification_results tables."""
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
    conn.execute("""
        CREATE TABLE IF NOT EXISTS verification_results (
            id              UUID PRIMARY KEY,
            requirement_id  TEXT NOT NULL,
            verdict         TEXT NOT NULL,
            evidence        TEXT,
            spec_name       TEXT NOT NULL,
            task_group      TEXT NOT NULL,
            session_id      TEXT NOT NULL,
            superseded_by   TEXT,
            created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        )
    """)


def _create_errata_table(conn: duckdb.DuckDBPyConnection) -> None:
    """Create the errata table for tests."""
    conn.execute("""
        CREATE TABLE IF NOT EXISTS errata (
            id              VARCHAR PRIMARY KEY,
            spec_name       VARCHAR NOT NULL,
            task_group      VARCHAR NOT NULL,
            finding_summary TEXT NOT NULL,
            requirement_ref VARCHAR,
            fix_summary     TEXT,
            created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
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

SAMPLE_VERIFICATION_MD = """\
# Verification Results

| Requirement | Result | Notes |
|---|---|---|
| 01-REQ-1.1 | PASS | All tests green |
| 01-REQ-1.2 | FAIL | Missing edge case |
"""

SAMPLE_ERRATA_MD = """\
# Errata: test_spec (auto-generated)

## Findings

### Finding 1

**Summary:** [critical] Schema divergence in migration v19
**Task Group:** 1
"""


def _make_spec_dir(tmp_path: Path) -> Path:
    """Create a spec directory with sample legacy files."""
    spec_dir = tmp_path / "specs" / "01_test_spec"
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "review.md").write_text(SAMPLE_REVIEW_MD, encoding="utf-8")
    (spec_dir / "verification.md").write_text(SAMPLE_VERIFICATION_MD, encoding="utf-8")
    return spec_dir


def _make_errata_dir(tmp_path: Path) -> Path:
    """Create an errata directory with a sample markdown file."""
    errata_dir = tmp_path / "docs" / "errata"
    errata_dir.mkdir(parents=True, exist_ok=True)
    (errata_dir / "test_spec_auto_errata.md").write_text(SAMPLE_ERRATA_MD, encoding="utf-8")
    return errata_dir


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

    def test_verdicts_idempotent_no_duplicates(self, tmp_path: Path) -> None:
        """First call inserts verdicts; second call is a no-op."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = _make_spec_dir(tmp_path)
        spec_name = "test_spec"

        _migrate_legacy_files(conn, spec_dir, spec_name)
        count_after_first = conn.execute(
            "SELECT COUNT(*) FROM verification_results WHERE spec_name = ?",
            [spec_name],
        ).fetchone()[0]
        assert count_after_first > 0, "First call should insert verdicts"

        _migrate_legacy_files(conn, spec_dir, spec_name)
        count_after_second = conn.execute(
            "SELECT COUNT(*) FROM verification_results WHERE spec_name = ?",
            [spec_name],
        ).fetchone()[0]
        assert count_after_first == count_after_second, (
            f"Second call should not insert duplicates: first={count_after_first}, second={count_after_second}"
        )
        conn.close()

    def test_combined_record_count_stable(self, tmp_path: Path) -> None:
        """Total record count across both tables is identical after 1 and 2 calls."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = _make_spec_dir(tmp_path)
        spec_name = "test_spec"

        _migrate_legacy_files(conn, spec_dir, spec_name)

        def _total_records() -> int:
            findings = conn.execute(
                "SELECT COUNT(*) FROM review_findings WHERE spec_name = ?",
                [spec_name],
            ).fetchone()[0]
            verdicts = conn.execute(
                "SELECT COUNT(*) FROM verification_results WHERE spec_name = ?",
                [spec_name],
            ).fetchone()[0]
            return findings + verdicts

        total_first = _total_records()
        _migrate_legacy_files(conn, spec_dir, spec_name)
        total_second = _total_records()

        assert total_first == total_second
        conn.close()

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
        """Calling with no review.md/verification.md is a silent no-op."""
        conn = duckdb.connect(":memory:")
        _create_review_schema(conn)
        spec_dir = tmp_path / "empty_spec"
        spec_dir.mkdir(parents=True, exist_ok=True)

        _migrate_legacy_files(conn, spec_dir, "empty_spec")

        findings = conn.execute("SELECT COUNT(*) FROM review_findings").fetchone()[0]
        verdicts = conn.execute("SELECT COUNT(*) FROM verification_results").fetchone()[0]
        assert findings == 0
        assert verdicts == 0
        conn.close()


# ---------------------------------------------------------------------------
# TS-06-15: index_errata_from_markdown idempotency
# ---------------------------------------------------------------------------


class TestIndexErrataIdempotency:
    """TS-06-15: Calling index_errata_from_markdown twice with the same
    (conn, project_root) produces no duplicate errata records."""

    def test_idempotent_second_call_no_duplicates(self, tmp_path: Path) -> None:
        """First call inserts errata; second call is a no-op."""
        conn = duckdb.connect(":memory:")
        _create_errata_table(conn)
        _make_errata_dir(tmp_path)

        first_inserted = index_errata_from_markdown(conn, tmp_path)
        count_after_first = conn.execute("SELECT COUNT(*) FROM errata WHERE spec_name = 'test_spec'").fetchone()[0]
        assert first_inserted > 0, "First call should insert errata"
        assert count_after_first > 0

        second_inserted = index_errata_from_markdown(conn, tmp_path)
        count_after_second = conn.execute("SELECT COUNT(*) FROM errata WHERE spec_name = 'test_spec'").fetchone()[0]

        assert second_inserted == 0, "Second call should insert nothing"
        assert count_after_first == count_after_second, (
            f"Record count should be stable: first={count_after_first}, second={count_after_second}"
        )
        conn.close()

    def test_no_error_on_repeated_calls(self, tmp_path: Path) -> None:
        """Repeated calls raise no exceptions."""
        conn = duckdb.connect(":memory:")
        _create_errata_table(conn)
        _make_errata_dir(tmp_path)

        for _ in range(3):
            index_errata_from_markdown(conn, tmp_path)
        conn.close()

    def test_multiple_files_idempotent(self, tmp_path: Path) -> None:
        """Idempotency holds across multiple errata files."""
        conn = duckdb.connect(":memory:")
        _create_errata_table(conn)
        errata_dir = _make_errata_dir(tmp_path)
        (errata_dir / "other_spec.md").write_text(
            "# Errata: other_spec — Another issue\n\nDetails here.\n",
            encoding="utf-8",
        )

        index_errata_from_markdown(conn, tmp_path)
        total_first = conn.execute("SELECT COUNT(*) FROM errata").fetchone()[0]

        index_errata_from_markdown(conn, tmp_path)
        total_second = conn.execute("SELECT COUNT(*) FROM errata").fetchone()[0]

        assert total_first == total_second
        assert total_first > 0
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

    @given(n_calls=st.integers(min_value=2, max_value=5))
    @settings(max_examples=5, deadline=10000)
    def test_index_errata_stable_for_n_calls(self, n_calls: int) -> None:
        """index_errata_from_markdown record count is stable after N >= 2 calls."""
        with tempfile.TemporaryDirectory() as td:
            tmp_path = Path(td)
            conn = duckdb.connect(":memory:")
            _create_errata_table(conn)
            _make_errata_dir(tmp_path)

            index_errata_from_markdown(conn, tmp_path)
            count_after_first = conn.execute("SELECT COUNT(*) FROM errata WHERE spec_name = 'test_spec'").fetchone()[0]

            for _ in range(n_calls - 1):
                index_errata_from_markdown(conn, tmp_path)

            count_after_n = conn.execute("SELECT COUNT(*) FROM errata WHERE spec_name = 'test_spec'").fetchone()[0]
            assert count_after_first == count_after_n
            conn.close()
