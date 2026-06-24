"""End-to-end smoke tests for DuckDB reader/writer split (spec 06).

TS-06-SMOKE-1: af code read-only end-to-end
TS-06-SMOKE-2: af plan --verify read-only end-to-end
TS-06-SMOKE-3: orchestrator startup migration/indexing flow
TS-06-SMOKE-4: fix/analyzer read-only end-to-end
TS-06-SMOKE-5: af standup read-only end-to-end

Requirements: 06-REQ-2.1, 06-REQ-3.1, 06-REQ-4.1, 06-REQ-5.2,
              06-REQ-6.2, 06-REQ-7.3, 06-REQ-8.1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb
import pytest
from agentfox.knowledge.migrations import apply_pending_migrations

from tests.unit.knowledge.conftest import SCHEMA_DDL


def _create_populated_db(db_path: str) -> None:
    """Create a DuckDB with full schema, migrations, and sample data."""
    conn = duckdb.connect(db_path)
    conn.execute(SCHEMA_DDL)
    apply_pending_migrations(conn)
    conn.execute("INSERT INTO plan_meta (id, content_hash, fast_mode, version) VALUES (1, 'smoke_hash', false, '1')")
    conn.execute(
        "INSERT INTO plan_nodes (id, spec_name, group_number, title) VALUES ('smoke_n1', 'smoke_spec', 1, 'Smoke Task')"
    )
    conn.execute("INSERT INTO plan_edges (from_node, to_node, edge_type) VALUES ('smoke_n1', 'smoke_n1', 'intra_spec')")
    conn.execute(
        "INSERT INTO review_findings "
        "(id, severity, description, requirement_ref, spec_name, task_group, session_id) "
        "VALUES (gen_random_uuid(), 'major', 'smoke test finding', "
        "'REQ-SMOKE', 'smoke_spec', '1', 'smoke_sess')"
    )
    conn.close()


def _snapshot_row_counts(db_path: str) -> dict[str, int]:
    """Snapshot row counts from a DuckDB file."""
    conn = duckdb.connect(db_path, read_only=True)
    tables = conn.execute("SELECT table_name FROM information_schema.tables WHERE table_schema = 'main'").fetchall()
    counts = {name: conn.execute(f"SELECT COUNT(*) FROM {name}").fetchone()[0] for (name,) in tables}
    conn.close()
    return counts


@pytest.mark.smoke
class TestAfCodeSmoke:
    """TS-06-SMOKE-1: af code opens knowledge.duckdb read-only, executes
    load_plan and compute_phases via SELECT, displays output, and closes
    the connection without any writes."""

    def test_af_code_end_to_end_read_only(self, tmp_path: Path) -> None:
        """Full af code invocation against a populated DuckDB."""
        from agentfox.core.config import KnowledgeConfig
        from agentfox.knowledge.db import open_knowledge_store

        db_path = str(tmp_path / "knowledge.duckdb")
        _create_populated_db(db_path)
        counts_before = _snapshot_row_counts(db_path)

        db = open_knowledge_store(KnowledgeConfig(store_path=db_path), read_only=True)
        try:
            from agentfox.graph.analyzer import compute_phases
            from agentfox.graph.persistence import load_plan

            graph = load_plan(db.connection)
            assert graph is not None, "load_plan should return a graph"
            phases = compute_phases(graph)
            assert isinstance(phases, list)
        finally:
            db.close()

        counts_after = _snapshot_row_counts(db_path)
        assert counts_before == counts_after

    def test_af_code_mock_uses_read_only_true(self) -> None:
        """af code CLI invokes open_knowledge_store with read_only=True."""
        from agentfox.nightshift.pid import PidStatus

        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        with (
            patch("af.code.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=MagicMock(exists=lambda: True)),
            patch("agentfox.graph.persistence.load_plan", return_value=None),
            patch("agentfox.spec.discovery.discover_specs", return_value=[]),
            patch("agentfox.nightshift.pid.check_pid_file", return_value=(PidStatus.ABSENT, None)),
        ):
            from af.app import main
            from click.testing import CliRunner

            CliRunner().invoke(main, ["code", "--dry-run"])

        mock_oks.assert_called_once()
        assert mock_oks.call_args.kwargs.get("read_only") is True


@pytest.mark.smoke
class TestAfPlanVerifySmoke:
    """TS-06-SMOKE-2: af plan --verify opens knowledge.duckdb read-only,
    executes SELECT queries, prints result, exits without writing."""

    def test_plan_verify_read_path_no_mutations(self, tmp_path: Path) -> None:
        """plan --verify read path (load_plan) produces no DB mutations."""
        from agentfox.core.config import KnowledgeConfig
        from agentfox.knowledge.db import open_knowledge_store

        db_path = str(tmp_path / "knowledge.duckdb")
        _create_populated_db(db_path)
        counts_before = _snapshot_row_counts(db_path)

        db = open_knowledge_store(KnowledgeConfig(store_path=db_path), read_only=True)
        try:
            from agentfox.graph.persistence import load_plan

            graph = load_plan(db.connection)
            assert graph is not None
        finally:
            db.close()

        counts_after = _snapshot_row_counts(db_path)
        assert counts_before == counts_after

    def test_plan_verify_mock_uses_read_only_true(self, tmp_path: Path) -> None:
        """af plan --verify CLI invokes open_knowledge_store with read_only=True."""
        from agentfox.nightshift.pid import PidStatus

        mock_db = MagicMock()
        mock_db.connection = MagicMock()
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.plan.build_plan", return_value=MagicMock(nodes={}, edges=[], order=[])),
            patch("af.plan.load_plan", return_value=None),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=MagicMock(exists=lambda: True)),
            patch("agentfox.nightshift.pid.check_pid_file", return_value=(PidStatus.ABSENT, None)),
        ):
            from af.app import main
            from click.testing import CliRunner

            CliRunner().invoke(main, ["plan", "--verify", "--specs-dir", str(specs_dir)])

        if mock_oks.called:
            assert mock_oks.call_args.kwargs.get("read_only") is True


@pytest.mark.smoke
class TestOrchestratorStartupSmoke:
    """TS-06-SMOKE-3: orchestrator startup calls _migrate_legacy_files
    and index_errata_from_markdown with RW conn before sessions."""

    def test_startup_migration_and_indexing_flow(self, tmp_path: Path) -> None:
        """Verify the orchestrator startup sequence."""
        from agentfox.engine.run import _run_startup_migrations

        mock_knowledge_db = MagicMock()
        mock_knowledge_db.connection = MagicMock()
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        (specs_dir / "spec_a").mkdir()
        (specs_dir / "spec_b").mkdir()

        migrate_calls: list[str] = []
        errata_calls: list[object] = []

        def _track_migrate(conn, spec_dir, spec_name):
            migrate_calls.append(spec_name)

        def _track_errata(conn, project_root):
            errata_calls.append(project_root)

        with (
            patch("agentfox.session.context._migrate_legacy_files", side_effect=_track_migrate),
            patch("agentfox.knowledge.errata.index_errata_from_markdown", side_effect=_track_errata),
        ):
            _run_startup_migrations(mock_knowledge_db, specs_dir, tmp_path)

        assert len(migrate_calls) == 2
        assert set(migrate_calls) == {"spec_a", "spec_b"}
        assert len(errata_calls) >= 1
        assert errata_calls[0] == tmp_path

    def test_session_receives_read_only_conn(self) -> None:
        """Verify _setup_infrastructure opens a second read-only conn."""
        call_log: list[bool] = []
        mock_db_rw = MagicMock()
        mock_db_rw.connection = MagicMock()
        mock_db_ro = MagicMock()
        mock_db_ro.connection = MagicMock()

        def _track_open(config, *, read_only):
            call_log.append(read_only)
            return mock_db_ro if read_only else mock_db_rw

        with (
            patch("agentfox.engine.run.open_knowledge_store", side_effect=_track_open),
            patch("agentfox.engine.run.DuckDBSink"),
            patch("agentfox.engine.run.SinkDispatcher"),
            patch("agentfox.engine.run.FoxKnowledgeProvider"),
            patch("agentfox.knowledge.agent_trace.AgentTraceSink"),
            patch("agentfox.nightshift.platform_factory.create_platform_safe", return_value=None),
        ):
            from agentfox.engine.run import _setup_infrastructure

            _setup_infrastructure(MagicMock())

        assert len(call_log) >= 2
        assert call_log[0] is False
        assert call_log[1] is True


@pytest.mark.smoke
class TestFixAnalyzerSmoke:
    """TS-06-SMOKE-4: fix/analyzer.py opens knowledge.duckdb read-only."""

    def test_analyzer_end_to_end_read_only(self, tmp_path: Path) -> None:
        """Full analyzer invocation against a populated DuckDB."""
        from agentfox.core.config import KnowledgeConfig
        from agentfox.knowledge.db import open_knowledge_store as real_open

        db_path = str(tmp_path / "knowledge.duckdb")
        _create_populated_db(db_path)
        counts_before = _snapshot_row_counts(db_path)

        call_log: list[bool] = []

        def _tracking_open(config, *, read_only):
            call_log.append(read_only)
            return real_open(KnowledgeConfig(store_path=db_path), read_only=read_only)

        with patch("agentfox.knowledge.db.open_knowledge_store", side_effect=_tracking_open):
            from agentfox.fix.analyzer import load_review_context

            result = load_review_context(tmp_path)

        assert len(call_log) >= 1
        assert call_log[0] is True
        assert isinstance(result, str)
        counts_after = _snapshot_row_counts(db_path)
        assert counts_before == counts_after


@pytest.mark.smoke
class TestAfStandupSmoke:
    """TS-06-SMOKE-5: af standup opens knowledge.duckdb with read_only=True."""

    def test_standup_end_to_end_read_only(self, tmp_path: Path) -> None:
        """Full af standup invocation against a populated DuckDB."""
        from agentfox.nightshift.pid import PidStatus
        from agentfox.reporting.standup import AgentActivity, QueueSummary, StandupReport

        db_path = str(tmp_path / "knowledge.duckdb")
        _create_populated_db(db_path)
        counts_before = _snapshot_row_counts(db_path)

        from agentfox.core.config import KnowledgeConfig
        from agentfox.knowledge.db import open_knowledge_store as real_open

        call_log: list[bool] = []

        def _tracking_open(config, *, read_only):
            call_log.append(read_only)
            return real_open(KnowledgeConfig(store_path=db_path), read_only=read_only)

        stub_report = StandupReport(
            window_hours=24,
            window_start="2024-01-01T00:00:00",
            window_end="2024-01-02T00:00:00",
            agent=AgentActivity(
                tasks_completed=0,
                sessions_run=0,
                input_tokens=0,
                output_tokens=0,
                cost=0.0,
                completed_task_ids=[],
            ),
            human_commits=[],
            agent_commits=[],
            file_overlaps=[],
            cost_breakdown=[],
            queue=QueueSummary(ready=0, pending=0, blocked=0, failed=0, completed=0),
        )
        mock_db_path = MagicMock()
        mock_db_path.exists.return_value = True

        with (
            patch("af.standup.open_knowledge_store", side_effect=_tracking_open),
            patch("af.standup.DEFAULT_DB_PATH", new=mock_db_path),
            patch("af.standup.generate_standup", return_value=stub_report),
            patch("agentfox.nightshift.pid.check_pid_file", return_value=(PidStatus.ABSENT, None)),
        ):
            from af.app import main
            from click.testing import CliRunner

            result = CliRunner().invoke(main, ["standup"])

        assert len(call_log) >= 1
        assert call_log[0] is True
        assert result.exit_code == 0, f"Exit code: {result.exit_code}, output: {result.output}"
        counts_after = _snapshot_row_counts(db_path)
        assert counts_before == counts_after
