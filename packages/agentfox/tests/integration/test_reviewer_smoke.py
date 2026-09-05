"""Integration smoke tests for reviewer consolidation wiring.

End-to-end tests that exercise real components without mocking injection,
convergence logic, or template loading.

Test Spec: TS-98-SMOKE-1, TS-98-SMOKE-2, TS-98-SMOKE-3
Requirements: 98-REQ-4.1, 98-REQ-4.2, 98-REQ-4.4, 98-REQ-5.1, 98-REQ-5.2
"""

from __future__ import annotations

from unittest.mock import MagicMock

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_reviewer_config(reviewer: bool = True):
    """Build a minimal ArchetypesConfig with reviewer enabled."""
    from agentfox.core.config import ArchetypesConfig

    return ArchetypesConfig(reviewer=reviewer)


def _make_coder_graph(spec_name: str = "myspec", group_number: int = 1):
    """Build a minimal TaskGraph with one coder node."""
    from agentfox.graph.types import Node, PlanMetadata, TaskGraph

    node = Node(
        id=f"{spec_name}:{group_number}",
        spec_name=spec_name,
        group_number=group_number,
        title="Test Task",
        optional=False,
        archetype="coder",
    )
    return TaskGraph(
        nodes={node.id: node},
        edges=[],
        order=[node.id],
        metadata=PlanMetadata(created_at="2026-01-01T00:00:00"),
    )


def _make_finding(severity: str = "critical", description: str = "Test finding"):
    """Build a Finding for convergence tests."""
    from agentfox.session.convergence import Finding

    return Finding(severity=severity, description=description)


# ---------------------------------------------------------------------------
# TS-98-SMOKE-1: Pre-review End-to-End
# Execution Path 1 from design.md
# Requirements: 98-REQ-4.2, 98-REQ-5.1
# ---------------------------------------------------------------------------


class TestPreReviewEndToEnd:
    """TS-98-SMOKE-1: Pre-flight injection through convergence.

    Verifies the complete chain:
    1. ensure_graph_archetypes creates reviewer:pre-flight node
    2. converge_reviewer dispatches to skeptic algorithm for pre-flight

    Does NOT mock injection or convergence logic.
    """

    def test_pre_flight_node_injected(self) -> None:
        """TS-98-SMOKE-1 (part 1): Graph contains reviewer:pre-flight after injection."""
        from agentfox.graph.injection import ensure_graph_archetypes

        graph = _make_coder_graph(spec_name="smoke_spec", group_number=1)
        config = _make_reviewer_config(reviewer=True)

        # Run real ensure_graph_archetypes (no mocking)
        ensure_graph_archetypes(graph, config)

        pre_nodes = [n for n in graph.nodes.values() if n.archetype == "reviewer" and n.mode == "pre-flight"]
        assert len(pre_nodes) >= 1, (
            f"Expected at least one reviewer:pre-flight node after injection, "
            f"got nodes: {[(n.archetype, n.mode) for n in graph.nodes.values()]}"
        )

    def test_pre_flight_convergence_uses_skeptic_algorithm(self) -> None:
        """TS-98-SMOKE-1 (part 2): converge_reviewer uses skeptic algorithm for pre-flight."""
        from agentfox.session.convergence import converge_reviewer, converge_reviewer_pre

        # Build mock findings (list[list[Finding]])
        findings_instance_1 = [
            _make_finding("critical", "Missing input validation"),
            _make_finding("major", "No error handling"),
        ]
        findings_instance_2 = [
            _make_finding("critical", "Missing input validation"),
        ]
        results = [findings_instance_1, findings_instance_2]

        # Run converge_reviewer with mode="pre-flight" (uses real convergence logic)
        reviewer_merged, reviewer_blocked = converge_reviewer(results, mode="pre-flight", block_threshold=3)
        # Run converge_reviewer_pre directly for comparison
        skeptic_merged, skeptic_blocked = converge_reviewer_pre(results, block_threshold=3)

        # Results must be identical — same algorithm
        assert reviewer_blocked == skeptic_blocked, (
            f"converge_reviewer('pre-flight') blocked={reviewer_blocked} "
            f"but converge_reviewer_pre blocked={skeptic_blocked}"
        )
        # Merged finding counts should be equal
        assert len(reviewer_merged) == len(skeptic_merged), (
            f"Merged finding count mismatch: reviewer={len(reviewer_merged)}, skeptic={len(skeptic_merged)}"
        )

    def test_no_old_archetype_nodes_after_injection(self) -> None:
        """TS-98-SMOKE-1 (property): No skeptic/oracle nodes after injection."""
        from agentfox.graph.injection import ensure_graph_archetypes

        graph = _make_coder_graph()
        config = _make_reviewer_config(reviewer=True)
        ensure_graph_archetypes(graph, config)

        all_archetypes = {n.archetype for n in graph.nodes.values()}
        assert "skeptic" not in all_archetypes, f"'skeptic' node found after consolidation: {all_archetypes}"
        assert "oracle" not in all_archetypes, f"'oracle' node found after consolidation: {all_archetypes}"
        assert "auditor" not in all_archetypes, f"'auditor' node found after consolidation: {all_archetypes}"


# ---------------------------------------------------------------------------
# TS-98-SMOKE-3: Coder Session Setup
# Execution Path 4 from design.md
# ---------------------------------------------------------------------------

_MOCK_KB = MagicMock()
_MOCK_KB.execute.return_value.fetchall.return_value = []


class TestCoderSessionSetup:
    """TS-98-SMOKE-3: Coder resolves correct config in session.

    Does NOT mock resolve_model_tier or resolve_security_config.
    """

    def test_coder_resolves_to_standard(self) -> None:
        """TS-98-SMOKE-3: coder resolves to STANDARD via registry default."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig()

        # Real NodeSessionRunner — no mocking of tier resolution
        runner = NodeSessionRunner(
            "myspec:1",
            config,
            archetype="coder",
            knowledge_db=_MOCK_KB,
        )

        # Coder registry default is STANDARD → claude-sonnet-4-6 (spec 15)
        assert runner._resolved_model_id == "claude-sonnet-4-6", (
            f"coder should resolve to STANDARD (Sonnet) via registry default (spec 15), "
            f"got {runner._resolved_model_id!r}"
        )

    def test_coder_no_mode_uses_default_profile(self) -> None:
        """TS-98-SMOKE-3 (regression): coder with no mode uses coder.md."""
        from agentfox.session.prompt import build_system_prompt

        # Real build_system_prompt with mode=None — no mocking
        system_prompt = build_system_prompt("test context", mode=None)

        assert len(system_prompt) > 100, "System prompt for coder (no mode) should have meaningful content"
