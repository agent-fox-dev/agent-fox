---
name: generation_user_tasks
description: Additional instructions for tasks artifact generation
---
Generate the implementation tasks artifact. Include task groups, subtasks, dependencies, verification steps, and traceability matrix.

## Task Group Ordering Rules

The following ordering rules are schema-enforced and must be respected exactly:

1. **The first task group (id: 1) must have `kind: "tests"`** — the first group always writes failing spec tests (TDD). No exceptions.
2. **The final task group must have `kind: "wiring_verification"`** — the last group always verifies integration wiring. No exceptions.
3. **No more than one `"wiring_verification"` group per spec** — only the final group may carry this kind.

Allowed `kind` values: `"tests"`, `"standard"`, `"checkpoint"`, `"wiring_verification"`.

## Subtask and Verification IDs

- Subtask ID format: `{group_id}.{N}` — e.g. `2.1`, `2.2`, `3.1`
- Verification subtask ID format: `{group_id}.V` — e.g. `1.V`, `2.V`, `3.V`
- Every task group must have exactly one verification subtask with ID `{group_id}.V`
- Target 3–6 regular subtasks per group (excluding the verification subtask)

## Example

The fragment below shows a correctly structured tasks artifact excerpt for a recipe-manager system. Use a different domain for your actual output — this example is for structural reference only.

```json
{
  "spec_id": "07",
  "spec_name": "recipe-manager",
  "schema_version": "1.0",
  "task_groups": [
    {
      "id": 1,
      "kind": "tests",
      "title": "Write failing tests for recipe creation",
      "subtasks": [
        {
          "id": "1.1",
          "title": "Write test for POST /recipes success case",
          "details": ["Add test asserting HTTP 201 and body shape"],
          "requirement_refs": ["07-REQ-1.1"],
          "test_spec_refs": ["TS-07-1"]
        },
        {
          "id": "1.2",
          "title": "Write test for POST /recipes missing-name validation",
          "details": ["Add test asserting HTTP 400 with descriptive error"],
          "requirement_refs": ["07-REQ-1.2"],
          "test_spec_refs": ["TS-07-2"]
        },
        {
          "id": "1.V",
          "title": "Verify all recipe tests exist and fail before implementation",
          "details": [],
          "requirement_refs": [],
          "test_spec_refs": [],
          "checks": ["All new tests are present and fail: go test ./... -count=1"]
        }
      ]
    },
    {
      "id": 2,
      "kind": "standard",
      "title": "Implement recipe creation endpoint",
      "subtasks": [
        {
          "id": "2.1",
          "title": "Add POST /recipes route handler",
          "details": ["Parse and validate request body", "Persist recipe to database", "Return 201 with created record"],
          "requirement_refs": ["07-REQ-1.1", "07-REQ-1.2"],
          "test_spec_refs": ["TS-07-1", "TS-07-2"]
        },
        {
          "id": "2.V",
          "title": "Verify recipe creation tests pass",
          "details": [],
          "requirement_refs": [],
          "test_spec_refs": [],
          "checks": ["Tests TS-07-1, TS-07-2 pass: go test ./... -count=1", "No linter warnings: go vet ./..."]
        }
      ]
    },
    {
      "id": 3,
      "kind": "wiring_verification",
      "title": "Verify end-to-end recipe pipeline wiring",
      "subtasks": [
        {
          "id": "3.1",
          "title": "Trace POST /recipes path from router to database layer",
          "details": ["Confirm handler calls repository layer, no stubs remain"],
          "requirement_refs": ["07-REQ-1.1"],
          "test_spec_refs": ["TS-07-SMOKE-1"]
        },
        {
          "id": "3.V",
          "title": "Confirm all smoke tests pass with real components",
          "details": [],
          "requirement_refs": [],
          "test_spec_refs": ["TS-07-SMOKE-1"],
          "checks": ["Smoke test TS-07-SMOKE-1 passes: go test ./... -run SMOKE -count=1"]
        }
      ]
    }
  ]
}
```
