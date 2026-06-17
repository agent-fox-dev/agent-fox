"""Import isolation and property tests for agentspec.

Verifies that agentspec's imports are independent of CLI packages,
modules are uniquely placed across packages, internal imports resolve,
and patch targets remain valid.

Test Spec Entries: TS-10-P1, TS-10-P4, TS-10-E3, TS-10-E4, TS-10-E10
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]


# ---------------------------------------------------------------------------
# TS-10-P1: Import Independence
# Property 1 from design.md
# Validates: 10-REQ-1.1, 10-REQ-1.E1
# ---------------------------------------------------------------------------


def test_ts10_p1_import_independence() -> None:
    """TS-10-P1: Importing agentspec never triggers import of click or rich."""
    # Clear click/rich from sys.modules if present from prior tests
    saved_click = sys.modules.pop("click", None)
    saved_rich = sys.modules.pop("rich", None)
    # Also clear agentspec to force a fresh import
    agentspec_mods = [k for k in sys.modules if k.startswith("agentspec")]
    saved_agentspec = {k: sys.modules.pop(k) for k in agentspec_mods}

    try:
        import agentspec  # noqa: F811

        assert "click" not in sys.modules, "importing agentspec should not pull in click"
        assert "rich" not in sys.modules, "importing agentspec should not pull in rich"
        # Verify public symbols are accessible
        assert hasattr(agentspec, "SpecSession")
        assert hasattr(agentspec, "Campaign")
    finally:
        # Restore original module state
        sys.modules.update(saved_agentspec)
        if saved_click is not None:
            sys.modules["click"] = saved_click
        if saved_rich is not None:
            sys.modules["rich"] = saved_rich


# ---------------------------------------------------------------------------
# TS-10-P4: Module Placement Correctness
# Property 4 from design.md
# Validates: 10-REQ-1.3, 10-REQ-6.1, 10-REQ-6.2
# ---------------------------------------------------------------------------


def test_ts10_p4_module_placement_uniqueness() -> None:
    """TS-10-P4: Every Python module exists in exactly one package."""
    packages_dir = REPO_ROOT / "packages"

    # All three packages must exist under packages/
    expected_packages = ["af", "afspec", "agentfox", "agentspec"]
    for pkg in expected_packages:
        assert (packages_dir / pkg).is_dir(), f"Package directory missing: packages/{pkg}"

    # Collect all module names across packages and check uniqueness
    modules: dict[str, str] = {}
    for pkg_dir in sorted(packages_dir.iterdir()):
        if not pkg_dir.is_dir() or pkg_dir.name.startswith("."):
            continue
        for py_file in pkg_dir.rglob("*.py"):
            # Skip test files, __pycache__, venvs, and build artifacts
            parts = py_file.relative_to(pkg_dir).parts
            if any(
                p in ("tests", "__pycache__", "build", "dist", ".eggs", ".venv", ".tox", ".nox", "node_modules")
                for p in parts
            ):
                continue
            rel = py_file.relative_to(pkg_dir)
            module_name = ".".join(rel.with_suffix("").parts)
            assert module_name not in modules, (
                f"Module {module_name} found in both {modules[module_name]} and {pkg_dir.name}"
            )
            modules[module_name] = pkg_dir.name


# ---------------------------------------------------------------------------
# TS-10-E3: Relative Imports Within agentspec Still Work
# Requirement: 10-REQ-6.E1
# ---------------------------------------------------------------------------


def test_ts10_e3_internal_import_resolution() -> None:
    """TS-10-E3: Internal imports within agentspec resolve after restructure.

    Importing agentspec.agent triggers imports of agentspec.prompts,
    agentspec.tools, and agentspec.errors.
    """
    try:
        from agentspec.agent import SpecAgent
    except ImportError as exc:
        pytest.fail(f"Failed to import SpecAgent from agentspec.agent: {exc}")

    assert SpecAgent is not None


# ---------------------------------------------------------------------------
# TS-10-E10: Patch Targets Still Valid
# Requirement: 10-REQ-6.E2
# ---------------------------------------------------------------------------


def test_ts10_e10_patch_targets_resolve() -> None:
    """TS-10-E10: Module patch paths used in tests still resolve."""
    with patch("agentspec.session._utcnow") as mock_utcnow:
        assert mock_utcnow is not None

    with patch("agentspec.session._create_agent") as mock_create:
        assert mock_create is not None


# ---------------------------------------------------------------------------
# TS-10-E4: agentspec Import Without afspec
# Requirement: 10-REQ-1.E2
# ---------------------------------------------------------------------------


def test_ts10_e4_agentspec_without_afspec() -> None:
    """TS-10-E4: Importing agentspec without afspec raises ImportError.

    Uses subprocess isolation to simulate an environment where afspec is
    not available.  Setting ``sys.modules['afspec'] = None`` in a fresh
    interpreter blocks the import of afspec (and any of its submodules)
    before agentspec is loaded, verifying that agentspec correctly fails
    with ImportError when its afspec dependency is missing.
    """
    code = "\n".join(
        [
            "import sys",
            "# Block afspec and all its sub-modules",
            "sys.modules['afspec'] = None",
            "try:",
            "    import agentspec",
            "    sys.exit(1)  # Should have raised ImportError",
            "except ImportError:",
            "    sys.exit(0)",
        ]
    )
    result = subprocess.run(
        [sys.executable, "-c", code],
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert result.returncode == 0, (
        "Expected ImportError when afspec is unavailable, but "
        f"agentspec imported successfully.\n"
        f"stdout: {result.stdout}\nstderr: {result.stderr}"
    )
