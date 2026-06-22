## Additional Instructions

### Titles
Every task group and subtask MUST have a non-empty `title`. Empty titles fail validation.

### Task group structure
- The first task group (id=1) MUST have `kind: "tests"` — writes spec tests before implementation.
- The last task group MUST have `kind: "wiring_verification"` — verifies end-to-end integration.
- Groups in between use `kind: "standard"` or `kind: "checkpoint"` (for intermediate verification gates).
- Exactly one wiring_verification group, always last.

### Subtask IDs and verification
- Subtask IDs use format `{group_id}.{N}` (e.g. `2.1`, `2.2`). Sequential within each group. Target 3-6 subtasks per group.
- Every group MUST have exactly one verification subtask with ID `{group_id}.V` (e.g. `2.V`). The verification subtask MUST have a non-empty `checks` array with concrete criteria, for example:
  - "Spec tests for this group pass: pytest -q tests/..."
  - "All existing tests still pass: pytest -q"
  - "No linter warnings introduced: ruff check"
  - "Requirements 05-REQ-1.1, 05-REQ-1.2 acceptance criteria met"

### Dependencies
The `dependencies` array declares cross-spec dependencies only. Set `depends_on_spec` to the spec_id of the other spec. Intra-spec ordering is implicit from task group IDs — do not add self-referencing dependencies. Leave `dependencies` empty if the spec has no cross-spec dependencies.

### Traceability
The `traceability` array links requirements to test specs and tasks. One entry per (requirement_id, test_spec_id) pair. Set `test_path` to null (filled in at implementation time).

Reference both requirement IDs and test IDs from the previously generated artifacts in subtask `requirement_refs` and `test_spec_refs` fields.

### Wiring verification (last group)
The final wiring_verification group must include subtasks that cover:
1. Trace execution paths — verify each path's entry point calls the next function in the chain, no stubs remain.
2. Verify return value propagation — confirm callers receive and use return values.
3. Run smoke tests — all SMOKE tests pass with real components.
4. Stub/dead-code audit — search for return None, pass in non-abstract methods, TODO, NotImplementedError.
5. Cross-spec entry point verification — if paths start in another spec, confirm they are called from production code.
