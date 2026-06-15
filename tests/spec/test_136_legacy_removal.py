"""Spec 136: Legacy Format Removal tests.

Test Spec: TS-136-1 through TS-136-10, TS-136-E1 through TS-136-E3,
           TS-136-P1, TS-136-P2, TS-136-SMOKE-1, TS-136-SMOKE-2
Requirements: 136-REQ-1.1, 136-REQ-1.2, 136-REQ-1.E1,
              136-REQ-2.1, 136-REQ-2.2, 136-REQ-2.E1,
              136-REQ-3.1, 136-REQ-3.2, 136-REQ-3.3, 136-REQ-3.4, 136-REQ-3.E1,
              136-REQ-4.1, 136-REQ-4.2, 136-REQ-4.3, 136-REQ-4.4, 136-REQ-4.E1,
              136-REQ-5.1, 136-REQ-5.2, 136-REQ-5.3, 136-REQ-5.E1,
              136-REQ-6.1, 136-REQ-6.2, 136-REQ-6.E1
"""

from __future__ import annotations

import importlib
import pathlib
import subprocess
import sys

import pytest

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

_PROJECT_ROOT = pathlib.Path(__file__).resolve().parents[2]
_AGENT_FOX_PKG = _PROJECT_ROOT / "agent_fox"

# Modules that must be deleted by this spec
_DELETED_MODULES = [
    "agent_fox.spec.parser",
    "agent_fox.spec.validators",
    "agent_fox.spec.verification_checklist",
    "agent_fox.spec.ai_validation",
]

# Engine consumer modules that must not import from parser.py
_ENGINE_MODULES = [
    _AGENT_FOX_PKG / "engine" / "session_lifecycle.py",
    _AGENT_FOX_PKG / "engine" / "hot_load.py",
    _AGENT_FOX_PKG / "engine" / "engine.py",
    _AGENT_FOX_PKG / "engine" / "dispatch.py",
]

# Graph consumer modules that must import from spec.types
_GRAPH_MODULES = [
    _AGENT_FOX_PKG / "graph" / "planner.py",
    _AGENT_FOX_PKG / "graph" / "builder.py",
]


# ---------------------------------------------------------------------------
# TS-136-1: types.py exports correct dataclasses
# Requirement: 136-REQ-1.1
# ---------------------------------------------------------------------------


class TestTypesExports:
    """Verify types.py contains TaskGroupDef, SubtaskDef, CrossSpecDep.

    Field signatures must match those from the deleted parser.py:
    - SubtaskDef: id (str), title (str), completed (bool)
    - TaskGroupDef: number (int), title (str), optional (bool),
      completed (bool), subtasks (tuple[SubtaskDef, ...]), body (str),
      archetype (str | None)
    - CrossSpecDep: from_spec (str), from_group (int), to_spec (str),
      to_group (int)
    """

    def test_subtask_def_importable_and_constructable(self) -> None:
        from agent_fox.spec.types import SubtaskDef

        sub = SubtaskDef(id="1.1", title="test subtask", completed=False)
        assert sub.id == "1.1"
        assert sub.title == "test subtask"
        assert sub.completed is False

    def test_task_group_def_importable_and_constructable(self) -> None:
        from agent_fox.spec.types import SubtaskDef, TaskGroupDef

        sub = SubtaskDef(id="1.1", title="test", completed=False)
        group = TaskGroupDef(
            number=1,
            title="test group",
            optional=False,
            completed=False,
            subtasks=(sub,),
            body="",
            archetype=None,
        )
        assert group.number == 1
        assert group.title == "test group"
        assert group.subtasks == (sub,)

    def test_cross_spec_dep_importable_and_constructable(self) -> None:
        from agent_fox.spec.types import CrossSpecDep

        dep = CrossSpecDep(
            from_spec="01_spec_a",
            from_group=1,
            to_spec="02_spec_b",
            to_group=2,
        )
        assert dep.from_spec == "01_spec_a"
        assert dep.from_group == 1
        assert dep.to_spec == "02_spec_b"
        assert dep.to_group == 2


# ---------------------------------------------------------------------------
# TS-136-2: Types are import-compatible across modules
# Requirement: 136-REQ-1.2
# ---------------------------------------------------------------------------


