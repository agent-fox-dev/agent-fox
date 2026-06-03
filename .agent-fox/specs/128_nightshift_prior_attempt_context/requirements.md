# Requirements Document

## Introduction

This spec adds prior fix attempt context retrieval to the night-shift fix
pipeline. When processing a GitHub issue that has been attempted before, the
coder receives a summary of prior attempts so it can avoid repeating the same
failed approach.

## Glossary

- **Prior attempt**: A previous fix run for the same GitHub issue, identified
  by matching `spec_name` in the `session_outcomes` table. Each run produces
  one or more coder sessions; only the final coder session per run is relevant.
- **Fix run**: A single execution of the fix pipeline for one issue, identified
  by a unique `run_id`. A run may contain multiple coder sessions (retries
  within the coder-reviewer loop).
- **spec_name**: The identifier used in `session_outcomes` to link sessions to
  an issue: `fix-issue-{issue_number}`.
- **Prior attempt record**: A dataclass holding the date, status, error message,
  and model of the last coder session from a prior fix run.

## Requirements

### Requirement 1: Query prior fix attempts

**User Story:** As the fix pipeline, I want to retrieve prior fix attempt
records for a given issue so they can be included in the coder prompt.

#### Acceptance Criteria

1. [128-REQ-1.1] WHEN the fix pipeline is about to dispatch a coder session for
   issue N, THE system SHALL query the `session_outcomes` table for prior coder
   sessions with `spec_name = 'fix-issue-{N}'` and `archetype = 'coder'`,
   excluding sessions from the current `run_id`, AND return a list of prior
   attempt records to the caller.
2. [128-REQ-1.2] THE query SHALL group results by `run_id` and return only the
   last coder session per run (by `created_at` descending), limited to the 3
   most recent prior runs.
3. [128-REQ-1.3] THE query function SHALL return a list of `PriorAttempt`
   dataclass instances, each containing `run_id` (str), `created_at` (str),
   `status` (str), `error_message` (str or None), and `model` (str or None).

#### Edge Cases

1. [128-REQ-1.E1] IF no prior coder sessions exist for the issue, THEN THE
   query function SHALL return an empty list.
2. [128-REQ-1.E2] IF the database query fails (e.g., table missing, connection
   error), THEN THE system SHALL log a warning and return an empty list.

### Requirement 2: Format prior attempt context

**User Story:** As the fix pipeline, I want prior attempt records formatted as
a concise markdown block so they can be injected into the coder prompt.

#### Acceptance Criteria

1. [128-REQ-2.1] WHEN prior attempt records are available, THE system SHALL
   format them as a markdown section with the heading
   `## Prior Fix Attempts` followed by one numbered entry per attempt.
2. [128-REQ-2.2] EACH entry SHALL include the date (from `created_at`), the
   outcome status, the model used, and the error message (if any), truncated
   to 500 characters.
3. [128-REQ-2.3] WHEN the prior attempt list is empty, THE formatting function
   SHALL return an empty string.

### Requirement 3: Inject context into coder prompt

**User Story:** As the fix pipeline, I want prior attempt context injected into
the coder's task prompt so the coder knows what was tried before.

#### Acceptance Criteria

1. [128-REQ-3.1] WHEN prior attempts exist for the issue being processed, THE
   `_build_coder_prompt()` method SHALL prepend the formatted prior attempt
   context to the task prompt, before the issue description.
2. [128-REQ-3.2] WHEN no prior attempts exist, THE `_build_coder_prompt()`
   method SHALL produce the same task prompt as before (no change to existing
   behavior).

### Requirement 4: Integration with fix pipeline

**User Story:** As a maintainer, I want the prior attempt retrieval wired into
the fix pipeline's issue processing flow.

#### Acceptance Criteria

1. [128-REQ-4.1] WHEN `process_issue()` is called, THE fix pipeline SHALL
   query for prior attempts before entering the coder-reviewer loop AND pass
   the results through to `_build_coder_prompt()`.
2. [128-REQ-4.2] THE fix pipeline SHALL pass the DuckDB connection (`conn`)
   and the current `run_id` to the query function so that the current run's
   sessions are excluded from results.
