"""Spec 135: v1.2 Skill Template and Validation Migration tests.

Test Spec: TS-135-1 through TS-135-10, TS-135-E1 through TS-135-E3,
           TS-135-P1, TS-135-P2, TS-135-SMOKE-1, TS-135-SMOKE-2
Requirements: 135-REQ-1.1, 135-REQ-1.2, 135-REQ-1.3, 135-REQ-1.E1,
              135-REQ-2.1, 135-REQ-2.2, 135-REQ-2.E1,
              135-REQ-3.1, 135-REQ-3.2,
              135-REQ-4.1, 135-REQ-4.2, 135-REQ-4.3,
              135-REQ-5.1, 135-REQ-5.2,
              135-REQ-6.1, 135-REQ-6.2
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox.spec.discovery import SpecInfo
from agentfox.spec.lint import LintResult, run_lint_specs
from agentfox.spec.types import Finding
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Repo root and skill template path
# ---------------------------------------------------------------------------

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
_SKILL_TEMPLATE_PATH = _REPO_ROOT / "agentfox" / "_templates" / "skills" / "afspec"

# ---------------------------------------------------------------------------
# Valid v1.2 fixture content
# ---------------------------------------------------------------------------

PRD_MD_VALID = """\
---
spec_id: "test-135"
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
        "spec_id": "test-135",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "introduction": "Test requirements",
        "glossary": {},
        "requirements": [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    },
    indent=2,
)

TEST_SPEC_JSON_VALID = json.dumps(
    {
        "spec_id": "test-135",
        "spec_name": "test_fixture",
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
    },
    indent=2,
)

TASKS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-135",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [],
        "traceability": [],
    },
    indent=2,
)

# Legacy v1 content
REQUIREMENTS_MD_LEGACY = """\
# Requirements Document

## Introduction
Test legacy spec.

## Glossary
| Term | Definition |
|------|-----------|

## Requirements

### Requirement 1: Test

**User Story:** As a user, I want to test, so that tests work.

#### Acceptance Criteria
1. [135-REQ-1.1] THE system SHALL do the thing.
"""

DESIGN_MD_LEGACY = """\
# Design Document: Test

## Overview
Test design.

## Architecture
Test architecture.

## Execution Paths
None.

## Correctness Properties

### Property 1: Test
*For any* input, the system SHALL produce output.
**Validates: Requirements 1.1**

## Error Handling

| Error Condition | Behavior | Requirement |
|----------------|----------|-------------|

## Definition of Done
A task group is complete when all tests pass.

## Testing Strategy
Unit tests.
"""

TEST_SPEC_MD_LEGACY = """\
# Test Specification: Test

## Overview
Test specification.

## Test Cases

### TS-135-1: Test
**Requirement:** 135-REQ-1.1
**Type:** unit
**Description:** Test.

## Integration Smoke Tests

None.

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 135-REQ-1.1 | TS-135-1 | unit |
"""

TASKS_MD_LEGACY = """\
# Implementation Plan

## Overview
Test implementation.

## Test Commands
- All tests: `uv run pytest -q`

## Tasks

- [ ] 1. First task
  - [ ] 1.1 Subtask A
    - Do the thing
  - [ ] 1.V Verify
    - [ ] Tests pass

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 135-REQ-1.1 | TS-135-1 | 1.1 | test_foo |
"""


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write_spec(spec_dir: Path, *, include_tasks: bool = True) -> None:
    """Populate a directory with valid v1.2 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    if include_tasks:
        (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)


def _write_v1_spec(spec_dir: Path) -> None:
    """Populate a directory with legacy v1 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.md").write_text(REQUIREMENTS_MD_LEGACY)
    (spec_dir / "design.md").write_text(DESIGN_MD_LEGACY)
    (spec_dir / "test_spec.md").write_text(TEST_SPEC_MD_LEGACY)
    (spec_dir / "tasks.md").write_text(TASKS_MD_LEGACY)


def _make_spec_info(root: Path, name: str, prefix: int) -> SpecInfo:
    """Create a SpecInfo for a v1.2 JSON spec."""
    return SpecInfo(
        name=name,
        prefix=prefix,
        path=root / name,
        has_tasks=True,
        has_prd=True,
    )


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def v12_specs_root(tmp_path: Path) -> Path:
    """A specs root with one v1.2 spec folder."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec(root / "02_modern")
    return root


@pytest.fixture
def v1_specs_root(tmp_path: Path) -> Path:
    """A specs root with one v1 markdown spec folder."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_v1_spec(root / "01_legacy")
    return root