class TestImportCompatibility:
    """Verify planner.py, builder.py, and parser_v12.py all use the same
    TaskGroupDef type from agent_fox.spec.types.
    """

    def test_types_module_loaded_after_consumer_imports(self) -> None:
        """After importing graph and parser_v12 modules, spec.types should
        be in sys.modules -- proving they import from the shared location."""
        # These imports must succeed AND pull in spec.types
        import agent_fox.graph.builder  # noqa: F401, I001
        import agent_fox.spec.parser_v12  # noqa: F401, I001

        assert "agent_fox.spec.types" in sys.modules

    def test_builder_uses_types_module_source(self) -> None:
        """builder.py must import TaskGroupDef from agent_fox.spec.types,
        not from agent_fox.spec.parser."""
        content = (_AGENT_FOX_PKG / "graph" / "builder.py").read_text(
            encoding="utf-8",
        )
        assert "from agent_fox.spec.types import" in content


# ---------------------------------------------------------------------------
# TS-136-3: parser.py is deleted
# Requirement: 136-REQ-2.1
# ---------------------------------------------------------------------------


class TestParserDeleted:
    """Verify parser.py does not exist on disk."""

    def test_parser_py_does_not_exist(self) -> None:
        parser_path = _AGENT_FOX_PKG / "spec" / "parser.py"
        assert not parser_path.exists(), (
            f"parser.py still exists at {parser_path}"
        )


# ---------------------------------------------------------------------------
# TS-136-4: validators/ directory is deleted
# Requirement: 136-REQ-3.1
# ---------------------------------------------------------------------------


class TestValidatorsDeleted:
    """Verify the validators directory does not exist."""

    def test_validators_dir_does_not_exist(self) -> None:
        validators_path = _AGENT_FOX_PKG / "spec" / "validators"
        assert not validators_path.exists(), (
            f"validators/ still exists at {validators_path}"
        )


# ---------------------------------------------------------------------------
# TS-136-5: verification_checklist.py is deleted
# Requirement: 136-REQ-3.2
# ---------------------------------------------------------------------------


class TestVerificationChecklistDeleted:
    """Verify verification_checklist.py does not exist."""

    def test_verification_checklist_does_not_exist(self) -> None:
        vc_path = _AGENT_FOX_PKG / "spec" / "verification_checklist.py"
        assert not vc_path.exists(), (
            f"verification_checklist.py still exists at {vc_path}"
        )


# ---------------------------------------------------------------------------
# TS-136-6: ai_validation.py is deleted
# Requirement: 136-REQ-3.3
# ---------------------------------------------------------------------------


class TestAiValidationDeleted:
    """Verify ai_validation.py does not exist."""

    def test_ai_validation_does_not_exist(self) -> None:
        ai_path = _AGENT_FOX_PKG / "spec" / "ai_validation.py"
        assert not ai_path.exists(), (
            f"ai_validation.py still exists at {ai_path}"
        )


# ---------------------------------------------------------------------------
# TS-136-7: Engine modules do not import from parser.py
# Requirement: 136-REQ-4.1, 136-REQ-4.2, 136-REQ-4.3
# ---------------------------------------------------------------------------


class TestEngineImports:
    """Verify engine modules import cleanly without referencing parser.py."""

    @pytest.mark.parametrize(
        "module_path",
        _ENGINE_MODULES,
        ids=[p.stem for p in _ENGINE_MODULES],
    )
    def test_engine_module_no_parser_import(
        self, module_path: pathlib.Path,
    ) -> None:
        """Each engine module must not contain imports from
        agent_fox.spec.parser."""
        content = module_path.read_text(encoding="utf-8")
        assert "from agent_fox.spec.parser" not in content, (
            f"{module_path.name} still imports from agent_fox.spec.parser"
        )

    def test_engine_modules_import_without_error(self) -> None:
        """All engine modules must be importable after rewiring."""
        # Run in a subprocess to avoid sys.modules caching effects
        script = (
            "import agent_fox.engine.session_lifecycle;"
            "import agent_fox.engine.hot_load;"
            "import agent_fox.engine.engine;"
            "import agent_fox.engine.dispatch;"
            "print('OK')"
        )
        result = subprocess.run(
            [sys.executable, "-c", script],
            capture_output=True,
            text=True,
            cwd=str(_PROJECT_ROOT),
        )
        assert result.returncode == 0, (
            f"Engine module import failed:\n{result.stderr}"
        )


