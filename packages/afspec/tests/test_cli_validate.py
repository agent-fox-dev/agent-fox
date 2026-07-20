"""Tests for CLI display of warnings and backward-compatibility of existing rules.

TS-08-23: CLI displays warning messages and prints valid: true with exit code 0
           when validation produces only warnings.
TS-08-24: CLI displays no warning messages and prints valid: true with exit code 0
           when validation produces no errors and no warnings.
TS-08-25: CLI displays both errors and warnings and prints valid: false with
           non-zero exit code when ValidationErrors are present alongside warnings.
TS-08-E9: When the spec file cannot be parsed, CLI reports a parse error as a
           ValidationError and exits with a non-zero code.
TS-08-26: Existing rule requiring groups[0].kind == 'tests' continues to be
           enforced after adding ValidationWarning logic.
TS-08-27: Multiple consecutive task groups with kind: tests are allowed without
           emitting a ValidationError.

These tests are in RED PHASE:
- CLI tests (TS-08-23, TS-08-25) will fail because the current CLI does not
  emit ValidationWarning messages.
- Backward-compat tests (TS-08-26, TS-08-27) will fail because validate()
  currently returns a plain list, not a structured ValidationResult with
  .valid / .errors / .warnings attributes.

CRITICAL NOTE (reviewer finding): All in-memory fixtures include a final
kind: wiring_verification group to avoid triggering the pre-existing
_validate_task_group_structure error that fires when the last group is
not wiring_verification.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from click.testing import CliRunner

from afspec.models import (
    Criterion,
    EARSPattern,
    EdgeCaseTest,
    ExecutionPath,
    PathStep,
    PRDDocument,
    PRDFrontmatter,
    Requirement,
    Requirements,
    SmokeTest,
    Spec,
    Subtask,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
)
from afspec.validation import validate

# ---------------------------------------------------------------------------
# Lazy import for spec.cli — it depends on agentfox/agentspec which may
# not always be available in isolation.  pytest.importorskip makes this
# explicit and produces a clear skip message.
# ---------------------------------------------------------------------------
spec_cli = pytest.importorskip("spec.cli", reason="spec.cli not importable")
cli_main = spec_cli.main


# ---------------------------------------------------------------------------
# Helpers — create spec fixture directories on disk for CLI tests
# ---------------------------------------------------------------------------

_PRD_TEMPLATE = """\
---
spec_id: "{spec_id}"
spec_name: "{spec_name}"
title: "CLI Validate Test Spec"
status: "draft"
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
owner: "test-author"
source: "https://example.com"
supersedes: []
tags: ["test"]
intent_hash: null
schema_version: 1
---
# CLI Validate Test Spec

## Intent

Fixture spec for CLI validation tests.

## Goals

- Validate CLI warning output.

## Non-goals

