# Requirements Document

## Introduction

This spec updates the session context assembly module and spec helper
functions to render v1.2 JSON specs into markdown for prompt injection,
replacing raw markdown file reads with afspec-based structured rendering.

## Glossary

| Term | Definition |
|------|-----------|
| afspec | Python library from af-core providing models, validation, rendering, and I/O for the v1.2 spec format |
| context assembly | The process in `agent_fox/session/context.py` that gathers spec documents, findings, memory facts, and steering directives into a single prompt string |
| v1.2 format | The JSON-based spec format with `requirements.json`, `test_spec.json`, `tasks.json`, and optional `architecture.md` |
| v1 format | The legacy markdown-based spec format with `requirements.md`, `design.md`, `test_spec.md`, `tasks.md` |
| render_individual | afspec function returning a dict mapping artifact names to rendered markdown strings |
| architecture.md | The v1.2 replacement for `design.md`, read directly from disk as a markdown file |
| verification checklist | Structured audit of task completion and requirement coverage injected into verifier sessions |

## Requirements

### Requirement 1: v1.2 Format Detection in Context Assembly

**User Story:** As the context assembly module, I want to detect whether a
spec folder uses v1.2 format, so that I can route to the correct rendering
path.

#### Acceptance Criteria

1. [134-REQ-1.1] WHEN `assemble_context()` is called with a spec directory
   containing `requirements.json`, THE system SHALL treat the spec as v1.2
   format AND use afspec-based rendering for the core spec sections.
2. [134-REQ-1.2] WHEN `assemble_context()` is called with a spec directory
   that does NOT contain `requirements.json`, THE system SHALL use the
   existing raw markdown file reading path unchanged.

#### Edge Cases

1. [134-REQ-1.E1] IF a v1.2 spec folder triggers an `afspec.LoadError` during
   loading, THEN THE system SHALL log a warning AND fall back to reading
   whatever markdown files exist in the folder.

### Requirement 2: v1.2 Spec Rendering via afspec

**User Story:** As a coding agent, I want v1.2 specs rendered into the same
markdown section format I expect, so that context quality is preserved
regardless of the underlying spec format.

#### Acceptance Criteria

1. [134-REQ-2.1] WHEN a v1.2 spec is loaded successfully, THE system SHALL
   render requirements, test spec, and tasks sections using
   `afspec.render_individual()` AND include each rendered artifact as a
   separate section in the assembled context.
2. [134-REQ-2.2] WHEN a v1.2 spec folder contains `architecture.md`, THE
   system SHALL read it from disk AND include it in the context under a
   "## Architecture" header.
3. [134-REQ-2.3] WHEN a v1.2 spec folder does NOT contain `architecture.md`,
   THE system SHALL omit the architecture section without warning.

#### Edge Cases

1. [134-REQ-2.E1] IF `afspec.render_individual()` returns an empty string for
   any artifact, THEN THE system SHALL omit that artifact's section from the
   assembled context.

### Requirement 3: v1.2-Aware Spec Helpers

**User Story:** As the graph builder, I want spec helpers to work with v1.2
specs, so that test count estimation and oracle gating remain accurate.

#### Acceptance Criteria

1. [134-REQ-3.1] WHEN `count_ts_entries()` is called with a v1.2 spec
   directory (containing `test_spec.json`), THE system SHALL load the test
   spec via afspec AND return the total count of test cases, property tests,
   edge case tests, and smoke tests.
2. [134-REQ-3.2] WHEN `count_ts_entries()` is called with a v1 spec directory
   (no `test_spec.json`), THE system SHALL use the existing `### TS-` heading
   counting logic unchanged.
3. [134-REQ-3.3] WHEN `spec_has_existing_code()` is called with a v1.2 spec
   directory, THE system SHALL check `architecture.md` instead of `design.md`
   for `(modified)` file references.

#### Edge Cases

1. [134-REQ-3.E1] IF `test_spec.json` exists but afspec fails to load it,
   THEN `count_ts_entries()` SHALL return 0 AND log a warning.

### Requirement 4: v1.2-Aware Verification Checklist

**User Story:** As the verifier archetype, I want the verification checklist
to extract structured data from v1.2 specs, so that task completion and
requirement coverage audits work correctly.

#### Acceptance Criteria

1. [134-REQ-4.1] WHEN `_audit_task_checkboxes()` is called with a v1.2 spec
   directory, THE system SHALL load `tasks.json` via afspec AND extract
   subtask completion state from the Pydantic models instead of parsing
   `tasks.md`.
2. [134-REQ-4.2] WHEN `scan_requirement_test_coverage()` is called with a
   v1.2 spec directory, THE system SHALL extract requirement IDs from
   `requirements.json` via afspec models instead of parsing `requirements.md`
   with regex.

#### Edge Cases

1. [134-REQ-4.E1] IF `tasks.json` or `requirements.json` cannot be loaded by
   afspec, THEN the corresponding checklist function SHALL return an empty
   list AND log a warning.