# ---------------------------------------------------------------------------
# TS-136-8: Graph modules import types from spec/types.py
# Requirement: 136-REQ-4.4
# ---------------------------------------------------------------------------


class TestGraphImports:
    """Verify graph modules import from the new spec.types location."""

    @pytest.mark.parametrize(
        "module_path",
        _GRAPH_MODULES,
        ids=[p.stem for p in _GRAPH_MODULES],
    )
    def test_graph_module_imports_from_types(
        self, module_path: pathlib.Path,
    ) -> None:
        """Each graph module must import from agent_fox.spec.types."""
        content = module_path.read_text(encoding="utf-8")
        assert "from agent_fox.spec.types import" in content, (
            f"{module_path.name} does not import from agent_fox.spec.types"
        )

    def test_parser_v12_imports_from_types(self) -> None:
        """parser_v12.py must import from agent_fox.spec.types."""
        p12_path = _AGENT_FOX_PKG / "spec" / "parser_v12.py"
        assert p12_path.exists(), "parser_v12.py does not exist"
        content = p12_path.read_text(encoding="utf-8")
        assert "from agent_fox.spec.types import" in content

    def test_graph_modules_import_without_error(self) -> None:
        """graph/planner.py, graph/builder.py, and spec/parser_v12.py must
        all import successfully."""
        script = (
            "import agent_fox.graph.planner;"
            "import agent_fox.graph.builder;"
            "import agent_fox.spec.parser_v12;"
            "print('OK')"
        )
        result = subprocess.run(
            [sys.executable, "-c", script],
            capture_output=True,
            text=True,
            cwd=str(_PROJECT_ROOT),
        )
        assert result.returncode == 0, (
            f"Graph/parser_v12 module import failed:\n{result.stderr}"
        )


# ---------------------------------------------------------------------------
# TS-136-9: No stale markdown references in source
# Requirement: 136-REQ-5.1, 136-REQ-5.2, 136-REQ-5.3
# ---------------------------------------------------------------------------


class TestNoStaleReferences:
    """Grep confirms no old spec filenames remain (excluding fix/spec_gen.py)."""

    def test_no_old_spec_filenames_in_source(self) -> None:
        """No Python file in agent_fox/ (except fix/spec_gen.py) should
        reference requirements.md, design.md, or test_spec.md."""
        result = subprocess.run(
            [
                "grep",
                "-rn",
                r"requirements\.md\|design\.md\|test_spec\.md",
                str(_AGENT_FOX_PKG),
                "--include=*.py",
            ],
            capture_output=True,
            text=True,
        )
        # Filter out allowed exceptions
        lines = [
            line
            for line in result.stdout.splitlines()
            if "spec_gen" not in line and "__pycache__" not in line
        ]
        assert len(lines) == 0, (
            f"Stale references found ({len(lines)} matches):\n"
            + "\n".join(lines[:20])
        )

    def test_no_core_spec_files_constant(self) -> None:
        """_CORE_SPEC_FILES referencing old markdown filenames must not
        exist in session/context.py."""
        context_py = _AGENT_FOX_PKG / "session" / "context.py"
        content = context_py.read_text(encoding="utf-8")
        assert "_CORE_SPEC_FILES" not in content, (
            "session/context.py still contains _CORE_SPEC_FILES"
        )

    def test_no_expected_files_old_list(self) -> None:
        """EXPECTED_FILES referencing old five-file list must not exist
        (excluding fix/spec_gen.py)."""
        result = subprocess.run(
            [
                "grep",
                "-rn",
                "EXPECTED_FILES",
                str(_AGENT_FOX_PKG),
                "--include=*.py",
            ],
            capture_output=True,
            text=True,
        )
        lines = [
            line
            for line in result.stdout.splitlines()
            if "spec_gen" not in line and "__pycache__" not in line
        ]
        assert len(lines) == 0, (
            "EXPECTED_FILES references found:\n" + "\n".join(lines[:20])
        )


