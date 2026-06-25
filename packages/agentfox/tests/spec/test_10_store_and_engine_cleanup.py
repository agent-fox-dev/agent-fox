"""Tests verifying removal of unused functions from review_store.py,
summary_store.py, and engine modules.

Covers static analysis of function definitions, call site removal,
and engine module cleanup.

Test Spec: TS-10-13, TS-10-14, TS-10-15, TS-10-16, TS-10-17,
           TS-10-18, TS-10-19, TS-10-20, TS-10-21
Requirements: 10-REQ-4.1, 10-REQ-4.2, 10-REQ-5.1, 10-REQ-5.2,
              10-REQ-5.3, 10-REQ-6.1, 10-REQ-6.2, 10-REQ-6.3, 10-REQ-6.4
"""

from __future__ import annotations

import subprocess
import uuid
from pathlib import Path

import duckdb
import pytest

_REPO_ROOT = Path(__file__).resolve().parents[4]
_PACKAGES_ROOT = _REPO_ROOT / "packages" / "agentfox"

# Paths to source files under test
_REVIEW_STORE = _PACKAGES_ROOT / "agentfox" / "knowledge" / "review_store.py"
_SUMMARY_STORE = _PACKAGES_ROOT / "agentfox" / "knowledge" / "summary_store.py"
_RUN_PY = _PACKAGES_ROOT / "agentfox" / "engine" / "run.py"
_RESULT_HANDLER = _PACKAGES_ROOT / "agentfox" / "engine" / "result_handler.py"
_SESSION_LIFECYCLE = _PACKAGES_ROOT / "agentfox" / "engine" / "session_lifecycle.py"
_NIGHTSHIFT_STARTUP = _REPO_ROOT / "packages" / "nightshift" / "nightshift" / "_startup.py"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _grep_file(file_path: Path, pattern: str) -> list[str]:
    """Grep a file for a pattern, returning matching lines."""
    if not file_path.exists():
        return []
    content = file_path.read_text()
    return [
        line
        for line in content.splitlines()
        if pattern in line
    ]


def _grep_all_python(pattern: str, *, exclude_test_10: bool = True) -> list[str]:
    """Grep all Python files under packages/agentfox for a pattern."""
    result = subprocess.run(
        [
            "grep",
            "-rn",
            "--include=*.py",
            pattern,
            str(_PACKAGES_ROOT),
        ],
        capture_output=True,
        text=True,
    )
    lines = result.stdout.strip().splitlines() if result.stdout.strip() else []
    if exclude_test_10:
        lines = [line for line in lines if "test_10_" not in line]
    return lines


# ---------------------------------------------------------------------------
# TS-10-13: review_store.py does not define the six removed functions
# ---------------------------------------------------------------------------

_REMOVED_REVIEW_STORE_FNS = [
    "query_active_verdicts",
    "query_cross_group_verdicts",
    "query_prior_run_findings",
    "query_prior_run_verdicts",
    "insert_verdicts",
    "validate_verdict",
]


