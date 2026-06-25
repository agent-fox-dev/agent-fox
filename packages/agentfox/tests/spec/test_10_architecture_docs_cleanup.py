"""Tests verifying architecture documentation reflects three-channel system.

Checks that docs/architecture.md, docs/architecture/05-knowledge-system-architecture.md,
and docs/architecture/03-execution-and-archetypes.md have been updated to remove
references to removed channels.

Test Spec: TS-10-30, TS-10-31, TS-10-32
Requirements: 10-REQ-9.1, 10-REQ-9.2, 10-REQ-9.3
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[4]
_DOCS = _REPO_ROOT / "docs"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _grep_doc(doc_path: Path, pattern: str, *, case_insensitive: bool = True) -> list[str]:
    """Return lines from a document matching a pattern."""
    if not doc_path.exists():
        pytest.skip(f"Document not found: {doc_path}")
    content = doc_path.read_text()
    flags = re.IGNORECASE if case_insensitive else 0
    return [
        line
        for line in content.splitlines()
        if re.search(pattern, line, flags)
    ]


# ---------------------------------------------------------------------------
# TS-10-30: docs/architecture.md has no removed-channel references
# ---------------------------------------------------------------------------


class TestArchitectureMdCleanup:
    """TS-10-30: docs/architecture.md contains no references to removed channels."""

    @pytest.mark.parametrize(
        "term",
        ["errata", r"\bADR\b", "verdict", "cross-spec", "prior-run"],
        ids=["errata", "ADR", "verdict", "cross-spec", "prior-run"],
    )
    def test_forbidden_term_absent(self, term: str) -> None:
        doc = _DOCS / "architecture.md"
        matches = _grep_doc(doc, term)
        assert not matches, (
            f"docs/architecture.md must not contain '{term}':\n"
            + "\n".join(matches)
        )


# ---------------------------------------------------------------------------
# TS-10-31: 05-knowledge-system-architecture.md describes exactly three channels
# ---------------------------------------------------------------------------


class TestKnowledgeSystemArchitectureCleanup:
    """TS-10-31: Knowledge system doc describes exactly three retrieval channels."""

    _DOC_PATH = _DOCS / "architecture" / "05-knowledge-system-architecture.md"

    @pytest.mark.parametrize(
        "removed_term",
        ["errata", r"\badr\b", "verdict", "cross-spec summar", "prior-run"],
        ids=["errata", "adr", "verdict", "cross-spec", "prior-run"],
    )
    def test_removed_channel_terms_absent(self, removed_term: str) -> None:
        matches = _grep_doc(self._DOC_PATH, removed_term)
        assert not matches, (
            f"05-knowledge-system-architecture.md must not contain '{removed_term}':\n"
            + "\n".join(matches)
        )

    @pytest.mark.parametrize(
        "retained_term",
        ["review findings", "cross-group review", "same-spec context"],
        ids=["review-findings", "cross-group-review", "same-spec-context"],
    )
    def test_retained_channels_documented(self, retained_term: str) -> None:
        doc = self._DOC_PATH
        if not doc.exists():
            pytest.skip(f"Document not found: {doc}")
        content = doc.read_text().lower()
        assert retained_term.lower() in content, (
            f"05-knowledge-system-architecture.md must document '{retained_term}'"
        )


# ---------------------------------------------------------------------------
# TS-10-32: 03-execution-and-archetypes.md has no verdict/errata references
# ---------------------------------------------------------------------------


class TestExecutionArchetypesCleanup:
    """TS-10-32: 03-execution-and-archetypes.md contains no verdict/errata refs."""

    _DOC_PATH = _DOCS / "architecture" / "03-execution-and-archetypes.md"

    @pytest.mark.parametrize(
        "term",
        ["verdict", "errata"],
    )
    def test_forbidden_term_absent(self, term: str) -> None:
        matches = _grep_doc(self._DOC_PATH, term)
        assert not matches, (
            f"03-execution-and-archetypes.md must not contain '{term}':\n"
            + "\n".join(matches)
        )
