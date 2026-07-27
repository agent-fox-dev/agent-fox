"""Unit tests for relevance-based summary ranking in FoxKnowledgeProvider.

Test Spec: TS-NS-1 through TS-NS-5
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5

Covers relevance filtering of session summary injection based on
file-footprint overlap between the current task group and prior groups.
"""

from __future__ import annotations

import uuid
from pathlib import Path
from unittest.mock import patch

import duckdb
import pytest
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.summary_store import (
    SummaryRecord,
    insert_summary,
)

_SESSION_SUMMARIES_DDL = """
CREATE TABLE IF NOT EXISTS session_summaries (
    id          UUID PRIMARY KEY,
    node_id     VARCHAR NOT NULL,
    run_id      VARCHAR NOT NULL,
    spec_name   VARCHAR NOT NULL,
    task_group  VARCHAR NOT NULL,
    archetype   VARCHAR NOT NULL,
    attempt     INTEGER NOT NULL DEFAULT 1,
    summary     TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL
);
"""


@pytest.fixture()
def provider_conn():
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    conn.execute(_SESSION_SUMMARIES_DDL)
    yield conn
    conn.close()


@pytest.fixture()
def provider_db(provider_conn):
    from agentfox.knowledge.db import KnowledgeDB

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = provider_conn
    return db


def _make_record(
    *,
    id=None,
    node_id="spec_a:1",
    run_id="run-1",
    spec_name="spec_a",
    task_group="1",
    archetype="coder",
    attempt=1,
    summary="Summary for group",
    created_at="2026-04-28T18:00:00",
):
    return SummaryRecord(
        id=id or str(uuid.uuid4()),
        node_id=node_id,
        run_id=run_id,
        spec_name=spec_name,
        task_group=task_group,
        archetype=archetype,
        attempt=attempt,
        summary=summary,
        created_at=created_at,
    )


def _make_provider(provider_db, run_id=None, spec_dir=None, max_summary_items=20):
    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    config = KnowledgeProviderConfig(max_summary_items=max_summary_items)
    provider = FoxKnowledgeProvider(provider_db, config)
    if run_id is not None:
        provider._run_id = run_id
    if spec_dir is not None:
        provider.set_spec_dir(spec_dir)
    return provider


def _insert_groups(conn, groups, *, spec_name="spec_a", run_id="run-1"):
    """Insert one coder summary for each group number in *groups*."""
    for g in groups:
        insert_summary(
            conn,
            _make_record(
                spec_name=spec_name,
                run_id=run_id,
                task_group=str(g),
                node_id=f"{spec_name}:{g}",
                summary=f"Summary for group {g}",
            ),
        )


def _extract_group_numbers(items):
    """Extract group numbers from [CONTEXT] formatted items."""
    import re

    groups = []
    for item in items:
        m = re.search(r"group (\d+)", item)
        if m:
            groups.append(int(m.group(1)))
    return groups


# TS-NS-1: Summaries with higher file-path overlap are ranked first
class TestRelevanceRanking:
    """NS-REQ-1: Higher file overlap groups are ranked before lower."""

    def test_high_overlap_groups_ranked_first(self, provider_db, provider_conn):
        """Groups 2 and 4 overlap with current footprint; groups 1, 3, 5 do not.

        The returned [CONTEXT] items should place groups 2 and 4 at the
        top of the list, before groups 1, 3, and 5.

        Note: the preceding group (group 5) is always included (NS-REQ-2)
        but is placed first as a fixed slot; the overlap-ranked items
        follow.  Among the ranked items, overlap groups precede non-overlap.
        """
        _insert_groups(provider_conn, [1, 2, 3, 4, 5])

        # Mock extract_file_impacts: groups 2 and 4 share files with current;
        # groups 1, 3, 5 do not.
        def mock_file_impacts(spec_dir, task_group):
            if task_group == 2:
                return {"src/auth.py", "src/login.py"}
            if task_group == 4:
                return {"src/auth.py", "src/utils.py"}
            return {"src/unrelated.py"}

        provider = _make_provider(
            provider_db, run_id="run-1", spec_dir=Path("/fake/spec")
        )

        with patch(
            "agentfox.graph.file_impacts.extract_file_impacts",
            side_effect=mock_file_impacts,
        ):
            items = provider.retrieve(
                "spec_a",
                "task description",
                task_group="6",
                file_footprint=["src/auth.py", "src/login.py"],
            )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        groups = _extract_group_numbers(context_items)

        # Groups 2 and 4 (with overlap) must both appear in results.
        overlap_groups = [g for g in groups if g in (2, 4)]
        assert len(overlap_groups) == 2, f"Expected groups 2 and 4 in results: {groups}"

        # After the preceding group (5, always first), the overlap groups
        # should appear before the remaining non-overlap groups (1, 3).
        # Skip the first item (preceding group) for overlap ranking check.
        remaining = groups[1:] if groups[0] == 5 else groups
        remaining_overlap = [g for g in remaining if g in (2, 4)]
        remaining_non_overlap = [g for g in remaining if g in (1, 3)]

        if remaining_overlap and remaining_non_overlap:
            last_overlap_idx = max(remaining.index(g) for g in remaining_overlap)
            first_non_overlap_idx = min(remaining.index(g) for g in remaining_non_overlap)
            assert last_overlap_idx < first_non_overlap_idx, (
                f"Overlap groups should precede non-overlap in ranked portion; "
                f"full order: {groups}"
            )


