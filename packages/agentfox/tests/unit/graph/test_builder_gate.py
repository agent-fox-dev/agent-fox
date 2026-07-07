"""Tests for checkpoint gate archetype assignment (issue #680)."""

from __future__ import annotations

from agentfox.archetypes import ARCHETYPE_REGISTRY, get_archetype
from agentfox.graph.builder import _create_nodes_and_intra_edges
from agentfox.spec.types import TaskGroupDef


def _make_group(number, title, kind=None, archetype=None):
    return TaskGroupDef(
        number=number, title=title, optional=False, completed=False,
        subtasks=(), body=f"Body of task {number}", kind=kind, archetype=archetype,
    )


class _FakeSpec:
    name = "test_spec"
    title = "Test Spec"


class TestCheckpointGateArchetype:
    def test_checkpoint_kind_assigns_gate_archetype(self):
        groups = [_make_group(1, "Tests", kind="tests"), _make_group(2, "Checkpoint", kind="checkpoint")]
        nodes, _ = _create_nodes_and_intra_edges([_FakeSpec()], {"test_spec": groups})
        assert nodes["test_spec:2"].archetype == "gate"

    def test_standard_kind_keeps_coder_archetype(self):
        nodes, _ = _create_nodes_and_intra_edges([_FakeSpec()], {"test_spec": [_make_group(1, "Impl", kind="standard")]})
        assert nodes["test_spec:1"].archetype == "coder"

    def test_none_kind_keeps_coder_archetype(self):
        nodes, _ = _create_nodes_and_intra_edges([_FakeSpec()], {"test_spec": [_make_group(1, "Impl")]})
        assert nodes["test_spec:1"].archetype == "coder"

    def test_wiring_verification_keeps_coder(self):
        nodes, _ = _create_nodes_and_intra_edges(
            [_FakeSpec()], {"test_spec": [_make_group(1, "Wire", kind="wiring_verification")]}
        )
        assert nodes["test_spec:1"].archetype == "coder"

    def test_explicit_archetype_overrides_checkpoint(self):
        nodes, _ = _create_nodes_and_intra_edges(
            [_FakeSpec()], {"test_spec": [_make_group(1, "CP", kind="checkpoint", archetype="coder")]}
        )
        assert nodes["test_spec:1"].archetype == "coder"


class TestGateArchetypeRegistry:
    def test_gate_registered(self):
        assert "gate" in ARCHETYPE_REGISTRY

    def test_gate_max_turns_30(self):
        assert get_archetype("gate").default_max_turns == 30

    def test_gate_thinking_disabled(self):
        assert get_archetype("gate").default_thinking_mode == "disabled"

    def test_gate_not_auto_injected(self):
        assert get_archetype("gate").injection is None

    def test_gate_task_assignable(self):
        assert get_archetype("gate").task_assignable is True
