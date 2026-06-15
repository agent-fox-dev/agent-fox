# Requirements Document

## Introduction

This spec removes the `docs/memory.md` file and all references to it from the
agent-fox codebase. The file is a legacy manual knowledge-sharing mechanism
that has been superseded by the automated knowledge store.

## Glossary

- **seed file**: A file created by `agent-fox init` to establish initial project
  structure (e.g., `docs/memory.md`).
- **agents_md template**: The template at `agent_fox/_templates/agents_md.md`
  used to generate `CLAUDE.md` and `AGENTS.md` during `agent-fox init`.
- **agent profile**: Archetype-specific instructions at
  `agent_fox/_templates/profiles/agent.md` loaded into session prompts.

## Requirements

### Requirement 1: Delete the file

**User Story:** As a maintainer, I want `docs/memory.md` removed from the
repository so it is no longer tracked or referenced.

#### Acceptance Criteria

1. [129-REQ-1.1] THE file `docs/memory.md` SHALL NOT exist in the repository's
   tracked files.

### Requirement 2: Remove from init scaffolding

**User Story:** As a maintainer, I want `agent-fox init` to stop creating
`docs/memory.md` so new projects do not get the legacy file.

#### Acceptance Criteria

1. [129-REQ-2.1] WHEN `agent-fox init` is run, THE system SHALL NOT create
   `docs/memory.md`.
2. [129-REQ-2.2] THE `init_project.py` module SHALL NOT contain the
   `_DOCS_MEMORY_CONTENT` constant.

### Requirement 3: Remove from agent instructions

**User Story:** As an agent, I want instruction templates to not reference a
file that no longer exists so I am not confused by stale directives.

#### Acceptance Criteria

1. [129-REQ-3.1] THE `agents_md.md` template SHALL NOT reference
   `docs/memory.md`.
2. [129-REQ-3.2] THE project's `CLAUDE.md` SHALL NOT reference
   `docs/memory.md`.
3. [129-REQ-3.3] THE project's `AGENTS.md` SHALL NOT reference
   `docs/memory.md`.
4. [129-REQ-3.4] THE `agent.md` profile template SHALL NOT reference
   `docs/memory.md`.

### Requirement 4: Remove from skill templates

**User Story:** As a skill user, I want skill templates to not reference a
file that no longer exists.

#### Acceptance Criteria

1. [129-REQ-4.1] THE `af-fix` skill template SHALL NOT reference
   `docs/memory.md`.
2. [129-REQ-4.2] THE installed `af-fix` skill at
   `.claude/skills/af-fix/SKILL.md` SHALL NOT reference `docs/memory.md`.

### Requirement 5: Remove from tests

**User Story:** As a maintainer, I want tests that verify `docs/memory.md`
creation removed so the test suite does not assert the existence of a deleted
feature.

#### Acceptance Criteria

1. [129-REQ-5.1] THE test suite SHALL NOT contain tests that assert
   `docs/memory.md` is created by `agent-fox init`.

### Requirement 6: No dangling references

**User Story:** As a maintainer, I want no remaining references to
`docs/memory.md` in any tracked source or template file.

#### Acceptance Criteria

1. [129-REQ-6.1] THE repository SHALL NOT contain any tracked `.py` or `.md`
   file (excluding `docs/audits/` and `.agent-fox/specs/`) that references the
   string `docs/memory.md` or the path `memory.md` in the context of agent
   instructions.
