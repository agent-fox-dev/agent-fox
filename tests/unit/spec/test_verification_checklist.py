"""Tests for the verification checklist builder.

Verifies that the checklist correctly audits task completion, maps
requirements to test functions, and renders a structured markdown
document for verifier context injection.

Updated for spec 137: all fixtures use v1.2 JSON format (tasks.json,
requirements.json) instead of v1 markdown files.
"""

from __future__ import annotations

import json
from pathlib import Path

import duckdb

from agent_fox.knowledge.migrations import run_migrations
from agent_fox.spec.verification_checklist import (
    RequirementMapping,
    SubtaskAuditEntry,
    VerificationChecklist,
    build_verification_checklist,
    render_checklist_markdown,
    scan_requirement_test_coverage,
)


def _make_conn() -> duckdb.DuckDBPyConnection:
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _make_requirement(
    req_id: str,
    title: str = "Core Feature",
    criteria: list[dict] | None = None,
    edge_cases: list[dict] | None = None,
) -> dict:
    """Build a valid afspec requirement dict."""
    return {
        "id": req_id,
        "title": title,
        "user_story": {"role": "user", "goal": "do things", "benefit": "value"},
        "acceptance_criteria": criteria or [],
        "edge_cases": edge_cases or [],
    }


def _make_criterion(criterion_id: str, text: str = "The system SHALL do X.") -> dict:
    """Build a valid afspec acceptance criterion dict."""
    return {
        "id": criterion_id,
        "ears_pattern": "ubiquitous",
        "criterion": text,
    }


