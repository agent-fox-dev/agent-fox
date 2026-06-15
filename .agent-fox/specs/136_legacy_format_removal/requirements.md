# Requirements Document

## Introduction

This spec removes all legacy markdown-based spec format code from agent-fox
after the v1.2 JSON format support has been implemented by specs 132-135.
The goal is a clean codebase with no dead code paths for the old format.

## Glossary

| Term | Definition |
|------|-----------|
| legacy code | Code that only serves the old markdown spec format (v1) and has been superseded by v1.2 JSON equivalents |
| parser.py | The markdown-based spec parser module at `agent_fox/spec/parser.py` |
| validators/ | The markdown validation directory at `agent_fox/spec/validators/` (9 files) |
| types.py | New module `agent_fox/spec/types.py` holding shared dataclasses extracted from parser.py |
| TaskGroupDef | Dataclass representing a parsed task group, used by graph builder and parsers |
| SubtaskDef | Dataclass representing a parsed subtask within a task group |
| CrossSpecDep | Dataclass representing a cross-spec dependency declaration |
| parser_v12 | The v1.2 JSON parser module created by spec 133 at `agent_fox/spec/parser_v12.py` |
| import rewiring | Updating import statements in consumer modules to point to new locations |

## Requirements

### Requirement 1: Extract Shared Dataclasses

**User Story:** As a developer, I want shared types in their own module,
so that deleting parser.py does not break consumers.

#### Acceptance Criteria

1. [136-REQ-1.1] THE system SHALL provide a module `agent_fox/spec/types.py`
   containing the `TaskGroupDef`, `SubtaskDef`, and `CrossSpecDep` dataclasses
   with identical field signatures to those in the deleted `parser.py`.
2. [136-REQ-1.2] WHEN any module imports `TaskGroupDef`, `SubtaskDef`, or
   `CrossSpecDep`, THE system SHALL resolve the import from
   `agent_fox.spec.types` AND the imported class SHALL be the same object
   regardless of import path.

#### Edge Cases

1. [136-REQ-1.E1] IF a module still imports from `agent_fox.spec.parser`,
   THEN THE system SHALL raise `ImportError` at import time because the
   module no longer exists.

### Requirement 2: Delete Legacy Parser

**User Story:** As a maintainer, I want the markdown parser removed,
so that there is no dead code for the old format.

#### Acceptance Criteria

1. [136-REQ-2.1] THE system SHALL NOT contain the file
   `agent_fox/spec/parser.py` after this spec is implemented.
2. [136-REQ-2.2] WHEN the test suite runs, THE system SHALL pass all tests
   without any reference to the deleted `parser.py` module.

#### Edge Cases

1. [136-REQ-2.E1] IF any test file imports from `agent_fox.spec.parser`,
   THEN that test SHALL be updated or deleted to remove the reference.

### Requirement 3: Delete Legacy Validators

**User Story:** As a maintainer, I want the markdown validators removed,
so that validation uses afspec exclusively.

#### Acceptance Criteria

1. [136-REQ-3.1] THE system SHALL NOT contain the directory
   `agent_fox/spec/validators/` or any of its files after this spec.
2. [136-REQ-3.2] THE system SHALL NOT contain the file
   `agent_fox/spec/verification_checklist.py` after this spec.
3. [136-REQ-3.3] THE system SHALL NOT contain the file
   `agent_fox/spec/ai_validation.py` after this spec.
4. [136-REQ-3.4] WHEN `agent-fox lint-specs` is invoked, THE system SHALL
   use afspec validation (from spec 135) AND NOT reference any deleted
   validator module.

#### Edge Cases

1. [136-REQ-3.E1] IF the `Finding` dataclass from `validators/_helpers.py`
   is still needed by `lint.py` or other modules, THEN THE system SHALL
   extract it to a surviving module before deletion.

### Requirement 4: Rewire Consumer Imports

**User Story:** As a developer, I want all imports updated to the new
locations, so that the codebase compiles cleanly after deletion.

#### Acceptance Criteria

1. [136-REQ-4.1] WHEN `agent_fox/engine/session_lifecycle.py` is loaded,
   THE system SHALL NOT import from `agent_fox.spec.parser` AND SHALL
   import from `agent_fox.spec.types` or `agent_fox.spec.parser_v12`
   instead.
2. [136-REQ-4.2] WHEN `agent_fox/engine/hot_load.py` is loaded, THE system
   SHALL NOT import from `agent_fox.spec.parser` AND SHALL use
   `parser_v12` functions for spec parsing.
3. [136-REQ-4.3] WHEN `agent_fox/engine/engine.py` or
   `agent_fox/engine/dispatch.py` is loaded, THE system SHALL NOT import
   from `agent_fox.spec.parser`.
4. [136-REQ-4.4] WHEN `agent_fox/graph/planner.py`,
   `agent_fox/graph/builder.py`, or `agent_fox/spec/parser_v12.py` is
   loaded, THE system SHALL import `TaskGroupDef`, `SubtaskDef`, and
   `CrossSpecDep` from `agent_fox.spec.types`.

#### Edge Cases

1. [136-REQ-4.E1] IF any import references a deleted module at runtime,
   THEN THE system SHALL raise `ImportError` immediately, making the
   broken reference visible.

### Requirement 5: Remove Stale File References

**User Story:** As a developer, I want all hardcoded markdown file
references removed, so that the codebase only references v1.2 filenames.

#### Acceptance Criteria

1. [136-REQ-5.1] THE system SHALL NOT contain the constant
   `_CORE_SPEC_FILES` referencing `requirements.md`, `design.md`,
   `test_spec.md`, or `tasks.md` in `agent_fox/session/context.py`.
2. [136-REQ-5.2] WHEN `grep -rn "requirements\.md\|design\.md\|test_spec\.md"
   agent_fox/ --include="*.py"` is run (excluding `fix/spec_gen.py`),
   THE system SHALL return zero matches.
3. [136-REQ-5.3] WHEN `grep -rn "EXPECTED_FILES" agent_fox/ --include="*.py"`
   is run (excluding `fix/spec_gen.py`), THE system SHALL return zero
   matches referencing the old five-file list.

#### Edge Cases

1. [136-REQ-5.E1] IF `fix/spec_gen.py` still references old filenames,
   THEN THE system SHALL leave those references intact (explicitly out
   of scope per the non-goals).

### Requirement 6: Clean Test Suite

**User Story:** As a developer, I want the test suite to pass cleanly
after all deletions, confirming no regressions.

#### Acceptance Criteria

1. [136-REQ-6.1] WHEN `make check` is run after all deletions, THE system
   SHALL pass with zero failures AND zero import errors.
2. [136-REQ-6.2] THE system SHALL delete or update any test files that
   exclusively test the deleted modules (e.g., tests for the markdown
   parser, tests for the markdown validators).

#### Edge Cases

1. [136-REQ-6.E1] IF a test file tests both legacy and shared functionality,
   THEN THE system SHALL remove only the legacy tests and preserve the
   shared ones.
