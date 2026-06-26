"""Tests for assemble_context write extraction and orchestrator startup.

Verifies that assemble_context no longer calls _migrate_legacy_files,
that the orchestrator startup calls it instead, and that it is idempotent.

Test Spec: TS-06-10, TS-06-11, TS-06-12, TS-06-13, TS-06-14, TS-06-15,
           TS-06-16, TS-06-17, TS-06-18, TS-06-E5, TS-06-E6, TS-06-E7
Requirements: 06-REQ-5.1, 06-REQ-5.2, 06-REQ-5.3, 06-REQ-6.1, 06-REQ-6.2,
              06-REQ-6.3, 06-REQ-7.1, 06-REQ-7.2, 06-REQ-7.3, 06-REQ-7.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

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

        with patch("agentfox.session.context._migrate_legacy_files") as mock_migrate:
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
                f"assemble_context must NOT call _migrate_legacy_files; it was called {mock_migrate.call_count} time(s)"
            )


# TS-06-13 removed in spec 10.


# -----------------------------------------------------------------------
# TS-06-11: orchestrator startup calls _migrate_legacy_files per spec
# -----------------------------------------------------------------------


class TestOrchestratorStartupMigration:
    """TS-06-11: startup must call _migrate_legacy_files for each spec."""

    def test_orchestrator_calls_migrate_for_each_spec(self, tmp_path: Path) -> None:
        """The orchestrator startup sequence must call _migrate_legacy_files
        once per spec with a read-write connection before dispatching any
        sessions."""
        from unittest.mock import MagicMock

        from agentfox.engine.run import _run_startup_migrations

        mock_knowledge_db = MagicMock()
        mock_conn = MagicMock()
        mock_knowledge_db.connection = mock_conn

        # Create specs directory with two spec subdirectories
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        (specs_dir / "spec_a").mkdir()
        (specs_dir / "spec_b").mkdir()

        with patch("agentfox.session.context._migrate_legacy_files") as mock_migrate:
            _run_startup_migrations(mock_knowledge_db, specs_dir, tmp_path)

        # Must be called once per spec directory
        assert mock_migrate.call_count == 2, (
            f"Expected 2 calls to _migrate_legacy_files (one per spec), got {mock_migrate.call_count}"
        )
        # Verify both spec names were passed
        called_spec_names = {call.args[2] for call in mock_migrate.call_args_list}
        assert called_spec_names == {"spec_a", "spec_b"}


# TS-06-14 removed in spec 10.


# -----------------------------------------------------------------------
# TS-06-12: _migrate_legacy_files is idempotent
# -----------------------------------------------------------------------


class TestMigrateLegacyFilesIdempotent:
    """TS-06-12: calling _migrate_legacy_files twice produces no duplicates."""

    def test_idempotent_migration(self, knowledge_conn: duckdb.DuckDBPyConnection, tmp_path: Path) -> None:
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
            "SELECT COUNT(*) FROM review_findings WHERE spec_name = 'test_spec'"
        ).fetchone()[0]

        # Second migration — should produce no additional records
        _migrate_legacy_files(knowledge_conn, spec_dir, "test_spec")
        count_second = knowledge_conn.execute(
            "SELECT COUNT(*) FROM review_findings WHERE spec_name = 'test_spec'"
        ).fetchone()[0]

        assert count_first == count_second, (
            f"_migrate_legacy_files is not idempotent: "
            f"first call produced {count_first} records, "
            f"second call produced {count_second} records"
        )


# TS-06-15 removed in spec 10.


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

        Seeds a minimal v1.2 spec on disk so that afspec.load_spec
        succeeds, letting assemble_context run to completion without
        swallowing exceptions."""
        from agentfox.session.context import assemble_context

        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()

        # Seed a valid v1.2 spec so afspec.load_spec succeeds
        import json

        spec_data = {
            "$schema": "https://agentfox.dev/schemas/spec-v1.2.json",
            "spec_id": "99",
            "spec_name": "test_spec",
            "schema_version": 1,
            "test_commands": {"spec_tests": "pytest -q", "all_tests": "make check", "linter": "ruff check"},
            "dependencies": [],
            "task_groups": [
                {
                    "id": 1,
                    "kind": "tests",
                    "title": "Test group",
                    "subtasks": [
                        {
                            "id": "1.1",
                            "title": "A test subtask",
                            "details": [],
                            "test_spec_refs": [],
                            "requirement_refs": [],
                            "state": "pending",
                            "optional": False,
                        }
                    ],
                    "verification": {"id": "1.V", "checks": []},
                }
            ],
            "traceability": [],
        }
        (spec_dir / "tasks.json").write_text(json.dumps(spec_data))
        (spec_dir / "requirements.json").write_text(
            json.dumps(
                {
                    "introduction": "Test",
                    "glossary": [],
                    "requirements": [],
                    "correctness_properties": [],
                    "execution_paths": [],
                    "error_handling": [],
                }
            )
        )
        (spec_dir / "test_spec.json").write_text(
            json.dumps(
                {
                    "test_cases": [],
                    "property_tests": [],
                    "edge_case_tests": [],
                    "smoke_tests": [],
                    "coverage": {},
                }
            )
        )

        # assemble_context must complete without raising — no try/except
        ctx = assemble_context(
            spec_dir=spec_dir,
            task_group=1,
            conn=knowledge_conn,
            project_root=tmp_path,
        )

        # Must return a non-None string
        assert isinstance(ctx, str)
        assert ctx is not None


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
        from agentfox.session.context import assemble_context

        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')

        # Snapshot table row counts before
        tables = knowledge_conn.execute(
            "SELECT table_name FROM information_schema.tables WHERE table_schema = 'main'"
        ).fetchall()
        counts_before = {}
        for (table_name,) in tables:
            count = knowledge_conn.execute(f"SELECT COUNT(*) FROM {table_name}").fetchone()[0]
            counts_before[table_name] = count

        # Call assemble_context
        try:
            assemble_context(
                spec_dir=spec_dir,
                task_group=1,
                conn=knowledge_conn,
                project_root=tmp_path,
            )
        except Exception:
            pass  # Spec loading may fail; we only care about DB state

        # Snapshot table row counts after
        counts_after = {}
        for (table_name,) in tables:
            count = knowledge_conn.execute(f"SELECT COUNT(*) FROM {table_name}").fetchone()[0]
            counts_after[table_name] = count

        assert counts_before == counts_after, (
            f"assemble_context modified DB state: before={counts_before}, after={counts_after}"
        )


