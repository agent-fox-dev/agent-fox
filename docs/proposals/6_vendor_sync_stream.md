---
spec_id: '06'
spec_name: vendor_sync_stream
title: Vendor Sync Work Stream
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor Sync Work Stream

## Summary

Add a `vendor-sync` work stream to nightshift's daemon runner that periodically
checks for upstream changes, syncs the fork's main branch, detects graduated
patches, rebases remaining patches onto the new upstream, and rebuilds the
deploy branch. This is the runtime orchestrator that drives the patch stack
module (spec 03) on a schedule.

## Goals

1. **Automatic upstream tracking** — when upstream moves, the vendor-sync
   stream detects it and triggers a full sync cycle without human intervention.
2. **Patch lifecycle management** — patches merged upstream are detected,
   marked in the manifest, and pruned. The operator sees a log entry and
   (optionally) a summary comment/notification.
3. **Deploy branch freshness** — after every upstream change, the deploy branch
   is rebuilt to reflect `current upstream + active patches`.
4. **Operator-gated conflict resolution** — mechanical work (fetch, rebase,
   rebuild) is automated. Unresolvable conflicts are reported and the
   conflicting patch is marked, not silently dropped.
5. **Coexistence with fix-pipeline** — the vendor-sync and fix-pipeline streams
   share the merge lock and do not interfere with each other.

## Non-Goals

- **Replacing the fix pipeline** — vendor-sync handles upstream tracking and
  patch management. Bug fixes still flow through the fix pipeline (spec 07
  adapts that pipeline for vendor mode).
- **Multi-upstream tracking** — only one upstream remote is supported per
  config. Tracking multiple upstreams requires multiple config files / repos.
- **Notifications beyond logging** — Slack, email, or webhook notifications
  for sync events are out of scope. Logging and issue comments are the
  reporting mechanism.
- **Automatic conflict resolution beyond rerere + merge agent** — if both
  rerere and the merge agent fail, the patch is marked `"conflicting"` and
  the operator must resolve it manually.

## Background

Nightshift's daemon runner (`DaemonRunner` in `daemon.py`) orchestrates work
streams — periodic async tasks that poll for work and process it. The existing
streams are `fix-pipeline` (polls for `af:fix` issues) and `pr-feedback`
(monitors open PRs). Each stream implements the `WorkStream` protocol: `name`,
`interval`, `enabled`, `run_once()`, `shutdown()`.

The vendor-sync stream fits this model naturally: it polls for upstream changes
on a configurable interval and processes them through the patch stack module.

The vendor-sync stream is the "scheduled GitHub Action" recommended by the
proposal — a recurring job that fetches upstream, rebases patches, rebuilds
deploy, and reports results.

## Tech Stack

- **Language:** Python 3.12+
- **Async runtime:** asyncio
- **Test framework:** pytest

## Functional Requirements

### FR-1: VendorSyncStream class

Implement `VendorSyncStream` in a new module
`packages/agentfox/agentfox/nightshift/vendor_sync.py`.

Implements the `WorkStream` protocol:

```python
class VendorSyncStream:
    name: str = "vendor-sync"
    interval: int  # from config.nightshift.vendor_sync_interval
    enabled: bool  # from config.vendor.enabled

    async def run_once(self) -> None: ...
    async def shutdown(self) -> None: ...
```

### FR-2: Startup initialization

On first `run_once()` invocation (or in stream constructor):

1. **Ensure upstream remote exists:** call `add_remote(repo_path,
   config.vendor.upstream_remote, config.vendor.upstream_url)`. Idempotent
   (spec 02 FR-1).
2. **Setup rerere:** if `config.vendor.rerere_enabled`, call
   `setup_rerere(repo_path)` (spec 05 FR-1).
3. **Load or create manifest:** attempt `load_manifest(repo_path)`. If
   `FileNotFoundError`, create a default manifest with the upstream config
   from `config.vendor` and an empty patch list, then `save_manifest`.
4. **Ensure deploy branch exists:** if the local deploy branch does not exist,
   create it from `{upstream_remote}/{upstream_branch}`.

