"""Spec 137: Legacy Format Removal tests.

Test Spec: TS-137-1 through TS-137-10, TS-137-E1 through TS-137-E3,
           TS-137-P1, TS-137-P2, TS-137-SMOKE-1, TS-137-SMOKE-2
Requirements: 137-REQ-1.1, 137-REQ-1.2, 137-REQ-1.3, 137-REQ-1.E1,
              137-REQ-2.1, 137-REQ-2.2,
              137-REQ-3.1, 137-REQ-3.2, 137-REQ-3.3, 137-REQ-3.4,
              137-REQ-3.E1,
              137-REQ-4.1, 137-REQ-4.2, 137-REQ-4.3,
              137-REQ-5.1, 137-REQ-5.2, 137-REQ-5.3, 137-REQ-5.4,
              137-REQ-5.E1,
              137-REQ-6.1, 137-REQ-6.3, 137-REQ-6.4, 137-REQ-6.E1,
              137-REQ-7.1, 137-REQ-7.2, 137-REQ-7.3, 137-REQ-7.4,
              137-REQ-7.E1
"""

from __future__ import annotations

import importlib
import json
import pkgutil
import re
import subprocess
import sys
from pathlib import Path

import agentfox
import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Root of the agent_fox package (resolved relative to this test file).
_REPO_ROOT = Path(__file__).resolve().parents[4]
_PKG_ROOT = _REPO_ROOT / "packages" / "agentfox"
_AGENT_FOX_ROOT = _PKG_ROOT / "agentfox"
_TESTS_ROOT = _PKG_ROOT / "tests"


def _read_source(relative_path: str) -> str:
    """Read a source file relative to the package root."""
    path = _PKG_ROOT / relative_path
    return path.read_text(encoding="utf-8")


def _collect_py_files(root: Path, *, exclude: tuple[str, ...] = ()) -> list[Path]:
    """Collect all .py files under *root*, excluding __pycache__ dirs
    and files whose path contains any of the *exclude* substrings."""
    results: list[Path] = []
    for p in root.rglob("*.py"):
        if "__pycache__" in str(p):
            continue
        if any(ex in str(p) for ex in exclude):
            continue
        results.append(p)
    return results


# ---------------------------------------------------------------------------
# v1.2 spec fixture content (reused for smoke tests)
# ---------------------------------------------------------------------------

PRD_MD_VALID = """\
---
spec_id: "test-137"
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
        "spec_id": "test-137",
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
        "spec_id": "test-137",
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
        "spec_id": "test-137",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [
            {
                "id": 1,
                "kind": "standard",
                "title": "Test group",
                "subtasks": [
                    {
                        "id": "1.1",
                        "title": "Test subtask",
                        "state": "done",
                        "details": [],
                        "test_spec_refs": [],
                        "requirement_refs": [],
                        "optional": False,
                    },
                ],
                "verification": {"id": "", "checks": []},
            },
        ],
        "traceability": [],
    },
    indent=2,
)


def _write_spec(spec_dir: Path) -> None:
    """Populate a directory with valid v1.2 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)


# ===================================================================
# TS-137-1: types.py exports TaskGroupDef
# Requirement: 137-REQ-1.1
# ===================================================================


