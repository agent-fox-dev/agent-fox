# Erratum: afspec.SubtaskState Enum Values

**Spec:** 135 (v1.2 Skill Template and Validation Migration)
**Date:** 2026-06-15

## Divergence

The design document and tasks.md use `completed` and `not_started` as
SubtaskState values. The test specification (TS-135-10) also references
`not_started`, `in_progress`, `completed`, and `queued` as task states.

The actual `afspec.SubtaskState` enum (Pydantic v2 model) has different
values:

```python
class SubtaskState(str, Enum):
    PENDING = "pending"
    QUEUED = "queued"
    IN_PROGRESS = "in_progress"
    DONE = "done"
    PENDING_REEVALUATION = "pending_reevaluation"
    DROPPED = "dropped"
```

**Key differences:**
- `completed` → `done` (SubtaskState.DONE)
- `not_started` → `pending` (SubtaskState.PENDING)
- Python enum names are uppercase (e.g., `SubtaskState.DONE`)
- JSON serialization uses lowercase values (e.g., `"done"`)

Similarly, `TaskGroupKind` values differ from spec assumptions:

```python
class TaskGroupKind(str, Enum):
    TESTS = "tests"        # not "test"
    STANDARD = "standard"  # not "implementation"
    CHECKPOINT = "checkpoint"
    WIRING_VERIFICATION = "wiring_verification"
```

## Impact on Implementation

- `_is_spec_implemented()` compares subtask states against
  `afspec.SubtaskState.DONE` (not `.completed`)
- Integration test helpers use `"done"` and `"pending"` in JSON
  serialization (not `"completed"` / `"not_started"`)
- `TaskGroupKind` uses `"tests"` and `"wiring_verification"` (afspec
  requires the last task group to have kind `wiring_verification`)
