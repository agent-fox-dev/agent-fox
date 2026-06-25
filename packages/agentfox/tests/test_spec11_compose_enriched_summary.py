"""Unit tests for compose_enriched_summary function.

Tests verify that the compose_enriched_summary function correctly merges
the narrative summary with structured rejected_approaches, gotchas, and
assumptions into a single enriched text string.

Test Spec: TS-11-10, TS-11-11, TS-11-12, TS-11-13, TS-11-15,
           TS-11-E1, TS-11-E2, TS-11-E3
Requirements: 11-REQ-3.1, 11-REQ-3.2, 11-REQ-3.3, 11-REQ-3.4,
              11-REQ-3.6, 11-REQ-1.E1, 11-REQ-3.E1, 11-REQ-3.E2
"""

from __future__ import annotations


def _compose_enriched_summary(*args, **kwargs):
    """Deferred import of compose_enriched_summary (not yet implemented)."""
    from agentfox.engine.session_lifecycle import compose_enriched_summary

    return compose_enriched_summary(*args, **kwargs)


# ---------------------------------------------------------------------------
# TS-11-10: Summary text always appears first in composed output (11-REQ-3.1)
# ---------------------------------------------------------------------------


class TestSummaryAppearsFirst:
    """Verify that compose_enriched_summary always starts with the narrative."""

    def test_summary_starts_output(self) -> None:
        result = _compose_enriched_summary(
            summary="Used a two-pass algorithm for performance.",
            rejected_approaches=[],
            gotchas=[],
            assumptions=[],
        )
        assert result.startswith("Used a two-pass algorithm for performance.")
        assert len(result) > 0

    def test_summary_starts_output_with_all_fields(self) -> None:
        result = _compose_enriched_summary(
            summary="Used a two-pass algorithm for performance.",
            rejected_approaches=[{"approach": "Brute force", "reason": "O(n^2)"}],
            gotchas=["Watch for off-by-one"],
            assumptions=["Input sorted"],
        )
        assert result.startswith("Used a two-pass algorithm for performance.")


# ---------------------------------------------------------------------------
# TS-11-11: Rejected approaches formatting (11-REQ-3.2)
# ---------------------------------------------------------------------------


