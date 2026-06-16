"""Tests for the coder session pre-flight check.

Verifies that the preflight module correctly identifies task groups
that are already complete (checkboxes done, no findings, tests pass)
and recommends skipping the coder session.
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb

from agent_fox.engine.dispatch import (
    PreflightVerdict,
    has_active_critical_findings,
    is_task_group_done,
    is_task_group_done_db,
    is_task_group_done_file,
    run_preflight,
)
from agent_fox.knowledge.migrations import run_migrations


def _make_conn() -> duckdb.DuckDBPyConnection:
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _insert_plan_node(
    conn: duckdb.DuckDBPyConnection,
    node_id: str,
    spec_name: str,
    group_number: int,
    status: str = "pending",
) -> None:
    conn.execute(
        """
        INSERT INTO plan_nodes (id, spec_name, group_number, title, body, status)
        VALUES (?, ?, ?, ?, '', ?)
        """,
        [node_id, spec_name, group_number, f"Task {group_number}", status],
    )


class TestIsTaskGroupDoneDb:
    def test_completed_node_returns_true(self) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "completed")
        assert is_task_group_done_db(conn, "spec", 1) is True

    def test_pending_node_returns_false(self) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "pending")
        assert is_task_group_done_db(conn, "spec", 1) is False

    def test_missing_node_returns_none(self) -> None:
        conn = _make_conn()
        assert is_task_group_done_db(conn, "spec", 1) is None

    def test_none_conn_returns_none(self) -> None:
        assert is_task_group_done_db(None, "spec", 1) is None


class TestIsTaskGroupDoneFile:
    def test_completed_group_returns_true(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        spec_dir = tmp_path / "my_spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="First task", optional=False, completed=True,
                subtasks=(SubtaskDef(id="1.1", title="detail", completed=True),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done_file(tmp_path, "my_spec", 1) is True

    def test_incomplete_group_returns_false(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        spec_dir = tmp_path / "my_spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="First task", optional=False, completed=False,
                subtasks=(SubtaskDef(id="1.1", title="detail", completed=False),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done_file(tmp_path, "my_spec", 1) is False

    def test_missing_tasks_file_returns_false(self, tmp_path: Path) -> None:
        assert is_task_group_done_file(tmp_path, "my_spec", 1) is False

    def test_wrong_group_number_returns_false(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        spec_dir = tmp_path / "my_spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="First task", optional=False, completed=True,
                subtasks=(SubtaskDef(id="1.1", title="detail", completed=True),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done_file(tmp_path, "my_spec", 99) is False


class TestIsTaskGroupDone:
    def test_db_takes_precedence_over_file(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "pending")
        spec_dir = tmp_path / "spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="Done task", optional=False, completed=True,
                subtasks=(SubtaskDef(id="1.1", title="t", completed=True),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done(conn, "spec", 1, tmp_path) is False

    def test_falls_back_to_file_when_db_missing(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        conn = _make_conn()
        spec_dir = tmp_path / "spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="Done task", optional=False, completed=True,
                subtasks=(SubtaskDef(id="1.1", title="t", completed=True),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done(conn, "spec", 1, tmp_path) is True

    def test_falls_back_to_file_when_conn_none(self, tmp_path: Path) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        spec_dir = tmp_path / "spec"
        spec_dir.mkdir()
        mock_groups = [
            TaskGroupDef(
                number=1, title="Done task", optional=False, completed=True,
                subtasks=(SubtaskDef(id="1.1", title="t", completed=True),),
                body="", archetype=None,
            ),
        ]
        with patch("agent_fox.spec.parser.parse_tasks", return_value=mock_groups):
            assert is_task_group_done(None, "spec", 1, tmp_path) is True


class TestHasActiveCriticalFindings:
    def test_no_findings_returns_false(self) -> None:
        conn = _make_conn()
        assert has_active_critical_findings(conn, "spec", 1) is False

    def test_critical_finding_returns_true(self) -> None:
        conn = _make_conn()
        conn.execute(
            """
            INSERT INTO review_findings
                (id, severity, description, spec_name, task_group, session_id)
            VALUES
                (gen_random_uuid(), 'critical', 'broken', 'spec', '1', 'sess-1')
            """
        )
        assert has_active_critical_findings(conn, "spec", 1) is True

    def test_major_finding_returns_true(self) -> None:
        conn = _make_conn()
        conn.execute(
            """
            INSERT INTO review_findings
                (id, severity, description, spec_name, task_group, session_id)
            VALUES
                (gen_random_uuid(), 'major', 'needs work', 'spec', '1', 'sess-1')
            """
        )
        assert has_active_critical_findings(conn, "spec", 1) is True

    def test_minor_finding_returns_false(self) -> None:
        conn = _make_conn()
        conn.execute(
            """
            INSERT INTO review_findings
                (id, severity, description, spec_name, task_group, session_id)
            VALUES
                (gen_random_uuid(), 'minor', 'nit', 'spec', '1', 'sess-1')
            """
        )
        assert has_active_critical_findings(conn, "spec", 1) is False

    def test_none_conn_returns_false(self) -> None:
        assert has_active_critical_findings(None, "spec", 1) is False


class TestRunPreflight:
    def test_incomplete_task_returns_launch(self, tmp_path: Path) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "pending")
        verdict = run_preflight("spec", 1, conn, tmp_path, tmp_path)
        assert verdict == PreflightVerdict.LAUNCH

    def test_done_with_findings_returns_launch(self, tmp_path: Path) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "completed")
        conn.execute(
            """
            INSERT INTO review_findings
                (id, severity, description, spec_name, task_group, session_id)
            VALUES
                (gen_random_uuid(), 'critical', 'broken', 'spec', '1', 'sess-1')
            """
        )
        verdict = run_preflight("spec", 1, conn, tmp_path, tmp_path)
        assert verdict == PreflightVerdict.LAUNCH

    @patch("agent_fox.engine.dispatch.do_tests_pass", return_value=False)
    def test_done_no_findings_tests_fail_returns_launch(
        self, _mock_tests: MagicMock, tmp_path: Path
    ) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "completed")
        verdict = run_preflight("spec", 1, conn, tmp_path, tmp_path)
        assert verdict == PreflightVerdict.LAUNCH

    @patch("agent_fox.engine.dispatch.do_tests_pass", return_value=True)
    def test_all_gates_pass_returns_skip(
        self, _mock_tests: MagicMock, tmp_path: Path
    ) -> None:
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "completed")
        verdict = run_preflight("spec", 1, conn, tmp_path, tmp_path)
        assert verdict == PreflightVerdict.SKIP

    @patch("agent_fox.engine.dispatch.do_tests_pass", return_value=True)
    def test_short_circuits_on_first_failure(
        self, mock_tests: MagicMock, tmp_path: Path
    ) -> None:
        """Tests are not run when task group is incomplete."""
        conn = _make_conn()
        _insert_plan_node(conn, "spec:1", "spec", 1, "pending")
        verdict = run_preflight("spec", 1, conn, tmp_path, tmp_path)
        assert verdict == PreflightVerdict.LAUNCH
        mock_tests.assert_not_called()
