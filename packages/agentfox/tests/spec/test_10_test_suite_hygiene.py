"""Tests verifying test-suite hygiene after removed-channel cleanup.

Checks that deleted test files are absent, retained test files are cleaned
of forbidden references, and the full test suite collects without import errors.

Test Spec: TS-10-24, TS-10-25, TS-10-26, TS-10-27, TS-10-28,
           TS-10-29, TS-10-E2, TS-10-P2, TS-10-SMOKE-3
Requirements: 10-REQ-8.1, 10-REQ-8.2, 10-REQ-8.3, 10-REQ-8.4,
              10-REQ-8.5, 10-REQ-8.6, 10-REQ-8.E1,
              10-REQ-2.3, 10-REQ-3.1, 10-REQ-4.2, 10-REQ-5.2,
              10-REQ-6.1, 10-REQ-6.2, 10-REQ-6.3
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[4]
_PACKAGES_ROOT = _REPO_ROOT / "packages" / "agentfox"
_TESTS_ROOT = _PACKAGES_ROOT / "tests"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _grep_file(file_path: Path, pattern: str) -> list[str]:
    """Return lines from a file matching a pattern."""
    if not file_path.exists():
        return []
    content = file_path.read_text()
    return [line for line in content.splitlines() if pattern in line]


def _file_exists(relative_path: str) -> bool:
    """Check if a file exists relative to _TESTS_ROOT."""
    return (_TESTS_ROOT / relative_path).exists()


# ---------------------------------------------------------------------------
# TS-10-24: Six entirely deleted test files are absent
# ---------------------------------------------------------------------------

_DELETED_TEST_FILES = [
    "unit/knowledge/test_errata.py",
    "unit/knowledge/test_adr.py",
    "property/knowledge/test_adr_props.py",
    "unit/knowledge/test_verdict_normalization.py",
    "unit/knowledge/test_cross_run_carryforward.py",
    "unit/engine/test_errata_on_blocking.py",
]


class TestDeletedTestFilesAbsent:
    """TS-10-24: All six deleted test files are absent from the repo."""

    @pytest.mark.parametrize("rel_path", _DELETED_TEST_FILES)
    def test_file_absent(self, rel_path: str) -> None:
        full_path = _TESTS_ROOT / rel_path
        assert not full_path.exists(), f"Deleted test file must be absent: {rel_path}"


# ---------------------------------------------------------------------------
# TS-10-25: Cross-spec portions removed from summary/provider test files
# ---------------------------------------------------------------------------


class TestCrossSpecPortionsRemoved:
    """TS-10-25: No cross-spec references in summary-related test files."""

    _TARGET_FILES = [
        _TESTS_ROOT / "unit" / "knowledge" / "test_fox_provider_summaries.py",
        _TESTS_ROOT / "unit" / "knowledge" / "test_summary_store.py",
        _TESTS_ROOT / "integration" / "knowledge" / "test_summary_lifecycle.py",
    ]

    @pytest.mark.parametrize(
        "forbidden",
        ["query_cross_spec_summaries", "[CROSS-SPEC]"],
    )
    def test_no_cross_spec_references(self, forbidden: str) -> None:
        for test_file in self._TARGET_FILES:
            if not test_file.exists():
                continue
            matches = _grep_file(test_file, forbidden)
            assert not matches, f"{test_file.name} must not reference '{forbidden}':\n" + "\n".join(matches)


# ---------------------------------------------------------------------------
# TS-10-26: Verdict portions removed from review store test files
# ---------------------------------------------------------------------------

_FORBIDDEN_VERDICT_FNS = [
    "insert_verdicts",
    "validate_verdict",
    "query_active_verdicts",
    "query_cross_group_verdicts",
]


class TestVerdictPortionsRemoved:
    """TS-10-26: No verdict references in review_store test files."""

    _TARGET_FILES = [
        _TESTS_ROOT / "unit" / "knowledge" / "test_review_store.py",
        _TESTS_ROOT / "property" / "knowledge" / "test_review_store_props.py",
    ]

    @pytest.mark.parametrize("forbidden", _FORBIDDEN_VERDICT_FNS)
    def test_no_verdict_references(self, forbidden: str) -> None:
        for test_file in self._TARGET_FILES:
            if not test_file.exists():
                continue
            matches = _grep_file(test_file, forbidden)
            assert not matches, f"{test_file.name} must not reference '{forbidden}':\n" + "\n".join(matches)


# ---------------------------------------------------------------------------
# TS-10-27: Prior-run and ADR/errata portions removed from smoke/idempotency
# ---------------------------------------------------------------------------

_FORBIDDEN_SMOKE_TERMS = [
    "query_prior_run_findings",
    "ingest_adr",
    "index_errata_from_markdown",
    "adr_entries",
]


class TestSmokePriorRunPortionsRemoved:
    """TS-10-27: Prior-run and ADR/errata removed from smoke/idempotency files."""

    _TARGET_FILES = [
        _TESTS_ROOT / "integration" / "knowledge" / "test_retrieval_fixes_smoke.py",
        _TESTS_ROOT / "integration" / "test_duckdb_reader_writer_smoke.py",
        _TESTS_ROOT / "unit" / "session" / "test_duckdb_reader_writer_idempotency.py",
    ]

    @pytest.mark.parametrize("forbidden", _FORBIDDEN_SMOKE_TERMS)
    def test_no_forbidden_references(self, forbidden: str) -> None:
        for test_file in self._TARGET_FILES:
            if not test_file.exists():
                continue
            matches = _grep_file(test_file, forbidden)
            # Filter out this test file itself
            assert not matches, f"{test_file.name} must not reference '{forbidden}':\n" + "\n".join(matches)


# ---------------------------------------------------------------------------
# TS-10-28: Errata portions removed from context/blocking/props test files
# ---------------------------------------------------------------------------


class TestErrataPortionsRemoved:
    """TS-10-28: No errata references in context assembly and blocking test files."""

    _TARGET_FILES = [
        _TESTS_ROOT / "test_assemble_context_readonly.py",
        _TESTS_ROOT / "unit" / "engine" / "test_audit_review_blocking.py",
        _TESTS_ROOT / "property" / "knowledge" / "test_duckdb_reader_writer_props.py",
    ]

    def test_no_errata_references(self) -> None:
        for test_file in self._TARGET_FILES:
            if not test_file.exists():
                continue
            matches = _grep_file(test_file, "errata")
            assert not matches, f"{test_file.name} must not reference 'errata':\n" + "\n".join(matches)


# ---------------------------------------------------------------------------
# TS-10-E2: pytest --collect-only raises no ImportError/ModuleNotFoundError
# ---------------------------------------------------------------------------


class TestCollectionNoImportErrors:
    """TS-10-E2: All retained test files can be collected without import errors."""

    def test_pytest_collect_no_errors(self) -> None:
        """Run pytest --collect-only and check for import errors."""
        result = subprocess.run(
            ["python", "-m", "pytest", "--collect-only", "-q", str(_TESTS_ROOT)],
            capture_output=True,
            text=True,
            cwd=str(_PACKAGES_ROOT),
            timeout=120,
        )
        # Check stderr for actual import errors (not test names containing "ImportError")
        assert "ImportError" not in result.stderr, f"pytest collection raised ImportError:\n{result.stderr}"
        assert "ModuleNotFoundError" not in result.stderr, (
            f"pytest collection raised ModuleNotFoundError:\n{result.stderr}"
        )
        assert result.returncode == 0, (
            f"pytest --collect-only failed with exit code {result.returncode}:\n{result.stderr}"
        )


# ---------------------------------------------------------------------------
# TS-10-P2: No deleted symbol referenced anywhere in the codebase
# ---------------------------------------------------------------------------

_DELETED_SYMBOLS = [
    "_query_errata",
    "_query_adrs",
    "_query_verdicts",
    "_query_cross_group_verdicts",
    "_query_cross_spec_summaries",
    "_query_prior_run_findings",
    "query_active_verdicts",
    "query_cross_group_verdicts",
    "query_prior_run_findings",
    "query_prior_run_verdicts",
    "insert_verdicts",
    "validate_verdict",
    "query_cross_spec_summaries",
    "index_errata_from_markdown",
    "_generate_errata",
]


class TestNoDeletedSymbolsInCodebase:
    """TS-10-P2: No Python file references any deleted symbol."""

    @pytest.mark.parametrize("symbol", _DELETED_SYMBOLS)
    def test_symbol_absent_from_codebase(self, symbol: str) -> None:
        result = subprocess.run(
            [
                "grep",
                "-rn",
                "--include=*.py",
                symbol,
                str(_PACKAGES_ROOT),
            ],
            capture_output=True,
            text=True,
        )
        if result.stdout.strip():
            # Filter out this test file and other test_10_* spec test files
            matches = [line for line in result.stdout.strip().splitlines() if "test_10_" not in line]
            assert not matches, f"Deleted symbol '{symbol}' found in codebase:\n" + "\n".join(matches)


