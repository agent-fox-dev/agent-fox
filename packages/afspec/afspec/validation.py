"""Schema and cross-file validation for spec packages."""

from __future__ import annotations

import json
import re
from typing import Any

import jsonschema
from pydantic import BaseModel

from afspec.discovery import DependencyGraph
from afspec.models import (
    EARSPattern,
    Spec,
    TaskGroup,
)
from afspec.schemas import schemas as load_schemas


class ValidationError(BaseModel):
    """A single validation error."""

    file: str = ""
    path: str = ""
    message: str = ""
    rule: str = ""
    value: Any | None = None


class ValidationWarning(BaseModel):
    """A non-blocking validation diagnostic.

    Unlike :class:`ValidationError`, warnings do not cause validation
    to fail.  They highlight potential sizing or complexity issues that
    a spec author may want to address.

    Attributes:
        message: Human-readable description of the warning.
        entity_id: Identifier of the offending entity (e.g. group ID
            or subtask ID such as ``"1"`` or ``"1.3"``).
    """

    message: str
    entity_id: str


class ValidationResult(BaseModel):
    """Structured result of spec validation.

    Combines errors and warnings into a single return value.
    ``valid`` is ``True`` when there are no errors, regardless of
    how many warnings are present.

    Attributes:
        valid: ``True`` when ``errors`` is empty.
        errors: Blocking validation errors.
        warnings: Non-blocking validation warnings (sizing, complexity).
    """

    valid: bool = True
    errors: list[ValidationError] = []
    warnings: list[ValidationWarning] = []


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
                value=err.instance if path else None,
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
# Wiring-verification semantic validation
# ---------------------------------------------------------------------------

_SMOKE_TEST_RE = re.compile(r"^TS-\w+-SMOKE-")
_STUB_AUDIT_RE = re.compile(r"stub|dead[\s_-]?code", re.IGNORECASE)

# ---------------------------------------------------------------------------
# ID format validation
# ---------------------------------------------------------------------------

_ID_PATTERNS = {
    "requirement": re.compile(r"^\w+-REQ-\d+$"),
    "criterion": re.compile(r"^\w+-REQ-\d+\.\d+$"),
    "edge_case": re.compile(r"^\w+-REQ-\d+\.E\d+$"),
    "property": re.compile(r"^\w+-PROP-\d+$"),
    "path": re.compile(r"^\w+-PATH-\d+$"),
    "error": re.compile(r"^\w+-ERR-\d+$"),
    "test_case": re.compile(r"^TS-\w+-\d+$"),
    "property_test": re.compile(r"^TS-\w+-P\d+$"),
    "edge_case_test": re.compile(r"^TS-\w+-E\d+$"),
    "smoke_test": re.compile(r"^TS-\w+-SMOKE-\d+$"),
    "subtask": re.compile(r"^\d+\.\d+$"),
    "verification": re.compile(r"^\d+\.V$"),
}


