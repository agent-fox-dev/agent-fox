"""Schema and cross-file validation for spec packages."""

from __future__ import annotations

import json
import re
from typing import Any

import jsonschema
from pydantic import BaseModel

from afspec.models import (
    EARSPattern,
    Spec,
)
from afspec.schemas import schemas as load_schemas


class ValidationError(BaseModel):
    """A single validation error."""

    file: str = ""
    path: str = ""
    message: str = ""
    rule: str = ""


# ---------------------------------------------------------------------------
# EARS pattern field constraints
# ---------------------------------------------------------------------------

# For each EARS pattern, the set of pattern-specific fields that MUST be present
_EARS_REQUIRED_FIELDS: dict[str, set[str]] = {
    "ubiquitous": set(),
    "event_driven": {"trigger"},
    "complex_event": {"trigger", "condition"},
    "state_driven": {"state"},
    "unwanted": {"error_condition"},
    "optional": {"feature"},
}

# All pattern-specific fields
_ALL_PATTERN_FIELDS = {"trigger", "condition", "error_condition", "state", "feature"}


# ---------------------------------------------------------------------------
# Schema validation
# ---------------------------------------------------------------------------


def _model_to_dict(model: Any) -> dict[str, Any]:
    """Convert a Pydantic model to a dict for JSON Schema validation.

    Uses by_alias to handle $schema correctly. Excludes None for
    Criterion pattern-specific fields to match the omitempty behaviour.
    """
    from afspec.io import _serialize_model

    return _serialize_model(model)


def _validate_against_schema(
    data: dict[str, Any],
    schema: dict[str, Any],
    file_name: str,
) -> list[ValidationError]:
    """Validate *data* against a JSON Schema and return errors."""
    errors: list[ValidationError] = []
    validator_cls = jsonschema.Draft202012Validator
    validator = validator_cls(schema)
    for err in validator.iter_errors(data):
        path = ".".join(str(p) for p in err.absolute_path) if err.absolute_path else ""
        errors.append(
            ValidationError(
                file=file_name,
                path=path,
                message=err.message,
                rule="schema",
            )
        )
    return errors


def _validate_ears_constraints(spec: Spec) -> list[ValidationError]:
    """Validate EARS pattern field constraints on all criteria.

    For each criterion, check that only the fields required by its
    ears_pattern are present (non-None) among the pattern-specific fields.
    """
    errors: list[ValidationError] = []

    for req in spec.requirements.requirements:
        for criteria_list, list_name in [
            (req.acceptance_criteria, "acceptance_criteria"),
            (req.edge_cases, "edge_cases"),
        ]:
            for idx, criterion in enumerate(criteria_list):
                pattern = criterion.ears_pattern
                if isinstance(pattern, EARSPattern):
                    pattern_str = pattern.value
                else:
                    pattern_str = str(pattern)

                if pattern_str not in _EARS_REQUIRED_FIELDS:
                    errors.append(
                        ValidationError(
                            file="requirements.json",
                            path=f"requirements.{req.id}.{list_name}[{idx}].ears_pattern",
                            message=f"Invalid ears_pattern value: {pattern_str!r}",
                            rule="schema",
                        )
                    )
                    continue

                required = _EARS_REQUIRED_FIELDS[pattern_str]
                forbidden = _ALL_PATTERN_FIELDS - required

                # Check that required fields are present (non-None)
                for field in required:
                    val = getattr(criterion, field, None)
                    if val is None:
                        errors.append(
                            ValidationError(
                                file="requirements.json",
                                path=f"requirements.{req.id}.{list_name}[{idx}].{field}",
                                message=(
                                    f"Criterion {criterion.id}: pattern {pattern_str!r} "
                                    f"requires field '{field}' but it is missing"
                                ),
                                rule="schema",
                            )
                        )

                # Check that forbidden fields are NOT present (must be None)
                for field in forbidden:
                    val = getattr(criterion, field, None)
                    if val is not None:
                        errors.append(
                            ValidationError(
                                file="requirements.json",
                                path=f"requirements.{req.id}.{list_name}[{idx}].{field}",
                                message=(
                                    f"Criterion {criterion.id}: pattern {pattern_str!r} "
                                    f"must not have field '{field}' but it is set to {val!r}"
                                ),
                                rule="schema",
                            )
                        )

    return errors


