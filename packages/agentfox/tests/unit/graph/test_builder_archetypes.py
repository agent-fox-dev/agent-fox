"""Tests for graph builder archetype injection and serialization.

Test Spec: TS-26-13 through TS-26-21, TS-26-E5 through TS-26-E8,
           TS-26-P7, TS-26-P8, TS-26-P14
Requirements: 26-REQ-4.1 through 26-REQ-4.E2,
              26-REQ-5.1 through 26-REQ-5.E2
"""

from __future__ import annotations

import json
import logging
from pathlib import Path
from typing import TYPE_CHECKING, Any

import duckdb
import pytest
from agentfox.knowledge.migrations import run_migrations

if TYPE_CHECKING:
    from agentfox.spec.types import TaskGroupDef

try:
    from hypothesis import given, settings
    from hypothesis import strategies as st

    HAS_HYPOTHESIS = True
except ImportError:
    HAS_HYPOTHESIS = False


def _tgd(number: int, title: str = "T", **kw: Any) -> TaskGroupDef:
    """Build a TaskGroupDef with short defaults."""
    from agentfox.spec.types import TaskGroupDef

    defaults: dict[str, Any] = dict(optional=False, completed=False, subtasks=(), body="")
    defaults.update(kw)
    return TaskGroupDef(number=number, title=title, **defaults)


def _spec(name="spec"):
    """Build a SpecInfo with short defaults."""
    from agentfox.spec.discovery import SpecInfo

    return SpecInfo(
        name=name,
        prefix=0,
        path=Path(f".specs/{name}"),
        has_tasks=True,
        has_prd=False,
    )


# -------------------------------------------------------------------
# TS-26-13: Node dataclass has archetype and instances
# Requirement: 26-REQ-4.1
# -------------------------------------------------------------------


class TestNodeArchetypeDefaults:
    """Verify Node has archetype and instances with defaults."""

    def test_default_archetype(self) -> None:
        from agentfox.graph.types import Node

        node = Node(
            id="s:1",
            spec_name="s",
            group_number=1,
            title="t",
            optional=False,
        )
        assert node.archetype == "coder"
        assert node.instances == 1

    def test_custom_archetype(self) -> None:
        from agentfox.graph.types import Node

        node = Node(
            id="s:0",
            spec_name="s",
            group_number=0,
            title="Review",
            optional=False,
            archetype="reviewer",
            mode="pre-review",
            instances=3,
        )
        assert node.archetype == "reviewer"
        assert node.mode == "pre-review"
        assert node.instances == 3


# -------------------------------------------------------------------
# TS-26-14: Plan serialization includes archetype fields
# Requirement: 26-REQ-4.2
# -------------------------------------------------------------------


class TestPlanSerializationArchetype:
    """Verify plan DB includes archetype and instances."""

    def test_serialization_includes_fields(self) -> None:
        from agentfox.graph.persistence import load_plan, save_plan
        from agentfox.graph.types import Node, TaskGraph

        node = Node(
            id="s:0",
            spec_name="s",
            group_number=0,
            title="Pre-Review",
            optional=False,
            archetype="reviewer",
            mode="pre-review",
            instances=3,
        )
        graph = TaskGraph(
            nodes={"s:0": node},
            edges=[],
            order=["s:0"],
        )
        conn = duckdb.connect(":memory:")
        run_migrations(conn)
        save_plan(graph, conn)

        loaded = load_plan(conn)
        assert loaded is not None
        assert loaded.nodes["s:0"].archetype == "reviewer"
        assert loaded.nodes["s:0"].instances == 3
        conn.close()


# -------------------------------------------------------------------
# TS-26-15: Legacy plan.json defaults
# Requirement: 26-REQ-4.3
# -------------------------------------------------------------------


class TestLegacyPlanDefaults:
    """Verify plan node without archetype defaults to coder/1."""

    def test_legacy_plan_defaults(self) -> None:
        from agentfox.graph.persistence import load_plan, save_plan
        from agentfox.graph.types import Node, TaskGraph

        # Save a plan with default archetype (coder) and instances (1)
        graph = TaskGraph(
            nodes={
                "s:1": Node(
                    id="s:1",
                    spec_name="s",
                    group_number=1,
                    title="t",
                    optional=False,
                ),
            },
            edges=[],
            order=["s:1"],
        )
        conn = duckdb.connect(":memory:")
        run_migrations(conn)
        save_plan(graph, conn)

        loaded = load_plan(conn)
        assert loaded is not None
        assert loaded.nodes["s:1"].archetype == "coder"
        assert loaded.nodes["s:1"].instances == 1
        conn.close()


