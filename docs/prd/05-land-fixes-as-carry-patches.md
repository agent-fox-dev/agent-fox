# Land fixes as carry patches

Status: **proposed** (2026-10-20). Nothing here is implemented. This is
step 5 of [PRD 01](01-triage-and-fix-forge-issues-as-a-service.md)'s
order of work (§10): PRD 01 §5 whole, on [PRD 03](03-run-the-agent-fox-service.md)'s
worker and [PRD 04](04-drive-the-service-from-the-forge.md)'s event
stream. Written to PRD 01's decisions D7 and D12.

## Intent

A `carry_patch` workspace on hub is the lifecycle of an agent's output on
a fork: an ordered list of patch branches, an integration branch rebuilt
as upstream plus the patches, merged patches detected and retired,
conflicts named by file. PRD 03's service refuses fix jobs on such a
workspace. This PRD makes them land: a fix becomes a patch branch pushed
to hub, registered, rebuilt by hub, optionally proposed upstream, and a
rebuild that leaves the patch in `conflict` becomes a fix job of its own.
The service never commits to the integration branch, never pushes to the
upstream, never pushes to the fork itself.

## Goals

1. A fix job on a `carry_patch` workspace lands as a patch branch
   `patch/<N>-<slug>` cut from the current upstream base, pushed to hub's
   git server, registered with `if_not_exists`, and reported `af:landed`
   once hub's rebuild applied it.
2. In `origin` mode the push is forwarded by hub (D12): the service
   requires `PUSH_PATCHES_TO_ORIGIN=true` at preflight and refuses before
   any model runs without it, naming the variable.
3. With `AF_FIX_UPSTREAM_PR=true` the patch is proposed upstream through
   hub's forge API and the patch's `upstream_pr_url` is set, so hub's
   sync retires it when it merges.
4. A rebuild that leaves a service-registered patch in `conflict` creates
   a fix job whose brief names the conflicting files; the resolved branch
   is pushed to the same target and the next rebuild applies it.
5. The never-do list of PRD 01 §5.4 is enforced by construction, not by
   the brain's good behaviour.

## Non-goals

- **Rebuilding, syncing or reordering patches** beyond what landing one
  requires (one `sync` call in `origin` mode). The stack is hub's.
- **Fixing conflicts in patches the service did not register**, unless
  `AF_FIX_FOREIGN_CONFLICTS=true`.
- **Gating the stack** (running the checks on the integration branch).
  PRD 01 and `docs/architecture.md` name it as hub's; a later PRD.
- **Choosing the patch's position** beyond `first` or `last`.

## 1. Preflight

Before the brief, for a `carry_patch` workspace (from `GET
/workspaces/:slug`):

| Check | Refusal |
|---|---|
| `PATCH_BRANCH_SOURCE` (from `vars/resolved`) is `origin` and `PUSH_PATCHES_TO_ORIGIN` is not `true` | `usage`, `fix_hint: {var: "PUSH_PATCHES_TO_ORIGIN", suggest: "true"}`, the issue commented once |
| `upstream_head_sha` is null (never synced) | `usage`, naming `afc workspace sync` |
| `AF_FIX_LAND` is `pr` | `usage`: a carry-patch workspace lands `patch`; `none` and `branch` are allowed |

## 2. The branch

- The base is the upstream base as the last sync left it:
  `refs/remotes/upstream/HEAD` in hub's trunk, which the clone from
  `hub_url` has as the integration branch's first parent; the worker
  resolves it as `upstream_head_sha` from the workspace record and
  checks it out detached.
- The branch is `patch/<N>-<slug>` (`gitx.BranchName("patch", N, title)`),
  unique per `gitx.UniqueBranchName`; the brief says the change must
  apply on this base and nothing else.
- The gate runs on the base and after, as for a standard fix; the
  revert check decides `Closes` versus `Refs` for the comment, not for
  a pull request, unless §4.

## 3. Landing

```
push       git push <hub_url> patch/<N>-<slug>        the PAT; in origin mode hub forwards to the fork before writing its ref
register   POST /workspaces/:slug/patches {branch_name, description: "<title> (<issue url>)", if_not_exists: true, position?: 1 when AF_PATCH_POSITION=first}
rebuild    hub mode: the push hook enqueued it (AUTO_REBUILD_AFTER_PUSH); origin mode: POST /workspaces/:slug/sync → rebuild_job_id
watch      hub.rebuild.* on the event stream for the workspace, or GET /workspaces/:slug/rebuilds/:id every 15 s up to AF_REBUILD_TIMEOUT (30 m)
outcome    completed with this patch success → af:landed; the comment names the patch id, its position and integration_head_sha
           failed or this patch conflict → af:needs-human? no: §5, a conflict job; the label stays af:in-progress until it ends
           AUTO_REBUILD_AFTER_PUSH=false in hub mode → af:landed with a warning rebuild_not_triggered naming afc rebuild submit
```

A push hub refuses (`ng <ref> <message>`: the forward failed, or the
branch is registered and forwarding is off) is a `git` failure with hub's
message, the attempt fails, and the comment carries it.