- None.
"""


def _write_spec_fixture(
    base_dir: Path,
    *,
    spec_id: str = "CV",
    spec_name: str = "cli_validate_test",
    dir_name: str = "01_cli_validate_test",
    num_refs: int = 1,
    first_group_kind: str = "tests",
    malformed_artifact: str | None = None,
) -> Path:
    """Write a complete spec fixture to disk for CLI integration testing.

    Parameters
    ----------
    base_dir:
        Parent directory (acts as the --spec-dir argument).
    spec_id:
        The spec_id used across all artifacts.
    spec_name:
        The spec_name used across all artifacts.
    dir_name:
        Name of the spec subdirectory (e.g. ``01_cli_validate_test``).
    num_refs:
        Total number of test_spec_refs across all subtasks in group 1.
    first_group_kind:
        The ``kind`` of the first task group (``"tests"`` or ``"standard"``).
    malformed_artifact:
        If set, write invalid JSON to this artifact file (e.g. ``"test_spec.json"``).

    Returns the path to the spec directory.
    """
    spec_dir = base_dir / dir_name
    spec_dir.mkdir(parents=True, exist_ok=True)

    # -- prd.md ---------------------------------------------------------------
    (spec_dir / "prd.md").write_text(_PRD_TEMPLATE.format(spec_id=spec_id, spec_name=spec_name))

    # -- Generate matching criteria, test cases, and refs ---------------------
    criteria: list[dict] = []
    test_cases: list[dict] = []
    traceability: list[dict] = []
    ref_ids: list[str] = []

    for i in range(1, num_refs + 1):
        cid = f"{spec_id}-REQ-1.{i}"
        tsid = f"TS-{spec_id}-{i}"
        criteria.append(
            {
                "id": cid,
                "ears_pattern": "ubiquitous",
                "system": "the system",
                "action": f"performs action {i}",
            }
        )
        test_cases.append(
            {
                "id": tsid,
                "requirement_id": cid,
                "kind": "unit",
                "description": f"Test case {i}",
                "preconditions": [],
                "input": {},
                "expected": {},
                "assertion_pseudocode": f"assert action_{i}()",
            }
        )
        ref_ids.append(tsid)
        traceability.append(
            {
                "requirement_id": cid,
                "test_spec_id": tsid,
                "task_id": f"1.{i}" if i <= 10 else f"1.{min(i, num_refs)}",
                "test_path": None,
            }
        )

    # -- Edge case test entry for cross-file rule 2 (edge cases need tests) ---
    edge_case_cid = f"{spec_id}-REQ-1.E1"
    edge_case_tsid = f"TS-{spec_id}-E1"

    # -- requirements.json ----------------------------------------------------
    requirements = {
        "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
        "spec_id": spec_id,
        "spec_name": spec_name,
        "schema_version": 1,
        "introduction": "Test spec for CLI validation.",
        "glossary": {"system": "The test system."},
        "requirements": [
            {
                "id": f"{spec_id}-REQ-1",
                "title": "Test requirement",
                "user_story": {
                    "role": "developer",
                    "goal": "test CLI validation",
                    "benefit": "quality",
                },
                "acceptance_criteria": criteria,
                "edge_cases": [
                    {
                        "id": edge_case_cid,
                        "ears_pattern": "unwanted",
                        "error_condition": "the system encounters invalid input",
                        "system": "the system",
                        "action": "reports an error",
                        "return_contract": "returns an error value",
                    }
                ],
            }
        ],
        "correctness_properties": [
            {
                "id": f"{spec_id}-PROP-1",
                "title": "Correctness property",
                "for_any": "valid input",
                "invariant": "output is correct",
                "validates": [criteria[0]["id"]] if criteria else [],
            }
        ],
        "execution_paths": [
            {
                "id": f"{spec_id}-PATH-1",
                "title": "Main execution path",
                "steps": [
                    {"actor": "user", "action": "invokes the system"},
                    {"actor": "system", "action": "processes the request"},
                ],
            }
        ],
        "error_handling": [
            {
                "id": f"{spec_id}-ERR-1",
                "condition": "Invalid input",
                "behavior": "Report error",
                "requirement_id": edge_case_cid,
            }
        ],
    }

    if malformed_artifact == "requirements.json":
        (spec_dir / "requirements.json").write_text("{invalid json!!!")
    else:
        (spec_dir / "requirements.json").write_text(json.dumps(requirements, indent=2))

    # -- test_spec.json -------------------------------------------------------
    test_spec = {
        "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
        "spec_id": spec_id,
        "spec_name": spec_name,
        "schema_version": 1,
        "test_cases": test_cases,
        "property_tests": [
            {
                "id": f"TS-{spec_id}-P1",
                "property_id": f"{spec_id}-PROP-1",
                "validates": [criteria[0]["id"]] if criteria else [],
                "description": "Property test",
                "for_any_strategy": "valid inputs",
                "invariant_check": "output matches expectation",
            }
        ],
        "edge_case_tests": [
            {
                "id": edge_case_tsid,
                "requirement_id": edge_case_cid,
                "kind": "unit",
                "description": "Edge case test",
                "preconditions": [],
                "input": {},
                "expected": {},
                "assertion_pseudocode": "assert error_raised()",
            }
        ],
        "smoke_tests": [
            {
                "id": f"TS-{spec_id}-SMOKE-1",
                "execution_path_id": f"{spec_id}-PATH-1",
                "description": "Smoke test",
                "trigger": "invoke system",
                "real_components": ["system"],
                "mockable": [],
                "expected_effects": ["system processes successfully"],
            }
        ],
        "coverage": {
            "requirements_covered": [c["id"] for c in criteria] + [edge_case_cid],
            "properties_covered": [f"{spec_id}-PROP-1"],
            "paths_covered": [f"{spec_id}-PATH-1"],
            "gaps": [],
        },
    }

    if malformed_artifact == "test_spec.json":
        (spec_dir / "test_spec.json").write_text("{invalid json!!!")
    else:
        (spec_dir / "test_spec.json").write_text(json.dumps(test_spec, indent=2))

    # -- tasks.json -----------------------------------------------------------
    # Split refs across subtasks (max ~10 per subtask for realism)
    subtasks: list[dict] = []
    chunk_size = max(1, min(10, num_refs))
    subtask_idx = 1
    for start in range(0, num_refs, chunk_size):
        end = min(start + chunk_size, num_refs)
        chunk_refs = ref_ids[start:end]
        subtasks.append(
            {
                "id": f"1.{subtask_idx}",
                "title": f"Subtask {subtask_idx}",
                "details": [f"Implement part {subtask_idx}"],
                "test_spec_refs": chunk_refs,
                "requirement_refs": [f"{spec_id}-REQ-1"],
                "state": "pending",
                "optional": False,
            }
        )
        subtask_idx += 1

    # Fix traceability task_ids to match actual subtask split
    fixed_traceability = []
    ref_to_subtask: dict[str, str] = {}
    for s in subtasks:
        for ref in s["test_spec_refs"]:
            ref_to_subtask[ref] = s["id"]
    for entry in traceability:
        fixed_traceability.append(
            {
                "requirement_id": entry["requirement_id"],
                "test_spec_id": entry["test_spec_id"],
                "task_id": ref_to_subtask.get(entry["test_spec_id"], "1.1"),
                "test_path": None,
            }
        )
    # Add edge case traceability
    fixed_traceability.append(
        {
            "requirement_id": edge_case_cid,
            "test_spec_id": edge_case_tsid,
            "task_id": "1.1",
            "test_path": None,
        }
    )

    task_groups = [
        {
            "id": 1,
            "kind": first_group_kind,
            "title": "Tests group" if first_group_kind == "tests" else "Standard group",
            "subtasks": subtasks,
            "verification": {
                "id": "1.V",
                "checks": ["All tests pass"],
            },
        },
        {
            "id": 2,
            "kind": "wiring_verification",
            "title": "Wiring verification",
            "subtasks": [
                {
                    "id": "2.1",
                    "title": "Trace execution paths and stub/dead-code audit",
                    "details": ["Check end-to-end wiring"],
                    "test_spec_refs": [f"TS-{spec_id}-SMOKE-1"],
                    "requirement_refs": [f"{spec_id}-REQ-1"],
                    "state": "pending",
                    "optional": False,
                }
            ],
            "verification": {
                "id": "2.V",
                "checks": ["Wiring verified"],
            },
        },
    ]
    # Add smoke test traceability
    fixed_traceability.append(
        {
            "requirement_id": criteria[0]["id"] if criteria else f"{spec_id}-REQ-1.1",
            "test_spec_id": f"TS-{spec_id}-SMOKE-1",
            "task_id": "2.1",
            "test_path": None,
        }
    )
    # Add property test traceability
    fixed_traceability.append(
        {
            "requirement_id": criteria[0]["id"] if criteria else f"{spec_id}-REQ-1.1",
            "test_spec_id": f"TS-{spec_id}-P1",
            "task_id": "1.1",
            "test_path": None,
        }
    )

    tasks = {
        "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
        "spec_id": spec_id,
        "spec_name": spec_name,
        "schema_version": 1,
        "test_commands": {
            "spec_tests": "pytest -q tests/",
            "all_tests": "pytest -q",
            "linter": "ruff check",
        },
        "dependencies": [],
        "task_groups": task_groups,
        "traceability": fixed_traceability,
    }

    if malformed_artifact == "tasks.json":
        (spec_dir / "tasks.json").write_text("{invalid json!!!")
    else:
        (spec_dir / "tasks.json").write_text(json.dumps(tasks, indent=2))

    return spec_dir


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def runner() -> CliRunner:
    """Click CLI test runner."""
    return CliRunner()


@pytest.fixture
def oversized_spec_dir(tmp_path: Path) -> Path:
    """Spec fixture with 20 total test_spec_refs (exceeds 15) and no errors.

    This spec should trigger a ValidationWarning for oversized
    test_spec_refs but no ValidationError.
    """
    return _write_spec_fixture(
        tmp_path,
        spec_id="OV",
        spec_name="oversized_test",
        dir_name="01_oversized_test",
        num_refs=20,
        first_group_kind="tests",
    )


@pytest.fixture
def clean_spec_dir(tmp_path: Path) -> Path:
    """Spec fixture with all groups within thresholds and no errors.

    This spec should trigger no ValidationWarning and no ValidationError.
    """
    return _write_spec_fixture(
        tmp_path,
        spec_id="CL",
        spec_name="clean_test",
        dir_name="01_clean_test",
        num_refs=3,
        first_group_kind="tests",
    )


@pytest.fixture
def error_and_warning_spec_dir(tmp_path: Path) -> Path:
    """Spec fixture with structural error AND oversized test_spec_refs.

    The first group has ``kind: standard`` (structural error) and 20
    total test_spec_refs (warning trigger).
    """
    return _write_spec_fixture(
        tmp_path,
        spec_id="EW",
        spec_name="error_warning_test",
        dir_name="01_error_warning_test",
        num_refs=20,
        first_group_kind="standard",
    )


@pytest.fixture
def malformed_spec_dir(tmp_path: Path) -> Path:
    """Spec fixture with a malformed JSON artifact (invalid test_spec.json)."""
    return _write_spec_fixture(
        tmp_path,
        spec_id="MF",
        spec_name="malformed_test",
        dir_name="01_malformed_test",
        num_refs=3,
        malformed_artifact="requirements.json",
    )


# ---------------------------------------------------------------------------
# TS-08-23: CLI warnings-only output — valid: true, exit code 0
# ---------------------------------------------------------------------------


class TestCLIWarningsOnly:
    """TS-08-23: CLI shows warnings and valid: true when only warnings present."""

    def test_exit_code_zero(self, runner: CliRunner, oversized_spec_dir: Path) -> None:
        """Exit code is 0 when validation produces only warnings."""
        spec_parent = oversized_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_oversized_test"],
        )
        assert result.exit_code == 0, (
            f"Expected exit code 0 for warnings-only, got {result.exit_code}.\noutput: {result.output}"
        )

    def test_valid_true_in_output(self, runner: CliRunner, oversized_spec_dir: Path) -> None:
        """Output contains valid: true (case-insensitive)."""
        spec_parent = oversized_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_oversized_test"],
        )
        output_lower = result.output.lower()
        assert "valid" in output_lower, f"Expected 'valid' in output, got:\n{result.output}"
        # Check that valid is true (JSON or text format)
        assert "true" in output_lower, f"Expected valid: true in output, got:\n{result.output}"

    def test_warning_messages_in_output(self, runner: CliRunner, oversized_spec_dir: Path) -> None:
        """Output contains at least one warning message.

        This test will FAIL in RED phase because the current CLI
        does not emit ValidationWarning messages.
        """
        spec_parent = oversized_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_oversized_test"],
        )
        output_lower = result.output.lower()
        assert "warning" in output_lower, f"Expected at least one warning message in output, got:\n{result.output}"


# ---------------------------------------------------------------------------
# TS-08-24: CLI clean spec — valid: true, no warnings, exit code 0
# ---------------------------------------------------------------------------


class TestCLICleanSpec:
    """TS-08-24: CLI shows valid: true with no warnings for a clean spec."""

    def test_exit_code_zero(self, runner: CliRunner, clean_spec_dir: Path) -> None:
        """Exit code is 0 for a clean spec with no errors or warnings."""
        spec_parent = clean_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_clean_test"],
        )
        assert result.exit_code == 0, (
            f"Expected exit code 0 for clean spec, got {result.exit_code}.\noutput: {result.output}"
        )

    def test_valid_true_in_output(self, runner: CliRunner, clean_spec_dir: Path) -> None:
        """Output contains valid: true (case-insensitive)."""
        spec_parent = clean_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_clean_test"],
        )
        output_lower = result.output.lower()
        assert "valid" in output_lower
        assert "true" in output_lower

    def test_no_warning_in_output(self, runner: CliRunner, clean_spec_dir: Path) -> None:
        """Output contains no warning messages."""
        spec_parent = clean_spec_dir.parent
        result = runner.invoke(
            cli_main,
            ["--spec-dir", str(spec_parent), "--quiet", "validate", "01_clean_test"],
        )
        output_lower = result.output.lower()
        assert "warning" not in output_lower, f"Expected no warning messages for clean spec, got:\n{result.output}"


# ---------------------------------------------------------------------------
# TS-08-25: CLI errors+warnings — valid: false, non-zero exit code
# ---------------------------------------------------------------------------


class TestCLIErrorsAndWarnings:
    """TS-08-25: CLI shows errors+warnings and valid: false with non-zero exit."""

    def test_non_zero_exit_code(self, runner: CliRunner, error_and_warning_spec_dir: Path) -> None:
        """Exit code is non-zero when both errors and warnings are present."""
        spec_parent = error_and_warning_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_error_warning_test",
            ],
        )
        assert result.exit_code != 0, f"Expected non-zero exit code, got {result.exit_code}.\noutput: {result.output}"

    def test_valid_false_in_output(self, runner: CliRunner, error_and_warning_spec_dir: Path) -> None:
        """Output contains valid: false (case-insensitive)."""
        spec_parent = error_and_warning_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_error_warning_test",
            ],
        )
        output_lower = result.output.lower()
        assert "valid" in output_lower
        assert "false" in output_lower

    def test_error_messages_in_output(self, runner: CliRunner, error_and_warning_spec_dir: Path) -> None:
        """Output contains at least one error message."""
        spec_parent = error_and_warning_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_error_warning_test",
            ],
        )
        output_lower = result.output.lower()
        assert "error" in output_lower, f"Expected error messages in output, got:\n{result.output}"

    def test_warning_messages_in_output(self, runner: CliRunner, error_and_warning_spec_dir: Path) -> None:
        """Output contains at least one warning message.

        This test will FAIL in RED phase because the current CLI
        does not emit ValidationWarning messages.
        """
        spec_parent = error_and_warning_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_error_warning_test",
            ],
        )
        output_lower = result.output.lower()
        assert "warning" in output_lower, f"Expected at least one warning message in output, got:\n{result.output}"


# ---------------------------------------------------------------------------
# TS-08-E9: CLI malformed spec — parse error as ValidationError, non-zero exit
# ---------------------------------------------------------------------------


class TestCLIMalformedSpec:
    """TS-08-E9: CLI reports parse error as ValidationError, no warnings."""

    def test_non_zero_exit_code(self, runner: CliRunner, malformed_spec_dir: Path) -> None:
        """Exit code is non-zero for a malformed spec."""
        spec_parent = malformed_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_malformed_test",
            ],
        )
        assert result.exit_code != 0, (
            f"Expected non-zero exit code for malformed spec, got {result.exit_code}.\noutput: {result.output}"
        )

    def test_error_message_in_output(self, runner: CliRunner, malformed_spec_dir: Path) -> None:
        """Output contains an error or parse failure message."""
        spec_parent = malformed_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_malformed_test",
            ],
        )
        output_lower = result.output.lower()
        has_parse = "parse" in output_lower
        has_error = "error" in output_lower
        has_cannot_read = "cannot read" in output_lower
        assert has_parse or has_error or has_cannot_read, (
            f"Expected parse/error message in output, got:\n{result.output}"
        )

    def test_no_warning_in_output(self, runner: CliRunner, malformed_spec_dir: Path) -> None:
        """No warning messages for a parse failure — only errors."""
        spec_parent = malformed_spec_dir.parent
        result = runner.invoke(
            cli_main,
            [
                "--spec-dir",
                str(spec_parent),
                "--quiet",
                "validate",
                "01_malformed_test",
            ],
        )
        output_lower = result.output.lower()
        assert "warning" not in output_lower, f"Expected no warning messages for malformed spec, got:\n{result.output}"


# ---------------------------------------------------------------------------
# TS-08-26: Backward-compat — first group must be kind: tests
# ---------------------------------------------------------------------------


def _build_spec_first_group_standard() -> Spec:
    """Build a spec where groups[0].kind == 'standard' (invalid).

    This triggers the pre-existing validation rule requiring the first
    group to have kind: tests.  Includes a wiring_verification group
    as the final group.
    """
    cid = "BC-REQ-1.1"
    tsid = "TS-BC-1"
    edge_cid = "BC-REQ-1.E1"
    edge_tsid = "TS-BC-E1"

    criteria = [
        Criterion(
            id=cid,
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="the system",
            action="performs action",
        )
    ]

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="BC",
                spec_name="backward_compat",
                title="Backward Compat Test",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Backward-compatibility test spec.",
        ),
        requirements=Requirements(
            spec_id="BC",
            spec_name="backward_compat",
            introduction="Backward-compatibility test spec.",
            requirements=[
                Requirement(
                    id="BC-REQ-1",
                    title="Test requirement",
                    user_story=UserStory(role="dev", goal="test", benefit="coverage"),
                    acceptance_criteria=criteria,
                    edge_cases=[
                        Criterion(
                            id=edge_cid,
                            ears_pattern=EARSPattern.UNWANTED,
                            error_condition="invalid input",
                            system="the system",
                            action="raises error",
                        )
                    ],
                )
            ],
            execution_paths=[
                ExecutionPath(id="BC-PATH-1", title="Main path", steps=[PathStep(actor="System", action="Run")])
            ],
        ),
        test_spec=TestSpec(
            spec_id="BC",
            spec_name="backward_compat",
            test_cases=[
                TestCase(
                    id=tsid,
                    requirement_id=cid,
                    kind="unit",
                    description="Test case",
                )
            ],
            edge_case_tests=[
                EdgeCaseTest(
                    id=edge_tsid,
                    requirement_id=edge_cid,
                    kind="unit",
                    description="Edge case test",
                )
            ],
            smoke_tests=[SmokeTest(id="TS-BC-SMOKE-1", execution_path_id="BC-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="BC",
            spec_name="backward_compat",
            task_groups=[
                TaskGroup(
                    id=1,
                    kind=TaskGroupKind.STANDARD,  # INVALID: should be tests
                    title="Standard group",
                    subtasks=[
                        Subtask(
                            id="1.1",
                            title="Subtask 1",
                            test_spec_refs=[tsid],
                            requirement_refs=["BC-REQ-1"],
                        )
                    ],
                    verification=VerificationSubtask(id="1.V", checks=["pass"]),
                ),
                TaskGroup(
                    id=2,
                    kind=TaskGroupKind.WIRING_VERIFICATION,
                    title="Wiring verification",
                    subtasks=[
                        Subtask(
                            id="2.1",
                            title="Trace execution paths and stub/dead-code audit",
                            test_spec_refs=["TS-BC-SMOKE-1"],
                            requirement_refs=["BC-REQ-1"],
                        )
                    ],
                    verification=VerificationSubtask(id="2.V", checks=["done"]),
                ),
            ],
            traceability=[
                TraceabilityEntry(
                    requirement_id=cid,
                    test_spec_id=tsid,
                    task_id="1.1",
                ),
                TraceabilityEntry(
                    requirement_id=edge_cid,
                    test_spec_id=edge_tsid,
                    task_id="1.1",
                ),
            ],
        ),
    )


class TestBackwardCompatFirstGroupKind:
    """TS-08-26: groups[0].kind must equal 'tests' — rule still enforced."""

    def test_valid_false_when_first_group_not_tests(self) -> None:
        """validate() returns valid=False when first group is kind: standard."""
        spec = _build_spec_first_group_standard()
        result = validate(spec)
        assert result.valid is False

    def test_error_mentions_kind_or_tests(self) -> None:
        """At least one error mentions 'kind' or 'tests'."""
        spec = _build_spec_first_group_standard()
        result = validate(spec)
        assert len(result.errors) >= 1
        error_texts = [str(e).lower() for e in result.errors]
        assert any("kind" in text or "tests" in text for text in error_texts), (
            f"Expected error mentioning kind/tests, got: {result.errors}"
        )


# ---------------------------------------------------------------------------
# TS-08-27: Backward-compat — multiple consecutive kind: tests allowed
# ---------------------------------------------------------------------------


def _build_spec_with_consecutive_test_groups() -> Spec:
    """Build a spec with two consecutive kind: tests groups.

    This should NOT produce a ValidationError — multiple consecutive
    test groups are permitted.  Includes a wiring_verification group
    as the final group.
    """
    cid1 = "MT-REQ-1.1"
    tsid1 = "TS-MT-1"
    cid2 = "MT-REQ-2.1"
    tsid2 = "TS-MT-2"
    edge_cid = "MT-REQ-1.E1"
    edge_tsid = "TS-MT-E1"

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="MT",
                spec_name="multi_tests",
                title="Multi Tests",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Multiple test groups spec.",
        ),
        requirements=Requirements(
            spec_id="MT",
            spec_name="multi_tests",
            introduction="Multiple test groups spec.",
            requirements=[
                Requirement(
                    id="MT-REQ-1",
                    title="First requirement",
                    user_story=UserStory(role="dev", goal="test", benefit="coverage"),
                    acceptance_criteria=[
                        Criterion(
                            id=cid1,
                            ears_pattern=EARSPattern.UBIQUITOUS,
                            system="the system",
                            action="first action",
                        )
                    ],
                    edge_cases=[
                        Criterion(
                            id=edge_cid,
                            ears_pattern=EARSPattern.UNWANTED,
                            error_condition="error state",
                            system="the system",
                            action="reports error",
                        )
                    ],
                ),
                Requirement(
                    id="MT-REQ-2",
                    title="Second requirement",
                    user_story=UserStory(role="dev", goal="test", benefit="coverage"),
                    acceptance_criteria=[
                        Criterion(
                            id=cid2,
                            ears_pattern=EARSPattern.UBIQUITOUS,
                            system="the system",
                            action="second action",
                        )
                    ],
                ),
            ],
            execution_paths=[
                ExecutionPath(id="MT-PATH-1", title="Main path", steps=[PathStep(actor="System", action="Run")])
            ],
        ),
        test_spec=TestSpec(
            spec_id="MT",
            spec_name="multi_tests",
            test_cases=[
                TestCase(
                    id=tsid1,
                    requirement_id=cid1,
                    kind="unit",
                    description="Test case 1",
                ),
                TestCase(
                    id=tsid2,
                    requirement_id=cid2,
                    kind="unit",
                    description="Test case 2",
                ),
            ],
            edge_case_tests=[
                EdgeCaseTest(
                    id=edge_tsid,
                    requirement_id=edge_cid,
                    kind="unit",
                    description="Edge case test",
                )
            ],
            smoke_tests=[SmokeTest(id="TS-MT-SMOKE-1", execution_path_id="MT-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="MT",
            spec_name="multi_tests",
            task_groups=[
                TaskGroup(
                    id=1,
                    kind=TaskGroupKind.TESTS,
                    title="First test group",
                    subtasks=[
                        Subtask(
                            id="1.1",
                            title="Subtask 1",
                            test_spec_refs=[tsid1],
                            requirement_refs=["MT-REQ-1"],
                        )
                    ],
                    verification=VerificationSubtask(id="1.V", checks=["pass"]),
                ),
                TaskGroup(
                    id=2,
                    kind=TaskGroupKind.TESTS,
                    title="Second test group",
                    subtasks=[
                        Subtask(
                            id="2.1",
                            title="Subtask 2",
                            test_spec_refs=[tsid2],
                            requirement_refs=["MT-REQ-2"],
                        )
                    ],
                    verification=VerificationSubtask(id="2.V", checks=["pass"]),
                ),
                TaskGroup(
                    id=3,
                    kind=TaskGroupKind.STANDARD,
                    title="Implementation group",
                    subtasks=[
                        Subtask(
                            id="3.1",
                            title="Implement feature",
                            requirement_refs=["MT-REQ-1"],
                        )
                    ],
                    verification=VerificationSubtask(id="3.V", checks=["pass"]),
                ),
                TaskGroup(
                    id=4,
                    kind=TaskGroupKind.WIRING_VERIFICATION,
                    title="Wiring verification",
                    subtasks=[
                        Subtask(
                            id="4.1",
                            title="Trace execution paths and stub/dead-code audit",
                            test_spec_refs=["TS-MT-SMOKE-1"],
                            requirement_refs=["MT-REQ-1"],
                        )
                    ],
                    verification=VerificationSubtask(id="4.V", checks=["done"]),
                ),
            ],
            traceability=[
                TraceabilityEntry(
                    requirement_id=cid1,
                    test_spec_id=tsid1,
                    task_id="1.1",
                ),
                TraceabilityEntry(
                    requirement_id=cid2,
                    test_spec_id=tsid2,
                    task_id="2.1",
                ),
                TraceabilityEntry(
                    requirement_id=edge_cid,
                    test_spec_id=edge_tsid,
                    task_id="1.1",
                ),
            ],
        ),
    )


class TestBackwardCompatMultipleTestGroups:
    """TS-08-27: multiple consecutive kind: tests groups are permitted."""

    def test_no_error_about_multiple_test_groups(self) -> None:
        """No ValidationError about multiple or consecutive test groups."""
        spec = _build_spec_with_consecutive_test_groups()
        result = validate(spec)
        error_texts = [str(e).lower() for e in result.errors]
        assert not any("multiple" in text and "tests" in text for text in error_texts), (
            f"Unexpected error about multiple test groups: {result.errors}"
        )
        assert not any("consecutive" in text for text in error_texts), (
            f"Unexpected error about consecutive groups: {result.errors}"
        )

    def test_no_error_about_kind_tests(self) -> None:
        """No ValidationError complaining about kind: tests for group 2."""
        spec = _build_spec_with_consecutive_test_groups()
        result = validate(spec)
        # The only kind-related error should be absent (groups 1 & 2 are
        # both kind: tests, which is valid).
        error_texts = [str(e).lower() for e in result.errors]
        kind_errors = [text for text in error_texts if "kind" in text and "group 2" in text]
        assert len(kind_errors) == 0, f"Unexpected kind error for group 2: {kind_errors}"
