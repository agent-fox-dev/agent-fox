"""Spec 134: v1.2 Context Assembly and Rendering tests.

Test Spec: TS-134-1 through TS-134-9, TS-134-E1 through TS-134-E4,
           TS-134-P1, TS-134-P2, TS-134-SMOKE-1, TS-134-SMOKE-2
Requirements: 134-REQ-1.1, 134-REQ-1.2, 134-REQ-1.E1,
              134-REQ-2.1, 134-REQ-2.2, 134-REQ-2.3, 134-REQ-2.E1,
              134-REQ-3.1, 134-REQ-3.2, 134-REQ-3.3, 134-REQ-3.E1,
              134-REQ-4.1, 134-REQ-4.2, 134-REQ-4.E1
"""

from __future__ import annotations

import json
import logging
from pathlib import Path
from unittest.mock import patch

import duckdb
import pytest

from agent_fox.graph.spec_helpers import count_ts_entries, spec_has_existing_code
from agent_fox.session.context import assemble_context
from agent_fox.spec.verification_checklist import (
    RequirementMapping,
    SubtaskAuditEntry,
    _audit_task_checkboxes,
    build_verification_checklist,
    scan_requirement_test_coverage,
)

# ---------------------------------------------------------------------------
# Valid v1.2 fixture content
# ---------------------------------------------------------------------------

PRD_MD_VALID = """\
---
spec_id: "test-134"
spec_name: "test_fixture"
title: "Test Fixture Spec"
status: "draft"
created_at: "2024-01-01T00:00:00Z"
updated_at: "2024-01-01T00:00:00Z"
owner: "test"
source: "test"
schema_version: 1
---
# Test PRD

Test PRD content.
"""

REQUIREMENTS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-134",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "introduction": "Test requirements introduction",
        "glossary": {"term1": "definition1"},
        "requirements": [
            {
                "id": "REQ-1",
                "title": "First requirement",
                "user_story": {
                    "role": "user",
                    "action": "do thing",
                    "benefit": "benefit",
                },
                "acceptance_criteria": [
                    {
                        "id": "134-REQ-1.1",
                        "ears_pattern": "event_driven",
                        "system": "the system",
                        "action": "SHALL do something",
                        "event": "a request is made",
                    },
                ],
                "edge_cases": [
                    {
                        "id": "134-REQ-1.E1",
                        "ears_pattern": "unwanted",
                        "system": "the system",
                        "action": "SHALL log a warning",
                        "unwanted_condition": "loading fails",
                    },
                ],
            },
            {
                "id": "REQ-2",
                "title": "Second requirement",
                "user_story": {
                    "role": "dev",
                    "action": "test",
                    "benefit": "quality",
                },
                "acceptance_criteria": [
                    {
                        "id": "134-REQ-2.1",
                        "ears_pattern": "event_driven",
                        "system": "the system",
                        "action": "SHALL render",
                        "event": "v1.2 spec is loaded",
                    },
                ],
                "edge_cases": [],
            },
            {
                "id": "REQ-3",
                "title": "Third requirement",
                "user_story": {
                    "role": "dev",
                    "action": "verify",
                    "benefit": "correctness",
                },
                "acceptance_criteria": [
                    {
                        "id": "134-REQ-3.1",
                        "ears_pattern": "event_driven",
                        "system": "the system",
                        "action": "SHALL count",
                        "event": "count is requested",
                    },
                ],
                "edge_cases": [],
            },
        ],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    },
    indent=2,
)

TEST_SPEC_JSON_VALID = json.dumps(
    {
        "spec_id": "test-134",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_cases": [
            {
                "id": "TS-134-1",
                "requirement_id": "134-REQ-1.1",
                "kind": "unit",
                "description": "Test case 1",
            },
            {
                "id": "TS-134-2",
                "requirement_id": "134-REQ-1.1",
                "kind": "unit",
                "description": "Test case 2",
            },
            {
                "id": "TS-134-3",
                "requirement_id": "134-REQ-2.1",
                "kind": "unit",
                "description": "Test case 3",
            },
        ],
        "property_tests": [
            {
                "id": "TS-134-P1",
                "property_id": "P1",
                "validates": ["134-REQ-1.1"],
                "description": "Property test 1",
            },
            {
                "id": "TS-134-P2",
                "property_id": "P2",
                "validates": ["134-REQ-2.1"],
                "description": "Property test 2",
            },
        ],
        "edge_case_tests": [
            {
                "id": "TS-134-E1",
                "requirement_id": "134-REQ-1.E1",
                "kind": "unit",
                "description": "Edge case 1",
            },
        ],
        "smoke_tests": [
            {
                "id": "TS-134-SMOKE-1",
                "execution_path_id": "path-1",
                "description": "Smoke test 1",
            },
        ],
        "coverage": {
            "requirements_covered": [],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": [],
        },
    },
    indent=2,
)

