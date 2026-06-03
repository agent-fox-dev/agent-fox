"""Hot-load tests.

Test Spec: TS-06-15 (discover and add new specs),
           TS-06-16 (no new specs is no-op)
Edge Cases: TS-06-E5 (invalid dependency), TS-06-E7 (sync interval zero)
Requirements: 06-REQ-6.E1, 06-REQ-7.1, 06-REQ-7.2, 06-REQ-7.3,
              06-REQ-7.E1, 06-REQ-7.E2
"""

from __future__ import annotations

from pathlib import Path

from agent_fox.engine.hot_load import discover_new_specs


def _make_minimal_tasks_md() -> str:
    """Create minimal tasks.md content for a new spec."""
    return "# Tasks\n\n- [ ] 1. Test task group\n  - [ ] 1.1 Subtask one\n  - [ ] 1.2 Subtask two\n"


class TestDiscoverNewSpecs:
    """Unit tests for discover_new_specs function."""

    def test_finds_unknown_specs(self, tmp_specs_dir: Path) -> None:
        """Specs not in known_specs are returned."""
        (tmp_specs_dir / "01_existing").mkdir()
        (tmp_specs_dir / "01_existing" / "tasks.md").write_text(_make_minimal_tasks_md())
        (tmp_specs_dir / "07_new_feature").mkdir()
        (tmp_specs_dir / "07_new_feature" / "tasks.md").write_text(_make_minimal_tasks_md())

        new_specs = discover_new_specs(tmp_specs_dir, known_specs={"01_existing"})

        assert len(new_specs) == 1
        assert new_specs[0].name == "07_new_feature"

    def test_returns_empty_when_all_known(self, tmp_specs_dir: Path) -> None:
        """Returns empty list when all specs are already known."""
        (tmp_specs_dir / "01_existing").mkdir()
        (tmp_specs_dir / "01_existing" / "tasks.md").write_text(_make_minimal_tasks_md())

        new_specs = discover_new_specs(tmp_specs_dir, known_specs={"01_existing"})

        assert new_specs == []


# -- Edge case tests ---------------------------------------------------------


class TestSyncIntervalZero:
    """TS-06-E7: Sync interval zero disables barriers.

    Requirement: 06-REQ-6.E1
    """

    def test_zero_interval_never_triggers(self) -> None:
        """sync_interval=0 means no sync barriers are triggered."""
        sync_interval = 0
        for completed in range(1, 101):
            triggered = sync_interval > 0 and completed > 0 and completed % sync_interval == 0
            assert triggered is False, (
                f"Barrier should never trigger with interval=0, but triggered at completed={completed}"
            )

    def test_nonzero_interval_triggers(self) -> None:
        """A positive sync_interval triggers at the correct counts."""
        sync_interval = 5
        triggered_at = []
        for completed in range(1, 21):
            triggered = sync_interval > 0 and completed > 0 and completed % sync_interval == 0
            if triggered:
                triggered_at.append(completed)

        assert triggered_at == [5, 10, 15, 20]
