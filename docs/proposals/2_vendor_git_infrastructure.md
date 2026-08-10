---
spec_id: '02'
spec_name: vendor_git_infrastructure
title: Vendor Git Infrastructure Extensions
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor Git Infrastructure Extensions

## Summary

Extend the git utility layer in `packages/agentfox/agentfox/workspace/git.py`
with operations required by a vendor-branch / carry-patch workflow: multi-remote
management, tagging, cherry-pick, three-argument rebase, and non-fast-forward
merge. These primitives are consumed by downstream specs (03-08) but have no
behavioral effect on existing workflows when unused.

## Goals

1. **Multi-remote support** -- callers can add, remove, list, and fetch from
   arbitrary named remotes (not just `origin`), enabling the `upstream` +
   `origin` (fork) dual-remote model.
2. **Tag operations** -- create lightweight and annotated tags, push tags to a
   remote, delete local tags, and list tags by pattern. This enables deployment
   tagging (`deploy-YYYY-MM-DD`) in the vendor-sync stream.
3. **Cherry-pick** -- apply one or more commits by SHA onto the current branch,
   with conflict detection. This supports the linear rebuild strategy where
   patches are replayed as individual commits.
4. **Three-argument rebase** -- `git rebase --onto <newbase> <upstream> <branch>`
   for transplanting a patch branch from one base to another without replaying
   commits that already exist on the new base.
5. **Non-fast-forward merge** -- `git merge --no-ff` with an explicit message,
   preserving patch boundaries in the merge-no-ff rebuild strategy.
6. **Zero regressions** -- existing callers of `git.py` functions are unaffected.
   All new functions are additive; no existing signatures change.

## Non-Goals

- **Submodule or subtree support** -- out of scope for this workflow.
- **Interactive rebase** -- not needed; all rebases are non-interactive.
- **`git stash`** -- dirty-state handling continues to use reset/checkout/clean.
- **Git hook suppression** -- no `--no-verify` flags; hooks continue to fire as
  before.
- **Changing existing caller behavior** -- no existing call site is modified in
  this spec. Downstream specs (06, 07) will update callers to pass non-`origin`
  remotes when appropriate.

## Background

The current git layer (`workspace/git.py`) supports a single-remote model.
`push_to_remote` and `fetch_remote` accept a `remote` parameter, but every
call site in the codebase passes `"origin"`. There are no tag, cherry-pick, or
three-argument rebase operations. The only merge variant is `git merge --squash`
in `harvest.py`.

The vendor-branch workflow described in `docs/proposals/github_pr.md` requires
a dual-remote model (`upstream` for the vendor repo, `origin` for the fork),
integration-branch rebuilds via merge-no-ff or cherry-pick, and deployment
tagging. This spec adds the git primitives; composition into higher-level
workflows is handled by specs 03 (patch stack), 06 (vendor sync), and 07 (fix
pipeline adaptation).

## Tech Stack

- **Language:** Python 3.12+
- **Async runtime:** asyncio (via existing `run_git`)
- **Test framework:** pytest with subprocess mocking

## Functional Requirements

### FR-1: Remote management

Add the following functions to `git.py`:

**`add_remote(repo_path, name, url)`**
- Runs `git remote add {name} {url}`.
- Validates `name` with `validate_ref_name()`.
- Idempotent: if the remote already exists with the same URL, returns
  successfully. If it exists with a different URL, raises `WorkspaceError`.

**`remove_remote(repo_path, name)`**
- Runs `git remote remove {name}`.
- Idempotent: if the remote does not exist, returns successfully.

**`list_remotes(repo_path)`**
- Runs `git remote -v`.
- Returns `dict[str, str]` mapping remote name to fetch URL.

**`get_remote_url(repo_path, remote)`** already exists -- no change needed.

### FR-2: Generalized fetch

**`fetch_from(repo_path, remote, refspec=None)`**
- Runs `git fetch {remote}` or `git fetch {remote} {refspec}`.
- Uses `_GIT_REMOTE_TIMEOUT` (120s).
- Returns `bool` (success/failure), never raises -- matches `fetch_remote`
  pattern.
- The existing `fetch_remote` function is preserved for backward compatibility
  but its implementation is updated to delegate to `fetch_from`.

### FR-3: Tag operations

**`create_tag(repo_path, name, ref, message=None)`**
- Lightweight tag: `git tag {name} {ref}`.
- Annotated tag (when `message` is not None): `git tag -a {name} {ref} -m {message}`.
- Validates `name` with `validate_ref_name()`.
- Raises `WorkspaceError` if tag already exists.