@pytest.fixture
def mixed_specs_root(tmp_path: Path) -> Path:
    """A specs root with one v1 and one v1.2 spec folder."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_v1_spec(root / "01_legacy")
    _write_spec(root / "02_modern")
    return root


# ===========================================================================
# TS-135-1: v1.2 spec routed to afspec.validate
# ===========================================================================


class TestV12RoutedToAfspec:
    """TS-135-1: Verify v1.2 spec is validated using afspec.validate().

    Requirement: 135-REQ-1.1
    """

    def test_v12_routed_to_afspec(self, v12_specs_root: Path) -> None:
        """A V1_2_JSON spec should be validated via afspec.validate()."""
        mock_result = type("MockResult", (), {"errors": [], "valid": True})()
        with patch("afspec.validate", return_value=mock_result) as mock_validate:
            result = run_lint_specs(v12_specs_root)
            assert mock_validate.called, "afspec.validate() should be called for v1.2 specs"
            assert isinstance(result, LintResult)

    def test_v12_returns_finding_instances(self, v12_specs_root: Path) -> None:
        """Results from v1.2 validation should be Finding instances."""
        import afspec

        mock_error = afspec.ValidationError(
            file="requirements.json",
            rule="test-rule",
            message="test message",
        )
        mock_result = type("MockResult", (), {"errors": [mock_error], "valid": False})()
        with patch("afspec.validate", return_value=mock_result):
            # The implementation must map ValidationError to Finding
            result = run_lint_specs(v12_specs_root)
            v12_findings = [f for f in result.findings if f.spec_name == "02_modern"]
            assert all(isinstance(f, Finding) for f in v12_findings)


# ===========================================================================
# TS-135-2: (Removed) v1 spec routed to custom validators
# v1 routing was removed in spec 137 (legacy format removal).
# All specs now go through afspec validation.
# ===========================================================================


# ===========================================================================
# TS-135-3: (Removed) Mixed format specs validated by correct validators
# v1/v1.2 mixed routing was removed in spec 137 (legacy format removal).
# All specs now go through afspec validation exclusively.
# ===========================================================================


# ===========================================================================
# TS-135-4: ValidationError mapped to Finding correctly
# ===========================================================================


class TestValidationErrorMapping:
    """TS-135-4: Verify ValidationError fields map to Finding correctly.

    Requirement: 135-REQ-2.1

    Note: afspec.ValidationError has only (file, path, message, rule) --
    no severity or line fields. The mapping function should default
    severity to "error" and line to None. See errata
    135_validation_error_fields.md.
    """

    def test_validation_error_mapped_to_finding(self) -> None:
        """Each ValidationError field should map to the correct Finding field."""
        import afspec

        # Import the mapping function (will fail until implemented)
        from agentfox.spec.lint import _map_afspec_findings

        ve = afspec.ValidationError(
            file="requirements.json",
            rule="missing-field",
            message="Field 'title' is required",
            path="requirements.title",
        )
        findings = _map_afspec_findings("02_modern", [ve])
        assert len(findings) == 1
        f = findings[0]
        assert f.spec_name == "02_modern"
        assert f.file == "requirements.json"
        assert f.rule == "missing-field"
        assert f.message == "Field 'title' is required"
        # ValidationError has no severity field -- should default to "error"
        assert f.severity == "error"
        # ValidationError has no line field -- should default to None
        assert f.line is None

    def test_multiple_errors_mapped(self) -> None:
        """Multiple ValidationErrors should each produce a Finding."""
        import afspec
        from agentfox.spec.lint import _map_afspec_findings

        errors = [
            afspec.ValidationError(
                file="requirements.json",
                rule="rule-a",
                message="msg a",
            ),
            afspec.ValidationError(
                file="tasks.json",
                rule="rule-b",
                message="msg b",
            ),
        ]
        findings = _map_afspec_findings("test_spec", errors)
        assert len(findings) == 2
        assert all(isinstance(f, Finding) for f in findings)
        assert findings[0].file == "requirements.json"
        assert findings[1].file == "tasks.json"


# ===========================================================================
# TS-135-5: Unknown severity defaults to error
# ===========================================================================


class TestUnknownSeverityDefault:
    """TS-135-5: Verify unknown severity defaults to "error".

    Requirement: 135-REQ-2.2

    Note: afspec.ValidationError has no severity field at all, so all
    findings from afspec default to "error" severity. This test verifies
    that default behavior. See errata 135_validation_error_fields.md.
    """

    def test_no_severity_defaults_to_error(self) -> None:
        """ValidationError without severity maps to Finding severity='error'."""
        import afspec
        from agentfox.spec.lint import _map_afspec_findings

        ve = afspec.ValidationError(
            file="tasks.json",
            rule="unknown-rule",
            message="Something bad",
        )
        findings = _map_afspec_findings("test_spec", [ve])
        assert findings[0].severity == "error"


# ===========================================================================
# TS-135-6: CLI flags unchanged
# ===========================================================================


# ===========================================================================
# TS-135-7: Skill template references v1.2 artifacts
# ===========================================================================


class TestSkillTemplateV12Artifacts:
    """TS-135-7: Verify skill template references v1.2 artifact names.

    Requirement: 135-REQ-4.1
    """

    def test_references_requirements_json(self) -> None:
        """Skill template should reference requirements.json."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "requirements.json" in content

    def test_references_test_spec_json(self) -> None:
        """Skill template should reference test_spec.json."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "test_spec.json" in content

    def test_references_tasks_json(self) -> None:
        """Skill template should reference tasks.json."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "tasks.json" in content

    def test_references_yaml_frontmatter(self) -> None:
        """Skill template should reference YAML frontmatter for prd.md."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        # Check for either "YAML frontmatter" or just "frontmatter"
        assert "frontmatter" in content.lower()

    def test_references_architecture_md(self) -> None:
        """Skill template should reference architecture.md as optional."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "architecture.md" in content


