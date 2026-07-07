"""Tests for TaskGroupDef kind propagation (issue #680)."""

from __future__ import annotations

from unittest.mock import MagicMock

from afspec.models import TaskGroupKind
from agentfox.spec.parser import _map_task_group


def _make_afspec_group(group_id=1, title="Test", kind=TaskGroupKind.STANDARD):
    group = MagicMock()
    group.id = group_id
    group.title = title
    group.kind = kind
    group.subtasks = []
    return group


class TestKindPropagation:
    def test_standard(self):
        assert _map_task_group(_make_afspec_group(kind=TaskGroupKind.STANDARD)).kind == "standard"

    def test_checkpoint(self):
        assert _map_task_group(_make_afspec_group(kind=TaskGroupKind.CHECKPOINT)).kind == "checkpoint"

    def test_wiring_verification(self):
        assert _map_task_group(_make_afspec_group(kind=TaskGroupKind.WIRING_VERIFICATION)).kind == "wiring_verification"

    def test_tests(self):
        assert _map_task_group(_make_afspec_group(kind=TaskGroupKind.TESTS)).kind == "tests"

    def test_none(self):
        g = _make_afspec_group()
        g.kind = None
        assert _map_task_group(g).kind is None
