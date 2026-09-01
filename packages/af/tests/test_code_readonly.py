"""Tests for af code read-only database connection.

Verifies that af code calls open_knowledge_store with read_only=True,
performs no write operations, and handles missing DB gracefully.

Test Spec: TS-06-4, TS-06-5, TS-06-E2
Requirements: 06-REQ-2.1, 06-REQ-2.2, 06-REQ-2.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from click.testing import CliRunner


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Prevent daemon check from interfering with tests."""
    from agentfox.maintenance.pid import PidStatus

    monkeypatch.setattr(
        "agentfox.maintenance.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


# -----------------------------------------------------------------------
# TS-06-4: af code calls open_knowledge_store with read_only=True
# -----------------------------------------------------------------------


class TestAfCodeReadOnly:
    """TS-06-4: af code must open knowledge store with read_only=True."""

    def test_code_dry_run_uses_read_only_true(self, cli_runner: CliRunner) -> None:
        """af code --dry-run must call open_knowledge_store with
        read_only=True before executing load_plan and compute_phases."""
        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        mock_graph = MagicMock()
        mock_graph.nodes = {"n1": MagicMock(status=MagicMock(__eq__=lambda s, o: False))}
        mock_graph.edges = []
        mock_graph.order = ["n1"]
        mock_graph.metadata = MagicMock(
            created_at="2024-01-01",
            fast_mode=False,
            filtered_spec=None,
            version="1",
        )

        mock_db_path = MagicMock(exists=lambda: True)

        with (
            patch("af.code.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("agentfox.graph.persistence.load_plan", return_value=mock_graph),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=mock_db_path),
            patch("agentfox.graph.analyzer.compute_phases", return_value=[]),
            patch("agentfox.graph.analyzer.critical_path", return_value=[]),
            patch(
                "agentfox.graph.analyzer.group_edges",
                return_value=MagicMock(intra_spec=[], cross_spec=[]),
            ),
            patch("agentfox.spec.discovery.discover_specs", return_value=[]),
            patch("agentfox.graph.planner.format_plan_analysis", return_value="Plan output"),
        ):
            cli_runner.invoke(main, ["code", "--dry-run"])

        # Verify open_knowledge_store was called with read_only=True
        mock_oks.assert_called_once()
        call_kwargs = mock_oks.call_args
        assert call_kwargs.kwargs.get("read_only") is True or (
            len(call_kwargs.args) >= 2 and call_kwargs.args[1] is True
        ), "af code must call open_knowledge_store with read_only=True"


# -----------------------------------------------------------------------
# TS-06-5: af code performs no INSERT/UPDATE/DELETE
# -----------------------------------------------------------------------


class TestAfCodeNoWrites:
    """TS-06-5: af code must not perform any write operations on the DB."""

    def test_code_dry_run_no_write_operations(self) -> None:
        """Running af code's read path (load_plan + compute_phases) against
        a seeded test DB must not produce any INSERT, UPDATE, or DELETE
        operations — row counts must be identical before and after."""
        import duckdb
        from agentfox.graph.analyzer import compute_phases
        from agentfox.graph.persistence import load_plan
        from agentfox.knowledge.migrations import run_migrations

        # Create in-memory DB with the real schema (all migrations applied)
        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        # Seed with sample plan data matching the real schema
        conn.execute("INSERT INTO plan_meta (id, content_hash, fast_mode, version) VALUES (1, 'abc123', false, '1')")
        conn.execute(
            "INSERT INTO plan_nodes (id, spec_name, group_number, title) VALUES ('n1', 'spec_a', 1, 'Task One')"
        )
        conn.execute("INSERT INTO plan_edges (from_node, to_node, edge_type) VALUES ('n1', 'n1', 'intra_spec')")

        # Snapshot row counts before
        nodes_before = conn.execute("SELECT COUNT(*) FROM plan_nodes").fetchone()[0]
        edges_before = conn.execute("SELECT COUNT(*) FROM plan_edges").fetchone()[0]
        meta_before = conn.execute("SELECT COUNT(*) FROM plan_meta").fetchone()[0]

        # Actually invoke the read functions that af code uses
        graph = load_plan(conn)
        if graph is not None:
            compute_phases(graph)

        # Snapshot row counts after — must be identical
        nodes_after = conn.execute("SELECT COUNT(*) FROM plan_nodes").fetchone()[0]
        edges_after = conn.execute("SELECT COUNT(*) FROM plan_edges").fetchone()[0]
        meta_after = conn.execute("SELECT COUNT(*) FROM plan_meta").fetchone()[0]

        assert nodes_before == nodes_after, "load_plan must not INSERT/UPDATE/DELETE plan_nodes"
        assert edges_before == edges_after, "load_plan must not INSERT/UPDATE/DELETE plan_edges"
        assert meta_before == meta_after, "load_plan must not INSERT/UPDATE/DELETE plan_meta"
        conn.close()


# -----------------------------------------------------------------------
# TS-06-E2: af code exits non-zero when knowledge.duckdb is missing
# -----------------------------------------------------------------------


class TestAfCodeMissingDB:
    """TS-06-E2: af code must exit non-zero when DB file is absent."""

    def test_code_dry_run_missing_db_exits_nonzero(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """When knowledge.duckdb does not exist, af code --dry-run must
        exit with a non-zero status code and display an error message."""
        nonexistent_db = tmp_path / "nonexistent" / "knowledge.duckdb"

        with patch(
            "agentfox.core.node_id.DEFAULT_DB_PATH",
            new=nonexistent_db,
        ):
            result = cli_runner.invoke(main, ["code", "--dry-run"])

        assert result.exit_code != 0, f"Expected non-zero exit code when DB is missing, got {result.exit_code}"
        # Should contain an informative error message
        output = result.output.lower()
        assert "plan" in output or "error" in output or "not found" in output, (
            f"Expected informative error message, got: {result.output}"
        )
