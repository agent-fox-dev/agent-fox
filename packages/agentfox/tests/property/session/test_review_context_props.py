"""Property tests for review context rendering.

Test Spec: TS-27-P3, TS-27-P7
Requirements: 27-REQ-5.1, 27-REQ-5.3, 27-REQ-5.E1, 27-REQ-10.1
"""

from __future__ import annotations

import uuid

import duckdb
from agentfox.knowledge.review_store import (
    ReviewFinding,
    insert_findings,
)
from agentfox.session.prompt import render_review_context
from hypothesis import given, settings
from hypothesis import strategies as st

from tests.unit.knowledge.conftest import create_schema

VALID_SEVERITIES = ("critical", "major", "minor", "observation")

# Only these severities are persisted by insert_findings() (issue #553).
ACTIONABLE_SEVERITIES = ("critical", "major")


@st.composite
def review_finding_list(draw: st.DrawFn) -> list[ReviewFinding]:
    """Generate a list of ReviewFinding objects with actionable severities.

    Restricted to critical/major because insert_findings() drops minor and
    observation findings (issue #553). The rendering properties tested here
    are about what appears in the output given the DB state — non-actionable
    findings are never in the DB and thus never appear in renders.
    """
    n = draw(st.integers(min_value=1, max_value=10))
    session_id = f"session-{draw(st.uuids())}"
    return [
        ReviewFinding(
            id=str(uuid.uuid4()),
            severity=draw(st.sampled_from(list(ACTIONABLE_SEVERITIES))),
            description=draw(
                st.text(
                    min_size=1,
                    max_size=80,
                    alphabet=st.characters(whitelist_categories=("L", "N", "P", "Z")),
                )
            ),
            requirement_ref=None,
            spec_name="prop_test_spec",
            task_group="1",
            session_id=session_id,
        )
        for _ in range(n)
    ]


class TestContextRenderingDeterminism:
    """TS-27-P3: Property 3 -- Context Rendering Structural Consistency.

    For any set of active findings, render_review_context produces
    structurally consistent output on repeated calls with the same DB state.

    Note: nonce-tagged boundaries (from sanitize_prompt_content) make
    exact string equality across calls impossible by design. The invariant
    is that all finding descriptions appear in both outputs.
    """

    @given(findings=review_finding_list())
    @settings(max_examples=20)
    def test_render_determinism(self, findings: list[ReviewFinding]) -> None:
        """Two calls to render_review_context include the same descriptions."""
        conn = duckdb.connect(":memory:")
        create_schema(conn)
        insert_findings(conn, findings)

        md1 = render_review_context(conn, "prop_test_spec")
        md2 = render_review_context(conn, "prop_test_spec")

        # Both renders must be non-None (same findings -> same non-empty result)
        assert (md1 is None) == (md2 is None)
        if md1 is None or md2 is None:
            conn.close()
            return

        # Every finding description must appear in both renders
        for finding in findings:
            assert finding.description in md1, f"Description '{finding.description}' missing from first render"
            assert finding.description in md2, f"Description '{finding.description}' missing from second render"

        # Structural markers must be present in both renders
        assert "## Reviewer Findings" in md1
        assert "## Reviewer Findings" in md2
        assert "Summary:" in md1
        assert "Summary:" in md2

        conn.close()


class TestFallbackCorrectness:
    """TS-27-P7: Property 7 -- Fallback Correctness.

    Review findings from DB are surfaced via render_review_context.
    Updated for spec 38: DuckDB is now mandatory, so conn is always provided.
    """

    @given(findings=review_finding_list())
    @settings(max_examples=10)
    def test_fallback_correctness(
        self,
        findings: list[ReviewFinding],
    ) -> None:
        """render_review_context includes findings when DB has records."""
        conn = duckdb.connect(":memory:")
        create_schema(conn)
        insert_findings(conn, findings)

        result = render_review_context(conn, "prop_test_spec")
        assert result is not None
        assert "Reviewer Findings" in result

        for finding in findings:
            assert finding.description in result

        conn.close()
