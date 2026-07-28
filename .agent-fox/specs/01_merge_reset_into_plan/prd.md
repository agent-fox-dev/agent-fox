---
spec_id: '01'
spec_name: merge_reset_into_plan
title: Merge Reset Into Plan
status: draft
created_at: '2026-07-28T07:26:01.434312+00:00'
updated_at: '2026-07-28T07:27:16.325237+00:00'
owner: ''
source: docs/proposals/prd1.md
schema_version: 1
---
# Merge Reset Functionality into Plan Command

## Summary

Consolidate the `af reset` command into `af plan` by adding `--clear`,
`--reset`, and `--reset-hard` flags, then remove the standalone `af reset`
command. This simplifies the CLI surface by grouping all plan-state management
under a single command.

## Goals

1. **Reduce top-level CLI commands by 1** — the `af reset` command is removed entirely; all plan-state mutations are accessible via `af plan`.
2. **100% behavioral parity** — `af plan --reset` and `af plan --reset-hard` produce identical results to the removed `af reset` and `af reset --hard` commands respectively, verified by the existing reset test suite passing without modification.
3. **≥90% test coverage on all new flag handlers** — `--clear`, `--reset`, `--reset-hard`, and `--yes` achieve at least 90% line coverage as measured by `pytest --cov`.

## Non-Goals

- **No deprecation period for `af reset`** — the command is removed immediately; no runtime redirect message or shim is provided (internal/personal project, breaking change documented in CLI reference).
- **No `--reset-hard --spec` support** — partial hard reset scoped to a single spec is explicitly out of scope, matching current `af reset` behavior.
- **No worktree removal, branch cleanup, or git rollback for `--clear`** — `--clear` is a forward-only operation; destructive cleanup remains the responsibility of `--reset` / `--reset-hard`.
- **No changes to `agentfox.engine.reset` library code** — only the CLI layer (`reset.py` module, `app.py` registration) and `run_plan()` API are modified; reset logic is reused as-is.
- **No interaction with `merge_strategy` spec** — that spec concerns worktree merge strategies during `af code` execution and is entirely unrelated to plan-state reset/clear operations.
- **No formal ownership or migration communication** — this is a personal/internal project; the PRD and updated CLI reference are the sole communication artifacts.

## Background

The `af` CLI currently has two separate entry points for plan-related operations: `af plan` (build, verify, dry-run) and `af reset` (soft/hard reset of task state). Reset operations are logically plan-state mutations — they change node statuses, clean worktrees, and optionally roll back code — yet they live in a separate top-level command. This split reduces discoverability and increases the surface area of the CLI unnecessarily.

The `af plan` command already owns plan building, verification (`--verify`), and dry-run analysis. Merging reset functionality under `af plan` follows the principle that a single command owns a single domain (plan state), making the CLI more intuitive for users and reducing the number of concepts they must learn.

This change is purely a CLI restructuring. The underlying reset logic in `agentfox.engine.reset` is preserved and called directly by the updated `af plan` handler.

## Tech Stack

- **Language:** Python 3.12+
- **CLI framework:** Click
- **Knowledge store:** DuckDB
- **Core library:** agentfox (engine/reset module, graph/planner module)
- **Test framework:** pytest with `click.testing.CliRunner` and `unittest.mock` — matches existing patterns in the `af` package (see `test_plan_verify.py` as the canonical example)

## Motivation

The `af plan` command already owns plan building, verification (`--verify`),
and dry-run analysis. Reset operations are logically plan-state mutations and
belong under the same command. A single `af plan` entry point for all
plan-related operations is more discoverable and reduces the number of
top-level commands.

## Functional Requirements

### FR-1: `--clear` flag on `af plan`

Add a `--clear` flag to `af plan` that forces **all** nodes in the database
to `completed` status. This provides a clean slate for subsequent coding
sessions without performing the full cleanup of a hard reset (no worktree
removal, no branch cleanup, no code rollback).

**Behavior:**
- Opens the knowledge store (read-write).
- Loads the persisted plan from DuckDB via `load_plan()`.
- **If `load_plan()` returns `None`** (no plan exists in the database): exits with code 1 and the error message `No plan found in database. Run 'agent-fox plan' first.` — consistent with how `--verify` and `--reset` handle missing plans.
- Sets every node's status to `completed` in the `plan_nodes` table.
- Clears session-scoped tables: `runs`, `session_outcomes`, `review_findings`, `drift_findings` — confirmed stable at `agentfox/engine/reset.py` lines 37–42 (`_SESSION_TABLES_ALL`).
- Does NOT perform worktree cleanup, branch cleanup, knowledge compaction,
  or git rollback.
- Composes with `--spec NAME`: when `--spec` is provided alongside `--clear`,
  only nodes belonging to the named spec are set to `completed`.
- Does NOT require confirmation (it is a non-destructive forward operation
  that marks work as done — it does not delete artifacts or roll back code).
- Reports the number of nodes cleared.

