---
spec_id: '07'
spec_name: vendor_fix_pipeline
title: Vendor-Mode Fix Pipeline Adaptation
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor-Mode Fix Pipeline Adaptation

## Summary

Adapt nightshift's fix pipeline so that fixes in vendor mode survive deploy
branch rebuilds. When `vendor.enabled = True`, each successfully integrated fix
creates a dedicated patch branch and registers it in the patch manifest, rather
than squash-merging directly into the deploy branch (which would be lost on the
next rebuild). The fix is also applied to the current deploy branch for
immediate effect.

## Goals

1. **Fixes survive rebuilds** — every nightshift fix in vendor mode produces a
   persistent patch branch that is registered in the manifest and participates
   in all future rebuilds.
2. **Immediate effect** — the fix is applied to the current deploy branch so
   it takes effect without waiting for the next vendor-sync rebuild cycle.
3. **Standard pipeline for non-vendor mode** — when `vendor.enabled = False`,
   the fix pipeline behaves exactly as it does today. Zero regressions.
4. **Manifest consistency** — the patch manifest is updated atomically with
   the fix integration, so the manifest always reflects the current set of
   carried patches.

## Non-Goals

- **Changing the triage/coder/reviewer loop** — the AI pipeline that produces
  the fix is unchanged. Only the integration step (what happens after the fix
  is coded) is modified.
- **Vendor-mode for `af code`** — this spec covers nightshift only. Adapting
  `af code` session lifecycle for vendor mode is a future spec.
- **Automatic upstream PR creation** — nightshift does not create PRs against
  the upstream repo. The operator creates upstream PRs manually from the patch
  branches. (Future enhancement: `nightshift patches upstream <branch>` could
  automate this.)
- **Rebasing fix patches onto upstream** — the vendor-sync stream (spec 06)
  handles rebasing all patches when upstream moves. The fix pipeline just
  creates the patch branch based on the current deploy branch.

## Background

In the standard (non-vendor) flow, nightshift's fix pipeline creates a fix
branch from the integration branch, runs the coder/reviewer loop, and
squash-merges the result back into the integration branch. The fix branch is
then deleted.

In vendor mode, the integration branch (deploy) is a derived artifact that is
force-pushed on every rebuild. A squash-merge into deploy would be lost on the
next rebuild. Instead, the fix must be preserved as a named patch branch that
the rebuild process replays.

