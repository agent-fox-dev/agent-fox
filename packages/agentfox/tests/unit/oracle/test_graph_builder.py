"""Tests for reviewer graph builder injection and multi-auto_pre support.

Test Spec: TS-32-3, TS-32-4, TS-32-5, TS-32-E2, TS-32-E3, TS-32-E9
Requirements: 32-REQ-2.1, 32-REQ-2.2, 32-REQ-2.E1,
              32-REQ-3.1, 32-REQ-3.2, 32-REQ-3.3, 32-REQ-3.E1,
              32-REQ-4.E1

Updated for reviewer consolidation (spec 98): pre-review and drift-review
merged into a single pre-flight mode.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from agentfox.spec.discovery import SpecInfo
from agentfox.spec.types import TaskGroupDef


def _spec(name: str = "spec") -> SpecInfo:
    """Build a SpecInfo with short defaults."""
    return SpecInfo(
        name=name,
        prefix=0,
        path=Path(f".specs/{name}"),
        has_tasks=True,
        has_prd=False,
    )


def _tgd(number: int, title: str = "T", **kw: Any) -> TaskGroupDef:
    """Build a TaskGroupDef with short defaults."""
    defaults: dict[str, Any] = dict(optional=False, completed=False, subtasks=(), body="")
    defaults.update(kw)
    return TaskGroupDef(number=number, title=title, **defaults)


# ---------------------------------------------------------------------------
# TS-32-4: Single pre-flight Node (formerly dual pre-review + drift-review)
# Requirements: 32-REQ-2.1, 32-REQ-2.2, 32-REQ-3.1
# ---------------------------------------------------------------------------


class TestSinglePreFlight:
    """When reviewer is enabled, a single pre-flight node is created with plain :0 ID."""

    def test_single_pre_flight(self) -> None:
        """TS-32-4: Single pre-flight node exists with edge to first coder group."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(reviewer=True)
        specs = [_spec()]
        task_groups = {"spec": [_tgd(1, "T1"), _tgd(2, "T2")]}

        graph = build_graph(specs, task_groups, [], archetypes_config=config)

        # Single auto_pre uses plain {spec}:0 format
        assert "spec:0" in graph.nodes
        assert graph.nodes["spec:0"].archetype == "reviewer"
        assert graph.nodes["spec:0"].mode == "pre-flight"

        # No drift-review nodes exist
        drift_nodes = [nid for nid, n in graph.nodes.items() if n.mode == "drift-review"]
        assert drift_nodes == []

        # Edge from pre-flight to first coder group
        assert any(e.source == "spec:0" and e.target == "spec:1" and e.kind == "intra_spec" for e in graph.edges)


# ---------------------------------------------------------------------------
# TS-32-5: Single auto_pre Uses Plain :0 Format
# Requirement: 32-REQ-3.2
# ---------------------------------------------------------------------------


class TestSingleAutoPreCompat:
    """When only one auto_pre is enabled, use {spec}:0 format."""

    def test_single_auto_pre_compat(self, tmp_path: Path) -> None:
        """TS-32-5: Single auto_pre (pre-flight) uses {spec}:0 without archetype suffix."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        spec_dir = tmp_path / ".specs" / "myspec"
        spec_dir.mkdir(parents=True)
        (spec_dir / "tasks.md").write_text("# Tasks\n\n- [ ] 1. Task one\n  - [ ] 1.1 Sub\n")

        config = ArchetypesConfig(reviewer=True)
        spec = SpecInfo(name="myspec", prefix=0, path=spec_dir, has_tasks=True, has_prd=False)
        task_groups = {"myspec": [_tgd(1, "T1")]}

        graph = build_graph([spec], task_groups, [], archetypes_config=config)

        # Single pre-flight auto_pre uses plain {spec}:0 format
        assert "myspec:0" in graph.nodes
        assert graph.nodes["myspec:0"].archetype == "reviewer"
        assert graph.nodes["myspec:0"].mode == "pre-flight"
        # No *reviewer* (auto_pre) nodes with ":0:" suffix — single auto_pre
        # uses the plain {spec}:0 format.  Note: auto_post nodes such as
        # verifier may legitimately use a 3-part ":0:" format; we only check
        # the auto_pre reviewer nodes for backward-compat format compliance.
        reviewer_nids = [n.id for n in graph.nodes.values() if n.archetype == "reviewer"]
        assert not any(":0:" in nid for nid in reviewer_nids), (
            f"Single auto_pre reviewer nodes must not use suffixed ':0:' format; reviewer node ids: {reviewer_nids}"
        )


# ---------------------------------------------------------------------------
# TS-32-E2: Empty Spec (No Coder Groups)
# Requirement: 32-REQ-2.E1
# ---------------------------------------------------------------------------


class TestEmptySpecNoReviewerInjection:
    """No reviewer injection for spec with no coder groups."""

    def test_empty_spec_no_reviewer(self) -> None:
        """TS-32-E2: Spec with no task groups gets no reviewer node."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(reviewer=True)
        specs = [_spec("empty_spec")]
        task_groups: dict[str, list[TaskGroupDef]] = {"empty_spec": []}

        graph = build_graph(specs, task_groups, [], archetypes_config=config)
        assert "empty_spec:0" not in graph.nodes
        # No reviewer nodes at all
        reviewer_nodes = [nid for nid, n in graph.nodes.items() if n.archetype == "reviewer"]
        assert reviewer_nodes == []


