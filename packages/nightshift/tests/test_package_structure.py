"""Tests for nightshift package structure and metadata.

Test Spec: TS-07-4, TS-07-5, TS-07-6, TS-07-7, TS-07-E2, TS-07-30, TS-07-P6
Requirements: 07-REQ-2.1, 07-REQ-2.2, 07-REQ-2.3, 07-REQ-2.4, 07-REQ-2.E1,
              07-REQ-6.1, 07-REQ-6.2
"""

from __future__ import annotations

import os
import tomllib
from pathlib import Path


def _load_nightshift_toml() -> dict:
    """Load and return packages/nightshift/pyproject.toml."""
    path = Path("packages/nightshift/pyproject.toml")
    with path.open("rb") as f:
        return tomllib.load(f)


def _load_af_toml() -> dict:
    """Load and return packages/af/pyproject.toml."""
    path = Path("packages/af/pyproject.toml")
    with path.open("rb") as f:
        return tomllib.load(f)


class TestPackageDirectoryStructure:
    """TS-07-4: All required files and directories exist.

    Requirements: 07-REQ-2.1
    """

    def test_pyproject_toml_exists(self) -> None:
        assert os.path.exists("packages/nightshift/pyproject.toml")

    def test_init_py_exists(self) -> None:
        assert os.path.exists("packages/nightshift/nightshift/__init__.py")

    def test_main_py_exists(self) -> None:
        assert os.path.exists("packages/nightshift/nightshift/__main__.py")

    def test_app_py_exists(self) -> None:
        assert os.path.exists("packages/nightshift/nightshift/app.py")


class TestPyprojectMetadata:
    """TS-07-5: pyproject.toml has correct metadata.

    Requirements: 07-REQ-2.2
    """

    def test_project_name(self) -> None:
        config = _load_nightshift_toml()
        assert config["project"]["name"] == "nightshift"

    def test_project_version(self) -> None:
        config = _load_nightshift_toml()
        assert config["project"]["version"] == "4.0.0-rc5"

    def test_project_description(self) -> None:
        config = _load_nightshift_toml()
        assert config["project"]["description"] == ("Standalone CLI for the AgentFox Night Shift fix daemon")

    def test_build_backend(self) -> None:
        config = _load_nightshift_toml()
        assert config["build-system"]["build-backend"] == "hatchling.build"

    def test_license_matches_af(self) -> None:
        """License field matches the af package (if present in af)."""
        ns_config = _load_nightshift_toml()
        af_config = _load_af_toml()
        ns_license = ns_config["project"].get("license")
        af_license = af_config["project"].get("license")
        # Both should be the same (both may be None if not set)
        assert ns_license == af_license

    def test_authors_match_af(self) -> None:
        """Authors field matches the af package (if present in af)."""
        ns_config = _load_nightshift_toml()
        af_config = _load_af_toml()
        ns_authors = ns_config["project"].get("authors")
        af_authors = af_config["project"].get("authors")
        assert ns_authors == af_authors


class TestDirectDependencies:
    """TS-07-6: pyproject.toml declares required direct dependencies.

    Requirements: 07-REQ-2.3
    """

    def test_agentfox_dependency(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        assert any("agentfox" in d for d in deps)
        assert any("agentfox" in d and "4.0.0rc5" in d for d in deps)

    def test_click_dependency(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        assert any("click" in d for d in deps)
        assert any("click" in d and "8.1" in d for d in deps)

    def test_rich_dependency(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        assert any("rich" in d for d in deps)
        assert any("rich" in d and "15.0" in d for d in deps)

    def test_duckdb_dependency(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        assert any("duckdb" in d for d in deps)
        assert any("duckdb" in d and "1.5.4" in d for d in deps)


class TestCliEntryPoint:
    """TS-07-7: CLI entry point is correctly configured.

    Requirements: 07-REQ-2.4
    """

    def test_night_shift_entry_point(self) -> None:
        config = _load_nightshift_toml()
        scripts = config["project"]["scripts"]
        assert scripts.get("night-shift") == "nightshift.app:main"


class TestNoDependencyOnAgentspecAfspec:
    """TS-07-E2 / TS-07-P6: nightshift must not depend on agentspec or afspec.

    Requirements: 07-REQ-2.E1
    """

    def test_no_agentspec_in_dependencies(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        for dep in deps:
            assert "agentspec" not in dep.lower(), f"nightshift must not depend on agentspec: {dep}"

    def test_no_afspec_in_dependencies(self) -> None:
        config = _load_nightshift_toml()
        deps = config["project"]["dependencies"]
        for dep in deps:
            assert "afspec" not in dep.lower(), f"nightshift must not depend on afspec: {dep}"


class TestRichDuckdbDirectDeps:
    """TS-07-30: rich and duckdb are direct deps, not transitive only.

    Requirements: 07-REQ-6.1
    """

    def test_rich_is_direct_dependency(self) -> None:
        config = _load_nightshift_toml()
        dep_names = [d.split(">")[0].split("<")[0].split("=")[0].strip() for d in config["project"]["dependencies"]]
        assert "rich" in dep_names

    def test_duckdb_is_direct_dependency(self) -> None:
        config = _load_nightshift_toml()
        dep_names = [d.split(">")[0].split("<")[0].split("=")[0].strip() for d in config["project"]["dependencies"]]
        assert "duckdb" in dep_names
