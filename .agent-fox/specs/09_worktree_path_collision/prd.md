---
spec_id: 09
spec_name: worktree_path_collision
title: Worktree Path Collision
status: draft
created_at: '2026-06-25T08:55:59.911099+00:00'
updated_at: '2026-06-25T09:00:06.117245+00:00'
owner: candlekeep
source: https://github.com/agent-fox-dev/agent-fox/issues/628
schema_version: 1
---
# Worktree Path Collision Fix

## Intent

Ensure reliable concurrent execution of multi-mode reviewer nodes by scoping worktree paths and branch names to the full node identity, eliminating the path collision that causes `git worktree add` failures when two nodes share the same `(spec_name, task_group)` but differ in role or mode.

## Background

Worktrees provide filesystem isolation per coding session so that concurrent nodes can edit files without interfering with each other. The worktree-per-node model was introduced alongside the parallel dispatcher (spec 04). When a node begins work, it creates a dedicated worktree at a deterministic path derived from its identity; when work completes, the worktree is destroyed within the same session lifecycle.

This collision was discovered when multi-mode reviewer nodes (`pre-review` and `drift-review`) were first dispatched concurrently within the same task group. No prior fix attempts have been made — this is the first time the scenario has been exercised in practice.

## Goals

- **Zero exit-code-128 failures** during concurrent reviewer node dispatch caused by worktree path collisions.
- **All existing unit and integration tests pass** with no regressions in coder node path structure.
- **Concurrent-dispatch test (test 4) added to CI** as a blocking required check in the existing pytest PR pipeline.

## Problem

When two nodes share the same `(spec_name, task_group)` but differ only in role/mode (e.g., `reviewer:pre-review` vs `reviewer:drift-review`), they are dispatched concurrently but both attempt to create a worktree at the **same** path. The second `git worktree add` fails with exit code 128 because the directory already exists.

## Root Cause

In `packages/agentfox/agentfox/workspace/worktree.py`, `create_worktree()` constructs the worktree path using only `spec_name` and `task_group`:

```python
worktree_path = worktrees_root / spec_name / str(task_group)
```

The branch name defaults to:

```python
branch_name = branch_name or f"feature/{spec_name}/{task_group}"
```

Neither includes the role (archetype) or mode, so distinct nodes like `08_spec_generation_improvement:0:reviewer:drift-review` and `08_spec_generation_improvement:0:reviewer:pre-review` resolve to the same worktree path and branch name.

## Impact

- Reviewer nodes at the same group level fail workspace setup and enter a retry loop.
- Retries eventually exhaust (3 attempts) and the node fails, skipping that review pass.
- The retry backoff adds unnecessary latency.

## Solution

Include role and mode in the worktree path and branch name **when mode is present**. Nodes without a mode (plain coders, single-mode archetypes) keep their current path structure unchanged.

### Worktree Path

```
# Nodes without mode (unchanged):
.agent-fox/worktrees/{spec_name}/{task_group}

# Nodes with mode:
.agent-fox/worktrees/{spec_name}/{task_group}/{role}/{mode}
# Example: .agent-fox/worktrees/08_spec_generation_improvement/0/reviewer/drift-review
```

### Branch Name

```
# Nodes without mode (unchanged):
feature/{spec_name}/{task_group}

# Nodes with mode:
feature/{spec_name}/{task_group}/{role}/{mode}
# Example: feature/08_spec_generation_improvement/0/reviewer/drift-review
```

### Asymmetric Argument Handling

The following rules govern all combinations of `role` and `mode` arguments:

| `role`      | `mode`      | Effective path level | Notes |
|-------------|-------------|----------------------|-------|
| `None`      | `None`      | 2-level              | Default — unchanged behaviour |
| `""` (empty)| `None`      | 2-level              | Empty string treated as `None` |
| `None`      | `""` (empty)| 2-level              | Empty string treated as `None` |
| `""` (empty)| `""` (empty)| 2-level              | Empty string treated as `None` |
| set         | `None`/`""` | 2-level              | Role is silently ignored; fall back to 2-level path |
| `None`/`""` | set         | 4-level              | `role` defaults to `"unknown"`; WARNING logged (see below) |
| set         | set         | 4-level              | Normal mode-bearing path |

**Empty-string sentinel**: `self._mode` on `NodeSessionRunner` may be an empty string (`""`) rather than `None` to indicate "no mode". Both `None` and `""` are treated identically throughout path derivation. The condition used internally is:

```python
effective_mode = mode or None   # normalises "" → None
effective_role = role or None   # normalises "" → None
```

**Role provided, mode absent**: When `role` is set but `effective_mode` is `None`, role is silently ignored and the 2-level path is used. This is not logged because it is a valid configuration (e.g., a coder archetype that happens to have a role label but no mode).

**Mode set, role absent**: When `effective_mode` is set but `effective_role` is `None`, a **WARNING-level log message** is emitted to surface the likely graph misconfiguration, and `role` is substituted with `"unknown"` to avoid a malformed path segment. Example log message:

```
WARNING worktree: mode='drift-review' was provided but role is None/empty for spec='08_spec_generation_improvement' task_group=0 — defaulting role to 'unknown'. Check graph config.
```

### Updated `create_worktree()` Signature

`role` and `mode` are added as separate optional keyword arguments:

```python
async def create_worktree(
    repo_root: Path,
    spec_name: str,
    task_group: int,
    base_branch: str,
    branch_name: str | None = None,
    role: str | None = None,
    mode: str | None = None,
) -> WorkspaceInfo:
    ...
```

- When both `role` and `mode` resolve to `None` after empty-string normalisation, the path and branch name are identical to the pre-fix behaviour.
- When `mode` resolves to a non-empty value, both `role` and `mode` are appended to the path and branch name.

### Input Validation and Sanitization

No additional branch-name sanitization is required. `spec_name` is already validated in `create_worktree()` with:

```python
re.fullmatch(r'[a-zA-Z0-9_-]+', spec_name)
```

`role` and `mode` values are defined in the graph config, not sourced from user input, and are guaranteed to contain only safe characters. No runtime sanitization step is needed.

The deployment environment is Linux only; maximum OS path length (4096 characters) is not a practical concern for the path depths introduced by this fix.

### `WorkspaceInfo` Dataclass Changes

Two new optional fields are added:

```python
@dataclass(frozen=True)
class WorkspaceInfo:
    path: Path                  # existing — full computed worktree path (unchanged)
    branch: str                 # existing
    spec_name: str              # existing
    task_group: int             # existing
    role: str | None = None     # NEW — role/archetype of the node (normalised value)
    mode: str | None = None     # NEW — mode of the node (normalised value)
```

`role` and `mode` default to `None` for backward compatibility with all existing callers that do not supply them. `WorkspaceInfo` is an in-memory-only object — it is not serialized to disk, passed over a message queue, or logged as structured data — so the new fields carry no serialization compatibility risk.

### `destroy_worktree()` Changes

No logic changes are required. `WorkspaceInfo` already stores the full computed worktree path in its `path` field, and `destroy_worktree()` uses `workspace.path` directly. It does not re-derive the path from `spec_name`/`task_group`/`role`/`mode`, so the deeper path structure is handled transparently.

### Stale-Worktree Cleanup

The existing stale-cleanup logic is depth-agnostic and requires no modification:

- The pre-creation stale check (`if worktree_path.exists(): git worktree remove --force <path>`) operates on the leaf path regardless of depth.
- `_cleanup_empty_ancestors` walks upward from the leaf path to the worktrees root, removing empty directories. This traversal is correct for both the 2-level (`{spec_name}/{task_group}`) and 4-level (`{spec_name}/{task_group}/{role}/{mode}`) structures.

### Threading

The role (archetype) and mode are already available in `NodeSessionRunner` as `self._archetype` and `self._mode`. They need to be passed through `_setup_workspace()` → `create_worktree()`. `self._mode` may be `None` or an empty string; both are normalised to `None` inside `create_worktree()`.

## Files Involved

- `packages/agentfox/agentfox/workspace/worktree.py` — `create_worktree()` signature update (add `role`, `mode` kwargs); empty-string normalisation logic; path/branch derivation logic; WARNING log when mode is set but role is absent; `WorkspaceInfo` dataclass (add `role: str | None = None`, `mode: str | None = None` fields)
- `packages/agentfox/agentfox/engine/session_lifecycle.py` — `NodeSessionRunner._setup_workspace()` must pass `role=self._archetype` and `mode=self._mode` to `create_worktree()`
- `packages/agentfox/agentfox/workspace/__init__.py` — re-exports; `WorkspaceInfo` re-export remains backward-compatible because new fields have defaults

## Non-Goals

- **No change to `parse_node_id()`**: Mode is already available via the task graph node (`node.mode`) and is passed as a separate parameter to `NodeSessionRunner`. The node_id parser does not need to extract it.
- **No concurrency lock in the dispatcher**: The fix eliminates the path collision entirely, so a per-path lock is unnecessary.
- **No path change for coder nodes**: Nodes without a mode keep the existing 2-level path (`{spec_name}/{task_group}`).
- **No branch-name sanitization**: Inputs are controlled and validated upstream; no additional sanitization is needed.
- **No OS path-length guards**: The deployment target is Linux only; path length is not a concern.

## Design Decisions

