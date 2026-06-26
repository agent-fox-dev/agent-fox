"""Property tests for DuckDB reader/writer split (spec 06).

TS-06-P1: Read-only call sites never perform writes.
TS-06-P2: open_knowledge_store requires explicit read_only on every call.
TS-06-P3: assemble_context performs zero writes.
TS-06-P5: af standup always uses read-only connection.

Requirements: 06-REQ-1.1, 06-REQ-2.1, 06-REQ-2.2, 06-REQ-3.1,
              06-REQ-4.1, 06-REQ-4.2, 06-REQ-7.2, 06-REQ-8.1, 06-REQ-8.2,
              06-REQ-10.1
"""

from __future__ import annotations

import tempfile
from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb
import pytest
from agentfox.core.config import KnowledgeConfig
from agentfox.knowledge.db import open_knowledge_store
from agentfox.knowledge.migrations import apply_pending_migrations
from hypothesis import given, settings
from hypothesis import strategies as st

from tests.unit.knowledge.conftest import SCHEMA_DDL


def _create_migrated_conn() -> duckdb.DuckDBPyConnection:
    """Create in-memory DuckDB with full schema and migrations."""
    conn = duckdb.connect(":memory:")
    conn.execute(SCHEMA_DDL)
    apply_pending_migrations(conn)
    return conn


def _snapshot_row_counts(conn: duckdb.DuckDBPyConnection) -> dict[str, int]:
    """Snapshot row counts of all tables in the database."""
    tables = conn.execute("SELECT table_name FROM information_schema.tables WHERE table_schema = 'main'").fetchall()
    return {name: conn.execute(f"SELECT COUNT(*) FROM {name}").fetchone()[0] for (name,) in tables}


def _seed_plan_data(conn: duckdb.DuckDBPyConnection) -> None:
    """Seed the database with a minimal plan for testing read operations."""
    conn.execute("INSERT INTO plan_meta (id, content_hash, fast_mode, version) VALUES (1, 'hash123', false, '1')")
    conn.execute(
        "INSERT INTO plan_nodes (id, spec_name, group_number, title) VALUES ('n1', 'test_spec', 1, 'Test Task')"
    )
    conn.execute("INSERT INTO plan_edges (from_node, to_node, edge_type) VALUES ('n1', 'n1', 'intra_spec')")


def _seed_review_data(conn: duckdb.DuckDBPyConnection) -> None:
    """Seed the database with review findings for testing."""
    conn.execute(
        "INSERT INTO review_findings "
        "(id, severity, description, requirement_ref, spec_name, task_group, session_id) "
        "VALUES (gen_random_uuid(), 'critical', 'test finding', 'REQ-1', 'test_spec', '1', 'sess1')"
    )


# ---------------------------------------------------------------------------
# TS-06-P1: Read-only call sites never perform writes
# ---------------------------------------------------------------------------

_READ_ONLY_CALL_SITES = [
    "af_code",
    "af_plan_verify",
    "fix_analyzer",
    "assemble_context",
]


