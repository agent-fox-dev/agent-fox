"""Tests for verification checklist injection into verifier context.

Verifies that assemble_context() includes the verification checklist
section when archetype is 'verifier' and omits it for other archetypes.
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import patch

import duckdb
from agentfox.knowledge.migrations import run_migrations
from agentfox.session.context import assemble_context


def _make_conn() -> duckdb.DuckDBPyConnection:
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _setup_spec(tmp_path: Path) -> Path:
    spec_dir = tmp_path / "10_my_spec"
    spec_dir.mkdir()
    (spec_dir / "prd.md").write_text(
        '---\nspec_id: "10"\nspec_name: "my_spec"\ntitle: "My Spec"\nstatus: "draft"\n'
        'created_at: "2024-01-01T00:00:00Z"\nupdated_at: "2024-01-01T00:00:00Z"\n'
        'owner: "t"\nsource: "t"\nschema_version: 1\n---\n# My Spec\n',
        encoding="utf-8",
    )
    (spec_dir / "requirements.json").write_text(
        json.dumps({
            "spec_id": "10", "spec_name": "my_spec", "schema_version": 1,
            "introduction": "REQ",
            "glossary": {},
            "requirements": [{
                "id": "REQ-1",
                "title": "First requirement",
                "user_story": {"role": "user", "action": "do X", "benefit": "value"},
                "acceptance_criteria": [{
                    "id": "10-REQ-1.1",
                    "ears_pattern": "ubiquitous",
                    "system": "the system",
                    "action": "SHALL do X",
                }],
                "edge_cases": [],
            }],
            "correctness_properties": [],
            "execution_paths": [],
            "error_handling": [],
        }),
        encoding="utf-8",
    )
    (spec_dir / "test_spec.json").write_text(
        json.dumps({
            "spec_id": "10", "spec_name": "my_spec", "schema_version": 1,
            "test_cases": [], "property_tests": [], "edge_case_tests": [],
            "smoke_tests": [],
            "coverage": {"requirements_covered": [], "properties_covered": [],
                         "paths_covered": [], "gaps": []},
        }),
        encoding="utf-8",
    )
    (spec_dir / "tasks.json").write_text(
        json.dumps({
            "spec_id": "10", "spec_name": "my_spec", "schema_version": 1,
            "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
            "dependencies": [],
            "task_groups": [{
                "id": 1, "kind": "tests", "title": "Write tests",
                "subtasks": [
                    {"id": "1.1", "title": "Unit tests", "state": "done"},
                    {"id": "1.2", "title": "Integration tests", "state": "pending"},
                ],
            }],
            "traceability": [],
        }),
        encoding="utf-8",
    )
    return spec_dir


class TestVerifierChecklistInjection:
    def test_verifier_context_includes_checklist(self, tmp_path: Path) -> None:
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        context = assemble_context(
            spec_dir,
            task_group=1,
            conn=conn,
            project_root=tmp_path,
            archetype="verifier",
        )
        assert "Verification Checklist" in context
        assert "Task Completion Audit" in context
        assert "1.1" in context
        assert "1.2" in context

    def test_coder_context_excludes_checklist(self, tmp_path: Path) -> None:
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        context = assemble_context(
            spec_dir,
            task_group=1,
            conn=conn,
            project_root=tmp_path,
            archetype="coder",
        )
        assert "Verification Checklist" not in context

    def test_no_archetype_excludes_checklist(self, tmp_path: Path) -> None:
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        context = assemble_context(
            spec_dir,
            task_group=1,
            conn=conn,
            project_root=tmp_path,
        )
        assert "Verification Checklist" not in context

    def test_checklist_includes_unchecked_warning(self, tmp_path: Path) -> None:
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        context = assemble_context(
            spec_dir,
            task_group=1,
            conn=conn,
            project_root=tmp_path,
            archetype="verifier",
        )
        assert "UNCHECKED" in context

    def test_checklist_includes_requirement_coverage(self, tmp_path: Path) -> None:
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        context = assemble_context(
            spec_dir,
            task_group=1,
            conn=conn,
            project_root=tmp_path,
            archetype="verifier",
        )
        assert "Requirement-to-Test Coverage" in context
        assert "10-REQ-1.1" in context

    def test_checklist_failure_does_not_crash(self, tmp_path: Path) -> None:
        """If checklist building fails, context assembly continues."""
        spec_dir = _setup_spec(tmp_path)
        conn = _make_conn()
        with patch(
            "agentfox.spec.verification_checklist.build_verification_checklist",
            side_effect=RuntimeError("boom"),
        ):
            context = assemble_context(
                spec_dir,
                task_group=1,
                conn=conn,
                project_root=tmp_path,
                archetype="verifier",
            )
        assert "Verification Checklist" not in context
        assert "Requirements" in context
