"""Unit tests for pre-review and drift-review injection into coder prompts.

Verifies that ``build_retry_context`` surfaces pre-review findings
(task_group='0') and drift findings on the very first coder attempt,
not only after a failed audit-review.

Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-4, TS-NS-5
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5
Issue: #610
"""

from __future__ import annotations

import uuid

from agentfox.knowledge.db import KnowledgeDB
from agentfox.knowledge.review_store import (
    DriftFinding,
    ReviewFinding,
    insert_drift_findings,
    insert_findings,
)


def _make_review_finding(
    severity: str = "critical",
    description: str = "Test finding",
    spec_name: str = "02_spec",
    task_group: str = "0",
    session_id: str = "pre-review-sess",
) -> ReviewFinding:
    return ReviewFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description=description,
        requirement_ref=None,
        spec_name=spec_name,
        task_group=task_group,
        session_id=session_id,
    )


def _make_drift_finding(
    severity: str = "critical",
    description: str = "Test drift",
    spec_name: str = "02_spec",
    task_group: str = "0",
    session_id: str = "drift-review-sess",
) -> DriftFinding:
    return DriftFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description=description,
        spec_ref=None,
        artifact_ref=None,
        spec_name=spec_name,
        task_group=task_group,
        session_id=session_id,
    )


# ---------------------------------------------------------------------------
# TS-NS-1: Pre-review findings (task_group='0') injected on attempt 1
# Requirements: NS-REQ-1
# ---------------------------------------------------------------------------


class TestPrereviewFindingsInjected:
    """TS-NS-1: Pre-review review_findings (task_group='0') appear in the coder
    task prompt even on the first attempt (before any audit-review runs).
    """

    def test_prereview_finding_included_for_group1(self, knowledge_db: KnowledgeDB) -> None:
        """Critical pre-review finding (task_group='0') appears in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        finding = _make_review_finding(
            severity="critical",
            description="pre-review-critical-issue",
            spec_name="02_spec",
            task_group="0",
        )
        insert_findings(conn, [finding])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context, "build_retry_context must return non-empty string when pre-review findings exist"
        assert "pre-review-critical-issue" in context, (
            "Pre-review critical finding must appear in group-1 coder context"
        )
        assert "## Prior Review Findings" in context

    def test_major_prereview_finding_included(self, knowledge_db: KnowledgeDB) -> None:
        """Major pre-review finding (task_group='0') also appears in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        finding = _make_review_finding(
            severity="major",
            description="pre-review-major-issue",
            spec_name="02_spec",
            task_group="0",
        )
        insert_findings(conn, [finding])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert "pre-review-major-issue" in context

    def test_prereview_finding_identical_on_attempt1_and_retry(
        self, knowledge_db: KnowledgeDB
    ) -> None:
        """TS-NS-5: build_retry_context produces consistent output regardless of attempt.

        The function does not receive an attempt number, so the same DB state
        must produce a non-empty result both on the first call and on any retry
        call.  (sanitize_prompt_content wraps descriptions in nonce boundaries
        that differ per call, so we check content presence, not byte equality.)
        """
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        finding = _make_review_finding(
            severity="critical",
            description="stable-prereview-issue",
            spec_name="02_spec",
            task_group="0",
        )
        insert_findings(conn, [finding])

        context_first = build_retry_context(knowledge_db, "02_spec", task_group="1")
        context_retry = build_retry_context(knowledge_db, "02_spec", task_group="1")

        # Both calls must return non-empty strings that contain the finding description
        assert context_first, "Context must be non-empty when pre-review findings exist (attempt 1)"
        assert context_retry, "Context must be non-empty when pre-review findings exist (retry)"
        assert "stable-prereview-issue" in context_first, "Finding must appear on first call"
        assert "stable-prereview-issue" in context_retry, "Finding must appear on retry call"
        assert "## Prior Review Findings" in context_first
        assert "## Prior Review Findings" in context_retry


# ---------------------------------------------------------------------------
# TS-NS-2: Drift findings (task_group='0') injected on attempt 1
# Requirements: NS-REQ-2
# ---------------------------------------------------------------------------


