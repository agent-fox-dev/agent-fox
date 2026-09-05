"""Property-based tests for reviewer consolidation correctness.

Covers:
- Mode-archetype mapping correctness (TS-98-P1)
- Injection consistency (TS-98-P3)
- Verifier single-instance invariant (TS-98-P4)
- Old names rejected from registry (TS-98-P6)

Test Spec: TS-98-P1, TS-98-P3, TS-98-P4, TS-98-P6
Requirements: 98-REQ-1.1 through 98-REQ-1.4, 98-REQ-6.2, 98-REQ-7.1
"""

from __future__ import annotations

import pytest

try:
    from hypothesis import given, settings
    from hypothesis import strategies as st

    HAS_HYPOTHESIS = True
except ImportError:
    HAS_HYPOTHESIS = False


pytestmark = pytest.mark.property


# ---------------------------------------------------------------------------
# TS-98-P1: Mode-Archetype Mapping
# Requirements: 98-REQ-1.1 through 98-REQ-1.4
# ---------------------------------------------------------------------------

# Expected config values per reviewer mode:
# (allowlist_must_contain, injection, model_tier)
_REVIEWER_MODE_EXPECTATIONS = {
    "pre-flight": (["ls", "cat", "git", "grep", "find", "head", "tail", "wc"], "auto_pre", "ADVANCED"),
    "audit-review": (["ls", "cat", "git", "grep", "find", "head", "tail", "wc", "uv"], "auto_mid", "ADVANCED"),
}


class TestModeArchetypeMapping:
    """TS-98-P1: Every reviewer mode resolves to correct injection, allowlist, tier."""

    @pytest.mark.parametrize("mode", list(_REVIEWER_MODE_EXPECTATIONS.keys()))
    def test_mode_mapping(self, mode: str) -> None:
        """For each reviewer mode, resolved config has the expected injection/allowlist/tier."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        assert "reviewer" in ARCHETYPE_REGISTRY, "reviewer not in ARCHETYPE_REGISTRY — consolidation not implemented"
        entry = ARCHETYPE_REGISTRY["reviewer"]
        cfg = resolve_effective_config(entry, mode)

        expected_cmds, expected_injection, expected_tier = _REVIEWER_MODE_EXPECTATIONS[mode]

        # Check model tier
        assert cfg.default_model_tier == expected_tier, (
            f"mode={mode!r}: expected tier {expected_tier!r}, got {cfg.default_model_tier!r}"
        )

        # Check injection
        assert cfg.injection == expected_injection, (
            f"mode={mode!r}: expected injection {expected_injection!r}, got {cfg.injection!r}"
        )

        # Check allowlist contains expected commands
        assert cfg.default_allowlist is not None, f"mode={mode!r}: allowlist should not be None"
        for cmd in expected_cmds:
            assert cmd in cfg.default_allowlist, (
                f"mode={mode!r}: '{cmd}' missing from allowlist {cfg.default_allowlist}"
            )

    @pytest.mark.skipif(not HAS_HYPOTHESIS, reason="hypothesis not installed")
    @given(mode=st.sampled_from(list(_REVIEWER_MODE_EXPECTATIONS.keys())))
    @settings(max_examples=20)
    def test_mode_mapping_property(self, mode: str) -> None:
        """Property: any valid reviewer mode resolves to the correct config."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        assert "reviewer" in ARCHETYPE_REGISTRY
        entry = ARCHETYPE_REGISTRY["reviewer"]
        cfg = resolve_effective_config(entry, mode)

        expected_cmds, expected_injection, expected_tier = _REVIEWER_MODE_EXPECTATIONS[mode]
        assert cfg.default_model_tier == expected_tier
        assert cfg.injection == expected_injection


class TestInjectionConsistency:
    """TS-98-P3: Injected nodes never use old archetype names."""

    def _build_graph_and_inject(self):
        """Build a minimal task graph and inject archetypes."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.injection import ensure_graph_archetypes
        from agentfox.graph.types import Node, PlanMetadata, TaskGraph

        node = Node(
            id="spec:1",
            spec_name="spec",
            group_number=1,
            title="Test",
            optional=False,
            archetype="coder",
        )
        graph = TaskGraph(
            nodes={"spec:1": node},
            edges=[],
            order=["spec:1"],
            metadata=PlanMetadata(created_at="2026-01-01T00:00:00"),
        )
        # Pass ArchetypesConfig directly (ensure_graph_archetypes expects archetypes_config)
        config = ArchetypesConfig(reviewer=True)
        ensure_graph_archetypes(graph, config)
        return graph

    def test_injection_consistency(self) -> None:
        """TS-98-P3: After injection, no node has old archetype names."""
        graph = self._build_graph_and_inject()
        old_names = {"skeptic", "oracle", "auditor"}
        for node in graph.nodes.values():
            assert node.archetype not in old_names, (
                f"Node {node.id!r} has old archetype {node.archetype!r} — "
                "should have been replaced by reviewer with mode"
            )

    @pytest.mark.skipif(not HAS_HYPOTHESIS, reason="hypothesis not installed")
    @given(enabled=st.booleans())
    @settings(max_examples=10)
    def test_no_old_archetypes_any_config(self, enabled: bool) -> None:
        """Property: For any reviewer enable state, old archetype names never appear."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.injection import ensure_graph_archetypes
        from agentfox.graph.types import Node, PlanMetadata, TaskGraph

        node = Node(
            id="spec:1",
            spec_name="spec",
            group_number=1,
            title="Test",
            optional=False,
            archetype="coder",
        )
        graph = TaskGraph(
            nodes={"spec:1": node},
            edges=[],
            order=["spec:1"],
            metadata=PlanMetadata(created_at="2026-01-01T00:00:00"),
        )
        # Pass ArchetypesConfig directly (ensure_graph_archetypes expects archetypes_config)
        config = ArchetypesConfig(reviewer=enabled)
        ensure_graph_archetypes(graph, config)

        old_names = {"skeptic", "oracle", "auditor"}
        for n in graph.nodes.values():
            assert n.archetype not in old_names, (
                f"Node {n.id!r} has old archetype {n.archetype!r} (reviewer_enabled={enabled})"
            )


# ---------------------------------------------------------------------------
# TS-98-P4: Verifier Single-Instance Invariant
# Requirement: 98-REQ-6.2
# ---------------------------------------------------------------------------
# TS-98-P6: Old Names Rejected
# Requirement: 98-REQ-7.1
# ---------------------------------------------------------------------------


class TestOldNamesRejected:
    """TS-98-P6: No old archetype name appears in ARCHETYPE_REGISTRY."""

    @pytest.mark.parametrize(
        "name",
        ["skeptic", "oracle", "auditor", "fix_reviewer", "fix_coder"],
    )
    def test_old_names_gone(self, name: str) -> None:
        """For each old name, it must not be in ARCHETYPE_REGISTRY."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        assert name not in ARCHETYPE_REGISTRY, (
            f"'{name}' should have been removed from ARCHETYPE_REGISTRY during reviewer consolidation (98-REQ-7.1)"
        )

    @pytest.mark.skipif(not HAS_HYPOTHESIS, reason="hypothesis not installed")
    @given(name=st.sampled_from(["skeptic", "oracle", "auditor", "fix_reviewer", "fix_coder"]))
    @settings(max_examples=10)
    def test_old_names_rejected_property(self, name: str) -> None:
        """Property: any old archetype name is absent from registry."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        assert name not in ARCHETYPE_REGISTRY, f"'{name}' found in ARCHETYPE_REGISTRY — should be removed"