def _validate_id_formats(spec: Spec) -> list[ValidationError]:
    """Validate that all entity IDs match their expected format and spec_id prefix.

    Checks three rules:
    (a) Each ID matches its expected regex pattern.
    (b) The spec_id prefix embedded in each ID matches the artifact's ``spec_id``.
    (c) No duplicate IDs within the same entity type.
    """
    errors: list[ValidationError] = []
    spec_id = spec.requirements.spec_id

    def _check_id(
        entity_id: str,
        entity_type: str,
        file: str,
        seen: set[str],
    ) -> None:
        pattern = _ID_PATTERNS.get(entity_type)
        if pattern and not pattern.match(entity_id):
            errors.append(
                ValidationError(
                    file=file,
                    path=entity_id,
                    message=(
                        f"{entity_type} ID '{entity_id}' does not match "
                        f"expected pattern {pattern.pattern}"
                    ),
                    rule="id-format",
                )
            )
        # Check spec_id prefix where applicable
        if pattern and entity_type in (
            "requirement", "criterion", "edge_case", "property", "path", "error",
        ):
            for marker in ("-REQ-", "-PROP-", "-PATH-", "-ERR-"):
                idx = entity_id.find(marker)
                if idx > 0:
                    prefix = entity_id[:idx]
                    break
            else:
                prefix = ""
            if prefix and prefix != spec_id:
                errors.append(
                    ValidationError(
                        file=file,
                        path=entity_id,
                        message=(
                            f"{entity_type} ID '{entity_id}' has spec_id prefix "
                            f"'{prefix}' but artifact spec_id is '{spec_id}'"
                        ),
                        rule="id-format",
                    )
                )
        elif pattern and entity_type in (
            "test_case", "property_test", "edge_case_test", "smoke_test",
        ):
            # These IDs have format TS-{spec_id}-{N} or TS-{spec_id}-SMOKE-{N}
            remainder = entity_id[3:] if entity_id.startswith("TS-") else ""
            ts_prefix = ""
            for marker in ("-SMOKE-", "-P", "-E"):
                idx = remainder.find(marker)
                if idx > 0:
                    ts_prefix = remainder[:idx]
                    break
            if not ts_prefix and remainder:
                last_dash = remainder.rfind("-")
                if last_dash > 0:
                    ts_prefix = remainder[:last_dash]
            if ts_prefix and ts_prefix != spec_id:
                    errors.append(
                        ValidationError(
                            file=file,
                            path=entity_id,
                            message=(
                                f"{entity_type} ID '{entity_id}' has spec_id prefix "
                                f"'{ts_prefix}' but artifact spec_id is '{spec_id}'"
                            ),
                            rule="id-format",
                        )
                    )
        # Duplicate check
        if entity_id in seen:
            errors.append(
                ValidationError(
                    file=file,
                    path=entity_id,
                    message=(
                        f"Duplicate {entity_type} ID '{entity_id}'"
                    ),
                    rule="id-format",
                )
            )
        seen.add(entity_id)

    # Requirements
    seen_reqs: set[str] = set()
    for req in spec.requirements.requirements:
        _check_id(req.id, "requirement", "requirements.json", seen_reqs)

        seen_criteria: set[str] = set()
        for criterion in req.acceptance_criteria:
            _check_id(criterion.id, "criterion", "requirements.json", seen_criteria)

        seen_edge_cases: set[str] = set()
        for edge_case in req.edge_cases:
            _check_id(edge_case.id, "edge_case", "requirements.json", seen_edge_cases)

    # Correctness properties
    seen_props: set[str] = set()
    for prop in spec.requirements.correctness_properties:
        _check_id(prop.id, "property", "requirements.json", seen_props)

    # Execution paths
    seen_paths: set[str] = set()
    for path in spec.requirements.execution_paths:
        _check_id(path.id, "path", "requirements.json", seen_paths)

    # Error handling
    seen_errors: set[str] = set()
    for eh in spec.requirements.error_handling:
        _check_id(eh.id, "error", "requirements.json", seen_errors)

    # Test cases
    seen_tcs: set[str] = set()
    for tc in spec.test_spec.test_cases:
        _check_id(tc.id, "test_case", "test_spec.json", seen_tcs)

    # Property tests
    seen_pts: set[str] = set()
    for pt in spec.test_spec.property_tests:
        _check_id(pt.id, "property_test", "test_spec.json", seen_pts)

    # Edge case tests
    seen_ets: set[str] = set()
    for et in spec.test_spec.edge_case_tests:
        _check_id(et.id, "edge_case_test", "test_spec.json", seen_ets)

    # Smoke tests
    seen_sts: set[str] = set()
    for st in spec.test_spec.smoke_tests:
        _check_id(st.id, "smoke_test", "test_spec.json", seen_sts)

    # Subtasks and verification
    seen_subtasks: set[str] = set()
    seen_verifications: set[str] = set()
    for group in spec.tasks.task_groups:
        for subtask in group.subtasks:
            _check_id(subtask.id, "subtask", "tasks.json", seen_subtasks)
        if group.verification and group.verification.id:
            _check_id(group.verification.id, "verification", "tasks.json", seen_verifications)

    return errors


