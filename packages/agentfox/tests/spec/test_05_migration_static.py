"""Spec 05: Static analysis tests for migration verification.

Tests that spec/ui.py has been deleted, StatusSpinner imports migrated,
and inline JSON/error patterns removed.

Test Spec: TS-05-16, TS-05-17, TS-05-18, TS-05-19, TS-05-20, TS-05-21,
           TS-05-24, TS-05-E6
Requirements: 05-REQ-4.1, 05-REQ-4.2, 05-REQ-4.3, 05-REQ-5.1, 05-REQ-5.2,
              05-REQ-5.3, 05-REQ-6.2
"""

from __future__ import annotations

import importlib
from pathlib import Path

import pytest


def _get_spec_package_dir() -> Path:
    """Return the directory of the spec package."""
    import spec

    return Path(spec.__file__).parent


def _get_cli_source() -> str:
    """Read the source text of spec/cli.py."""
    import spec.cli

    return Path(spec.cli.__file__).read_text()


def _find_python_files(directory: Path) -> list[Path]:
    """Find all .py files recursively in a directory."""
    return list(directory.rglob("*.py"))


def _find_test_python_files() -> list[Path]:
    """Find all .py files in the test directories."""
    # Look for test files across the project
    project_root = _get_spec_package_dir().parent.parent.parent
    test_dirs = [
        project_root / "packages" / "agentfox" / "tests",
        project_root / "packages" / "agentspec" / "tests",
        project_root / "packages" / "afspec" / "tests",
        project_root / "packages" / "af" / "tests",
    ]
    files = []
    for d in test_dirs:
        if d.exists():
            files.extend(d.rglob("*.py"))
    return files


# ===========================================================================
# TS-05-16: spec/ui.py does not exist
# ===========================================================================


class TestSpecUiDeleted:
    """TS-05-16: spec/ui.py does not exist after migration.

    Requirement: 05-REQ-4.1
    """

    @pytest.mark.xfail(reason="spec/ui.py not yet deleted")
    def test_spec_ui_does_not_exist(self) -> None:
        """spec/ui.py file does not exist in the spec package."""
        spec_dir = _get_spec_package_dir()
        ui_path = spec_dir / "ui.py"
        assert not ui_path.exists(), f"spec/ui.py still exists at {ui_path}"


# ===========================================================================
# TS-05-17: StatusSpinner imports from agentfox.io
# ===========================================================================


class TestStatusSpinnerImport:
    """TS-05-17: All StatusSpinner imports use agentfox.io.

    Requirement: 05-REQ-4.2
    """

    @pytest.mark.xfail(reason="StatusSpinner import migration not yet done")
    def test_statusspinner_from_agentfox_io(self) -> None:
        """All spec package files import StatusSpinner from agentfox.io."""
        spec_dir = _get_spec_package_dir()
        for py_file in _find_python_files(spec_dir):
            source = py_file.read_text()
            if "StatusSpinner" in source:
                assert "from agentfox.io import StatusSpinner" in source or "from agentfox.io import" in source, (
                    f"{py_file} imports StatusSpinner but not from agentfox.io"
                )


# ===========================================================================
# TS-05-18: No 'from spec.ui import' statements
# ===========================================================================


class TestNoSpecUiImports:
    """TS-05-18: No 'from spec.ui import' in any spec package file.

    Requirement: 05-REQ-4.3
    """

    @pytest.mark.xfail(reason="spec.ui imports not yet migrated")
    def test_no_spec_ui_imports_in_spec_package(self) -> None:
        """Zero files in spec/ contain 'from spec.ui import'."""
        spec_dir = _get_spec_package_dir()
        for py_file in _find_python_files(spec_dir):
            source = py_file.read_text()
            assert "from spec.ui import" not in source, (
                f"{py_file} still contains 'from spec.ui import'"
            )


# ===========================================================================
# TS-05-19: No click.echo(json.dumps(...)) in spec/cli.py
# ===========================================================================


class TestNoClickEchoJsonDumps:
    """TS-05-19: spec/cli.py has no click.echo(json.dumps(...)) calls.

    Requirement: 05-REQ-5.1
    """

    @pytest.mark.xfail(reason="click.echo(json.dumps) not yet removed")
    def test_no_click_echo_json_dumps(self) -> None:
        """spec/cli.py does not contain click.echo(json.dumps(...)."""
        source = _get_cli_source()
        assert "click.echo(json.dumps" not in source


# ===========================================================================
# TS-05-20: No _json_error_exit in spec/cli.py
# ===========================================================================


class TestNoJsonErrorExit:
    """TS-05-20: spec/cli.py has no _json_error_exit definition or calls.

    Requirement: 05-REQ-5.2
    """

    @pytest.mark.xfail(reason="_json_error_exit not yet removed")
    def test_no_json_error_exit(self) -> None:
        """_json_error_exit does not appear in spec/cli.py."""
        source = _get_cli_source()
        assert "_json_error_exit" not in source


# ===========================================================================
# TS-05-21: No _assessment_to_json in spec/cli.py
# ===========================================================================


class TestNoAssessmentToJson:
    """TS-05-21: _assessment_to_json helper removed from spec/cli.py.

    Requirement: 05-REQ-5.3
    """

    @pytest.mark.xfail(reason="_assessment_to_json not yet removed")
    def test_no_assessment_to_json(self) -> None:
        """_assessment_to_json does not appear in spec/cli.py."""
        source = _get_cli_source()
        assert "_assessment_to_json" not in source


# ===========================================================================
# TS-05-24: No test imports from spec.ui
# ===========================================================================


class TestNoTestImportsFromSpecUi:
    """TS-05-24: No test file imports from spec.ui.

    Requirement: 05-REQ-6.2
    """

    @pytest.mark.xfail(reason="Test imports not yet migrated")
    def test_no_spec_ui_imports_in_tests(self) -> None:
        """Zero test files contain 'from spec.ui import' or 'import spec.ui'."""
        test_files = _find_test_python_files()
        for tf in test_files:
            source = tf.read_text()
            assert "from spec.ui import" not in source, (
                f"{tf} still contains 'from spec.ui import'"
            )
            assert "import spec.ui" not in source, (
                f"{tf} still contains 'import spec.ui'"
            )


# ===========================================================================
# TS-05-E6: Importing from spec.ui raises ImportError
# ===========================================================================


class TestSpecUiImportError:
    """TS-05-E6: from spec.ui import StatusSpinner raises ImportError.

    Requirement: 05-REQ-4.E1
    """

    @pytest.mark.xfail(reason="spec/ui.py not yet deleted")
    def test_import_spec_ui_raises_importerror(self) -> None:
        """Attempting to import from spec.ui raises ImportError."""
        # Ensure any cached import is cleared
        import sys

        for mod_name in list(sys.modules):
            if mod_name.startswith("spec.ui"):
                del sys.modules[mod_name]

        with pytest.raises(ImportError):
            importlib.import_module("spec.ui")
