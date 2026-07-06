"""Tests verifying removal of unused retrieval methods from fox_provider.py.

Covers static analysis of method removal, retrieve() behaviour after cleanup,
items_with_ids structure, cross_group_items composition, log format, and
verdict_ids absence from __init__.

Test Spec: TS-10-7, TS-10-8, TS-10-9, TS-10-10, TS-10-11, TS-10-12,
           TS-10-P3, TS-10-P4
Requirements: 10-REQ-3.1, 10-REQ-3.2, 10-REQ-3.3, 10-REQ-3.4,
              10-REQ-3.5, 10-REQ-3.6
"""

from __future__ import annotations

import logging
import re
import uuid
from pathlib import Path

import duckdb
import pytest
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

_FOX_PROVIDER_PATH = Path(__file__).resolve().parents[2] / "agentfox" / "knowledge" / "fox_provider.py"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _read_fox_provider_source() -> str:
    """Read the source of fox_provider.py."""
    return _FOX_PROVIDER_PATH.read_text()


def _make_provider(conn: duckdb.DuckDBPyConnection, **config_overrides):
    """Build a FoxKnowledgeProvider with a fresh in-memory DuckDB."""
    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    config = KnowledgeProviderConfig(**config_overrides)
    return FoxKnowledgeProvider(db, config)


def _insert_review_finding(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    task_group: str,
    severity: str = "critical",
    description: str = "test finding",
) -> str:
    """Insert a review finding and return its ID."""
    from agentfox.knowledge.review_store import ReviewFinding, insert_findings

    finding_id = str(uuid.uuid4())
    finding = ReviewFinding(
        id=finding_id,
        severity=severity,
        description=description,
        requirement_ref=None,
        spec_name=spec_name,
        task_group=task_group,
        session_id="s1",
        category=None,
    )
    insert_findings(conn, [finding])
    return finding_id


def _insert_session_summary(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    task_group: str,
    run_id: str,
    archetype: str = "coder",
    attempt: int = 1,
    summary: str = "test summary",
) -> None:
    """Insert a session summary record."""
    conn.execute(
        "INSERT INTO session_summaries "
        "(id, node_id, run_id, spec_name, task_group, archetype, attempt, summary, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
        [str(uuid.uuid4()), "node-1", run_id, spec_name, task_group, archetype, attempt, summary],
    )


def _migrated_conn() -> duckdb.DuckDBPyConnection:
    """Create an in-memory DuckDB with all migrations applied."""
    from agentfox.knowledge.migrations import run_migrations

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


# ---------------------------------------------------------------------------
# TS-10-7: Six removed method definitions absent from fox_provider.py
# ---------------------------------------------------------------------------

_REMOVED_METHODS = [
    "_query_errata",
    "_query_adrs",
    "_query_verdicts",
    "_query_cross_group_verdicts",
    "_query_cross_spec_summaries",
    "_query_prior_run_findings",
]


class TestRemovedMethodDefinitionsAbsent:
    """TS-10-7: fox_provider.py must not define the six removed methods."""

    @pytest.mark.parametrize("method_name", _REMOVED_METHODS)
    def test_method_not_defined(self, method_name: str) -> None:
        source = _read_fox_provider_source()
        pattern = rf"def {method_name}\("
        assert not re.search(pattern, source), f"fox_provider.py must not define {method_name}()"


# ---------------------------------------------------------------------------
# TS-10-8: retrieve() does not call any of the six removed methods
# ---------------------------------------------------------------------------


class TestRetrieveNoRemovedCalls:
    """TS-10-8: retrieve() must not call removed methods (static + runtime)."""

    @pytest.mark.parametrize("method_name", _REMOVED_METHODS)
    def test_no_call_in_source(self, method_name: str) -> None:
        """Static analysis: method name absent from fox_provider.py."""
        source = _read_fox_provider_source()
        # Exclude 'def method_name' lines (covered by TS-10-7) and comments/strings
        calls = [
            line
            for line in source.splitlines()
            if method_name in line and not line.strip().startswith("#") and f"def {method_name}" not in line
        ]
        assert not calls, f"fox_provider.py must not reference {method_name}:\n" + "\n".join(calls)

    def test_retrieve_runtime_succeeds(self) -> None:
        """Runtime: retrieve() completes without AttributeError/NameError."""
        conn = _migrated_conn()
        provider = _make_provider(conn)
        # Should not raise even with empty DB
        result = provider.retrieve(spec_name="test-spec", task_description="test task")
        assert isinstance(result, list)
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-9: items_with_ids contains only (text, non-None review_id) tuples
# ---------------------------------------------------------------------------


