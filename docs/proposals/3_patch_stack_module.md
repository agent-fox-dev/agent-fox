---
spec_id: '03'
spec_name: patch_stack_module
title: Patch Stack Module
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Patch Stack Module

## Summary

Introduce a patch stack management module at
`packages/agentfox/agentfox/workspace/patch_stack.py` that implements the core
operations of the vendor-branch / carry-patch workflow: a declarative patch
manifest (`.agent-fox/patches.toml`), patch lifecycle detection (active, merged
upstream, conflicting), ordered rebase of patch branches onto a new upstream
base, and deterministic rebuild of an integration/deploy branch from the patch
list.

## Goals

1. **Declarative patch manifest** -- a TOML file (`.agent-fox/patches.toml`)
   that lists carry-patch branches in application order, with optional metadata
   (upstream PR URL, description, dependencies). This file is the single source
   of truth for what patches are carried.
2. **Patch lifecycle detection** -- determine whether a patch has been merged
   upstream, is still active, or is in conflict, using both git diff analysis
   and platform API PR status checks.
3. **Rebase-all** -- rebase every patch branch in the manifest onto a new
   upstream base, respecting ordering, with rerere-first / merge-agent-fallback
   conflict resolution.
4. **Integration branch rebuild** -- reconstruct the deploy branch as
   `upstream/main + patch1 + patch2 + ... + patchN` using either merge-no-ff
   (default) or linear (cherry-pick) strategy.
5. **Deployment tagging** -- tag each successful rebuild with a date-stamped tag
   (`deploy-YYYY-MM-DD` or `deploy-YYYY-MM-DD.N`).

## Non-Goals

- **UI for manifest editing** -- the CLI subcommands for add/remove/list are
  covered by spec 08 (Vendor CLI). This spec provides the library API only.
- **Automatic patch creation from nightshift fixes** -- covered by spec 07
  (Vendor Fix Pipeline).
- **Daemon-mode scheduling** -- covered by spec 06 (Vendor Sync Stream).
- **Stacked Git or Jujutsu integration** -- plain git only.
- **Inter-patch dependency resolution** -- patches are applied in manifest order.
  The `depends_on` field is metadata for documentation; it does not influence
  application order.

## Background

The current agent-fox integration model is imperative: each fix or feature
branch is squash-merged into the integration branch as it completes. There is
no concept of a persistent, ordered collection of changes that can be replayed.
When the integration branch diverges from upstream, the divergence is permanent
and accumulates.

The vendor-branch model inverts this: the integration branch is a derived
artifact, mechanically rebuilt from a list of patches on top of upstream. This
makes upstream sync trivial (just rebuild) and makes the carried patch set
explicit and auditable.

This spec introduces the data structures and operations for that model. It is
consumed by the vendor-sync stream (spec 06) and the adapted fix pipeline
(spec 07).

## Tech Stack

- **Language:** Python 3.12+
- **Manifest format:** TOML (parsed with `tomllib` stdlib / `tomli-w` for writing)
- **Async runtime:** asyncio
- **Test framework:** pytest

## Functional Requirements

### FR-1: Patch manifest schema

The manifest file `.agent-fox/patches.toml` has the following schema:

```toml
[upstream]
remote = "upstream"           # git remote name
branch = "main"               # upstream branch to track
last_synced_sha = "abc123"    # last upstream commit SHA processed

[[patches]]
branch = "feature/foo"
upstream_pr = "https://github.com/upstream/repo/pull/123"  # optional
description = "Add widget support"                          # optional
added_at = "2026-08-10"                                     # ISO date
status = "active"             # "active" | "merged" | "conflicting"

[[patches]]
branch = "feature/bar"
description = "Fix auth flow for SSO"
added_at = "2026-08-05"
status = "active"
```

**Fields:**

`[upstream]` section:
- `remote` (str, required) -- name of the git remote pointing to the vendor repo.
- `branch` (str, required) -- branch name to track on the upstream remote.
- `last_synced_sha` (str, optional) -- the SHA of the upstream commit at the
  last successful sync. Used to detect whether upstream has moved.