## 4. The upstream pull request

With `AF_FIX_UPSTREAM_PR=true`: `POST /workspaces/:slug/pulls?target=upstream
{title, body, head: "<fork-owner>:patch/<N>-<slug>", base: <upstream default branch from GET /forge/repository?target=upstream>, draft when the verdict did not land}`
through hub (the upstream credential is hub's), then `PATCH
/workspaces/:slug/patches/:id {upstream_pr_url}`. Hub's sync detects the
merge, squash included, and retires the patch. The comment names the
pull request. In `hub` mode without a mirror the fork has no branch to
open a pull request from: the service refuses this flag at preflight
unless `PUSH_PATCHES_TO_ORIGIN=true`.

## 5. A conflict is a fix

A `hub.rebuild.completed` or `hub.rebuild.failed` event (or the polled
rebuild) whose `patch_results` names a patch the service registered with
`status: conflict` creates a fix job:

- `kind: fix`, `source.kind: rebuild`, `source.by: <rebuild id>`, input
  the patch's issue (from the registration description), `parent_job_id`
  the landing job.
- The brief's first section: the patch branch, the new upstream base, the
  `conflict_files`, the rule: resolve these files so the patch applies on
  this base, keep the patch's intent, change nothing else.
- The worker rebases the branch onto the new base up to the first
  conflict (`git rebase` with the hermetic runner, stopped at the
  conflict), syncs that tree in, and the brain resolves; the gate runs
  on the resolved tree; `af fix land` continues the rebase, pushes the
  rebased branch (force-with-lease, the one allowed rewrite, because the
  branch is the service's own), and the next rebuild applies it.
- The issue's label stays `af:landed` while the conflict job runs and
  becomes `af:needs-human` with the conflict comment when it cannot be
  resolved within `AF_MAX_ATTEMPTS`.
- Patches not registered by the service are ignored unless
  `AF_FIX_FOREIGN_CONFLICTS=true`, in which case the issue is unknown and
  the job's input is the patch itself (`input.patch`), with the comment
  going nowhere but the job record.

The same shape serves a rebuild hub started for an upstream advance.

## 6. What the service never does

By construction: the worker has no upstream remote (the clone is from
`hub_url`), pushes only to `hub_url`, never checks out the integration
branch for writing, never calls `POST /rebuild` while `GET /rebuilds`
shows one running, and the brain's box has no credential at all.

## 7. Acceptance criteria

Offline, against the `httptest` hub serving a `carry_patch` workspace,
patches, rebuilds and the forge:

1. A fix on a `hub`-mode workspace pushes `patch/42-…` to `hub_url`,
   registers it with `if_not_exists: true`, reads the rebuild (served
   `completed`, the patch `success`) and sets `af:landed` with the
   comment naming the patch id and the new integration head.
2. On an `origin`-mode workspace with `PUSH_PATCHES_TO_ORIGIN=true` the
   same, plus one `POST /sync`; with the variable unset the job fails at
   preflight with `usage` naming the variable, no model called, one
   comment on the issue.
3. With `AF_FIX_UPSTREAM_PR=true`, a `POST /pulls?target=upstream` with
   `head: <fork-owner>:patch/…` and a `PATCH /patches/:id` with its URL
   follow the registration.
4. A rebuild event with the patch in `conflict` and `conflict_files`
   creates a fix job whose brief names the files, whose landing pushes
   the rebased branch with force-with-lease, and which ends `af:landed`
   when the next served rebuild is `completed`; a conflict in a patch
   the service did not register creates no job unless
   `AF_FIX_FOREIGN_CONFLICTS=true`.
5. The worker never pushes to `git_url` or `upstream_url` (every push
   observed targets `hub_url`), never sends `POST /rebuild`, and the
   integration branch's ref in the clone is unchanged after a job.
6. `AUTO_REBUILD_AFTER_PUSH=false` in hub mode lands with the
   `rebuild_not_triggered` warning and the comment naming `afc rebuild
   submit`.

## 8. What is given up, stated plainly

- **The rebuild is hub's clock.** A landing waits up to
  `AF_REBUILD_TIMEOUT` for hub; a slow stack delays `af:landed`, and a
  timeout is `af:landed` with a warning, not a failure, because the
  patch is registered.
- **One rewrite is allowed**, the force-with-lease of the service's own
  patch branch after a conflict resolution. It is the branch's purpose.
- **No stack gate.** Two green patches can be red together; the
  architecture document assigns that to hub.

## Documentation

| Change | Update |
|---|---|
| the carry-patch landing, the conflict job, the preflight | `docs/architecture.md` (the carry-patch section), `docs/cli.md` (the warnings and categories) |
| `AF_FIX_UPSTREAM_PR`, `AF_PATCH_POSITION`, `AF_FIX_FOREIGN_CONFLICTS`, `AF_REBUILD_TIMEOUT` | `docs/configuration.md` |
| the events consumed | `docs/api.md` |