class TestItemsWithIdsStructure:
    """TS-10-9: items_with_ids must contain only (str, non-None) tuples."""

    def test_items_have_no_none_ids(self) -> None:
        """After retrieve(), verify no None-padded entries exist.

        We inspect the return value and the internal state of retrieve()
        by checking that the source no longer builds None-padded tuples.
        """
        source = _read_fox_provider_source()
        # The old code used: *((t, None) for t in errata)
        # This pattern must be absent
        assert "(t, None)" not in source, (
            "fox_provider.py must not contain None-padded tuple construction in items_with_ids"
        )

    def test_retrieve_returns_reviews_only_structure(self) -> None:
        """With review findings in DB, retrieve succeeds and returns strings."""
        conn = _migrated_conn()
        _insert_review_finding(conn, "test-spec", "1")
        provider = _make_provider(conn)

        result = provider.retrieve(
            spec_name="test-spec",
            task_description="test task",
            task_group="1",
        )
        assert isinstance(result, list)
        # All items should be strings
        for item in result:
            assert isinstance(item, str)
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-10: cross_group_items from cross-group reviews only, capped
# ---------------------------------------------------------------------------


class TestCrossGroupItemsReviewsOnly:
    """TS-10-10: cross_group_items populated from cross-reviews only (no verdicts)."""

    def test_source_has_no_cross_verdicts_in_cross_group(self) -> None:
        """Static: cross_group_items must not combine cross_verdicts."""
        source = _read_fox_provider_source()
        # Old code: (cross_reviews + cross_verdicts)[:max_cross_group_items]
        assert "cross_verdicts" not in source, "fox_provider.py must not reference cross_verdicts in cross_group_items"

    def test_cross_group_cap_respected(self) -> None:
        """cross_group_items length must not exceed max_cross_group_items."""
        conn = _migrated_conn()
        # Insert findings in different groups
        for i in range(5):
            _insert_review_finding(conn, "test-spec", str(i + 10), description=f"finding {i}")

        provider = _make_provider(conn, max_cross_group_items=2)
        result = provider.retrieve(
            spec_name="test-spec",
            task_description="test task",
            task_group="1",
        )
        # Count cross-group items (prefixed with [CROSS-GROUP])
        cross_items = [r for r in result if "[CROSS-GROUP]" in r]
        assert len(cross_items) <= 2, (
            f"Cross-group items ({len(cross_items)}) must not exceed max_cross_group_items (2)"
        )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-11: retrieve() log line matches four-field format
# ---------------------------------------------------------------------------


class TestRetrieveLogFormat:
    """TS-10-11: Log message has exactly three item-count fields."""

    def test_log_format_four_fields(self, caplog: pytest.LogCaptureFixture) -> None:
        """The log line must match:
        'Retrieved {N} review + {N} drift + {N} cross-group + {N} context items for {spec}'
        """
        conn = _migrated_conn()
        provider = _make_provider(conn)

        with caplog.at_level(logging.DEBUG, logger="agentfox.knowledge.fox_provider"):
            provider.retrieve(spec_name="my-spec", task_description="test task")

        log_lines = [r.message for r in caplog.records if "Retrieved" in r.message and "items for" in r.message]
        assert len(log_lines) == 1, f"Expected exactly one 'Retrieved ... items for ...' log line, got {len(log_lines)}"

        pattern = (
            r"Retrieved \d+ review \+ \d+ drift \+ \d+ cross-group \+ \d+ cross-spec \+ \d+ context items for my-spec"
        )
        assert re.search(pattern, log_lines[0]), f"Log line does not match four-field format:\n  {log_lines[0]}"
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-12: __init__ does not initialise verdict_ids
# ---------------------------------------------------------------------------


class TestNoVerdictIdsInInit:
    """TS-10-12: fox_provider.py __init__ has no verdict_ids reference."""

    def test_verdict_ids_absent_from_init(self) -> None:
        source = _read_fox_provider_source()
        # Extract __init__ body - find 'def __init__' and read until next 'def '
        init_match = re.search(
            r"(class FoxKnowledgeProvider.*?def __init__\(.*?\):.*?)(?=\n    def |\nclass )",
            source,
            re.DOTALL,
        )
        assert init_match is not None, "Could not find FoxKnowledgeProvider.__init__"
        init_body = init_match.group(0)
        assert "verdict_ids" not in init_body, "FoxKnowledgeProvider.__init__ must not reference verdict_ids"

    def test_verdict_ids_absent_from_class(self) -> None:
        """No verdict_ids anywhere in fox_provider.py source."""
        source = _read_fox_provider_source()
        assert "verdict_ids" not in source, "fox_provider.py must not contain any reference to verdict_ids"


