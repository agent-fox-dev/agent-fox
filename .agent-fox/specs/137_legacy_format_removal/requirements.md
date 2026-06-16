# Requirements Document

## Introduction

This spec removes all v1 (markdown-only) spec format support from agent-fox,
leaving only the v1.2 JSON-based format. It extracts shared types into a new
module, deletes legacy modules, rewires consumer imports, and updates
documentation.

## Glossary

- **v1 format**: The original all-markdown spec format using five files
  (`prd.md`, `requirements.md`, `design.md`, `test_spec.md`, `tasks.md`).
- **v1.2 format**: The JSON-based spec format using `prd.md`,
  `requirements.json`, `test_spec.json`, `tasks.json`, and optionally
  `architecture.md`.
- **afspec**: Library from af-core providing Pydantic models, schema
  validation, and rendering for v1.2 specs.
- **TaskGroupDef**: Frozen dataclass representing a parsed task group.
- **SubtaskDef**: Frozen dataclass representing a parsed subtask.
- **CrossSpecDep**: Frozen dataclass representing a cross-spec dependency.
- **Finding**: Dataclass representing a validation finding (file, line, rule,
  message, severity).
- **SpecInfo**: Dataclass carrying spec metadata (name, path, format, etc.).

## Requirements

### Requirement 1: Extract shared types to `spec/types.py`

**User Story:** As a developer, I want shared spec-layer types in a single
canonical module, so that import paths are stable after legacy module deletion.

#### Acceptance Criteria

1. [137-REQ-1.1] WHEN `agent_fox.spec.types` is imported, THE module SHALL
   export `TaskGroupDef`, `SubtaskDef`, and `CrossSpecDep` with identical
   field signatures to those previously in `parser.py`.

2. [137-REQ-1.2] WHEN `agent_fox.spec.types` is imported, THE module SHALL
   export `Finding`, `SEVERITY_ERROR`, `SEVERITY_WARNING`, `SEVERITY_HINT`,
   `compute_exit_code`, and `sort_findings` with identical signatures to those
   previously in `validators/_helpers.py`.

3. [137-REQ-1.3] THE `parser_v12.py` module SHALL import `TaskGroupDef`,
   `SubtaskDef`, and `CrossSpecDep` from `agent_fox.spec.types`.

#### Edge Cases

1. [137-REQ-1.E1] IF a consumer imports from the deleted `agent_fox.spec.parser`
   module, THEN Python SHALL raise `ImportError` or `ModuleNotFoundError`.

### Requirement 2: Delete legacy parser module

**User Story:** As a maintainer, I want dead v1 parsing code removed, so that
the codebase is smaller and easier to navigate.

#### Acceptance Criteria

1. [137-REQ-2.1] WHEN the repository is checked, THE file
   `agent_fox/spec/parser.py` SHALL NOT exist on disk.

2. [137-REQ-2.2] THE `agent_fox` package SHALL be fully importable (no
   `ImportError` from any module) after `parser.py` is deleted.

### Requirement 3: Delete legacy validators directory

**User Story:** As a maintainer, I want the v1-only validation rules removed,
so that validation flows exclusively through `afspec`.

#### Acceptance Criteria

1. [137-REQ-3.1] WHEN the repository is checked, THE directory
   `agent_fox/spec/validators/` SHALL NOT exist.

2. [137-REQ-3.2] THE `lint.py` module SHALL NOT import from
   `agent_fox.spec.validators`.

3. [137-REQ-3.3] THE `lint_specs.py` CLI module SHALL NOT import from
   `agent_fox.spec.validators`.

4. [137-REQ-3.4] THE `hot_load.py` module SHALL NOT import from
   `agent_fox.spec.validators`.

#### Edge Cases

1. [137-REQ-3.E1] IF a consumer imports from `agent_fox.spec.validators`,
   THEN Python SHALL raise `ImportError` or `ModuleNotFoundError`.

### Requirement 4: Delete legacy ai_validation and verification v1 paths

**User Story:** As a maintainer, I want v1-only AI validation and v1 code
paths in the verification checklist removed.

#### Acceptance Criteria

1. [137-REQ-4.1] WHEN the repository is checked, THE file
   `agent_fox/spec/ai_validation.py` SHALL NOT exist.

2. [137-REQ-4.2] THE `verification_checklist.py` module SHALL NOT contain
   any reference to `tasks.md` or `requirements.md` as string literals.

