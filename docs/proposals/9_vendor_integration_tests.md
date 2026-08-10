---
spec_id: '09'
spec_name: vendor_integration_tests
title: Vendor Workflow Integration Tests
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor Workflow Integration Tests

## Summary

Create a cross-cutting integration test suite that validates the vendor-branch
workflow end-to-end, using synthetic git repositories to exercise the full
cycle: upstream sync, patch lifecycle, rebase, rebuild, fix integration, and
CLI commands. These tests complement the unit tests in individual specs (02–08)
by testing the interactions between components.

## Goals

1. **End-to-end vendor sync** — test the complete cycle from upstream commit
   to rebuilt deploy branch with tags, using a synthetic upstream repo and
   fork.
2. **Patch lifecycle** — test the full lifecycle of a patch: added to manifest,
   rebased on upstream move, applied during rebuild, detected as merged
   upstream, pruned from manifest.
3. **Rerere + merge agent chain** — test that recurring conflicts are resolved
   by rerere on the second rebuild, not by the merge agent.
4. **Fix pipeline in vendor mode** — test that nightshift fixes create
   persistent patch branches that survive rebuilds.
5. **CLI commands** — test `nightshift vendor-sync`, `nightshift rebuild`, and
   `nightshift patches` subcommands against real git state.
6. **Rebuild idempotency** — test that rebuilding with the same manifest and
   upstream state produces the same tree (same file contents), even if commit
   SHAs differ.

## Non-Goals

- **Performance/load testing** — not testing with large numbers of patches or
  large repos.
- **Network-dependent tests** — all tests use local git repos, not real GitHub.
  Platform API calls are mocked.
- **Exhaustive error-path testing** — error cases are covered by unit tests in
  individual specs. Integration tests focus on the happy path and the most
  important failure modes.

## Background

The vendor workflow spans six packages/modules: git infrastructure (git.py),
patch stack (patch_stack.py), configuration (config.py), rerere (rerere.py),
vendor-sync stream (vendor_sync.py), and fix pipeline (fix_pipeline.py). Each
has its own unit tests. However, the interactions between these components —
particularly around git state, manifest state, and merge lock coordination —
are where integration bugs are most likely.

The synthetic repo setup creates a "miniature fork ecosystem": an upstream bare
repo, a fork (clone), and the agent-fox working directory. This allows testing
fetch/push/rebase/merge operations against real git repos without network
access.

## Tech Stack

- **Language:** Python 3.12+
- **Test framework:** pytest
- **Git repos:** created via `git init --bare` and `git clone` in `tmp_path`
  fixtures
- **Platform mocking:** `unittest.mock` for GitHub API calls

## Functional Requirements

### FR-1: Test fixtures

**`vendor_ecosystem` fixture** (session or function-scoped):

Creates:
1. **Upstream bare repo** (`upstream/`) — initialized with `main` branch,
   a `README.md`, and a few source files.
2. **Fork repo** (`fork/`) — cloned from upstream. Configured with:
   - `origin` remote pointing to a second bare repo (`origin-bare/`).
   - `upstream` remote pointing to the upstream bare repo.
   - `.agent-fox/config.toml` with `vendor.enabled = true` and appropriate
     settings.
3. **Helper functions:**
   - `upstream_commit(message, files)` — creates a commit in the upstream
     repo and pushes to its bare remote.
   - `create_patch_branch(name, files)` — creates a branch in the fork with
     the given changes.
   - `get_deploy_tree()` — returns the file contents of the deploy branch as
     a dict for comparison.

### FR-2: End-to-end sync cycle test

**`test_full_sync_cycle`**

1. Setup: upstream has commits A, B. Fork has two patch branches that modify
   different files.
2. Initialize manifest with both patches.
3. Run vendor-sync cycle.
4. Assert: deploy branch contains upstream A + B + patch₁ + patch₂.
5. Assert: tag `deploy-YYYY-MM-DD` exists.
6. Add commit C to upstream.
7. Run vendor-sync cycle again.
8. Assert: deploy branch contains upstream A + B + C + patch₁ + patch₂.
9. Assert: new tag exists (with `.1` suffix if same day).
10. Assert: `manifest.upstream.last_synced_sha` matches commit C.

### FR-3: Patch merged upstream test

**`test_patch_graduated_upstream`**

1. Setup: upstream has commit A. Fork has patch branch that adds file X.
2. Initialize manifest with the patch.
3. Run sync → deploy has A + patch.
4. Add file X to upstream (simulating the upstream PR being merged).
5. Run sync again.
6. Assert: patch is detected as `"merged"` and pruned from manifest.
7. Assert: deploy branch still contains file X (now from upstream, not patch).
8. Assert: patch branch is no longer in the manifest.

### FR-4: Conflict and rerere test

**`test_rerere_replays_resolution`**

1. Setup: upstream has file F with content "original". Patch branch modifies
   F to "patched".
2. Run sync → deploy has "patched".
3. Upstream modifies F to "upstream-changed" (conflict with patch).
4. Run sync → conflict during rebase/rebuild. Merge agent resolves it
   (mocked to produce "merged-content"). Rerere records the resolution.
5. Assert: deploy has "merged-content".
6. Force a rebuild (same state) → same conflict recurs.
7. Assert: rerere auto-resolves without invoking merge agent.
8. Assert: deploy has "merged-content" (same as before).

### FR-5: Fix pipeline vendor-mode test