def _validate_wiring_semantics(spec: Spec) -> list[ValidationError]:
    """Validate semantic content of the wiring_verification group.

    Checks three wiring-1 sub-rules on the final task group when it has
    ``kind: wiring_verification``:

    1. At least one subtask has non-empty ``test_spec_refs``.
    2. At least one ``test_spec_ref`` matches the smoke-test ID pattern
       ``TS-{spec_id}-SMOKE-*``.
    3. At least one subtask title/details or verification check references
       a stub/dead-code audit.
    """
    errors: list[ValidationError] = []
    groups = spec.tasks.task_groups
    if not groups:
        return errors

    wiring = groups[-1]
    if wiring.kind.value != "wiring_verification":
        return errors

    idx = len(groups) - 1

    has_refs = any(st.test_spec_refs for st in wiring.subtasks)
    if not has_refs:
        errors.append(
            ValidationError(
                file="tasks.json",
                path=f"task_groups[{idx}].subtasks",
                message=(
                    "Wiring verification group has no subtask with "
                    "test_spec_refs; wiring checks must reference tests"
                ),
                rule="wiring-1",
            )
        )

    has_smoke = any(
        _SMOKE_TEST_RE.match(ref)
        for st in wiring.subtasks
        for ref in st.test_spec_refs
    )
    if not has_smoke:
        errors.append(
            ValidationError(
                file="tasks.json",
                path=f"task_groups[{idx}].subtasks",
                message=(
                    "Wiring verification group has no smoke test "
                    "reference (expected pattern TS-*-SMOKE-*)"
                ),
                rule="wiring-1",
            )
        )

    has_stub = any(
        _STUB_AUDIT_RE.search(text)
        for st in wiring.subtasks
        for text in [st.title] + list(st.details)
    )
    if not has_stub and wiring.verification:
        has_stub = any(
            _STUB_AUDIT_RE.search(c) for c in wiring.verification.checks
        )
    if not has_stub:
        errors.append(
            ValidationError(
                file="tasks.json",
                path=f"task_groups[{idx}]",
                message=(
                    "Wiring verification group has no subtask or "
                    "verification check referencing stub/dead-code audit"
                ),
                rule="wiring-1",
            )
        )

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


_NON_TERM_RE = re.compile(
    r"^-?\d+(\.\d+)?$"
    r"|^[\"'].*[\"']$"
    r"|^.$"
)


def _extract_backtick_terms(text: str) -> set[str]:
    """Extract backtick-wrapped terms, excluding non-domain-term patterns.

    Filters out pure numerics (-1, 42, 3.14), quoted strings, single
    characters, and strings longer than 80 characters — these are code
    literals, not domain terms that belong in the glossary.
    """
    raw = set(re.findall(r"`([^`]+)`", text))
    return {t for t in raw if not _NON_TERM_RE.match(t) and len(t) <= 80}


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
    all_requirement_ids = _collect_all_requirement_ids(spec)
    all_test_spec_ids = _collect_all_test_spec_ids(spec)

    # -----------------------------------------------------------------------
    # Rule 1: requirement_id references must resolve.
    # - test_cases and traceability reference criterion/edge-case IDs
    # - error_handling references requirement-level IDs
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

    all_req_and_criterion_ids = all_requirement_ids | all_criterion_ids
    for eh in spec.requirements.error_handling:
        if eh.requirement_id not in all_req_and_criterion_ids:
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

    # -----------------------------------------------------------------------
    # Rule 9: subtask requirement_refs must resolve to known
    # requirement or criterion IDs
    # -----------------------------------------------------------------------
    for group in spec.tasks.task_groups:
        for subtask in group.subtasks:
            for ref in subtask.requirement_refs:
                if ref not in all_req_and_criterion_ids:
                    errors.append(
                        ValidationError(
                            file="tasks.json",
                            path=f"task_groups.{group.id}.subtasks.{subtask.id}.requirement_refs",
                            message=(
                                f"Subtask {subtask.id} references requirement_ref "
                                f"'{ref}' which does not exist in requirements"
                            ),
                            rule="cross-file-9",
                        )
                    )

    # -----------------------------------------------------------------------
    # Rule 10: unwanted-pattern criteria must have return_contract
    # -----------------------------------------------------------------------
    for req in spec.requirements.requirements:
        for criterion in req.acceptance_criteria + req.edge_cases:
            if criterion.return_contract is not None:
                continue
            if criterion.ears_pattern == EARSPattern.UNWANTED:
                errors.append(
                    ValidationError(
                        file="requirements.json",
                        path=f"requirements.{req.id}.{criterion.id}",
                        message=(
                            f"Criterion {criterion.id} has ears_pattern 'unwanted' "
                            f"but null return_contract — error-path criteria must "
                            f"specify the caller-observable response"
                        ),
                        rule="cross-file-10",
                    )
                )

    # -----------------------------------------------------------------------
    # ID format validation
    # -----------------------------------------------------------------------
    errors.extend(_validate_id_formats(spec))

    return errors