# ===========================================================================
# TS-135-8: (Removed) Skill template references v1.2 ID formats
# ID format details are now in the spec CLI prompt templates, not the skill.
# ===========================================================================


# ===========================================================================
# TS-135-9: (Removed) Skill template describes EARS JSON structure
# EARS pattern details are now in the spec CLI prompt templates, not the skill.
# ===========================================================================


# ===========================================================================
# TS-135-10: (Removed) Skill template describes tasks JSON structure
# Task state details are now in the spec CLI prompt templates, not the skill.
# ===========================================================================


# ===========================================================================
# TS-135-E1: afspec.validate raises exception
# ===========================================================================


class TestAfspecValidateException:
    """TS-135-E1: Verify exception from afspec.validate() is caught.

    Requirement: 135-REQ-1.E1
    """

    def test_exception_produces_error_finding(self, v12_specs_root: Path) -> None:
        """An exception from afspec.validate() should produce error Finding."""
        with patch("afspec.validate", side_effect=RuntimeError("schema broken")):
            result = run_lint_specs(v12_specs_root)
            afspec_error_findings = [f for f in result.findings if f.rule == "afspec-error"]
            assert len(afspec_error_findings) >= 1, "Should have at least one afspec-error finding"
            error_finding = afspec_error_findings[0]
            assert error_finding.severity == "error"
            assert "schema broken" in error_finding.message

    def test_exception_does_not_crash(self, v12_specs_root: Path) -> None:
        """An exception should not crash the linter."""
        with patch("afspec.validate", side_effect=RuntimeError("schema broken")):
            # Should not raise -- should return a result with error findings
            result = run_lint_specs(v12_specs_root)
            assert isinstance(result, LintResult)


# ===========================================================================
# TS-135-E2: Empty validation result for clean v1.2 spec
# ===========================================================================


class TestEmptyValidationResult:
    """TS-135-E2: Verify empty ValidationError list produces zero findings.

    Requirement: 135-REQ-2.E1
    """

    def test_empty_result_produces_zero_findings(self, v12_specs_root: Path) -> None:
        """Empty ValidationError list should produce zero findings."""
        mock_result = type("MockResult", (), {"errors": [], "valid": True})()
        with patch("afspec.validate", return_value=mock_result):
            result = run_lint_specs(v12_specs_root)
            v12_findings = [f for f in result.findings if f.spec_name == "02_modern"]
            assert len(v12_findings) == 0


# ===========================================================================
# TS-135-E3: ValidationError with unknown severity
# ===========================================================================


class TestUnknownSeverityMapping:
    """TS-135-E3: Verify unknown severity maps to "error".

    Requirement: 135-REQ-2.2

    Note: afspec.ValidationError has no severity field, so all findings
    default to "error". See errata 135_validation_error_fields.md.
    """

    def test_defaults_to_error_severity(self) -> None:
        """Finding severity should default to 'error'."""
        import afspec
        from agentfox.spec.lint import _map_afspec_findings

        ve = afspec.ValidationError(
            file="x.json",
            rule="r",
            message="m",
        )
        findings = _map_afspec_findings("spec", [ve])
        assert findings[0].severity == "error"