# -------------------------------------------------------------------
# TS-26-17: tasks.md archetype tag extraction
# Requirement: 26-REQ-5.1
# -------------------------------------------------------------------


class TestArchetypeTagExtraction:
    """Verify parse_tasks() returns groups with archetype=None (v1.2)."""

    @staticmethod
    def _write_spec(spec_dir: Path, task_groups: list[dict]) -> None:
        spec_dir.mkdir(parents=True, exist_ok=True)
        (spec_dir / "prd.md").write_text(
            '---\nspec_id: "test"\nspec_name: "test"\ntitle: "Test"\n'
            'status: "draft"\ncreated_at: "2024-01-01T00:00:00Z"\n'
            'updated_at: "2024-01-01T00:00:00Z"\nowner: "test"\n'
            'source: "test"\nschema_version: 1\n---\n# Test\n'
        )
        (spec_dir / "requirements.json").write_text(
            json.dumps(
                {
                    "spec_id": "test",
                    "spec_name": "test",
                    "schema_version": 1,
                    "introduction": "",
                    "glossary": {},
                    "requirements": [],
                    "correctness_properties": [],
                    "execution_paths": [],
                    "error_handling": [],
                }
            )
        )
        (spec_dir / "test_spec.json").write_text(
            json.dumps(
                {
                    "spec_id": "test",
                    "spec_name": "test",
                    "schema_version": 1,
                    "test_cases": [],
                    "property_tests": [],
                    "edge_case_tests": [],
                    "smoke_tests": [],
                    "coverage": {
                        "requirements_covered": [],
                        "properties_covered": [],
                        "paths_covered": [],
                        "gaps": [],
                    },
                }
            )
        )
        (spec_dir / "tasks.json").write_text(
            json.dumps(
                {
                    "spec_id": "test",
                    "spec_name": "test",
                    "schema_version": 1,
                    "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
                    "dependencies": [],
                    "task_groups": task_groups,
                    "traceability": [],
                }
            )
        )

    def test_v12_group_parsed_with_title(
        self,
        tmp_path: pytest.TempPathFactory,
    ) -> None:
        from agentfox.spec.parser import parse_tasks

        spec_dir = tmp_path / "03_spec"  # type: ignore[operator]
        self._write_spec(
            spec_dir,
            [
                {
                    "id": 3,
                    "kind": "standard",
                    "title": "Update docs",
                    "subtasks": [
                        {
                            "id": "3.1",
                            "title": "Write docs",
                            "state": "pending",
                            "details": [],
                            "test_spec_refs": [],
                            "requirement_refs": [],
                            "optional": False,
                        }
                    ],
                    "verification": {"id": "", "checks": []},
                }
            ],
        )

        groups = parse_tasks(spec_dir)
        assert len(groups) == 1
        assert groups[0].archetype is None
        assert "Update docs" in groups[0].title

    def test_no_tag_leaves_none(
        self,
        tmp_path: pytest.TempPathFactory,
    ) -> None:
        from agentfox.spec.parser import parse_tasks

        spec_dir = tmp_path / "01_spec"  # type: ignore[operator]
        self._write_spec(
            spec_dir,
            [
                {
                    "id": 1,
                    "kind": "standard",
                    "title": "Normal task",
                    "subtasks": [
                        {
                            "id": "1.1",
                            "title": "Sub",
                            "state": "pending",
                            "details": [],
                            "test_spec_refs": [],
                            "requirement_refs": [],
                            "optional": False,
                        }
                    ],
                    "verification": {"id": "", "checks": []},
                }
            ],
        )

        groups = parse_tasks(spec_dir)
        assert len(groups) == 1
        assert groups[0].archetype is None


# -------------------------------------------------------------------
# TS-26-18: Three-layer assignment priority
# Requirement: 26-REQ-5.2
# -------------------------------------------------------------------


class TestThreeLayerPriority:
    """Verify assignment layers in correct priority order."""

    def test_tasks_md_tag_wins(self) -> None:
        from agentfox.graph.builder import build_graph

        specs = [_spec()]
        task_groups = {"spec": [_tgd(3, "Task", archetype="reviewer")]}

        graph = build_graph(specs, task_groups, [])
        assert graph.nodes["spec:3"].archetype == "reviewer"


# -------------------------------------------------------------------
# TS-26-19: Reviewer pre-review auto-injection at group 0
# Requirement: 26-REQ-5.3
# -------------------------------------------------------------------