**JSON output:** When `--json` is active, emits:
```json
{"cleared": 5, "spec": null}
```
or with `--spec`:
```json
{"cleared": 3, "spec": "04_personal_org"}
```

### FR-2: `--reset` flag on `af plan`

Add a `--reset` flag to `af plan` that performs the equivalent of the current
`af reset` command (soft reset). This resets failed, blocked, and in-progress
tasks to pending, cleans up worktrees and branches.

**Behavior when no plan exists:** exits with code 1 and the error message
`No plan found in database. Run 'agent-fox plan' first.` — consistent with
`--clear` and `--verify`.

**Supported combinations:**
- `af plan --reset` — reset all incomplete tasks (with confirmation).
- `af plan --reset TASK_ID` — reset a single task and cascade-unblock
  downstream dependents (no confirmation).
- `af plan --reset --spec NAME` — reset all tasks for a single spec
  (with confirmation).

**TASK_ID handling:** `--reset` accepts an optional trailing argument. When
present, it acts as the target task ID. Implement this as an optional Click
argument that is only valid when `--reset` or `--reset-hard` is active.

**Confirmation:** Operations that affect multiple tasks (no TASK_ID, or
`--spec`) require confirmation. Skip with `--yes / -y`. Single-task reset
(with TASK_ID) does not require confirmation.

**JSON output:** When `--json` is active, emits the same structure as the
current reset result:
```json
{
  "reset_tasks": ["spec:0", "spec:1"],
  "unblocked_tasks": [],
  "cleaned_worktrees": [],
  "cleaned_branches": []
}
```

### FR-3: `--reset-hard` flag on `af plan`

Add a `--reset-hard` flag to `af plan` that performs the equivalent of the
current `af reset --hard` command (hard reset with code rollback).

**Behavior when no plan exists:** exits with code 1 and the error message
`No plan found in database. Run 'agent-fox plan' first.`

**Supported combinations:**
- `af plan --reset-hard` — full hard reset: all tasks to pending, worktree/
  branch cleanup, code rollback (with confirmation).
- `af plan --reset-hard TASK_ID` — partial hard reset: target task + cascaded
  tasks, code rollback to pre-task state (with confirmation).

**Note:** `--reset-hard --spec` is NOT supported (matches current `af reset`
behavior where `--spec` and `--hard` are mutually exclusive).

**Confirmation:** Always requires confirmation. Skip with `--yes / -y`.

**JSON output:** When `--json` is active, emits:
```json
{
  "reset_tasks": ["spec:0", "spec:1"],
  "cleaned_worktrees": [],
  "cleaned_branches": [],
  "compaction": [0, 0],
  "rollback_sha": "abc123"
}
```

### FR-4: Remove `af reset` command

Remove the `af reset` command from the CLI entirely:
- Remove the `reset_cmd` registration from `app.py`.
- Delete the `packages/af/af/reset.py` CLI module.
- The `agentfox.engine.reset` module (library code) is NOT removed — it
  contains the reset logic that `af plan` will now call directly.
- **No runtime redirect message** is emitted when users invoke the removed command — the standard Click "No such command" error is sufficient for this internal project. The breaking change is documented in `docs/cli-reference.md` and the release notes.

### FR-5: `--yes / -y` flag on `af plan`

Add a `--yes / -y` flag to `af plan` that skips confirmation prompts. This
flag is only meaningful when `--reset` or `--reset-hard` is active. When used
without those flags, it is silently ignored.

### FR-6: Mutual exclusivity

The following flags are mutually exclusive mode selectors:
- `--dry-run`
- `--verify`
- `--clear`
- `--reset`
- `--reset-hard`

If more than one is provided, exit with code 1 and an error message listing
the conflicting flags.

`--spec` composes with: `--clear`, `--reset`, and the normal plan build.
`--spec` does NOT compose with `--reset-hard` (mutually exclusive).

`--fast` only applies to the normal plan build and `--dry-run`/`--verify`
modes. It is silently ignored by `--clear`, `--reset`, and `--reset-hard`.

### FR-7: Update `run_plan()` API

Extend the `run_plan()` function in `agentfox.graph.planner` to support the
new modes. Add parameters:
- `clear: bool = False`
- `reset: bool = False`
- `reset_hard: bool = False`
- `target: str | None = None` (task ID for single-task reset)

This keeps `run_plan()` as the single programmatic entry point for all
plan-related operations, matching the CLI consolidation.

### FR-8: Update documentation

Update the following documentation to reflect the CLI changes:
- `docs/cli-reference.md` — add `--clear`, `--reset`, `--reset-hard`,
  `--yes` to the plan command section; remove the `reset` command section;
  update the quick reference table.
- `README.md` — update quick start section if it references `af reset`.

## Non-Functional Requirements

- **Backward compatibility:** The `af reset` command is removed. No
  deprecation period. Users must use `af plan --reset` instead. The breaking
  change is communicated via updated CLI reference and release notes only
  (internal/personal project).
- **Exit codes:** All new flags follow the existing pattern: `0` success,
  `1` error. Missing plan always exits with code 1 (consistent with `--verify`
  and `--reset` behavior).