# ---------------------------------------------------------------------------
# Cross-spec validation
# ---------------------------------------------------------------------------


def _extract_interface_contracts(spec: Spec) -> dict[str, set[str]]:
    """Extract backtick terms from criteria actions paired with return_contracts.

    Returns a dict mapping each backtick-wrapped term found in a criterion's
    ``action`` field to the set of ``return_contract`` values associated with
    criteria containing that term (only non-empty contracts are included).
    """
    contracts: dict[str, set[str]] = {}
    for req in spec.requirements.requirements:
        for criterion in req.acceptance_criteria + req.edge_cases:
            rc = criterion.return_contract
            if not rc:
                continue
            terms = _extract_backtick_terms(criterion.action)
            for term in terms:
                if term not in contracts:
                    contracts[term] = set()
                contracts[term].add(rc)
    return contracts


def validate_cross_spec(
    specs: dict[str, Spec],
    graph: DependencyGraph,
) -> list[ValidationError]:
    """Check cross-spec interface consistency rules.

    Validates five rules across all specs in the dependency graph:

    1. **cross-spec-1**: Duplicate external API symbol with different
       signature across any two specs.
    2. **cross-spec-2**: Glossary term conflict — same term defined with
       different definitions across specs.
    3. **cross-spec-3**: Dependency on unknown spec — a task dependency
       references a spec not present in *specs*.
    4. **cross-spec-4**: Interface contract mismatch — a downstream spec's
       criteria reference upstream functions with different return contracts.
    5. **cross-spec-5**: Missing boundary coverage — a downstream spec has
       no execution path referencing an actor from the upstream spec.

    Args:
        specs: Mapping of spec_id to loaded ``Spec`` objects.
        graph: The dependency graph for the spec root.

    Returns:
        A list of ``ValidationError`` values for all violations found.
    """
    errors: list[ValidationError] = []

    # ------------------------------------------------------------------
    # Check 1 (cross-spec-1): Duplicate external API symbol with
    # different signature.
    # ------------------------------------------------------------------
    # Key: (name, import_path) -> (signature, first_spec_id)
    symbol_registry: dict[tuple[str, str], tuple[str, str]] = {}
    for spec_id, spec in specs.items():
        for api in spec.requirements.external_apis:
            for sym in api.symbols:
                key = (sym.name, sym.import_path)
                if key in symbol_registry:
                    existing_sig, existing_spec = symbol_registry[key]
                    if sym.signature != existing_sig:
                        errors.append(
                            ValidationError(
                                file="requirements.json",
                                path=f"external_apis.{sym.name}",
                                message=(
                                    f"External API symbol '{sym.name}' (import: "
                                    f"'{sym.import_path}') has different signatures "
                                    f"across specs: spec {existing_spec} defines "
                                    f"'{existing_sig}', spec {spec_id} defines "
                                    f"'{sym.signature}'"
                                ),
                                rule="cross-spec-1",
                            )
                        )
                else:
                    symbol_registry[key] = (sym.signature, spec_id)

    # ------------------------------------------------------------------
    # Check 2 (cross-spec-2): Glossary term conflict.
    # ------------------------------------------------------------------
    # Collect all (term, definition, spec_id) tuples, group by term.
    term_definitions: dict[str, list[tuple[str, str]]] = {}
    for spec_id, spec in specs.items():
        for term, definition in spec.requirements.glossary.items():
            if term not in term_definitions:
                term_definitions[term] = []
            term_definitions[term].append((definition, spec_id))

    for term, defs in term_definitions.items():
        # Deduplicate definitions for comparison
        unique_defs = {d for d, _ in defs}
        if len(unique_defs) > 1:
            spec_list = ", ".join(sorted({sid for _, sid in defs}))
            errors.append(
                ValidationError(
                    file="requirements.json",
                    path=f"glossary.{term}",
                    message=(f"Glossary term '{term}' has conflicting definitions across specs: {spec_list}"),
                    rule="cross-spec-2",
                )
            )

    # ------------------------------------------------------------------
    # Check 3 (cross-spec-3): Dependency on unknown spec.
    # ------------------------------------------------------------------
    for spec_id, spec in specs.items():
        for dep in spec.tasks.dependencies:
            if dep.depends_on_spec and dep.depends_on_spec not in specs:
                errors.append(
                    ValidationError(
                        file="tasks.json",
                        path=f"dependencies.{dep.depends_on_spec}",
                        message=(
                            f"Spec {spec_id} depends on spec "
                            f"'{dep.depends_on_spec}' which is not present "
                            f"in the loaded specs"
                        ),
                        rule="cross-spec-3",
                    )
                )

    # ------------------------------------------------------------------
    # Check 4 (cross-spec-4): Interface contract mismatch along
    # dependency edges.
    # ------------------------------------------------------------------
    seen_edges: set[tuple[str, str]] = set()
    for edge in graph.edges():
        pair = (edge.from_spec, edge.to_spec)
        if pair in seen_edges:
            continue
        seen_edges.add(pair)

        upstream = specs.get(edge.from_spec)
        downstream = specs.get(edge.to_spec)
        if upstream is None or downstream is None:
            continue

        up_contracts = _extract_interface_contracts(upstream)
        down_contracts = _extract_interface_contracts(downstream)

        for term in sorted(set(up_contracts) & set(down_contracts)):
            up_rc = up_contracts[term]
            down_rc = down_contracts[term]
            if not up_rc & down_rc:
                errors.append(
                    ValidationError(
                        file="requirements.json",
                        path="requirements.return_contract",
                        message=(
                            f"Interface contract mismatch for '{term}' "
                            f"between spec {edge.from_spec} and spec "
                            f"{edge.to_spec}: upstream declares "
                            f"{sorted(up_rc)}, downstream assumes "
                            f"{sorted(down_rc)}"
                        ),
                        rule="cross-spec-4",
                    )
                )

    # ------------------------------------------------------------------
    # Check 5 (cross-spec-5): Downstream spec must have at least one
    # execution path with a step referencing an upstream actor.
    # ------------------------------------------------------------------
    seen_edges_5: set[tuple[str, str]] = set()
    for edge in graph.edges():
        pair = (edge.from_spec, edge.to_spec)
        if pair in seen_edges_5:
            continue
        seen_edges_5.add(pair)

        upstream = specs.get(edge.from_spec)
        downstream = specs.get(edge.to_spec)
        if upstream is None or downstream is None:
            continue

        upstream_actors: set[str] = set()
        for path in upstream.requirements.execution_paths:
            for step in path.steps:
                if step.actor:
                    upstream_actors.add(step.actor.lower())

        if not upstream_actors:
            continue

        found = False
        for path in downstream.requirements.execution_paths:
            for step in path.steps:
                if step.actor and step.actor.lower() in upstream_actors:
                    found = True
                    break
            if found:
                break

        if not found:
            errors.append(
                ValidationError(
                    file="requirements.json",
                    path="execution_paths",
                    message=(
                        f"Spec {edge.to_spec} depends on spec "
                        f"{edge.from_spec} but has no execution path "
                        f"with a step referencing an actor from spec "
                        f"{edge.from_spec}"
                    ),
                    rule="cross-spec-5",
                )
            )

    return errors


