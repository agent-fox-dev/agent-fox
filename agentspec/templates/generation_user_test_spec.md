---
name: generation_user_test_spec
description: Additional instructions for test_spec artifact generation
---
Generate the test specification artifact. Include test cases, property tests, edge case tests, and smoke tests with assertion pseudocode.

## 1:1 Coverage Mapping Rule

Every entry in `requirements.json` maps to exactly one corresponding test entry:

- One `test_case` per acceptance criterion (`{spec_id}-REQ-{N}.{C}`) — set `requirement_id` to the criterion ID.
- One `edge_case_test` per edge case (`{spec_id}-REQ-{N}.E{C}`) — set `requirement_id` to the edge case ID.
- One `property_test` per correctness property (`{spec_id}-PROP-{N}`) — set `property_id` to the property ID.
- One `smoke_test` per execution path (`{spec_id}-PATH-{N}`) — set `execution_path_id` to the path ID.

Do not skip entries. If a requirement has three acceptance criteria, produce three `test_case` entries.

## ID Format Rules

Use the spec_id from the PRD frontmatter as the numeric prefix:

| Test type      | Format                   | Example         |
|----------------|--------------------------|-----------------|
| test_case      | `TS-{spec_id}-{N}`       | `TS-05-3`       |
| edge_case_test | `TS-{spec_id}-E{N}`      | `TS-05-E1`      |
| property_test  | `TS-{spec_id}-P{N}`      | `TS-05-P2`      |
| smoke_test     | `TS-{spec_id}-SMOKE-{N}` | `TS-05-SMOKE-1` |

N is a sequential positive integer starting at 1, scoped within each test type.

## Assertion Pseudocode Style

Write `assertion_pseudocode` in language-agnostic pseudocode that names concrete functions, expected values, and a clear assertion. Avoid vague prose.

**Good:**
```
result = LoadSpec("05", "/tmp/specs")
assert result.spec_id == "05"
assert result.requirements[0].id == "05-REQ-1"
```

**Bad:**
```
Call the function and verify that it returns the correct result.
```

The good example names the function (`LoadSpec`), its arguments, the expected field values, and uses `assert` statements. The bad example gives no testable detail.

## Smoke Test Quality Rules

For each `smoke_test`:

- `real_components`: list the actual system components exercised end-to-end (not mocks).
- `mockable`: list external dependencies (network, file I/O, third-party services) that may be replaced with test doubles.
- `expected_effects`: list observable side effects or return values that confirm the path executed correctly.
- `trigger`: describe the event or API call that initiates the execution path.

## Property Test Quality Rules

For each `property_test`:

- `for_any_strategy`: describe the input generation strategy (e.g. "any non-empty string", "any valid spec ID matching `[0-9]{2}`").
- `invariant_check`: state the property that must hold for all generated inputs (e.g. "rendered output always contains the spec_id").
- `validates`: list the property IDs (`{spec_id}-PROP-{N}`) this test covers.

## Coverage Object

Submit `coverage` with empty arrays — the validation library computes coverage automatically from the `requirement_id`, `property_id`, and `execution_path_id` fields:

```json
"coverage": {
  "requirements_covered": [],
  "properties_covered": [],
  "paths_covered": [],
  "gaps": []
}
```

## Example

The fragment below shows a correctly structured test_spec artifact excerpt for a recipe-manager system. Use a different domain for your actual output — this example is for structural reference only.

```json
{
  "spec_id": "07",
  "spec_name": "recipe-manager",
  "schema_version": "1.0",
  "test_cases": [
    {
      "id": "TS-07-1",
      "requirement_id": "07-REQ-1.1",
      "description": "Creating a valid recipe returns HTTP 201 with the assigned ID",
      "preconditions": ["Recipe catalog database is initialised and empty"],
      "input": {"name": "Pasta Carbonara", "ingredients": ["eggs", "pancetta"]},
      "expected": {"status": 201, "body": {"id": "string", "name": "Pasta Carbonara"}},
      "assertion_pseudocode": "result = client.post('/recipes', body); assert result.status == 201; assert result.body.name == 'Pasta Carbonara'",
      "coverage": {"requirements_covered": [], "properties_covered": [], "paths_covered": [], "gaps": []}
    },
    {
      "id": "TS-07-2",
      "requirement_id": "07-REQ-1.2",
      "description": "Submitting a recipe with a missing name field returns HTTP 400",
      "preconditions": ["Recipe catalog database is initialised"],
      "input": {"ingredients": ["eggs"]},
      "expected": {"status": 400, "body": {"error": "name is required"}},
      "assertion_pseudocode": "result = client.post('/recipes', body); assert result.status == 400; assert 'name' in result.body.error",
      "coverage": {"requirements_covered": [], "properties_covered": [], "paths_covered": [], "gaps": []}
    }
  ]
}
```