def _validate_task_group_structure(spec: Spec) -> list[ValidationError]:
    """Validate task group structural rules.

    - Group 1 (first group) must have kind 'tests'.
    - The final group must have kind 'wiring_verification'.
    """
    errors: list[ValidationError] = []
    groups = spec.tasks.task_groups

    if not groups:
        return errors

    # First group must be kind "tests"
    if groups[0].kind.value != "tests":
        errors.append(
            ValidationError(
                file="tasks.json",
                path="task_groups[0].kind",
                message=(f"Task group 1 must have kind 'tests', got '{groups[0].kind.value}'"),
                rule="schema",
            )
        )

    # Last group must be kind "wiring_verification"
    if groups[-1].kind.value != "wiring_verification":
        errors.append(
            ValidationError(
                file="tasks.json",
                path=f"task_groups[{len(groups) - 1}].kind",
                message=(f"Final task group must have kind 'wiring_verification', got '{groups[-1].kind.value}'"),
                rule="schema",
            )
        )

    return errors


def validate_schema(spec: Spec) -> list[ValidationError]:
    """Validate spec artifacts against bundled JSON Schemas.

    Validates each JSON artifact against its bundled JSON Schema and
    additionally checks EARS criterion pattern field constraints and
    task group structural rules. Returns a list of all violations.
    """
    all_schemas = load_schemas()
    errors: list[ValidationError] = []

    # Parse all schemas
    schema_map: dict[str, dict[str, Any]] = {}
    for name, raw in all_schemas.items():
        schema_map[name] = json.loads(raw)

    # Validate PRD frontmatter
    fm_data = _model_to_dict(spec.prd.frontmatter)
    errors.extend(_validate_against_schema(fm_data, schema_map["prd-frontmatter.v1.json"], "prd.md"))

    # Validate requirements.json
    req_data = _model_to_dict(spec.requirements)
    # Inject any __dict__ extras that wouldn't normally serialize
    # (for testing unknown fields — TS-01-E8)
    for key, val in spec.requirements.__dict__.items():
        if key.startswith("_"):
            continue
        if key not in req_data and key not in type(spec.requirements).model_fields:
            req_data[key] = val
    errors.extend(_validate_against_schema(req_data, schema_map["requirements.v1.json"], "requirements.json"))

    # Validate test_spec.json
    ts_data = _model_to_dict(spec.test_spec)
    errors.extend(_validate_against_schema(ts_data, schema_map["test_spec.v1.json"], "test_spec.json"))

    # Validate tasks.json
    tasks_data = _model_to_dict(spec.tasks)
    errors.extend(_validate_against_schema(tasks_data, schema_map["tasks.v1.json"], "tasks.json"))

    # Validate EARS pattern constraints (in-memory check)
    errors.extend(_validate_ears_constraints(spec))

    # Validate task group structural rules
    errors.extend(_validate_task_group_structure(spec))

    return errors


# ---------------------------------------------------------------------------
# Cross-file integrity validation
# ---------------------------------------------------------------------------


def _collect_all_criterion_ids(spec: Spec) -> set[str]:
    """Collect all acceptance criterion and edge case IDs from requirements."""
    ids: set[str] = set()
    for req in spec.requirements.requirements:
        for c in req.acceptance_criteria:
            ids.add(c.id)
        for c in req.edge_cases:
            ids.add(c.id)
    return ids