# ---------------------------------------------------------------------------
# Warning checks — non-blocking sizing / complexity diagnostics
# ---------------------------------------------------------------------------

# Maximum total test_spec_refs across all subtasks in a single group.
_MAX_GROUP_TEST_SPEC_REFS = 15

# Maximum number of non-verification subtasks per group.
_MAX_SUBTASKS_PER_GROUP = 6

# Maximum test_spec_refs for a single subtask.
_MAX_SUBTASK_TEST_SPEC_REFS = 8


def _check_group_test_spec_refs(group: TaskGroup) -> list[ValidationWarning]:
    """Warn if a group's total ``test_spec_refs`` count exceeds the ceiling.

    Sums ``len(subtask.test_spec_refs)`` for all non-verification subtasks.
    Applies to all ``kind`` values.
    """
    total = sum(len(subtask.test_spec_refs) for subtask in group.subtasks)
    if total > _MAX_GROUP_TEST_SPEC_REFS:
        return [
            ValidationWarning(
                message=(f"Group {group.id} has {total} test_spec_refs (limit {_MAX_GROUP_TEST_SPEC_REFS})"),
                entity_id=str(group.id),
            )
        ]
    return []


def _check_group_subtask_count(group: TaskGroup) -> list[ValidationWarning]:
    """Warn if a group has more than the allowed number of subtasks.

    The verification subtask (stored separately in ``group.verification``)
    is excluded from the count.  Only ``group.subtasks`` are counted.
    """
    count = len(group.subtasks)
    if count > _MAX_SUBTASKS_PER_GROUP:
        return [
            ValidationWarning(
                message=(
                    f"Group {group.id} has {count} subtasks (limit {_MAX_SUBTASKS_PER_GROUP}, excluding verification)"
                ),
                entity_id=str(group.id),
            )
        ]
    return []


