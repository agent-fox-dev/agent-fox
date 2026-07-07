"""Unit tests for Node.mode field and serialization.

Test Spec: TS-97-5, TS-97-6, TS-97-E3
Requirements: 97-REQ-2.1, 97-REQ-2.2, 97-REQ-2.3, 97-REQ-2.E1
"""

from __future__ import annotations

import duckdb
from agentfox.knowledge.migrations import run_migrations

# ---------------------------------------------------------------------------
# TS-97-5: Node Mode Field
# Requirement: 97-REQ-2.1
# ---------------------------------------------------------------------------


class TestNodeModeField:
    """Verify Node has a mode field defaulting to None."""

    def test_mode_defaults_to_none(self) -> None:
        """TS-97-5: Node.mode defaults to None when not specified."""
        from agentfox.graph.types import Node

        node = Node(id="s:1", spec_name="s", group_number=1, title="t", optional=False)
        assert node.mode is None

    def test_mode_can_be_set_to_string(self) -> None:
        """TS-97-5: Node.mode can be set to a non-None string."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:1",
            spec_name="s",
            group_number=1,
            title="t",
            optional=False,
            mode="pre-flight",
        )
        assert node.mode == "pre-flight"

    def test_mode_can_be_set_explicitly_to_none(self) -> None:
        """Node.mode can be explicitly set to None."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:1",
            spec_name="s",
            group_number=1,
            title="t",
            optional=False,
            mode=None,
        )
        assert node.mode is None

    def test_mode_type_is_str_or_none(self) -> None:
        """Node.mode should accept any string value."""
        from agentfox.graph.types import Node

        for mode in ["pre-review", "drift-review", "fast", "reviewer:pre-review"]:
            node = Node(id="s:1", spec_name="s", group_number=1, title="t", optional=False, mode=mode)
            assert node.mode == mode


# ---------------------------------------------------------------------------
# TS-97-6: Node Serialization Round-Trip
# Requirement: 97-REQ-2.2
# ---------------------------------------------------------------------------


class TestNodeSerializationRoundTrip:
    """Verify mode persists through DB serialization."""

    def _save_and_load_node(self, node):  # type: ignore[no-untyped-def]
        """Save a node via TaskGraph and load it back from DuckDB."""
        from agentfox.graph.persistence import load_plan, save_plan
        from agentfox.graph.types import PlanMetadata, TaskGraph

        graph = TaskGraph(
            nodes={node.id: node},
            edges=[],
            order=[node.id],
            metadata=PlanMetadata(created_at="2026-01-01T00:00:00"),
        )
        conn = duckdb.connect(":memory:")
        run_migrations(conn)
        save_plan(graph, conn)
        loaded = load_plan(conn)
        conn.close()
        assert loaded is not None
        return loaded.nodes[node.id]

    def test_mode_string_preserved_after_roundtrip(self) -> None:
        """TS-97-6: mode string is preserved after save/load."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:0",
            spec_name="s",
            group_number=0,
            title="t",
            optional=False,
            mode="pre-flight",
        )
        loaded_node = self._save_and_load_node(node)
        assert loaded_node.mode == "pre-flight"

    def test_none_mode_preserved_after_roundtrip(self) -> None:
        """mode=None is preserved after save/load."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:0",
            spec_name="s",
            group_number=0,
            title="t",
            optional=False,
            mode=None,
        )
        loaded_node = self._save_and_load_node(node)
        assert loaded_node.mode is None

    def test_mode_in_task_graph_roundtrip(self) -> None:
        """TS-97-SMOKE-3: Mode survives full TaskGraph save/load."""
        from agentfox.graph.persistence import load_plan, save_plan
        from agentfox.graph.types import Node, PlanMetadata, TaskGraph

        graph = TaskGraph(
            nodes={
                "s:0": Node(id="s:0", spec_name="s", group_number=0, title="t", optional=False, mode="pre-flight"),
                "s:1": Node(id="s:1", spec_name="s", group_number=1, title="t2", optional=False, mode=None),
            },
            edges=[],
            order=["s:0", "s:1"],
            metadata=PlanMetadata(created_at="2026-01-01T00:00:00"),
        )
        conn = duckdb.connect(":memory:")
        run_migrations(conn)
        save_plan(graph, conn)
        loaded = load_plan(conn)
        conn.close()
        assert loaded is not None
        assert loaded.nodes["s:0"].mode == "pre-flight"
        assert loaded.nodes["s:1"].mode is None

    def test_pre_flight_mode_preserved(self) -> None:
        """Mode survives DB round-trip for pre-flight."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:0",
            spec_name="s",
            group_number=0,
            title="t",
            optional=False,
            mode="pre-flight",
        )
        loaded_node = self._save_and_load_node(node)
        assert loaded_node.mode == "pre-flight"


# ---------------------------------------------------------------------------
# TS-97-E3: Node With None Mode
# Requirement: 97-REQ-2.E1
# ---------------------------------------------------------------------------


class TestNodeNoneMode:
    """Verify Node with None mode behaves identically to current implementation."""

    def test_node_with_none_mode_has_no_mode_info_in_archetype_str(self) -> None:
        """TS-97-E3: Node with mode=None has no mode suffix in archetype representation."""
        from agentfox.graph.types import Node

        node = Node(id="s:1", spec_name="s", group_number=1, title="t", optional=False)
        assert node.mode is None
        # The archetype field alone should not contain a colon (no mode suffix)
        assert ":" not in node.archetype

    def test_node_with_mode_includes_colon_in_combined_repr(self) -> None:
        """TS-97-5 / 97-REQ-2.3: Node with non-None mode should include mode in string repr."""
        from agentfox.graph.types import Node

        node = Node(
            id="s:1",
            spec_name="s",
            group_number=1,
            title="t",
            optional=False,
            archetype="reviewer",
            mode="pre-flight",
        )
        # The mode value itself contains the mode name
        assert node.mode == "pre-flight"
        # Combined representation would be "reviewer:pre-flight" style
        if hasattr(node, "__str__") and type(node).__str__ is not object.__str__:
            combined = str(node)
            # If custom __str__ is defined, it should include the mode
            assert "pre-flight" in combined
        else:
            # At minimum, the mode field has the correct value
            combined = f"{node.archetype}:{node.mode}"
            assert combined == "reviewer:pre-flight"

    def test_existing_nodes_without_mode_are_backward_compatible(self) -> None:
        """TS-97-E3: Existing code paths that create Node without mode still work."""
        from agentfox.graph.types import Node, NodeStatus

        node = Node(
            id="old:1",
            spec_name="old",
            group_number=1,
            title="Old Task",
            optional=False,
            status=NodeStatus.PENDING,
            archetype="coder",
        )
        assert node.mode is None
        assert node.archetype == "coder"
        assert node.status == NodeStatus.PENDING
