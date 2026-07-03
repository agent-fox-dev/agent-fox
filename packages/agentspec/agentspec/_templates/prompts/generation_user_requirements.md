## Additional Instructions

### Introduction
The `introduction` field is required — write a brief (1-2 sentence) description of the system being specified.

### Titles
Every requirement, correctness property, and execution path MUST have a non-empty `title` — a short human-readable label (e.g. "Event ingestion endpoint", "Bearer token authentication"). Empty titles fail validation.

### Glossary completeness
The `glossary` defines project-specific terms that a developer unfamiliar with this system would not know from general knowledge. Only use backticks around terms that genuinely need a contextual definition — every backtick-delimited term in `action`, `trigger`, `condition`, `state`, `error_condition`, `for_any`, and `invariant` fields MUST have a glossary entry. Missing entries fail validation.

**Include** (backtick + define): project-specific identifiers like table or column names (`events`, `received_at`), environment variables (`AUTH_BEARER_TOKEN`), custom API endpoints (`POST /v1/events`), domain concepts with meaning specific to this system, and configuration values whose purpose is not self-evident.

**Exclude** (use plain prose, no backticks): standard HTTP status codes (200, 404, 500), well-known protocols and formats (JSON, HTTP, UUID), standard ports, generic error response shapes, language keywords, file path conventions, log levels, and any term a working developer would already know. Write these in plain text without backticks.

**Pre-submission check**: Before submitting, scan every `action`, `trigger`,
`error_condition`, `state`, `for_any`, and `invariant` field for backtick-delimited
terms. Verify each one has a glossary entry. This is the #1 cause of validation
failures — catch it before submission rather than requiring a repair cycle.

### Error handling
The `error_handling` array maps error conditions to system behavior. Each entry needs:
- `id`: format `{spec_id}-ERR-{N}`
- `condition`: the error condition
- `behavior`: what the system does in response
- `requirement_id`: the requirement or edge case ID that specifies this behavior (e.g. `05-REQ-2.E1`)

### Execution paths
Each execution path traces a user-visible feature from entry point to observable side effect using logical actors (not module names). Every path must start at a user action and end at a concrete side effect. Steps need `actor` and `action` fields. At least two steps per path.

### Return contracts
Set `return_contract` to a non-null string on every criterion whose action produces an observable response — HTTP status codes, return values, response bodies, error messages. Only use null when the action has no caller-visible output (e.g. a background side effect). Concrete return contracts make implementation and testing significantly easier.

### Correctness properties
Each property's `validates` array must reference acceptance criterion IDs that exist in `requirements`.

### Cross-spec integration (multi-spec PRDs)
When the PRD describes a system split into multiple specs with dependency edges
(e.g. layers, pipeline stages, or separate subsystems that compose at runtime),
the **last spec in the dependency chain** must include at least one execution
path that traces the **full end-to-end user flow** — from the user-facing entry
point (CLI command, API call) through every upstream layer to the final
observable side effect. This path must name actors from each upstream spec it
depends on.

Without this path, no spec owns the integration glue between layers, and the
wiring verification step cannot verify that the layers actually connect. This is
the most common cause of "individually correct but collectively broken"
implementations.

If this spec is the terminal spec in a multi-spec dependency chain, include
such a path. If it is an upstream dependency consumed by a later spec, this
rule does not apply — the downstream spec is responsible.