**`test_nightshift_fix_creates_patch`**

1. Setup: vendor mode enabled. Deploy branch exists with upstream + 1 patch.
2. Simulate a nightshift fix (mock the coder/reviewer loop to produce a
   commit on the fix branch).
3. Run fix pipeline integration.
4. Assert: fix branch exists as a persistent branch (not deleted).
5. Assert: manifest contains a new patch entry for the fix branch.
6. Assert: deploy branch contains the fix (immediate effect).
7. Trigger a rebuild from the manifest.
8. Assert: deploy branch still contains the fix (survived rebuild).

### FR-6: Rebuild idempotency test

**`test_rebuild_idempotency`**

1. Setup: manifest with 3 patches, upstream at commit X.
2. Run rebuild → capture deploy tree (file contents).
3. Run rebuild again (same manifest, same upstream).
4. Assert: deploy tree is identical (same file contents, even if commit SHAs
   differ due to timestamps).

### FR-7: Rebuild strategy comparison test

**`test_merge_no_ff_vs_linear_strategies`**

1. Setup: manifest with 2 patches.
2. Rebuild with `merge-no-ff` strategy → capture deploy tree.
3. Rebuild with `linear` strategy → capture deploy tree.
4. Assert: both produce the same file contents.
5. Assert: `merge-no-ff` has merge commits; `linear` does not.

### FR-8: CLI integration tests

**`test_cli_vendor_sync`**
- Run `nightshift vendor-sync --json` via `CliRunner`.
- Assert JSON output contains expected fields.
- Assert git state matches expected state.

**`test_cli_rebuild`**
- Run `nightshift rebuild --json` via `CliRunner`.
- Assert rebuild succeeded.

**`test_cli_patches_list`**
- Run `nightshift patches list --json` via `CliRunner`.
- Assert output lists all patches with correct statuses.

**`test_cli_patches_add_remove`**
- Run `nightshift patches add <branch> --description "test"` via `CliRunner`.
- Assert manifest contains the new patch.
- Run `nightshift patches remove <branch> --yes` via `CliRunner`.
- Assert manifest no longer contains the patch.

**`test_cli_daemon_guard`**
- Create a PID file simulating a running daemon.
- Run `nightshift vendor-sync` via `CliRunner`.
- Assert exit code 1 and appropriate error message.

### FR-9: Merge lock coordination test

**`test_sync_and_fix_merge_lock`**

1. Setup: vendor mode enabled with patches.
2. Start a vendor-sync rebuild (hold merge lock).
3. Attempt fix pipeline integration concurrently.
4. Assert: fix pipeline waits for merge lock (does not fail immediately).
5. Vendor-sync completes, releases lock.
6. Fix pipeline acquires lock and completes.

### FR-10: Multi-patch conflict test

**`test_multiple_patches_partial_conflict`**

1. Setup: 3 patches. Patch 2 conflicts with upstream.
2. Run sync with `on_conflict="skip"`.
3. Assert: patches 1 and 3 are applied; patch 2 is skipped and marked
   `"conflicting"`.
4. Assert: deploy branch contains patches 1 and 3 but not patch 2.
5. Assert: manifest shows patch 2 as `"conflicting"`.

## Non-Functional Requirements

- **Test isolation** — each test uses its own temporary directory and git
  repos. No shared state between tests.
- **No network access** — all git operations use local bare repos. Platform
  API calls are mocked.
- **Reasonable speed** — individual tests should complete in under 30 seconds.
  Git operations on small synthetic repos are fast.
- **Test markers** — all integration tests are marked with
  `@pytest.mark.integration` so they can be run separately via
  `make test-integration`.

## Design Decisions

1. **Synthetic repos, not fixtures.** Each test creates its own git repo
   ecosystem from scratch. This avoids test interdependence and makes each
   test self-documenting.

2. **Merge agent is mocked.** The AI merge agent is expensive and
   non-deterministic. Integration tests mock it to return a predictable
   resolution. This tests the plumbing (rerere → agent → rerere records)
   without the cost of API calls.

3. **Platform API is mocked.** Tests that check patch lifecycle (merged
   upstream) mock the platform's `get_issue()` response. This tests the
   detection logic without requiring a GitHub token.

4. **Tree comparison, not SHA comparison.** Rebuild idempotency is tested by
   comparing file contents (tree), not commit SHAs. Commit SHAs differ
   between rebuilds due to timestamps and author info, but the tree should be
   identical.

5. **Both strategies tested.** The `merge-no-ff` and `linear` strategies
   produce different commit histories but the same file tree. Testing both
   validates strategy dispatch and confirms functional equivalence.

6. **Merge lock test uses asyncio.** The merge lock coordination test runs
   two async tasks concurrently to verify lock behavior. This tests the
   real `MergeLock` implementation, not a mock.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 02 (vendor_git_infrastructure) | Upstream | All git primitives |
| Spec 03 (patch_stack_module) | Upstream | Manifest, rebase, rebuild |
| Spec 04 (vendor_config) | Upstream | Configuration |
| Spec 05 (rerere_integration) | Upstream | Rerere setup and resolution |
| Spec 06 (vendor_sync_stream) | Upstream | Sync cycle |
| Spec 07 (vendor_fix_pipeline) | Upstream | Fix integration in vendor mode |
| Spec 08 (vendor_cli) | Upstream | CLI commands |
| `pytest` | External | Test framework |
| `click.testing.CliRunner` | External | CLI testing |
