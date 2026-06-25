"""Tests for afspec validation — schema, cross-file, and EARS rules.

All tests are expected to FAIL against the current stub implementation
(which raises NotImplementedError).
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from hypothesis import given
from hypothesis.strategies import sampled_from

import afspec
from afspec import (
    Criterion,
    EARSPattern,
    Requirement,
    Spec,
    TaskGroupKind,
    TestCase,
    TraceabilityEntry,
    UserStory,
    validate_cross_file,
    validate_schema,
)
from afspec.schemas import schemas

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _load_golden(valid_spec_dir: Path) -> Spec:
    """Load the golden valid spec via afspec.load_spec."""
    return afspec.load_spec(valid_spec_dir)


def _golden_requirements_dict(valid_spec_dir: Path) -> dict:
    """Load requirements.json from the golden fixture as a raw dict."""
    return json.loads((valid_spec_dir / "requirements.json").read_text())


def _golden_test_spec_dict(valid_spec_dir: Path) -> dict:
    """Load test_spec.json from the golden fixture as a raw dict."""
    return json.loads((valid_spec_dir / "test_spec.json").read_text())


def _golden_tasks_dict(valid_spec_dir: Path) -> dict:
    """Load tasks.json from the golden fixture as a raw dict."""
    return json.loads((valid_spec_dir / "tasks.json").read_text())


# ---------------------------------------------------------------------------
# TS-01-13: Schema validation catches missing required fields
# ---------------------------------------------------------------------------


class TestSchemaValidation:
    """TS-01-13: validate_schema returns errors for invalid specs."""

    def test_schema_validation(self, valid_spec_dir: Path) -> None:
        """A spec with a missing required field produces schema errors."""
        spec = _load_golden(valid_spec_dir)

        # Mutate: remove the required 'introduction' field from requirements
        # to make the spec invalid for schema validation.
        spec.requirements.introduction = ""
        # Also clear spec_id to provoke a more obvious schema violation
        original_spec_id = spec.requirements.spec_id
        spec.requirements.spec_id = ""

        errors = validate_schema(spec)

        # The contract: validate_schema returns a non-empty list with at
        # least one error referencing "requirements.json".
        assert isinstance(errors, list)
        assert len(errors) > 0
        assert any("requirements.json" in e.file or "requirements.json" in e.message for e in errors)

        # Restore and verify valid spec passes
        spec.requirements.introduction = "The test feature validates the spec library."
        spec.requirements.spec_id = original_spec_id


# ---------------------------------------------------------------------------
# TS-01-14: Bundled JSON Schemas are accessible
# ---------------------------------------------------------------------------


class TestSchemasEmbedded:
    """TS-01-14: schemas() returns embedded JSON Schema bytes."""

    def test_schemas_embedded(self) -> None:
        result = schemas()
        assert isinstance(result, dict)
        assert len(result) == 4

        for name, raw in result.items():
            assert isinstance(raw, bytes)
            assert len(raw) > 0
            # Each value must be valid JSON.
            parsed = json.loads(raw)
            assert isinstance(parsed, dict)


# ---------------------------------------------------------------------------
# TS-01-15 / TS-01-E1: EARS pattern mismatch (ubiquitous + trigger)
# ---------------------------------------------------------------------------


class TestEarsPatternMismatch:
    """TS-01-15: ubiquitous criterion must not have a trigger field."""

    def test_ears_pattern_mismatch(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Mutate: set first criterion to ubiquitous but keep a trigger value.
        req = spec.requirements.requirements[0]
        criterion = req.acceptance_criteria[0]
        criterion.ears_pattern = EARSPattern.UBIQUITOUS
        criterion.trigger = "some trigger that should not be here"

        errors = validate_schema(spec)
        assert len(errors) > 0
        assert any("trigger" in e.message.lower() for e in errors)


# ---------------------------------------------------------------------------
# TS-01-16: Cross-file validation on a valid spec returns no errors
# ---------------------------------------------------------------------------


class TestCrossFileValid:
    """TS-01-16: validate_cross_file on golden spec produces zero errors."""

    def test_cross_file_valid(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)
        errors = validate_cross_file(spec)
        assert isinstance(errors, list)
        assert len(errors) == 0


# ---------------------------------------------------------------------------
# TS-01-17: Dangling requirement reference in test cases
# ---------------------------------------------------------------------------


class TestDanglingRequirementRef:
    """TS-01-17: test case referencing non-existent requirement triggers error."""

    def test_dangling_requirement_ref(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Inject a test case referencing a requirement that does not exist.
        bogus_case = TestCase(
            id="TS-01-BOGUS",
            requirement_id="99-REQ-99.1",
            kind="unit",
            description="Bogus test for dangling ref",
            preconditions=[],
            input={},
            expected={},
            assertion_pseudocode="assert False",
        )
        spec.test_spec.test_cases.append(bogus_case)

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        assert any("99-REQ-99.1" in e.message for e in errors)


# ---------------------------------------------------------------------------
# TS-01-18: Missing test coverage for a requirement
# ---------------------------------------------------------------------------


class TestMissingTestCoverage:
    """TS-01-18: requirement with no test case triggers cross-file-2."""

    def test_missing_test_coverage(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Add a requirement with no corresponding test case.
        orphan_req = Requirement(
            id="05-REQ-1.1",
            title="Orphan requirement",
            user_story=UserStory(
                role="developer",
                goal="have coverage",
                benefit="completeness",
            ),
            acceptance_criteria=[],
            edge_cases=[],
        )
        spec.requirements.requirements.append(orphan_req)

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        matching = [e for e in errors if e.rule == "cross-file-2" and "05-REQ-1.1" in e.message]
        assert len(matching) > 0


# ---------------------------------------------------------------------------
# TS-01-19: Dangling test_spec_id in traceability
# ---------------------------------------------------------------------------


class TestDanglingTestSpecRef:
    """TS-01-19: traceability entry referencing non-existent test_spec_id."""

    def test_dangling_test_spec_ref(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Add a traceability entry pointing to a test case that does not exist.
        bad_entry = TraceabilityEntry(
            requirement_id="01-REQ-1.1",
            test_spec_id="TS-05-999",
            task_id="1.1",
            test_path=None,
        )
        spec.tasks.traceability.append(bad_entry)

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        assert any("TS-05-999" in e.message for e in errors)


# ---------------------------------------------------------------------------
# TS-01-20: Glossary cross-check for backtick terms
# ---------------------------------------------------------------------------


class TestGlossaryCrossCheck:
    """TS-01-20: requirement action using undefined glossary term."""

    def test_glossary_cross_check(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Mutate the action of the first criterion to mention an undefined term.
        criterion = spec.requirements.requirements[0].acceptance_criteria[0]
        criterion.action = "return a populated `SpaceManager` instance"

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        matching = [e for e in errors if e.rule == "cross-file-6" and "SpaceManager" in e.message]
        assert len(matching) > 0


# ---------------------------------------------------------------------------
# TS-01-21: Spec ID consistency across artifacts
# ---------------------------------------------------------------------------


class TestSpecIdConsistency:
    """TS-01-21: mismatched spec_id across artifacts triggers cross-file-7."""

    def test_spec_id_consistency(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # PRD has spec_id "01"; change requirements to "06".
        spec.requirements.spec_id = "06"

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        assert any(e.rule == "cross-file-7" for e in errors)


# ---------------------------------------------------------------------------
# TS-01-52: Schema validation for task group kind
# ---------------------------------------------------------------------------


class TestSchemaTaskGroup:
    """TS-01-52: task group 1 must have kind 'tests', not 'standard'."""

    def test_schema_task_group(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Group 1 must have kind "tests". Set it to "standard" to trigger error.
        spec.tasks.task_groups[0].kind = TaskGroupKind.STANDARD

        errors = validate_schema(spec)
        assert len(errors) > 0


# ---------------------------------------------------------------------------
# TS-01-53: Duplicate traceability entries
# ---------------------------------------------------------------------------


class TestDuplicateTraceability:
    """TS-01-53: duplicate (requirement_id, test_spec_id) pair."""

    def test_duplicate_traceability(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Duplicate the first traceability entry.
        first = spec.tasks.traceability[0]
        dup = TraceabilityEntry(
            requirement_id=first.requirement_id,
            test_spec_id=first.test_spec_id,
            task_id=first.task_id,
            test_path=first.test_path,
        )
        spec.tasks.traceability.append(dup)

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        assert any("duplicate" in e.message.lower() and "traceability" in e.message.lower() for e in errors)


# ---------------------------------------------------------------------------
# TS-01-E8: Unknown / additional fields rejected
# ---------------------------------------------------------------------------


class TestUnknownFields:
    """TS-01-E8: extra unknown field in requirements triggers error."""

    def test_unknown_fields(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Inject an unknown field into the requirements model.
        # Pydantic v2 by default ignores extra; the schema validator should
        # reject it when checking against JSON Schema.
        spec.requirements.__dict__["unknown_field"] = "should not be here"

        errors = validate_schema(spec)
        assert len(errors) > 0
        assert any("unknown" in e.message.lower() or "additional" in e.message.lower() for e in errors)


# ---------------------------------------------------------------------------
# TS-01-E9: Invalid EARS pattern value
# ---------------------------------------------------------------------------


class TestInvalidEarsPattern:
    """TS-01-E9: criterion with invalid ears_pattern value."""

    def test_invalid_ears_pattern(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Force an invalid pattern value onto the first criterion.
        criterion = spec.requirements.requirements[0].acceptance_criteria[0]
        # Bypass enum validation by directly setting the value.
        object.__setattr__(criterion, "ears_pattern", "invalid_pattern")

        errors = validate_schema(spec)
        assert len(errors) > 0
        assert any("ears_pattern" in e.message.lower() for e in errors)


# ---------------------------------------------------------------------------
# TS-01-E10: Incomplete spec (empty spec_id in sub-artifacts)
# ---------------------------------------------------------------------------


class TestIncompleteSpec:
    """TS-01-E10: empty spec_id sentinel triggers 'incomplete' error."""

    def test_incomplete_spec(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        # Set spec_id to empty string — the sentinel for unpopulated.
        spec.requirements.spec_id = ""
        spec.test_spec.spec_id = ""
        spec.tasks.spec_id = ""

        errors = validate_cross_file(spec)
        assert len(errors) > 0
        assert any("incomplete" in e.message.lower() for e in errors)


# ---------------------------------------------------------------------------
# Property TS-01-P2: EARS fields — required fields pass, extra fields fail
# ---------------------------------------------------------------------------


_EARS_PATTERNS = [p for p in EARSPattern]


class TestPropertyEarsFields:
    """TS-01-P2: EARS criterion field constraints per pattern."""

    @given(pattern=sampled_from(_EARS_PATTERNS))
    def test_property_ears_fields(self, pattern: EARSPattern) -> None:
        """For each EARS pattern, constructing a criterion with exactly
        the required fields should pass validation, and with extra
        fields it should fail.
        """
        # Build a criterion with the required fields for each pattern.
        base_fields: dict = {
            "id": "01-REQ-1.1",
            "ears_pattern": pattern,
            "system": "the system",
            "action": "do something",
        }

        if pattern == EARSPattern.UBIQUITOUS:
            valid_fields = {**base_fields}
            invalid_fields = {**base_fields, "trigger": "spurious trigger"}
        elif pattern == EARSPattern.EVENT_DRIVEN:
            valid_fields = {**base_fields, "trigger": "an event occurs"}
            invalid_fields = {**base_fields, "trigger": "an event occurs", "state": "spurious state"}
        elif pattern == EARSPattern.COMPLEX_EVENT:
            valid_fields = {**base_fields, "trigger": "an event", "condition": "a condition"}
            invalid_fields = {**base_fields, "trigger": "an event", "condition": "a cond", "feature": "spurious"}
        elif pattern == EARSPattern.STATE_DRIVEN:
            valid_fields = {**base_fields, "state": "a state"}
            invalid_fields = {**base_fields, "state": "a state", "trigger": "spurious trigger"}
        elif pattern == EARSPattern.UNWANTED:
            valid_fields = {**base_fields, "error_condition": "an error"}
            invalid_fields = {**base_fields, "error_condition": "an error", "feature": "spurious"}
        elif pattern == EARSPattern.OPTIONAL:
            valid_fields = {**base_fields, "feature": "a feature"}
            invalid_fields = {**base_fields, "feature": "a feature", "error_condition": "spurious"}
        else:
            pytest.fail(f"Unhandled pattern: {pattern}")

        # Valid criterion: should construct without error and pass validation.
        valid_criterion = Criterion(**valid_fields)
        valid_spec = _make_single_criterion_spec(valid_criterion)
        valid_errors = validate_schema(valid_spec)
        assert len(valid_errors) == 0, f"Valid {pattern.value} criterion produced errors: {valid_errors}"

        # Invalid criterion: extra field should cause a validation error.
        invalid_criterion = Criterion(**invalid_fields)
        invalid_spec = _make_single_criterion_spec(invalid_criterion)
        invalid_errors = validate_schema(invalid_spec)
        assert len(invalid_errors) > 0, f"Invalid {pattern.value} criterion should have errors"


def _make_single_criterion_spec(criterion: Criterion) -> Spec:
    """Build a minimal Spec containing a single criterion for validation."""
    spec = afspec.create_spec("01", "test_feature")
    req = Requirement(
        id="01-REQ-1",
        title="Test",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=[criterion],
        edge_cases=[],
    )
    spec.requirements.requirements = [req]
    return spec


# ---------------------------------------------------------------------------
# Property TS-01-P4: Schema soundness — valid spec passes schema validation
# ---------------------------------------------------------------------------


class TestPropertySchemaSoundness:
    """TS-01-P4: golden valid spec passes schema validation with 0 errors."""

    def test_property_schema_soundness(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)
        errors = validate_schema(spec)
        assert errors == []


# ---------------------------------------------------------------------------
# Property TS-01-P5: Cross-file integrity — valid spec + all refs exist
# ---------------------------------------------------------------------------


class TestPropertyCrossFileIntegrity:
    """TS-01-P5: golden spec passes cross-file checks and refs are sound."""

    def test_property_cross_file_integrity(self, valid_spec_dir: Path) -> None:
        spec = _load_golden(valid_spec_dir)

        errors = validate_cross_file(spec)
        assert errors == []

        # Additionally verify that every requirement_id referenced in
        # test_cases actually exists in requirements.
        req_ids = {c.id for r in spec.requirements.requirements for c in r.acceptance_criteria}
        req_ids |= {c.id for r in spec.requirements.requirements for c in r.edge_cases}

        for tc in spec.test_spec.test_cases:
            assert tc.requirement_id in req_ids, (
                f"Test case {tc.id} references {tc.requirement_id} which is not in requirements"
            )


# ---------------------------------------------------------------------------
# TS-01-SMOKE-3: Validate spec end-to-end (PATH-3)
# ---------------------------------------------------------------------------


class TestSmokeValidate:
    """TS-01-SMOKE-3: validate exercises both schema and cross-file checks."""

    def test_smoke_validate(self, valid_spec_dir: Path) -> None:
        """Valid spec passes validate(); mutated spec produces errors.

        No mocking of schema loading or validation engine.
        """
        # Valid spec: zero errors from combined validate()
        valid_spec = _load_golden(valid_spec_dir)
        result = afspec.validate(valid_spec)
        assert len(result.errors) == 0

        # Mutated spec: introduce a cross-file violation (dangling ref)
        invalid_spec = _load_golden(valid_spec_dir)
        bogus_case = TestCase(
            id="TS-BOGUS",
            requirement_id="99-REQ-99.1",
            kind="unit",
            description="Bogus",
        )
        invalid_spec.test_spec.test_cases.append(bogus_case)

        result = afspec.validate(invalid_spec)
        assert len(result.errors) > 0
        assert any("99-REQ-99.1" in e.message for e in result.errors)