### FR-3: Sync cycle (`run_once`)

Each invocation of `run_once()` executes the following steps:

**Step 1: Fetch upstream**
- `fetch_from(repo_path, config.vendor.upstream_remote)`.
- On failure: log WARNING, return early (retry on next cycle).

**Step 2: Detect upstream movement**
- Resolve `{upstream_remote}/{upstream_branch}` to a SHA.
- Compare to `manifest.upstream.last_synced_sha`.
- If unchanged: log DEBUG "upstream unchanged", return early.
- If changed: log INFO "upstream moved from {old} to {new}", continue.

**Step 3: Sync fork's main**
- `push_to_remote(repo_path, f"{upstream_remote}/{upstream_branch}:main",
  remote="origin", force=True)`.
- This keeps the fork's `main` as a pure mirror of upstream.
- Uses `--force-with-lease` (via `push_to_remote(force=True)`).
- On failure: log ERROR, continue (the sync can proceed without updating
  origin/main — the deploy branch rebuild is what matters).

**Step 4: Check patch lifecycle**
- Call `check_all_patches(repo_path, manifest, platform)`.
- For each patch that returns `MERGED`:
  a. Update `patch.status = "merged"` in the manifest.
  b. Log INFO: `"Patch '{branch}' merged upstream (PR {url}). Removing from
     carry set."`.
