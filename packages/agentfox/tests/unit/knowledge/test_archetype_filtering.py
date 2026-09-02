"""Unit tests for archetype-aware knowledge filtering in FoxKnowledgeProvider.

Test Spec: TS-NS-1 through TS-NS-5
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5

Issue #750: FoxKnowledgeProvider.retrieve() returns the same knowledge items
regardless of archetype.  Gate sessions should receive nothing; reviewer and
verifier sessions should skip [CONTEXT] summaries; verifier and gate sessions
should skip [CROSS-SPEC] drift.
"""

from __future__ import annotations

import inspect
import uuid
from typing import Any
from unittest.mock import MagicMock, patch

import duckdb
import pytest
from agentfox.core.config import KnowledgeProviderConfig
from agentfox.knowledge.db import KnowledgeDB
from agentfox.knowledge.fox_provider import (
    FoxKnowledgeProvider,
    KnowledgeProvider,
    NoOpKnowledgeProvider,
)
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.review_store import (
    DriftFinding,
    ReviewFinding,
    insert_drift_findings,
    insert_findings,
)

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def conn() -> duckdb.DuckDBPyConnection:
    """DuckDB with full migrated schema."""
    c = duckdb.connect(":memory:")
    run_migrations(c)
    yield c
    c.close()


@pytest.fixture()
def db(conn: duckdb.DuckDBPyConnection) -> KnowledgeDB:
    """KnowledgeDB wrapper around conn."""
    obj = KnowledgeDB.__new__(KnowledgeDB)
    obj._conn = conn
    return obj


@pytest.fixture()
def provider(db: KnowledgeDB) -> FoxKnowledgeProvider:
    """FoxKnowledgeProvider with default config."""
    return FoxKnowledgeProvider(db, KnowledgeProviderConfig())


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _insert_review(
    c: duckdb.DuckDBPyConnection,
    spec: str,
    severity: str,
    description: str,
    task_group: str = "1",
) -> str:
    fid = str(uuid.uuid4())
    insert_findings(
        c,
        [
            ReviewFinding(
                id=fid,
                severity=severity,
                description=description,
                requirement_ref=None,
                spec_name=spec,
                task_group=task_group,
                session_id="s-setup",
            ),
        ],
    )
    return fid


def _insert_drift(
    c: duckdb.DuckDBPyConnection,
    spec: str,
    description: str,
    artifact_ref: str | None = None,
    severity: str = "critical",
) -> str:
    fid = str(uuid.uuid4())
    insert_drift_findings(
        c,
        [
            DriftFinding(
                id=fid,
                severity=severity,
                description=description,
                spec_ref=None,
                artifact_ref=artifact_ref,
                spec_name=spec,
                task_group="0",
                session_id="s-drift",
            ),
        ],
    )
    return fid


def _insert_summary(
    c: duckdb.DuckDBPyConnection,
    spec: str,
    task_group: str,
    run_id: str,
    summary: str = "Session summary text",
    archetype: str = "coder",
    attempt: int = 1,
) -> None:
    from agentfox.knowledge.summary_store import SummaryRecord, insert_summary

    record = SummaryRecord(
        id=str(uuid.uuid4()),
        node_id=f"node-{uuid.uuid4().hex[:8]}",
        run_id=run_id,
        spec_name=spec,
        task_group=task_group,
        archetype=archetype,
        attempt=attempt,
        summary=summary,
        created_at="2026-01-01T00:00:00Z",
    )
    insert_summary(c, record)


# ===========================================================================
# TS-NS-1: retrieve() accepts optional archetype parameter (backward-compat)
# ===========================================================================


