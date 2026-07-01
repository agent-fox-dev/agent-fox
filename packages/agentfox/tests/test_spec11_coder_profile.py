"""Static tests for coder.md profile template structured fields.

Tests verify that the coder agent profile template instructs agents to
populate session-summary.json with non-obvious learnings, rejected
approaches, gotchas, and assumptions.

Test Spec: TS-11-5, TS-11-6, TS-11-7, TS-11-8, TS-11-9
Requirements: 11-REQ-2.1, 11-REQ-2.2, 11-REQ-2.3, 11-REQ-2.4, 11-REQ-2.5
"""

from __future__ import annotations

from pathlib import Path

import pytest

CODER_MD_PATH = Path(__file__).resolve().parents[1] / "agentfox" / "_templates" / "profiles" / "coder.md"


@pytest.fixture()
def coder_md_content() -> str:
    """Read the coder.md profile template content."""
    return CODER_MD_PATH.read_text(encoding="utf-8")


# ---------------------------------------------------------------------------
# TS-11-5: coder.md instructs agents to record non-obvious/surprising
#          implementation details in the summary field (11-REQ-2.1)
# ---------------------------------------------------------------------------


class TestSummaryNonObviousLearnings:
    """Verify coder.md instructs recording non-obvious or surprising details."""

    def test_contains_non_obvious_or_surprising(self, coder_md_content: str) -> None:
        content_lower = coder_md_content.lower()
        assert "non-obvious" in content_lower or "surprising" in content_lower, (
            "coder.md must instruct agents to record non-obvious or surprising "
            "implementation details in the summary field"
        )

    def test_references_summary_field(self, coder_md_content: str) -> None:
        assert "summary" in coder_md_content, "coder.md must reference the summary field"


# ---------------------------------------------------------------------------
# TS-11-6: coder.md instructs agents to populate rejected_approaches
#          (11-REQ-2.2)
# ---------------------------------------------------------------------------


class TestRejectedApproachesInstructions:
    """Verify coder.md instructs populating rejected_approaches."""

    def test_contains_rejected_approaches_field(self, coder_md_content: str) -> None:
        assert "rejected_approaches" in coder_md_content, "coder.md must reference the rejected_approaches field"

    def test_contains_rejected_or_tried_language(self, coder_md_content: str) -> None:
        content_lower = coder_md_content.lower()
        assert "rejected" in content_lower or "tried" in content_lower, (
            "coder.md must instruct agents about approaches tried and rejected"
        )


# ---------------------------------------------------------------------------
# TS-11-7: coder.md instructs agents to populate gotchas (11-REQ-2.3)
# ---------------------------------------------------------------------------


class TestGotchasInstructions:
    """Verify coder.md instructs populating gotchas."""

    def test_contains_gotchas_field(self, coder_md_content: str) -> None:
        assert "gotchas" in coder_md_content, "coder.md must reference the gotchas field"

    def test_contains_edge_case_or_watch_out_language(self, coder_md_content: str) -> None:
        content_lower = coder_md_content.lower()
        assert "edge case" in content_lower or "watch out" in content_lower or "counter-intuitive" in content_lower, (
            "coder.md must instruct agents about edge cases, watch-outs, or "
            "counter-intuitive behaviors in the gotchas field"
        )


# ---------------------------------------------------------------------------
# TS-11-8: coder.md instructs agents to populate assumptions (11-REQ-2.4)
# ---------------------------------------------------------------------------


class TestAssumptionsInstructions:
    """Verify coder.md instructs populating assumptions."""

    def test_contains_assumptions_field(self, coder_md_content: str) -> None:
        assert "assumptions" in coder_md_content, "coder.md must reference the assumptions field"

    def test_contains_later_or_might_not_hold(self, coder_md_content: str) -> None:
        content_lower = coder_md_content.lower()
        assert "later" in content_lower or "might not hold" in content_lower, (
            "coder.md must instruct agents about assumptions that might not hold for later task groups"
        )


# ---------------------------------------------------------------------------
# TS-11-9: coder.md targets ~500-1000 characters and non-obvious learnings
#          (11-REQ-2.5)
# ---------------------------------------------------------------------------


class TestSummaryCharacterTarget:
    """Verify coder.md references ~500-1000 character target for summary."""

    def test_contains_500_and_1000(self, coder_md_content: str) -> None:
        assert "500" in coder_md_content and "1000" in coder_md_content, (
            "coder.md must reference a ~500-1000 character target for the summary field"
        )

    def test_contains_non_obvious_or_learnings(self, coder_md_content: str) -> None:
        content_lower = coder_md_content.lower()
        assert "non-obvious" in content_lower or "learnings" in content_lower, (
            "coder.md must frame the summary around non-obvious learnings rather than completion status"
        )
