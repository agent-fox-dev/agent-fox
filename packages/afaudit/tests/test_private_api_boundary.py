"""Tests for private API boundary enforcement.

TS-01-E2: _dispatch_optional not referenced in external call-sites or tests
TS-01-E6: _dispatch_optional not referenced in packages/afaudit/tests/
"""

from __future__ import annotations

from pathlib import Path

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]


def _collect_py_files(*dirs: Path) -> list[Path]:
    """Collect all .py files from the given directories."""
    files: list[Path] = []
    for d in dirs:
        if d.is_dir():
            files.extend(d.rglob("*.py"))
    return files


class TestDispatchOptionalBoundary:
    """TS-01-E2: _dispatch_optional is not referenced in external call-sites.

    Requirement: 01-REQ-2.E1
    """

    def test_no_dispatch_optional_in_external_code(self) -> None:
        """_dispatch_optional must not appear in tests, af, nightshift, or agentfox code.

        Excludes afaudit/afaudit/sink.py itself (where it is legitimately defined).
        """
        search_dirs = [
            WORKSPACE_ROOT / "packages" / "afaudit" / "tests",
            WORKSPACE_ROOT / "packages" / "af",
            WORKSPACE_ROOT / "packages" / "agentfox",
        ]
        # The definition site is allowed
        sink_path = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "sink.py"

        # This test file itself mentions _dispatch_optional in strings/docs
        this_file = Path(__file__).resolve()

        violations: list[str] = []
        for py_file in _collect_py_files(*search_dirs):
            if py_file.resolve() == sink_path.resolve():
                continue
            if py_file.resolve() == this_file:
                continue
            try:
                content = py_file.read_text(encoding="utf-8")
            except OSError:
                continue
            if "_dispatch_optional" in content:
                rel = py_file.relative_to(WORKSPACE_ROOT)
                violations.append(str(rel))

        assert violations == [], f"Private _dispatch_optional referenced in: {violations}"


class TestDispatchOptionalInAfauditTests:
    """TS-01-E6: _dispatch_optional not referenced in afaudit test files.

    Requirement: 01-REQ-11.E1
    """

    def test_no_dispatch_optional_in_afaudit_tests(self) -> None:
        """No test file in packages/afaudit/tests/ references _dispatch_optional."""
        test_dir = WORKSPACE_ROOT / "packages" / "afaudit" / "tests"
        violations: list[str] = []
        for py_file in _collect_py_files(test_dir):
            # Skip this file itself (it mentions the name in strings/docstrings
            # for test documentation purposes)
            if py_file.name == "test_private_api_boundary.py":
                continue
            try:
                content = py_file.read_text(encoding="utf-8")
            except OSError:
                continue
            if "_dispatch_optional" in content:
                violations.append(py_file.name)

        assert violations == [], f"Private _dispatch_optional referenced in afaudit test: {violations}"
