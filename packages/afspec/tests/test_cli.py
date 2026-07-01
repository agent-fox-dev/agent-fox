"""Tests for the afspec CLI."""

from __future__ import annotations

from pathlib import Path

from afspec import Subtask, SubtaskState, load_spec, save
from afspec.cli import main
from afspec.constructors import create_spec
from afspec.models import TaskGroup, Tasks


def _make_spec_dir(tmp_path: Path) -> Path:
    """Create a minimal spec on disk with one in-progress subtask."""
    spec = create_spec(spec_id="01", spec_name="test_cli")
    spec = spec.model_copy(
        update={
            "tasks": Tasks(
                spec_id="01",
                spec_name="test_cli",
                task_groups=[
                    TaskGroup(
                        id=1,
                        title="Group 1",
                        subtasks=[
                            Subtask(id="1.1", title="First", state=SubtaskState.IN_PROGRESS),
                            Subtask(id="1.2", title="Second", state=SubtaskState.PENDING),
                        ],
                    ),
                ],
            ),
        }
    )
    spec_dir = tmp_path / "test_cli"
    spec_dir.mkdir()
    save(spec, spec_dir)
    return spec_dir


class TestUpdateSubtaskCommand:
    def test_valid_transition(self, tmp_path: Path) -> None:
        spec_dir = _make_spec_dir(tmp_path)
        rc = main(["update-subtask", str(spec_dir), "1.1", "done"])
        assert rc == 0
        reloaded = load_spec(spec_dir)
        assert reloaded.tasks.task_groups[0].subtasks[0].state == SubtaskState.DONE

    def test_illegal_transition(self, tmp_path: Path) -> None:
        spec_dir = _make_spec_dir(tmp_path)
        rc = main(["update-subtask", str(spec_dir), "1.2", "done"])
        assert rc == 1

    def test_subtask_not_found(self, tmp_path: Path) -> None:
        spec_dir = _make_spec_dir(tmp_path)
        rc = main(["update-subtask", str(spec_dir), "9.9", "done"])
        assert rc == 1

    def test_invalid_spec_dir(self, tmp_path: Path) -> None:
        rc = main(["update-subtask", str(tmp_path / "nonexistent"), "1.1", "done"])
        assert rc == 1

    def test_no_command(self) -> None:
        rc = main([])
        assert rc == 1

    def test_other_subtask_unchanged(self, tmp_path: Path) -> None:
        spec_dir = _make_spec_dir(tmp_path)
        main(["update-subtask", str(spec_dir), "1.1", "done"])
        reloaded = load_spec(spec_dir)
        assert reloaded.tasks.task_groups[0].subtasks[1].state == SubtaskState.PENDING
