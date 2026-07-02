"""Meta-tests for test file location, coverage, and isolation.

TS-01-40: packages/afaudit/tests/ contains tests for all afaudit-exclusive symbols
TS-01-42: Edge case tests present for enforce_file_retention (missing dir, bad names, failures)
TS-01-44: agentfox/tests/ retains DuckDBSink and calculate_session_cost tests with updated imports
"""

from __future__ import annotations

from pathlib import Path

import pytest

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
AFAUDIT_TESTS = WORKSPACE_ROOT / "packages" / "afaudit" / "tests"
AGENTFOX_TESTS = WORKSPACE_ROOT / "packages" / "agentfox" / "tests"


def _read_all_test_content(test_dir: Path) -> str:
    """Read and concatenate all .py test file contents under a directory."""
    parts: list[str] = []
    if not test_dir.is_dir():
        return ""
    for f in test_dir.rglob("*.py"):
        try:
            parts.append(f.read_text(encoding="utf-8"))
        except OSError:
            continue
    return "\n".join(parts)


class TestAfauditTestCoverage:
    """TS-01-40: packages/afaudit/tests/ covers all afaudit-exclusive symbols.

    Requirement: 01-REQ-11.1
    """

    # All afaudit-exclusive symbols that must appear in the test suite.
    REQUIRED_SYMBOLS = [
        "AuditEvent",
        "AuditJsonlSink",
        "AgentTraceSink",
        "build_postmortem",
        "write_postmortem",
        "should_dump",
        "purge_stale_audit_files",
        "enforce_file_retention",
    ]

    def test_all_required_symbols_covered(self) -> None:
        """Each afaudit-exclusive symbol must appear in at least one test file."""
        all_content = _read_all_test_content(AFAUDIT_TESTS)
        missing = [sym for sym in self.REQUIRED_SYMBOLS if sym not in all_content]
        assert not missing, f"The following symbols are not tested in packages/afaudit/tests/: {missing}"

    def test_test_files_exist(self) -> None:
        """packages/afaudit/tests/ must contain test_*.py files."""
        test_files = list(AFAUDIT_TESTS.glob("test_*.py"))
        assert len(test_files) > 0, "No test files found in packages/afaudit/tests/"


class TestEnforceFileRetentionEdgeCaseCoverage:
    """TS-01-42: Edge case tests present for enforce_file_retention.

    Requirement: 01-REQ-11.3
    """

    def test_missing_dir_edge_case_covered(self) -> None:
        """A test must exist for enforce_file_retention with a missing audit_dir."""
        all_content = _read_all_test_content(AFAUDIT_TESTS)
        # Check for patterns indicating the missing-directory edge case.
        has_coverage = (
            "nonexistent" in all_content.lower() or "does_not_exist" in all_content or "missing" in all_content.lower()
        )
        assert has_coverage, (
            "No test found in packages/afaudit/tests/ covering the enforce_file_retention missing-directory edge case"
        )

    def test_unparseable_filename_edge_case_covered(self) -> None:
        """A test must exist for enforce_file_retention with unparseable filenames."""
        all_content = _read_all_test_content(AFAUDIT_TESTS)
        has_coverage = (
            "unparseable" in all_content.lower() or "BADNAME" in all_content or "invalid" in all_content.lower()
        )
        assert has_coverage, (
            "No test found in packages/afaudit/tests/ covering the "
            "enforce_file_retention unparseable-filename edge case"
        )

    def test_deletion_failure_edge_case_covered(self) -> None:
        """A test must exist for enforce_file_retention with file deletion failures."""
        all_content = _read_all_test_content(AFAUDIT_TESTS)
        has_coverage = (
            "PermissionError" in all_content
            or "permission" in all_content.lower()
            or "deletion fail" in all_content.lower()
            or "mock_unlink" in all_content
        )
        assert has_coverage, (
            "No test found in packages/afaudit/tests/ covering the enforce_file_retention deletion-failure edge case"
        )


@pytest.mark.integration
class TestAgentfoxTestsRetainHeavyTests:
    """TS-01-44: agentfox/tests/ retains DuckDBSink and calculate_session_cost tests.

    Requirement: 01-REQ-11.5
    """

    def test_duckdb_sink_tests_remain_in_agentfox(self) -> None:
        """agentfox/tests/ must still contain tests for DuckDBSink."""
        all_content = _read_all_test_content(AGENTFOX_TESTS)
        assert "DuckDBSink" in all_content, (
            "DuckDBSink tests should remain in agentfox/tests/ (they require agentfox infrastructure)"
        )

    def test_calculate_session_cost_tests_remain_in_agentfox(self) -> None:
        """agentfox/tests/ must still contain tests for calculate_session_cost."""
        all_content = _read_all_test_content(AGENTFOX_TESTS)
        assert "calculate_session_cost" in all_content, (
            "calculate_session_cost tests should remain in agentfox/tests/ "
            "(it depends on agentfox-internal pricing models)"
        )

    def test_agentfox_tests_use_afaudit_imports(self) -> None:
        """agentfox/tests/ files that reference audit symbols must import from afaudit.

        After migration, any agentfox test file that uses AuditEvent,
        SessionSink, or similar symbols must import them from afaudit.*,
        not from the old agentfox.knowledge.* paths.
        """
        old_import_patterns = [
            "from agentfox.knowledge.audit import",
            "from agentfox.knowledge.sink import",
            "from agentfox.knowledge.agent_trace import",
        ]
        for test_file in AGENTFOX_TESTS.rglob("*.py"):
            try:
                content = test_file.read_text(encoding="utf-8")
            except OSError:
                continue
            # Only check files that reference audit symbols
            has_audit_symbols = any(
                sym in content for sym in ["AuditEvent", "SessionSink", "SessionOutcome", "AgentTraceSink"]
            )
            if not has_audit_symbols:
                continue
            for old_pattern in old_import_patterns:
                assert old_pattern not in content, (
                    f"agentfox test file {test_file.relative_to(WORKSPACE_ROOT)} "
                    f"still uses old import path: '{old_pattern}'. "
                    f"Should import from afaudit.* instead."
                )
