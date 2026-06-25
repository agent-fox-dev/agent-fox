"""Static tests for 05-knowledge-system-architecture.md enriched summary updates.

Tests verify that the architecture documentation describes the enriched
session-summary.json schema, composition step, non-obvious learnings in
session_summaries, enriched retrieval content, and cross-session continuity.

Test Spec: TS-11-21, TS-11-22, TS-11-23, TS-11-24
Requirements: 11-REQ-5.1, 11-REQ-5.2, 11-REQ-5.3, 11-REQ-5.4
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

ARCH_DOC_PATH = (
    Path(__file__).resolve().parents[3]
    / "docs"
    / "architecture"
    / "05-knowledge-system-architecture.md"
)


@pytest.fixture()
def arch_doc_content() -> str:
    """Read the architecture documentation content."""
    return ARCH_DOC_PATH.read_text(encoding="utf-8")


def _extract_section(content: str, heading_number: str) -> str:
    """Extract a section by its heading number prefix (e.g. '4.2', '5.5').

    Captures text from the heading line up to the next heading of equal or
    higher level. For sub-sections like '4.2', captures up to the next '###'
    or '##' heading. For top-level sections like '6' or '11', captures up to
    the next '##' heading.
    """
    lines = content.split("\n")
    in_section = False
    section_lines: list[str] = []
    # Determine the heading level we expect
    dot_count = heading_number.count(".")
    if dot_count == 0:
        # Top-level section like "## 6." or "## 11."
        pattern = re.compile(rf"^##\s+{re.escape(heading_number)}[\.\s]")
        end_pattern = re.compile(r"^---$|^##\s+\d")
    else:
        # Sub-section like "### 4.2" or "### 5.5"
        pattern = re.compile(rf"^###\s+{re.escape(heading_number)}[\.\s]")
        end_pattern = re.compile(r"^---$|^##\s+\d|^###\s+\d")

    for line in lines:
        if in_section:
            if end_pattern.match(line):
                break
            section_lines.append(line)
        elif pattern.match(line):
            in_section = True
            section_lines.append(line)

    return "\n".join(section_lines)


# ---------------------------------------------------------------------------
# TS-11-21: Section 4.2 describes enriched schema fields and composition step
#           (11-REQ-5.1)
# ---------------------------------------------------------------------------


class TestSection42EnrichedSchema:
    """Verify Section 4.2 describes enriched session-summary.json schema."""

    def test_section_42_mentions_rejected_approaches(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "4.2")
        assert "rejected_approaches" in section, (
            "Section 4.2 must reference the rejected_approaches field"
        )

    def test_section_42_mentions_gotchas(self, arch_doc_content: str) -> None:
        section = _extract_section(arch_doc_content, "4.2")
        assert "gotchas" in section, (
            "Section 4.2 must reference the gotchas field"
        )

    def test_section_42_mentions_assumptions(self, arch_doc_content: str) -> None:
        section = _extract_section(arch_doc_content, "4.2")
        assert "assumptions" in section, (
            "Section 4.2 must reference the assumptions field"
        )

    def test_section_42_mentions_composition(self, arch_doc_content: str) -> None:
        section = _extract_section(arch_doc_content, "4.2")
        section_lower = section.lower()
        assert "compos" in section_lower, (
            "Section 4.2 must describe the composition step that transforms "
            "structured fields into stored text"
        )


# ---------------------------------------------------------------------------
# TS-11-22: Section 5.5 notes session_summaries rows contain non-obvious
#           learnings (11-REQ-5.2)
# ---------------------------------------------------------------------------


class TestSection55NonObviousLearnings:
    """Verify Section 5.5 notes non-obvious learnings in session_summaries."""

    def test_section_55_mentions_non_obvious_or_rejected(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "5.5")
        section_lower = section.lower()
        assert "non-obvious" in section_lower or "rejected" in section_lower, (
            "Section 5.5 must mention non-obvious learnings or rejected "
            "approaches in session_summaries rows"
        )

    def test_section_55_no_longer_completion_status(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "5.5")
        section_lower = section.lower()
        # Either 'completion' should not appear, or it should be qualified
        # with 'no longer' or similar negation
        if "completion" in section_lower:
            assert "no longer" in section_lower or "not" in section_lower, (
                "Section 5.5 should indicate completion-status pings are no "
                "longer stored, not describe them as current behavior"
            )


# ---------------------------------------------------------------------------
# TS-11-23: Section 6 retrieval table updates Same-spec summaries row
#           to reflect enriched content (11-REQ-5.3)
# ---------------------------------------------------------------------------


class TestSection6RetrievalTable:
    """Verify Section 6 retrieval table reflects enriched Same-spec summaries."""

    def test_section_6_mentions_same_spec(self, arch_doc_content: str) -> None:
        section = _extract_section(arch_doc_content, "6")
        section_lower = section.lower()
        assert "same-spec" in section_lower or "same spec" in section_lower, (
            "Section 6 must reference Same-spec summaries"
        )

    def test_section_6_mentions_enriched_content(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "6")
        section_lower = section.lower()
        assert (
            "enriched" in section_lower
            or "rejected" in section_lower
            or "gotcha" in section_lower
        ), (
            "Section 6 Same-spec summaries row must reference enriched content "
            "from structured session-summary fields"
        )


# ---------------------------------------------------------------------------
# TS-11-24: Section 11 cross-session continuity paragraph describes enriched
#           summary content (11-REQ-5.4)
# ---------------------------------------------------------------------------


class TestSection11CrossSessionContinuity:
    """Verify Section 11 describes enriched summary content."""

    def test_section_11_mentions_structured_fields(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "11")
        section_lower = section.lower()
        assert (
            "rejected" in section_lower
            or "gotcha" in section_lower
            or "assumption" in section_lower
        ), (
            "Section 11's cross-session continuity paragraph must reference "
            "rejected approaches, gotchas, or assumptions"
        )

    def test_section_11_no_generic_completion_status(
        self, arch_doc_content: str
    ) -> None:
        section = _extract_section(arch_doc_content, "11")
        section_lower = section.lower()
        if "completion status" in section_lower:
            assert "no longer" in section_lower or "rather than" in section_lower, (
                "Section 11 should not describe generic completion status as "
                "current behavior"
            )
