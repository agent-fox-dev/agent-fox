Generate the test specification: **one flat list** of tests. The `kind` field distinguishes them and the `verifies` field links each to what it proves. There are no separate arrays for edge-case, property or smoke tests, and no `coverage` object — coverage is computed from `verifies`.

The complete requirements artifact is above. Use its real IDs. Do not invent one.

## Test fields

- `id` — `TS-{spec_id}-{N}`, N sequential from 1. **One number series for all kinds.**
- `kind` — one of:
  - `unit` — exercises one component in isolation; collaborators may be stubbed.
  - `integration` — exercises two or more real components; only external I/O may be stubbed.
  - `property` — property-based; `when` names the input domain, `then` states the invariant.
  - `smoke` — traverses a full execution path with real components.
- `verifies` — non-empty. Criterion IDs (`…-REQ-N.C`) for `unit`, `integration` and `property`; a path ID (`…-PATH-N`) for `smoke`, which may also list criterion IDs.
- `title` — one sentence saying what is verified.
- `given` — preconditions. May be empty, but write them when they matter.
- `when` — the action or input. For `property`, the generator: "for any non-empty list of …".
- `then` — non-empty. Observable outcomes. For `smoke`, the side effects at the end of the path.
- `pseudocode` — optional, language-agnostic assertions. Recommended for `unit`, `integration` and `property`; it may name concrete functions and files.
- `real_components` — required and non-empty for `smoke`, and **must be absent for every other kind**. Lists what must not be mocked.

## Coverage you must achieve

- Every criterion in the requirements artifact appears in the `verifies` of at least one test. A criterion with no test makes the spec invalid.
- Every execution path appears in the `verifies` of at least one `smoke` test.
- A test verifies at most four criteria. If you find yourself listing more, it is two tests.
- For every `unwanted` criterion, assert the caller-observable outcome its `contract` names — the status code, the exit code, the returned error — not merely that "an error occurred".
- Write a `property` test for each `ubiquitous` criterion whose action begins "for any …".

## Example

```json
{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v2.json",
  "spec_id": "07",
  "spec_name": "recipe_manager",
  "schema_version": 2,
  "tests": [
    {
      "id": "TS-07-1",
      "kind": "unit",
      "verifies": ["07-REQ-1.1"],
      "title": "A valid recipe body is persisted and its identifier returned",
      "given": ["an empty catalog"],
      "when": "POST /recipes is called with a name and one ingredient",
      "then": ["the status is 201", "the body carries a non-empty id", "the catalog holds one row"],
      "pseudocode": "r = post('/recipes', body); assert r.status == 201; assert len(r.json.id) > 0"
    },
    {
      "id": "TS-07-2",
      "kind": "unit",
      "verifies": ["07-REQ-1.2"],
      "title": "A body with no name is rejected and nothing is persisted",
      "given": ["an empty catalog"],
      "when": "POST /recipes is called with a body that has no name field",
      "then": ["the status is 400", "the body carries a non-empty error string", "the catalog is still empty"],
      "pseudocode": "r = post('/recipes', {}); assert r.status == 400; assert count(catalog) == 0"
    },
    {
      "id": "TS-07-3",
      "kind": "property",
      "verifies": ["07-REQ-1.3"],
      "title": "A persisted recipe keeps its identifier across reads",
      "given": ["an empty catalog"],
      "when": "for any recipe with a non-empty name, it is written once and read twice",
      "then": ["both reads return the same identifier"],
      "pseudocode": "for rec in gen_recipes(): id = post(rec).json.id; assert get(id).id == get(id).id"
    },
    {
      "id": "TS-07-4",
      "kind": "smoke",
      "verifies": ["07-PATH-1", "07-REQ-1.1"],
      "title": "A cook saves a recipe and reads it back through the running service",
      "given": ["the service is running against a real database"],
      "when": "a recipe is posted and then fetched by its returned identifier",
      "then": ["the post returns 201", "the fetch returns the recipe that was posted"],
      "real_components": ["HTTP handler", "recipe catalog", "database"]
    }
  ]
}
```
