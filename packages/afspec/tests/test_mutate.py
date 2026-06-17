"""Tests for programmatic construction API / mutation methods."""

from __future__ import annotations

from pathlib import Path

import pytest

from afspec import (
    Requirement,
    Requirements,
    Spec,
    Subtask,
    SubtaskState,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
    create_spec,
    event_driven_criterion,
    load_spec,
    save,
)
from afspec.mutate import (
    add_criterion,
    add_requirement,
    add_subtask,
    add_task_group,
    add_test_case,
    add_traceability_entry,
    get_requirement,
    next_requirement_id,
    next_test_case_id,
    remove_requirement,
    set_glossary_entry,
)


def _make_user_story() -> UserStory:
    return UserStory(role="dev", goal="build things", benefit="value")


def _make_requirement(id: str, title: str = "Test") -> Requirement:
    return Requirement(id=id, title=title, user_story=_make_user_story())


# ---------------------------------------------------------------------------
# TS-01-46: Add methods append and reject duplicates (01-REQ-11.1)
# ---------------------------------------------------------------------------


def test_add_requirement() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    req1 = _make_requirement("01-REQ-1", "First")
    updated = add_requirement(req, req1)
    assert len(updated.requirements) == 1

    req2 = _make_requirement("01-REQ-2", "Second")
    updated2 = add_requirement(updated, req2)
    assert len(updated2.requirements) == 2


def test_add_duplicate() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    req1 = _make_requirement("01-REQ-1", "First")
    updated = add_requirement(req, req1)

    dup = _make_requirement("01-REQ-1", "Duplicate")
    with pytest.raises(ValueError, match="01-REQ-1"):
        add_requirement(updated, dup)
    # Collection unchanged
    assert len(updated.requirements) == 1


# ---------------------------------------------------------------------------
# TS-01-47: Get methods return value or None (01-REQ-11.2)
# ---------------------------------------------------------------------------


def test_get_requirement() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    r1 = _make_requirement("01-REQ-1", "First")
    updated = add_requirement(req, r1)

    found = get_requirement(updated, "01-REQ-1")
    assert found is not None
    assert found.id == "01-REQ-1"
    assert found.title == "First"

    missing = get_requirement(updated, "01-REQ-99")
    assert missing is None


# ---------------------------------------------------------------------------
# TS-01-48: Remove methods return True/False (01-REQ-11.3)
# ---------------------------------------------------------------------------


def test_remove_requirement() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    r1 = _make_requirement("01-REQ-1", "First")
    updated = add_requirement(req, r1)
    assert len(updated.requirements) == 1

    result, ok = remove_requirement(updated, "01-REQ-1")
    assert ok is True
    assert len(result.requirements) == 0

    result2, ok2 = remove_requirement(result, "01-REQ-1")
    assert ok2 is False


# ---------------------------------------------------------------------------
# TS-01-49: ID generation helpers produce sequential IDs (01-REQ-11.5)
# ---------------------------------------------------------------------------


def test_next_ids() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    assert next_requirement_id(req) == "01-REQ-1"

    r1 = _make_requirement("01-REQ-1", "First")
    updated = add_requirement(req, r1)
    assert next_requirement_id(updated) == "01-REQ-2"

    ts = TestSpec(spec_id="01", spec_name="test")
    assert next_test_case_id(ts) == "TS-01-1"


# ---------------------------------------------------------------------------
# TS-01-E21: Add with duplicate ID raises error, collection unchanged (01-REQ-11.E1)
# ---------------------------------------------------------------------------


def test_add_requirement_duplicate_unchanged() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    orig = _make_requirement("01-REQ-1", "Original")
    updated = add_requirement(req, orig)

    dup = _make_requirement("01-REQ-1", "Duplicate")
    with pytest.raises(ValueError, match="01-REQ-1"):
        add_requirement(updated, dup)
    assert len(updated.requirements) == 1
    found = get_requirement(updated, "01-REQ-1")
    assert found is not None
    assert found.title == "Original"


# ---------------------------------------------------------------------------
# TS-01-E22: Get non-existent returns None (01-REQ-11.E2)
# ---------------------------------------------------------------------------


def test_get_requirement_missing() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    found = get_requirement(req, "01-REQ-99")
    assert found is None


# ---------------------------------------------------------------------------
# TS-01-E23: next_requirement_id on empty returns first ID (01-REQ-11.E3)
# ---------------------------------------------------------------------------


def test_next_requirement_id_empty() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    assert next_requirement_id(req) == "01-REQ-1"

    req2 = Requirements(spec_id="05", spec_name="feat", introduction="intro")
    assert next_requirement_id(req2) == "05-REQ-1"


# ---------------------------------------------------------------------------
# TS-01-E25: set_glossary_entry overwrites existing entry (01-REQ-11.4)
# ---------------------------------------------------------------------------


