"""Tests verifying removed-channel prompt tags are absent from assembly code
and that retained channels still produce correct output.

Test Spec: TS-10-22, TS-10-23, TS-10-P5
Requirements: 10-REQ-7.1, 10-REQ-7.2
"""

from __future__ import annotations

import uuid
from pathlib import Path

import duckdb
import pytest
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

_REPO_ROOT = Path(__file__).resolve().parents[4]
_FOX_PROVIDER_PATH = (
    _REPO_ROOT / "packages" / "agentfox" / "agentfox" / "knowledge" / "fox_provider.py"
)

# Tags from the five removed channels
_REMOVED_TAGS = ["[ERRATA]", "[ADR]", "[VERIFY]", "[CROSS-SPEC]", "[PRIOR-RUN]"]


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_provider(conn: duckdb.DuckDBPyConnection, **config_overrides):
    """Build a FoxKnowledgeProvider with a fresh in-memory DuckDB."""
    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    config = KnowledgeProviderConfig(**config_overrides)
    return FoxKnowledgeProvider(db, config)


def _migrated_conn() -> duckdb.DuckDBPyConnection:
    from agentfox.knowledge.migrations import run_migrations

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _insert_review_finding(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    task_group: str,
    severity: str = "critical",
    description: str = "test finding",
) -> str:
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
    conn.execute(
        "INSERT INTO session_summaries "
        "(id, node_id, run_id, spec_name, task_group, archetype, attempt, summary, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
        [str(uuid.uuid4()), "node-1", run_id, spec_name, task_group, archetype, attempt, summary],
    )


# ---------------------------------------------------------------------------
# TS-10-22: Assembled prompts contain no removed channel tags
# ---------------------------------------------------------------------------


class TestNoRemovedChannelTags:
    """TS-10-22: Assembled coder prompts contain none of the removed tags."""

    def test_empty_db_no_removed_tags(self) -> None:
        """With an empty DB, retrieve() returns no removed-channel tags."""
        conn = _migrated_conn()
        provider = _make_provider(conn)
        result = provider.retrieve(spec_name="test-spec", task_description="test task")

        prompt = "\n".join(result)
        for tag in _REMOVED_TAGS:
            assert tag not in prompt, (
                f"Assembled prompt must not contain removed tag {tag}"
            )
        conn.close()

    def test_populated_db_no_removed_tags(self) -> None:
        """With data in all retained channels, no removed tags appear."""
        conn = _migrated_conn()
        provider = _make_provider(conn)
        provider.set_run_id("run-1")

        # Populate retained channels
        _insert_review_finding(conn, "test-spec", "1")
        _insert_review_finding(conn, "test-spec", "2", description="cross-group finding")
        _insert_session_summary(conn, "test-spec", "1", "run-1")

        result = provider.retrieve(
            spec_name="test-spec",
            task_description="test task",
            task_group="3",
        )

        prompt = "\n".join(result)
        for tag in _REMOVED_TAGS:
            assert tag not in prompt, (
                f"Assembled prompt must not contain removed tag {tag}"
            )
        conn.close()


# ---------------------------------------------------------------------------
# TS-10-23: Three retained channels are present when data is available
# ---------------------------------------------------------------------------


class TestRetainedChannelsPresent:
    """TS-10-23: Review, cross-group review, and same-spec summary sections present."""

    def test_all_three_channels_present(self) -> None:
        """When data exists in all three retained channels, their sections appear."""
        conn = _migrated_conn()
        provider = _make_provider(conn)
        provider.set_run_id("run-1")

        # 1. Review findings for task_group "3"
        _insert_review_finding(conn, "test-spec", "3", description="direct review finding")

        # 2. Cross-group findings from another group
        _insert_review_finding(conn, "test-spec", "1", description="cross-group review")

        # 3. Same-spec summary from an earlier group
        _insert_session_summary(conn, "test-spec", "1", "run-1", summary="earlier group summary")

        result = provider.retrieve(
            spec_name="test-spec",
            task_description="test task",
            task_group="3",
        )

        # Check [REVIEW] items present
        has_review = any("[REVIEW]" in item for item in result)
        assert has_review, "Retained [REVIEW] channel must be present with review data"

        # Check [CROSS-GROUP] items present
        has_cross_group = any("[CROSS-GROUP]" in item for item in result)
        assert has_cross_group, (
            "Retained cross-group review channel must be present with cross-group data"
        )

        # Check [CONTEXT] items present
        has_context = any("[CONTEXT]" in item for item in result)
        assert has_context, (
            "Retained [CONTEXT] same-spec summary channel must be present with summary data"
        )

        conn.close()


# ---------------------------------------------------------------------------
# TS-10-P5: Property — removed tags never appear across varied scenarios
# ---------------------------------------------------------------------------


class TestRemovedTagsPropertyTest:
    """TS-10-P5: No removed tag appears in any prompt across diverse scenarios."""

    @pytest.mark.parametrize(
        "scenario",
        [
            {"desc": "empty DB", "findings": 0, "summaries": 0, "cross": False},
            {"desc": "reviews only", "findings": 3, "summaries": 0, "cross": False},
            {"desc": "summaries only", "findings": 0, "summaries": 2, "cross": False},
            {"desc": "all three", "findings": 2, "summaries": 1, "cross": True},
        ],
        ids=lambda s: s["desc"],
    )
    def test_no_removed_tags(self, scenario: dict) -> None:
        conn = _migrated_conn()
        provider = _make_provider(conn)
        provider.set_run_id("run-1")

        for i in range(scenario["findings"]):
            group = "1" if not scenario["cross"] else str(i + 10)
            _insert_review_finding(conn, "test-spec", group, description=f"finding {i}")

        for i in range(scenario["summaries"]):
            _insert_session_summary(conn, "test-spec", str(i), "run-1")

        task_group = "5" if scenario["cross"] else None
        result = provider.retrieve(
            spec_name="test-spec",
            task_description="test task",
            task_group=task_group,
        )

        prompt = "\n".join(result)
        for tag in _REMOVED_TAGS:
            assert tag not in prompt, (
                f"Removed tag {tag} found in scenario '{scenario['desc']}'"
            )
        conn.close()
