---
spec_id: '08'
spec_name: vendor_cli
title: Vendor CLI Commands
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor CLI Commands

## Summary

Add CLI commands to nightshift and `af` for manual interaction with the
vendor-branch workflow: one-shot vendor sync, on-demand deploy branch rebuild,
and patch manifest management (list, add, remove). These commands provide
operator control over operations that the vendor-sync daemon stream handles
automatically.

## Goals

1. **Manual vendor sync** — `nightshift vendor-sync` runs a single sync cycle
   (fetch upstream, check patches, rebase, rebuild) without starting the
   daemon.
2. **On-demand rebuild** — `nightshift rebuild` rebuilds the deploy branch
   from the current manifest without checking upstream.
3. **Patch management** — `nightshift patches` subcommands list, add, and
   remove patches from the manifest.
4. **Both CLIs** — `af vendor-sync`, `af rebuild`, and `af patches` provide
   the same functionality from the `af` CLI for users who prefer it. Both
   CLIs delegate to the same library functions.
5. **Consistent UX** — all commands follow existing CLI conventions (Click,
   `--json` output, exit codes, confirmation prompts).

## Non-Goals

- **Daemon management via these commands** — starting/stopping the nightshift
  daemon is handled by the existing `nightshift` / `nightshift stop` commands.
  These new commands are one-shot operations.
- **Upstream PR creation** — `nightshift patches upstream <branch>` (creating
  a PR against the upstream repo from a patch branch) is a future enhancement,
  not part of this spec.
- **Patch reordering via CLI** — the `reorder_patches` library function exists
  (spec 03) but is not exposed via CLI in this spec. Operators edit
  `patches.toml` directly to reorder.
- **Interactive conflict resolution** — conflicts during sync/rebuild are
  handled by rerere + merge agent. The CLI reports results but does not
  prompt for manual resolution.

## Background

The vendor-sync stream (spec 06) runs automatically as part of the nightshift
daemon. However, operators need manual control for:
- Initial setup and testing before enabling the daemon stream.
- On-demand rebuilds after manually resolving a conflicting patch.
- Adding patches that don't originate from nightshift fixes (e.g., feature
  patches carried from upstream PRs not yet merged).
- Inspecting the current patch set and their statuses.

