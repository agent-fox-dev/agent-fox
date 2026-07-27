"""Property tests for context rendering and convergence equivalence.

Test Spec: TS-27-P3, TS-27-P4, TS-27-P7
Requirements: 27-REQ-5.1, 27-REQ-5.3, 27-REQ-5.E1, 27-REQ-6.1, 27-REQ-6.2,
              27-REQ-10.1
"""

from __future__ import annotations

import uuid

import duckdb
from agentfox.knowledge.review_store import (
    ReviewFinding,
    insert_findings,
)
from agentfox.session.convergence import (
    Finding,
    converge_reviewer_pre,
    converge_reviewer_pre_records,
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


class TestConvergenceEquivalence:
    """TS-27-P4: Property 4 -- Convergence Equivalence.

    converge_reviewer_pre_records produces the same blocking decision as
    converge_reviewer_pre for equivalent input data.
    """

    @given(
        instance_count=st.integers(min_value=2, max_value=4),
        severity=st.sampled_from(list(VALID_SEVERITIES)),
        desc=st.text(
            min_size=1,
            max_size=40,
            alphabet=st.characters(whitelist_categories=("L", "N")),
        ),
        threshold=st.integers(min_value=0, max_value=3),
    )
    @settings(max_examples=30)
    def test_convergence_equivalence(
        self,
        instance_count: int,
        severity: str,
        desc: str,
        threshold: int,
    ) -> None:
        """Old and new convergence agree on blocking decision."""
        # Build identical input for both old and new convergence
        old_instances: list[list[Finding]] = []
        new_instances: list[list[ReviewFinding]] = []

        for i in range(instance_count):
            old_instances.append([Finding(severity=severity, description=desc)])
            new_instances.append(
                [
                    ReviewFinding(
                        id=str(uuid.uuid4()),
                        severity=severity,
                        description=desc,
                        requirement_ref=None,
                        spec_name="test",
                        task_group="1",
                        session_id=f"s{i}",
                    )
                ]
            )

        old_merged, old_blocked = converge_reviewer_pre(old_instances, threshold)
        new_merged, new_blocked = converge_reviewer_pre_records(new_instances, threshold)

        assert old_blocked == new_blocked
        assert len(old_merged) == len(new_merged)


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
