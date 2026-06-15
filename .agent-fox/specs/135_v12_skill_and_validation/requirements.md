# Requirements Document

## Introduction

This spec updates two user-facing surfaces to support the v1.2 spec format:
the `lint-specs` CLI command (which validates spec files) and the `af-spec`
Claude Code skill template (which instructs an AI agent to produce spec
artifacts). The lint-specs command gains format-aware routing so that v1.2
specs are validated by `afspec.validate()` while v1 markdown specs continue
to use the existing custom validators. The skill template is rewritten to
produce v1.2 artifacts.

## Glossary

| Term | Definition |
|------|-----------|
| afspec | Python library from af-core that provides models, validation, rendering, and discovery for the v1.2 spec format |
| ValidationError | Dataclass returned by `afspec.validate()` representing a single validation finding (has fields: file, rule, severity, message, line) |
| Finding | Frozen dataclass in `agent_fox.spec.validators._helpers` representing a single lint finding (has fields: spec_name, file, rule, severity, message, line) |
| SpecFormat | Enum in `agent_fox.spec.discovery` distinguishing v1 (markdown) from v1.2 (JSON) spec formats |
| v1 format | The legacy markdown-based spec format with `requirements.md`, `design.md`, `test_spec.md`, `tasks.md` |
| v1.2 format | The new JSON-based spec format with `requirements.json`, `test_spec.json`, `tasks.json`, and optional `architecture.md` |
| af-spec skill | Claude Code skill template at `agent_fox/_templates/skills/af-spec` that instructs the AI agent how to produce spec artifacts |
| EARS | Easy Approach to Requirements Syntax -- a structured pattern for writing requirements using keywords like SHALL, WHEN, WHILE, IF/THEN |
| YAML frontmatter | Metadata block at the top of `prd.md` delimited by `---` markers, containing fields like spec_id, title, status |

## Requirements

### Requirement 1: Format-Aware Validation Routing

**User Story:** As a developer running `agent-fox lint-specs`, I want v1.2
specs to be validated using `afspec.validate()`, so that the linter enforces
the canonical v1.2 schema and cross-file integrity checks.

#### Acceptance Criteria

1. [135-REQ-1.1] WHEN `run_lint_specs()` encounters a spec with
   `SpecInfo.format == V1_2_JSON`, THE system SHALL validate it using
   `afspec.validate()` instead of the custom `validate_specs()` function AND
   return the results as `Finding` instances.
2. [135-REQ-1.2] WHEN `run_lint_specs()` encounters a spec with
   `SpecInfo.format == V1_MARKDOWN`, THE system SHALL validate it using the
   existing custom `validate_specs()` function AND return findings unchanged.
3. [135-REQ-1.3] WHEN a mix of v1 and v1.2 specs is discovered, THE system
   SHALL validate each spec using the appropriate validator for its format AND
   return a combined list of `Finding` instances sorted by spec name, file,
   and severity.

#### Edge Cases

1. [135-REQ-1.E1] IF `afspec.validate()` raises an unexpected exception for a
   v1.2 spec, THEN THE system SHALL emit a single error-severity `Finding`
   with rule `afspec-error` and the exception message, AND continue validating
   remaining specs.

### Requirement 2: ValidationError to Finding Mapping

**User Story:** As the lint-specs CLI, I want `afspec.ValidationError`
instances mapped to `Finding` instances, so that the output format is
unchanged regardless of which validator was used.

#### Acceptance Criteria

1. [135-REQ-2.1] WHEN `afspec.validate()` returns a list of
   `ValidationError` instances, THE system SHALL map each to a `Finding` with
   `spec_name` set to the spec folder name, `file` set to the
   `ValidationError.file` field, `rule` set to the `ValidationError.rule`
   field, `severity` mapped to the matching `Finding` severity constant,
   `message` set to the `ValidationError.message` field, and `line` set to
   the `ValidationError.line` field AND return the list of `Finding` instances
   to the caller.
2. [135-REQ-2.2] WHEN a `ValidationError` has a severity value not in
   `{error, warning, hint}`, THE system SHALL default the `Finding` severity
   to `error`.

#### Edge Cases

1. [135-REQ-2.E1] IF `afspec.validate()` returns an empty list for a v1.2
   spec, THEN THE system SHALL produce zero findings for that spec (clean
   validation pass).

### Requirement 3: CLI Interface Preservation

**User Story:** As a developer, I want the `agent-fox lint-specs` CLI to
work identically for both v1 and v1.2 specs, so that I do not need to change
my workflow.

#### Acceptance Criteria

1. [135-REQ-3.1] THE `lint-specs` CLI command SHALL accept the same flags
   (`--ai`, `--all`) and produce the same output formats (table and JSON) as
   before the v1.2 routing change.
2. [135-REQ-3.2] WHEN `lint-specs` is run with `--all`, THE system SHALL
   include both v1 and v1.2 specs in the validation results.

### Requirement 4: Skill Template v1.2 Artifact Instructions

**User Story:** As an AI agent using the af-spec skill, I want the skill
template to instruct me to produce v1.2 format artifacts, so that newly
authored specs use the canonical JSON format.

#### Acceptance Criteria

1. [135-REQ-4.1] THE af-spec skill template SHALL instruct the agent to
   produce the following artifacts: `prd.md` (with YAML frontmatter),
   `requirements.json`, `test_spec.json`, `tasks.json`, and optionally
   `architecture.md`.
2. [135-REQ-4.2] THE af-spec skill template SHALL reference v1.2 ID formats:
   `{spec_id}-REQ-{N}` for requirements, `{spec_id}-PROP-{N}` for
   properties, and `{spec_id}-TS-{N}` for test cases.
3. [135-REQ-4.3] THE af-spec skill template SHALL describe the JSON structure
   for requirements (discriminated union on `ears_pattern`), test specs (test
   cases with typed entries), and tasks (state machine with task groups).

### Requirement 5: Skill Template Validation Step

**User Story:** As an AI agent, I want the skill template to include a
validation step that runs `agent-fox lint-specs`, so that I can verify the
generated spec passes validation before presenting it.

#### Acceptance Criteria

1. [135-REQ-5.1] THE af-spec skill template SHALL include a step that
   instructs the agent to run `agent-fox lint-specs` after generating all
   spec artifacts AND fix any validation errors before presenting the spec to
   the user.
2. [135-REQ-5.2] THE af-spec skill template SHALL reference `afspec`'s
   format specification as the authoritative source for artifact schemas.

### Requirement 6: Skill Template EARS JSON Structure

**User Story:** As an AI agent, I want the skill template to describe the
EARS pattern JSON structure, so that I produce correctly structured
`requirements.json` files.

#### Acceptance Criteria

1. [135-REQ-6.1] THE af-spec skill template SHALL describe the EARS pattern
   discriminated union with fields: `ears_pattern` (enum: ubiquitous,
   event_driven, complex_event, state_driven, unwanted, optional),
   pattern-specific fields (e.g., `trigger` for event_driven, `condition`
   for unwanted), and `action` (the SHALL clause).
2. [135-REQ-6.2] THE af-spec skill template SHALL describe the tasks JSON
   structure with task groups containing subtasks, each with a state field
   (not_started, in_progress, completed, queued, optional) replacing
   markdown checkboxes.
