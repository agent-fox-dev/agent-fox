"""Tests for nightshift dependency isolation.

Test Spec: TS-07-P6, TS-07-E2
Requirements: 07-REQ-2.E1
"""

from __future__ import annotations

import tomllib
from pathlib import Path


def _load_nightshift_toml() -> dict:
    """Load packages/nightshift/pyproject.toml."""
    path = Path("packages/nightshift/pyproject.toml")
    with path.open("rb") as f:
        return tomllib.load(f)


class TestPyprojectNoBannedDeps:
    """TS-07-P6: pyproject.toml has no agentspec/afspec dependencies.

    Requirements: 07-REQ-2.E1
    """

    def test_no_agentspec_in_pyproject(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        for dep in deps:
            assert "agentspec" not in dep.lower(), (
                f"nightshift must not depend on agentspec: {dep}"
            )

    def test_no_afspec_in_pyproject(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        for dep in deps:
            assert "afspec" not in dep.lower(), (
                f"nightshift must not depend on afspec: {dep}"
            )


class TestInstalledDepsNoBanned:
    """TS-07-E2: Installed nightshift has no agentspec/afspec.

    Requirements: 07-REQ-2.E1
    """

    def test_no_agentspec_importable_from_nightshift(self) -> None:
        """nightshift package itself does not import agentspec."""
        source_dir = Path("packages/nightshift/nightshift")
        for py_file in source_dir.glob("*.py"):
            content = py_file.read_text()
            assert "import agentspec" not in content, (
                f"{py_file} imports agentspec"
            )
            assert "from agentspec" not in content, (
                f"{py_file} imports from agentspec"
            )

    def test_no_afspec_importable_from_nightshift(self) -> None:
        """nightshift package itself does not import afspec."""
        source_dir = Path("packages/nightshift/nightshift")
        for py_file in source_dir.glob("*.py"):
            content = py_file.read_text()
            assert "import afspec" not in content, (
                f"{py_file} imports afspec"
            )
            assert "from afspec" not in content, (
                f"{py_file} imports from afspec"
            )
