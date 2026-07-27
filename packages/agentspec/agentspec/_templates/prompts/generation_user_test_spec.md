## Additional Instructions

### Complete 1:1 coverage (mandatory)
Cross-file validation enforces strict coverage. You MUST generate:
- One `test_case` per acceptance criterion (requirement_id = the criterion ID, e.g. `05-REQ-1.1`)
- One `edge_case_test` per edge case (requirement_id = the edge case ID, e.g. `05-REQ-1.E1`)
- One `property_test` per correctness property (property_id = the property ID, e.g. `05-PROP-1`)
- One `smoke_test` per execution path (execution_path_id = the path ID, e.g. `05-PATH-1`)

Cross-check against the requirements artifact before submitting. Any missing coverage fails validation.

### Test quality
- Every test entry MUST have a non-empty `description` — a one-sentence explanation of what is being verified.
- `assertion_pseudocode` must be concrete enough that a developer can translate it directly to test code. Include specific function calls, expected values, and assertions. Use language-agnostic pseudocode, not language-specific syntax.
- `preconditions` must list all system state required before the test runs (database state, config, running services).
- `expected` must describe concrete observable outcomes, not vague statements.

### Error response verification
For every edge case or acceptance criterion whose `action` describes an error condition (error returned, request rejected, validation failed), the corresponding test case MUST assert the **caller-observable error response**, not just that an error occurred internally. Specifically:
- If the system exposes an HTTP API, assert the HTTP status code and response body structure.
- If the system is a CLI, assert the exit code and stderr output.
- If the system is a library, assert the error type/value returned to the caller.

The `expected` field must include the concrete error response shape, not just "an error is returned." If the requirement's `return_contract` is null for an error path, flag this in the test description as needing a return contract.

### Language consistency
The `assertion_pseudocode` must use language-agnostic pseudocode as stated above.
However, test `preconditions` and `expected` descriptions should reference the
project's actual components, tooling, and file paths (e.g. "SQLite database is
initialised with the events table" not "database fixture is set up") to be
useful to implementers working in the project's language.

### Termination and bounded iteration
For every correctness property or requirement involving a loop, retry path, or
iterative process, generate at least one property test that asserts
**termination or bounded iteration** — e.g., "for any input, the loop
executes at most N iterations" or "the retry count never exceeds the
configured maximum." These properties catch unbounded loops that only manifest
when the happy path fails, which unit tests for the happy path will not cover.

### Smoke test quality
- `real_components` must include ALL actors named in the execution path — do not mock them
- `mockable` must contain ONLY external I/O (filesystem, network, third-party services)
- `expected_effects` must be concrete observable outcomes, not vague assertions

### Property test quality
- `for_any_strategy` must specify the concrete sampling approach (not just "random valid inputs")
- `invariant_check` must be a concrete assertion, not a restatement of the property description

### Example: good vs bad test case
**Good:** `description: "Creating an org with a duplicate name returns HTTP 409"`, `expected: {"status": 409, "body": {"error": "organization name already exists"}}`, `assertion_pseudocode: "result = client.post('/orgs', {name: existing_name}); assert result.status == 409; assert result.body.error contains 'already exists'"`

**Bad:** `description: "Test duplicate org"`, `expected: {"error": true}`, `assertion_pseudocode: "create org and check it fails"`

### Coverage object
The `coverage` object is computed by the validation library. Submit it with empty arrays: `{"requirements_covered": [], "properties_covered": [], "paths_covered": [], "gaps": []}`