The key change: in vendor mode, the fix branch is not ephemeral. It becomes a
carry-patch that lives in the manifest alongside feature patches. The fix
pipeline's integration step must:
1. Register the fix branch in the manifest.
2. Apply the fix to the current deploy branch (for immediate effect).
3. Not delete the fix branch (it's now a persistent patch).

## Tech Stack

- **Language:** Python 3.12+
- **Async runtime:** asyncio
- **Test framework:** pytest

## Functional Requirements

### FR-1: Base branch selection

When `vendor.enabled = True`, fix branches are created from the **deploy
branch** (`config.vendor.deploy_branch`) instead of the integration branch
(`config.workspace.integration_branch`).

Modify the `create_worktree` call in `fix_pipeline.py` to select the base
branch:

```python
base_branch = (
    config.vendor.deploy_branch
    if config.vendor.enabled
    else config.workspace.integration_branch
)
```

This ensures the fix is developed against the current deployed state (upstream
+ existing patches), not against raw upstream.

### FR-2: Vendor-mode integration step

When `vendor.enabled = True` and the coder/reviewer loop produces changes,
replace the standard squash-merge integration with:

**Step 1: Rename fix branch to patch convention**

The fix branch (`fix/{N}-{slug}`) is already a valid patch branch name. No
rename is needed — it becomes the patch branch as-is.

**Step 2: Register in manifest**

1. Load manifest: `load_manifest(repo_path)`.
2. Add patch: `add_patch(manifest, branch=fix_branch_name, description=issue_title)`.
3. Save manifest: `save_manifest(repo_path, manifest)`.

**Step 3: Apply to current deploy branch (immediate effect)**

1. Acquire `MergeLock`.
2. Checkout deploy branch in main repo.
3. `merge_no_ff(repo_path, fix_branch, message="CARRY: fix(#{N}): {title}")`.
4. On conflict: use `resolve_conflicts_with_rerere` (spec 05).
5. Push deploy branch to origin.
6. Release `MergeLock`.

This gives the fix immediate effect without waiting for a full rebuild. The
next rebuild will replay it from the manifest along with all other patches.

**Step 4: Push fix/patch branch**

Push the fix branch to origin: `push_to_remote(repo_path, fix_branch,
force=False)`. The branch must exist on the remote so it survives local
cleanup and can be used for upstream PR creation.

### FR-3: Worktree cleanup adaptation

In vendor mode, `destroy_worktree` must NOT delete the fix branch (since it's
now a persistent patch branch). Modify the cleanup to skip branch deletion
when `vendor.enabled = True` and the fix was successfully integrated (i.e.,
registered in the manifest).

When integration fails, the branch is cleaned up as usual (it was never
registered in the manifest).

### FR-4: Issue update adaptation

In vendor mode, after successful integration:

- Add label `af:fixed` to the issue.
- Close the issue.
- Post a comment:
  ```
  Fix applied to deploy branch and registered as carry-patch `{branch}`.
  The fix will persist across future upstream syncs.
  ```

This differs from the standard flow only in the comment text (which mentions
the carry-patch registration).

### FR-5: PR strategy in vendor mode

When `config.workspace.merge_strategy = "pr"` AND `vendor.enabled = True`:

- The fix branch is pushed to origin (same as standard PR mode).
- A PR is created against the **deploy branch** (not main, since main is a
  pure upstream mirror).
- The fix branch is registered in the manifest.
- The issue is updated with `af:pr` label.
- When the PR is merged (detected by pr-feedback stream), the fix is already
  in the deploy branch. No additional integration is needed.
- The fix branch is NOT deleted after PR merge (it remains as a patch branch
  for future rebuilds).

### FR-6: Branch strategy in vendor mode

When `config.workspace.merge_strategy = "branch"` AND `vendor.enabled = True`:

- The fix branch is kept locally (not pushed).
- The fix branch is registered in the manifest.
- The fix is NOT applied to the deploy branch (operator merges manually).
- Issue comment:
  ```
  Fix branch created: `{branch}`. Registered as carry-patch.
  Merge strategy is set to `branch` — please review and merge manually.
  ```

### FR-7: No-change sessions

When the coder/reviewer loop produces no changes (no new commits on the fix
branch), the vendor-mode integration is skipped entirely. The fix branch is
not registered in the manifest. Standard no-change handling applies (label
`af:no-change`, leave issue open).

### FR-8: Conditional logic isolation

The vendor-mode branching is isolated to the integration step. The rest of
the fix pipeline (issue discovery, dependency analysis, worktree creation,
triage, coder/reviewer loop) is unchanged. The integration step dispatches
on `config.vendor.enabled`:

```python
if config.vendor.enabled:
    return await _integrate_fix_vendor(...)
else:
    return await _integrate_fix(...)  # existing function, unchanged
```

## Non-Functional Requirements

- **Zero regressions in non-vendor mode** — all existing fix pipeline tests
  pass without modification when `vendor.enabled = False`.
- **Merge lock contention** — the vendor-mode integration acquires `MergeLock`
  for the deploy branch merge. This is the same lock used by the vendor-sync
  rebuild. Contention is possible but handled by the lock's wait mechanism.
- **Manifest atomicity** — the manifest is loaded, modified, and saved within
  the merge lock scope. No concurrent modification is possible.
- **Test coverage** — >=90% line coverage on the vendor-mode integration path.
  Tests cover: successful integration, conflict during deploy merge, no-change
  sessions, all three merge strategies in vendor mode.

## Design Decisions

1. **Fix branches become patch branches directly.** Rather than creating a
   separate "patch" branch from the fix branch, the fix branch itself becomes
   the carry-patch. The `fix/{N}-{slug}` naming convention is clear and
   self-documenting in the manifest.

2. **Immediate effect via merge-no-ff.** Rather than waiting for the next
   vendor-sync rebuild cycle, the fix is merged into the current deploy branch
   immediately. This provides the same "fix is live now" experience as the
   standard pipeline. The next rebuild will replay it from the manifest,
   producing the same result.

3. **Branch not deleted on success.** In standard mode, fix branches are
   ephemeral and deleted after squash-merge. In vendor mode, they are
   persistent patch branches. This is a fundamental model change that requires
   explicit handling in `destroy_worktree`.

4. **PR target is deploy branch, not main.** In vendor mode, main is a pure
   upstream mirror. PRs must target the deploy branch, which is the actual
   integration target. This is consistent with the vendor model where deploy
   is the "working" branch.

5. **Integration function split.** Rather than adding vendor-mode conditionals
   throughout `_integrate_fix`, a separate `_integrate_fix_vendor` function
   handles the vendor path. This keeps each path readable and testable in
   isolation.

6. **Manifest update inside merge lock.** The manifest is updated (patch
   added) while holding the merge lock. This prevents a race where the
   vendor-sync stream reads the manifest between "fix merged to deploy" and
   "patch added to manifest" — which would cause the fix to be missing from
   the next rebuild.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 02 (vendor_git_infrastructure) | Upstream | `merge_no_ff`, `push_to_remote` |
| Spec 03 (patch_stack_module) | Upstream | `load_manifest`, `save_manifest`, `add_patch` |
| Spec 04 (vendor_config) | Upstream | `VendorConfig` fields |
| Spec 05 (rerere_integration) | Upstream | `resolve_conflicts_with_rerere` |
| Spec 06 (vendor_sync_stream) | Sibling | Shares merge lock, reads same manifest |
| `nightshift/fix_pipeline.py` | Internal | Modified integration step |
| `workspace/worktree.py` | Internal | Modified cleanup behavior |
| `workspace/merge_lock.py` | Internal | `MergeLock` for deploy branch merge |
