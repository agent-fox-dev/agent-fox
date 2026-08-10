---
spec_id: '05'
spec_name: rerere_integration
title: Git Rerere Integration
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Git Rerere Integration

## Summary

Integrate `git rerere` (reuse recorded resolution) into agent-fox's conflict
resolution pipeline. When enabled, rerere automatically records merge/rebase
conflict resolutions and replays them when the same conflicts recur -- which
happens on every vendor-branch rebuild cycle. The AI merge agent remains as a
fallback for novel conflicts, and its resolutions are recorded by rerere for
future replay.

## Goals

1. **Deterministic replay of known conflicts** -- conflicts that have been
   resolved once (either manually or by the AI merge agent) are automatically
   resolved on subsequent rebuilds without invoking the AI.
2. **Zero-cost for existing workflows** -- rerere is only enabled when
   `vendor.rerere_enabled = True` in config. Non-vendor workflows are
   unaffected.
3. **Rerere-first, AI-fallback** -- the conflict resolution chain is:
   attempt rerere auto-resolution, check if resolved, if not invoke merge
   agent, agent's resolution is recorded by rerere for future replay.
4. **Shared rerere state across worktrees** -- git worktrees share the parent
   repo's `.git/rr-cache/`, so rerere state is automatically available in all
   worktrees without explicit sharing.

## Non-Goals

- **Cross-machine rerere sharing** -- syncing `.git/rr-cache/` between machines
  (e.g., via a dedicated branch or artifact) is out of scope. This can be added
  later but requires careful consideration of security and staleness.
- **Rerere for non-vendor workflows** -- while rerere could benefit the standard
  harvest squash-merge path, this spec scopes rerere to vendor mode only to
  limit blast radius.
- **`rerere-train`** -- pre-seeding rerere from existing merge history is not
  implemented. The cache builds organically from actual conflict resolutions.
- **Rerere garbage collection** -- `git rerere gc` is not scheduled. The default
  git gc settings handle this.

## Background

In the vendor-branch workflow, the same conflicts recur on every rebuild: when
a carry-patch modifies code that upstream also changed, the merge/rebase
produces the same conflict each cycle. Without rerere, the AI merge agent would
re-resolve identical conflicts every time -- expensive, non-deterministic, and
potentially inconsistent.

`git rerere` solves this by recording conflict resolutions in `.git/rr-cache/`.
When the same conflict (identified by the conflict hunks, not by commit SHA)
recurs, rerere replays the recorded resolution automatically. With
`rerere.autoupdate = true`, resolved files are automatically staged.

The key insight is that the AI merge agent's resolutions are novel only once.
After the first resolution, rerere replays it deterministically and for free.

## Tech Stack

- **Language:** Python 3.12+
- **Git config:** `git config` commands via `run_git`
- **Test framework:** pytest

## Functional Requirements

### FR-1: Rerere setup

**`setup_rerere(repo_path)`**

Enables rerere for the repository:
1. `git config rerere.enabled true` -- enable rerere.
2. `git config rerere.autoupdate true` -- auto-stage resolved files.

Called once during vendor-sync stream startup (spec 06) and during `af init`
when vendor mode is enabled.

Idempotent: safe to call multiple times. Git config set operations are
inherently idempotent.

### FR-2: Rerere status check

**`rerere_status(repo_path) -> RerereStatus`**

After a failed merge/rebase that produced conflicts, check whether rerere has
auto-resolved them:

1. `git rerere status` -- lists files with remaining (unresolved) conflicts.
2. If the output is empty, all conflicts were auto-resolved by rerere.
3. If the output lists files, those files still have unresolved conflicts.

Returns:
```python
@dataclass(frozen=True)
class RerereStatus:
    all_resolved: bool
    unresolved_files: list[str]
```

### FR-3: Rerere-aware conflict resolution chain

**`resolve_conflicts_with_rerere(repo_path, merge_agent_fn=None) -> bool`**

A composite operation used by the patch stack module (spec 03) during rebase
and rebuild:

1. Check `rerere_status(repo_path)`.
2. If `all_resolved`: stage all files (`git add -A`), return `True`.
3. If unresolved files remain and `merge_agent_fn` is provided:
   a. Invoke `merge_agent_fn(repo_path)` -- the existing merge agent.
   b. After agent completes, verify with `git diff --check` (no conflict
      markers remain).
   c. If verified: `git add -A`, return `True`. The resolution is now
      recorded in rerere's cache automatically (rerere records resolutions
      when the conflicted file is staged and the merge/rebase completes).
   d. If verification fails: return `False`.
4. If unresolved files remain and no `merge_agent_fn`: return `False`.

### FR-4: Rerere cache diagnostics

**`rerere_cache_stats(repo_path) -> RerereCacheStats`**

For observability and debugging:

1. Count entries in `.git/rr-cache/` directory.
2. Return:
```python
@dataclass(frozen=True)
class RerereCacheStats:
    total_entries: int
    cache_path: str
```

### FR-5: Clear rerere cache

**`clear_rerere_cache(repo_path)`**

Removes all entries from `.git/rr-cache/`. Used for recovery when the cache
contains incorrect resolutions.

1. `git rerere clear` -- removes all recorded resolutions.

### FR-6: Module location and exports

New module: `packages/agentfox/agentfox/workspace/rerere.py`

Add to `workspace/__init__.py`:
- `setup_rerere`
- `rerere_status`, `RerereStatus`
- `resolve_conflicts_with_rerere`
- `rerere_cache_stats`, `RerereCacheStats`
- `clear_rerere_cache`

## Non-Functional Requirements

- **No effect when disabled** -- when `vendor.rerere_enabled = False`, none of
  these functions are called. The patch stack module (spec 03) checks the
  config flag before invoking rerere operations.
- **Worktree compatibility** -- git worktrees share the parent repo's
  `.git/rr-cache/`. No special handling is needed for rerere to work in
  worktrees. This is verified by a test.
- **Test coverage** -- >=90% line coverage. Tests must verify the full
  rerere-first, AI-fallback, rerere-records chain using a synthetic repo
  with a reproducible conflict.

## Design Decisions

1. **`rerere.autoupdate = true`.** Without autoupdate, rerere resolves conflicts
   but leaves the files unstaged, requiring an explicit `git add`. With
   autoupdate, resolved files are automatically staged, which simplifies the
   resolution chain and avoids a class of "rerere resolved it but the rebase
   still failed because files weren't staged" bugs.

2. **`resolve_conflicts_with_rerere` is the primary interface.** Rather than
   having every caller manually check rerere status and fall back to the merge
   agent, this composite function encapsulates the resolution chain. Callers
   (patch stack rebase, rebuild) call this single function.

3. **Merge agent function passed as callback.** The rerere module doesn't
   depend on the merge agent module directly. Instead, the caller passes the
   merge agent as a callback (`merge_agent_fn`). This keeps the dependency
   direction clean: `patch_stack` depends on both `rerere` and `merge_agent`,
   but `rerere` depends on neither.

4. **No cross-machine sharing.** Sharing `.git/rr-cache/` across machines (via
   a branch, artifact, or external store) adds significant complexity. The
   cache builds quickly (one rebuild cycle captures all recurring conflicts)
   and is machine-local by design. Cross-machine sharing can be added later
   if needed.

5. **No scheduled garbage collection.** Git's default `gc.rerereResolved` (60
   days) and `gc.rerereUnresolved` (15 days) settings are sufficient. Running
   `git gc` (which the user or CI already does) handles rerere cache expiry.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 02 (vendor_git_infrastructure) | Upstream | Uses `run_git` for git config/commands |
| Spec 04 (vendor_config) | Upstream | `vendor.rerere_enabled` flag |
| `workspace/git.py` | Internal | `run_git` for executing git commands |
| `workspace/merge_agent.py` | Internal | Passed as callback by callers, not imported directly |
| Spec 03 (patch_stack_module) | Downstream | Primary consumer of `resolve_conflicts_with_rerere` |