TASKS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-134",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_commands": {
            "spec_tests": "pytest tests/spec/test_134.py",
            "all_tests": "pytest",
            "linter": "ruff check",
        },
        "dependencies": [],
        "task_groups": [
            {
                "id": 1,
                "kind": "tests",
                "title": "Write failing spec tests",
                "subtasks": [
                    {
                        "id": "1.1",
                        "title": "Create test file",
                        "state": "done",
                    },
                    {
                        "id": "1.2",
                        "title": "Add unit tests",
                        "state": "pending",
                    },
                ],
            },
            {
                "id": 2,
                "kind": "standard",
                "title": "Implement feature",
                "subtasks": [
                    {
                        "id": "2.1",
                        "title": "Add core logic",
                        "state": "in_progress",
                    },
                    {
                        "id": "2.2",
                        "title": "Wire up API",
                        "state": "dropped",
                    },
                ],
            },
        ],
        "traceability": [],
    },
    indent=2,
)

ARCHITECTURE_MD = """\
# Architecture

## Components

The system uses a modular architecture.

## File References

**`agent_fox/session/context.py`** (modified)
"""

# Legacy v1 content
REQUIREMENTS_MD_LEGACY = """\
# Requirements

## Requirement 1

### 134-REQ-1.1

Some legacy requirement text.

### 134-REQ-2.1

Another requirement.
"""

DESIGN_MD_LEGACY = """\
# Design

Design content for the v1 spec.
"""

TEST_SPEC_MD_LEGACY = """\
# Test Specification

### TS-134-1: First test

Test description 1.

### TS-134-2: Second test

Test description 2.

### TS-134-3: Third test

Test description 3.

### TS-134-4: Fourth test

Test description 4.

### TS-134-5: Fifth test

Test description 5.
"""

TASKS_MD_LEGACY = """\
# Implementation Plan

## Tasks

- [ ] 1. First task group
  - [ ] 1.1 Subtask A
  - [x] 1.2 Subtask B
  - [-] 1.3 Subtask C
"""


# ---------------------------------------------------------------------------
# Fixture helpers
# ---------------------------------------------------------------------------


def _write_v12_spec(
    spec_dir: Path,
    *,
    include_architecture: bool = False,
    include_tasks: bool = True,
) -> None:
    """Populate a directory with valid v1.2 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    if include_tasks:
        (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)
    if include_architecture:
        (spec_dir / "architecture.md").write_text(ARCHITECTURE_MD)


def _write_v1_spec(spec_dir: Path) -> None:
    """Populate a directory with legacy v1 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "requirements.md").write_text(REQUIREMENTS_MD_LEGACY)
    (spec_dir / "design.md").write_text(DESIGN_MD_LEGACY)
    (spec_dir / "test_spec.md").write_text(TEST_SPEC_MD_LEGACY)
    (spec_dir / "tasks.md").write_text(TASKS_MD_LEGACY)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def knowledge_conn() -> duckdb.DuckDBPyConnection:
    """In-memory DuckDB connection with schema for context assembly tests."""
    from agent_fox.knowledge.migrations import apply_pending_migrations
    from tests.unit.knowledge.conftest import SCHEMA_DDL

    conn = duckdb.connect(":memory:")
    conn.execute(SCHEMA_DDL)
    apply_pending_migrations(conn)
    return conn


@pytest.fixture
def v12_spec_dir(tmp_path: Path) -> Path:
    """A v1.2 spec directory with all valid artifacts (no architecture.md)."""
    spec_dir = tmp_path / "specs" / "134_test_spec"
    _write_v12_spec(spec_dir)
    return spec_dir


@pytest.fixture
def v12_spec_dir_with_arch(tmp_path: Path) -> Path:
    """A v1.2 spec directory with architecture.md included."""
    spec_dir = tmp_path / "specs" / "134_test_spec_arch"
    _write_v12_spec(spec_dir, include_architecture=True)
    return spec_dir