`[[patches]]` entries (ordered list):
- `branch` (str, required) -- local branch name carrying the patch.
- `upstream_pr` (str, optional) -- URL of the upstream PR this patch corresponds
  to. Used for fast-path lifecycle detection via platform API.
- `description` (str, optional) -- human-readable description.
- `added_at` (str, optional) -- ISO date when the patch was added to the manifest.
- `status` (str, required) -- one of `"active"`, `"merged"`, `"conflicting"`.
  Only `"active"` patches participate in rebuild. `"merged"` patches are
  retained temporarily for audit trail, then pruned. `"conflicting"` patches
  are skipped during rebuild until manually resolved.

### FR-2: Manifest operations

**`load_manifest(repo_path) -> PatchManifest`**
- Reads and parses `.agent-fox/patches.toml` relative to `repo_path`.
- Returns a `PatchManifest` dataclass containing the upstream config and
  ordered list of `PatchEntry` dataclasses.
- Raises `FileNotFoundError` if the manifest does not exist.
- Raises `ManifestError(WorkspaceError)` on parse errors or schema violations.

**`save_manifest(repo_path, manifest)`**
- Serializes the `PatchManifest` to `.agent-fox/patches.toml`.
- Preserves patch ordering.
- Atomic write (write to temp file, then rename).

**`add_patch(manifest, branch, upstream_pr=None, description=None) -> PatchManifest`**
- Appends a new `PatchEntry` to the manifest with status `"active"`.
- Raises `ManifestError` if a patch with the same branch name already exists.
- Returns the updated manifest (does not write to disk -- caller calls
  `save_manifest`).

**`remove_patch(manifest, branch) -> PatchManifest`**
- Removes the `PatchEntry` with the given branch name.
- Raises `ManifestError` if no patch with that branch name exists.
- Returns the updated manifest.

**`reorder_patches(manifest, branches) -> PatchManifest`**
- Reorders patches to match the given branch name list.
- Raises `ManifestError` if the branch list doesn't match the manifest's
  patch set exactly (no additions, no removals, just reordering).
- Returns the updated manifest.

### FR-3: Patch lifecycle detection

**`check_patch_status(repo_path, patch, upstream_remote, upstream_branch, platform=None) -> PatchStatus`**

Returns a `PatchStatus` enum: `ACTIVE`, `MERGED`, `CONFLICTING`.

Detection strategy:

1. **Fast path (platform API):** If `patch.upstream_pr` is set and `platform`
   is provided, check the PR merge status via `platform.get_issue()` or
   equivalent. If the PR is merged, return `MERGED`.
2. **Slow path (git):** If no PR URL or no platform, use
   `git log --cherry-mark --oneline {upstream_remote}/{upstream_branch}...{patch.branch}`
   to detect whether the patch's commits are already present in upstream.
   If all commits are marked as equivalent (`=`), return `MERGED`.
3. **Conflict check:** Attempt a dry-run merge or diff to detect whether the
   patch would conflict with the current upstream. If it would, return
   `CONFLICTING`. If not, return `ACTIVE`.

**`check_all_patches(repo_path, manifest, platform=None) -> list[tuple[PatchEntry, PatchStatus]]`**
- Runs `check_patch_status` for every patch in the manifest.
- Returns results in manifest order.

**`prune_merged_patches(manifest) -> PatchManifest`**
- Removes all patches with `status = "merged"` from the manifest.
- Returns the updated manifest.

### FR-4: Rebase all patches

**`rebase_patches(repo_path, manifest, new_base_ref) -> RebaseReport`**

Rebases each `"active"` patch branch onto `new_base_ref` in manifest order.

For each patch:
1. `git rebase --onto {new_base_ref} {old_base} {patch.branch}` where
   `old_base` is computed as the merge-base of the patch branch and the
   previous upstream base (from `manifest.upstream.last_synced_sha` or
   computed).