def _collect_all_requirement_ids(spec: Spec) -> set[str]:
    """Collect all requirement-level IDs (not criterion IDs)."""
    return {r.id for r in spec.requirements.requirements}


def _collect_all_test_spec_ids(spec: Spec) -> set[str]:
    """Collect all test spec entry IDs."""
    ids: set[str] = set()
    for tc in spec.test_spec.test_cases:
        ids.add(tc.id)
    for pt in spec.test_spec.property_tests:
        ids.add(pt.id)
    for et in spec.test_spec.edge_case_tests:
        ids.add(et.id)
    for st in spec.test_spec.smoke_tests:
        ids.add(st.id)
    return ids


def _extract_backtick_terms(text: str) -> set[str]:
    """Extract all backtick-wrapped terms from a text string."""
    return set(re.findall(r"`([^`]+)`", text))


def validate_cross_file(spec: Spec) -> list[ValidationError]:
    """Check cross-file integrity rules.

    Checks all eight cross-file integrity rules defined in the spec format.
    Returns a list of ValidationError values listing all violations.

    If any sub-artifact has an empty spec_id (the sentinel for an
    unpopulated artifact), returns a ValidationError with message
    containing 'incomplete' and does not proceed to rule checks.
    """
    errors: list[ValidationError] = []

    # Check artifact completeness: empty spec_id sentinel
    incomplete_artifacts = []
    if not spec.requirements.spec_id:
        incomplete_artifacts.append("requirements")
    if not spec.test_spec.spec_id:
        incomplete_artifacts.append("test_spec")
    if not spec.tasks.spec_id:
        incomplete_artifacts.append("tasks")

    if incomplete_artifacts:
        errors.append(
            ValidationError(
                file="",
                path="",
                message=(
                    f"Spec is incomplete: the following artifact(s) have empty "
                    f"spec_id: {', '.join(incomplete_artifacts)}"
                ),
                rule="completeness",
            )
        )
        return errors

    # Collect all IDs for reference checking
    all_criterion_ids = _collect_all_criterion_ids(spec)
    all_test_spec_ids = _collect_all_test_spec_ids(spec)

    # -----------------------------------------------------------------------
    # Rule 1: requirement_id references in test cases, traceability, and
    # error_handling must exist as criterion or edge case IDs
    # -----------------------------------------------------------------------
    for tc in spec.test_spec.test_cases:
        if tc.requirement_id not in all_criterion_ids:
            errors.append(
                ValidationError(
                    file="test_spec.json",
                    path=f"test_cases.{tc.id}.requirement_id",
                    message=(
                        f"Test case {tc.id} references requirement_id "
                        f"'{tc.requirement_id}' which does not exist in requirements"
                    ),
                    rule="cross-file-1",
                )
            )

    for entry in spec.tasks.traceability:
        if entry.requirement_id not in all_criterion_ids:
            errors.append(
                ValidationError(
                    file="tasks.json",
                    path=f"traceability.{entry.requirement_id}",
                    message=(
                        f"Traceability entry references requirement_id "
                        f"'{entry.requirement_id}' which does not exist in requirements"
                    ),
                    rule="cross-file-1",
                )
            )

    for eh in spec.requirements.error_handling:
        if eh.requirement_id not in all_criterion_ids:
            errors.append(
                ValidationError(
                    file="requirements.json",
                    path=f"error_handling.{eh.id}.requirement_id",
                    message=(
                        f"Error handling entry {eh.id} references requirement_id "
                        f"'{eh.requirement_id}' which does not exist in requirements"
                    ),
                    rule="cross-file-1",
                )
            )

    # -----------------------------------------------------------------------
    # Rule 2: every acceptance criterion and edge case must have a test case.
    # A requirement with no acceptance criteria and no edge cases is also
    # flagged as having no test coverage.
    # -----------------------------------------------------------------------
    tested_requirement_ids = {tc.requirement_id for tc in spec.test_spec.test_cases}
    tested_edge_case_ids = {et.requirement_id for et in spec.test_spec.edge_case_tests}
    all_tested = tested_requirement_ids | tested_edge_case_ids

    for req in spec.requirements.requirements:
        if not req.acceptance_criteria and not req.edge_cases:
            # Requirement has no criteria at all — flag as lacking coverage
            errors.append(
                ValidationError(
                    file="test_spec.json",
                    path="test_cases",
                    message=(
                        f"Requirement '{req.id}' has no acceptance criteria "
                        f"or edge cases and therefore no test coverage"
                    ),
                    rule="cross-file-2",
                )
            )
            continue

        for criterion in req.acceptance_criteria:
            if criterion.id not in all_tested:
                errors.append(
                    ValidationError(
                        file="test_spec.json",
                        path="test_cases",
                        message=(
                            f"Acceptance criterion '{criterion.id}' in requirement "
                            f"'{req.id}' has no corresponding test case"
                        ),
                        rule="cross-file-2",
                    )
                )
        for edge_case in req.edge_cases:
            if edge_case.id not in all_tested:
                errors.append(
                    ValidationError(
                        file="test_spec.json",
                        path="edge_case_tests",
                        message=(
                            f"Edge case '{edge_case.id}' in requirement '{req.id}' has no corresponding test case"
                        ),
                        rule="cross-file-2",
                    )
                )

    # -----------------------------------------------------------------------
    # Rule 3: every correctness property must have a property test
    # -----------------------------------------------------------------------
    tested_properties = {pt.property_id for pt in spec.test_spec.property_tests}
    for prop in spec.requirements.correctness_properties:
        if prop.id not in tested_properties:
            errors.append(
                ValidationError(
                    file="test_spec.json",
                    path="property_tests",
                    message=(f"Correctness property '{prop.id}' has no corresponding property test"),
                    rule="cross-file-3",
                )
            )

    # -----------------------------------------------------------------------
    # Rule 4: every execution path must have a smoke test
    # -----------------------------------------------------------------------
    tested_paths = {st.execution_path_id for st in spec.test_spec.smoke_tests}
    for path in spec.requirements.execution_paths:
        if path.id not in tested_paths:
            errors.append(
                ValidationError(
                    file="test_spec.json",
                    path="smoke_tests",
                    message=(f"Execution path '{path.id}' has no corresponding smoke test"),
                    rule="cross-file-4",
                )
            )

    # -----------------------------------------------------------------------
    # Rule 5: test_spec_id references in traceability and subtask
    # test_spec_refs must exist in test_spec
    # -----------------------------------------------------------------------
    for entry in spec.tasks.traceability:
        if entry.test_spec_id not in all_test_spec_ids:
            errors.append(
                ValidationError(
                    file="tasks.json",
                    path=f"traceability.{entry.test_spec_id}",
                    message=(
                        f"Traceability entry references test_spec_id "
                        f"'{entry.test_spec_id}' which does not exist in test_spec"
                    ),
                    rule="cross-file-5",
                )
            )

    for group in spec.tasks.task_groups:
        for subtask in group.subtasks:
            for ref in subtask.test_spec_refs:
                if ref not in all_test_spec_ids:
                    errors.append(
                        ValidationError(
                            file="tasks.json",
                            path=f"task_groups.{group.id}.subtasks.{subtask.id}.test_spec_refs",
                            message=(
                                f"Subtask {subtask.id} references test_spec_id "
                                f"'{ref}' which does not exist in test_spec"
                            ),
                            rule="cross-file-5",
                        )
                    )

    # -----------------------------------------------------------------------
    # Rule 6: glossary cross-check — backtick terms in checked fields must
    # have glossary entries
    # -----------------------------------------------------------------------
    glossary_terms = set(spec.requirements.glossary.keys())
    _CHECKED_FIELDS = [
        "action",
        "trigger",
        "condition",
        "error_condition",
        "state",
        "feature",
        "for_any",
        "invariant",
    ]

    for req in spec.requirements.requirements:
        for criteria_list in [req.acceptance_criteria, req.edge_cases]:
            for criterion in criteria_list:
                for field_name in _CHECKED_FIELDS:
                    val = getattr(criterion, field_name, None)
                    if val is None:
                        continue
                    terms = _extract_backtick_terms(val)
                    for term in terms:
                        if term not in glossary_terms:
                            errors.append(
                                ValidationError(
                                    file="requirements.json",
                                    path=f"requirements.{req.id}.{field_name}",
                                    message=(
                                        f"Term '{term}' is used in backticks in "
                                        f"criterion {criterion.id} field '{field_name}' "
                                        f"but has no glossary entry"
                                    ),
                                    rule="cross-file-6",
                                )
                            )

    # Also check correctness properties
    for prop in spec.requirements.correctness_properties:
        for field_name in ["for_any", "invariant"]:
            val = getattr(prop, field_name, None)
            if val is None:
                continue
            terms = _extract_backtick_terms(val)
            for term in terms:
                if term not in glossary_terms:
                    errors.append(
                        ValidationError(
                            file="requirements.json",
                            path=f"correctness_properties.{prop.id}.{field_name}",
                            message=(
                                f"Term '{term}' is used in backticks in "
                                f"correctness property {prop.id} field '{field_name}' "
                                f"but has no glossary entry"
                            ),
                            rule="cross-file-6",
                        )
                    )

    # -----------------------------------------------------------------------
    # Rule 7: spec_id and spec_name must be identical across all artifacts
    # -----------------------------------------------------------------------
    prd_id = spec.prd.frontmatter.spec_id
    prd_name = spec.prd.frontmatter.spec_name

    for artifact_name, artifact_id, artifact_name_val in [
        ("requirements.json", spec.requirements.spec_id, spec.requirements.spec_name),
        ("test_spec.json", spec.test_spec.spec_id, spec.test_spec.spec_name),
        ("tasks.json", spec.tasks.spec_id, spec.tasks.spec_name),
    ]:
        if artifact_id != prd_id:
            errors.append(
                ValidationError(
                    file=artifact_name,
                    path="spec_id",
                    message=(f"spec_id mismatch: prd.md has '{prd_id}' but {artifact_name} has '{artifact_id}'"),
                    rule="cross-file-7",
                )
            )
        if artifact_name_val != prd_name:
            errors.append(
                ValidationError(
                    file=artifact_name,
                    path="spec_name",
                    message=(
                        f"spec_name mismatch: prd.md has '{prd_name}' but {artifact_name} has '{artifact_name_val}'"
                    ),
                    rule="cross-file-7",
                )
            )

    # -----------------------------------------------------------------------
    # Rule 8: no duplicate (requirement_id, test_spec_id) pairs in
    # traceability
    # -----------------------------------------------------------------------
    seen_pairs: set[tuple[str, str]] = set()
    for entry in spec.tasks.traceability:
        pair = (entry.requirement_id, entry.test_spec_id)
        if pair in seen_pairs:
            errors.append(
                ValidationError(
                    file="tasks.json",
                    path="traceability",
                    message=(
                        f"Duplicate traceability pair: "
                        f"(requirement_id={entry.requirement_id!r}, "
                        f"test_spec_id={entry.test_spec_id!r})"
                    ),
                    rule="cross-file-8",
                )
            )
        seen_pairs.add(pair)

    return errors


# ---------------------------------------------------------------------------
# Combined validation
# ---------------------------------------------------------------------------


def validate(spec: Spec) -> list[ValidationError]:
    """Run both schema and cross-file validation.

    Returns the combined list of all ValidationError values from both
    schema validation and cross-file integrity checks.
    """
    errors: list[ValidationError] = []
    errors.extend(validate_schema(spec))
    errors.extend(validate_cross_file(spec))
    return errors