class TestReviewerPreReviewAutoInjection:
    """Verify group-0 reviewer:pre-review node injected when enabled."""

    def test_reviewer_pre_review_node_injected(self) -> None:
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(
            reviewer=True,
        )
        specs = [_spec()]
        task_groups = {"spec": [_tgd(1, "T1"), _tgd(2, "T2")]}

        graph = build_graph(
            specs,
            task_groups,
            [],
            archetypes_config=config,
        )

        # Find the auto_pre reviewer node (may have suffixed ID)
        pre_review_nodes = [n for n in graph.nodes.values() if n.archetype == "reviewer" and n.mode == "pre-review"]
        assert len(pre_review_nodes) >= 1
        pre_node = pre_review_nodes[0]
        assert any(e.source == pre_node.id and e.target == "spec:1" and e.kind == "intra_spec" for e in graph.edges)


# -------------------------------------------------------------------
# TS-26-20: Auto-post injection as siblings
# Requirement: 26-REQ-5.4
# -------------------------------------------------------------------


class TestAutoPostSiblings:
    """Verify auto_post archetypes are chained sequentially by injection_order."""

    def test_verifier_injected_after_last(self) -> None:
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(curator=False, verifier=True)
        specs = [_spec()]
        task_groups = {"spec": [_tgd(1, "T1"), _tgd(2, "T2")]}

        graph = build_graph(
            specs,
            task_groups,
            [],
            archetypes_config=config,
        )

        verifier_nodes = [n for n in graph.nodes.values() if n.archetype == "verifier"]
        assert len(verifier_nodes) >= 1

        for vn in verifier_nodes:
            assert any(e.source == "spec:2" and e.target == vn.id for e in graph.edges)

    def test_curator_verifier_chained(self) -> None:
        """With both curator and verifier enabled, they chain: last_coder -> curator -> verifier."""
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(curator=True, verifier=True)
        specs = [_spec()]
        task_groups = {"spec": [_tgd(1, "T1"), _tgd(2, "T2")]}

        graph = build_graph(specs, task_groups, [], archetypes_config=config)

        curator_nodes = [n for n in graph.nodes.values() if n.archetype == "curator"]
        verifier_nodes = [n for n in graph.nodes.values() if n.archetype == "verifier"]
        assert len(curator_nodes) == 1
        assert len(verifier_nodes) == 1

        cn = curator_nodes[0]
        vn = verifier_nodes[0]

        assert any(e.source == "spec:2" and e.target == cn.id for e in graph.edges)
        assert any(e.source == cn.id and e.target == vn.id for e in graph.edges)
        assert not any(e.source == "spec:2" and e.target == vn.id for e in graph.edges)

    def test_verifier_node_id_has_arch_suffix(self) -> None:
        """Verifier node_id must use 3-part format with sentinel group 0.

        Regression test for issue #534: the old format "{spec}:{last+1}" was
        indistinguishable from a real coder group node and caused phantom
        task-group dispatches (e.g. group 7 for a 6-group spec).  A subsequent
        attempt used "{spec}:{last_group}:{arch}" but group_number still
        coincided with a real task group.

        The correct format is "{spec}:0:{arch}" with group_number=0 — the
        sentinel value 0 is never a real task group number.
        """
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(curator=False, verifier=True)
        specs = [_spec()]
        # 6 real task groups (mirrors the 08_parking_operator_adaptor scenario)
        task_groups = {"spec": [_tgd(i, f"T{i}") for i in range(1, 7)]}

        graph = build_graph(specs, task_groups, [], archetypes_config=config)

        verifier_nodes = [n for n in graph.nodes.values() if n.archetype == "verifier"]
        assert len(verifier_nodes) == 1
        vn = verifier_nodes[0]

        # AC-1: node_id must end with ":verifier" (3-part format, not 2-part)
        parts = vn.id.split(":")
        assert len(parts) == 3, f"Expected 3-part node_id, got: {vn.id!r}"
        assert parts[2] == "verifier"

        # AC-1: node_id must use sentinel "0", not any real group number
        assert parts[1] == "0", f"Verifier node_id must embed sentinel '0', got: {vn.id!r}"

        # AC-1 (key assertion): group_number must NOT be in the set of real task groups
        real_group_numbers = {tgd.number for tgd in task_groups["spec"]}
        assert vn.group_number not in real_group_numbers, (
            f"Verifier group_number={vn.group_number} coincides with real task group; real groups: {real_group_numbers}"
        )
        assert vn.group_number == 0, f"Verifier group_number must be sentinel 0, got {vn.group_number}"

        # AC-4: no coder node with group_number beyond the last real group
        coder_nodes = [n for n in graph.nodes.values() if n.archetype == "coder"]
        assert all(n.group_number in real_group_numbers for n in coder_nodes), (
            "Coder node has group_number beyond the real task groups"
        )

        # AC-4: exactly 6 coder nodes for 6 real groups
        assert len(coder_nodes) == 6

        # Verifier has edge from last coder group (edge still wired to last real group)
        assert any(e.source == "spec:6" and e.target == vn.id for e in graph.edges)

    def test_verifier_not_in_coder_group_count(self) -> None:
        """Verifier must not inflate the coder task count.

        AC-4: format_plan_summary should see coder_count == len(real groups).
        """
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(verifier=True)
        specs = [_spec()]
        n_real = 6
        task_groups = {"spec": [_tgd(i, f"T{i}") for i in range(1, n_real + 1)]}

        graph = build_graph(specs, task_groups, [], archetypes_config=config)

        coder_nodes = [n for n in graph.nodes.values() if n.spec_name == "spec" and n.archetype == "coder"]
        assert len(coder_nodes) == n_real, f"Expected {n_real} coder nodes, got {len(coder_nodes)}"


