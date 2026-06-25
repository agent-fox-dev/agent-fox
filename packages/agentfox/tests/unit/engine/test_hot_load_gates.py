"""Hot-load gate pipeline tests: git-tracked, completeness, lint, tasks-complete gates.

Test Spec: TS-51-12 through TS-51-22, TS-444-1 through TS-444-5
Requirements: 51-REQ-4.1, 51-REQ-4.2, 51-REQ-4.E1,
              51-REQ-5.1, 51-REQ-5.2, 51-REQ-5.E1,
              51-REQ-6.1, 51-REQ-6.2, 51-REQ-6.3, 51-REQ-6.E1,
              51-REQ-7.1, 51-REQ-7.2, 51-REQ-7.3,
              444-AC-1, 444-AC-2, 444-AC-3, 444-AC-4
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox.engine.hot_load import (
    _are_all_plan_nodes_done,
    are_all_tasks_done,
    discover_new_specs_gated,
    is_spec_complete,
    is_spec_tracked_on_branch,
    lint_spec_gate,
)

REQUIRED_FILES = ["prd.md", "requirements.json", "test_spec.json", "tasks.json"]


def _create_spec_files(
    spec_path: Path,
    files: list[str] | None = None,
    empty: list[str] | None = None,
) -> None:
    """Helper to create spec files. Files in `empty` are created with 0 bytes."""
    spec_path.mkdir(parents=True, exist_ok=True)
    if files is None:
        files = REQUIRED_FILES
    empty = empty or []
    for f in files:
        fp = spec_path / f
        if f in empty:
            fp.write_text("")
        else:
            fp.write_text(f"# {f}\nContent for {f}\n")


# ---------------------------------------------------------------------------
# TS-51-12: Git-tracked gate accepts tracked spec
# ---------------------------------------------------------------------------


class TestGitTrackedGateAccepts:
    """TS-51-12: Git-tracked gate accepts tracked spec.

    Requirements: 51-REQ-4.1
    """

    @pytest.mark.asyncio
    async def test_tracked_spec_returns_true(self, tmp_path: Path) -> None:
        """Spec tracked on develop returns True."""

        async def mock_run_git(
            args: list[str], cwd: Path, check: bool = True, **kwargs: object
        ) -> tuple[int, str, str]:
            return (0, "100644 blob abc123\tprd.md\n", "")

        with patch(
            "agentfox.engine.hot_load.run_git",
            side_effect=mock_run_git,
        ):
            result = await is_spec_tracked_on_branch(tmp_path, "42_feature", "main")

        assert result is True


# ---------------------------------------------------------------------------
# TS-51-13: Git-tracked gate rejects untracked spec
# ---------------------------------------------------------------------------


class TestGitTrackedGateRejects:
    """TS-51-13: Git-tracked gate rejects untracked spec.

    Requirements: 51-REQ-4.2
    """

    @pytest.mark.asyncio
    async def test_untracked_spec_returns_false(self, tmp_path: Path) -> None:
        """Spec not tracked on develop returns False."""

        async def mock_run_git(
            args: list[str], cwd: Path, check: bool = True, **kwargs: object
        ) -> tuple[int, str, str]:
            return (0, "", "")

        with patch(
            "agentfox.engine.hot_load.run_git",
            side_effect=mock_run_git,
        ):
            result = await is_spec_tracked_on_branch(tmp_path, "42_feature", "main")

        assert result is False


# ---------------------------------------------------------------------------
# TS-51-14: Git-tracked gate fallback on failure
# ---------------------------------------------------------------------------


class TestGitTrackedGateFallback:
    """TS-51-14: Git-tracked gate fallback on failure.

    Requirements: 51-REQ-4.E1
    """

    @pytest.mark.asyncio
    async def test_fallback_to_permissive_on_failure(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """git ls-tree failure returns True (permissive) and logs warning."""

        async def mock_run_git(
            args: list[str], cwd: Path, check: bool = True, **kwargs: object
        ) -> tuple[int, str, str]:
            raise Exception("git command failed")

        with (
            patch(
                "agentfox.engine.hot_load.run_git",
                side_effect=mock_run_git,
            ),
            caplog.at_level(logging.WARNING, logger="agentfox.engine.hot_load"),
        ):
            result = await is_spec_tracked_on_branch(tmp_path, "42_feature", "main")

        assert result is True
        assert len(caplog.records) > 0


# ---------------------------------------------------------------------------
# TS-51-15: Completeness gate with all files
# ---------------------------------------------------------------------------


class TestCompletenessGateAllFiles:
    """TS-51-15: Completeness gate with all 5 non-empty files.

    Requirements: 51-REQ-5.1
    """

    def test_all_files_present_and_nonempty(self, tmp_path: Path) -> None:
        """Returns (True, []) when all 5 files exist and are non-empty."""
        spec_path = tmp_path / "42_feature"
        _create_spec_files(spec_path)

        passed, missing = is_spec_complete(spec_path)
        assert passed is True
        assert missing == []


# ---------------------------------------------------------------------------
# TS-51-16: Completeness gate with missing file
# ---------------------------------------------------------------------------


class TestCompletenessGateMissingFile:
    """TS-51-16: Completeness gate with missing file.

    Requirements: 51-REQ-5.2
    """

    def test_missing_requirements_json(self, tmp_path: Path) -> None:
        """Returns (False, ['requirements.json']) when requirements.json is missing."""
        spec_path = tmp_path / "42_feature"
        files = [f for f in REQUIRED_FILES if f != "requirements.json"]
        _create_spec_files(spec_path, files=files)

        passed, missing = is_spec_complete(spec_path)
        assert passed is False
        assert "requirements.json" in missing


# ---------------------------------------------------------------------------
# TS-51-17: Completeness gate with empty file
# ---------------------------------------------------------------------------


class TestCompletenessGateEmptyFile:
    """TS-51-17: Completeness gate with empty file.

    Requirements: 51-REQ-5.E1
    """

    def test_empty_requirements_json(self, tmp_path: Path) -> None:
        """Empty requirements.json is treated as incomplete."""
        spec_path = tmp_path / "42_feature"
        _create_spec_files(spec_path, empty=["requirements.json"])

        passed, missing = is_spec_complete(spec_path)
        assert passed is False
        assert "requirements.json" in missing


# ---------------------------------------------------------------------------
# TS-51-E2: Empty spec file treated as incomplete (tasks.md)
# ---------------------------------------------------------------------------


class TestCompletenessGateEmptyTasksMd:
    """TS-51-E2: Zero-byte tasks.md causes completeness gate to fail.

    Requirements: 51-REQ-5.E1
    """

    def test_empty_tasks_json(self, tmp_path: Path) -> None:
        """Empty tasks.json is treated as incomplete."""
        spec_path = tmp_path / "42_feature"
        _create_spec_files(spec_path, empty=["tasks.json"])

        passed, missing = is_spec_complete(spec_path)
        assert passed is False
        assert "tasks.json" in missing


# ---------------------------------------------------------------------------
# TS-51-18: Lint gate accepts clean spec
# ---------------------------------------------------------------------------


class TestLintGateAcceptsClean:
    """TS-51-18: Lint gate accepts clean spec (warnings only, no errors).

    Requirements: 51-REQ-6.1, 51-REQ-6.3
    """

    def test_no_errors_passes(self, tmp_path: Path) -> None:
        """Spec with no validation errors passes."""
        spec_path = tmp_path / "42_feature"
        spec_path.mkdir(parents=True)

        mock_spec = type("MockSpec", (), {})()

        with (
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.validate", return_value=[]),
        ):
            passed, errors = lint_spec_gate("42_feature", spec_path)

        assert passed is True
        assert errors == []


# ---------------------------------------------------------------------------
# TS-51-19: Lint gate rejects spec with errors
# ---------------------------------------------------------------------------


class TestLintGateRejectsErrors:
    """TS-51-19: Lint gate rejects spec with error findings.

    Requirements: 51-REQ-6.2
    """

    def test_error_finding_fails_gate(self, tmp_path: Path) -> None:
        """Spec with error-severity finding is rejected."""
        mock_error = type(
            "MockValidationError",
            (),
            {
                "rule": "missing-file",
                "message": "Expected file 'requirements.json' is missing",
                "file": "requirements.json",
            },
        )()

        mock_spec = type("MockSpec", (), {})()
        spec_path = tmp_path / "42_feature"
        spec_path.mkdir(parents=True)

        with (
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.validate", return_value=[mock_error]),
        ):
            passed, errors = lint_spec_gate("42_feature", spec_path)

        assert passed is False
        assert len(errors) == 1
        assert "missing-file" in errors[0]


# ---------------------------------------------------------------------------
# TS-51-20 / TS-51-E3: Lint gate handles validator exception
# ---------------------------------------------------------------------------


class TestLintGateValidatorException:
    """TS-51-20: Lint gate handles validator crash gracefully.

    Requirements: 51-REQ-6.E1
    """

    def test_validator_exception_returns_false(self, tmp_path: Path) -> None:
        """Validator crash returns (False, [error desc]), no exception propagated."""
        spec_path = tmp_path / "42_feature"
        spec_path.mkdir(parents=True)

        with patch(
            "afspec.load_spec",
            side_effect=RuntimeError("boom"),
        ):
            passed, errors = lint_spec_gate("42_feature", spec_path)

        assert passed is False
        assert len(errors) == 1
        assert "boom" in errors[0]


# ---------------------------------------------------------------------------
# TS-51-21: Full gate pipeline filters correctly
# ---------------------------------------------------------------------------


class TestFullGatePipeline:
    """TS-51-21: Full gate pipeline filters specs through all gates.

    Requirements: 51-REQ-7.1
    """

    @pytest.mark.asyncio
    async def test_pipeline_filters_correctly(self, tmp_path: Path) -> None:
        """Only spec passing all gates is returned.

        spec_a: tracked, complete, lint-clean -> accepted
        spec_b: tracked, complete, has lint errors -> rejected at lint
        spec_c: not tracked on develop -> rejected at git gate
        """
        specs_dir = tmp_path / ".specs"

        # Create spec_a (valid)
        _create_spec_files(specs_dir / "42_spec_a")
        # Create spec_b (lint errors)
        _create_spec_files(specs_dir / "43_spec_b")
        # Create spec_c (untracked)
        _create_spec_files(specs_dir / "44_spec_c")

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return spec_name != "44_spec_c"

        def mock_is_complete(spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            if spec_name == "43_spec_b":
                return (False, ["missing-file: design.md"])
            return (True, [])

        from agentfox.spec.discovery import SpecInfo

        mock_new_specs = [
            SpecInfo(
                name="42_spec_a",
                prefix=42,
                path=specs_dir / "42_spec_a",
                has_tasks=True,
                has_prd=True,
            ),
            SpecInfo(
                name="43_spec_b",
                prefix=43,
                path=specs_dir / "43_spec_b",
                has_tasks=True,
                has_prd=True,
            ),
            SpecInfo(
                name="44_spec_c",
                prefix=44,
                path=specs_dir / "44_spec_c",
                has_tasks=True,
                has_prd=True,
            ),
        ]

        with (
            patch(
                "agentfox.engine.hot_load.discover_new_specs",
                return_value=mock_new_specs,
            ),
            patch(
                "agentfox.engine.hot_load.is_spec_tracked_on_branch",
                side_effect=mock_is_tracked,
            ),
            patch(
                "agentfox.engine.hot_load.is_spec_complete",
                side_effect=mock_is_complete,
            ),
            patch(
                "agentfox.engine.hot_load.lint_spec_gate",
                side_effect=mock_lint_gate,
            ),
        ):
            result = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main")

        assert len(result) == 1
        assert result[0].name == "42_spec_a"


# ---------------------------------------------------------------------------
# TS-51-22: Previously skipped spec accepted after fix
# ---------------------------------------------------------------------------


class TestSkippedSpecReEvaluation:
    """TS-51-22: Previously skipped spec accepted after fix.

    Requirements: 51-REQ-7.2, 51-REQ-7.3
    """

    @pytest.mark.asyncio
    async def test_spec_accepted_after_fix(self, tmp_path: Path) -> None:
        """Spec skipped at barrier N passes at barrier N+1 after being fixed."""
        specs_dir = tmp_path / ".specs"
        spec_path = specs_dir / "42_feature"
        # First: create incomplete spec (missing test_spec.json)
        files_without_test_spec = [f for f in REQUIRED_FILES if f != "test_spec.json"]
        _create_spec_files(spec_path, files=files_without_test_spec)

        from agentfox.spec.discovery import SpecInfo

        mock_spec = SpecInfo(
            name="42_feature",
            prefix=42,
            path=spec_path,
            has_tasks=True,
            has_prd=True,
        )

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return True

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        with (
            patch(
                "agentfox.engine.hot_load.discover_new_specs",
                return_value=[mock_spec],
            ),
            patch(
                "agentfox.engine.hot_load.is_spec_tracked_on_branch",
                side_effect=mock_is_tracked,
            ),
            patch(
                "agentfox.engine.hot_load.lint_spec_gate",
                side_effect=mock_lint_gate,
            ),
        ):
            # Barrier N: spec is incomplete
            result_1 = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main")
            assert result_1 == []

            # Fix spec: add test_spec.json
            (spec_path / "test_spec.json").write_text('{"test_cases": []}\n')

            # Barrier N+1: spec now passes
            result_2 = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main")
            assert len(result_2) == 1
            assert result_2[0].name == "42_feature"


# ---------------------------------------------------------------------------
# TS-444-1: are_all_tasks_done
# ---------------------------------------------------------------------------


class TestAreAllTasksDone:
    """TS-444-1: task group checkbox gate for completed specs.

    Requirements: 444-AC-1
    """

    def test_all_groups_completed(self, tmp_path: Path) -> None:
        """Returns True when all task groups are marked completed."""
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        spec_path = tmp_path / "42_feature"
        spec_path.mkdir()

        groups = [
            TaskGroupDef(
                number=1,
                title="First",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="1.1", title="s1", completed=True),),
                body="",
                archetype=None,
            ),
            TaskGroupDef(
                number=2,
                title="Second",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="2.1", title="s2", completed=True),),
                body="",
                archetype=None,
            ),
        ]

        with patch("agentfox.engine.hot_load.parse_tasks", return_value=groups):
            assert are_all_tasks_done(spec_path) is True

    def test_some_groups_incomplete(self, tmp_path: Path) -> None:
        """Returns False when some groups are not completed."""
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        spec_path = tmp_path / "42_feature"
        spec_path.mkdir()

        groups = [
            TaskGroupDef(
                number=1,
                title="First",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="1.1", title="s1", completed=True),),
                body="",
                archetype=None,
            ),
            TaskGroupDef(
                number=2,
                title="Second",
                optional=False,
                completed=False,
                subtasks=(SubtaskDef(id="2.1", title="s2", completed=False),),
                body="",
                archetype=None,
            ),
        ]

        with patch("agentfox.engine.hot_load.parse_tasks", return_value=groups):
            assert are_all_tasks_done(spec_path) is False

    def test_no_groups_found(self, tmp_path: Path) -> None:
        """Returns False when parser returns empty list."""
        spec_path = tmp_path / "42_feature"
        spec_path.mkdir()

        with patch("agentfox.engine.hot_load.parse_tasks", return_value=[]):
            assert are_all_tasks_done(spec_path) is False

    def test_spec_dir_missing(self, tmp_path: Path) -> None:
        """Returns False when spec directory does not exist."""
        spec_path = tmp_path / "42_feature"
        # directory NOT created

        assert are_all_tasks_done(spec_path) is False

    def test_parse_error(self, tmp_path: Path) -> None:
        """Returns False when parse_tasks raises an exception."""
        spec_path = tmp_path / "42_feature"
        spec_path.mkdir()

        with patch(
            "agentfox.engine.hot_load.parse_tasks",
            side_effect=RuntimeError("parse error"),
        ):
            assert are_all_tasks_done(spec_path) is False

    def test_new_unchecked_tasks_added(self, tmp_path: Path) -> None:
        """Returns False when previously complete spec has new unchecked tasks.

        Acceptance criterion: 444-AC-4
        """
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        spec_path = tmp_path / "42_feature"
        spec_path.mkdir()

        groups = [
            TaskGroupDef(
                number=1,
                title="First",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="1.1", title="s1", completed=True),),
                body="",
                archetype=None,
            ),
            TaskGroupDef(
                number=2,
                title="Second",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="2.1", title="s2", completed=True),),
                body="",
                archetype=None,
            ),
            TaskGroupDef(
                number=3,
                title="New task",
                optional=False,
                completed=False,
                subtasks=(SubtaskDef(id="3.1", title="s3", completed=False),),
                body="",
                archetype=None,
            ),
        ]

        with patch("agentfox.engine.hot_load.parse_tasks", return_value=groups):
            assert are_all_tasks_done(spec_path) is False


# ---------------------------------------------------------------------------
# TS-444-2: _are_all_plan_nodes_done
# ---------------------------------------------------------------------------


class TestAreAllPlanNodesDone:
    """TS-444-2: plan node state gate for completed specs (queries plan_nodes DB table).

    Requirements: 444-AC-2
    """

    def test_all_nodes_completed(self) -> None:
        """Returns True when all nodes for spec have 'completed' status."""
        import duckdb

        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:0', '42_feature', 'completed')")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:1', '42_feature', 'completed')")

        assert _are_all_plan_nodes_done("42_feature", conn) is True
        conn.close()

    def test_some_nodes_not_completed(self) -> None:
        """Returns False when some nodes are pending."""
        import duckdb

        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:0', '42_feature', 'completed')")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:1', '42_feature', 'pending')")

        assert _are_all_plan_nodes_done("42_feature", conn) is False
        conn.close()

    def test_no_nodes_for_spec(self) -> None:
        """Returns False when plan has no nodes for this spec."""
        import duckdb

        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('99_other:0', '99_other', 'completed')")

        assert _are_all_plan_nodes_done("42_feature", conn) is False
        conn.close()

    def test_conn_is_none(self) -> None:
        """Returns False when no DB connection is available."""
        assert _are_all_plan_nodes_done("42_feature", None) is False

    def test_missing_table(self) -> None:
        """Returns False when plan_nodes table does not exist."""
        import duckdb

        conn = duckdb.connect(":memory:")
        assert _are_all_plan_nodes_done("42_feature", conn) is False
        conn.close()


# ---------------------------------------------------------------------------
# TS-444-3: Gate 4 integration in discover_new_specs_gated pipeline
# ---------------------------------------------------------------------------


class TestTasksCompleteGatePipeline:
    """TS-444-3: Tasks-complete gate filters fully implemented specs.

    Requirements: 444-AC-1, 444-AC-2, 444-AC-3, 444-AC-4
    """

    @pytest.mark.asyncio
    async def test_both_signals_done_skips_spec(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """Spec skipped when all tasks done AND plan nodes all completed in DB."""
        import duckdb
        from agentfox.spec.discovery import SpecInfo

        specs_dir = tmp_path / ".specs"
        spec_path = specs_dir / "42_feature"
        _create_spec_files(spec_path)

        # Create in-memory DuckDB with completed plan nodes
        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:1', '42_feature', 'completed')")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:2', '42_feature', 'completed')")

        mock_spec = SpecInfo(
            name="42_feature",
            prefix=42,
            path=spec_path,
            has_tasks=True,
            has_prd=True,
        )

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return True

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        with (
            patch("agentfox.engine.hot_load.discover_new_specs", return_value=[mock_spec]),
            patch("agentfox.engine.hot_load.is_spec_tracked_on_branch", side_effect=mock_is_tracked),
            patch("agentfox.engine.hot_load.is_spec_complete", return_value=(True, [])),
            patch("agentfox.engine.hot_load.lint_spec_gate", side_effect=mock_lint_gate),
            patch("agentfox.engine.hot_load.are_all_tasks_done", return_value=True),
            caplog.at_level(logging.INFO, logger="agentfox.engine.hot_load"),
        ):
            result = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main", db_conn=conn)

        assert result == []
        assert any("fully implemented" in r.message for r in caplog.records)
        conn.close()

    @pytest.mark.asyncio
    async def test_tasks_done_but_nodes_not_done(self, tmp_path: Path) -> None:
        """Spec NOT skipped when all tasks done but plan nodes are pending."""
        import duckdb
        from agentfox.spec.discovery import SpecInfo

        specs_dir = tmp_path / ".specs"
        spec_path = specs_dir / "42_feature"
        _create_spec_files(spec_path)

        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:1', '42_feature', 'pending')")

        mock_spec = SpecInfo(
            name="42_feature",
            prefix=42,
            path=spec_path,
            has_tasks=True,
            has_prd=True,
        )

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return True

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        with (
            patch("agentfox.engine.hot_load.discover_new_specs", return_value=[mock_spec]),
            patch("agentfox.engine.hot_load.is_spec_tracked_on_branch", side_effect=mock_is_tracked),
            patch("agentfox.engine.hot_load.is_spec_complete", return_value=(True, [])),
            patch("agentfox.engine.hot_load.lint_spec_gate", side_effect=mock_lint_gate),
            patch("agentfox.engine.hot_load.are_all_tasks_done", return_value=True),
        ):
            result = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main", db_conn=conn)

        assert len(result) == 1
        assert result[0].name == "42_feature"
        conn.close()

    @pytest.mark.asyncio
    async def test_nodes_done_but_tasks_not_done(self, tmp_path: Path) -> None:
        """Spec NOT skipped when plan nodes completed but tasks not all done."""
        import duckdb
        from agentfox.spec.discovery import SpecInfo

        specs_dir = tmp_path / ".specs"
        spec_path = specs_dir / "42_feature"
        _create_spec_files(spec_path)

        conn = duckdb.connect(":memory:")
        conn.execute("CREATE TABLE plan_nodes (id VARCHAR PRIMARY KEY, spec_name VARCHAR, status VARCHAR)")
        conn.execute("INSERT INTO plan_nodes VALUES ('42_feature:1', '42_feature', 'completed')")

        mock_spec = SpecInfo(
            name="42_feature",
            prefix=42,
            path=spec_path,
            has_tasks=True,
            has_prd=True,
        )

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return True

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        with (
            patch("agentfox.engine.hot_load.discover_new_specs", return_value=[mock_spec]),
            patch("agentfox.engine.hot_load.is_spec_tracked_on_branch", side_effect=mock_is_tracked),
            patch("agentfox.engine.hot_load.is_spec_complete", return_value=(True, [])),
            patch("agentfox.engine.hot_load.lint_spec_gate", side_effect=mock_lint_gate),
            patch("agentfox.engine.hot_load.are_all_tasks_done", return_value=False),
        ):
            result = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main", db_conn=conn)

        assert len(result) == 1
        conn.close()

    @pytest.mark.asyncio
    async def test_no_db_conn_does_not_skip(self, tmp_path: Path) -> None:
        """Spec NOT skipped when db_conn is None (even if tasks all done)."""
        from agentfox.spec.discovery import SpecInfo

        specs_dir = tmp_path / ".specs"
        spec_path = specs_dir / "42_feature"
        _create_spec_files(spec_path)

        mock_spec = SpecInfo(
            name="42_feature",
            prefix=42,
            path=spec_path,
            has_tasks=True,
            has_prd=True,
        )

        async def mock_is_tracked(repo_root: Path, spec_name: str, branch: str = "main", **kwargs: object) -> bool:
            return True

        def mock_lint_gate(spec_name: str, spec_path: Path) -> tuple[bool, list[str]]:
            return (True, [])

        with (
            patch("agentfox.engine.hot_load.discover_new_specs", return_value=[mock_spec]),
            patch("agentfox.engine.hot_load.is_spec_tracked_on_branch", side_effect=mock_is_tracked),
            patch("agentfox.engine.hot_load.is_spec_complete", return_value=(True, [])),
            patch("agentfox.engine.hot_load.lint_spec_gate", side_effect=mock_lint_gate),
            patch("agentfox.engine.hot_load.are_all_tasks_done", return_value=True),
        ):
            # No db_conn argument — backward compatible
            result = await discover_new_specs_gated(specs_dir, known_specs=set(), repo_root=tmp_path, integration_branch="main")

        assert len(result) == 1