- Call `prune_merged_patches(manifest)` to remove merged patches.
- Save updated manifest.
- Commit manifest change to fork's `main`: `git add .agent-fox/patches.toml &&
  git commit -m "chore(vendor): prune merged patches: {branch_list}"`.

**Step 5: Rebase active patches**
- Call `rebase_patches(repo_path, manifest, f"{upstream_remote}/{upstream_branch}")`.
- Log the `RebaseReport`: number rebased, conflicting, skipped.
- For conflicting patches: update `patch.status = "conflicting"` in manifest,
  save manifest, log WARNING with details.

**Step 6: Rebuild deploy branch**
- Call `rebuild_integration(repo_path, manifest, config.vendor.deploy_branch,
  strategy=config.vendor.rebuild_strategy)`.
- Log the `RebuildReport`: success/failure, applied/skipped/conflicting patches,
  tag if created.

**Step 7: Update manifest state**
- Set `manifest.upstream.last_synced_sha` to the new upstream SHA.
- Save manifest.
- Commit: `git add .agent-fox/patches.toml && git commit -m
  "chore(vendor): sync upstream to {short_sha}"`.

**Step 8: Push manifest commits**
- Push fork's `main` to origin (to persist manifest changes).

### FR-4: No-upstream-change fast path

When upstream has not moved since `last_synced_sha`, `run_once` returns
immediately after Step 2. No rebase, rebuild, or manifest changes occur. This
is the common case and should be cheap (one `git fetch` + one SHA comparison).

### FR-5: Error handling

| Error | Behavior |
|---|---|
| Fetch upstream fails | Log WARNING, return early. Retry on next cycle. |
| Fork main push fails | Log ERROR, continue with sync. Non-critical. |
| Manifest load fails | Log ERROR, return early. Operator must fix manifest. |
| Single patch rebase fails | Mark patch `"conflicting"`, continue with others. |
| Rebuild fails entirely | Log ERROR. Deploy branch is left in its previous state (not partially updated — the rebuild resets to upstream before applying). |
| Manifest save/commit fails | Log ERROR. The in-memory manifest is correct; it will be re-saved on next cycle. |
| Tag creation fails | Log WARNING. Non-critical; the deploy branch is still updated. |

### FR-6: Stream registration

Update `streams.py:build_streams()` to include `VendorSyncStream` when
`config.vendor.enabled = True`. The stream is constructed with the repo path,
config, platform instance, and engine reference.

### FR-7: Daemon runner integration

No changes to `DaemonRunner` itself — it already supports arbitrary
`WorkStream` instances. The vendor-sync stream is added to the stream list
by `build_streams()`.

### FR-8: Audit events

Emit structured audit events (via `afaudit`) for:
- `vendor.sync.started` — upstream fetch initiated.
- `vendor.sync.upstream_moved` — upstream SHA changed.
- `vendor.patch.merged` — a patch was detected as merged upstream.
- `vendor.patch.conflicting` — a patch failed to rebase.
- `vendor.rebuild.success` — deploy branch rebuilt successfully.
- `vendor.rebuild.failure` — deploy branch rebuild failed.
- `vendor.sync.completed` — sync cycle finished.

## Non-Functional Requirements

- **Merge lock coexistence** — the vendor-sync stream acquires `MergeLock`
  during rebuild (via `rebuild_integration`). The fix pipeline also acquires
  `MergeLock` during harvest. These are mutually exclusive — one waits for
  the other. The merge lock's heartbeat and stale-detection mechanisms prevent
  deadlocks.
- **Budget awareness** — vendor-sync cycles that invoke the AI merge agent
  for conflict resolution consume budget tokens. The stream respects the
  daemon's budget limits.
- **Interval independence** — `vendor_sync_interval` is independent of the
  fix-pipeline's `poll_interval`. They can run at different frequencies.
- **Test coverage** — >=90% line coverage on the stream class and sync cycle.

## Design Decisions

1. **Sync fork's main via force-push.** The proposal recommends
   `git push origin upstream/main:main --force-with-lease`. This keeps fork
   main as a pure upstream mirror. The force-push is safe because fork main
   should never have direct commits (all changes are on patch branches).

2. **Manifest commits go to fork's main.** Manifest changes (pruned patches,
   updated SHA) are committed to the fork's main branch. This creates a minor
   deviation from "pure mirror" — fork main has manifest commits that upstream
   does not. This is acceptable because: (a) the manifest file lives under
   `.agent-fox/` which is gitignored by upstream, and (b) the next upstream
   sync force-push will overwrite these commits. The manifest's authoritative
   state is rebuilt each cycle.

   **Alternative considered:** storing the manifest on a separate `vendor-meta`
   branch. Rejected because it adds complexity (branch management, checkout
   switching) for little benefit. The force-push-overwrite behavior is actually
   desirable — it prevents manifest state from drifting.

   **Correction:** If fork main is force-pushed from upstream on every sync,
   manifest commits on main would be lost. Therefore: manifest commits should
   NOT go to main. Instead, the manifest is a local-only file that is NOT
   committed to any branch. It lives at `.agent-fox/patches.toml` which is
   in `.gitignore`. State persistence relies on the file existing on disk.
   For sharing across machines, the manifest can be committed to a separate
   `vendor-meta` branch or stored externally. This is a future enhancement.

3. **One upstream remote only.** Supporting multiple upstreams adds significant
   complexity (multiple manifests, merge ordering). One upstream per repo
   is the common case and sufficient for the initial implementation.

4. **Conflicting patches are skipped, not removed.** A patch that fails rebase
   is marked `"conflicting"` and skipped during rebuild. The operator must
   manually resolve it (rebase the branch, update status to `"active"`, trigger
   a rebuild). Automatic removal would silently drop functionality.

5. **Audit events follow existing patterns.** The event names use the
   `vendor.` prefix for namespacing, consistent with how nightshift events
   are structured in `afaudit`.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 02 (vendor_git_infrastructure) | Upstream | `add_remote`, `fetch_from`, `push_to_remote`, `create_tag` |
| Spec 03 (patch_stack_module) | Upstream | `load_manifest`, `save_manifest`, `check_all_patches`, `prune_merged_patches`, `rebase_patches`, `rebuild_integration` |
| Spec 04 (vendor_config) | Upstream | `VendorConfig`, `vendor_sync_interval` |
| Spec 05 (rerere_integration) | Upstream | `setup_rerere` |
| `nightshift/streams.py` | Internal | `WorkStream` protocol, `build_streams()` |
| `nightshift/daemon.py` | Internal | `DaemonRunner` (no changes needed) |
| `workspace/merge_lock.py` | Internal | Via `rebuild_integration` |
| `afaudit` | Internal | Structured audit events |
| Spec 07 (vendor_fix_pipeline) | Downstream | Fix pipeline must be aware of deploy branch |
