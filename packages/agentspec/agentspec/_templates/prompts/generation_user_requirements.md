## Additional Instructions

### EARS pattern selection
Choose the EARS pattern that matches the situation:

| Situation | Pattern |
|-----------|---------|
| Invariant that must always hold | `ubiquitous` |
| Discrete event triggers behavior | `event_driven` |
| Event + guard condition | `complex_event` |
| Behavior depends on ongoing system state | `state_driven` |
| Error/failure path | `unwanted` |
| Feature-flagged behavior | `optional` |

Do NOT default to `ubiquitous` — most requirements are `event_driven` or `unwanted`.

### Introduction
The `introduction` field is required — write a brief (1-2 sentence) description of the system being specified.

### Titles
Every requirement, correctness property, and execution path MUST have a non-empty `title` — a short human-readable label (e.g. "Event ingestion endpoint", "Bearer token authentication"). Empty titles fail validation.

### Glossary completeness
The `glossary` defines project-specific terms that a developer unfamiliar with this system would not know from general knowledge. Only use backticks around terms that genuinely need a contextual definition — every backtick-delimited term in `action`, `trigger`, `condition`, `state`, `error_condition`, `for_any`, and `invariant` fields MUST have a glossary entry. Missing entries fail validation.

**Include** (backtick + define): project-specific identifiers like table or column names (`events`, `received_at`), environment variables (`AUTH_BEARER_TOKEN`), custom API endpoints (`POST /v1/events`), domain concepts with meaning specific to this system, and configuration values whose purpose is not self-evident.

**Exclude** (use plain prose, no backticks): standard HTTP status codes (200, 404, 500), well-known protocols and formats (JSON, HTTP, UUID), standard ports, generic error response shapes, language keywords, file path conventions, log levels, and any term a working developer would already know. Write these in plain text without backticks.

**Common mistakes** (do NOT backtick these):
- Numeric values or suffixes: write "appends -1, -2" not "appends `-1`, `-2`"
- Error message strings: write 'returns "not found"' not 'returns `"not found"`'
- Standard library functions or types: write "calls os.Exit" not "calls `os.Exit`"
- Single-character values or operators: write "returns 0" not "returns `0`"

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
Each execution path traces a user-visible feature from entry point to observable side effect using logical actors (not module names). Steps need `actor` and `action` fields. At least two steps per path.

Must start at: a user action, CLI command, API call, or scheduled trigger.
Must end at: a file written, API call made, value returned to caller, or state change persisted.

### Return contracts
Set `return_contract` to a non-null string on every criterion whose action produces an observable response — HTTP status codes, return values, response bodies, error messages. **This includes error paths**: if a criterion or edge case describes a failure condition, the return_contract must specify what the caller observes (e.g., "returns HTTP 401 with JSON body {error: string}", "returns (nil, ErrUnauthorized)"). Only use null when the action has no caller-visible output (e.g. a background side effect with no response). Concrete return contracts make implementation and testing significantly easier.

Every criterion with `ears_pattern: "unwanted"` MUST have a non-null `return_contract`. This is enforced by validation — error paths always have a caller-observable result.

### Correctness properties
The `validates` array must reference acceptance criterion IDs that exist in `requirements`. Coverage rules:
1. Every requirement's primary acceptance criterion should have at least one validating property
2. Generate at least one property for the happy path, one for failure handling, and one for boundary conditions
3. Properties must be testable — each maps to a property-based test

### Defensive design edge cases
For any requirement that involves subprocesses, external commands, loops,
retries, or calls to external services, you MUST generate edge cases covering:

- **Timeout / hang** — the subprocess or call does not return. The edge case
  must specify a maximum wait time or timeout mechanism and what happens when
  it fires (error returned, process killed, state cleaned up).
- **Resource cleanup on failure** — when the operation fails midway, partial
  state (temporary files, worktrees, open connections, child processes) must
  be released. The edge case must specify what gets cleaned up and how.
- **Unbounded iteration** — loops and retry paths must have a maximum
  iteration cap or be explicitly delegated to a named safety mechanism (e.g.,
  a circuit breaker). If delegated, the edge case must state what happens
  when the safety mechanism is absent or disabled.
- **Library vs. application boundary** — if the system is structured as a
  library consumed by a CLI or application, library code must never terminate
  the process directly. The edge case must specify that errors are signaled
  via return values or exceptions, not process termination.

- **External API response variance** — when the system calls an external API,
  edge cases must cover: the API returning an error status, the API returning
  a success status with missing or null fields the system depends on, and the
  API returning an unexpected data shape. These are the most common source of
  "works in development, breaks in production" defects for API integrations.

These are the most common source of "works on happy path, breaks in
production" defects. Do not skip them.

### Systematic edge cases (mandatory)
Every requirement MUST have edge cases covering these categories where applicable:
1. **Empty/null input** — what happens when required input is missing or empty
2. **Boundary values** — minimum, maximum, just-over-limit values
3. **Operation failure** — what happens when the core operation fails
4. **Authorization failure** — what happens when the caller lacks permission
5. **Concurrent operations** — what happens under simultaneous access

A requirement with an empty `edge_cases` array is almost always wrong.

### Example: good vs bad requirement
**Good:** `ears_pattern: "event_driven"`, `trigger: "a user calls POST /orgs"`, `action: "create an Organization row with a generated ULID and return it"`, `return_contract: "returns HTTP 201 with JSON body {id: string, name: string}"`

**Bad:** `ears_pattern: "ubiquitous"`, `action: "handle organization creation properly"`, `return_contract: null`

### External library references
If the PRD contains a `## Verified External API` section, use **only** the
function names, signatures, return types, and import paths listed there when
writing requirements that reference external libraries. Do not assume functions
or types that are not in the verified table — if a requirement needs a function
not listed, note the gap in the requirement's `action` field (e.g., "calls a
local wrapper around X since the library does not provide Y directly").

If the PRD references external libraries but has **no** Verified External API
section, note this in the `introduction` field (e.g., "Note: requirements
referencing afspec/afaudit APIs are based on unverified PRD assumptions and
should be cross-checked against the installed library before implementation").
This warns the coding agent to verify signatures before coding.

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