class TestReadOnlyCallSitesNeverWrite:
    """TS-06-P1: For any designated read-only call site, the connection
    used is always opened with read_only=True and no write SQL statement
    is executed on knowledge.duckdb during its lifetime."""

    @pytest.mark.parametrize("call_site", _READ_ONLY_CALL_SITES)
    def test_call_site_produces_no_db_mutations(self, call_site: str, tmp_path: Path) -> None:
        """Each read-only call site must produce zero DB mutations."""
        conn = _create_migrated_conn()
        _seed_plan_data(conn)
        _seed_review_data(conn)
        counts_before = _snapshot_row_counts(conn)

        if call_site == "af_code":
            from agentfox.graph.analyzer import compute_phases
            from agentfox.graph.persistence import load_plan

            graph = load_plan(conn)
            if graph is not None:
                compute_phases(graph)
        elif call_site == "af_plan_verify":
            from agentfox.graph.persistence import load_plan

            load_plan(conn)
        elif call_site == "fix_analyzer":
            from agentfox.knowledge.review_store import query_active_findings

            query_active_findings(conn, spec_name="")
        elif call_site == "assemble_context":
            from agentfox.session.context import assemble_context

            spec_dir = tmp_path / "test_spec"
            spec_dir.mkdir()
            (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')
            try:
                assemble_context(spec_dir=spec_dir, task_group=1, conn=conn, project_root=tmp_path)
            except Exception:
                pass

        counts_after = _snapshot_row_counts(conn)
        assert counts_before == counts_after, (
            f"{call_site} modified DB state: before={counts_before}, after={counts_after}"
        )
        conn.close()

    @pytest.mark.parametrize("call_site", ["af_code", "af_plan_verify", "fix_analyzer"])
    def test_call_site_opens_with_read_only_true(self, call_site: str) -> None:
        """Each read-only call site must call open_knowledge_store with read_only=True."""
        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        if call_site == "af_code":
            with (
                patch("af.code.open_knowledge_store", return_value=mock_db) as mock_oks,
                patch("agentfox.graph.persistence.load_plan", return_value=None),
                patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=MagicMock(exists=lambda: True)),
                patch("agentfox.graph.analyzer.compute_phases", return_value=[]),
                patch("agentfox.graph.analyzer.critical_path", return_value=[]),
                patch("agentfox.graph.analyzer.group_edges", return_value=MagicMock(intra_spec=[], cross_spec=[])),
                patch("agentfox.spec.discovery.discover_specs", return_value=[]),
                patch("agentfox.graph.planner.format_plan_analysis", return_value="output"),
            ):
                from af.app import main
                from click.testing import CliRunner

                CliRunner().invoke(main, ["code", "--dry-run"])
            mock_oks.assert_called_once()
            assert mock_oks.call_args.kwargs.get("read_only") is True
        elif call_site == "af_plan_verify":
            with (
                patch("af.plan.open_knowledge_store", return_value=mock_db) as mock_oks,
                patch("af.plan.build_plan", return_value=MagicMock(nodes={}, edges=[], order=[])),
                patch("af.plan.load_plan", return_value=None),
                patch("af.plan.discover_specs", return_value=[]),
                patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=MagicMock(exists=lambda: True)),
            ):
                from af.app import main
                from click.testing import CliRunner

                CliRunner().invoke(main, ["plan", "--verify", "--specs-dir", "/tmp"])
            if mock_oks.called:
                assert mock_oks.call_args.kwargs.get("read_only") is True
        elif call_site == "fix_analyzer":
            with (
                patch("agentfox.knowledge.db.open_knowledge_store", return_value=mock_db) as mock_oks,
                patch("agentfox.knowledge.review_store.query_active_findings", return_value=[]),
            ):
                from agentfox.fix.analyzer import load_review_context

                load_review_context(Path("/tmp/fake"))
            mock_oks.assert_called_once()
            assert mock_oks.call_args.kwargs.get("read_only") is True


# ---------------------------------------------------------------------------
# TS-06-P2: open_knowledge_store requires explicit read_only
# ---------------------------------------------------------------------------


class TestOpenKnowledgeStoreRequiresReadOnly:
    """TS-06-P2: For any invocation of open_knowledge_store, read_only
    must always be supplied. Omitting it always raises TypeError before
    any I/O regardless of other arguments."""

    @given(
        store_path_suffix=st.text(
            alphabet=st.characters(categories=("L", "N"), min_codepoint=ord("a"), max_codepoint=ord("z")),
            min_size=1,
            max_size=10,
        ),
    )
    @settings(max_examples=10, deadline=10000)
    def test_omitting_read_only_raises_type_error_for_any_config(self, store_path_suffix: str) -> None:
        """For any valid KnowledgeConfig, calling open_knowledge_store
        without read_only raises TypeError before any file I/O."""
        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            config = KnowledgeConfig(store_path=str(tmp / f"{store_path_suffix}.duckdb"))
            with pytest.raises(TypeError, match="read_only"):
                open_knowledge_store(config)  # type: ignore[call-arg]
            db_file = tmp / f"{store_path_suffix}.duckdb"
            assert not db_file.exists(), "No DB file should be created when read_only is omitted"

    def test_omitting_read_only_with_no_args_raises_type_error(self) -> None:
        """Calling open_knowledge_store() with no arguments raises TypeError."""
        with pytest.raises(TypeError):
            open_knowledge_store()  # type: ignore[call-arg]

    def test_supplying_read_only_true_does_not_raise_type_error(self, tmp_path: Path) -> None:
        """Calling with read_only=True does not raise TypeError."""
        db_path = tmp_path / "test.duckdb"
        duckdb.connect(str(db_path)).close()
        config = KnowledgeConfig(store_path=str(db_path))
        db = open_knowledge_store(config, read_only=True)
        db.close()

    def test_supplying_read_only_false_does_not_raise_type_error(self, tmp_path: Path) -> None:
        """Calling with read_only=False does not raise TypeError."""
        config = KnowledgeConfig(store_path=str(tmp_path / "test.duckdb"))
        db = open_knowledge_store(config, read_only=False)
        db.close()