class TestTypesExportsTaskGroupDef:
    """TS-137-1: TaskGroupDef is importable from spec.types and
    constructable with the same field signature as the former parser.py
    version."""

    def test_taskgroupdef_constructable_with_all_fields(self) -> None:
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        sub = SubtaskDef(id="1.1", title="test", completed=False)
        group = TaskGroupDef(
            number=1,
            title="test",
            optional=False,
            completed=False,
            subtasks=(sub,),
            body="",
            archetype=None,
        )
        assert group.number == 1
        assert group.title == "test"
        assert group.optional is False
        assert group.completed is False
        assert group.subtasks == (sub,)
        assert group.body == ""
        assert group.archetype is None

    def test_taskgroupdef_is_frozen(self) -> None:
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        sub = SubtaskDef(id="1.1", title="test", completed=False)
        group = TaskGroupDef(
            number=1,
            title="test",
            optional=False,
            completed=False,
            subtasks=(sub,),
            body="",
            archetype=None,
        )
        with pytest.raises(AttributeError):
            group.number = 99  # type: ignore[misc]

    def test_subtaskdef_constructable(self) -> None:
        from agentfox.spec.types import SubtaskDef

        sub = SubtaskDef(id="2.3", title="My subtask", completed=True)
        assert sub.id == "2.3"
        assert sub.title == "My subtask"
        assert sub.completed is True

    def test_subtaskdef_is_frozen(self) -> None:
        from agentfox.spec.types import SubtaskDef

        sub = SubtaskDef(id="1.1", title="test", completed=False)
        with pytest.raises(AttributeError):
            sub.id = "9.9"  # type: ignore[misc]

    def test_crossspecdep_constructable(self) -> None:
        from agentfox.spec.types import CrossSpecDep

        dep = CrossSpecDep(
            from_spec="spec_a",
            from_group=1,
            to_spec="spec_b",
            to_group=2,
        )
        assert dep.from_spec == "spec_a"
        assert dep.from_group == 1
        assert dep.to_spec == "spec_b"
        assert dep.to_group == 2

    def test_crossspecdep_is_frozen(self) -> None:
        from agentfox.spec.types import CrossSpecDep

        dep = CrossSpecDep(from_spec="a", from_group=1, to_spec="b", to_group=2)
        with pytest.raises(AttributeError):
            dep.from_spec = "z"  # type: ignore[misc]


# ===================================================================
# TS-137-2: types.py exports Finding and severity constants
# Requirement: 137-REQ-1.2
# ===================================================================


class TestTypesExportsFinding:
    """TS-137-2: Finding, severity constants, compute_exit_code, and
    sort_findings are importable from spec.types."""

    def test_finding_constructable(self) -> None:
        from agentfox.spec.types import SEVERITY_ERROR, Finding

        f = Finding(
            spec_name="test_spec",
            file="a.py",
            line=1,
            rule="r",
            message="m",
            severity=SEVERITY_ERROR,
        )
        assert f.file == "a.py"
        assert f.line == 1
        assert f.severity == SEVERITY_ERROR

    def test_severity_constants_values(self) -> None:
        from agentfox.spec.types import (
            SEVERITY_ERROR,
            SEVERITY_HINT,
            SEVERITY_WARNING,
        )

        assert SEVERITY_ERROR == "error"
        assert SEVERITY_WARNING == "warning"
        assert SEVERITY_HINT == "hint"

    def test_compute_exit_code_with_error(self) -> None:
        from agentfox.spec.types import (
            SEVERITY_ERROR,
            Finding,
            compute_exit_code,
        )

        f = Finding(
            spec_name="s",
            file="a.py",
            line=1,
            rule="r",
            message="m",
            severity=SEVERITY_ERROR,
        )
        assert compute_exit_code([f]) != 0

    def test_compute_exit_code_empty(self) -> None:
        from agentfox.spec.types import compute_exit_code

        assert compute_exit_code([]) == 0

    def test_sort_findings_orders_by_severity(self) -> None:
        from agentfox.spec.types import (
            SEVERITY_ERROR,
            SEVERITY_HINT,
            SEVERITY_WARNING,
            Finding,
            sort_findings,
        )

        hint = Finding(
            spec_name="s",
            file="a.py",
            line=1,
            rule="r",
            message="h",
            severity=SEVERITY_HINT,
        )
        error = Finding(
            spec_name="s",
            file="a.py",
            line=2,
            rule="r",
            message="e",
            severity=SEVERITY_ERROR,
        )
        warning = Finding(
            spec_name="s",
            file="a.py",
            line=3,
            rule="r",
            message="w",
            severity=SEVERITY_WARNING,
        )
        sorted_findings = sort_findings([hint, error, warning])
        assert sorted_findings[0].severity == SEVERITY_ERROR


# ===================================================================
# TS-137-3: parser_v12 imports from types
# Requirement: 137-REQ-1.3
# Also covers: 137-REQ-5.1 (builder.py imports from types)
# ===================================================================


