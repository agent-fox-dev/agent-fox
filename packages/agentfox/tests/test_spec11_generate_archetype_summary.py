"""Unit tests for generate_archetype_summary changes (spec 11).

Tests verify that generate_archetype_summary returns None for trivial
reviewer/verifier sessions and continues to return non-empty strings
for sessions with actual findings or verdicts.

Test Spec: TS-11-16, TS-11-17, TS-11-18, TS-11-19, TS-11-E4
Requirements: 11-REQ-4.1, 11-REQ-4.2, 11-REQ-4.3, 11-REQ-4.4,
              11-REQ-4.E1
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

from agentfox.knowledge.formatting import generate_archetype_summary
from agentfox.knowledge.review_store import ReviewFinding, VerificationResult


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


def _make_verdict(
    *,
    requirement_id: str = "REQ-1.1",
    verdict: str = "PASS",
    evidence: str | None = None,
) -> VerificationResult:
    return VerificationResult(
        id=str(uuid.uuid4()),
        requirement_id=requirement_id,
        verdict=verdict,
        evidence=evidence,
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
# TS-11-17: Returns None for verifier with empty verdicts (11-REQ-4.2)
# ---------------------------------------------------------------------------


class TestVerifierNoVerdicts:
    """Verify None returned for verifier session with no verdicts."""

    def test_returns_none_empty_verdicts(self) -> None:
        result = generate_archetype_summary("verifier", verdicts=[])
        assert result is None

    def test_returns_none_no_verdicts_arg(self) -> None:
        result = generate_archetype_summary("verifier")
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


# ---------------------------------------------------------------------------
# TS-11-19: Returns non-empty string for verifier with verdicts (11-REQ-4.4)
# ---------------------------------------------------------------------------


class TestVerifierWithVerdicts:
    """Verify non-empty summary for verifier with actual verdicts."""

    def test_returns_nonempty_with_verdicts(self) -> None:
        verdicts = [
            _make_verdict(requirement_id="11-REQ-3.1", verdict="FAIL", evidence="Function not found"),
        ]
        result = generate_archetype_summary("verifier", verdicts=verdicts)
        assert result is not None
        assert len(result) > 0
        assert "11-REQ-3.1" in result or "fail" in result.lower()


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
