# Design Document: Remove docs/memory.md

## Overview

Purely subtractive change: delete one file, remove references from 8 other
files, delete 2 tests. No new code, no new modules, no behavioral changes.

## Architecture

No architectural changes. The knowledge store (`session_summaries` +
`FoxKnowledgeProvider`) continues to provide session context — this spec
removes only the legacy manual mechanism.

## Execution Paths

### Path 1: agent-fox init (after change)

1. `cli/init.py: init_cmd()` — invokes `init_project()`
2. `workspace/init_project.py: init_project()` — calls `_ensure_seed_files()`
3. `workspace/init_project.py: _ensure_seed_files()` — no longer creates
   `docs/memory.md` (function may be removed entirely if it has no other work)
4. Side effect: `docs/` directory may still be created for other purposes, but
   `docs/memory.md` is not written

## Components and Interfaces

### Files to modify

| File | Change |
|------|--------|
| `docs/memory.md` | Delete (git rm) |
| `agent_fox/workspace/init_project.py` | Remove `_DOCS_MEMORY_CONTENT`, remove memory.md logic from `_ensure_seed_files()` |
| `agent_fox/_templates/agents_md.md` | Remove 2 references to `docs/memory.md` |
| `CLAUDE.md` | Remove 2 references to `docs/memory.md` |
| `AGENTS.md` | Remove 2 references to `docs/memory.md` |
| `agent_fox/_templates/skills/af-fix` | Remove `docs/memory.md` from file list |
| `.claude/skills/af-fix/SKILL.md` | Remove `docs/memory.md` from file list |
| `agent_fox/_templates/profiles/agent.md` | Remove "DO NOT READ docs/memory.md" line |
| `tests/integration/test_init.py` | Delete 2 test methods |

## Correctness Properties

### Property 1: No references in source or templates

*For any* git-tracked `.py` or `.md` file outside `docs/audits/` and
`.agent-fox/specs/`, the file SHALL NOT contain the string `docs/memory.md`.

**Validates: 129-REQ-6.1**

### Property 2: init does not create memory.md

*For any* invocation of `init_project()` on a fresh project directory, the
path `docs/memory.md` SHALL NOT exist after the call completes.

**Validates: 129-REQ-2.1**

## Error Handling

| Error Condition | Behavior | Requirement |
|----------------|----------|-------------|
| N/A | This spec is purely subtractive; no error paths introduced | — |

## Technology Stack

No new dependencies.

## Definition of Done

A task group is complete when ALL of the following are true:

1. All subtasks within the group are checked off (`[x]`)
2. All spec tests (`test_spec.md` entries) for the task group pass
3. All property tests for the task group pass
4. All previously passing tests still pass (no regressions)
5. No linter warnings or errors introduced
6. Code is committed on a feature branch and merged into `develop`
7. Feature branch is merged back to `develop`
8. `tasks.md` checkboxes are updated to reflect completion

## Testing Strategy

- **Property test** greps all tracked files to verify no dangling references.
- **Unit test** runs `init_project()` and verifies `docs/memory.md` is not
  created.
- **Integration smoke test** runs `agent-fox init` via CliRunner and verifies
  the file is absent.