The `af` CLI already has a pattern of commands that mirror nightshift
functionality (e.g., `af code` mirrors nightshift's coding pipeline). Adding
`af vendor-sync`, `af rebuild`, and `af patches` follows this pattern.

## Tech Stack

- **Language:** Python 3.12+
- **CLI framework:** Click
- **Test framework:** pytest with `click.testing.CliRunner`

## Functional Requirements

### FR-1: `nightshift vendor-sync`

A Click command registered on the nightshift CLI group.

**Behavior:**
1. Load config. Verify `vendor.enabled = True`. If not, exit with code 1 and
   message: `"Vendor mode is not enabled. Set vendor.enabled = true in config."`.
2. Run a single vendor-sync cycle: the same logic as
   `VendorSyncStream.run_once()` (spec 06 FR-3).
3. Print a summary to stdout:
   - Upstream SHA (before → after, or "unchanged").
   - Patches merged upstream (list of branch names, or "none").
   - Patches rebased (count).
   - Patches conflicting (list of branch names, or "none").
   - Rebuild result (success/failure, tag if created).
4. Exit code 0 on success, 1 on failure (upstream fetch failed, rebuild failed).

**Flags:**
- `--json` — output summary as JSON instead of human-readable text.
- `--dry-run` — fetch upstream and check patch status, but do not rebase,
  rebuild, or push. Reports what would happen.

**JSON output:**
```json
{
  "upstream_sha_before": "abc123",
  "upstream_sha_after": "def456",
  "patches_merged": ["feature/foo"],
  "patches_rebased": 3,
  "patches_conflicting": [],
  "rebuild_success": true,
  "tag": "deploy-2026-08-10"
}
```

### FR-2: `nightshift rebuild`

A Click command registered on the nightshift CLI group.

**Behavior:**
1. Load config. Verify `vendor.enabled = True`. Same guard as FR-1.
2. Load manifest. If manifest does not exist, exit with code 1 and message:
   `"No patch manifest found at {path}. Run 'nightshift vendor-sync' first."`.
3. Call `rebuild_integration(repo_path, manifest, deploy_branch, strategy)`.
4. Print rebuild summary to stdout (applied, skipped, conflicting patches,
   tag if created).
5. Exit code 0 on success, 1 on failure.

**Flags:**
- `--json` — output as JSON.
- `--strategy <merge-no-ff|linear>` — override the configured rebuild
  strategy for this invocation only.
- `--no-tag` — skip tagging even if `tag_rebuilds = true` in config.

**JSON output:**
```json
{
  "success": true,
  "deploy_branch": "deploy",
  "upstream_sha": "def456",
  "applied_patches": ["feature/foo", "feature/bar"],
  "skipped_patches": [],
  "conflicting_patches": [],
  "tag": "deploy-2026-08-10"
}
```

### FR-3: `nightshift patches`

A Click group registered on the nightshift CLI group, with subcommands.

#### FR-3a: `nightshift patches list`

**Behavior:**
1. Load manifest. If not found, exit with code 1.
2. Print a table of patches:
   ```
   #  Branch              Status      Added       Upstream PR
   1  feature/foo         active      2026-08-05  https://github.com/upstream/repo/pull/123
   2  feature/bar         active      2026-08-10  —
   3  fix/42-login-bug    conflicting 2026-08-09  —
   ```
3. Print upstream info: remote, branch, last synced SHA.

**Flags:**
- `--json` — output as JSON.
- `--status <active|merged|conflicting>` — filter by status.

#### FR-3b: `nightshift patches add <branch>`

**Behavior:**
1. Load manifest.
2. Verify that `<branch>` exists as a local git branch. If not, exit with
   code 1 and message: `"Branch '{branch}' does not exist locally."`.
3. Call `add_patch(manifest, branch, upstream_pr, description)`.
4. Save manifest.
5. Print confirmation: `"Added patch '{branch}' to manifest."`.

**Flags:**
- `--upstream-pr <url>` — optional upstream PR URL.
- `--description <text>` — optional description.
- `--json` — output as JSON.

#### FR-3c: `nightshift patches remove <branch>`

**Behavior:**
1. Load manifest.
2. Call `remove_patch(manifest, branch)`.
3. Save manifest.
4. Print confirmation: `"Removed patch '{branch}' from manifest."`.
5. The local git branch is NOT deleted. The operator deletes it manually if
   desired.

**Flags:**
- `--json` — output as JSON.
- `--yes / -y` — skip confirmation prompt. Without `--yes`, prompt:
  `"Remove patch '{branch}' from manifest? This does not delete the git branch. [y/N]"`.

### FR-4: `af` CLI equivalents

Register the following commands on the `af` CLI group in
`packages/af/af/app.py`:

- `af vendor-sync` — delegates to the same function as
  `nightshift vendor-sync`.
- `af rebuild` — delegates to the same function as `nightshift rebuild`.
- `af patches` — delegates to the same subcommands as `nightshift patches`.

Implementation: the Click command functions are defined in shared modules
(either in `agentfox` or in a shared CLI utilities module) and registered
on both CLI groups. No code duplication.

### FR-5: Daemon guard

`nightshift vendor-sync` and `nightshift rebuild` check the nightshift PID
file. If the daemon is running, exit with code 1 and message:
`"Nightshift daemon is running (PID {pid}). Stop it before running manual sync/rebuild."`.

This prevents concurrent sync/rebuild operations between the daemon and
manual CLI invocations.

`nightshift patches list` does NOT check the daemon PID (read-only operation).
`nightshift patches add` and `nightshift patches remove` DO check the daemon
PID (manifest writes could conflict with daemon operations).

### FR-6: Documentation

Update `docs/cli-reference.md` with the new commands, flags, and examples.

## Non-Functional Requirements

- **Exit codes** — 0 success, 1 error. Consistent with existing CLI commands.
- **JSON output** — all commands support `--json` for machine-readable output.
  Consistent with existing `af plan --json`, `af standup --json`.
- **Test coverage** — >=90% line coverage on all CLI commands. Tests use
  `click.testing.CliRunner` following existing test patterns.
- **No daemon dependency** — all commands work without the daemon running
  (except the daemon guard check).

## Design Decisions

1. **Both CLIs.** Having the commands on both `nightshift` and `af` follows
   the existing pattern where `af` is the primary user-facing CLI and
   `nightshift` is the daemon-specific CLI. Users can use whichever they
   prefer.

2. **Shared implementation.** CLI command functions are thin wrappers around
   library functions in `agentfox`. No logic in the CLI layer beyond argument
   parsing, config loading, and output formatting. Both `af` and `nightshift`
   commands call the same library functions.

3. **`patches remove` does not delete the branch.** The manifest and git
   branches are separate concerns. Removing a patch from the manifest means
   "stop carrying this patch" — the branch may still be useful for reference
   or for creating an upstream PR. The operator deletes the branch explicitly
   if desired.

4. **Daemon guard for write operations.** Read operations (`patches list`)
   are safe to run concurrently with the daemon. Write operations
   (`vendor-sync`, `rebuild`, `patches add/remove`) could conflict with
   daemon operations and are guarded.

5. **`--dry-run` on vendor-sync only.** A dry-run rebuild is less useful
   (you'd just run the rebuild and inspect the result). Dry-run vendor-sync
   is valuable for verifying what would happen before enabling the daemon.

6. **No `patches reorder` command.** Reordering patches requires careful
   consideration of dependencies. Exposing it via CLI risks accidental
   misordering. Operators edit `patches.toml` directly, which provides full
   control and makes the ordering decision explicit.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| Spec 03 (patch_stack_module) | Upstream | `load_manifest`, `save_manifest`, `add_patch`, `remove_patch`, `rebuild_integration` |
| Spec 04 (vendor_config) | Upstream | `VendorConfig` for validation and defaults |
| Spec 06 (vendor_sync_stream) | Upstream | `VendorSyncStream.run_once()` or equivalent library function |
| `nightshift/app.py` | Internal | CLI command registration |
| `af/app.py` | Internal | CLI command registration |
| `docs/cli-reference.md` | Internal | Documentation update |