- **Daemon guard:** `--clear` and `--reset`/`--reset-hard` must respect the
  nightshift daemon PID guard (refuse to run while daemon is active), same
  as the existing plan command behavior.
- **Test coverage:** ≥90% line coverage on all new flag handlers, measured
  via `pytest --cov`. Tests use `click.testing.CliRunner` and `unittest.mock`,
  following the pattern in `test_plan_verify.py`.

## Design Decisions

1. **`--clear` sets ALL nodes to completed (not just mismatched ones).**
   This is simpler and provides a clean blanket reset. The user can scope
   it with `--spec` if they only want to clear a specific spec.

2. **Full interface preserved for `--reset`/`--reset-hard`.** TASK_ID,
   `--spec`, `--yes` all carry over from the original `af reset` command
   to maintain feature parity.

3. **Confirmation prompts only for reset operations.** `--clear` does not
   require confirmation because it is a forward operation (marking work
   complete). `--reset` and `--reset-hard` require confirmation because
   they are backward operations (undoing progress).

4. **`--reset-hard --spec` remains unsupported.** This matches the current
   mutual exclusivity in `af reset` and avoids complexity around partial
   code rollback scoped to a single spec.

5. **JSON output for all new flags.** All three new modes produce structured
   JSON output when `--json` is active, consistent with existing plan modes.

6. **Same exit code pattern.** All new flags use `0` for success and `1` for
   error, matching existing plan and reset behavior. Missing-plan edge case
   always returns exit code 1 with an explicit error message.

7. **`run_plan()` API extended.** The programmatic API gains the same modes
   as the CLI, maintaining the pattern that `run_plan()` is the single entry
   point for non-CLI callers.

8. **No deprecation shim for `af reset`.** This is a personal/internal project;
   the standard Click "No such command" error is sufficient. The breaking
   change is documented in the CLI reference and release notes.

9. **Session table names are stable.** The four tables cleared by `--clear`
   (`runs`, `session_outcomes`, `review_findings`, `drift_findings`) are
   confirmed against `agentfox/engine/reset.py` (`_SESSION_TABLES_ALL`,
   lines 37–42) and are not managed by any other spec.

## Verified External API

### `agentfox.engine.reset` (internal library)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `reset_all` | `agentfox.engine.reset` | `(worktrees_dir: Path, repo_path: Path, db_conn=None) -> ResetResult` | |
| `reset_task` | `agentfox.engine.reset` | `(task_id: str, worktrees_dir: Path, repo_path: Path, db_conn=None) -> ResetResult` | |
| `reset_spec` | `agentfox.engine.reset` | `(spec_name: str, worktrees_dir: Path, repo_path: Path, db_conn=None, specs_dir=None) -> ResetResult` | |
| `hard_reset_all` | `agentfox.engine.reset` | `(worktrees_dir: Path, repo_path: Path, memory_path: Path, db_conn=None, integration_branch='main') -> HardResetResult` | |
| `hard_reset_task` | `agentfox.engine.reset` | `(task_id: str, worktrees_dir: Path, repo_path: Path, memory_path: Path, db_conn=None, integration_branch='main') -> HardResetResult` | |
| `run_reset` | `agentfox.engine.reset` | `(target=None, config=None, *, soft=True, hard=False, spec=None, ...) -> ResetResult \| HardResetResult` | Convenience wrapper |
| `ResetResult` | `agentfox.engine.reset` | `@dataclass(frozen=True)` | Fields: reset_tasks, unblocked_tasks, cleaned_worktrees, cleaned_branches, skipped_completed |
| `HardResetResult` | `agentfox.engine.reset` | `@dataclass(frozen=True)` | Fields: reset_tasks, cleaned_worktrees, cleaned_branches, compaction, rollback_sha |
| `_RESETTABLE_STATUSES` | `agentfox.engine.reset` | `frozenset({"failed", "blocked", "in_progress"})` | |
| `_SESSION_TABLES_ALL` | `agentfox.engine.reset` | `('runs', 'session_outcomes', 'review_findings', 'drift_findings')` | Lines 37–42; used by `--clear` to wipe session state |
| `_load_state_or_raise` | `agentfox.engine.reset` | `(db_conn) -> ExecutionState` | |

### `agentfox.engine.state` (internal library)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `persist_node_status` | `agentfox.engine.state` | `(conn, node_id, status, blocked_reason=None)` | Updates plan_nodes table |

### `agentfox.graph.persistence` (internal library)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `load_plan` | `agentfox.graph.persistence` | `(conn) -> TaskGraph \| None` | Returns None when no plan exists; `--clear`, `--reset`, `--reset-hard` all exit code 1 on None |
| `save_plan` | `agentfox.graph.persistence` | `(graph, conn) -> None` | |

### `agentfox.knowledge.db` (internal library)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `open_knowledge_store` | `agentfox.knowledge.db` | `(config, read_only=False) -> KnowledgeStore` | |

## Source

source: "docs/proposals/prd1.md"