3. [137-REQ-4.3] THE `verification_checklist.py` module SHALL NOT import
   from `agent_fox.spec.parser`.

### Requirement 5: Rewire consumer imports

**User Story:** As a developer, I want all modules to import shared types
from their new canonical location, so that the codebase compiles cleanly.

#### Acceptance Criteria

1. [137-REQ-5.1] THE `graph/builder.py` module SHALL import `TaskGroupDef`
   and `CrossSpecDep` from `agent_fox.spec.types`.

2. [137-REQ-5.2] THE `graph/planner.py` module SHALL NOT import from
   `agent_fox.spec.parser`.

3. [137-REQ-5.3] THE engine modules (`session_lifecycle.py`, `hot_load.py`,
   `engine.py`, `dispatch.py`) SHALL NOT import from `agent_fox.spec.parser`.

4. [137-REQ-5.4] THE `spec/lint.py` module SHALL import `Finding`,
   `compute_exit_code`, and `sort_findings` from `agent_fox.spec.types`.

#### Edge Cases

1. [137-REQ-5.E1] IF any Python module under `agent_fox/` (excluding
   `fix/spec_gen.py`) contains the string `from agent_fox.spec.parser`,
   THEN this spec's acceptance criteria are violated.

### Requirement 6: Remove format-routing and v1 filename references

**User Story:** As a developer, I want format-routing conditionals and v1
filename strings removed, so that the code reflects the single-format reality.

#### Acceptance Criteria

1. [137-REQ-6.1] THE `discovery.py` module SHALL NOT define a
   `SpecFormat.V1_MARKDOWN` enum member or contain the string `V1_MARKDOWN`
   in its source.

2. [137-REQ-6.2] WHEN `discover_specs()` is called, THE function SHALL
   return all valid specs without format filtering.

3. [137-REQ-6.3] THE `session/context.py` module SHALL NOT contain the
   `_CORE_SPEC_FILES` constant.

4. [137-REQ-6.4] WHEN any Python file under `agent_fox/` (excluding
   `fix/spec_gen.py`) is scanned, THE file SHALL NOT contain the strings
   `requirements.md`, `design.md`, or `test_spec.md` as operational
   references (comments and docstrings documenting history are exempt).

#### Edge Cases

1. [137-REQ-6.E1] IF a spec directory lacks `requirements.json`, THEN
   `discover_specs()` SHALL exclude it (it is not a valid v1.2 spec).

### Requirement 7: Clean up test suite

**User Story:** As a developer, I want the test suite to pass cleanly after
legacy module deletion, with no imports from deleted modules.

#### Acceptance Criteria

1. [137-REQ-7.1] WHEN `make test` is run, THE full test suite SHALL pass
   with zero failures.

2. [137-REQ-7.2] WHEN any test file under `tests/` is scanned, THE file
   SHALL NOT contain `from agent_fox.spec.parser import` (the deleted module).

3. [137-REQ-7.3] WHEN any test file under `tests/` is scanned, THE file
   SHALL NOT contain `from agent_fox.spec.validators` (the deleted package).

4. [137-REQ-7.4] WHEN any test file under `tests/` is scanned, THE file
   SHALL NOT contain `from agent_fox.spec.ai_validation import` (the deleted
   module).

#### Edge Cases

1. [137-REQ-7.E1] IF `tests/spec/test_133_v12_parsing.py` imports
   `TaskGroupDef`, THEN it SHALL import from `agent_fox.spec.types`, not
   from `agent_fox.spec.parser`.

### Requirement 8: Update documentation

**User Story:** As a reader of the architecture docs, I want the
documentation to reflect the v1.2-only world without references to
dual-format support or v1 code paths.

#### Acceptance Criteria

1. [137-REQ-8.1] WHEN `docs/architecture/06-spec-format-v12.md` is read,
   THE document SHALL describe v1.2 as the sole format (not a dual-format
   coexistence model).

2. [137-REQ-8.2] WHEN `docs/architecture/01-spec-authoring.md` is read,
   THE document SHALL describe only the v1.2 artifact set (not the v1
   five-file model as an active format).

3. [137-REQ-8.3] WHEN `docs/README.md` is read, THE workflow description
   SHALL reference only v1.2 spec artifacts.

4. [137-REQ-8.4] WHEN `docs/architecture.md` is read, THE spec artifacts
   table SHALL list only v1.2 files.