# ---------------------------------------------------------------------------
# TS-136-10: Full test suite passes
# Requirement: 136-REQ-2.2, 136-REQ-6.1
# ---------------------------------------------------------------------------


class TestFullSuiteIntegrity:
    """Verify the test suite has no import errors from deleted modules."""

    def test_no_deleted_module_imports_in_tests(self) -> None:
        """No test file should import from any deleted module."""
        deleted_patterns = (
            r"from agent_fox\.spec\.parser import"
            r"\|from agent_fox\.spec\.validators"
            r"\|from agent_fox\.spec\.verification_checklist import"
            r"\|from agent_fox\.spec\.ai_validation import"
        )
        result = subprocess.run(
            [
                "grep",
                "-rn",
                deleted_patterns,
                str(_PROJECT_ROOT / "tests"),
                "--include=*.py",
            ],
            capture_output=True,
            text=True,
        )
        # Filter out this test file itself and __pycache__
        lines = [
            line
            for line in result.stdout.splitlines()
            if "test_136_legacy_removal" not in line
            and "__pycache__" not in line
        ]
        assert len(lines) == 0, (
            f"Test files still import from deleted modules "
            f"({len(lines)} matches):\n" + "\n".join(lines[:30])
        )

    def test_pytest_collection_succeeds(self) -> None:
        """pytest --collect-only should succeed without import errors."""
        result = subprocess.run(
            [sys.executable, "-m", "pytest", "--collect-only", "-q"],
            capture_output=True,
            text=True,
            cwd=str(_PROJECT_ROOT),
            timeout=120,
        )
        assert result.returncode == 0, (
            f"pytest collection failed:\n{result.stderr[-2000:]}"
        )
        assert "ImportError" not in result.stderr


# ---------------------------------------------------------------------------
# TS-136-E1: Import from deleted parser raises ImportError
# Requirement: 136-REQ-1.E1, 136-REQ-4.E1
# ---------------------------------------------------------------------------


class TestParserImportError:
    """Attempting to import from parser.py raises ImportError."""

    def test_import_parse_tasks_raises_import_error(self) -> None:
        """from agent_fox.spec.parser import parse_tasks must fail."""
        script = "from agent_fox.spec.parser import parse_tasks"
        result = subprocess.run(
            [sys.executable, "-c", script],
            capture_output=True,
            text=True,
            cwd=str(_PROJECT_ROOT),
        )
        assert result.returncode != 0, (
            "Import from deleted parser.py should fail with ImportError"
        )
        assert "ImportError" in result.stderr or "ModuleNotFoundError" in result.stderr


# ---------------------------------------------------------------------------
# TS-136-E2: Legacy test files cleaned up
# Requirement: 136-REQ-2.E1, 136-REQ-6.2, 136-REQ-6.E1
# ---------------------------------------------------------------------------


class TestLegacyTestsCleaned:
    """No test file imports from deleted modules."""

    def test_no_parser_imports_in_tests(self) -> None:
        """No test file should import from agent_fox.spec.parser."""
        result = subprocess.run(
            [
                "grep",
                "-rn",
                r"from agent_fox\.spec\.parser import",
                str(_PROJECT_ROOT / "tests"),
                "--include=*.py",
            ],
            capture_output=True,
            text=True,
        )
        lines = [
            line
            for line in result.stdout.splitlines()
            if "__pycache__" not in line
        ]
        assert len(lines) == 0, (
            "Test files still import from parser.py:\n"
            + "\n".join(lines[:20])
        )

    def test_no_validator_imports_in_tests(self) -> None:
        """No test file should import from agent_fox.spec.validators."""
        result = subprocess.run(
            [
                "grep",
                "-rn",
                r"from agent_fox\.spec\.validators",
                str(_PROJECT_ROOT / "tests"),
                "--include=*.py",
            ],
            capture_output=True,
            text=True,
        )
        lines = [
            line
            for line in result.stdout.splitlines()
            if "__pycache__" not in line
        ]
        assert len(lines) == 0, (
            "Test files still import from validators:\n"
            + "\n".join(lines[:20])
        )