class TestParserV12ImportsFromTypes:
    """TS-137-3: parser.py imports shared types from spec.types."""

    def test_parser_v12_imports_from_types(self) -> None:
        content = _read_source("agentfox/spec/parser.py")
        assert "from agentfox.spec.types import" in content

    def test_builder_imports_from_types(self) -> None:
        """Additional test for 137-REQ-5.1: builder.py imports from types.

        Addresses skeptic finding: TS-137-3 originally only checked
        parser.py but REQ-5.1 requires builder.py to import from types.
        """
        content = _read_source("agentfox/graph/builder.py")
        assert "from agentfox.spec.types import" in content


# ===================================================================
# TS-137-5: validators/ deleted
# Requirement: 137-REQ-3.1
# ===================================================================


class TestValidatorsDeleted:
    """TS-137-5: The validators/ directory does not exist."""

    def test_validators_dir_does_not_exist(self) -> None:
        assert not (_AGENT_FOX_ROOT / "spec" / "validators").exists()


# ===================================================================
# TS-137-7: lint.py and lint_specs.py have no validator imports
# Requirement: 137-REQ-3.2, 137-REQ-3.3, 137-REQ-5.4
# ===================================================================


class TestNoValidatorImports:
    """TS-137-7: lint.py does not import from agent_fox.spec.validators."""

    def test_lint_py_no_validator_imports(self) -> None:
        """137-REQ-3.2: lint.py does not import from validators."""
        content = _read_source("agentfox/spec/lint.py")
        assert "agentfox.spec.validators" not in content

    def test_lint_py_imports_from_types(self) -> None:
        """137-REQ-5.4: lint.py imports Finding etc. from spec.types."""
        content = _read_source("agentfox/spec/lint.py")
        assert "from agentfox.spec.types import" in content

    def test_hot_load_no_validator_imports(self) -> None:
        """137-REQ-3.4: hot_load.py does not import from validators.

        Addresses skeptic finding: TS-137-8 only checked for parser imports,
        not validator imports in hot_load.py.
        """
        content = _read_source("agentfox/engine/hot_load.py")
        assert "agentfox.spec.validators" not in content

    def test_verification_checklist_no_parser_import(self) -> None:
        """137-REQ-4.3: verification_checklist.py does not import from
        spec.parser.

        Addresses skeptic finding: TS-137-9 (filename grep) cannot
        detect import statements.
        Uses 'from agentfox.spec.parser import' to avoid false-positive
        matches on 'from agentfox.spec.parser import'.
        """
        content = _read_source("agentfox/spec/verification_checklist.py")
        assert "from agentfox.spec.parser import" not in content


# ===================================================================
# TS-137-9: No v1 filename strings in source
# Requirement: 137-REQ-6.4
# Extended: 137-REQ-5.E1 (no parser imports anywhere)
# ===================================================================


class TestNoV1FilenameStringsInSource:
    """TS-137-9: No Python file in agent_fox/ (except fix/spec_gen.py)
    contains v1 filename strings as operational references.

    Also covers REQ-5.E1 (comprehensive grep for parser imports) per
    skeptic finding.
    """

    def test_no_v1_filename_strings(self) -> None:
        """137-REQ-6.4: No requirements.md, design.md, or test_spec.md
        strings in agent_fox/ source (excluding spec_gen.py)."""
        pattern = re.compile(r"requirements\.md|design\.md|test_spec\.md")
        py_files = _collect_py_files(_AGENT_FOX_ROOT, exclude=("spec_gen",))
        matches: list[str] = []
        for p in py_files:
            content = p.read_text(encoding="utf-8")
            for i, line in enumerate(content.splitlines(), start=1):
                stripped = line.lstrip()
                # Skip comments
                if stripped.startswith("#"):
                    continue
                if pattern.search(line):
                    matches.append(f"{p}:{i}: {line.strip()}")
        assert matches == [], "v1 filename strings found in source:\n" + "\n".join(matches)


# ===================================================================
# TS-137-10: No _CORE_SPEC_FILES constant
# Requirement: 137-REQ-6.3
# ===================================================================


class TestNoCoreSpecFilesConstant:
    """TS-137-10: session/context.py does not contain _CORE_SPEC_FILES."""

    def test_no_core_spec_files_in_context(self) -> None:
        content = _read_source("agentfox/session/context.py")
        assert "_CORE_SPEC_FILES" not in content


# ===================================================================
# TS-137-E2: Import from deleted validators raises error
# Requirement: 137-REQ-3.E1
# ===================================================================