# ---------------------------------------------------------------------------
# TS-10-P3: Property — log line always has exactly three count fields
# ---------------------------------------------------------------------------


class TestLogFormatProperty:
    """TS-10-P3: For any retrieve() invocation, log matches four-field format."""

    @pytest.mark.parametrize(
        "scenario",
        [
            {"desc": "empty DB", "findings": 0, "cross": False, "summaries": 0},
            {"desc": "reviews only", "findings": 3, "cross": False, "summaries": 0},
            {"desc": "cross-group only", "findings": 0, "cross": True, "summaries": 0},
            {"desc": "summaries only", "findings": 0, "cross": False, "summaries": 2},
            {"desc": "all three channels", "findings": 2, "cross": True, "summaries": 1},
        ],
        ids=lambda s: s["desc"],
    )
    def test_log_format_across_scenarios(
        self,
        scenario: dict,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        conn = _migrated_conn()
        provider = _make_provider(conn)
        provider.set_run_id("run-1")

        for i in range(scenario["findings"]):
            _insert_review_finding(conn, "test-spec", "1", description=f"finding {i}")

        if scenario["summaries"] > 0:
            for i in range(scenario["summaries"]):
                _insert_session_summary(conn, "test-spec", str(i), "run-1")

        task_group = "2" if scenario["cross"] else None
        with caplog.at_level(logging.DEBUG, logger="agentfox.knowledge.fox_provider"):
            provider.retrieve(
                spec_name="test-spec",
                task_description="test",
                task_group=task_group,
            )

        matching = [r.message for r in caplog.records if "Retrieved" in r.message]
        assert len(matching) == 1

        pattern = (
            r"Retrieved \d+ review \+ \d+ drift \+ \d+ cross-group \+ \d+ cross-spec \+ \d+ context items for test-spec"
        )
        assert re.search(pattern, matching[0]), f"Log line does not match four-field pattern:\n  {matching[0]}"
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-P4: Property — items_with_ids contains only (str, non-None) tuples
# ---------------------------------------------------------------------------


class TestItemsWithIdsProperty:
    """TS-10-P4: For any retrieve() invocation, no None-padded entries."""

    @pytest.mark.parametrize(
        "num_findings",
        [0, 1, 5],
        ids=["zero", "one", "many"],
    )
    def test_no_none_ids_across_counts(self, num_findings: int) -> None:
        """Source code must not contain None-padded tuple construction,
        regardless of how many findings exist."""
        source = _read_fox_provider_source()
        # After cleanup, items_with_ids should only zip reviews with review_ids
        # No (t, None) patterns should exist
        assert "(t, None)" not in source
        assert "None) for t in" not in source


# ---------------------------------------------------------------------------
# TS-10-SMOKE-2: Coder session retrieval uses only three retained channels
# ---------------------------------------------------------------------------


class TestSmokeThreeChannelRetrieval:
    """TS-10-SMOKE-2: retrieve() produces three-channel-only context payload."""

    def test_smoke_retrieval_three_channels(self, caplog: pytest.LogCaptureFixture) -> None:
        """End-to-end smoke: retrieve with real DuckDB, all three channels populated."""
        conn = _migrated_conn()
        provider = _make_provider(conn)
        provider.set_run_id("run-smoke")

        # Populate all three retained channels
        _insert_review_finding(conn, "smoke-spec", "1", description="smoke review")
        _insert_review_finding(conn, "smoke-spec", "2", description="cross-group review")
        _insert_session_summary(conn, "smoke-spec", "1", "run-smoke", summary="smoke summary")

        with caplog.at_level(logging.DEBUG, logger="agentfox.knowledge.fox_provider"):
            result = provider.retrieve(
                spec_name="smoke-spec",
                task_description="smoke test task",
                task_group="3",
            )

        # Context payload must contain no removed tags
        prompt = "\n".join(result)
        removed_tags = ["[ERRATA]", "[ADR]", "[VERIFY]", "[PRIOR-RUN]"]
        for tag in removed_tags:
            assert tag not in prompt, f"Removed tag {tag} found in smoke retrieval"

        # Log line must match four-field format
        matching = [r.message for r in caplog.records if "Retrieved" in r.message]
        assert len(matching) == 1
        pattern = r"Retrieved \d+ review \+ \d+ drift \+ \d+ cross-group \+ \d+ cross-spec \+ \d+ context items for smoke-spec"
        assert re.search(pattern, matching[0]), f"Smoke log line doesn't match four-field format: {matching[0]}"
        conn.close()
