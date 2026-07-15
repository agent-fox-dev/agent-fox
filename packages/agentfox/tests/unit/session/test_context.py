"""Context assembly tests.

Test Spec: TS-03-4 (spec documents), TS-03-5 (memory facts),
           TS-03-E4 (missing spec file),
           TS-15-1, TS-15-2, TS-15-E1 (test_spec.md inclusion)
Requirements: 03-REQ-4.1 through 03-REQ-4.E1, 15-REQ-1.1, 15-REQ-1.2, 15-REQ-1.E1
"""

from __future__ import annotations

import logging
from pathlib import Path

import duckdb
import pytest
from agentfox.session.prompt import assemble_context


class TestContextAssemblySpecDocs:
    """TS-03-4: Context assembly includes spec documents."""

    def test_includes_requirements_content(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context includes requirements.md content."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        assert "REQ content here" in ctx

    def test_includes_architecture_content(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context includes architecture.md content."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        assert "Design content here" in ctx

    def test_includes_tasks_content(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context includes tasks.md content."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        assert "Task content here" in ctx

    def test_has_section_headers(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context has section headers separating documents."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        # Should have some kind of header/separator for each document
        # The exact format is implementation-defined, but each section
        # should be clearly delineated
        assert ctx.count("#") >= 1 or ctx.count("---") >= 1


class TestContextAssemblyMemoryFacts:
    """TS-03-5: Context assembly includes memory facts."""

    def test_includes_memory_facts(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Memory facts appear in the assembled context."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            memory_facts=["Fact 1", "Fact 2"],
            conn=knowledge_conn,
        )
        assert "Fact 1" in ctx
        assert "Fact 2" in ctx

    def test_memory_facts_in_labeled_section(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Memory facts appear in a clearly labeled section."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            memory_facts=["Important fact"],
            conn=knowledge_conn,
        )
        # The memory section should have some label
        lower_ctx = ctx.lower()
        assert "memory" in lower_ctx or "fact" in lower_ctx


class TestContextAssemblyMissingFile:
    """TS-03-E4: Context assembly with missing spec file."""

    def test_missing_file_does_not_raise(self, tmp_path: Path, knowledge_conn: duckdb.DuckDBPyConnection) -> None:
        """A missing/incomplete spec directory is handled gracefully."""
        spec_dir = tmp_path / "specs" / "partial"
        spec_dir.mkdir(parents=True)

        ctx = assemble_context(spec_dir, task_group=1, conn=knowledge_conn)
        assert isinstance(ctx, str)

    def test_returns_string_with_partial_files(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Context returns a string even when spec files are incomplete."""
        spec_dir = tmp_path / "specs" / "partial"
        spec_dir.mkdir(parents=True)

        ctx = assemble_context(spec_dir, task_group=1, conn=knowledge_conn)
        assert isinstance(ctx, str)


class TestContextIncludesTestSpec:
    """TS-15-1: Context assembly includes test_spec.md content.

    Requirement: 15-REQ-1.1
    """

    def test_context_includes_test_spec_content(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context includes test spec section."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        assert "## Test Specification" in ctx

    def test_context_has_test_specification_header(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context includes the ## Test Specification header."""
        ctx = assemble_context(tmp_spec_dir, task_group=2, conn=knowledge_conn)
        assert "## Test Specification" in ctx


class TestTestSpecOrdering:
    """TS-15-2: test_spec appears in context with requirements and tasks.

    Requirement: 15-REQ-1.2
    """

    def test_test_spec_in_context(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Context contains Requirements, Test Specification, and Tasks sections."""
        ctx = assemble_context(tmp_spec_dir, task_group=1, conn=knowledge_conn)
        assert "## Requirements" in ctx
        assert "## Test Specification" in ctx
        assert "## Tasks" in ctx


class TestScopedContextAssembly:
    """Issue #717: Context assembly scopes spec content to the active task group."""

    def test_scoped_context_includes_spec_overview(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Scoped context includes Spec Overview for orientation."""
        spec_dir = _make_scoped_spec_dir(tmp_path)
        ctx = assemble_context(spec_dir, task_group=2, conn=knowledge_conn)
        assert "Spec Overview" in ctx

    def test_scoped_context_shows_target_group_subtasks(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Scoped context includes subtask detail for the target group."""
        spec_dir = _make_scoped_spec_dir(tmp_path)
        ctx = assemble_context(spec_dir, task_group=2, conn=knowledge_conn)
        assert "2.1" in ctx

    def test_scoped_context_summarizes_other_groups(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Scoped context excludes subtask detail for non-target groups."""
        spec_dir = _make_scoped_spec_dir(tmp_path)
        ctx = assemble_context(spec_dir, task_group=2, conn=knowledge_conn)
        # Group 1's subtask 1.1 should not appear (only group title)
        assert "1.1" not in ctx

    def test_scoped_context_always_includes_test_commands(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Test Commands section is always included in scoped context."""
        spec_dir = _make_scoped_spec_dir(tmp_path)
        ctx = assemble_context(spec_dir, task_group=2, conn=knowledge_conn)
        assert "Test Commands" in ctx


def _make_scoped_spec_dir(tmp_path: Path) -> Path:
    """Create a spec directory with multiple groups and refs for scoping tests."""
    import json

    spec_dir = tmp_path / "specs" / "scoped_spec"
    spec_dir.mkdir(parents=True)

    (spec_dir / "prd.md").write_text(
        '---\nspec_id: "scoped"\nspec_name: "scoped"\ntitle: "Scoped"\n'
        'status: "draft"\ncreated_at: "2024-01-01T00:00:00Z"\n'
        'updated_at: "2024-01-01T00:00:00Z"\nowner: "test"\n'
        'source: "test"\nschema_version: 1\n---\n# Scoped\n'
    )
    (spec_dir / "requirements.json").write_text(
        json.dumps(
            {
                "spec_id": "scoped",
                "spec_name": "scoped",
                "schema_version": 1,
                "introduction": "Test requirements",
                "glossary": {},
                "requirements": [
                    {
                        "id": "REQ-A",
                        "title": "Requirement A",
                        "user_story": {"role": "user", "goal": "do A", "benefit": "get A"},
                        "acceptance_criteria": [
                            {
                                "id": "REQ-A.1",
                                "ears_pattern": "ubiquitous",
                                "system": "sys",
                                "action": "act A",
                            }
                        ],
                        "edge_cases": [],
                    },
                    {
                        "id": "REQ-B",
                        "title": "Requirement B",
                        "user_story": {"role": "user", "goal": "do B", "benefit": "get B"},
                        "acceptance_criteria": [
                            {
                                "id": "REQ-B.1",
                                "ears_pattern": "ubiquitous",
                                "system": "sys",
                                "action": "act B",
                            }
                        ],
                        "edge_cases": [],
                    },
                ],
                "correctness_properties": [],
                "execution_paths": [],
                "error_handling": [],
            }
        )
    )
    (spec_dir / "test_spec.json").write_text(
        json.dumps(
            {
                "spec_id": "scoped",
                "spec_name": "scoped",
                "schema_version": 1,
                "test_cases": [
                    {"id": "TS-A-1", "requirement_id": "REQ-A", "kind": "unit", "description": "Test for A"},
                    {"id": "TS-B-1", "requirement_id": "REQ-B", "kind": "unit", "description": "Test for B"},
                ],
                "property_tests": [],
                "edge_case_tests": [],
                "smoke_tests": [],
                "coverage": {"requirements_covered": [], "properties_covered": [], "paths_covered": [], "gaps": []},
            }
        )
    )
    (spec_dir / "tasks.json").write_text(
        json.dumps(
            {
                "spec_id": "scoped",
                "spec_name": "scoped",
                "schema_version": 1,
                "test_commands": {"spec_tests": "pytest", "all_tests": "make test", "linter": "ruff check"},
                "dependencies": [],
                "task_groups": [
                    {
                        "id": 1,
                        "kind": "standard",
                        "title": "Group one",
                        "subtasks": [
                            {
                                "id": "1.1",
                                "title": "Subtask one-one",
                                "state": "done",
                                "details": [],
                                "test_spec_refs": ["TS-A-1"],
                                "requirement_refs": ["REQ-A.1"],
                                "optional": False,
                            }
                        ],
                        "verification": {"id": "", "checks": []},
                    },
                    {
                        "id": 2,
                        "kind": "standard",
                        "title": "Group two",
                        "subtasks": [
                            {
                                "id": "2.1",
                                "title": "Subtask two-one",
                                "state": "pending",
                                "details": [],
                                "test_spec_refs": ["TS-B-1"],
                                "requirement_refs": ["REQ-B.1"],
                                "optional": False,
                            }
                        ],
                        "verification": {"id": "", "checks": []},
                    },
                ],
                "traceability": [],
            }
        )
    )
    return spec_dir


class TestMissingTestSpecFile:
    """TS-15-E1: Missing spec files handled gracefully.

    Requirement: 15-REQ-1.E1
    """

    def test_missing_spec_does_not_raise(self, tmp_path: Path, knowledge_conn: duckdb.DuckDBPyConnection) -> None:
        """Context assembly succeeds when spec directory is incomplete."""
        spec_dir = tmp_path / "specs" / "no_test_spec"
        spec_dir.mkdir(parents=True)

        ctx = assemble_context(spec_dir, task_group=1, conn=knowledge_conn)
        assert isinstance(ctx, str)

    def test_missing_spec_logs_warning(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """A warning is logged when spec files are missing."""
        spec_dir = tmp_path / "specs" / "no_test_spec"
        spec_dir.mkdir(parents=True)

        with caplog.at_level(logging.WARNING):
            assemble_context(spec_dir, task_group=1, conn=knowledge_conn)

        assert any("Failed to load" in record.message for record in caplog.records)
