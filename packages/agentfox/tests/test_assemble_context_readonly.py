"""Tests for assemble_context write extraction and orchestrator startup.

Verifies that assemble_context no longer calls _migrate_legacy_files or
index_errata_from_markdown, that the orchestrator startup calls them
instead, and that they are idempotent.

Test Spec: TS-06-10, TS-06-11, TS-06-12, TS-06-13, TS-06-14, TS-06-15,
           TS-06-16, TS-06-17, TS-06-18, TS-06-E5, TS-06-E6, TS-06-E7
Requirements: 06-REQ-5.1, 06-REQ-5.2, 06-REQ-5.3, 06-REQ-6.1, 06-REQ-6.2,
              06-REQ-6.3, 06-REQ-7.1, 06-REQ-7.2, 06-REQ-7.3, 06-REQ-7.E1
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb
import pytest


# -----------------------------------------------------------------------
# TS-06-10: assemble_context no longer calls _migrate_legacy_files
# -----------------------------------------------------------------------


class TestAssembleContextNoMigration:
    """TS-06-10: _migrate_legacy_files must NOT be called from assemble_context."""

    def test_assemble_context_does_not_call_migrate_legacy_files(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """When assemble_context is called, _migrate_legacy_files must
        not be invoked — migration is moved to orchestrator startup."""
        from agentfox.session.context import assemble_context

        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()
        # Create a minimal tasks.json for spec parsing
        (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')

        with patch(
            "agentfox.session.context._migrate_legacy_files"
        ) as mock_migrate:
            try:
                assemble_context(
                    spec_dir=spec_dir,
                    task_group=1,
                    conn=knowledge_conn,
                    project_root=tmp_path,
                )
            except Exception:
                pass  # We only care about whether _migrate was called

            assert mock_migrate.call_count == 0, (
                "assemble_context must NOT call _migrate_legacy_files; "
                f"it was called {mock_migrate.call_count} time(s)"
            )


# -----------------------------------------------------------------------
# TS-06-13: assemble_context no longer calls index_errata_from_markdown
# -----------------------------------------------------------------------


class TestAssembleContextNoErrataIndex:
    """TS-06-13: index_errata_from_markdown must NOT be called from assemble_context."""

    def test_assemble_context_does_not_call_index_errata(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """When assemble_context is called, index_errata_from_markdown
        must not be invoked — indexing is moved to orchestrator startup."""
        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')

        with patch(
            "agentfox.session.context.index_errata_from_markdown",
            create=True,
        ) as mock_index:
            from agentfox.session.context import assemble_context

            try:
                assemble_context(
                    spec_dir=spec_dir,
                    task_group=1,
                    conn=knowledge_conn,
                    project_root=tmp_path,
                )
            except Exception:
                pass

            assert mock_index.call_count == 0, (
                "assemble_context must NOT call index_errata_from_markdown; "
                f"it was called {mock_index.call_count} time(s)"
            )


# -----------------------------------------------------------------------
# TS-06-11: orchestrator startup calls _migrate_legacy_files per spec
# -----------------------------------------------------------------------


class TestOrchestratorStartupMigration:
    """TS-06-11: startup must call _migrate_legacy_files for each spec."""

    def test_orchestrator_calls_migrate_for_each_spec(self) -> None:
        """The orchestrator startup sequence must call _migrate_legacy_files
        once per spec with a read-write connection before dispatching any
        sessions."""
        # This test verifies the startup sequence that will be implemented
        # in af/nightshift.py or agentfox/engine/run.py.
        # After spec 06, the orchestrator startup should:
        # 1. Open a read-write connection
        # 2. Call _migrate_legacy_files for each spec
        # 3. Close the write connection before dispatching sessions
        pytest.skip(
            "Orchestrator startup migration not yet extracted from "
            "assemble_context (spec 06 group 5)"
        )


# -----------------------------------------------------------------------
# TS-06-14: orchestrator startup calls index_errata_from_markdown
# -----------------------------------------------------------------------


class TestOrchestratorStartupErrata:
    """TS-06-14: startup must call index_errata_from_markdown with rw conn."""

    def test_orchestrator_calls_index_errata_at_startup(self) -> None:
        """The orchestrator startup sequence must call
        index_errata_from_markdown with a read-write connection before
        dispatching any sessions."""
        pytest.skip(
            "Orchestrator startup errata indexing not yet extracted "
            "from assemble_context (spec 06 group 5)"
        )


# -----------------------------------------------------------------------
# TS-06-12: _migrate_legacy_files is idempotent
# -----------------------------------------------------------------------


class TestMigrateLegacyFilesIdempotent:
    """TS-06-12: calling _migrate_legacy_files twice produces no duplicates."""

    def test_idempotent_migration(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """Calling _migrate_legacy_files twice with the same arguments
        must produce the same record count — no duplicate records."""
        from agentfox.session.context import _migrate_legacy_files

        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()

        # Create legacy review.md
        review_content = """## Skeptic Review