1. **Role AND mode in branch name**: Both role and mode are included in the branch name (`feature/{spec}/{group}/{role}/{mode}`) for explicitness and to prevent hypothetical collisions between different roles sharing a mode name.
2. **Path extension only when mode is set**: Adding role/mode to the path for every node would be a gratuitous breaking change. Mode-bearing nodes are the only ones that can collide, so they are the only ones that get deeper paths.
3. **Role-present, mode-absent → 2-level fallback (silent)**: When role is provided without a mode, the role is ignored and the 2-level path is used silently. This is a valid configuration (e.g., a coder archetype with a role label but no mode) and does not indicate a misconfiguration.
4. **Mode-present, role-absent → WARNING + `'unknown'` default**: A WARNING is logged to surface likely graph configuration bugs while still allowing the node to proceed. Silent failure would mask misconfiguration.
5. **Empty string normalised to `None`**: `self._mode` (and `self._role`) may be `""` as a sentinel for "not set". Both `None` and `""` are normalised at the entry point of `create_worktree()` so that all downstream logic uses only `None` or a non-empty string.
6. **`_cleanup_empty_ancestors` unchanged**: The existing cleanup function already walks upward from the worktree path to the worktrees root, removing empty directories. Deeper paths work correctly with no changes.
7. **`WorkspaceInfo` new fields default to `None`**: Backward compatibility with all existing callers is preserved; no call sites outside `NodeSessionRunner._setup_workspace()` require updates.
8. **`destroy_worktree()` unchanged**: Because `WorkspaceInfo.path` stores the full computed path, `destroy_worktree()` requires no logic changes to handle deeper paths.
9. **`WorkspaceInfo` is in-memory only**: The dataclass is not serialized or persisted, so the new fields carry no migration or schema-compatibility risk.

## Test Strategy

The following test scenarios must be implemented using pytest. Tests 1–3 are standard unit tests; **test 4 must be added to the existing pytest job in the PR pipeline as a blocking required check** so that a future regression would block a PR automatically.

1. **Path/branch derivation — without mode**: Unit test `create_worktree()` called without `role` or `mode` (both `None`), verifying the resulting path is `{worktrees_root}/{spec_name}/{task_group}` and the branch name is `feature/{spec_name}/{task_group}`.
2. **Path/branch derivation — with mode**: Unit test `create_worktree()` called with `role="reviewer"` and `mode="drift-review"`, verifying the resulting path is `{worktrees_root}/{spec_name}/{task_group}/reviewer/drift-review` and the branch name is `feature/{spec_name}/{task_group}/reviewer/drift-review`.
3. **Coder node regression**: Confirm that nodes without a mode produce paths identical to the pre-fix behaviour, ensuring no regressions in coder node path structure.
4. **Concurrent distinct paths (CI required check — PR pipeline blocking)**: Unit test that two concurrent `create_worktree()` calls with the same `(spec_name, task_group)` but different `(role, mode)` pairs — specifically `("reviewer", "pre-review")` and `("reviewer", "drift-review")` — produce distinct, non-colliding paths, simulating the real-world failure scenario.
5. **Empty-string mode treated as None**: Unit test `create_worktree()` called with `mode=""` (and `role=""`) verifying the resulting path is the 2-level `{worktrees_root}/{spec_name}/{task_group}`, identical to the all-None case.
6. **Role-present, mode-absent → 2-level fallback**: Unit test `create_worktree()` called with `role="reviewer"` and `mode=None` verifying the resulting path is the 2-level path (role is ignored).
7. **Mode-present, role-absent → WARNING + `'unknown'` fallback**: Unit test `create_worktree()` called with `role=None` and `mode="drift-review"` verifying: (a) the resulting path uses `"unknown"` as the role segment, and (b) a WARNING-level log message is emitted containing the relevant context fields.

## Acceptance Criteria / Definition of Done

The fix is considered complete when all of the following are true:

- [ ] `create_worktree()` accepts `role` and `mode` keyword arguments and normalises empty strings to `None`.
- [ ] Nodes without a mode produce a 2-level worktree path and branch name identical to pre-fix behaviour.
- [ ] Nodes with a mode produce a 4-level worktree path and branch name including role and mode.
- [ ] When `mode` is set but `role` is absent/empty, a WARNING is logged and `role` is substituted with `"unknown"`.
- [ ] When `role` is set but `mode` is absent/empty, the 2-level path is used silently (role is ignored).
- [ ] `WorkspaceInfo` exposes `role` and `mode` fields (defaulting to `None`), and all existing callers continue to work without modification.
- [ ] `NodeSessionRunner._setup_workspace()` passes `role=self._archetype` and `mode=self._mode` to `create_worktree()`.
- [ ] All seven tests described in the Test Strategy section pass.
- [ ] Test 4 is registered as a blocking required check in the PR pytest pipeline and a failing run of test 4 blocks PR merge.
- [ ] No existing unit or integration tests regress.
- [ ] A manual or automated concurrent-dispatch run of `reviewer:pre-review` and `reviewer:drift-review` in the same task group completes without exit-code-128 errors.

## Rollback and Migration

No migration is needed. Worktrees are ephemeral artifacts created and destroyed within a single session lifecycle. Any stale worktrees left from a previous session (e.g., from an interrupted run) are cleaned up automatically on the next `create_worktree()` call via the depth-agnostic stale-cleanup logic. There is no persistent state tied to the old naming scheme that requires manual remediation.

The `WorkspaceInfo` field additions are backward-compatible (new fields default to `None`), and `WorkspaceInfo` is not serialized, so no deserialization or schema migration is required. No existing callers outside `NodeSessionRunner._setup_workspace()` require updates.

## Tech Stack

- Python 3.12+
- asyncio
- git CLI (worktree operations)
- pytest (testing)
- Linux OS (only deployment and CI target)

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/628