@pytest.fixture
def v1_spec_dir(tmp_path: Path) -> Path:
    """A v1 spec directory with markdown artifacts."""
    spec_dir = tmp_path / "specs" / "134_legacy_spec"
    _write_v1_spec(spec_dir)
    return spec_dir


@pytest.fixture
def malformed_v12_dir(tmp_path: Path) -> Path:
    """A v1.2 spec directory with malformed JSON that causes LoadError."""
    spec_dir = tmp_path / "specs" / "134_malformed"
    spec_dir.mkdir(parents=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text("{invalid json content!!!")
    (spec_dir / "requirements.md").write_text(REQUIREMENTS_MD_LEGACY)
    (spec_dir / "design.md").write_text(DESIGN_MD_LEGACY)
    (spec_dir / "test_spec.md").write_text(TEST_SPEC_MD_LEGACY)
    (spec_dir / "tasks.md").write_text(TASKS_MD_LEGACY)
    return spec_dir


@pytest.fixture
def malformed_test_spec_dir(tmp_path: Path) -> Path:
    """A spec directory with malformed test_spec.json."""
    spec_dir = tmp_path / "specs" / "134_bad_ts"
    spec_dir.mkdir(parents=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text("{bad json!!")
    (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)
    return spec_dir


@pytest.fixture
def malformed_tasks_dir(tmp_path: Path) -> Path:
    """A v1.2 spec directory with malformed tasks.json."""
    spec_dir = tmp_path / "specs" / "134_bad_tasks"
    spec_dir.mkdir(parents=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    (spec_dir / "tasks.json").write_text("{bad json!!")
    return spec_dir


# ===========================================================================
# TS-134-1: v1.2 format detection in assemble_context
# ===========================================================================


class TestV12FormatDetection:
    """TS-134-1: Verify assemble_context detects v1.2 and uses afspec rendering.

    Requirement: 134-REQ-1.1, 134-REQ-2.1
    """

    def test_v12_context_contains_requirements_section(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context from v1.2 spec contains ## Requirements."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert "## Requirements" in context

    def test_v12_context_contains_test_specification_section(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context from v1.2 spec contains ## Test Specification."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert "## Test Specification" in context

    def test_v12_context_contains_tasks_section(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assembled context from v1.2 spec contains ## Tasks."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert "## Tasks" in context

    def test_v12_context_rendered_from_afspec(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Content is rendered from afspec, not raw file reads."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert "134-REQ-1.1" in context


# ===========================================================================
# TS-134-2: v1 format unchanged in assemble_context
# ===========================================================================


class TestV1FormatUnchanged:
    """TS-134-2: Verify assemble_context uses raw markdown reads for v1 specs.

    Requirement: 134-REQ-1.2
    """

    def test_v1_context_contains_requirements(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context contains ## Requirements header."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)
        assert "## Requirements" in context

    def test_v1_context_contains_design(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context contains ## Design header."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)
        assert "## Design" in context

    def test_v1_context_contains_test_specification(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context contains ## Test Specification header."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)
        assert "## Test Specification" in context

    def test_v1_context_contains_tasks(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context contains ## Tasks header."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)
        assert "## Tasks" in context

    def test_v1_content_matches_raw_files(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context content comes from raw file reads."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)
        assert "Design content for the v1 spec" in context


# ===========================================================================
# TS-134-3: v1.2 architecture.md included when present
# ===========================================================================


class TestArchitectureMdIncluded:
    """TS-134-3: Verify architecture.md is included for v1.2 specs.

    Requirement: 134-REQ-2.2
    """

    def test_architecture_section_present(
        self,
        v12_spec_dir_with_arch: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Context contains ## Architecture when architecture.md exists."""
        context = assemble_context(v12_spec_dir_with_arch, 1, conn=knowledge_conn)
        assert "## Architecture" in context

    def test_architecture_content_included(
        self,
        v12_spec_dir_with_arch: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Context includes the architecture.md file content."""
        context = assemble_context(v12_spec_dir_with_arch, 1, conn=knowledge_conn)
        assert "modular architecture" in context


# ===========================================================================
# TS-134-4: v1.2 architecture.md omitted when absent
# ===========================================================================


class TestArchitectureMdOmitted:
    """TS-134-4: Verify missing architecture.md is silently omitted.

    Requirement: 134-REQ-2.3
    """

    def test_no_architecture_section(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Context does NOT contain ## Architecture when file is absent."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert "## Architecture" not in context

    def test_no_warning_logged(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """No warning is logged about missing architecture.md."""
        with caplog.at_level(logging.WARNING):
            assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        assert not any("architecture" in r.message.lower() for r in caplog.records)


# ===========================================================================
# TS-134-5: count_ts_entries with v1.2 test_spec.json
# ===========================================================================


class TestCountTsV12:
    """TS-134-5: Verify count_ts_entries loads test_spec.json and counts.

    Requirement: 134-REQ-3.1
    """

    def test_count_matches_test_entries(self, v12_spec_dir: Path) -> None:
        """Returns the sum of all test entry lists from test_spec.json.

        Fixture has: 3 test_cases + 2 property_tests + 1 edge_case_test
        + 1 smoke_test = 7.
        """
        count = count_ts_entries(v12_spec_dir)
        assert count == 7


# ===========================================================================
# TS-134-6: count_ts_entries with v1 test_spec.md unchanged
# ===========================================================================


class TestCountTsV1:
    """TS-134-6: Verify count_ts_entries uses heading counting for v1 specs.

    Requirement: 134-REQ-3.2
    """

    def test_count_matches_ts_headings(self, v1_spec_dir: Path) -> None:
        """Returns the count of ### TS- headings in test_spec.md.

        Fixture has 5 ### TS- headings.
        """
        count = count_ts_entries(v1_spec_dir)
        assert count == 5


# ===========================================================================
# TS-134-7: spec_has_existing_code checks architecture.md for v1.2
# ===========================================================================


class TestExistingCodeV12:
    """TS-134-7: Verify spec_has_existing_code reads architecture.md for v1.2.

    Requirement: 134-REQ-3.3
    """

    def test_checks_architecture_md(self, v12_spec_dir_with_arch: Path) -> None:
        """Returns True when architecture.md references an existing file."""
        result = spec_has_existing_code(v12_spec_dir_with_arch)
        assert result is True

    def test_does_not_check_design_md(self, tmp_path: Path) -> None:
        """For v1.2, design.md is NOT checked (architecture.md is used)."""
        spec_dir = tmp_path / "specs" / "134_v12_design_test"
        spec_dir.mkdir(parents=True)
        (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
        (spec_dir / "design.md").write_text("**`agent_fox/session/context.py`** (modified)\n")
        (spec_dir / "architecture.md").write_text("**`nonexistent/file.py`** (modified)\n")
        result = spec_has_existing_code(spec_dir)
        assert result is False


# ===========================================================================
# TS-134-8: v1.2 verification checklist extracts tasks from JSON
# ===========================================================================


class TestChecklistTasksV12:
    """TS-134-8: Verify _audit_task_checkboxes loads tasks.json via afspec.

    Requirement: 134-REQ-4.1
    """

    def test_returns_subtask_entries(self, v12_spec_dir: Path) -> None:
        """Returns a non-empty list of SubtaskAuditEntry objects."""
        entries = _audit_task_checkboxes(v12_spec_dir)
        assert len(entries) > 0
        assert all(isinstance(e, SubtaskAuditEntry) for e in entries)

    def test_subtask_ids_match_json(self, v12_spec_dir: Path) -> None:
        """Subtask IDs match those in tasks.json."""
        entries = _audit_task_checkboxes(v12_spec_dir)
        ids = {e.subtask_id for e in entries}
        assert "1.1" in ids
        assert "1.2" in ids
        assert "2.1" in ids

    def test_checked_state_matches_json(self, v12_spec_dir: Path) -> None:
        """Checked state reflects tasks.json subtask state."""
        entries = _audit_task_checkboxes(v12_spec_dir)
        entry_map = {e.subtask_id: e for e in entries}
        assert entry_map["1.1"].checked is True
        assert entry_map["1.2"].checked is False

    def test_group_numbers_match(self, v12_spec_dir: Path) -> None:
        """Group numbers match the task group IDs from tasks.json."""
        entries = _audit_task_checkboxes(v12_spec_dir)
        entry_map = {e.subtask_id: e for e in entries}
        assert entry_map["1.1"].group_number == 1
        assert entry_map["2.1"].group_number == 2


# ===========================================================================
# TS-134-9: v1.2 verification checklist extracts requirements from JSON
# ===========================================================================


class TestChecklistRequirementsV12:
    """TS-134-9: Verify scan_requirement_test_coverage uses requirements.json.

    Requirement: 134-REQ-4.2
    """

    def test_returns_requirement_mappings(
        self,
        v12_spec_dir: Path,
        tmp_path: Path,
    ) -> None:
        """Returns a non-empty list of RequirementMapping objects."""
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        mappings = scan_requirement_test_coverage(v12_spec_dir, tests_dir)
        assert len(mappings) > 0
        assert all(isinstance(m, RequirementMapping) for m in mappings)

    def test_requirement_ids_from_json(
        self,
        v12_spec_dir: Path,
        tmp_path: Path,
    ) -> None:
        """Requirement IDs are extracted from requirements.json criteria."""
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        mappings = scan_requirement_test_coverage(v12_spec_dir, tests_dir)
        req_ids = {m.requirement_id for m in mappings}
        assert "134-REQ-1.1" in req_ids

    def test_covered_when_test_references_id(
        self,
        v12_spec_dir: Path,
        tmp_path: Path,
    ) -> None:
        """A requirement is marked covered when a test file references its ID."""
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        test_file = tests_dir / "test_example.py"
        test_file.write_text(
            '''"""Test for 134-REQ-1.1."""
def test_req_134_1_1():
    pass
'''
        )
        mappings = scan_requirement_test_coverage(v12_spec_dir, tests_dir)
        covered = {m.requirement_id for m in mappings if m.covered}
        assert "134-REQ-1.1" in covered


# ===========================================================================
# TS-134-E1: LoadError fallback in assemble_context
# ===========================================================================


class TestLoadErrorFallback:
    """TS-134-E1: LoadError causes fallback to raw markdown reads.

    Requirement: 134-REQ-1.E1
    """

    def test_does_not_raise(
        self,
        malformed_v12_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """assemble_context does not raise when LoadError occurs."""
        context = assemble_context(malformed_v12_dir, 1, conn=knowledge_conn)
        assert context is not None

    def test_fallback_reads_md_files(
        self,
        malformed_v12_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Fallback reads whatever .md files exist in the folder."""
        context = assemble_context(malformed_v12_dir, 1, conn=knowledge_conn)
        assert "## " in context

    def test_warning_logged(
        self,
        malformed_v12_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """A warning is logged about the load failure."""
        with caplog.at_level(logging.WARNING):
            assemble_context(malformed_v12_dir, 1, conn=knowledge_conn)
        assert any("load" in r.message.lower() or "error" in r.message.lower() for r in caplog.records)


# ===========================================================================
# TS-134-E2: Empty render_individual artifact omitted
# ===========================================================================


class TestEmptyArtifactOmitted:
    """TS-134-E2: Empty rendered artifact sections are omitted.

    Requirement: 134-REQ-2.E1
    """

    def test_empty_tasks_section_omitted(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """When render_individual returns empty for tasks, ## Tasks is absent."""
        import afspec

        original_render = afspec.render_individual

        def mock_render(spec):
            result = original_render(spec)
            result["tasks"] = ""
            return result

        with patch("afspec.render_individual", side_effect=mock_render):
            context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)

        assert "## Tasks" not in context


# ===========================================================================
# TS-134-E3: count_ts_entries returns 0 on load failure
# ===========================================================================


class TestCountTsLoadFailure:
    """TS-134-E3: count_ts_entries returns 0 when loading fails.

    Requirement: 134-REQ-3.E1
    """

    def test_returns_zero(self, malformed_test_spec_dir: Path) -> None:
        """Returns 0 for malformed test_spec.json."""
        count = count_ts_entries(malformed_test_spec_dir)
        assert count == 0

    def test_warning_logged(
        self,
        malformed_test_spec_dir: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """A warning is logged about the load failure."""
        with caplog.at_level(logging.WARNING):
            count_ts_entries(malformed_test_spec_dir)
        assert any("test_spec" in r.message.lower() or "fail" in r.message.lower() for r in caplog.records)


# ===========================================================================
# TS-134-E4: Verification checklist returns empty on JSON load failure
# ===========================================================================


class TestChecklistLoadFailure:
    """TS-134-E4: Checklist functions return empty on load failure.

    Requirement: 134-REQ-4.E1
    """

    def test_audit_returns_empty(self, malformed_tasks_dir: Path) -> None:
        """_audit_task_checkboxes returns empty list for malformed tasks.json."""
        entries = _audit_task_checkboxes(malformed_tasks_dir)
        assert entries == []

    def test_audit_warning_logged(
        self,
        malformed_tasks_dir: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """A warning is logged when tasks.json loading fails."""
        with caplog.at_level(logging.WARNING):
            _audit_task_checkboxes(malformed_tasks_dir)
        assert any("task" in r.message.lower() or "fail" in r.message.lower() for r in caplog.records)


# ===========================================================================
# TS-134-P1: v1 path produces identical output
# ===========================================================================


class TestV1PathIdentical:
    """TS-134-P1: v1 spec folders produce unchanged output.

    Property: Property 2 from design.md
    Validates: 134-REQ-1.2
    """

    @pytest.mark.property
    def test_v1_output_matches_expected(
        self,
        v1_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """v1 context has the same structure as pre-change behavior."""
        context = assemble_context(v1_spec_dir, 1, conn=knowledge_conn)

        assert "## Requirements" in context
        assert "## Design" in context
        assert "## Test Specification" in context
        assert "## Tasks" in context
        assert "Design content for the v1 spec" in context
        assert "Some legacy requirement text" in context


# ===========================================================================
# TS-134-P2: v1.2 rendering preserves section order
# ===========================================================================


class TestV12SectionOrder:
    """TS-134-P2: v1.2 rendered context has sections in canonical order.

    Property: Property 1 from design.md
    Validates: 134-REQ-2.1
    """

    @pytest.mark.property
    def test_section_order_preserved(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """## Requirements < ## Test Specification < ## Tasks in context."""
        context = assemble_context(v12_spec_dir, 1, conn=knowledge_conn)
        idx_req = context.index("## Requirements")
        idx_ts = context.index("## Test Specification")
        idx_tasks = context.index("## Tasks")
        assert idx_req < idx_ts < idx_tasks


# ===========================================================================
# TS-134-SMOKE-1: End-to-end v1.2 context assembly
# ===========================================================================


class TestSmokeV12Assembly:
    """TS-134-SMOKE-1: End-to-end v1.2 context assembly.

    Execution Path: Path 1 from design.md
    Must NOT mock afspec.load_spec or render_individual.
    """

    def test_full_v12_assembly(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Assemble full context from valid v1.2 spec with all artifacts."""
        project_root = tmp_path / "project"
        project_root.mkdir()
        agent_fox_dir = project_root / ".agent-fox"
        agent_fox_dir.mkdir()
        (agent_fox_dir / "steering.md").write_text("")

        spec_dir = project_root / ".agent-fox" / "specs" / "134_smoke_test"
        _write_v12_spec(spec_dir, include_architecture=True)

        context = assemble_context(
            spec_dir,
            1,
            memory_facts=["fact1"],
            conn=knowledge_conn,
            project_root=project_root,
        )

        assert "## Requirements" in context
        assert "## Test Specification" in context
        assert "## Tasks" in context
        assert "## Architecture" in context
        assert "## Memory Facts" in context
        assert "fact1" in context


# ===========================================================================
# TS-134-SMOKE-2: End-to-end v1.2 verification checklist
# ===========================================================================


class TestSmokeV12Checklist:
    """TS-134-SMOKE-2: End-to-end v1.2 verification checklist.

    Execution Path: Path 5 from design.md
    Must NOT mock afspec.load_spec.
    """

    def test_full_v12_checklist(
        self,
        v12_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
        tmp_path: Path,
    ) -> None:
        """Build complete verification checklist from v1.2 spec."""
        tests_dir = tmp_path / "tests"
        tests_dir.mkdir()
        test_file = tests_dir / "test_coverage.py"
        test_file.write_text(
            '''"""Tests for 134-REQ-1.1."""
def test_req_134_1_1():
    pass
'''
        )

        checklist = build_verification_checklist(v12_spec_dir, knowledge_conn, tests_dir=tests_dir)

        # Task audit: tasks.json has 2 groups with subtasks
        assert len(checklist.task_audit) >= 3
        assert len(checklist.requirement_coverage) >= 1
        covered = [m for m in checklist.requirement_coverage if m.covered]
        assert len(covered) >= 1