### Finding 1
- **Severity:** major
- **Description:** Test finding for idempotency check
"""
        (spec_dir / "review.md").write_text(review_content)

        # First migration
        _migrate_legacy_files(knowledge_conn, spec_dir, "test_spec")
        count_first = knowledge_conn.execute(
            "SELECT COUNT(*) FROM findings WHERE spec_name = 'test_spec'"
        ).fetchone()[0]

        # Second migration — should produce no additional records
        _migrate_legacy_files(knowledge_conn, spec_dir, "test_spec")
        count_second = knowledge_conn.execute(
            "SELECT COUNT(*) FROM findings WHERE spec_name = 'test_spec'"
        ).fetchone()[0]

        assert count_first == count_second, (
            f"_migrate_legacy_files is not idempotent: "
            f"first call produced {count_first} records, "
            f"second call produced {count_second} records"
        )


# -----------------------------------------------------------------------
# TS-06-15: index_errata_from_markdown is idempotent
# -----------------------------------------------------------------------


class TestIndexErrataIdempotent:
    """TS-06-15: calling index_errata_from_markdown twice produces no duplicates."""

    def test_idempotent_errata_indexing(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """Calling index_errata_from_markdown twice with the same
        project_root must produce the same errata record count."""
        from agentfox.knowledge.errata import index_errata_from_markdown

        # Create minimal errata directory structure
        errata_dir = tmp_path / "docs" / "errata"
        errata_dir.mkdir(parents=True)
        (errata_dir / "01_test_erratum.md").write_text(
            "# Erratum: test\n\nThis is a test erratum.\n"
        )

        # First indexing
        index_errata_from_markdown(knowledge_conn, tmp_path)
        count_first = knowledge_conn.execute(
            "SELECT COUNT(*) FROM errata"
        ).fetchone()[0]

        # Second indexing — should produce no additional records
        index_errata_from_markdown(knowledge_conn, tmp_path)
        count_second = knowledge_conn.execute(
            "SELECT COUNT(*) FROM errata"
        ).fetchone()[0]

        assert count_first == count_second, (
            f"index_errata_from_markdown is not idempotent: "
            f"first call produced {count_first} records, "
            f"second call produced {count_second} records"
        )


# -----------------------------------------------------------------------
# TS-06-16: assemble_context works with read-only conn and returns context
# -----------------------------------------------------------------------


class TestAssembleContextWithReadOnlyConn:
    """TS-06-16: assemble_context must work with a read-only connection."""

    def test_assemble_context_with_read_only_conn(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """assemble_context must complete successfully when given a
        read-only connection, returning a populated context string.
        Currently blocked because assemble_context still performs writes."""
        # After spec 06 group 5 extracts writes from assemble_context,
        # this test should pass with a genuine read-only connection.
        # For now, skip since assemble_context still calls write functions.
        pytest.skip(
            "assemble_context still calls _migrate_legacy_files and "
            "index_errata_from_markdown (spec 06 group 5 not implemented)"
        )


# -----------------------------------------------------------------------
# TS-06-17: assemble_context performs zero write operations on conn
# -----------------------------------------------------------------------


class TestAssembleContextNoWrites:
    """TS-06-17: assemble_context must not INSERT/UPDATE/DELETE."""

    def test_assemble_context_db_state_unchanged(
        self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path
    ) -> None:
        """After calling assemble_context, the DB state must be identical
        to before the call — no INSERT, UPDATE, or DELETE operations."""
        # After spec 06 group 5, assemble_context should perform zero writes.
        # This test verifies that contract by snapshotting DB state.
        pytest.skip(
            "assemble_context still performs writes via "
            "_migrate_legacy_files and index_errata_from_markdown "
            "(spec 06 group 5 not implemented)"
        )


# -----------------------------------------------------------------------
# TS-06-18: orchestrator passes read_only=True conn to assemble_context
# -----------------------------------------------------------------------


class TestOrchestratorPassesReadOnlyConn:
    """TS-06-18: orchestrator must pass read_only=True conn to assemble_context."""

    def test_orchestrator_session_uses_read_only_conn(self) -> None:
        """The orchestrator must pass a connection opened with
        read_only=True to assemble_context when dispatching sessions."""
        pytest.skip(
            "Orchestrator read-only conn wiring not yet implemented "
            "(spec 06 group 5)"
        )


# -----------------------------------------------------------------------
# TS-06-E5: _migrate_legacy_files failure for one spec doesn't abort
# -----------------------------------------------------------------------


class TestMigrationFailureIsolation:
    """TS-06-E5: migration failure for one spec must not abort startup."""

    def test_migration_failure_does_not_abort_remaining_specs(self) -> None:
        """When _migrate_legacy_files fails for spec_a, the orchestrator
        must log the error and continue processing spec_b."""
        pytest.skip(
            "Orchestrator startup error isolation not yet implemented "
            "(spec 06 group 5)"
        )


# -----------------------------------------------------------------------
# TS-06-E6: index_errata_from_markdown failure doesn't block dispatch
# -----------------------------------------------------------------------


class TestErrataIndexFailureIsolation:
    """TS-06-E6: errata indexing failure must not block session dispatch."""

    def test_errata_failure_does_not_block_sessions(self) -> None:
        """When index_errata_from_markdown raises, the orchestrator must
        log the error and continue with session dispatch."""
        pytest.skip(
            "Orchestrator startup error isolation not yet implemented "
            "(spec 06 group 5)"
        )


# -----------------------------------------------------------------------
# TS-06-E7: write re-introduced into assemble_context raises on read-only
# -----------------------------------------------------------------------


class TestAssembleContextWriteRegression:
    """TS-06-E7: a write in assemble_context with read-only conn must raise."""

    def test_write_on_read_only_conn_raises(self, tmp_path: Path) -> None:
        """If a write operation is re-introduced into assemble_context,
        a read-only DuckDB connection must raise immediately, surfacing
        the regression."""
        db_path = str(tmp_path / "test.duckdb")

        # Create DB in read-write mode
        conn_rw = duckdb.connect(db_path)
        conn_rw.execute("""
            CREATE TABLE IF NOT EXISTS findings (
                id TEXT PRIMARY KEY,
                spec_name TEXT,
                task_group TEXT,
                severity TEXT,
                description TEXT,
                source TEXT,
                status TEXT DEFAULT 'active',
                created_at TIMESTAMP
            )
        """)
        conn_rw.close()

        # Open read-only — any write attempt must raise
        conn_ro = duckdb.connect(db_path, read_only=True)
        with pytest.raises(duckdb.InvalidInputException):
            conn_ro.execute(
                "INSERT INTO findings VALUES "
                "('f1', 'spec', '1', 'major', 'desc', 'src', 'active', "
                "CURRENT_TIMESTAMP)"
            )
        conn_ro.close()
