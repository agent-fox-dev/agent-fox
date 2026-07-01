"""Tests verifying deleted knowledge modules are absent from the repository.

Checks that errata.py and adr.py have been deleted and no import
references remain anywhere in the codebase.

Test Spec: TS-10-4, TS-10-5, TS-10-6
Requirements: 10-REQ-2.1, 10-REQ-2.2, 10-REQ-2.3
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

# Repository root (three levels up from this file's directory)
_REPO_ROOT = Path(__file__).resolve().parents[4]
_PACKAGES_ROOT = _REPO_ROOT / "packages" / "agentfox"


# ---------------------------------------------------------------------------
# TS-10-4: errata.py file is absent
# ---------------------------------------------------------------------------


class TestErrataModuleAbsent:
    """TS-10-4: packages/agentfox/agentfox/knowledge/errata.py must not exist."""

    def test_errata_file_absent(self) -> None:
        errata_path = _PACKAGES_ROOT / "agentfox" / "knowledge" / "errata.py"
        assert not errata_path.exists(), f"errata.py must be deleted: {errata_path}"

    def test_errata_import_raises(self) -> None:
        """Importing agentfox.knowledge.errata must raise ImportError."""
        with pytest.raises(ImportError):
            import importlib

            importlib.import_module("agentfox.knowledge.errata")


# ---------------------------------------------------------------------------
# TS-10-5: adr.py file is absent
# ---------------------------------------------------------------------------


class TestAdrModuleAbsent:
    """TS-10-5: packages/agentfox/agentfox/knowledge/adr.py must not exist."""

    def test_adr_file_absent(self) -> None:
        adr_path = _PACKAGES_ROOT / "agentfox" / "knowledge" / "adr.py"
        assert not adr_path.exists(), f"adr.py must be deleted: {adr_path}"

    def test_adr_import_raises(self) -> None:
        """Importing agentfox.knowledge.adr must raise ImportError."""
        with pytest.raises(ImportError):
            import importlib

            importlib.import_module("agentfox.knowledge.adr")


# ---------------------------------------------------------------------------
# TS-10-6: No import references to deleted modules in any Python file
# ---------------------------------------------------------------------------


class TestNoImportReferences:
    """TS-10-6: Zero import references to errata or adr knowledge modules."""

    @pytest.mark.parametrize(
        "pattern",
        [
            "agentfox.knowledge.errata",
            "agentfox.knowledge.adr",
            "from .errata import",
            "from .adr import",
        ],
    )
    def test_no_import_references(self, pattern: str) -> None:
        """Grep the codebase for import references to deleted modules."""
        result = subprocess.run(
            [
                "grep",
                "-r",
                "--include=*.py",
                pattern,
                str(_PACKAGES_ROOT),
            ],
            capture_output=True,
            text=True,
        )
        matches = result.stdout.strip()
        # Filter out this test file itself from matches
        if matches:
            filtered = [
                line
                for line in matches.splitlines()
                if "test_10_deleted_modules_absent.py" not in line and "test_knowledge_pruning.py" not in line
            ]
            assert not filtered, f"Found import references to deleted module pattern '{pattern}':\n" + "\n".join(
                filtered
            )