class TestArchetypeParameterSignature:
    """NS-REQ-1: All three classes declare retrieve(..., archetype: str | None = None)."""

    def test_protocol_has_archetype_parameter(self) -> None:
        sig = inspect.signature(KnowledgeProvider.retrieve)
        assert "archetype" in sig.parameters
        assert sig.parameters["archetype"].default is None

    def test_noop_has_archetype_parameter(self) -> None:
        sig = inspect.signature(NoOpKnowledgeProvider.retrieve)
        assert "archetype" in sig.parameters
        assert sig.parameters["archetype"].default is None

    def test_fox_has_archetype_parameter(self) -> None:
        sig = inspect.signature(FoxKnowledgeProvider.retrieve)
        assert "archetype" in sig.parameters
        assert sig.parameters["archetype"].default is None

    def test_backward_compat_no_archetype(self, provider: FoxKnowledgeProvider) -> None:
        """Calling retrieve() without archetype continues to work."""
        result = provider.retrieve("spec_01", "task desc")
        assert isinstance(result, list)

    def test_noop_backward_compat(self) -> None:
        """NoOp.retrieve() without archetype returns []."""
        noop = NoOpKnowledgeProvider()
        assert noop.retrieve("spec_01", "task desc") == []

    def test_noop_with_archetype(self) -> None:
        """NoOp.retrieve() with archetype still returns []."""
        noop = NoOpKnowledgeProvider()
        assert noop.retrieve("spec_01", "task desc", archetype="gate") == []

    def test_isinstance_still_satisfied(self) -> None:
        """NoOpKnowledgeProvider still satisfies KnowledgeProvider after change."""
        assert isinstance(NoOpKnowledgeProvider(), KnowledgeProvider)


# ===========================================================================
# TS-NS-2: Gate archetype skips all knowledge queries, returns []
# ===========================================================================