2. On conflict:
   a. Check `git rerere` status -- if rerere auto-resolved all conflicts,
      continue.
   b. If unresolved conflicts remain, invoke merge agent for resolution.
   c. If merge agent fails, `git rebase --abort`, mark patch as
      `"conflicting"` in the manifest, and continue with the next patch.
3. On success, force-push the rebased branch to `origin` with
   `--force-with-lease`.

Returns a `RebaseReport` dataclass:
```python
@dataclass(frozen=True)
class RebaseReport:
    rebased: list[str]       # branch names successfully rebased
    conflicting: list[str]   # branch names that failed rebase
    skipped: list[str]       # branches skipped (already "merged" or "conflicting")
```

### FR-5: Rebuild integration branch

**`rebuild_integration(repo_path, manifest, deploy_branch, strategy="merge-no-ff") -> RebuildReport`**

Reconstructs the deploy branch from scratch.

Steps:
1. Acquire `MergeLock`.
2. Resolve the upstream base: `{upstream_remote}/{upstream_branch}`.
3. `git checkout -B {deploy_branch} {upstream_base}` -- reset deploy branch to
   upstream tip.
4. For each `"active"` patch in manifest order:
   - **merge-no-ff strategy:** `git merge --no-ff {patch.branch} -m "CARRY: {patch.description or patch.branch}"`.
   - **linear strategy:** `git cherry-pick {upstream_base}..{patch.branch}`
     (replays all commits from the patch branch).
5. On conflict at any patch:
   a. Attempt rerere auto-resolution.
   b. Fall back to merge agent.
   c. If both fail: abort, mark patch as `"conflicting"`, and either skip it
      or abort the entire rebuild (configurable via `on_conflict` parameter:
      `"skip"` or `"abort"`, default `"skip"`).
6. On successful rebuild:
   a. Force-push deploy branch to origin with `--force-with-lease`.
   b. If tagging is enabled: create tag `{tag_prefix}{date}` (e.g.,
      `deploy-2026-08-10`). If tag exists, append `.N` suffix
      (`deploy-2026-08-10.1`). Push tag to origin.
7. Release `MergeLock`.

Returns a `RebuildReport` dataclass:
```python
@dataclass(frozen=True)
class RebuildReport:
    success: bool
    deploy_branch: str
    upstream_sha: str
    applied_patches: list[str]
    skipped_patches: list[str]
    conflicting_patches: list[str]
    tag: str | None
```

### FR-6: Data classes

Define the following in `patch_stack.py`:

```python
class PatchStatus(Enum):
    ACTIVE = "active"
    MERGED = "merged"
    CONFLICTING = "conflicting"

@dataclass
class PatchEntry:
    branch: str
    upstream_pr: str | None = None
    description: str | None = None
    added_at: str | None = None
    status: str = "active"

@dataclass
class UpstreamConfig:
    remote: str
    branch: str
    last_synced_sha: str | None = None

@dataclass
class PatchManifest:
    upstream: UpstreamConfig
    patches: list[PatchEntry]

@dataclass(frozen=True)
class RebaseReport:
    rebased: list[str]
    conflicting: list[str]
    skipped: list[str]

@dataclass(frozen=True)
class RebuildReport:
    success: bool
    deploy_branch: str
    upstream_sha: str
    applied_patches: list[str]
    skipped_patches: list[str]
    conflicting_patches: list[str]
    tag: str | None
```

### FR-7: Error types

**`ManifestError(WorkspaceError)`** -- raised for manifest parse errors, schema
violations, and manifest-level constraint violations (duplicate branch names,
branch-not-found). Added to `core/errors.py`.

### FR-8: Public API exports

Add to `workspace/__init__.py`:
- `load_manifest`, `save_manifest`, `add_patch`, `remove_patch`,
  `reorder_patches`
