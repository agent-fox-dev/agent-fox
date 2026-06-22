---
spec_id: '01'
spec_name: nightshift_afspec_models
title: Nightshift Afspec Models
status: draft
created_at: '2026-06-22T12:54:23.327498+00:00'
updated_at: '2026-06-22T12:54:23.327498+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Night-Shift In-Memory afspec Construction

## Intent

Enable the night-shift fix pipeline to construct afspec `Spec` objects
in memory from triage output, so that night-shift coders receive the same
structured context format (EARS requirements, test contracts, task groups)
that the `code` command produces from on-disk specs.

## Goals

1. Provide a `build_afspec_from_triage()` function in
   `nightshift/spec_builder.py` that maps `TriageResult` →
   in-memory afspec `Spec` (no disk I/O).
2. Extract a `render_inmemory_spec_sections()` function in
   `session/context.py` that renders an in-memory `Spec` to the same
   markdown section format that `_render_spec_sections()` produces from
   on-disk specs.
3. Keep all afspec usage as pure data model construction — no
   `afspec.load_spec()`, no `afspec.save()`, no file system access.

## Non-Goals

- Changing the triage agent's output format or prompts.
- Changing how `_build_coder_prompt()` or `_build_reviewer_prompt()` work
  (that is the second spec).
- Persisting the in-memory spec to disk or `_session.json`.
- Changing the `code` command path.

## Background

The `code` command loads specs from `.agent-fox/specs/NN_name/` via
`afspec.load_spec()` and renders them via `afspec.render_individual()`.
This produces structured markdown sections (`## Requirements`,
`## Test Specification`, `## Tasks`) that are injected into the coder's
system prompt via `assemble_context()` → `build_system_prompt()`.

Night-shift currently constructs ad-hoc plain text from triage results:
`TriageResult` contains a list of `AcceptanceCriterion(id, description,
preconditions, expected, assertion)` which is rendered as flat markdown
in `_render_criteria_context()`.

The gap is that night-shift's coder receives a different, less structured
context format than the code-path coder. This spec bridges that gap at
the data layer.

## Tech Stack

- Python 3.12
- afspec library (Pydantic models: `Spec`, `Requirements`, `TestSpec`,
  `Tasks`, `PRDDocument`, `PRDFrontmatter`, `Requirement`, `TestCase`,
  `TaskGroup`, `Subtask`)
- Existing types: `TriageResult`, `AcceptanceCriterion`, `InMemorySpec`
  from `agentfox.nightshift.fix_pipeline` and
  `agentfox.nightshift.spec_builder`

## Detailed Requirements

### Triage → afspec Mapping (adapter logic)

The mapping uses existing `AcceptanceCriterion` fields without changing
triage output:

| AcceptanceCriterion | afspec Requirement |
|---------------------|-------------------|
| `id` (e.g. "AC-1") | `Requirement.id` (e.g. "NS-REQ-1") |
| `description` | `Requirement.title` |
| `preconditions` + `expected` | `Requirement.acceptance_criteria` (list with one entry) |
| (none) | `Requirement.edge_cases` (empty list) |
| (none) | `Requirement.user_story` (criterion description verbatim) |

### TestSpec derivation

Each `AcceptanceCriterion` maps to one `TestCase`:

| AcceptanceCriterion | afspec TestCase |
|---------------------|----------------|
| `id` (e.g. "AC-1") | `TestCase.id` (e.g. "TS-NS-1") |
| `id` | `TestCase.requirement_id` (e.g. "NS-REQ-1") |
| `description` | `TestCase.description` |
| `preconditions` | `TestCase.preconditions` |
| `expected` | `TestCase.expected` |
| `assertion` | `TestCase.assertion_pseudocode` |
| (none) | `TestCase.input` (empty string) |
| (none) | `TestCase.kind` ("acceptance") |

### Tasks structure

A single `TaskGroup` with `kind="tests"`:
- `id`: 1
- `title`: "Fix issue #{issue_number}"
- One `Subtask` per `AcceptanceCriterion`:
  - `id`: "1.N" (sequential)
  - `title`: criterion description
  - `details`: list containing preconditions, expected, assertion
  - `state`: `SubtaskState.PENDING`
  - `test_spec_refs`: reference to derived TestCase id
  - `requirement_refs`: reference to mapped Requirement id

### PRD document

Construct a minimal `PRDDocument` from the issue metadata:
- `PRDFrontmatter(spec_id="fix-{issue_number}", spec_name="fix_issue_{issue_number}")`
- `body`: the sanitized issue body text

### render_inmemory_spec_sections()

Extract the rendering logic from `_render_spec_sections()` so it can
operate on an in-memory `Spec` object:

```python
def render_inmemory_spec_sections(spec: afspec.Spec) -> list[str]:
    """Render an in-memory Spec to markdown sections.

    Uses afspec.render_individual() — same rendering as the disk-based
    path but without file I/O.
    """
```

The existing `_render_spec_sections()` should be refactored to call
this new function internally after loading from disk, avoiding code
duplication.

### Error handling

- If `TriageResult` has no criteria, return a `Spec` with empty
  requirements/test_spec and a single "Fix the issue" subtask.
- If any `AcceptanceCriterion` field is empty/None, use sensible
  defaults (empty string) rather than raising.

## Design Decisions

1. **Adapter over reshape:** We map existing triage fields into afspec
   equivalents rather than changing the triage agent, because triage is a
   separate concern with its own spec and prompt chain. Changing it would
   cascade to triage parsing, tests, and prompt templates.

2. **Derive TestSpec from criteria:** Each acceptance criterion maps to
   one test case. This gives the coder actionable test contracts without
   requiring a separate test-generation step. The mapping is mechanical
   (fields are direct counterparts).

3. **Single task group:** Night-shift fixes are atomic — one issue, one
   branch, one coder session. Multiple task groups would add graph
   complexity with no benefit since night-shift doesn't use the task
   graph planner.

4. **Spec IDs use "fix-{N}" prefix:** Distinguishes night-shift specs
   from code-path specs (which use numeric "NN" prefixes). The
   `spec_id` and `spec_name` fields in afspec models are metadata for
   rendering — they don't affect file paths since these specs never
   touch disk.

5. **PRDDocument is minimal:** Only `spec_id`, `spec_name`, and `body`
   are populated. Fields like `status`, `created_at`, `owner` are
   irrelevant for ephemeral in-memory specs.

## Clarifications

1. **user_story derivation:** `Requirement.user_story` uses the criterion
   description verbatim. No template, no LLM call.
2. **render_individual() signature:** `afspec.render_individual(spec: Spec)`
   already accepts an in-memory `Spec` object directly — no file path
   needed. The no-file-I/O constraint is satisfied without changes to
   afspec.

## Source

Source: Input provided by user via interactive prompt (based on prior
analysis of night-shift vs code command architecture)