class TestReviewStoreRemovedFunctions:
    """TS-10-13: review_store.py must not define the six removed functions."""

    @pytest.mark.parametrize("fn_name", _REMOVED_REVIEW_STORE_FNS)
    def test_function_not_defined(self, fn_name: str) -> None:
        matches = _grep_file(_REVIEW_STORE, f"def {fn_name}")
        assert not matches, (
            f"review_store.py must not define {fn_name}():\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-14: No call sites for removed review_store functions in codebase
# ---------------------------------------------------------------------------


class TestNoReviewStoreCallSites:
    """TS-10-14: Zero call sites for the six removed functions."""

    @pytest.mark.parametrize("fn_name", _REMOVED_REVIEW_STORE_FNS)
    def test_no_call_sites(self, fn_name: str) -> None:
        matches = _grep_all_python(fn_name)
        assert not matches, (
            f"Found call sites for removed function {fn_name}:\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-15: summary_store.py does not define query_cross_spec_summaries
# ---------------------------------------------------------------------------


class TestQueryCrossSpecSummariesRemoved:
    """TS-10-15: query_cross_spec_summaries not defined in summary_store.py."""

    def test_function_not_defined(self) -> None:
        matches = _grep_file(_SUMMARY_STORE, "def query_cross_spec_summaries")
        assert not matches, (
            "summary_store.py must not define query_cross_spec_summaries():\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-16: No call sites for query_cross_spec_summaries in codebase
# ---------------------------------------------------------------------------


class TestNoQueryCrossSpecCallSites:
    """TS-10-16: Zero call sites for query_cross_spec_summaries."""

    def test_no_call_sites(self) -> None:
        matches = _grep_all_python("query_cross_spec_summaries")
        assert not matches, (
            "Found call sites for query_cross_spec_summaries:\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-17: query_same_spec_summaries is retained and functional
# ---------------------------------------------------------------------------


class TestQuerySameSpecSummariesRetained:
    """TS-10-17: query_same_spec_summaries is present and queryable."""

    def test_function_defined(self) -> None:
        matches = _grep_file(_SUMMARY_STORE, "def query_same_spec_summaries")
        assert matches, "query_same_spec_summaries() must remain defined in summary_store.py"

    def test_queries_session_summaries(self) -> None:
        """query_same_spec_summaries returns rows from session_summaries."""
        from agentfox.knowledge.migrations import run_migrations
        from agentfox.knowledge.summary_store import query_same_spec_summaries

        conn = duckdb.connect(":memory:")
        run_migrations(conn)

        # Insert a test row
        conn.execute(
            "INSERT INTO session_summaries "
            "(id, node_id, run_id, spec_name, task_group, archetype, attempt, summary, created_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
            [str(uuid.uuid4()), "node-1", "run-1", "test-spec", "1", "coder", 1, "test summary"],
        )

        # Query for task_group "2" should see task_group "1" summaries
        results = query_same_spec_summaries(
            conn, "test-spec", "2", "run-1"
        )
        assert len(results) >= 1, (
            "query_same_spec_summaries must return rows from session_summaries"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-18: run.py contains no call to index_errata_from_markdown
# ---------------------------------------------------------------------------


class TestRunPyNoErrataIndexing:
    """TS-10-18: Zero occurrences of index_errata_from_markdown in run.py."""

    def test_no_errata_reference(self) -> None:
        matches = _grep_file(_RUN_PY, "index_errata_from_markdown")
        assert not matches, (
            "run.py must not reference index_errata_from_markdown:\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-19: nightshift/_startup.py contains no call to index_errata_from_markdown
# ---------------------------------------------------------------------------


class TestNightshiftStartupNoErrataIndexing:
    """TS-10-19: Zero occurrences of index_errata_from_markdown in _startup.py."""

    def test_no_errata_reference(self) -> None:
        matches = _grep_file(_NIGHTSHIFT_STARTUP, "index_errata_from_markdown")
        assert not matches, (
            "nightshift/_startup.py must not reference index_errata_from_markdown:\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-20: result_handler.py neither defines nor calls _generate_errata
# ---------------------------------------------------------------------------


class TestResultHandlerNoGenerateErrata:
    """TS-10-20: Zero occurrences of _generate_errata in result_handler.py."""

    def test_no_generate_errata(self) -> None:
        matches = _grep_file(_RESULT_HANDLER, "_generate_errata")
        assert not matches, (
            "result_handler.py must not define or call _generate_errata:\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-21: session_lifecycle.py contains no reference to index_errata_from_markdown
# ---------------------------------------------------------------------------


class TestSessionLifecycleNoErrataReference:
    """TS-10-21: Zero occurrences of 'index_errata_from_markdown' in session_lifecycle.py."""

    def test_no_errata_reference(self) -> None:
        matches = _grep_file(_SESSION_LIFECYCLE, "index_errata_from_markdown")
        assert not matches, (
            "session_lifecycle.py must not reference index_errata_from_markdown "
            "(including comments):\n" + "\n".join(matches)
        )
