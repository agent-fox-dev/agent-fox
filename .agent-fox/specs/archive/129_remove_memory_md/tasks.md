# Implementation Plan: Remove docs/memory.md

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Small subtractive spec: write tests that assert the file and references are
gone, then delete everything in one group. Three task groups total including
wiring verification.

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/test_remove_memory_md.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file `tests/unit/test_remove_memory_md.py`
    - TS-129-1: file deleted from repo
    - TS-129-2: init does not create memory.md (integration, uses tmp_git_repo)
    - TS-129-3: _DOCS_MEMORY_CONTENT removed from init_project.py
    - TS-129-4: template has no memory.md references
    - TS-129-5: CLAUDE.md has no memory.md references
    - TS-129-6: AGENTS.md has no memory.md references
    - TS-129-7: agent profile has no memory.md references
    - TS-129-8: af-fix skill has no memory.md references (both locations)
    - TS-129-9: no init tests for memory.md
    - _Test Spec: TS-129-1 through TS-129-9_

  - [x] 1.2 Create property and smoke tests
    - TS-129-P1: no dangling references in any tracked file
    - TS-129-P2: init_project does not create memory.md
    - TS-129-SMOKE-1: full init without memory.md
    - _Test Spec: TS-129-P1, TS-129-P2, TS-129-SMOKE-1_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) -- no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`

- [x] 2. Remove docs/memory.md and all references
  - [x] 2.1 Delete the file
    - `git rm docs/memory.md`
    - _Requirements: 129-REQ-1.1_

  - [x] 2.2 Remove from init_project.py
    - Delete `_DOCS_MEMORY_CONTENT` constant
    - Remove memory.md creation logic from `_ensure_seed_files()`
    - If `_ensure_seed_files()` has no remaining work, remove the function
      and its call site
    - _Requirements: 129-REQ-2.1, 129-REQ-2.2_

  - [x] 2.3 Remove from agent instruction templates and project files
    - Edit `agent_fox/_templates/agents_md.md`: remove step 2 reference to
      `docs/memory.md` and session completion commit instruction
    - Edit `CLAUDE.md`: remove same 2 references
    - Edit `AGENTS.md`: remove same 2 references
    - Edit `agent_fox/_templates/profiles/agent.md`: remove "DO NOT READ
      docs/memory.md" line
    - _Requirements: 129-REQ-3.1, 129-REQ-3.2, 129-REQ-3.3, 129-REQ-3.4_

  - [x] 2.4 Remove from skill templates
    - Edit `agent_fox/_templates/skills/af-fix`: remove `docs/memory.md` line
    - Edit `.claude/skills/af-fix/SKILL.md`: remove same line
    - _Requirements: 129-REQ-4.1, 129-REQ-4.2_

  - [x] 2.5 Remove from tests
    - Delete `test_init_creates_docs_memory_md` method from
      `tests/integration/test_init.py`
    - Delete `test_reinit_preserves_existing_seed_files` method from same file
    - _Requirements: 129-REQ-5.1_

  - [x] 2.V Verify task group 2
    - [x] All spec tests TS-129-1 through TS-129-9, TS-129-P1, TS-129-P2,
          TS-129-SMOKE-1 pass
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`
    - [x] 129-REQ-1.1 through 129-REQ-6.1 acceptance criteria met

- [x] 3. Wiring verification

  - [x] 3.1 Trace every execution path from design.md end-to-end
    - Path 1: init_cmd -> init_project -> _ensure_seed_files (verify memory.md
      is not created)
    - _Requirements: all_

  - [x] 3.2 Verify no dangling references
    - Run the TS-129-P1 property test (grep all tracked files)
    - Confirm no file outside audits/specs references docs/memory.md
    - _Requirements: 129-REQ-6.1_

  - [x] 3.3 Run the integration smoke tests
    - TS-129-SMOKE-1 passes
    - _Test Spec: TS-129-SMOKE-1_

  - [x] 3.4 Stub / dead-code audit
    - Verify no orphaned imports or dead branches from the removal
    - Check if `_ensure_seed_files` is now empty and can be removed
    - _Requirements: all_

  - [x] 3.5 Cross-spec entry point verification
    - Verify `init_project()` still works end-to-end without memory.md creation
    - _Requirements: all_

  - [x] 3.V Verify wiring group
    - [x] All smoke tests pass
    - [x] No unjustified stubs remain in touched files
    - [x] All execution paths from design.md are live
    - [x] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 129-REQ-1.1 | TS-129-1 | 2.1 | `test_remove_memory_md.py::test_file_deleted` |
| 129-REQ-2.1 | TS-129-2, TS-129-P2 | 2.2 | `test_remove_memory_md.py::test_init_no_memory_md` |
| 129-REQ-2.2 | TS-129-3 | 2.2 | `test_remove_memory_md.py::test_constant_removed` |
| 129-REQ-3.1 | TS-129-4 | 2.3 | `test_remove_memory_md.py::test_template_clean` |
| 129-REQ-3.2 | TS-129-5 | 2.3 | `test_remove_memory_md.py::test_claude_md_clean` |
| 129-REQ-3.3 | TS-129-6 | 2.3 | `test_remove_memory_md.py::test_agents_md_clean` |
| 129-REQ-3.4 | TS-129-7 | 2.3 | `test_remove_memory_md.py::test_profile_clean` |
| 129-REQ-4.1 | TS-129-8 | 2.4 | `test_remove_memory_md.py::test_skill_template_clean` |
| 129-REQ-4.2 | TS-129-8 | 2.4 | `test_remove_memory_md.py::test_skill_installed_clean` |
| 129-REQ-5.1 | TS-129-9 | 2.5 | `test_remove_memory_md.py::test_init_tests_removed` |
| 129-REQ-6.1 | TS-129-P1 | 2.1-2.5 | `test_remove_memory_md.py::test_no_dangling_refs` |

## Notes

- This is a purely subtractive spec. No new behavior is introduced.
- The `docs/audits/audit2.md` file references `memory.md` in historical
  context and is intentionally left unchanged.
- All changes can be done in a single task group (group 2) since they are
  independent deletions with no ordering constraints.