# ===========================================================================
# TS-135-P1: Finding mapping preserves all fields (property test)
# ===========================================================================


class TestFindingMappingPreservesFields:
    """TS-135-P1: For any ValidationError, mapping preserves all fields.

    Property: Property 2 from design.md
    Validates: 135-REQ-2.1, 135-REQ-2.2

    Note: Adapted for actual afspec.ValidationError interface which has
    only (file, path, message, rule). See errata
    135_validation_error_fields.md.
    """

    @pytest.mark.property
    @settings(max_examples=20, deadline=None)
    @given(
        file=st.text(min_size=1, max_size=50).filter(lambda s: s.strip()),
        rule=st.text(min_size=1, max_size=50).filter(lambda s: s.strip()),
        message=st.text(min_size=1, max_size=100).filter(lambda s: s.strip()),
        path=st.text(min_size=0, max_size=50),
        spec_name=st.text(min_size=1, max_size=30).filter(lambda s: s.strip()),
    )
    def test_mapping_preserves_fields(
        self,
        file: str,
        rule: str,
        message: str,
        path: str,
        spec_name: str,
    ) -> None:
        """All ValidationError fields should map correctly to Finding."""
        import afspec
        from agentfox.spec.lint import _map_afspec_findings

        ve = afspec.ValidationError(
            file=file,
            rule=rule,
            message=message,
            path=path,
        )
        findings = _map_afspec_findings(spec_name, [ve])
        assert len(findings) == 1
        f = findings[0]
        assert f.spec_name == spec_name
        assert f.file == file
        assert f.rule == rule
        assert f.message == message
        # ValidationError has no severity -- should default to "error"
        assert f.severity == "error"
        # ValidationError has no line -- should default to None
        assert f.line is None


# ===========================================================================
# TS-135-P2: (Removed) Format routing is exhaustive (property test)
# v1/v1.2 format routing was removed in spec 137 (legacy format removal).
# All specs now go through afspec validation exclusively.
# ===========================================================================


# ===========================================================================
# TS-135-SMOKE-1: (Simplified) Lint end-to-end
# Mixed-format routing was removed in spec 137. This now tests that all
# specs go through afspec validation.
# ===========================================================================


class TestLintSmoke:
    """TS-135-SMOKE-1: End-to-end lint with v1.2 specs.

    Execution Path: Path 3 from design.md

    Updated for spec 137: all specs now go through afspec validation.
    """

    def test_lint_end_to_end(self, v12_specs_root: Path) -> None:
        """Linting v1.2 specs should invoke afspec validation."""
        v12_spec = _make_spec_info(v12_specs_root, "02_modern", 2)

        mock_result = type("MockResult", (), {"errors": [], "valid": True})()
        with (
            patch(
                "agentfox.spec.lint.discover_specs",
                return_value=[v12_spec],
            ),
            patch("afspec.validate", return_value=mock_result) as mock_v12,
            patch("afspec.load_spec"),
        ):
            result = run_lint_specs(v12_specs_root, lint_all=True)
            assert isinstance(result, LintResult)
            assert mock_v12.called, "afspec.validate() should be called for v1.2 specs"


# ===========================================================================
# TS-135-SMOKE-2: Skill template content validation
# ===========================================================================


class TestSkillTemplateContentSmoke:
    """TS-135-SMOKE-2: Verify skill template contains all v1.2 references.

    Execution Path: N/A (template content test)
    """

    def test_skill_template_complete_v12_content(self) -> None:
        """Skill template should contain all required v1.2 references."""
        content = _SKILL_TEMPLATE_PATH.read_text()

        # v1.2 artifact names
        assert "requirements.json" in content, "Template should reference requirements.json"
        assert "test_spec.json" in content, "Template should reference test_spec.json"
        assert "tasks.json" in content, "Template should reference tasks.json"

        # Validation step
        assert "spec validate" in content, "Template should reference spec validate"

        # CLI commands
        assert "spec new" in content, "Template should reference spec new"
        assert "spec generate" in content, "Template should reference spec generate"

    def test_skill_template_validation_step(self) -> None:
        """Skill template should include a validation step."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "spec validate" in content

    def test_skill_template_afspec_reference(self) -> None:
        """Skill template should reference afspec."""
        content = _SKILL_TEMPLATE_PATH.read_text()
        assert "afspec" in content.lower()