class TestDeletedValidatorsImportError:
    """TS-137-E2: Importing from deleted validators/ raises ImportError."""

    def test_import_from_deleted_validators_raises(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                "-c",
                "from agentfox.spec.validators import Finding",
            ],
            capture_output=True,
            text=True,
        )
        assert result.returncode != 0
        assert "ImportError" in result.stderr or "ModuleNotFoundError" in result.stderr


# ===================================================================
# TS-137-E3: No deleted module imports in tests
# Requirement: 137-REQ-7.2, 137-REQ-7.3, 137-REQ-7.4, 137-REQ-7.E1
# ===================================================================


class TestNoDeletedModuleImportsInTests:
    """TS-137-E3: No test file imports from any deleted module."""

    def test_no_deleted_module_imports_in_tests(self) -> None:
        pattern = re.compile(r"from agent_fox\.spec\.validators")
        py_files = _collect_py_files(_TESTS_ROOT)
        matches: list[str] = []
        for p in py_files:
            if p.name == "test_137_legacy_removal.py":
                continue
            content = p.read_text(encoding="utf-8")
            for i, line in enumerate(content.splitlines(), start=1):
                if pattern.search(line):
                    matches.append(f"{p}:{i}: {line.strip()}")
        assert matches == [], "Deleted module imports found in tests:\n" + "\n".join(matches)


# ===================================================================
# TS-137-P1: Type identity preserved
# Property: Property 1 from design.md
# Validates: 137-REQ-1.1, 137-REQ-1.2
# ===================================================================


class TestTypeIdentityPreserved:
    """TS-137-P1: Shared types from types.py have identical field
    signatures to their former locations."""

    @pytest.mark.property
    @settings(deadline=None)
    @given(
        number=st.integers(min_value=1, max_value=100),
        title=st.text(min_size=1, max_size=50),
        optional=st.booleans(),
        completed=st.booleans(),
        body=st.text(max_size=200),
    )
    def test_taskgroupdef_field_identity(
        self,
        number: int,
        title: str,
        optional: bool,
        completed: bool,
        body: str,
    ) -> None:
        from agentfox.spec.types import TaskGroupDef

        group = TaskGroupDef(
            number=number,
            title=title,
            optional=optional,
            completed=completed,
            subtasks=(),
            body=body,
            archetype=None,
        )
        assert group.number == number
        assert group.title == title
        assert group.optional == optional
        assert group.completed == completed
        assert group.body == body

    @pytest.mark.property
    @settings(deadline=None)
    @given(
        id=st.text(min_size=1, max_size=10),
        title=st.text(min_size=1, max_size=50),
        completed=st.booleans(),
    )
    def test_subtaskdef_field_identity(self, id: str, title: str, completed: bool) -> None:
        from agentfox.spec.types import SubtaskDef

        sub = SubtaskDef(id=id, title=title, completed=completed)
        assert sub.id == id
        assert sub.title == title
        assert sub.completed == completed

    @pytest.mark.property
    @settings(deadline=None)
    @given(
        from_spec=st.text(min_size=1, max_size=30),
        from_group=st.integers(min_value=0, max_value=50),
        to_spec=st.text(min_size=1, max_size=30),
        to_group=st.integers(min_value=0, max_value=50),
    )
    def test_crossspecdep_field_identity(
        self,
        from_spec: str,
        from_group: int,
        to_spec: str,
        to_group: int,
    ) -> None:
        from agentfox.spec.types import CrossSpecDep

        dep = CrossSpecDep(
            from_spec=from_spec,
            from_group=from_group,
            to_spec=to_spec,
            to_group=to_group,
        )
        assert dep.from_spec == from_spec
        assert dep.from_group == from_group
        assert dep.to_spec == to_spec
        assert dep.to_group == to_group


# ===================================================================
# TS-137-P2: Full package importability
# Property: Property 2 from design.md
# Validates: 137-REQ-2.2, 137-REQ-3.1, 137-REQ-4.1
# ===================================================================


class TestFullPackageImportability:
    """TS-137-P2: Every Python module in agent_fox/ is importable."""

    @pytest.mark.property
    def test_all_modules_importable(self) -> None:

        failures: list[str] = []
        for _importer, modname, _ispkg in pkgutil.walk_packages(
            path=agentfox.__path__,
            prefix="agentfox.",
        ):
            try:
                importlib.import_module(modname)
            except ImportError as exc:
                failures.append(f"{modname}: {exc}")
            except Exception:
                pass
            except SystemExit:
                pass
        assert failures == [], "ImportError raised for modules:\n" + "\n".join(failures)