class TestRejectedApproachesFormatting:
    """Verify 'Tried: {approach} — rejected because: {reason}' format."""

    def test_single_rejected_approach(self) -> None:
        result = _compose_enriched_summary(
            summary="Implemented caching layer.",
            rejected_approaches=[
                {"approach": "Redis cache", "reason": "Adds an external dependency"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Tried: Redis cache — rejected because: Adds an external dependency" in result

    def test_multiple_rejected_approaches(self) -> None:
        result = _compose_enriched_summary(
            summary="Implemented caching layer.",
            rejected_approaches=[
                {"approach": "Redis cache", "reason": "Adds an external dependency"},
                {"approach": "In-memory dict", "reason": "Not thread-safe"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Tried: Redis cache — rejected because: Adds an external dependency" in result
        assert "Tried: In-memory dict — rejected because: Not thread-safe" in result
        # Each on a separate line
        lines = result.split("\n")
        assert any("Tried: Redis cache" in line for line in lines)
        assert any("Tried: In-memory dict" in line for line in lines)


# ---------------------------------------------------------------------------
# TS-11-12: Gotchas formatting (11-REQ-3.3)
# ---------------------------------------------------------------------------


class TestGotchasFormatting:
    """Verify 'Watch out: {gotcha}' format."""

    def test_gotchas_formatted(self) -> None:
        result = _compose_enriched_summary(
            summary="Refactored the formatter.",
            rejected_approaches=[],
            gotchas=[
                "DuckDB closes connection on fork",
                "Empty arrays serialize as null in some paths",
            ],
            assumptions=[],
        )
        assert "Watch out: DuckDB closes connection on fork" in result
        assert "Watch out: Empty arrays serialize as null in some paths" in result


# ---------------------------------------------------------------------------
# TS-11-13: Assumptions formatting (11-REQ-3.4)
# ---------------------------------------------------------------------------


class TestAssumptionsFormatting:
    """Verify 'Assumes: {assumption}' format."""

    def test_assumptions_formatted(self) -> None:
        result = _compose_enriched_summary(
            summary="Added retry logic.",
            rejected_approaches=[],
            gotchas=[],
            assumptions=[
                "Spec 10 retains session_summaries table",
                "DuckDB version >= 0.9",
            ],
        )
        assert "Assumes: Spec 10 retains session_summaries table" in result
        assert "Assumes: DuckDB version >= 0.9" in result


# ---------------------------------------------------------------------------
# TS-11-15: Backward-compat — no structured fields returns raw summary (11-REQ-3.6)
# ---------------------------------------------------------------------------


class TestBackwardCompatibility:
    """Verify raw summary returned unchanged when no structured fields present."""

    def test_none_fields_returns_raw_summary(self) -> None:
        result = _compose_enriched_summary(
            summary="Implemented task group 3 for spec 11.",
            rejected_approaches=None,
            gotchas=None,
            assumptions=None,
        )
        assert result == "Implemented task group 3 for spec 11."

    def test_empty_list_fields_returns_raw_summary(self) -> None:
        result = _compose_enriched_summary(
            summary="Implemented task group 3 for spec 11.",
            rejected_approaches=[],
            gotchas=[],
            assumptions=[],
        )
        assert result == "Implemented task group 3 for spec 11."


# ---------------------------------------------------------------------------
# TS-11-E1: Malformed rejected_approaches entry skipped (11-REQ-1.E1)
# ---------------------------------------------------------------------------


class TestMalformedRejectedApproaches:
    """Verify malformed entries are skipped without exceptions."""

    def test_missing_approach_key_skipped(self) -> None:
        result = _compose_enriched_summary(
            summary="Some narrative.",
            rejected_approaches=[
                {"reason": "Too slow"},  # missing 'approach'
                {"approach": "Valid approach", "reason": "Not compatible"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Tried: Valid approach — rejected because: Not compatible" in result
        # The malformed entry should not produce a "Tried:" line with "Too slow"
        for line in result.split("\n"):
            if "Too slow" in line:
                assert not line.strip().startswith("Tried:")

    def test_missing_reason_key_skipped(self) -> None:
        result = _compose_enriched_summary(
            summary="Some narrative.",
            rejected_approaches=[
                {"approach": "Missing reason"},  # missing 'reason'
                {"approach": "Valid approach", "reason": "Not compatible"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Tried: Valid approach — rejected because: Not compatible" in result

    def test_no_exception_on_all_malformed(self) -> None:
        # Should not raise even if all entries are malformed
        result = _compose_enriched_summary(
            summary="Some narrative.",
            rejected_approaches=[
                {"reason": "Too slow"},
                {"approach": "No reason"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Some narrative." in result


# ---------------------------------------------------------------------------
# TS-11-E2: Empty summary with structured fields (11-REQ-3.E1)
# ---------------------------------------------------------------------------


class TestEmptySummary:
    """Verify empty summary field handling."""

    def test_empty_summary_with_structured_fields(self) -> None:
        result = _compose_enriched_summary(
            summary="",
            rejected_approaches=[
                {"approach": "Approach A", "reason": "Reason A"},
            ],
            gotchas=[],
            assumptions=[],
        )
        assert "Tried: Approach A — rejected because: Reason A" in result

    def test_empty_summary_with_no_structured_fields(self) -> None:
        result = _compose_enriched_summary(
            summary="",
            rejected_approaches=[],
            gotchas=[],
            assumptions=[],
        )
        assert result == ""


# ---------------------------------------------------------------------------
# TS-11-E3: Section separator / no trailing newline (11-REQ-3.E2)
# ---------------------------------------------------------------------------


class TestSectionSeparation:
    """Verify sections separated by newlines, no trailing newline."""

    def test_newline_separation_no_trailing(self) -> None:
        result = _compose_enriched_summary(
            summary="Core narrative.",
            rejected_approaches=[
                {"approach": "Approach X", "reason": "Reason X"},
            ],
            gotchas=["Watch this"],
            assumptions=["Assumes Y"],
        )
        assert "\n" in result
        assert not result.endswith("\n")
        parts = result.split("\n")
        assert parts[0].startswith("Core narrative.")

    def test_sections_ordered_correctly(self) -> None:
        result = _compose_enriched_summary(
            summary="Narrative.",
            rejected_approaches=[
                {"approach": "A", "reason": "R"},
            ],
            gotchas=["G"],
            assumptions=["S"],
        )
        lines = result.split("\n")
        # First line is the narrative
        assert lines[0].startswith("Narrative.")
        # Find indices
        tried_idx = next(i for i, ln in enumerate(lines) if "Tried:" in ln)
        watch_idx = next(i for i, ln in enumerate(lines) if "Watch out:" in ln)
        assumes_idx = next(i for i, ln in enumerate(lines) if "Assumes:" in ln)
        assert tried_idx < watch_idx < assumes_idx