# TS-NS-2: Preceding group always included
class TestPrecedingGroupAlwaysIncluded:
    """NS-REQ-2: The immediately preceding group's summary is always included."""

    def test_preceding_group_included_despite_zero_overlap(
        self, provider_db, provider_conn
    ):
        """Group 4 has zero overlap but should still appear because it is
        the immediately preceding group (current = 5).
        """
        _insert_groups(provider_conn, [1, 2, 3, 4])

        # Mock: only group 2 overlaps; group 4 (preceding) has zero overlap.
        def mock_file_impacts(spec_dir, task_group):
            if task_group == 2:
                return {"src/auth.py"}
            return set()

        provider = _make_provider(
            provider_db, run_id="run-1", spec_dir=Path("/fake/spec")
        )

        with patch(
            "agentfox.graph.file_impacts.extract_file_impacts",
            side_effect=mock_file_impacts,
        ):
            items = provider.retrieve(
                "spec_a",
                "task description",
                task_group="5",
                file_footprint=["src/auth.py"],
            )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        groups = _extract_group_numbers(context_items)

        assert 4 in groups, (
            f"Group 4 (immediately preceding) should be included despite zero overlap; "
            f"got groups: {groups}"
        )


# TS-NS-3: Total summaries respect max_summary_items
class TestMaxSummaryItemsRespected:
    """NS-REQ-3: Total injected summaries never exceeds max_summary_items."""

    def test_cap_respected_with_many_groups(self, provider_db, provider_conn):
        """Insert 10 prior-group summaries, cap at 3, verify at most 3."""
        _insert_groups(provider_conn, range(1, 11))

        def mock_file_impacts(spec_dir, task_group):
            return {"src/common.py"}

        provider = _make_provider(
            provider_db,
            run_id="run-1",
            spec_dir=Path("/fake/spec"),
            max_summary_items=3,
        )

        with patch(
            "agentfox.graph.file_impacts.extract_file_impacts",
            side_effect=mock_file_impacts,
        ):
            items = provider.retrieve(
                "spec_a",
                "task description",
                task_group="11",
                file_footprint=["src/common.py"],
            )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        assert len(context_items) <= 3, (
            f"Expected at most 3 [CONTEXT] items; got {len(context_items)}"
        )


# TS-NS-4: Fallback to original ordering when file_footprint is None/empty
class TestFallbackOrdering:
    """NS-REQ-4: Falls back to ascending task-group order when no footprint."""

    def test_none_footprint_preserves_ascending_order(
        self, provider_db, provider_conn
    ):
        """file_footprint=None -> ascending group order (1, 2, 3)."""
        _insert_groups(provider_conn, [1, 2, 3])

        provider = _make_provider(provider_db, run_id="run-1")

        items = provider.retrieve(
            "spec_a",
            "task description",
            task_group="4",
            file_footprint=None,
        )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        groups = _extract_group_numbers(context_items)

        assert groups == [1, 2, 3], (
            f"Expected ascending order [1, 2, 3] with None footprint; got {groups}"
        )

    def test_empty_footprint_preserves_ascending_order(
        self, provider_db, provider_conn
    ):
        """file_footprint=[] -> ascending group order (1, 2, 3)."""
        _insert_groups(provider_conn, [1, 2, 3])

        provider = _make_provider(provider_db, run_id="run-1")

        items = provider.retrieve(
            "spec_a",
            "task description",
            task_group="4",
            file_footprint=[],
        )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        groups = _extract_group_numbers(context_items)

        assert groups == [1, 2, 3], (
            f"Expected ascending order [1, 2, 3] with empty footprint; got {groups}"
        )


# TS-NS-5: Graceful handling of extract_file_impacts failures
class TestExtractFileImpactsFailure:
    """NS-REQ-5: No exception when extract_file_impacts raises for a group."""

    def test_exception_treated_as_zero_overlap(self, provider_db, provider_conn):
        """When extract_file_impacts raises for group 1, that group is
        treated as zero overlap and the call returns normally.
        """
        _insert_groups(provider_conn, [1, 2, 3])

        def mock_file_impacts(spec_dir, task_group):
            if task_group == 1:
                raise RuntimeError("Spec dir missing")
            if task_group == 2:
                return {"src/auth.py"}
            return set()

        provider = _make_provider(
            provider_db, run_id="run-1", spec_dir=Path("/fake/spec")
        )

        with patch(
            "agentfox.graph.file_impacts.extract_file_impacts",
            side_effect=mock_file_impacts,
        ):
            items = provider.retrieve(
                "spec_a",
                "task description",
                task_group="4",
                file_footprint=["src/auth.py"],
            )

        context_items = [i for i in items if i.startswith("[CONTEXT]")]
        groups = _extract_group_numbers(context_items)

        # No exception was raised, and groups 2 and 3 still appear.
        assert 2 in groups, f"Group 2 should appear; got {groups}"
        assert 3 in groups, f"Group 3 should appear; got {groups}"
        # Group 1 should also appear (just with zero overlap score).
        assert 1 in groups, f"Group 1 should still appear with zero overlap; got {groups}"