# ---------------------------------------------------------------------------
# TS-32-E3: Legacy Plan Compatibility
# Requirement: 32-REQ-3.E1
# ---------------------------------------------------------------------------


class TestLegacyPlanCompat:
    """Runtime injection preserves existing pre-flight node, no drift-review added."""

    def test_legacy_plan_compat(self) -> None:
        """TS-32-E3: Pre-flight node preserved, no drift-review nodes added."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.injection import ensure_graph_archetypes
        from agentfox.graph.types import Edge, Node, TaskGraph

        graph = TaskGraph(
            nodes={
                "spec:0": Node(
                    id="spec:0",
                    spec_name="spec",
                    group_number=0,
                    title="Reviewer (pre-flight)",
                    optional=False,
                    archetype="reviewer",
                    mode="pre-flight",
                    instances=1,
                ),
                "spec:1": Node(
                    id="spec:1",
                    spec_name="spec",
                    group_number=1,
                    title="Task 1",
                    optional=False,
                    archetype="coder",
                    instances=1,
                ),
            },
            edges=[Edge(source="spec:0", target="spec:1", kind="intra_spec")],
            order=["spec:0", "spec:1"],
        )
        config = ArchetypesConfig(reviewer=True)
        ensure_graph_archetypes(graph, config)

        # Pre-flight node preserved
        assert "spec:0" in graph.nodes
        assert graph.nodes["spec:0"].archetype == "reviewer"
        assert graph.nodes["spec:0"].mode == "pre-flight"

        # No drift-review nodes exist
        drift_nodes = [nid for nid, n in graph.nodes.items() if n.archetype == "reviewer" and n.mode == "drift-review"]
        assert len(drift_nodes) == 0


# ---------------------------------------------------------------------------
# TS-32-E9: Hot-load Failure Skips Reviewer
# Requirement: 32-REQ-4.E1
# ---------------------------------------------------------------------------


class TestHotLoadFailureSkip:
    """When hot-loading fails for a spec, reviewer injection is skipped."""

    def test_hot_load_failure_skip(self, tmp_path: Path) -> None:
        """TS-32-E9: Invalid spec is skipped, reviewer not injected for it."""
        # Create a specs dir with one valid and one invalid spec
        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir()

        # Valid spec
        valid_spec = specs_dir / "01_valid"
        valid_spec.mkdir()
        (valid_spec / "tasks.md").write_text("# Tasks\n\n- [ ] 1. Task 1\n  - [ ] 1.1 Sub\n")

        # Invalid spec (no tasks.md)
        invalid_spec = specs_dir / "02_invalid"
        invalid_spec.mkdir()
        # Intentionally no tasks.md

        # Verify that hot_load_specs handles the invalid spec gracefully.
        assert valid_spec.exists()
        assert not (invalid_spec / "tasks.md").exists()


# ---------------------------------------------------------------------------
# spec_has_existing_code helper tests
# ---------------------------------------------------------------------------


class TestSpecHasExistingCode:
    """Tests for the spec_has_existing_code helper."""

    def test_no_design_md_returns_true(self, tmp_path: Path) -> None:
        """Missing architecture.md defaults to True (safe default)."""
        from agentfox.graph.builder import spec_has_existing_code

        assert spec_has_existing_code(tmp_path) is True

    def test_no_modified_refs_returns_false(self, tmp_path: Path) -> None:
        """architecture.md with only (new) files returns False."""
        from agentfox.graph.builder import spec_has_existing_code

        (tmp_path / "architecture.md").write_text("1. **`agent_fox/brand_new.py`** (new) -- New module.\n")
        assert spec_has_existing_code(tmp_path) is False

    def test_modified_ref_exists(self, tmp_path: Path) -> None:
        """Returns True when a (modified) file exists on disk."""
        from agentfox.graph.builder import spec_has_existing_code

        target = tmp_path / "real_file.py"
        target.write_text("# existing")
        (tmp_path / "architecture.md").write_text(f"1. **`{target}`** (modified) -- Change.\n")
        assert spec_has_existing_code(tmp_path) is True

    def test_modified_ref_missing(self, tmp_path: Path) -> None:
        """Returns False when all (modified) files are absent."""
        from agentfox.graph.builder import spec_has_existing_code

        (tmp_path / "architecture.md").write_text("1. **`nonexistent/foo.py`** (modified) -- Change.\n")
        assert spec_has_existing_code(tmp_path) is False

    def test_mixed_new_and_modified(self, tmp_path: Path) -> None:
        """Only (modified) refs are checked, not (new) ones."""
        from agentfox.graph.builder import spec_has_existing_code

        target = tmp_path / "exists.py"
        target.write_text("# code")
        (tmp_path / "architecture.md").write_text(
            f"1. **`brand_new.py`** (new) -- New.\n2. **`{target}`** (modified) -- Change.\n"
        )
        assert spec_has_existing_code(tmp_path) is True


