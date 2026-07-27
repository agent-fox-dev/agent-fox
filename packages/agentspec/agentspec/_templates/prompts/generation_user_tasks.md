## Additional Instructions

### Titles
Every task group and subtask MUST have a non-empty `title`. Empty titles fail validation.

### Task group structure
- The first task group (id=1) MUST have `kind: "tests"` — writes spec tests before implementation.
- The last task group MUST have `kind: "wiring_verification"` — verifies end-to-end integration.
- Groups in between use `kind: "standard"` or `kind: "checkpoint"` (for intermediate verification gates).
- Exactly one wiring_verification group, always last.

### Task Group Splitting Rules

These rules apply to all task group kinds (tests, standard, checkpoint). When any of the following thresholds is exceeded, the group MUST be split into smaller groups.

**Rule 1 — test_spec_refs ceiling:** If the total count of `test_spec_refs` across all subtasks in a proposed task group exceeds 15, split the group into smaller groups each containing 15 or fewer `test_spec_refs`. This rule applies to all task group kinds (tests, standard, checkpoint), not only `kind: tests` groups.

**Rule 2 — subtask count ceiling:** If a task group would exceed 6 subtasks (excluding the verification subtask), split it into smaller groups each containing 6 or fewer non-verification subtasks. This applies to all task group kinds (tests, standard, checkpoint).

**Rule 3 — complexity weighting:** A subtask is complex if it involves multiple file changes, cross-module coordination, or intricate assertion patterns. If a proposed group contains 4 or more complex subtasks, split it even if the numeric thresholds (15 test_spec_refs, 6 subtasks) have not been reached. This rule applies regardless of whether other splitting thresholds are exceeded.

**Splitting strategy — group by requirement:** When splitting, group subtasks by the requirement they trace to. Each resulting group should cover a distinct set of requirements. If all subtasks trace to a single requirement but still exceed a threshold, further subdivide that requirement's subtasks across multiple groups.

**Kind preservation:** When a `kind: tests` group is split, all resulting groups retain `kind: tests`. The first split group retains group_id 1, and subsequent groups receive sequential IDs (2, 3, …).

**ID renumbering:** Non-test groups shift their IDs to follow the last test group. Subtask IDs within each group use the `{group_id}.{N}` format.

### Subtask IDs and verification
- Subtask IDs use format `{group_id}.{N}` (e.g. `2.1`, `2.2`). Sequential within each group. Target 3-6 subtasks per group.
- Every group MUST have exactly one verification subtask with ID `{group_id}.V` (e.g. `2.V`). The verification subtask MUST have a non-empty `checks` array with concrete criteria. Use the project's actual test runner and linter — for example:
  - "Spec tests for this group pass: <project test command matching the language>"
  - "All existing tests still pass: <project test-all command>"
  - "No linter warnings introduced: <project lint command>"
  - "Requirements 05-REQ-1.1, 05-REQ-1.2 acceptance criteria met"

  Examples by language: Go → `go test ./... -count=1`, `go vet ./...`; Python → `pytest -q`, `ruff check`; TypeScript → `npm test`, `eslint .`. Derive the correct commands from the PRD's tech stack — do NOT default to Python.

### Test commands
The `test_commands` object must use the project's actual tooling as specified in
the PRD's Tech Stack section. For cross-spec dependencies, match the conventions
of the upstream spec's `test_commands` if available in prior artifacts.

Do NOT use default Python commands (`pytest`, `ruff`) unless the project is
actually a Python project. Examples: Go → `"spec_tests": "go test ./internal/integration/... -count=1 -v"`, `"linter": "go vet ./..."`; TypeScript → `"spec_tests": "npm test"`, `"linter": "eslint ."`.

### Dependencies
The `dependencies` array declares cross-spec dependencies only. Set `depends_on_spec` to the spec_id of the other spec. Intra-spec ordering is implicit from task group IDs — do not add self-referencing dependencies. Leave `dependencies` empty if the spec has no cross-spec dependencies.

### Traceability
The `traceability` array links requirements to test specs and tasks. One entry per (requirement_id, test_spec_id) pair. Set `test_path` to null (filled in at implementation time).

Reference both requirement IDs and test IDs from the previously generated artifacts in subtask `requirement_refs` and `test_spec_refs` fields.

### Smoke test authoring
Every smoke test in test_spec.json (TS-{spec_id}-SMOKE-*) must have a corresponding authoring subtask in an earlier test-writing group. Do not defer smoke test creation to the wiring_verification group — that group only *runs* smoke tests, it does not write them.

### Wiring verification (last group)
The final wiring_verification group must include subtasks that cover:
1. Trace execution paths — verify each path's entry point calls the next function in the chain, no stubs remain.
2. Verify return value propagation — confirm callers receive and use return values.
3. Run smoke tests — all SMOKE tests pass with real components.
4. Stub/dead-code audit — search for language-appropriate stub markers. Examples: Go → `panic("not implemented")`, bare `return nil` in non-trivial paths, `// TODO`; Python → `return None`, `pass` in non-abstract methods, `raise NotImplementedError`; TypeScript → `throw new Error("not implemented")`. Use the markers appropriate to the PRD's language.
5. Cross-spec entry point verification — if paths start in another spec, confirm they are called from production code.
6. Call-site verification — for every function defined by an acceptance criterion that has a `return_contract`, include a subtask that verifies the function is called from production code (not just defined and tested). A function can be fully specified, implemented, and tested yet still be dead code if nothing wires it into the application entry point.

**Structural requirements (validated by `spec validate`):**
- At least one subtask in the wiring_verification group MUST have non-empty `test_spec_refs`.
- At least one `test_spec_ref` MUST reference a smoke test (pattern `TS-{spec_id}-SMOKE-*`).
- At least one subtask title, detail, or verification check MUST reference a stub/dead-code audit.

### Example: good vs bad subtask
**Good:** `title: "Implement org creation endpoint"`, `details: ["Add POST /orgs route handler", "Validate name uniqueness against DB", "Return 201 with created org JSON"]`, `test_spec_refs: ["TS-05-1", "TS-05-E1"]`, `requirement_refs: ["05-REQ-1.1", "05-REQ-1.E1"]`

**Bad:** `title: "Implement feature"`, `details: ["Write the code"]`, `test_spec_refs: []`, `requirement_refs: []`
