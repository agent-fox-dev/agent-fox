Generate the implementation plan: **one flat, ordered list of tasks**. A task is the unit of dispatch — one coder session, one task. There are no task groups, no subtasks, no verification subtasks and no `traceability` array.

The complete requirements artifact and the full table of tests are above. Use their real IDs. Do not invent one.

## Task fields

- `id` — integer, sequential from 1. Array order is the default execution order.
- `kind` — `implement` or `integration`. **Exactly one `integration` task, and it is last.**
- `title` — imperative and specific.
- `criteria` — requirement or criterion IDs this task satisfies. Non-empty for `implement`; a requirement ID means all of its criteria. May be empty for `integration`.
- `tests` — non-empty. The test IDs this task must make pass.
- `steps` — non-empty, in order, concrete enough that a coder who sees only this task, its criteria and its tests needs nothing else.
- `touches` — optional. The files, packages or modules the task creates or modifies.
- `depends_on` — optional. Task IDs that must be done first; must reference lower IDs. Absent means "the previous task". Use it to let independent tasks run in parallel.
- `done_when` — optional. Extra completion checks beyond the implicit ones.
- `state` — `pending` for a freshly generated plan.
- `optional` — optional boolean, default false.

Every task carries an implicit definition of done that you do not need to write out: the listed tests exist, are executable and pass; `test_commands.all_tests` passes; `test_commands.linter` passes. The coder works test-first, so **there are no separate "write the tests" tasks** — tests are written inside the task that owns them.

## Rules that make the plan invalid

- **Every test in the test spec is listed in the `tests` of at least one task.** Nothing may be left unowned — that is the single most common way a plan ships with holes.
- **Every criterion is covered by the `criteria` of at least one `implement` task**, directly or through its requirement ID.
- Every ID in `criteria`, `tests` and `depends_on` resolves.
- The `integration` task's `tests` include **every** test of kind `smoke`.

Keep a task to at most 10 tests and 12 steps, and the spec to at most 12 tasks. Group by requirement, not by layer: a task should deliver one or two requirements end to end — model, logic, interface and tests — so that each task leaves the system in a working state.

## The integration task

The last task exists to catch wiring gaps that component-level tests cannot see. Its steps must cover, in the project's own vocabulary:

1. Trace every execution path through the real code; confirm each step calls the next and no stub remains.
2. For every criterion with a `contract`, confirm the producer's result is consumed by a caller in production code, not only by tests.
3. Search the files listed in every task's `touches` for stub markers appropriate to the language — `panic("not implemented")`, `TODO`, `NotImplementedError`, a bare `return nil` on a non-trivial path.
4. For any path whose entry point belongs to another spec, confirm that entry point is called from production code.

An execution path that is not live in production code fails this task. Errata and deferrals do not satisfy it.

## Test commands

`test_commands.all_tests` and `test_commands.linter` are required and must be the project's real commands, taken from the language and tooling stated above — never a default from another ecosystem. `spec_tests` is optional and narrows the run to this spec's tests.

## Example

```json
{
  "spec_id": "07",
  "spec_name": "recipe_manager",
  "schema_version": 2,
  "test_commands": {
    "all_tests": "go test ./... -count=1",
    "linter": "go vet ./...",
    "spec_tests": "go test ./catalog/... -count=1"
  },
  "dependencies": [
    { "spec": "06", "reason": "uses the HTTP router and the database handle it sets up" }
  ],
  "tasks": [
    {
      "id": 1,
      "kind": "implement",
      "title": "Persist and read back recipes",
      "criteria": ["07-REQ-1"],
      "tests": ["TS-07-1", "TS-07-2", "TS-07-3"],
      "steps": [
        "Write TS-07-1, TS-07-2 and TS-07-3 and confirm they fail",
        "Add the recipe row type and the catalog Insert and Get methods",
        "Wire POST /recipes to Insert; return 201 with the assigned id",
        "Reject a body with no name with 400 and an error string, writing nothing"
      ],
      "touches": ["catalog/recipe.go", "catalog/store.go", "http/handlers.go"],
      "state": "pending"
    },
    {
      "id": 2,
      "kind": "integration",
      "title": "Verify the save-and-read path end to end",
      "criteria": [],
      "tests": ["TS-07-4"],
      "steps": [
        "Write TS-07-4 against the running service and a real database and confirm it passes",
        "Trace 07-PATH-1 from the HTTP handler through the catalog to the database; confirm each step calls the next",
        "Confirm the identifier returned by Insert is consumed by the handler in production code, not only in tests",
        "Grep catalog/ and http/ for TODO and panic(\"not implemented\") and remove any that remain"
      ],
      "touches": ["http/smoke_test.go"],
      "depends_on": [1],
      "done_when": ["`go test ./... -count=1` passes against a real database"],
      "state": "pending"
    }
  ]
}
```