def _check_subtask_overload(group: TaskGroup) -> list[ValidationWarning]:
    """Warn if any individual subtask references too many ``test_spec_refs``.

    Subtasks with missing or empty ``test_spec_refs`` count as zero.
    """
    warnings: list[ValidationWarning] = []
    for subtask in group.subtasks:
        count = len(subtask.test_spec_refs)
        if count > _MAX_SUBTASK_TEST_SPEC_REFS:
            warnings.append(
                ValidationWarning(
                    message=(
                        f"Subtask {subtask.id} references {count} test_spec_refs (limit {_MAX_SUBTASK_TEST_SPEC_REFS})"
                    ),
                    entity_id=subtask.id,
                )
            )
    return warnings


_ERROR_PATH_RE = re.compile(
    r"\b(?:error|fail|reject|denied|deny|invalid|unauthorized|"
    r"unauthorised|forbidden|timeout|not found)\b",
    re.IGNORECASE,
)


def _check_error_path_return_contract(spec: Spec) -> list[ValidationWarning]:
    """Warn when error-path criteria have a null ``return_contract``.

    Scans acceptance criteria and edge cases for action text containing
    error-indicating keywords or a non-empty ``error_condition`` field.
    If such a criterion has ``return_contract is None``, a warning is
    emitted because the error response format is likely unspecified.
    """
    warnings: list[ValidationWarning] = []
    for req in spec.requirements.requirements:
        for criterion in req.acceptance_criteria + req.edge_cases:
            if criterion.return_contract is not None:
                continue
            has_error_keywords = _ERROR_PATH_RE.search(criterion.action)
            has_error_condition = bool(
                criterion.error_condition and criterion.error_condition.strip()
            )
            if has_error_keywords or has_error_condition:
                warnings.append(
                    ValidationWarning(
                        message=(
                            f"Criterion {criterion.id} describes an error "
                            f"path but has null return_contract — the "
                            f"caller-observable error response is unspecified"
                        ),
                        entity_id=criterion.id,
                    )
                )
    return warnings


# ---------------------------------------------------------------------------
# Vague language detector
# ---------------------------------------------------------------------------

_VAGUE_WORDS_RE = re.compile(
    r"\b(?:appropriate|properly|correctly|reasonable|relevant|"
    r"adequate|suitable|as needed|if necessary|etc)\b",
    re.IGNORECASE,
)


def _check_vague_language(spec: Spec) -> list[ValidationWarning]:
    """Warn when criterion fields or error_handling behaviors contain vague language.

    Scans ``action``, ``trigger``, ``condition``, and ``error_condition``
    on all acceptance criteria and edge cases, plus ``behavior`` on all
    error_handling entries.
    """
    warnings: list[ValidationWarning] = []
    _CHECKED_FIELDS = ["action", "trigger", "condition", "error_condition"]

    for req in spec.requirements.requirements:
        for criterion in req.acceptance_criteria + req.edge_cases:
            for field_name in _CHECKED_FIELDS:
                val = getattr(criterion, field_name, None)
                if val is None:
                    continue
                match = _VAGUE_WORDS_RE.search(val)
                if match:
                    warnings.append(
                        ValidationWarning(
                            message=(
                                f"Criterion {criterion.id} field '{field_name}' "
                                f"contains vague language: '{match.group()}'"
                            ),
                            entity_id=criterion.id,
                        )
                    )

    for eh in spec.requirements.error_handling:
        match = _VAGUE_WORDS_RE.search(eh.behavior)
        if match:
            warnings.append(
                ValidationWarning(
                    message=(
                        f"Error handling {eh.id} field 'behavior' "
                        f"contains vague language: '{match.group()}'"
                    ),
                    entity_id=eh.id,
                )
            )

    return warnings