# ---------------------------------------------------------------------------
# TS-06-P3: assemble_context performs zero writes
# ---------------------------------------------------------------------------


class TestAssembleContextZeroWrites:
    """TS-06-P3: For any invocation of assemble_context,
    _migrate_legacy_files is not called and the conn is used
    exclusively for SELECT operations."""

    @pytest.mark.parametrize("task_group", [1, 2, 3], ids=["group_1", "group_2", "group_3"])
    def test_no_write_functions_called_for_any_group(self, task_group: int, tmp_path: Path) -> None:
        """assemble_context must not call _migrate_legacy_files for any task group."""
        conn = _create_migrated_conn()
        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')

        with patch("agentfox.session.context._migrate_legacy_files") as mock_migrate:
            from agentfox.session.context import assemble_context

            try:
                assemble_context(spec_dir=spec_dir, task_group=task_group, conn=conn, project_root=tmp_path)
            except Exception:
                pass
            assert mock_migrate.call_count == 0
        conn.close()

    @pytest.mark.parametrize("task_group", [1, 2, 3], ids=["group_1", "group_2", "group_3"])
    def test_db_state_unchanged_for_any_group(self, task_group: int, tmp_path: Path) -> None:
        """assemble_context must not modify DB state for any task group."""
        conn = _create_migrated_conn()
        _seed_plan_data(conn)
        _seed_review_data(conn)
        spec_dir = tmp_path / "test_spec"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text('{"version":"1.2","tasks":[]}')
        counts_before = _snapshot_row_counts(conn)

        from agentfox.session.context import assemble_context

        try:
            assemble_context(spec_dir=spec_dir, task_group=task_group, conn=conn, project_root=tmp_path)
        except Exception:
            pass
        counts_after = _snapshot_row_counts(conn)
        assert counts_before == counts_after, (
            f"assemble_context modified DB for group {task_group}: before={counts_before}, after={counts_after}"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-06-P5: af standup always uses read-only connection
# ---------------------------------------------------------------------------


class TestStandupAlwaysReadOnly:
    """TS-06-P5: For any invocation of af standup, open_knowledge_store
    is always called with read_only=True and no write operation is
    performed on knowledge.duckdb."""

    @pytest.mark.parametrize("hours", [1, 12, 24, 48], ids=["1h", "12h", "24h", "48h"])
    def test_standup_uses_read_only_for_any_hours(self, hours: int) -> None:
        """af standup must use read_only=True regardless of --hours value."""
        from agentfox.nightshift.pid import PidStatus

        mock_db = MagicMock()
        mock_db.connection = MagicMock()
        mock_report = MagicMock()
        mock_report.cost_by_spec = {}
        mock_report.cost_by_archetype = {}
        mock_db_path = MagicMock()
        mock_db_path.exists.return_value = True

        with (
            patch("af.standup.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.standup.DEFAULT_DB_PATH", new=mock_db_path),
            patch("af.standup.generate_standup", return_value=mock_report),
            patch("agentfox.nightshift.pid.check_pid_file", return_value=(PidStatus.ABSENT, None)),
        ):
            from af.app import main
            from click.testing import CliRunner

            CliRunner().invoke(main, ["standup", "--hours", str(hours)])

        mock_oks.assert_called_once()
        assert mock_oks.call_args.kwargs.get("read_only") is True, f"af standup --hours {hours} must use read_only=True"