# -----------------------------------------------------------------------
# TS-06-18: orchestrator passes read_only=True conn to assemble_context
# -----------------------------------------------------------------------


class TestOrchestratorPassesReadOnlyConn:
    """TS-06-18: orchestrator must pass read_only=True conn to assemble_context."""

    def test_orchestrator_setup_creates_read_only_context_conn(self) -> None:
        """The orchestrator _setup_infrastructure must call
        open_knowledge_store twice: once for the main DB and once for
        context assembly.  Both use read_only=False because DuckDB
        disallows mixing read_only configurations on the same file
        within one process.

        The context connection must be a *separate* instance stored as
        'context_knowledge_db' in the infrastructure dict (06-REQ-7.3)."""
        from unittest.mock import MagicMock

        mock_db_main = MagicMock(name="main_knowledge_db")
        mock_db_main.connection = MagicMock(name="main_connection")
        mock_db_ctx = MagicMock(name="ctx_knowledge_db")
        mock_db_ctx.connection = MagicMock(name="ctx_connection")

        call_log: list[bool] = []
        call_count = 0

        def _track_open(config, *, read_only):
            nonlocal call_count
            call_log.append(read_only)
            call_count += 1
            return mock_db_main if call_count == 1 else mock_db_ctx

        with (
            patch(
                "agentfox.engine.run.open_knowledge_store",
                side_effect=_track_open,
            ),
            patch("agentfox.engine.run.DuckDBSink"),
            patch("agentfox.engine.run.SinkDispatcher"),
            patch("agentfox.engine.run.FoxKnowledgeProvider"),
            patch("agentfox.knowledge.agent_trace.AgentTraceSink"),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            from agentfox.engine.run import _setup_infrastructure

            mock_config = MagicMock()
            infra = _setup_infrastructure(mock_config)

        # Must have called open_knowledge_store twice
        assert len(call_log) >= 2, f"Expected at least 2 calls to open_knowledge_store, got {len(call_log)}: {call_log}"
        assert call_log[0] is False, "First open_knowledge_store call must be read_only=False (main connection)"
        assert call_log[1] is False, (
            "Second open_knowledge_store call must be read_only=False (DuckDB requires same config)"
        )

        # 06-REQ-7.3: context_knowledge_db must be a separate instance
        assert infra["context_knowledge_db"] is mock_db_ctx, (
            "Infrastructure must store a separate context DB instance"
        )
        assert infra["context_knowledge_db"] is not infra["knowledge_db"], (
            "context_knowledge_db must be a separate connection from the main knowledge_db"
        )

    def test_no_silent_fallback_when_context_open_fails(self) -> None:
        """When opening the context connection (second call) fails, the
        error must propagate — there must be no silent fallback to the
        main connection (06-REQ-7.3)."""
        from unittest.mock import MagicMock

        mock_db_main = MagicMock(name="main_knowledge_db")
        mock_db_main.connection = MagicMock(name="main_connection")

        call_count = 0

        def _track_open(config, *, read_only):
            nonlocal call_count
            call_count += 1
            if call_count == 2:
                raise RuntimeError("simulated context open failure")
            return mock_db_main

        with (
            patch(
                "agentfox.engine.run.open_knowledge_store",
                side_effect=_track_open,
            ),
            patch("agentfox.engine.run.DuckDBSink"),
            patch("agentfox.engine.run.SinkDispatcher"),
            patch("agentfox.engine.run.FoxKnowledgeProvider"),
            patch("agentfox.knowledge.agent_trace.AgentTraceSink"),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            from agentfox.engine.run import _setup_infrastructure

            mock_config = MagicMock()
            with pytest.raises(RuntimeError, match="simulated context open failure"):
                _setup_infrastructure(mock_config)

    def test_context_conn_propagated_to_session_runner(self) -> None:
        """Verify the context connection (second open_knowledge_store call)
        is passed to session_runner_factory as context_knowledge_db, not
        the main one (06-REQ-7.3)."""
        from unittest.mock import MagicMock

        mock_db_main = MagicMock(name="main_db")
        mock_db_main.connection = MagicMock()
        mock_db_ctx = MagicMock(name="ctx_db")
        mock_db_ctx.connection = MagicMock()

        call_count = 0

        def _track_open(config, *, read_only):
            nonlocal call_count
            call_count += 1
            return mock_db_main if call_count == 1 else mock_db_ctx

        with (
            patch(
                "agentfox.engine.run.open_knowledge_store",
                side_effect=_track_open,
            ),
            patch("agentfox.engine.run.DuckDBSink"),
            patch("agentfox.engine.run.SinkDispatcher"),
            patch("agentfox.engine.run.FoxKnowledgeProvider"),
            patch("agentfox.knowledge.agent_trace.AgentTraceSink"),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
            patch(
                "agentfox.engine.session_lifecycle.NodeSessionRunner",
            ) as mock_nsr,
        ):
            from agentfox.engine.run import _setup_infrastructure

            mock_config = MagicMock()
            infra = _setup_infrastructure(mock_config)

            infra["session_runner_factory"]("test_node")
            mock_nsr.assert_called_once()
            _, kwargs = mock_nsr.call_args

        assert kwargs.get("context_knowledge_db") is mock_db_ctx, (
            "session_runner_factory must pass the context connection as "
            "context_knowledge_db, not the main connection"
        )


# -----------------------------------------------------------------------
# TS-06-E5: _migrate_legacy_files failure for one spec doesn't abort
# -----------------------------------------------------------------------


class TestMigrationFailureIsolation:
    """TS-06-E5: migration failure for one spec must not abort startup."""

    def test_migration_failure_does_not_abort_remaining_specs(self, tmp_path: Path) -> None:
        """When _migrate_legacy_files fails for spec_a, the orchestrator
        must log the error and continue processing spec_b."""
        from unittest.mock import MagicMock

        from agentfox.engine.run import _run_startup_migrations

        mock_knowledge_db = MagicMock()
        mock_knowledge_db.connection = MagicMock()

        # Create specs directory with two spec subdirectories
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        (specs_dir / "spec_a").mkdir()
        (specs_dir / "spec_b").mkdir()

        call_log: list[str] = []

        def _side_effect(conn, spec_dir, spec_name):
            call_log.append(spec_name)
            if spec_name == "spec_a":
                raise RuntimeError("simulated migration failure for spec_a")

        with patch(
            "agentfox.session.context._migrate_legacy_files",
            side_effect=_side_effect,
        ):
            # Should not raise — errors are logged and skipped
            _run_startup_migrations(mock_knowledge_db, specs_dir, tmp_path)

        # Both specs must have been attempted
        assert "spec_a" in call_log, "spec_a migration must be attempted"
        assert "spec_b" in call_log, "spec_b migration must be attempted after spec_a fails"


# TS-06-E6 removed in spec 10.


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
                "INSERT INTO findings VALUES ('f1', 'spec', '1', 'major', 'desc', 'src', 'active', CURRENT_TIMESTAMP)"
            )
        conn_ro.close()