- `check_patch_status`, `check_all_patches`, `prune_merged_patches`
- `rebase_patches`
- `rebuild_integration`
- `PatchManifest`, `PatchEntry`, `UpstreamConfig`, `PatchStatus`
- `RebaseReport`, `RebuildReport`
- `ManifestError`

## Non-Functional Requirements

- **Manifest atomicity** -- `save_manifest` writes to a temporary file and
  renames, preventing partial writes on crash.
- **Merge lock** -- `rebuild_integration` acquires `MergeLock` for the entire
  rebuild. No other harvest or rebuild can run concurrently.
- **Idempotency** -- `rebuild_integration` is safe to call repeatedly. The
  deploy branch is reset to upstream tip before applying patches, so the
  result is deterministic for a given manifest + upstream state.
- **Test coverage** -- >=90% line coverage on all new functions.

## Design Decisions

1. **TOML for the manifest, not YAML or plain text.** TOML is in the Python
   stdlib (`tomllib`), supports ordered arrays of tables naturally, and is
   already used by `pyproject.toml` in this project. Comments are preserved
   by the format (though `tomli-w` does not preserve them on write -- accepted
   trade-off).

2. **Manifest lives on disk, not committed to any branch.** The deploy branch
   is force-pushed on every rebuild. The fork's main is force-pushed from
   upstream on every sync. Neither branch can reliably host the manifest.
   The manifest is a local file at `.agent-fox/patches.toml` (which is in
   `.gitignore`). For sharing across machines, it can be committed to a
   separate `vendor-meta` branch or stored externally -- a future enhancement.

3. **`"merged"` patches retained temporarily.** Rather than immediately
   deleting merged patches, they are marked `"merged"` and retained until
   `prune_merged_patches` is called. This gives the operator a window to
   verify the upstream merge is correct before the patch disappears from
   the manifest.

4. **Merge-no-ff as default strategy.** Merge commits preserve patch boundaries
   in `git log --graph`, making it easy to see which carry-patch each commit
   belongs to. Linear (cherry-pick) gives a cleaner history but loses this
   visibility. Both are supported; merge-no-ff is the default per user
   preference.

5. **Conflict handling: skip by default.** When a single patch conflicts during
   rebuild, skipping it and continuing with remaining patches produces a
   partially-complete deploy branch. This is preferable to aborting the entire
   rebuild, because the operator can inspect which patch failed and fix it
   independently. The `on_conflict="abort"` option is available for stricter
   workflows.

6. **Rebase force-pushes patch branches.** After rebasing a patch branch onto
   the new upstream, it must be force-pushed to origin so the remote state
   matches. `--force-with-lease` is used for safety.

7. **Tag suffix for multiple rebuilds per day.** Using `.N` suffix
   (`deploy-2026-08-10.1`) avoids tag collisions when multiple rebuilds happen
   on the same day, without requiring timestamps that are harder to read.

8. **`depends_on` is metadata only.** Patch ordering is determined by position
   in the `[[patches]]` array, not by dependency resolution. The `depends_on`
   field (if added later) serves as documentation for why a particular ordering
   was chosen. This keeps the rebuild algorithm simple and predictable.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 02 (vendor_git_infrastructure) | Upstream | Uses `merge_no_ff`, `cherry_pick`, `rebase_onto` (3-arg), `create_tag`, `push_tags`, `fetch_from` |
| Spec 05 (rerere_integration) | Upstream | Rerere auto-resolution during rebase and rebuild |
| `workspace/merge_lock.py` | Internal | `MergeLock` used during rebuild |
| `workspace/merge_agent.py` | Internal | Fallback conflict resolution |
| `workspace/git.py` | Internal | All git primitives |
| `core/errors.py` | Internal | `WorkspaceError` base class for `ManifestError` |
| `tomllib` (stdlib) | External | TOML parsing |
| `tomli-w` | External | TOML writing (add to dependencies) |
| Spec 04 (vendor_config) | Sibling | Config fields consumed by callers, not by this module directly |
