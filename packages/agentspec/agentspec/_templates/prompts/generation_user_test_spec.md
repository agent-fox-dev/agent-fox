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

### Coverage object
The `coverage` object is computed by the validation library. Submit it with empty arrays: `{"requirements_covered": [], "properties_covered": [], "paths_covered": [], "gaps": []}`