class TestDriftFindingsInjected:
    """TS-NS-2: Drift findings from drift-review (task_group='0') appear in the
    coder task prompt even on the first attempt.
    """

    def test_drift_finding_included_for_group1(self, knowledge_db: KnowledgeDB) -> None:
        """Critical drift finding (task_group='0') appears in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        drift = _make_drift_finding(
            severity="critical",
            description="drift-critical-divergence",
            spec_name="02_spec",
            task_group="0",
        )
        insert_drift_findings(conn, [drift])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context, "build_retry_context must return non-empty string when drift findings exist"
        assert "drift-critical-divergence" in context, (
            "Drift finding must appear in group-1 coder context"
        )

    def test_major_drift_finding_included(self, knowledge_db: KnowledgeDB) -> None:
        """Major drift finding is surfaced in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        drift = _make_drift_finding(
            severity="major",
            description="drift-major-api-mismatch",
            spec_name="02_spec",
            task_group="0",
        )
        insert_drift_findings(conn, [drift])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert "drift-major-api-mismatch" in context


# ---------------------------------------------------------------------------
# TS-NS-3: Empty DB → task prompt unchanged
# Requirements: NS-REQ-3
# ---------------------------------------------------------------------------


class TestEmptyFindingsProducesEmptyContext:
    """TS-NS-3: When no critical/major pre-review or drift findings exist,
    build_retry_context returns an empty string.
    """

    def test_empty_db_returns_empty_string(self, knowledge_db: KnowledgeDB) -> None:
        """build_retry_context returns '' when review_findings and drift_findings are empty."""
        from agentfox.engine.session_lifecycle import build_retry_context

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context == "", "build_retry_context must return empty string when no findings exist"

    def test_minor_findings_not_included(self, knowledge_db: KnowledgeDB) -> None:
        """Only critical and major are actionable; minor/observation are excluded."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        minor_drift = _make_drift_finding(
            severity="minor",
            description="minor-drift-issue",
            spec_name="02_spec",
            task_group="0",
        )
        # insert_findings silently drops minor/observation rows (ACTIONABLE_SEVERITIES guard);
        # we test the drift path filtering here.
        insert_drift_findings(conn, [minor_drift])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context == "", "Minor/observation findings must not produce context output"


# ---------------------------------------------------------------------------
# TS-NS-4: Findings from other groups are excluded
# Requirements: NS-REQ-4
# ---------------------------------------------------------------------------


class TestOtherGroupFindingsExcluded:
    """TS-NS-4: Findings from task_group='2' (or other groups) must not appear
    in the group-1 coder context — only group-0 and group-1 are included.
    """

    def test_group2_review_finding_excluded_from_group1_context(
        self, knowledge_db: KnowledgeDB
    ) -> None:
        """Critical group-2 review finding does not appear in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        group2_finding = _make_review_finding(
            severity="critical",
            description="group2-critical-issue",
            spec_name="02_spec",
            task_group="2",
        )
        insert_findings(conn, [group2_finding])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context == "", (
            "Group-2 review finding must not appear in group-1 coder context"
        )

    def test_group2_drift_finding_excluded_from_group1_context(
        self, knowledge_db: KnowledgeDB
    ) -> None:
        """Critical group-2 drift finding does not appear in group-1 context."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        group2_drift = _make_drift_finding(
            severity="critical",
            description="group2-drift-issue",
            spec_name="02_spec",
            task_group="2",
        )
        insert_drift_findings(conn, [group2_drift])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert context == "", (
            "Group-2 drift finding must not appear in group-1 coder context"
        )

    def test_group0_included_but_group2_excluded(self, knowledge_db: KnowledgeDB) -> None:
        """group-0 pre-review finding appears; group-2 finding does not."""
        from agentfox.engine.session_lifecycle import build_retry_context

        conn = knowledge_db._conn
        prereview_finding = _make_review_finding(
            severity="critical",
            description="group0-prereview-issue",
            spec_name="02_spec",
            task_group="0",
        )
        other_group_finding = _make_review_finding(
            severity="critical",
            description="group2-other-issue",
            spec_name="02_spec",
            task_group="2",
        )
        insert_findings(conn, [prereview_finding, other_group_finding])

        context = build_retry_context(knowledge_db, "02_spec", task_group="1")

        assert "group0-prereview-issue" in context, "Pre-review finding must appear"
        assert "group2-other-issue" not in context, "Group-2 finding must NOT appear"
