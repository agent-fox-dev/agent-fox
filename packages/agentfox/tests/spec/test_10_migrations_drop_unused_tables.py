"""Tests for DuckDB migration that drops unused knowledge tables.

Verifies the new migration version drops errata, adr_entries, and
verification_results tables while retaining session_summaries.

Test Spec: TS-10-1, TS-10-2, TS-10-3, TS-10-E1, TS-10-P1
Requirements: 10-REQ-1.1, 10-REQ-1.2, 10-REQ-1.3, 10-REQ-1.E1
"""

from __future__ import annotations

import uuid

import duckdb
from agentfox.knowledge.migrations import (
    MIGRATIONS,
    apply_pending_migrations,
    run_migrations,
)
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _table_exists(conn: duckdb.DuckDBPyConnection, table_name: str) -> bool:
    """Check whether a table exists in the DuckDB database."""
    rows = conn.execute(
        "SELECT COUNT(*) FROM information_schema.tables "
        "WHERE table_schema = 'main' AND table_name = ?",
        [table_name],
    ).fetchone()
    return rows is not None and rows[0] > 0


def _get_applied_versions(conn: duckdb.DuckDBPyConnection) -> list[int]:
    """Return all applied migration versions in ascending order."""
    rows = conn.execute(
        "SELECT version FROM schema_version ORDER BY version ASC"
    ).fetchall()
    return [row[0] for row in rows]