# ---------------------------------------------------------------------------
# TS-136-E3: fix/spec_gen.py left intact
# Requirement: 136-REQ-5.E1
# ---------------------------------------------------------------------------


class TestSpecGenPreserved:
    """fix/spec_gen.py still exists and can be imported."""

    def test_spec_gen_exists(self) -> None:
        spec_gen_path = _AGENT_FOX_PKG / "fix" / "spec_gen.py"
        assert spec_gen_path.exists(), (
            f"fix/spec_gen.py should not have been deleted: {spec_gen_path}"
        )

    def test_spec_gen_importable(self) -> None:
        """fix/spec_gen.py must be importable without error."""
        mod = importlib.import_module("agent_fox.fix.spec_gen")
        assert mod is not None


# ---------------------------------------------------------------------------
# TS-136-P1 / TS-136-SMOKE-1: Full package importability
# Property: Property 2 from design.md
# Requirement: 136-REQ-2.1, 136-REQ-3.1, 136-REQ-4.1-4.3
# ---------------------------------------------------------------------------


class TestPackageImportability:
    """Every Python module in agent_fox/ can be imported without ImportError."""

    def test_all_modules_importable(self) -> None:
        """Walk agent_fox package and import every discovered module.
        Any ImportError indicates a dangling reference to a deleted module."""
        import pkgutil

        errors: list[str] = []
        for _importer, name, _ispkg in pkgutil.walk_packages(
            [str(_AGENT_FOX_PKG)],
            prefix="agent_fox.",
        ):
            try:
                importlib.import_module(name)
            except ImportError as exc:
                errors.append(f"{name}: {exc}")
            except Exception:  # noqa: BLE001
                # Non-import errors (missing env vars, etc.) are out of scope
                pass
        assert not errors, (
            f"Import errors found ({len(errors)}):\n" + "\n".join(errors)
        )


# ---------------------------------------------------------------------------
# TS-136-P2: No old-format references in source
# Property: Property 3 from design.md
# Requirement: 136-REQ-5.2
# ---------------------------------------------------------------------------


class TestNoOldFormatRefs:
    """No Python source file in agent_fox/ (excluding fix/spec_gen.py)
    contains old spec filename strings."""

    _OLD_PATTERNS = ("requirements.md", "design.md", "test_spec.md")

    def test_no_old_format_references(self) -> None:
        violations: list[str] = []
        for py_file in sorted(_AGENT_FOX_PKG.rglob("*.py")):
            if "spec_gen" in py_file.name:
                continue
            if "__pycache__" in str(py_file):
                continue
            content = py_file.read_text(encoding="utf-8")
            for pattern in self._OLD_PATTERNS:
                if pattern in content:
                    violations.append(f"{py_file.name}: contains '{pattern}'")
        assert not violations, (
            f"Old format references found ({len(violations)}):\n"
            + "\n".join(violations[:30])
        )


# ---------------------------------------------------------------------------
# TS-136-SMOKE-2: lint-specs works after validator deletion
# Execution Path: Path 3 from design.md
# Requirement: 136-REQ-3.4
# ---------------------------------------------------------------------------


class TestLintSpecsAfterDeletion:
    """lint module doesn't import from deleted validators."""

    def test_lint_py_no_validator_imports(self) -> None:
        """agent_fox/spec/lint.py must not import from validators."""
        lint_py = _AGENT_FOX_PKG / "spec" / "lint.py"
        assert lint_py.exists()
        content = lint_py.read_text(encoding="utf-8")
        assert "agent_fox.spec.validators" not in content, (
            "lint.py still imports from agent_fox.spec.validators"
        )

    def test_lint_specs_cli_no_validator_imports(self) -> None:
        """agent_fox/cli/lint_specs.py must not import from validators."""
        cli_py = _AGENT_FOX_PKG / "cli" / "lint_specs.py"
        assert cli_py.exists()
        content = cli_py.read_text(encoding="utf-8")
        assert "agent_fox.spec.validators" not in content, (
            "lint_specs.py still imports from agent_fox.spec.validators"
        )
