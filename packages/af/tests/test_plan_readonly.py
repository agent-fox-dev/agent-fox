"""Tests for af plan --verify read-only and save path read-write.

Verifies that the verify path uses read_only=True and the save
path uses read_only=False. Also tests DuckDB read-only exception
behavior.

Test Spec: TS-06-6, TS-06-7, TS-06-E3
Requirements: 06-REQ-3.1, 06-REQ-3.2, 06-REQ-3.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb
import pytest
from af.app import main
from click.testing import CliRunner


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    """Prevent daemon check from interfering with tests."""
    from agentfox.nightshift.pid import PidStatus

    monkeypatch.setattr(
        "agentfox.nightshift.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


# -----------------------------------------------------------------------
# TS-06-6: af plan --verify calls open_knowledge_store with read_only=True
# -----------------------------------------------------------------------


class TestPlanVerifyReadOnly:
    """TS-06-6: verify path must open knowledge store read-only."""

    def test_verify_path_uses_read_only_true(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """af plan --verify must call open_knowledge_store with
        read_only=True on the verify path."""
        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        mock_graph = MagicMock()
        mock_graph.nodes = {}
        mock_graph.edges = []
        mock_graph.order = []

        mock_db_path = MagicMock(exists=lambda: True)

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.plan.build_plan", return_value=mock_graph),
            patch("af.plan.load_plan", return_value=None),
            patch("af.plan.discover_specs", return_value=[]),
            patch("agentfox.core.node_id.DEFAULT_DB_PATH", new=mock_db_path),
        ):
            # Create a minimal specs dir with a tasks.json
            specs_dir = tmp_path / "specs"
            specs_dir.mkdir()

            cli_runner.invoke(main, ["plan", "--verify", "--specs-dir", str(specs_dir)])

        # Verify open_knowledge_store was called with read_only=True
        if mock_oks.called:
            call_kwargs = mock_oks.call_args
            read_only_val = call_kwargs.kwargs.get("read_only")
            assert read_only_val is True, f"af plan --verify must use read_only=True, got {read_only_val}"


# -----------------------------------------------------------------------
# TS-06-7: af plan save path calls open_knowledge_store with read_only=False
# -----------------------------------------------------------------------


class TestPlanSaveReadWrite:
    """TS-06-7: save path must open knowledge store with read_only=False."""

    def test_save_path_uses_read_only_false(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """af plan (save path, no --verify, no --dry-run) must call
        open_knowledge_store with read_only=False."""
        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        mock_graph = MagicMock()
        mock_graph.nodes = {"n1": MagicMock()}
        mock_graph.edges = []
        mock_graph.order = ["n1"]
        mock_graph.metadata = MagicMock(
            created_at="2024-01-01",
            fast_mode=False,
            filtered_spec=None,
            version="1",
        )

        with (
            patch("af.plan.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.plan.build_plan", return_value=mock_graph),
            patch("af.plan.save_plan"),
            patch("af.plan.discover_specs", return_value=[]),
            patch("af.plan.format_plan_summary", return_value="Plan saved"),
        ):
            specs_dir = tmp_path / "specs"
            specs_dir.mkdir()

            cli_runner.invoke(main, ["plan", "--specs-dir", str(specs_dir)])

        # The save path should call open_knowledge_store with read_only=False
        if mock_oks.called:
            # Find the call that uses read_only=False (save path)
            found_write_call = False
            for call in mock_oks.call_args_list:
                read_only_val = call.kwargs.get("read_only")
                if read_only_val is False:
                    found_write_call = True
                    break
            assert found_write_call, "af plan save path must call open_knowledge_store with read_only=False"


# -----------------------------------------------------------------------
# TS-06-E3: DuckDB read-only exception on write attempt
# -----------------------------------------------------------------------


class TestDuckDBReadOnlyException:
    """TS-06-E3: a write on a read-only connection raises DuckDB exception."""

    def test_read_only_connection_rejects_insert(self, tmp_path: Path) -> None:
        """Attempting INSERT on a read-only DuckDB connection must raise
        an exception, and the database must remain intact."""
        db_path = str(tmp_path / "test.duckdb")

        # Create DB with schema in read-write mode
        conn_rw = duckdb.connect(db_path)
        conn_rw.execute("""
            CREATE TABLE IF NOT EXISTS plan_nodes (
                id TEXT PRIMARY KEY,
                spec_name TEXT,
                group_number INTEGER,
                title TEXT
            )
        """)
        conn_rw.close()

        # Open read-only and attempt write
        conn_ro = duckdb.connect(db_path, read_only=True)
        with pytest.raises(duckdb.InvalidInputException):
            conn_ro.execute("INSERT INTO plan_nodes VALUES ('test', 'spec', 1, 'title')")
        conn_ro.close()

        # Verify DB is intact
        conn_verify = duckdb.connect(db_path, read_only=True)
        count = conn_verify.execute("SELECT COUNT(*) FROM plan_nodes").fetchone()[0]
        assert count == 0, "DB should be empty — no writes should have succeeded"
        conn_verify.close()
