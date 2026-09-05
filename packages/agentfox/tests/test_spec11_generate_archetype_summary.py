"""Unit tests for generate_archetype_summary changes (spec 11).

Tests verify that generate_archetype_summary returns None for trivial
reviewer sessions and continues to return non-empty strings
for sessions with actual findings or verdicts.

Test Spec: TS-11-16, TS-11-17, TS-11-18, TS-11-19, TS-11-E4
Requirements: 11-REQ-4.1, 11-REQ-4.2, 11-REQ-4.3, 11-REQ-4.4,
              11-REQ-4.E1
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

from agentfox.knowledge.formatting import generate_archetype_summary
from agentfox.knowledge.review_store import ReviewFinding


def _make_finding(
    *,
    severity: str = "critical",
    description: str = "Issue A",
) -> ReviewFinding:
    return ReviewFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description=description,
        requirement_ref=None,
        spec_name="test_spec",
        task_group="1",
        session_id="s1",
    )


# ---------------------------------------------------------------------------
# TS-11-16: Returns None for reviewer with empty findings (11-REQ-4.1)
# ---------------------------------------------------------------------------


class TestReviewerNoFindings:
    """Verify None returned for reviewer session with no findings."""

    def test_returns_none_empty_findings(self) -> None:
        result = generate_archetype_summary("reviewer", findings=[])
        assert result is None

    def test_returns_none_no_findings_arg(self) -> None:
        result = generate_archetype_summary("reviewer")
        assert result is None


# ---------------------------------------------------------------------------
# TS-11-18: Returns non-empty string for reviewer with findings (11-REQ-4.3)
# ---------------------------------------------------------------------------


class TestReviewerWithFindings:
    """Verify non-empty summary for reviewer with actual findings."""

    def test_returns_nonempty_with_findings(self) -> None:
        findings = [
            _make_finding(severity="high", description="Missing input validation on user ID field"),
            _make_finding(severity="low", description="Unused import in formatting.py"),
        ]
        result = generate_archetype_summary("reviewer", findings=findings)
        assert result is not None
        assert len(result) > 0
        # TS-11-18: verify content contains severity counts or finding descriptions
        assert "high" in result.lower() or "finding" in result.lower()


# ---------------------------------------------------------------------------
# TS-11-E4: Returns None when findings present but all counts zero (11-REQ-4.E1)
# ---------------------------------------------------------------------------


class TestReviewerAllZeroCounts:
    """Verify None returned when findings data is structurally present
    but all severity counts are zero.

    Uses SimpleNamespace mock objects since production ReviewFinding
    objects don't have a ``count`` attribute — see errata
    docs/errata/11_enrich_context_summaries.md for details.
    """

    def test_returns_none_all_zero_counts(self) -> None:
        findings = [
            SimpleNamespace(count=0, severity="high"),
            SimpleNamespace(count=0, severity="medium"),
            SimpleNamespace(count=0, severity="low"),
        ]
        result = generate_archetype_summary("reviewer", findings=findings)
        assert result is None