**`push_tags(repo_path, remote, tag_name=None)`**
- Specific tag: `git push {remote} refs/tags/{tag_name}`.
- All tags: `git push {remote} --tags`.
- Uses `_GIT_REMOTE_TIMEOUT`.
- Returns `bool`, never raises.

**`delete_tag(repo_path, name)`**
- Runs `git tag -d {name}`.
- Idempotent: returns successfully if tag does not exist.

**`list_tags(repo_path, pattern=None)`**
- Runs `git tag --list` or `git tag --list {pattern}`.
- Returns `list[str]` of tag names.

### FR-4: Cherry-pick

**`cherry_pick(repo_path, *commits)`**
- Runs `git cherry-pick {commit1} {commit2} ...`.
- Raises `IntegrationError(retryable=False)` on conflict.
- The caller is responsible for aborting on failure
  (`git cherry-pick --abort`).

**`abort_cherry_pick(repo_path)`**
- Runs `git cherry-pick --abort`.
- Best-effort (check=False).

### FR-5: Three-argument rebase

**Extend `rebase_onto`** to support the three-argument form:

Current signature: `rebase_onto(repo_path, branch, onto)`
New signature: `rebase_onto(repo_path, branch, onto, upstream=None)`

- Two-argument (current): `git rebase {onto} {branch}` (unchanged).
- Three-argument: `git rebase --onto {onto} {upstream} {branch}`.
- Raises `IntegrationError` on conflict (unchanged).
- `abort_rebase` is unchanged.

### FR-6: Non-fast-forward merge

**`merge_no_ff(repo_path, branch, message=None)`**
- Runs `git merge --no-ff {branch}` or `git merge --no-ff {branch} -m {message}`.
- Raises `IntegrationError(retryable=False)` on conflict.
- The caller is responsible for aborting on failure (`git merge --abort`).

**`abort_merge(repo_path)`**
- Runs `git merge --abort`.
- Best-effort (check=False).

### FR-7: Remote timeout classification

Update the remote-command detection in `run_git` to recognize any new
subcommands that interact with remotes. Currently detected: `fetch`, `push`,
`pull`, `clone`, `ls-remote`. No new remote subcommands are introduced by this
spec (all new operations are local except `push_tags` and `fetch_from`, which
use `push` and `fetch` subcommands respectively and are already detected).

### FR-8: Public API exports

Add all new functions to `workspace/__init__.py`:
- `add_remote`, `remove_remote`, `list_remotes`
- `fetch_from`
- `create_tag`, `push_tags`, `delete_tag`, `list_tags`
- `cherry_pick`, `abort_cherry_pick`
- `merge_no_ff`, `abort_merge`

The extended `rebase_onto` signature is backward-compatible (new parameter is
optional with default `None`).

## Non-Functional Requirements

- **No existing test regressions** -- `make check` passes before and after.
- **Timeout behavior** -- remote operations use 120s timeout; local operations
  use 60s timeout. Matches existing pattern.
- **Error typing** -- new errors use existing `WorkspaceError` and
  `IntegrationError` types from `core/errors.py`. No new error types.
- **Security** -- all ref/tag/remote names pass through `validate_ref_name()`
  before use in git commands. Remote URLs are not validated (git handles this).
- **Test coverage** -- >=90% line coverage on all new functions.

## Design Decisions

1. **`fetch_from` vs modifying `fetch_remote`.** Both are kept. `fetch_remote`
   is a widely-used function with callers that expect `(repo, branch, remote)`
   signature. `fetch_from` uses `(repo, remote, refspec)` -- remote-first, since
   the refspec is optional. `fetch_remote` delegates to `fetch_from` internally.

2. **`rebase_onto` extended, not replaced.** The new `upstream` parameter
   defaults to `None`, preserving backward compatibility. Callers using the
   two-argument form are unaffected.

3. **Cherry-pick raises, merge_no_ff raises.** These operations produce
   conflicts that require explicit resolution. Raising (rather than returning
   bool) forces callers to handle conflicts, matching the pattern of
   `rebase_onto`.

4. **Tag names validated with `validate_ref_name`.** Tags live in the same ref
   namespace as branches and have the same naming constraints.

5. **`add_remote` is idempotent for same URL.** This prevents errors when the
   vendor-sync stream runs setup on a repo that already has the upstream remote
   configured.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| `workspace/git.py` | Internal | Extended with new functions |
| `workspace/__init__.py` | Internal | Updated exports |
| `core/errors.py` | Internal | Uses existing `WorkspaceError`, `IntegrationError` |
| Specs 03-08 | Downstream consumers | Depend on primitives added here |