def test_set_glossary_entry() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    updated = set_glossary_entry(req, "Spec", "A package")
    assert updated.glossary["Spec"] == "A package"

    updated2 = set_glossary_entry(updated, "Spec", "Updated definition")
    assert updated2.glossary["Spec"] == "Updated definition"
    assert len(updated2.glossary) == 1


# ---------------------------------------------------------------------------
# TS-01-E34: add_traceability_entry with duplicate pair (01-REQ-11.E4)
# ---------------------------------------------------------------------------


def test_add_traceability_duplicate() -> None:
    tasks = Tasks(spec_id="01", spec_name="test")
    entry1 = TraceabilityEntry(
        requirement_id="01-REQ-1.1",
        test_spec_id="TS-01-1",
        task_id="1.1",
        test_path=None,
    )
    updated = add_traceability_entry(tasks, entry1)
    assert len(updated.traceability) == 1

    entry2 = TraceabilityEntry(
        requirement_id="01-REQ-1.1",
        test_spec_id="TS-01-1",
        task_id="2.1",
        test_path=None,
    )
    with pytest.raises(ValueError, match="duplicate"):
        add_traceability_entry(updated, entry2)
    assert len(updated.traceability) == 1


# ---------------------------------------------------------------------------
# TS-01-46 cont: add_task_group and add_subtask (01-REQ-11.6)
# ---------------------------------------------------------------------------


def test_add_task_group() -> None:
    tasks = Tasks(spec_id="01", spec_name="test")
    group = TaskGroup(
        id=1,
        kind=TaskGroupKind.TESTS,
        title="Write tests",
        verification=VerificationSubtask(id="1.V", checks=["pass"]),
    )
    updated = add_task_group(tasks, group)
    assert len(updated.task_groups) == 1


def test_add_traceability() -> None:
    tasks = Tasks(spec_id="01", spec_name="test")
    entry = TraceabilityEntry(
        requirement_id="01-REQ-1.1",
        test_spec_id="TS-01-1",
        task_id="1.1",
        test_path=None,
    )
    updated = add_traceability_entry(tasks, entry)
    assert len(updated.traceability) == 1


# ---------------------------------------------------------------------------
# TS-01-P12: Collection mutation idempotency (Property 12)
# ---------------------------------------------------------------------------


def test_property_mutation_idempotency() -> None:
    req = Requirements(spec_id="01", spec_name="test", introduction="intro")
    items = [_make_requirement(f"01-REQ-{i}", f"Req {i}") for i in range(1, 6)]
    current = req
    for item in items:
        current = add_requirement(current, item)
    for item in items:
        found = get_requirement(current, item.id)
        assert found is not None
        assert found == item
    assert get_requirement(current, "nonexistent") is None


# ---------------------------------------------------------------------------
# TS-01-SMOKE-8: Programmatic spec construction end-to-end (PATH-8)
# ---------------------------------------------------------------------------


def test_smoke_programmatic_construction(tmp_path: Path) -> None:
    spec = create_spec("99", "programmatic_test")
    assert spec.prd.frontmatter.spec_id == "99"
    assert spec.requirements.spec_id == "99"

    story = UserStory(role="developer", goal="construct specs", benefit="programmatic creation")
    r = Requirement(id="99-REQ-1", title="Test Feature", user_story=story)
    c = event_driven_criterion("99-REQ-1.1", "data arrives", "the system", "process it")
    r_with_c = add_criterion(r, c)

    updated_req = add_requirement(spec.requirements, r_with_c)
    updated_req = set_glossary_entry(updated_req, "system", "the afspec library")

    tc = TestCase(
        id="TS-99-1",
        requirement_id="99-REQ-1.1",
        kind="unit",
        description="Test the feature",
    )
    updated_ts = add_test_case(spec.test_spec, tc)

    group = TaskGroup(
        id=1,
        kind=TaskGroupKind.STANDARD,
        title="Implement feature",
        verification=VerificationSubtask(id="1.V", checks=["pass"]),
    )
    sub = Subtask(id="1.1", title="Write code", state=SubtaskState.PENDING)
    group_with_sub = add_subtask(group, sub)
    updated_tasks = add_task_group(spec.tasks, group_with_sub)

    entry = TraceabilityEntry(
        requirement_id="99-REQ-1.1",
        test_spec_id="TS-99-1",
        task_id="1.1",
        test_path=None,
    )
    updated_tasks = add_traceability_entry(updated_tasks, entry)

    # Build final spec
    final = Spec(
        prd=spec.prd,
        requirements=updated_req,
        test_spec=updated_ts,
        tasks=updated_tasks,
    )
    save(final, tmp_path)
    reloaded = load_spec(tmp_path)
    assert reloaded.requirements.spec_id == "99"