def _write_spec(
    spec_dir: Path,
    *,
    task_groups: list[dict] | None = None,
    requirements: list[dict] | None = None,
) -> None:
    """Write v1.2 spec artifacts (prd.md, requirements.json, tasks.json).

    Provides minimal valid JSON structures for testing.
    """
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(
        "---\n"
        'spec_id: "test"\n'
        'spec_name: "test"\n'
        'title: "Test"\n'
        'status: "draft"\n'
        'created_at: "2024-01-01T00:00:00Z"\n'
        'updated_at: "2024-01-01T00:00:00Z"\n'
        'owner: "test"\n'
        'source: "test"\n'
        "schema_version: 1\n"
        "---\n# Test PRD\n",
        encoding="utf-8",
    )
    req_data = {
        "spec_id": "test",
        "spec_name": "test",
        "schema_version": 1,
        "introduction": "Test",
        "glossary": {},
        "requirements": requirements or [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    }
    (spec_dir / "requirements.json").write_text(
        json.dumps(req_data, indent=2), encoding="utf-8"
    )
    tasks_data = {
        "spec_id": "test",
        "spec_name": "test",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": task_groups or [],
        "traceability": [],
    }
    (spec_dir / "tasks.json").write_text(
        json.dumps(tasks_data, indent=2), encoding="utf-8"
    )
    test_spec_data = {
        "spec_id": "test",
        "spec_name": "test",
        "schema_version": 1,
        "test_cases": [],
        "property_tests": [],
        "edge_case_tests": [],
        "smoke_tests": [],
        "coverage": {
            "requirements_covered": [],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": [],
        },
    }
    (spec_dir / "test_spec.json").write_text(
        json.dumps(test_spec_data, indent=2), encoding="utf-8"
    )


class TestSubtaskAudit:
    def test_all_checked_returns_no_unchecked(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[{
                "id": 1, "kind": "standard", "title": "Write failing tests",
                "subtasks": [
                    {"id": "1.1", "title": "Write unit tests", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.2", "title": "Write integration tests", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.V", "title": "Verify task group 1", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                ],
                "verification": {"id": "", "checks": []},
            }],
        )
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        unchecked = [e for e in checklist.task_audit if not e.checked]
        assert unchecked == []

    def test_unchecked_subtasks_flagged(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[{
                "id": 1, "kind": "standard", "title": "Write failing tests",
                "subtasks": [
                    {"id": "1.1", "title": "Write unit tests", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.2", "title": "Write integration tests", "state": "pending",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.V", "title": "Verify task group 1", "state": "pending",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                ],
                "verification": {"id": "", "checks": []},
            }],
        )
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        unchecked = [e for e in checklist.task_audit if not e.checked]
        assert len(unchecked) == 2
        ids = {e.subtask_id for e in unchecked}
        assert "1.2" in ids
        assert "1.V" in ids

    def test_erratum_covers_unchecked_subtask(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[{
                "id": 1, "kind": "standard", "title": "Write failing tests",
                "subtasks": [
                    {"id": "1.1", "title": "Skipped subtask", "state": "pending",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                ],
                "verification": {"id": "", "checks": []},
            }],
        )
        conn = _make_conn()
        conn.execute(
            "INSERT INTO errata "
            "(id, spec_name, task_group, finding_summary, created_at) "
            "VALUES ('e1', '10_my_spec', '1', 'Documented deviation for 1.1', CURRENT_TIMESTAMP)",
        )
        checklist = build_verification_checklist(spec_dir, conn)
        assert checklist.has_errata is True

    def test_multiple_groups_audited(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[
                {
                    "id": 1, "kind": "standard", "title": "Write failing tests",
                    "subtasks": [
                        {"id": "1.1", "title": "Unit tests", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    ],
                    "verification": {"id": "", "checks": []},
                },
                {
                    "id": 2, "kind": "standard", "title": "Implement",
                    "subtasks": [
                        {"id": "2.1", "title": "Core logic", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                        {"id": "2.2", "title": "Edge cases", "state": "pending",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    ],
                    "verification": {"id": "", "checks": []},
                },
            ],
        )
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        groups = {e.group_number for e in checklist.task_audit}
        assert 1 in groups
        assert 2 in groups

    def test_skipped_subtasks_excluded(self, tmp_path: Path) -> None:
        """Subtasks marked as dropped are intentionally skipped."""
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[{
                "id": 1, "kind": "standard", "title": "Partially done",
                "subtasks": [
                    {"id": "1.1", "title": "Done", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.2", "title": "Skipped intentionally", "state": "dropped",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.3", "title": "Not applicable", "state": "dropped",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                ],
                "verification": {"id": "", "checks": []},
            }],
        )
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        unchecked = [e for e in checklist.task_audit if not e.checked and not e.skipped]
        assert unchecked == []


class TestRequirementTestCoverage:
    def test_requirement_found_in_test_docstring(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            requirements=[
                _make_requirement("req-1", criteria=[_make_criterion("10-REQ-1.1")]),
            ],
        )
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        (tests_dir / "test_core.py").write_text(
            '"""Tests for core feature.\n\nRequirements: 10-REQ-1.1\n"""\n\n'
            "def test_core_does_x():\n    pass\n",
            encoding="utf-8",
        )
        mappings = scan_requirement_test_coverage(spec_dir, tests_dir)
        mapped = {m.requirement_id: m for m in mappings}
        assert "10-REQ-1.1" in mapped
        assert mapped["10-REQ-1.1"].covered is True

    def test_requirement_found_in_function_name(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            requirements=[
                _make_requirement("req-1", criteria=[_make_criterion("10-REQ-1.1")]),
            ],
        )
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        (tests_dir / "test_feature.py").write_text(
            "def test_req_10_1_1_something():\n    pass\n",
            encoding="utf-8",
        )
        mappings = scan_requirement_test_coverage(spec_dir, tests_dir)
        mapped = {m.requirement_id: m for m in mappings}
        assert "10-REQ-1.1" in mapped
        assert mapped["10-REQ-1.1"].covered is True

    def test_unmapped_requirement_flagged(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            requirements=[
                _make_requirement(
                    "req-1",
                    criteria=[
                        _make_criterion("10-REQ-1.1"),
                        _make_criterion("10-REQ-1.2", "The system SHALL do Y."),
                    ],
                ),
            ],
        )
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        (tests_dir / "test_feature.py").write_text(
            "# Tests for 10-REQ-1.1\ndef test_x():\n    pass\n",
            encoding="utf-8",
        )
        mappings = scan_requirement_test_coverage(spec_dir, tests_dir)
        mapped = {m.requirement_id: m for m in mappings}
        assert mapped["10-REQ-1.1"].covered is True
        assert mapped["10-REQ-1.2"].covered is False

    def test_no_requirements_file(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        spec_dir.mkdir()
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        mappings = scan_requirement_test_coverage(spec_dir, tests_dir)
        assert mappings == []

    def test_no_tests_dir(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            requirements=[
                _make_requirement("req-1", criteria=[_make_criterion("10-REQ-1.1")]),
            ],
        )
        tests_dir = tmp_path / "nonexistent"
        mappings = scan_requirement_test_coverage(spec_dir, tests_dir)
        mapped = {m.requirement_id: m for m in mappings}
        assert mapped["10-REQ-1.1"].covered is False


class TestRenderChecklistMarkdown:
    def test_renders_task_audit_section(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[{
                "id": 1, "kind": "standard", "title": "Write tests",
                "subtasks": [
                    {"id": "1.1", "title": "Unit tests", "state": "done",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    {"id": "1.2", "title": "Integration tests", "state": "pending",
                     "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                ],
                "verification": {"id": "", "checks": []},
            }],
        )
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        md = render_checklist_markdown(checklist)
        assert "## Verification Checklist" in md
        assert "Task Completion Audit" in md
        assert "1.1" in md
        assert "1.2" in md

    def test_renders_requirement_coverage(self, tmp_path: Path) -> None:
        checklist = VerificationChecklist(
            spec_name="10_my_spec",
            task_audit=[],
            requirement_coverage=[
                RequirementMapping("10-REQ-1.1", True, ["test_core.py"]),
                RequirementMapping("10-REQ-1.2", False, []),
            ],
            has_errata=False,
        )
        md = render_checklist_markdown(checklist)
        assert "Requirement-to-Test Coverage" in md
        assert "10-REQ-1.1" in md
        assert "10-REQ-1.2" in md
        assert "UNCOVERED" in md

    def test_renders_errata_notice(self, tmp_path: Path) -> None:
        checklist = VerificationChecklist(
            spec_name="10_my_spec",
            task_audit=[
                SubtaskAuditEntry(1, "1.1", "Do stuff", False, False),
            ],
            requirement_coverage=[],
            has_errata=True,
        )
        md = render_checklist_markdown(checklist)
        assert "errata" in md.lower()

    def test_empty_checklist_renders_cleanly(self) -> None:
        checklist = VerificationChecklist(
            spec_name="10_my_spec",
            task_audit=[],
            requirement_coverage=[],
            has_errata=False,
        )
        md = render_checklist_markdown(checklist)
        assert "## Verification Checklist" in md


class TestBuildVerificationChecklist:
    def test_full_checklist_integration(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        _write_spec(
            spec_dir,
            task_groups=[
                {
                    "id": 1, "kind": "standard", "title": "Write failing tests",
                    "subtasks": [
                        {"id": "1.1", "title": "Unit tests", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                        {"id": "1.V", "title": "Verify", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    ],
                    "verification": {"id": "", "checks": []},
                },
                {
                    "id": 2, "kind": "standard", "title": "Implement",
                    "subtasks": [
                        {"id": "2.1", "title": "Core", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                        {"id": "2.V", "title": "Verify", "state": "done",
                         "details": [], "test_spec_refs": [], "requirement_refs": [], "optional": False},
                    ],
                    "verification": {"id": "", "checks": []},
                },
            ],
            requirements=[
                _make_requirement("req-1", criteria=[_make_criterion("10-REQ-1.1")]),
            ],
        )
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        (tests_dir / "test_core.py").write_text(
            "# 10-REQ-1.1\ndef test_x():\n    pass\n",
            encoding="utf-8",
        )
        conn = _make_conn()
        checklist = build_verification_checklist(
            spec_dir, conn, tests_dir=tests_dir
        )
        assert checklist.spec_name == "10_my_spec"
        assert len(checklist.task_audit) > 0
        assert len(checklist.requirement_coverage) == 1
        assert checklist.requirement_coverage[0].covered is True

    def test_missing_tasks_file_returns_empty_audit(self, tmp_path: Path) -> None:
        spec_dir = tmp_path / "10_my_spec"
        spec_dir.mkdir()
        conn = _make_conn()
        checklist = build_verification_checklist(spec_dir, conn)
        assert checklist.task_audit == []