class TestGateArchetypeSkipsAll:
    """NS-REQ-2: Gate sessions receive an empty list with zero DB calls."""

    def test_gate_returns_empty(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Gate archetype returns [] even when data exists."""
        _insert_review(conn, "spec_01", "critical", "SQL injection")
        result = provider.retrieve("spec_01", "task", archetype="gate")
        assert result == []

    def test_gate_no_query_helpers_called(self, db: KnowledgeDB) -> None:
        """Gate archetype does not invoke any _query_* helpers."""
        p = FoxKnowledgeProvider(db, KnowledgeProviderConfig())
        with (
            patch.object(p, "_query_reviews") as m_reviews,
            patch.object(p, "_query_drift") as m_drift,
            patch.object(p, "_query_cross_group_reviews") as m_cg,
            patch.object(p, "_query_cross_spec_drift") as m_cs,
            patch.object(p, "_query_same_spec_summaries") as m_sum,
        ):
            result = p.retrieve("spec_01", "task", archetype="gate")

        assert result == []
        m_reviews.assert_not_called()
        m_drift.assert_not_called()
        m_cg.assert_not_called()
        m_cs.assert_not_called()
        m_sum.assert_not_called()

    def test_gate_with_full_args(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Gate still returns [] with all optional arguments provided."""
        _insert_review(conn, "spec_01", "critical", "finding")
        _insert_drift(conn, "other_spec", "drift", artifact_ref="src/a.py")
        result = provider.retrieve(
            "spec_01",
            "task",
            task_group="1",
            session_id="sid",
            file_footprint=["src/a.py"],
            archetype="gate",
        )
        assert result == []


# ===========================================================================
# TS-NS-3: Reviewer/verifier skip _query_same_spec_summaries ([CONTEXT])
# ===========================================================================


class TestReviewerVerifierSkipContext:
    """NS-REQ-3: Reviewer and verifier sessions have no [CONTEXT] items."""

    def test_reviewer_no_context_items(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Reviewer archetype returns no [CONTEXT] items."""
        run_id = "run-reviewer"
        provider.set_run_id(run_id)
        _insert_review(conn, "spec_01", "critical", "important finding", task_group="2")
        _insert_summary(conn, "spec_01", "1", run_id, summary="Prior session summary")

        result = provider.retrieve("spec_01", "task", task_group="2", archetype="reviewer")

        context_items = [r for r in result if r.startswith("[CONTEXT]")]
        review_items = [r for r in result if r.startswith("[REVIEW]")]
        assert context_items == [], f"Reviewer should have no [CONTEXT] items, got: {context_items}"
        assert len(review_items) == 1, "Reviewer should still see [REVIEW] items"

    def test_verifier_no_context_items(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Verifier archetype returns no [CONTEXT] items."""
        run_id = "run-verifier"
        provider.set_run_id(run_id)
        _insert_review(conn, "spec_01", "critical", "important finding", task_group="2")
        _insert_summary(conn, "spec_01", "1", run_id, summary="Prior session summary")

        result = provider.retrieve("spec_01", "task", task_group="2", archetype="verifier")

        context_items = [r for r in result if r.startswith("[CONTEXT]")]
        review_items = [r for r in result if r.startswith("[REVIEW]")]
        assert context_items == [], f"Verifier should have no [CONTEXT] items, got: {context_items}"
        assert len(review_items) == 1, "Verifier should still see [REVIEW] items"

    def test_coder_still_gets_context(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Coder archetype still receives [CONTEXT] items (no regression)."""
        run_id = "run-coder"
        provider.set_run_id(run_id)
        _insert_summary(conn, "spec_01", "1", run_id, summary="Prior session summary")

        result = provider.retrieve("spec_01", "task", task_group="2", archetype="coder")

        context_items = [r for r in result if r.startswith("[CONTEXT]")]
        assert len(context_items) >= 1, "Coder should still see [CONTEXT] items"

    def test_none_archetype_still_gets_context(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Default (None) archetype still receives [CONTEXT] items."""
        run_id = "run-default"
        provider.set_run_id(run_id)
        _insert_summary(conn, "spec_01", "1", run_id, summary="Prior session summary")

        result = provider.retrieve("spec_01", "task", task_group="2", archetype=None)

        context_items = [r for r in result if r.startswith("[CONTEXT]")]
        assert len(context_items) >= 1, "Default archetype should still see [CONTEXT] items"

    def test_reviewer_keeps_review_and_drift(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Reviewer still gets [REVIEW] and [DRIFT] items."""
        _insert_review(conn, "spec_01", "critical", "review finding", task_group="1")
        _insert_drift(conn, "spec_01", "drift finding")

        result = provider.retrieve("spec_01", "task", task_group="1", archetype="reviewer")

        review_items = [r for r in result if r.startswith("[REVIEW]")]
        drift_items = [r for r in result if r.startswith("[DRIFT]")]
        assert len(review_items) >= 1, "Reviewer should see [REVIEW] items"
        assert len(drift_items) >= 1, "Reviewer should see [DRIFT] items"


# ===========================================================================
# TS-NS-4: Verifier/gate skip _query_cross_spec_drift ([CROSS-SPEC])
# ===========================================================================


class TestVerifierGateSkipCrossSpec:
    """NS-REQ-4: Verifier and gate sessions have no [CROSS-SPEC] items."""

    def test_verifier_no_cross_spec(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Verifier archetype returns no [CROSS-SPEC] items."""
        _insert_drift(conn, "other_spec", "cross-spec drift", artifact_ref="src/shared.py")

        result = provider.retrieve(
            "my_spec",
            "task",
            task_group="1",
            file_footprint=["src/shared.py"],
            archetype="verifier",
        )

        cross_spec = [r for r in result if r.startswith("[CROSS-SPEC]")]
        assert cross_spec == [], f"Verifier should have no [CROSS-SPEC] items, got: {cross_spec}"

    def test_gate_no_cross_spec(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Gate archetype returns no [CROSS-SPEC] items (returns [] entirely)."""
        _insert_drift(conn, "other_spec", "cross-spec drift", artifact_ref="src/shared.py")

        result = provider.retrieve(
            "my_spec",
            "task",
            task_group="1",
            file_footprint=["src/shared.py"],
            archetype="gate",
        )

        assert result == [], "Gate should return empty list"

    def test_coder_still_gets_cross_spec(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Coder archetype still receives [CROSS-SPEC] items (no regression)."""
        _insert_drift(conn, "other_spec", "cross-spec drift", artifact_ref="src/shared.py")

        result = provider.retrieve(
            "my_spec",
            "task",
            task_group="1",
            file_footprint=["src/shared.py"],
            archetype="coder",
        )

        cross_spec = [r for r in result if r.startswith("[CROSS-SPEC]")]
        assert len(cross_spec) >= 1, "Coder should still see [CROSS-SPEC] items"

    def test_reviewer_still_gets_cross_spec(self, provider: FoxKnowledgeProvider, conn) -> None:
        """Reviewer archetype still receives [CROSS-SPEC] items."""
        _insert_drift(conn, "other_spec", "cross-spec drift", artifact_ref="src/shared.py")

        result = provider.retrieve(
            "my_spec",
            "task",
            task_group="1",
            file_footprint=["src/shared.py"],
            archetype="reviewer",
        )

        cross_spec = [r for r in result if r.startswith("[CROSS-SPEC]")]
        assert len(cross_spec) >= 1, "Reviewer should still see [CROSS-SPEC] items"


# ===========================================================================
# TS-NS-5: _build_prompts() threads archetype into retrieve() call
# ===========================================================================


class _TrackedProvider:
    """Lightweight mock that records retrieve() kwargs."""

    def __init__(self, returns: list[str] | None = None) -> None:
        self.retrieve_called = False
        self.retrieve_kwargs: dict[str, Any] = {}
        self._returns = returns or []

    def ingest(self, session_id: str, spec_name: str, context: dict[str, Any]) -> None:
        pass

    def retrieve(
        self,
        spec_name: str,
        task_description: str,
        task_group: str | None = None,
        session_id: str | None = None,
        file_footprint: list[str] | None = None,
        archetype: str | None = None,
    ) -> list[str]:
        self.retrieve_called = True
        self.retrieve_kwargs = {
            "task_group": task_group,
            "session_id": session_id,
            "file_footprint": file_footprint,
            "archetype": archetype,
        }
        return self._returns


class TestBuildPromptsThreadsArchetype:
    """NS-REQ-5: _build_prompts() passes archetype=self._archetype to retrieve()."""

    def _make_mock_config(self) -> MagicMock:
        mock_config = MagicMock()
        mock_config.knowledge = MagicMock()
        mock_config.models = MagicMock()
        mock_config.orchestrator = MagicMock()
        mock_config.archetypes.overrides.get.return_value = None
        mock_config.models.coding = None
        mock_config.models.review = None
        return mock_config

    @pytest.mark.parametrize("archetype", ["coder", "reviewer", "verifier", "gate"])
    def test_archetype_forwarded_to_retrieve(self, archetype: str) -> None:
        """_build_prompts passes the runner's archetype to retrieve()."""
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_provider = _TrackedProvider(returns=["fact"])
        mock_config = self._make_mock_config()
        mock_db = MagicMock()

        runner = NodeSessionRunner(
            "spec_01:1",
            mock_config,
            archetype=archetype,
            knowledge_db=mock_db,
            knowledge_provider=mock_provider,
            sink_dispatcher=MagicMock(),
        )

        with (
            patch("agentfox.engine.session_lifecycle.assemble_context", return_value=MagicMock()),
            patch("agentfox.engine.session_lifecycle.build_system_prompt", return_value="sys"),
            patch("agentfox.engine.session_lifecycle.build_task_prompt", return_value="task"),
            patch("agentfox.core.config.resolve_spec_root", return_value=MagicMock()),
        ):
            runner._build_prompts("/tmp/repo", 1, None)

        assert mock_provider.retrieve_called
        assert mock_provider.retrieve_kwargs["archetype"] == archetype
