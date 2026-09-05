"""Property-based tests for enriched context summaries.

Uses Hypothesis to verify invariants across randomized inputs for
compose_enriched_summary and generate_archetype_summary.

Test Spec: TS-11-P1, TS-11-P2, TS-11-P3, TS-11-P4
Requirements: 11-REQ-1.4, 11-REQ-3.6, 11-REQ-4.1, 11-REQ-4.2,
              11-REQ-4.5, 11-REQ-3.2, 11-REQ-3.3, 11-REQ-3.4,
              11-REQ-4.3
"""

from __future__ import annotations

import uuid

from agentfox.knowledge.formatting import generate_archetype_summary
from agentfox.knowledge.review_store import ReviewFinding
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

_summary_text = st.text(
    alphabet=st.characters(categories=("L", "N", "P", "Z")),
    min_size=1,
    max_size=2000,
)

_field_text = st.text(
    alphabet=st.characters(categories=("L", "N", "P", "Z")),
    min_size=1,
    max_size=200,
)

_rejected_approach = st.fixed_dictionaries(
    {"approach": _field_text, "reason": _field_text},
)

_severity = st.sampled_from(["critical", "major", "minor", "observation"])


def _compose_enriched_summary(*args, **kwargs):
    """Deferred import of compose_enriched_summary (not yet implemented)."""
    from agentfox.engine.session_lifecycle import compose_enriched_summary

    return compose_enriched_summary(*args, **kwargs)


# ---------------------------------------------------------------------------
# TS-11-P1: Backward compatibility invariant (11-REQ-1.4, 11-REQ-3.6)
# ---------------------------------------------------------------------------


class TestPropertyBackwardCompat:
    """For any summary with no structured fields, output equals input."""

    @settings(max_examples=50)
    @given(s=_summary_text)
    def test_none_fields_identity(self, s: str) -> None:
        result = _compose_enriched_summary(
            summary=s,
            rejected_approaches=None,
            gotchas=None,
            assumptions=None,
        )
        assert result == s

    @settings(max_examples=50)
    @given(s=_summary_text)
    def test_empty_fields_identity(self, s: str) -> None:
        result = _compose_enriched_summary(
            summary=s,
            rejected_approaches=[],
            gotchas=[],
            assumptions=[],
        )
        assert result == s


# ---------------------------------------------------------------------------
# TS-11-P2: No trivial summaries invariant (11-REQ-4.1, 11-REQ-4.2, 11-REQ-4.5)
# ---------------------------------------------------------------------------


class TestPropertyNoTrivialSummaries:
    """For zero findings, generate_archetype_summary returns None."""

    @settings(max_examples=30)
    @given(st.data())
    def test_reviewer_empty_returns_none(self, data: st.DataObject) -> None:
        result = generate_archetype_summary("reviewer", findings=[])
        assert result is None


# ---------------------------------------------------------------------------
# TS-11-P3: Composition completeness (11-REQ-3.2, 11-REQ-3.3, 11-REQ-3.4)
# ---------------------------------------------------------------------------


class TestPropertyCompositionCompleteness:
    """Every entry in every populated field appears in the composed text."""

    @settings(max_examples=50)
    @given(
        s=_summary_text,
        ra=st.lists(_rejected_approach, min_size=1, max_size=5),
        g=st.lists(_field_text, min_size=1, max_size=5),
        a=st.lists(_field_text, min_size=1, max_size=5),
    )
    def test_all_entries_present(
        self,
        s: str,
        ra: list[dict[str, str]],
        g: list[str],
        a: list[str],
    ) -> None:
        result = _compose_enriched_summary(
            summary=s,
            rejected_approaches=ra,
            gotchas=g,
            assumptions=a,
        )
        for entry in ra:
            expected = f"Tried: {entry['approach']} — rejected because: {entry['reason']}"
            assert expected in result
        for gotcha in g:
            assert f"Watch out: {gotcha}" in result
        for assumption in a:
            assert f"Assumes: {assumption}" in result


# ---------------------------------------------------------------------------
# TS-11-P4: Findings-present invariant (11-REQ-4.3)
# ---------------------------------------------------------------------------


class TestPropertyFindingsPresent:
    """For any reviewer with 1+ findings, returns non-empty string."""

    @settings(max_examples=30)
    @given(
        severities=st.lists(_severity, min_size=1, max_size=10),
    )
    def test_reviewer_with_findings_returns_nonempty(
        self,
        severities: list[str],
    ) -> None:
        findings = [
            ReviewFinding(
                id=str(uuid.uuid4()),
                severity=sev,
                description=f"Finding {i}",
                requirement_ref=None,
                spec_name="test_spec",
                task_group="1",
                session_id="s1",
            )
            for i, sev in enumerate(severities)
        ]
        result = generate_archetype_summary("reviewer", findings=findings)
        assert result is not None
        assert len(result) > 0