# -------------------------------------------------------------------
# TS-26-21: Archetype assignment logged at INFO
# Requirement: 26-REQ-5.5
# -------------------------------------------------------------------


class TestAssignmentLogged:
    """Verify archetype assignments logged at INFO level."""

    def test_assignment_logged(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(reviewer=True)
        specs = [_spec()]
        task_groups = {"spec": [_tgd(1, "T1")]}

        with caplog.at_level(logging.INFO, logger="agentfox.graph.builder"):
            build_graph(
                specs,
                task_groups,
                [],
                archetypes_config=config,
            )

        assert any("archetype" in r.message.lower() for r in caplog.records if r.name == "agentfox.graph.builder")


# -------------------------------------------------------------------
# TS-26-E5: Coder instances clamped to 1
# Requirement: 26-REQ-4.E1
# -------------------------------------------------------------------


class TestCoderInstancesClamped:
    """Verify instances > 1 for coder is clamped to 1."""

    def test_coder_clamped(self, caplog: pytest.LogCaptureFixture) -> None:
        from agentfox.engine.sdk_params import clamp_instances

        with caplog.at_level(logging.WARNING):
            result = clamp_instances("coder", 3)
        assert result == 1
        assert any("clamped" in r.message.lower() or "coder" in r.message.lower() for r in caplog.records)


# -------------------------------------------------------------------
# TS-26-E6: Instances > 5 clamped
# Requirement: 26-REQ-4.E2
# -------------------------------------------------------------------


class TestInstancesOver5Clamped:
    """Verify instances > 5 is clamped to 5."""

    def test_instances_clamped_in_config(self) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig(reviewer=10)
        assert cfg.reviewer == 5

    def test_instances_clamped_at_runner_level(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        from agentfox.engine.sdk_params import clamp_instances

        with caplog.at_level(logging.WARNING):
            result = clamp_instances("reviewer", 10)
        assert result == 5


# -------------------------------------------------------------------
# TS-26-E8: Unknown archetype in tasks.md tag
# Requirement: 26-REQ-5.E2
# -------------------------------------------------------------------


class TestUnknownTagDefaultsCoder:
    """Verify v1.2 parser always returns archetype=None (no tag parsing)."""

    def test_v12_archetype_always_none(
        self,
        tmp_path: pytest.TempPathFactory,
    ) -> None:
        from agentfox.spec.parser import parse_tasks

        spec_dir = tmp_path / "03_spec"  # type: ignore[operator]
        TestArchetypeTagExtraction._write_spec(
            spec_dir,
            [
                {
                    "id": 3,
                    "kind": "standard",
                    "title": "Task",
                    "subtasks": [
                        {
                            "id": "3.1",
                            "title": "Sub",
                            "state": "pending",
                            "details": [],
                            "test_spec_refs": [],
                            "requirement_refs": [],
                            "optional": False,
                        }
                    ],
                    "verification": {"id": "", "checks": []},
                }
            ],
        )

        groups = parse_tasks(spec_dir)
        assert len(groups) == 1
        assert groups[0].archetype is None


# -------------------------------------------------------------------
# TS-26-P7: Auto-Injection Graph Structure (Property)
# Property 7: Auto-injected nodes have correct edges
# Validates: 26-REQ-5.3, 26-REQ-5.4
# -------------------------------------------------------------------


class TestPropertyInjectionStructure:
    """Auto-injected nodes have correct edges."""

    @pytest.mark.skipif(
        not HAS_HYPOTHESIS,
        reason="hypothesis not installed",
    )
    @given(n_groups=st.integers(min_value=1, max_value=5))
    @settings(max_examples=10)
    def test_prop_injection_structure(
        self,
        n_groups: int,
    ) -> None:
        from agentfox.core.config import ArchetypesConfig
        from agentfox.graph.builder import build_graph

        config = ArchetypesConfig(
            reviewer=True,
            curator=True,
            verifier=True,
        )
        specs = [_spec()]
        task_groups = {"spec": [_tgd(i, f"T{i}") for i in range(1, n_groups + 1)]}

        graph = build_graph(
            specs,
            task_groups,
            [],
            archetypes_config=config,
        )

        # Reviewer pre-review node precedes group 1
        pre_review_nodes = [n for n in graph.nodes.values() if n.archetype == "reviewer" and n.mode == "pre-review"]
        assert len(pre_review_nodes) >= 1
        pre_node = pre_review_nodes[0]
        assert any(e.source == pre_node.id and e.target == "spec:1" for e in graph.edges)

        # Auto_post nodes are chained sequentially: last_coder -> curator -> verifier
        curator_nodes = [n for n in graph.nodes.values() if n.archetype == "curator"]
        verifier_nodes = [n for n in graph.nodes.values() if n.archetype == "verifier"]
        last_coder_id = f"spec:{n_groups}"

        if curator_nodes and verifier_nodes:
            curator_node = curator_nodes[0]
            verifier_node = verifier_nodes[0]
            assert any(e.source == last_coder_id and e.target == curator_node.id for e in graph.edges)
            assert any(e.source == curator_node.id and e.target == verifier_node.id for e in graph.edges)


# -------------------------------------------------------------------
# TS-26-P8: Instance Clamping (Property)
# Property 8: Instance counts clamped to valid ranges
# Validates: 26-REQ-4.E1, 26-REQ-4.E2
# -------------------------------------------------------------------


class TestPropertyInstanceClamping:
    """Instance counts are clamped to valid ranges."""

    @pytest.mark.skipif(
        not HAS_HYPOTHESIS,
        reason="hypothesis not installed",
    )
    @given(instances=st.integers(min_value=0, max_value=20))
    @settings(max_examples=20)
    def test_prop_config_clamping(self, instances: int) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig(reviewer=instances)
        assert 1 <= cfg.reviewer <= 5

    @pytest.mark.skipif(
        not HAS_HYPOTHESIS,
        reason="hypothesis not installed",
    )
    @given(
        archetype=st.sampled_from(["coder", "reviewer", "curator", "verifier"]),
        instances=st.integers(min_value=0, max_value=20),
    )
    @settings(max_examples=30)
    def test_prop_runner_clamping(self, archetype: str, instances: int) -> None:
        from agentfox.engine.sdk_params import clamp_instances

        result = clamp_instances(archetype, instances)
        if archetype in ("coder", "curator", "verifier"):
            # Coder and verifier are always single-instance
            assert result == 1
        elif instances > 5:
            assert result == 5
        elif instances < 1:
            assert result == 1
        else:
            assert result == instances


# -------------------------------------------------------------------
# TS-26-P14: Backward Compatibility (Property)
# Property 14: Legacy data defaults correctly
# Validates: 26-REQ-4.3, 26-REQ-6.E1
# -------------------------------------------------------------------


class TestPropertyBackwardCompat:
    """Plan nodes default to coder/1."""

    def test_prop_legacy_nodes_default(self) -> None:
        from agentfox.graph.persistence import load_plan, save_plan
        from agentfox.graph.types import Node, NodeStatus, TaskGraph

        # Save a plan with default archetype values
        graph = TaskGraph(
            nodes={
                "s:1": Node(
                    id="s:1",
                    spec_name="s",
                    group_number=1,
                    title="Legacy task",
                    optional=False,
                    status=NodeStatus.PENDING,
                ),
                "s:2": Node(
                    id="s:2",
                    spec_name="s",
                    group_number=2,
                    title="Another",
                    optional=True,
                    status=NodeStatus.COMPLETED,
                ),
            },
            edges=[],
            order=["s:1", "s:2"],
        )
        conn = duckdb.connect(":memory:")
        run_migrations(conn)
        save_plan(graph, conn)

        loaded = load_plan(conn)
        assert loaded is not None
        for node in loaded.nodes.values():
            assert node.archetype == "coder"
            assert node.instances == 1
        conn.close()
