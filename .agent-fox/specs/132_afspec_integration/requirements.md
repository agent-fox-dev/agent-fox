# Requirements Document

## Introduction

This spec adds the `afspec` library as a dependency and updates the spec
discovery module to detect and distinguish v1.2 (JSON) spec packages from
legacy v1 (markdown) spec packages.

## Glossary

| Term | Definition |
|------|-----------|
| afspec | Python library from af-core that provides models, validation, rendering, and discovery for the v1.2 spec format |
| SpecInfo | Dataclass in `agent_fox/spec/discovery.py` carrying metadata about a discovered spec folder |
| SpecFormat | Enum distinguishing v1 (markdown) from v1.2 (JSON) spec formats |
| v1 format | The legacy markdown-based spec format with `requirements.md`, `design.md`, `test_spec.md`, `tasks.md` |
| v1.2 format | The new JSON-based spec format with `requirements.json`, `test_spec.json`, `tasks.json`, and optional `architecture.md` |
| spec discovery | The process of scanning `.agent-fox/specs/` for valid spec folders |

## Requirements

### Requirement 1: afspec Dependency

**User Story:** As a developer, I want `afspec` available as an importable
package, so that agent-fox can use its models, validation, and rendering.

#### Acceptance Criteria

1. [132-REQ-1.1] THE build system SHALL declare `afspec` as a dependency in
   `pyproject.toml` using a local path reference to the af-core repository.
2. [132-REQ-1.2] WHEN `import afspec` is executed in the agent-fox environment,
   THE system SHALL successfully import the package AND return the `afspec`
   module to the caller.
3. [132-REQ-1.3] WHEN `afspec.load_spec(dir)` is called with a valid v1.2 spec
   directory, THE system SHALL return a populated `afspec.Spec` object AND
   return it to the caller for use in subsequent operations.

#### Edge Cases

1. [132-REQ-1.E1] IF the `afspec` package path does not exist at install time,
   THEN THE build system SHALL report an installation error with the missing
   path.

### Requirement 2: Spec Format Enum

**User Story:** As the orchestrator pipeline, I want a format discriminator,
so that I can route spec loading to the correct parser.

#### Acceptance Criteria

1. [132-REQ-2.1] THE system SHALL provide a `SpecFormat` enum in
   `agent_fox/spec/discovery.py` with values `V1_MARKDOWN` and `V1_2_JSON`.
2. [132-REQ-2.2] THE `SpecInfo` dataclass SHALL include a `format` field of
   type `SpecFormat` indicating which format the spec folder uses.

#### Edge Cases

1. [132-REQ-2.E1] IF a spec folder contains neither `requirements.md` nor
   `requirements.json`, THEN THE discovery module SHALL skip that folder
   without error.

### Requirement 3: v1.2 Format Detection

**User Story:** As the discovery module, I want to detect which format a spec
folder uses, so that only v1.2 specs enter the pipeline.

#### Acceptance Criteria

1. [132-REQ-3.1] WHEN a spec folder contains `requirements.json`, THE
   discovery module SHALL classify it as `SpecFormat.V1_2_JSON`.
2. [132-REQ-3.2] WHEN a spec folder contains `requirements.md` but not
   `requirements.json`, THE discovery module SHALL classify it as
   `SpecFormat.V1_MARKDOWN`.
3. [132-REQ-3.3] WHEN `discover_specs()` is called, THE system SHALL return
   only specs with format `V1_2_JSON` AND exclude all `V1_MARKDOWN` specs
   from the result list.
4. [132-REQ-3.4] WHEN a v1.2 spec folder is discovered, THE system SHALL
   populate `SpecInfo.has_tasks` by checking for `tasks.json` AND
   populate `SpecInfo.has_prd` by checking for `prd.md`.

#### Edge Cases

1. [132-REQ-3.E1] IF a spec folder contains both `requirements.md` and
   `requirements.json`, THEN THE discovery module SHALL classify it as
   `V1_2_JSON` (JSON takes precedence).

### Requirement 4: afspec Integration Verification

**User Story:** As a developer, I want to confirm that afspec can load
specs found by discovery, so that the foundation is proven end-to-end.

#### Acceptance Criteria

1. [132-REQ-4.1] WHEN `afspec.load_spec()` is called with the path from a
   discovered `SpecInfo` with format `V1_2_JSON`, THE system SHALL return
   a valid `afspec.Spec` object with populated `prd`, `requirements`,
   `test_spec`, and `tasks` fields AND return it to the caller.
2. [132-REQ-4.2] WHEN `afspec.render_combined()` is called with a loaded
   `Spec`, THE system SHALL return a non-empty markdown string AND return
   it to the caller for use in context assembly.

#### Edge Cases

1. [132-REQ-4.E1] IF a discovered spec folder has malformed JSON in any
   artifact, THEN `afspec.load_spec()` SHALL raise `afspec.LoadError`
   with a message identifying the malformed file.