# ---------------------------------------------------------------------------
# Scope limit warning
# ---------------------------------------------------------------------------


def _check_scope_limit(spec: Spec) -> list[ValidationWarning]:
    """Warn when the spec has more than 10 requirements.

    Large specs are harder to implement and review in a single session.
    """
    count = len(spec.requirements.requirements)
    if count > 10:
        return [
            ValidationWarning(
                message=(
                    f"Spec has {count} requirements (recommended maximum "
                    f"is 10) — consider splitting"
                ),
                entity_id=spec.requirements.spec_id,
            )
        ]
    return []


# ---------------------------------------------------------------------------
# Combined validation
# ---------------------------------------------------------------------------


def validate(spec: Spec) -> ValidationResult:
    """Run both schema and cross-file validation.

    Returns a :class:`ValidationResult` containing all errors and
    warnings.  ``result.valid`` is ``True`` when ``result.errors`` is
    empty, regardless of how many warnings are present.
    """
    errors: list[ValidationError] = []
    errors.extend(validate_schema(spec))
    errors.extend(validate_cross_file(spec))
    errors.extend(_validate_wiring_semantics(spec))

    warnings: list[ValidationWarning] = []
    for group in spec.tasks.task_groups:
        warnings.extend(_check_group_test_spec_refs(group))
        warnings.extend(_check_group_subtask_count(group))
        warnings.extend(_check_subtask_overload(group))
    warnings.extend(_check_error_path_return_contract(spec))
    warnings.extend(_check_vague_language(spec))
    warnings.extend(_check_scope_limit(spec))

    return ValidationResult(
        valid=len(errors) == 0,
        errors=errors,
        warnings=warnings,
    )


def validate_structured(spec: Spec) -> dict[str, Any]:
    """Run full validation and return a structured dict for CLI consumption.

    Wraps :func:`validate` and reshapes the :class:`ValidationResult`
    into a dict with categorised errors and warnings.  Each error carries
    a ``category`` key (``"schema"`` or ``"integrity"``) plus
    format-specific fields so the CLI can emit the result directly
    without rebuilding the JSON shape from low-level validation
    functions.

    Returns a dict with keys:

    - ``valid`` — ``True`` when there are no errors.
    - ``errors`` — list of dicts, each with ``category``, ``message``,
      and category-specific keys (``artifact``/``path`` for schema
      errors; ``check``/``requirement_id`` for integrity errors).
    - ``warnings`` — list of dicts with ``category``, ``message``, and
      ``entity_id`` (present only when non-empty).
    """
    result = validate(spec)

    errors: list[dict[str, Any]] = []
    for err in result.errors:
        if err.rule.startswith("cross-file") or err.rule.startswith("cross-spec"):
            error_dict: dict[str, Any] = {
                "category": "integrity",
                "check": err.rule,
                "message": err.message,
            }
            req_match = re.search(r"'([A-Z][\w.-]+)'", err.message)
            if req_match:
                error_dict["requirement_id"] = req_match.group(1)
        else:
            error_dict = {
                "category": "schema",
                "artifact": err.file,
                "message": err.message,
            }
            if err.path:
                error_dict["path"] = err.path
            if err.value is not None:
                error_dict["value"] = err.value
        errors.append(error_dict)

    warning_dicts: list[dict[str, Any]] = []
    for w in result.warnings:
        warning_dicts.append(
            {
                "category": "warning",
                "message": w.message,
                "entity_id": w.entity_id,
            }
        )

    output: dict[str, Any] = {
        "valid": result.valid,
        "errors": errors,
    }
    if warning_dicts:
        output["warnings"] = warning_dicts
    return output