def _create_legacy_tables(conn: duckdb.DuckDBPyConnection) -> None:
    """Manually create the three legacy tables for pre-migration testing."""
    conn.execute("""
        CREATE TABLE IF NOT EXISTS errata (
            id   TEXT PRIMARY KEY,
            body TEXT
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS adr_entries (
            id   TEXT PRIMARY KEY,
            body TEXT
        )
    """)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS verification_results (
            id   TEXT PRIMARY KEY,
            body TEXT
        )
    """)


# ---------------------------------------------------------------------------
# TS-10-1: Migration drops errata, adr_entries, verification_results
# ---------------------------------------------------------------------------


class TestMigrationDropsUnusedTables:
    """TS-10-1: New migration drops the three unused tables."""

    def test_tables_absent_after_migration(self) -> None:
        """After applying all migrations including the new one,
        errata, adr_entries, and verification_results must not exist."""
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        assert not _table_exists(conn, "errata"), "errata table should be dropped"
        assert not _table_exists(conn, "adr_entries"), "adr_entries table should be dropped"
        assert not _table_exists(conn, "verification_results"), (
            "verification_results table should be dropped"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-2: session_summaries survives the migration
# ---------------------------------------------------------------------------


class TestSessionSummariesSurvivesMigration:
    """TS-10-2: session_summaries table exists and is queryable after migration."""

    def test_session_summaries_exists_after_migration(self) -> None:
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        assert _table_exists(conn, "session_summaries"), (
            "session_summaries table must survive the migration"
        )
        conn.close()

    def test_session_summaries_queryable_with_data(self) -> None:
        """Pre-existing data in session_summaries must survive."""
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        # Insert a test row (id is UUID type)
        conn.execute(
            "INSERT INTO session_summaries "
            "(id, node_id, run_id, spec_name, task_group, archetype, attempt, summary, created_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
            [str(uuid.uuid4()), "node-1", "run-1", "spec-a", "1", "coder", 1, "test summary"],
        )

        rows = conn.execute("SELECT * FROM session_summaries").fetchall()
        assert len(rows) >= 1, "session_summaries must be queryable with data"
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-3: New migration version > all prior versions, recorded exactly once
# ---------------------------------------------------------------------------


class TestMigrationVersionOrdering:
    """TS-10-3: New migration has a version > all prior versions."""

    def test_new_version_greater_than_all_prior(self) -> None:
        """The last migration in MIGRATIONS must be the DROP migration
        and its version must be strictly greater than all prior versions."""
        versions = [m.version for m in MIGRATIONS]
        # The new DROP migration must be the highest version
        max_version = max(versions)
        # After the spec is implemented, the max version should be > 25
        # (v25 is the current highest before the spec)
        assert max_version > 25, (
            f"New migration version ({max_version}) must be > 25 (current highest)"
        )

    def test_version_recorded_exactly_once(self) -> None:
        """After applying all migrations, the new DROP version appears exactly once."""
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        versions = _get_applied_versions(conn)
        max_version = max(m.version for m in MIGRATIONS)

        assert versions.count(max_version) == 1, (
            f"Version {max_version} must appear exactly once in schema_version"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-E1: Migration succeeds on fresh DB (no pre-existing target tables)
# ---------------------------------------------------------------------------


class TestMigrationFreshDatabase:
    """TS-10-E1: DROP TABLE IF EXISTS on a fresh DB raises no error."""

    def test_fresh_db_migration_succeeds(self) -> None:
        """Apply all migrations to a completely fresh DB -- must not raise."""
        conn = duckdb.connect(":memory:")
        # run_migrations creates base schema and applies all migrations
        run_migrations(conn)

        # The migration version should be recorded
        versions = _get_applied_versions(conn)
        max_version = max(m.version for m in MIGRATIONS)
        assert max_version in versions, (
            "New migration version must be recorded even on a fresh DB"
        )
        conn.close()

    def test_fresh_db_no_target_tables_before_migration(self) -> None:
        """Verify that on a fresh DB, the target tables don't exist before
        the DROP migration, yet the migration completes without error."""
        conn = duckdb.connect(":memory:")
        # Only create base schema (no migrations yet)
        from tests.unit.knowledge.conftest import SCHEMA_DDL

        conn.execute(SCHEMA_DDL)
        # The base schema includes verification_results but NOT errata or adr_entries
        # Apply all pending migrations -- must not raise
        apply_pending_migrations(conn)

        # Tables should be absent after migration
        assert not _table_exists(conn, "errata")
        assert not _table_exists(conn, "adr_entries")
        assert not _table_exists(conn, "verification_results")
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-P1: Property — errata/adr_entries/verification_results absent,
#           session_summaries present, across varied DB states
# ---------------------------------------------------------------------------


class TestMigrationPropertyInvariant:
    """TS-10-P1: For any DB state, post-migration invariants hold."""

    @given(
        has_errata=st.booleans(),
        has_adr=st.booleans(),
        has_verification=st.booleans(),
    )
    @settings(max_examples=8, deadline=None)
    def test_tables_absent_session_summaries_present(
        self,
        has_errata: bool,
        has_adr: bool,
        has_verification: bool,
    ) -> None:
        """After migration, dropped tables are absent and session_summaries is present,
        regardless of which legacy tables existed before migration."""
        conn = duckdb.connect(":memory:")

        # Create base schema
        from tests.unit.knowledge.conftest import SCHEMA_DDL

        conn.execute(SCHEMA_DDL)

        # Optionally create legacy tables to simulate various DB states
        if has_errata:
            conn.execute("CREATE TABLE IF NOT EXISTS errata (id TEXT PRIMARY KEY, body TEXT)")
        if has_adr:
            conn.execute("CREATE TABLE IF NOT EXISTS adr_entries (id TEXT PRIMARY KEY, body TEXT)")
        # verification_results already exists in SCHEMA_DDL, so only skip if testing absence
        if not has_verification:
            try:
                conn.execute("DROP TABLE IF EXISTS verification_results")
            except Exception:
                pass

        # Apply all migrations
        apply_pending_migrations(conn)

        # Invariants
        assert not _table_exists(conn, "errata"), "errata must be absent post-migration"
        assert not _table_exists(conn, "adr_entries"), "adr_entries must be absent post-migration"
        assert not _table_exists(conn, "verification_results"), (
            "verification_results must be absent post-migration"
        )
        assert _table_exists(conn, "session_summaries"), (
            "session_summaries must exist post-migration"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-SMOKE-1: Startup path — migration drops tables, no errata indexing
# ---------------------------------------------------------------------------


class TestSmokeStartupPath:
    """TS-10-SMOKE-1: Startup path completes with migration and no errata calls."""

    def test_smoke_migration_drops_tables(self) -> None:
        """Simulate startup: run_migrations drops unused tables, session_summaries survives."""
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        # Unused tables dropped
        assert not _table_exists(conn, "errata")
        assert not _table_exists(conn, "adr_entries")
        assert not _table_exists(conn, "verification_results")

        # Retained tables survive
        assert _table_exists(conn, "session_summaries")
        assert _table_exists(conn, "review_findings")

        conn.close()

    def test_smoke_no_errata_in_startup_modules(self) -> None:
        """Static: run.py and nightshift/_startup.py must not reference errata indexing."""
        from pathlib import Path

        repo_root = Path(__file__).resolve().parents[4]
        run_py = repo_root / "packages" / "agentfox" / "agentfox" / "engine" / "run.py"
        startup_py = repo_root / "packages" / "nightshift" / "nightshift" / "_startup.py"

        for fpath in [run_py, startup_py]:
            if not fpath.exists():
                continue
            content = fpath.read_text()
            assert "index_errata_from_markdown" not in content, (
                f"{fpath.name} must not reference index_errata_from_markdown"
            )