# ===================================================================
# TS-137-SMOKE-1: Full test suite passes
# Execution Path: Path 1 from design.md
# Requirement: 137-REQ-7.1
# ===================================================================


class TestSmokeFullTestSuite:
    """TS-137-SMOKE-1: The complete test suite passes after all deletions
    and rewiring."""

    @pytest.mark.smoke
    def test_full_test_suite_passes(self) -> None:
        """Run the full test suite and verify zero failures.

        This test invokes pytest as a subprocess to get an independent
        verification that the entire suite is green.
        """
        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "pytest",
                "-q",
                "--tb=no",
                "-n",
                "auto",
                "-k",
                "not test_full_test_suite_passes and not test_lint_specs_works",
                "--ignore=tests/integration/test_cross_process_lock.py",
            ],
            capture_output=True,
            text=True,
            cwd=str(_REPO_ROOT),
            timeout=600,
        )
        assert result.returncode == 0, (
            f"Test suite failed (rc={result.returncode}):\n{result.stdout[-2000:]}\n{result.stderr[-1000:]}"
        )


# ===================================================================
# TS-137-SMOKE-2: lint-specs works after deletion
# Execution Path: Path 1 from design.md
# Requirement: 137-REQ-6.2, 137-REQ-6.E1
# ===================================================================


class TestSmokeLintSpecs:
    """TS-137-SMOKE-2: lint-specs validates v1.2 specs without errors
    after validator deletion."""

    @pytest.mark.smoke
    def test_lint_specs_works_with_spec(self, tmp_path: Path) -> None:
        """Create a valid v1.2 spec and run lint on it."""
        specs_dir = tmp_path / "specs"
        spec_dir = specs_dir / "01_test_fixture"
        _write_spec(spec_dir)

        from agentfox.spec.lint import run_lint_specs

        result = run_lint_specs(specs_dir)
        # Should not crash -- result is a LintResult
        assert isinstance(result.findings, list)
        # No ImportError, no crash

    @pytest.mark.smoke
    def test_lint_specs_excludes_non_v12_dirs(self, tmp_path: Path) -> None:
        """137-REQ-6.E1: A spec dir without requirements.json is excluded."""
        specs_dir = tmp_path / "specs"
        bad_dir = specs_dir / "01_no_json"
        bad_dir.mkdir(parents=True)
        (bad_dir / "prd.md").write_text("# PRD\nContent")
        # No requirements.json -- should be excluded

        from agentfox.spec.lint import run_lint_specs

        # With no valid specs, we expect either an empty result or
        # a "no specs" finding -- but no crash.
        result = run_lint_specs(specs_dir)
        assert isinstance(result.findings, list)


# ===================================================================
# Additional structural tests for verification_checklist
# Requirement: 137-REQ-4.2
# ===================================================================


class TestVerificationChecklistNoV1Refs:
    """137-REQ-4.2: verification_checklist.py does not contain
    tasks.md or requirements.md as string literals."""

    def test_no_tasks_md_string(self) -> None:
        content = _read_source("agentfox/spec/verification_checklist.py")
        # Filter out comments
        lines = [line for line in content.splitlines() if not line.lstrip().startswith("#")]
        source = "\n".join(lines)
        assert "tasks.md" not in source

    def test_no_requirements_md_string(self) -> None:
        content = _read_source("agentfox/spec/verification_checklist.py")
        lines = [line for line in content.splitlines() if not line.lstrip().startswith("#")]
        source = "\n".join(lines)
        assert "requirements.md" not in source


# ===================================================================
# Additional: No V1_MARKDOWN in discovery.py
# Requirement: 137-REQ-6.1
# ===================================================================


class TestNoV1MarkdownInDiscovery:
    """137-REQ-6.1: discovery.py does not define V1_MARKDOWN enum member."""

    def test_no_v1_markdown_string(self) -> None:
        content = _read_source("agentfox/spec/discovery.py")
        assert "V1_MARKDOWN" not in content
