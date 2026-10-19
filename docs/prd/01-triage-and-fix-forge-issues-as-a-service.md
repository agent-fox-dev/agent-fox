# Triage and fix forge issues as a service

Status: **workshop draft, first pass** (2026-10-19). Nothing here is
implemented. The decisions in §0 are recommendations with their reasoning;
each is a question for the workshop until it is marked decided. The rest of
the document is written to the recommendations so the shape can be judged
whole. Details left to settle at spec time are marked *spec time*.

Builds on [`docs/architecture.md`](../architecture.md) (the brain, the
toolbelt and the hands), AgentKit
[PRD 11](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/11-run-tools-in-an-isolated-sandbox.md)
(sandboxes and the gateway) and
[PRD 12](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/12-simplify-the-embedder-contract.md)
(toolset, runner, `mcpserve`), and on hub's workspaces, git server, carry
patches, sessions and audit as they exist today. It changes nothing in
AgentKit; what it asks of hub is listed in §9 and is small.

## Intent

agent-fox is autonomous agentic software development for Git-hosted
organizations: GitHub, GitLab and, later, Forgejo. The product promise is
that a team writes up a bug or a change request where it already writes
them, an issue on its forge, and comes back to a diagnosed issue and then to
a pull request, without anyone having opened a terminal in between.

Today that work is a set of tools a person runs by hand on a machine that
happens to have them installed, against a checkout that happens to be
there. The previous implementation (`triage`, `fix`, `impl`; last at
`1e6458d`) had the right contracts, one input, one JSON envelope, one
exit-code table, a `needs_human` shape and a `next` suggestion, but no
process that ran them unattended, no notion of who asked, and no place a
result lived except stdout and a state directory on that machine.

This PRD adds the **agent-fox service**: one long-running process, local on
a developer's machine or on a server, that

- watches the forge for issues a team has labelled for it, or is told about
  them by a webhook, or is asked outright from a CLI;
- runs the **triage** agent on an issue and writes the diagnosis back to
  it, marking it ready;
- runs the **fix** agent on a triaged issue, lands the change as a pull
  request or as a carry patch, and says so on the issue;
- does all of that in a sandbox, with hub as the authority on who the
  caller is, which repository a workspace is, where a landed branch goes
  and what it cost.

It is built on `agentkit-go` in the same shape as hub: **API first, with a
small CLI as its client.** The service is the only thing that holds a
sandbox provider, a queue and the forge credentials; the CLI holds a hub
credential and nothing else, and can run the same pipeline locally when
there is no server to talk to.

The one rule of `docs/architecture.md` stands and is the design constraint
for everything below: **the brain never touches git, the forge or hub
except through the program, and the program never runs a model except
through a brain it was handed.**

## 0. Decisions to workshop

Each is a question, a recommendation, and the reasoning. The rest of the
document assumes the recommendation.

### D1. Two binaries, named like hub's: `agent-fox` the server, `af` the client

**Recommended.** Hub is `hub` (the server) and `afc` (its CLI). agent-fox is
`agent-fox` (`cmd/agent-fox`: the API, the queue, the workers) and `af`
(`cmd/af`: the client, and the local runner). `af` is also the name
`docs/architecture.md` gives the toolbelt a brain calls (`af gate run`,
`af fix land`, …); that is deliberate: the toolbelt commands are the ring-1
and ring-2 commands the service's workers run, and the same binary serves
them to a person at a shell. One binary a brain is handed, one binary a
person installs. The alternative, a third binary for the toolbelt, is one
more thing to version against the service.

### D2. Hub is the identity provider; agent-fox has no users of its own

**Recommended.** Every request to the service carries a hub credential
(`Authorization: Bearer <api key or PAT>`), and the service validates it by
asking hub (`GET /api/v1/user`) and caches the answer briefly. Which
workspaces a caller may act on is hub's answer too: the service looks a
workspace up with the caller's own token (`GET /api/v1/workspaces/:slug`),
and a 404 from hub is a 404 from the service. No user table, no OAuth
callback, no admin token of its own; `af login` is `afc login`'s credential
file (`~/.af/config.toml`), read as is.

The cost is a hub round trip per request, and that hub's ownership model
today is owner-private: an org member cannot see a workspace they do not
own ([`permissions.md`](https://github.com/agent-fox-dev/hub/blob/main/docs/permissions.md),
"Ownership enforcement"). The inbox draft already names that as hub work to
land first; this PRD inherits the dependency rather than working around it.

### D3. The service holds a hub PAT per subscribed workspace, for the work it does when nobody is calling

**Recommended.** A poll, a webhook firing and a rebuild it watches all
happen with no caller on the line. For those the service needs a hub
credential bound to the workspace: a PAT the subscriber mints and hands over
at subscribe time, with exactly the scopes §6 lists (`workspaces:read`,
`vars:read`, `git:read` and `git:write`, `patches:write`, `rebuilds:read`,
`sessions:write`, `audit:write`). The service stores it encrypted at rest
under a key from its own configuration, and uses it for every background
action on that workspace. The caller's own token is used for everything a
caller asks for directly.

Hub's README names a "workspace-scoped token" as a third credential type and
the audit ingestion rules mention "workspace-scoped PATs must match the URL
slug"; whether that is a PAT with a workspace binding or something else is
*to confirm with hub*. If it exists, it is the credential this decision
wants, minted by hub rather than by the subscriber.

### D4. The forge credential and the vendor key are the service's configuration, not hub secrets

**Recommended, and the one to argue about.** Hub stores secrets and resolves
them only inside its own jobs; its API lists names and never returns a
value. An external service therefore cannot read a workspace's `GIT_PAT` or
an `ANTHROPIC_API_KEY` stored there. Three ways out:

- **(a) agent-fox configuration.** Forge credentials per forge host or per
  org in the service's `config.toml` (`[[forges]]`, `${VAR}` expansion as
  hub does), the vendor key in the environment of the service or the
  gateway. One place, no hub change, and the shape a GitHub App
  installation needs later anyway. **Recommended for the first release.**
- **(b) A hub endpoint that resolves a workspace secret** to a credential
  that holds a new scope (`secrets:resolve`) and is bound to that
  workspace. Keeps one secret store; it is also the first endpoint that
  ever returns a secret value, which is a hub policy decision, not a
  feature.
- **(c) Hub runs the sandbox** and injects resolved secrets into it, as the
  hub tasks draft has it. Then the service is not a service beside hub but
  a task type inside it. That is the convergence §10 names and is not the
  first release.

(a) first; (b) if the hub maintainers want one store; (c) is the roadmap.

### D5. Labels are the trigger and the visible status; the service's own records are the state

**Recommended.** A team asks for triage or a fix by putting a label on an
issue, and reads the outcome as a label, because that is what a forge
renders in every list, board and notification. The previous PRD 12 refused
a label state machine for the tools, for a reason that still holds: labels
have to exist, be spelled the project's way and be creatable by the token.
So here the labels are a *protocol the service owns*, created by the service
on subscribe (`CreateLabel` is in `issuex`), under a prefix the workspace
may change (`af:` by default), and the truth about a job is the service's
job record, which the labels mirror. A label a person removes or adds by
hand is read as a request, never as state the service must reconcile.

### D6. Triage writes into the issue body, in a region it owns

**Recommended, over a comment.** The ask is "the triage agent investigates
and updates the issue". A diagnosis in a comment is buried after three
replies; a diagnosis in the body is what the fix agent and every reader
open first. The previous PRD 12's design decision 1 (never rewrite a
person's body) is kept in its load-bearing part: the service **appends** a
marked region (`<!-- af:triage -->` … `<!-- /af:triage -->`) below the
person's text, rewrites only that span on a later triage, and leaves
everything outside it byte for byte. A person who deletes the markers has
taken the body over; the service then comments instead and says so. The
CLI's text input, which has no issue yet, creates one whose whole body is
the region.

### D7. A fix lands as a pull request on a standard workspace and as a carry patch on a carry-patch workspace

**Recommended.** Hub's workspace mode decides. On a `standard` workspace the
branch goes to the forge and a pull request is opened against the
workspace's branch, `Closes` or `Refs` the issue, as the old `fix` did. On
a `carry_patch` workspace the branch is a patch, `patch/<n>-<slug>`, pushed
where `PATCH_BRANCH_SOURCE` says (the fork in `origin` mode, hub's git
server in `hub` mode), registered with `POST /patches` (`if_not_exists`),
rebuilt by hub, and an upstream pull request is opened only when the
workspace asks for it (`AF_FIX_UPSTREAM_PR=true`). The service watches the
rebuild and turns a `conflict` into a fix job of its own (§5.3).

### D8. Two brains in the first release: the AgentKit driver for triage, a headless harness for fix

**Recommended.** Triage is read-only and tool-light: six read tools and a
terminating `file_diagnosis` tool. That is the AgentKit driver's home
ground, on the host with borrowed hands over a `NetworkNone` box, no vendor
CLI in the image, and the whole path testable offline with `provider/faux`.
Fix is where a harness's own tools pay for themselves: Claude Code headless
inside the box, from the tools image, with the `af-fix` skill as its brief,
as `docs/architecture.md` has it for unattended runs. Both go through the
one `Brain` interface, `AF_BRAIN` on the workspace picks, and a second
harness (`pi`, `codex`, `opencode`) is a row, not a design change. The
alternative, one brain for everything, is simpler to operate and is what
the first measurement (§11) should decide.

### D9. The service owns its queue and its job records; hub tasks are the convergence, not the start

**Recommended.** The hub tasks draft puts a task queue, templates and an
inbox inside hub. It is the right end state and it is not built. The
service needs a durable queue on day one, and hub's `internal/jobqueue`
(SQLite, retry, per-key serialization, crash recovery) is exactly it, but
it is internal to hub's module. Either hub extracts it to a shared module
(a hub decision, §9) or the service carries a copy of the same shape. The
service's job API (§6) is written so that a hub task can later be its
caller: a task's program is `af fix`, and what hub adds is the scheduling
before and the carrying after, which is what the architecture document
already says.

### D10. One sandbox provider per deployment, chosen by configuration

**Recommended.** `podman` on the machine the service runs on (a laptop, a
single server), or `gateway.Dial` to an `agentkit-gateway` elsewhere.
`landlock` is for `af --local` on a developer's Linux machine and for CI.
The service never picks per job; a workspace that needs a different image
says so in a variable (`AF_SANDBOX_IMAGE`), and the provider refuses what
it cannot honour, as `CheckSpec` already does.

## Goals

1. A person labels an issue `af:triage` on GitHub or GitLab and, with no
   other action, the issue gains a cited diagnosis, a severity and a
   confidence, and the label `af:ready` or `af:needs-human`.
2. A person labels a triaged issue `af:fix` and, with no other action, a
   branch is pushed, a pull request is opened (or a carry patch registered
   and rebuilt), the issue says where the work is, and the label says
   `af:landed`, `af:needs-human` or `af:failed`.
3. The same two things happen from a CLI, for a person with an issue URL,
   a file or a sentence, against the service or, with `--local`, on their
   own machine with no service at all. The CLI never embeds a model.
4. Every attempt runs in a sandbox the service holds. The brain inside has
   no forge token, no hub token and no git credential; what it produces
   crosses the boundary as an exported tree and a validated submission,
   and the host commits, pushes and writes to the forge.
5. Hub is the source of truth for the caller's identity, the workspace's
   repository and branch, the carry-patch configuration, the landed
   branch's registration, the attempt's cost and its audit trail. The
   service duplicates none of those.
6. The service is observable without a transcript: every job has a
   lifecycle a client can poll or stream, every attempt has its envelope
   and its events, and `needs_human` is answered through the API, not by
   editing the input.
7. Everything but the containers and the forges runs offline in
   `make check`: the forges as `httptest` servers (as `issuex` tests them
   today), hub as an `httptest` server over its documented API, the
   sandbox on `local`, the brain on `provider/faux` or a scripted harness.

## Non-goals

- **`impl`, `spec` and `review`.** This PRD is the two agents the
  product's front door needs. The spec pipeline follows the same shape
  (the hub tasks draft's `spec` then `impl` chain) and is its own PRD.
- **A web client.** The API is client-neutral; the CLI is the first
  client; the inbox and a browser are the inbox draft's and hub's.
- **A model client in hub, or running a model in the service outside a
  brain.** Hub never calls a model; the service calls one only through
  `Brain.Run`.
- **A second forge client.** `issuex` is the forge seam, unchanged. Forgejo
  is a new `issuex` backend (the Gitea API), scheduled in §10, not a
  change to this design.
- **Steering a running attempt.** An attempt runs to a stop; `needs_human`
  is a stop and an answer is a new attempt, as the hub tasks draft decides
  for the same reason: the agent never waits in a sandbox for a person.
- **Multi-tenant isolation beyond hub's.** The service trusts hub's
  answer about who may see a workspace. It adds no tenancy of its own.
- **A GitHub App, GitLab OAuth app or Forgejo app.** The first release
  authenticates to forges with tokens from its configuration (D4). An app
  installation is the second release's credential, on the same seam.
- **Resuming a brain's transcript.** An attempt that dies is a new
  attempt; the branch and the commits on it are the durable state, as the
  architecture document already assumes.

## Background: what exists

| Need | Exists in | What |
|---|---|---|
| A forge client | agent-fox `issuex` | `CreateIssue`, `ReadIssue`, `UpdateIssue`, `ListIssues` with a label filter, `AddLabels`, `RemoveLabel`, `CreateLabel`, `CreatePullRequest`, `GetCIChecks`, `GetPRReviews`; GitHub and GitLab, `httptest`-tested |
| Hermetic git | agent-fox `internal/gitx` | branch, commit, push from Go with a clean environment |
| Skills | agent-fox `skills/` | `af-fix`, `af-bug-hunter`, `af-code-quality`, … : the briefs a harness brain reads |
| The sandbox | agentkit-go `sandbox` | `Spec`, `Provider`, `Sandbox`, `Open`; `podman`, `landlock`, `local`, `gateway`; `LocalDir` sync and export over rsync, `.git` never exported |
| The gateway | agentkit-go `sandbox/gatewayd` | `agentkit-gateway`: bearer tokens, TTL and count ceilings, Podman behind it |
| Tools without a model | agentkit-go root | `Toolset`, `NewRunner`, `Runner.Call`; `mcpserve` to hand them to a harness over MCP |
| The driver | agentkit-go root | `agentkit.New`, `Config.Prefix`, `MaxCostUSD`, `Effort`; `provider/faux` for tests |
| A harness in a box | agentkit-go `examples/harness` | Claude Code and Pi headless with `--tools ""` and the served tools, one task of a spec, measured before and after |
| Workspaces, git server, carry patches | hub | `POST /workspaces`, `hub_url`, `PATCH_BRANCH_SOURCE`, `POST /patches` with `if_not_exists`, rebuild jobs with per-patch `conflict_files`, `patch-status` |
| Identity | hub (apikit) | API keys, PATs with scopes, `GET /api/v1/user`, `afc login` |
| Cost and audit | hub `internal/audit` | `POST /sessions`, `/sessions/:id/usage`, `/complete`; `POST /workspaces/:slug/runs/:run_id/events`; `GET /events` (SSE) |
| A durable queue | hub `internal/jobqueue` | SQLite, retry policy, per-key and group serialization, crash recovery; internal to hub |
| The envelope | the previous implementation (`1e6458d`, `internal/toolio`) | `status`, `summary`, `needs_human` with `--context`, `artifacts`, `side_effects`, `next`, warning codes, the exit-code table |
| The triage contract | the previous `issuetriage` | read-only tools, a terminating `file_issue` tool, every cited path resolved against the workspace, `rejected_path_calls`, `--label af:fix` |
| The fix contract | the previous `codefix` | checks before and after, the verdict table, the revert check deciding `Closes` versus `Refs`, parking as `wip:` |

What does not exist: a process that runs any of it unattended, a record of
a job, a trigger of any kind, a credential bound to a workspace that a
service may hold, a Forgejo backend, and the `af` toolbelt commands the
architecture document names (`af fix start/submit/land`, `af forge`, `af
guard check`), which this PRD's spec is the first to build.

## 1. Vocabulary

| Term | Meaning |
|---|---|
| **Service** | The `agent-fox` process: API, queue, workers, the sandbox provider, the forge and hub clients. |
| **Workspace** | A hub workspace: a repository, a branch, an owner, a mode (`standard` or `carry_patch`), and, once subscribed, the service's settings for it. The service never creates one. |
| **Subscription** | The service's record that it watches a workspace: the forge repository, the label prefix, the triggers, the background PAT, the resolved settings. |
| **Job** | One unit of work on one workspace: `triage` or `fix`, an input, a lifecycle, zero or more attempts, at most one result. |
| **Attempt** | One execution of a job in one sandbox with one brain. Cost, events, the envelope and the hub session belong to the attempt. |
| **Brain** | What runs the model and its loop: the AgentKit driver on the host, or a harness (Claude Code, Pi, …) inside the box. The `Brain` interface of `docs/architecture.md`. |
| **Brief** | The document a brain works from: the skill, the issue, the facts the program measured. |
| **Submission** | What a brain hands back through the toolbelt: a diagnosis (`af triage submit`) or an implementation report (`af fix submit`). Never parsed from the brain's output. |
| **Label protocol** | The request and status labels under the workspace's prefix (§3). |
| **Region** | The span of an issue body between `<!-- af:triage -->` markers, owned by the service. |

## 2. The shape

```
 forge (GitHub · GitLab · Forgejo)        hub (identity · workspaces · git server · patches · sessions · audit)
      ▲  issues, labels, PRs, webhooks          ▲  validate caller · workspace · vars · register patch · session · events
      │                                         │
      └──────────────┬──────────────────────────┘
                     │
            agent-fox (the service)         one process; local or on a server
              api      /api/v1  jobs, workspaces, answers, events (SSE)      ← af, a web client, a hub task later
              triggers poll (issuex.ListIssues by label) · webhooks (/hooks/:slug) · the API
              queue    durable, SQLite, one attempt at a time per workspace key
              worker   the ring-2 program: clone, box, brief, brain, submit, gate, export, commit, push, forge
                     │
                     │ podman on this host · or gateway.Dial(url, token)
                     ▼
            box (agentkit sandbox)          the tree (with .git) · agentkit-sandboxd · af (ring 1) · the brain, when it has its own hands
```

Three facts about the picture:

- **Credentials never go down.** The worker holds the hub PAT, the forge
  token and the git credential; the box holds the vendor key only when a
  harness runs inside it, and nothing else. The gateway, when used, holds
  the engine's credentials and the worker holds the gateway's token (one
  hop of trust, as PRD 11 §5 has it).
- **Effects never come up.** A brain's commit inside the box stays inside;
  `Export` drops `.git`; the host classifies the exported tree against its
  baseline and makes the commit that counts. The push, the pull request,
  the patch registration and every label are host actions after the brain
  has stopped.
- **Nothing is in a transcript.** A job's state is its record and its
  attempts' envelopes; a brain that dies loses nothing the program had
  measured, and the next attempt starts from the branch.

## 3. The label protocol

Under the subscription's prefix, `af:` by default:

| Label | Kind | Set by | Means |
|---|---|---|---|
| `af:triage` | request | a person, a bot, a poll rule | run triage on this issue |
| `af:fix` | request | a person, or the service after triage when `AF_AUTO_FIX=true` | run fix on this issue |
| `af:in-progress` | status | the service | an attempt holds a sandbox for this issue |
| `af:ready` | status | the service | triaged: a diagnosis with confidence `confirmed` or `probable` is in the body region |
| `af:needs-human` | status | the service | the last attempt stopped with a question; it is on the issue as a comment and in the job |
| `af:landed` | status | the service | a branch is pushed and a pull request is open, or a carry patch is registered and its rebuild succeeded |
| `af:failed` | status | the service | the last attempt failed and retries are spent; the comment says why |

Rules:

- **A request label is consumed.** When the service takes a job for a
  request label it removes the label, so adding it again is a new request
  and a label left in place never means "running". `af:in-progress` is the
  running mark.
- **One status label at a time.** Setting one removes the others of the
  status kind. `af:ready` survives `af:fix`'s run and is replaced only by
  its outcome.
- **Labels are created on subscribe**, with the description from this
  table, by `CreateLabel`; a label that exists is left as it is. A
  workspace whose token may not create labels fails to subscribe with the
  forge's answer, not later.
- **A label is never the only record.** The job record (§6) says what
  happened; the label is its mirror on the forge. The service reconciles
  on its own writes only; it does not watch for labels a person moved and
  undo them.
- `af:validated` was proposed as the triaged mark. `af:ready` is
  recommended because the label is read by the person deciding whether to
  spend a fix on it, and "ready" is that question; `validated` reads as a
  claim about the report. *Workshop.*

## 4. The two agents

Both are the shape of a run in `docs/architecture.md`: the program
measures and owns every effect; the brain works from a brief to a
submission; the toolbelt is the only way back.

### 4.1 Triage

**Input.** An issue (`owner/repo#N` or a URL) on the subscribed repository;
from the CLI also a file or text, which has no issue yet.

**The brief.** The issue's title, body and comments, each fenced and
labelled with its provenance (a comment by a maintainer is not a log line),
the repository map at a token budget, and the triage skill: find the
signals, locate each in the code, read the tests, widen once, separate
symptom from cause, cite only what you read, state confidence honestly.
The `--context` block, when a person answered a question, follows the
report.

**The tools.** The six read tools and `code_search` when the index is
built; no tool that writes, no shell. The terminating tool is
`file_diagnosis` with the schema the previous `triage` validated:
`severity`, `confidence` (`confirmed` | `probable` | `suspected` |
`not_reproducible`), `root_cause`, `affected_files` (each resolved against
the workspace before the submission is accepted; a path not there is an
error back to the brain, counted in `rejected_path_calls`),
`suggested_fix`, `acceptance_criteria`, `question` when the brain could not
decide between readings.

**The effects**, all host-side after the brain stops:

1. The region is rendered by Go from the submission and written into the
   body under the region rule (D6); for a text or file input, an issue is
   created with the region as its body, titled from the submission.
2. The status label: `af:ready` for `confirmed` or `probable`;
   `af:needs-human` for `suspected` with a `question`, or
   `not_reproducible`, with one comment carrying the question and the
   answer path (`af jobs answer <id>`, or re-label with `af:triage` after
   editing the issue).
3. When `AF_AUTO_FIX=true` and the label is `af:ready`, the service adds
   `af:fix` itself, which is a new request.

**Bounds.** From the workspace's variables with service defaults: 100
turns, USD 2 per attempt, 15 minutes. A spent bound is a failed attempt
with `category: budget` and the `fix_hint` the old envelope carried.

### 4.2 Fix

**Input.** An issue on the subscribed repository. With a region in its
body the brief starts from the diagnosis; without one the brain analyses
first, as the old `fix`'s analyse phase did, so a fix on an untriaged issue
is allowed and is simply more expensive.

**The brief.** The issue and its region, the `af-fix` skill, the gate
commands the program detected (`af lang detect`) with their baseline
result, the repository map, and the rule that ends the skill: never commit,
never push, never edit the issue; when done, `af fix submit` with the
report.

**The box and the brain.** The tree is synced in with `.git`, so `git log`
and `git diff` inside are true. The brain is the workspace's `AF_BRAIN`
(D8): a harness in the box with the vendor key in `Spec.Env` and
`NetworkUnrestricted` (it needs its vendor; on Podman that is all or
nothing), or the driver on the host over proxy tools with `NetworkNone`.
The guard (`guard.Allow` over the detected gate programs plus `git` for
reading) sees every call the driver makes; the harness's own hook
(`af guard check` in the image) is best-effort, and the gate is the
invariant.

**The gate**, host-run inside the box over `execute`, keyed by tree hash:
the project's checks before any change and after the submission, compared
as the old `fix` compared them: `pass`, `pass_was_already_failing`,
`regressed`, `still_failing`. Then the revert check: the non-test files put
back, the tests left in, the checks run once more; failing means the tests
prove the fix (`Closes`), passing means they do not (`Refs`, with a
`fix_not_proven` warning).

**Landing**, host-side, per the workspace mode (D7, §5):

- `standard`: branch `fix/<N>-<slug>` from the workspace's branch, one
  commit with the program's trailer, push to the forge, a pull request
  whose body is rendered from facts (the verdict, the checks, the revert
  check, `Closes`/`Refs <issue url>`), draft when the verdict did not land.
- `carry_patch`: branch `patch/<N>-<slug>` from the integration branch's
  upstream base, push per `PATCH_BRANCH_SOURCE`, `POST /patches`, watch the
  rebuild.

**The effects on the issue.** One comment, `kind: fix_summary`, rendered
from facts: the branch, the pull request or the patch, the verdict, what
was not proven; and the status label. A run that parks (`wip:` on the
branch, the verdict not landable) is `af:needs-human` with the comment
saying what failed and how to answer; a run whose attempts are spent is
`af:failed`.

**Bounds.** 150 turns, USD 10, 45 minutes per attempt; 2 attempts per job
before `af:failed`. From the workspace's variables.

### 4.3 What the brain may and may not do

Prevention is the hook and the guard; detection is the gate; the box makes
three things physical, as the architecture document says: the brain never
pushes (no token in the box), never makes a commit that counts (`.git` is
not exported), never reaches the service's state (the job record is on the
host). The toolbelt refuses, with a structured reason the brain reads on
its next turn, a submission that cites a path it did not read, a report
that names no commit subject, or a tree that touched a protected path
(`.specs/`, `.github/workflows/`, the forge's own config files).

## 5. The carry-patch workflow

Hub's carry-patch workspace is the lifecycle of an agent's output on a
fork: an ordered list of patch branches, an integration branch rebuilt as
upstream plus the patches, merged patches detected and retired, conflicts
named by file, resolutions remembered with rerere. The service uses it in
three places and is careful about one.

### 5.1 A fix is a patch

On a `carry_patch` workspace a landed fix is registered, not merged:

1. The branch is `patch/<N>-<slug>`, cut from the current upstream base
   (`refs/remotes/upstream/HEAD` as the last sync left it; the service
   reads `upstream_head_sha` from the workspace and clones from hub's git
   server, which has every ref).
2. The push target follows `PATCH_BRANCH_SOURCE`, read from
   `GET /workspaces/:slug/vars/resolved`: `origin` means the fork at
   `git_url` with the forge credential; `hub` means `hub_url` with the hub
   PAT. In `origin` mode a push to hub would be refused or forwarded, so
   the service never tries it.
3. `POST /workspaces/:slug/patches {branch_name, description,
   upstream_pr_url?, if_not_exists: true}`. The description is the issue's
   title and URL. Position is appended; a workspace may pin it with
   `AF_PATCH_POSITION=first`.
4. In `origin` mode the service calls `POST /workspaces/:slug/sync`, which
   brings the fork's tip in and enqueues the rebuild; in `hub` mode the
   push hook already enqueued it. Either way the sync or push response
   carries `rebuild_job_id`.
5. The service watches `GET /workspaces/:slug/rebuilds/:id` to a terminal
   state. `completed` with the patch `success` is `af:landed`; the comment
   names the patch, its position and the integration branch's new head.

### 5.2 The upstream pull request

When `AF_FIX_UPSTREAM_PR=true`, and only then, the service opens a pull
request on the upstream repository from the fork's branch
(`CreatePullRequest` with `head: <fork-owner>:patch/…`, `base` the
upstream's default branch), records its URL as the patch's
`upstream_pr_url` (`PATCH /patches/:id`), and from then on hub's sync
detects the merge, squash included, and retires the patch. Off by default:
a team that carries internal patches does not want every fix proposed
upstream.

### 5.3 A conflict is a fix

A rebuild that leaves the patch in `conflict` names the files. The service
creates a `fix` job with the patch branch as the base, the new upstream
base, and `conflict_files` as the brief's first section: resolve these
files so the patch applies on this base, keep the patch's intent, change
nothing else. The brain works in a box on the patch branch rebased by the
program up to the first conflict; `af fix land` pushes the rebased branch
to the same target as §5.1 and the next rebuild applies it. The issue the
patch came from gets the comment; the label stays `af:landed` while the
conflict job runs and becomes `af:needs-human` if it cannot be resolved.

The same job shape serves a rebuild hub starts for a reason the service
did not cause (an upstream advance): the service subscribes to hub's SSE
stream filtered to the workspace, reads the rebuild's result, and acts on
a `conflict` in a patch it registered. Patches it did not register are not
its business unless `AF_FIX_FOREIGN_CONFLICTS=true`.

### 5.4 What the service never does on a carry-patch workspace

Commit to the integration branch; push to the upstream remote; push a
registered patch branch to hub in `origin` mode; run `afc rebuild submit`
while a rebuild is running (hub answers 409; the service waits on the
running one); touch the trunk. These are the rules hub's own agent guidance
(`docs/examples/AGENTS_carry_patch.md`) gives a person, and the program
enforces them on the brain by giving it no way to do any of them.

## 6. The service

### 6.1 Configuration

`config.toml` in the working directory or `$XDG_CONFIG_HOME/agent-fox/`,
with `${VAR}` expansion, the same shape as hub's:

```toml
[server]
listen = "127.0.0.1:8090"
external_url = "https://fox.example.com"

[hub]
url = "https://hub.example.com"

[database]
path = "agent-fox.db"            # SQLite: subscriptions, jobs, attempts, events

[secrets]
key_file = "/etc/agent-fox/key"  # encrypts the stored PATs at rest

[sandbox]
provider = "podman"              # podman | gateway | landlock
image = "quay.io/agentfox/tools:latest"
# gateway_url = "https://gw.example.com:8443"
# gateway_token = "${AGENTKIT_GATEWAY_TOKEN}"

[[forges]]
host = "github.com"
token = "${GITHUB_TOKEN}"        # a GitHub App installation in the second release

[[forges]]
host = "gitlab.example.com"
token = "${GITLAB_TOKEN}"

[brains.claude]
env = { ANTHROPIC_API_KEY = "${ANTHROPIC_API_KEY}" }   # goes into Spec.Env of a box that runs this brain
[brains.agentkit]
model = "claude-sonnet-5-5"      # anthropic.Resolve on the host's environment
```

Per-workspace settings are **hub workspace variables**, read through
`GET /workspaces/:slug/vars/resolved` at the start of every job, so a team
changes them with `afc vars` and nothing restarts:

| Variable | Default | Meaning |
|---|---|---|
| `AF_LABEL_PREFIX` | `af:` | the label protocol's prefix |
| `AF_AUTO_FIX` | `false` | add `af:fix` after a triage that ends `af:ready` |
| `AF_BRAIN` | `agentkit` for triage, `claude` for fix | the brain per agent (`AF_BRAIN_TRIAGE`, `AF_BRAIN_FIX` override) |
| `AF_MODEL` | the brain's | the model id handed to the brain |
| `AF_MAX_BUDGET_USD`, `AF_MAX_TURNS`, `AF_TIMEOUT` | per agent (§4) | the attempt's bounds |
| `AF_MAX_ATTEMPTS` | `2` | attempts per job before `af:failed` |
| `AF_POLL_INTERVAL` | `5m`; `0` disables | how often the subscription polls the forge |
| `AF_SANDBOX_IMAGE` | the service's | the box's image for this workspace |
| `AF_FIX_LAND` | `pr` on standard, `patch` on carry-patch | `pr` \| `branch` \| `patch` \| `none` |
| `AF_FIX_UPSTREAM_PR` | `false` | §5.2 |
| `AF_PATCH_POSITION` | `last` | `first` \| `last` |
| `AF_FIX_FOREIGN_CONFLICTS` | `false` | §5.3 |

### 6.2 The API

Under `/api/v1`, a hub credential on every request, JSON in and out, the
apikit error envelope shape (`{error: {code, message, error_type?}}`) so a
client of hub needs no second error decoder. `GET /healthz`, `/readyz` and
`/version` outside it.

```
GET    /workspaces                              subscriptions the caller can see (hub says which)
PUT    /workspaces/:slug                        subscribe: {forge_repo?, label_prefix?, hub_token, webhook: {enabled, secret?}}
GET    /workspaces/:slug                        the subscription, its resolved settings, its label state
DELETE /workspaces/:slug                        unsubscribe; labels are left in place
POST   /workspaces/:slug/triage                 {issue} | {text, title?} | {file: {name, content}}; → 202 {job}
POST   /workspaces/:slug/fix                    {issue, context?}                                → 202 {job}
GET    /workspaces/:slug/jobs?kind=&state=&issue=&cursor=
GET    /jobs/:id                                the job with its attempts' summaries
POST   /jobs/:id/cancel                         stops the attempt, tears the box down, label af:failed? no: the request label is restored
POST   /jobs/:id/answer                         {answer} | {option}: a new attempt with --context, from blocked
POST   /jobs/:id/retry                          from failed
GET    /jobs/:id/events?since=<seq>             replay from the stored log, then tail (SSE)
GET    /jobs/:id/attempts/:n/envelope           the attempt's envelope, full detail
GET    /jobs/:id/attempts/:n/report             the report file

POST   /hooks/:slug                             the forge's webhook; signature checked with the subscription's secret
```

A job:

```jsonc
{
  "id": "job_…", "workspace": "widgets", "kind": "fix",
  "input": { "issue": "https://github.com/acme/widgets/issues/42" },
  "source": { "kind": "label" | "webhook" | "api" | "rebuild" | "triage", "by": "usr_… | label af:fix | hook delivery id" },
  "state": "queued" | "running" | "blocked" | "done" | "failed" | "cancelled",
  "blocked": { "reason": "needs_human", "question": "…", "options": [ … ], "answer_with": "POST /jobs/job_…/answer" },
  "attempt_count": 1, "current_attempt": 1,
  "result": { /* the envelope's result: summary view */ },
  "artifacts": [ { "kind": "branch", … }, { "kind": "pull_request", … }, { "kind": "patch", "id": "…", "position": 3 } ],
  "side_effects": [ … ],
  "labels": { "set": "af:landed", "removed": ["af:fix", "af:in-progress"] },
  "usage": { "cost_usd": 1.84, "input_tokens": …, "output_tokens": … },
  "hub": { "session_id": "…", "run_id": "20261019_141205_a1b2c3" },
  "created_at": "…", "started_at": "…", "finished_at": "…"
}
```

The lifecycle is the hub tasks draft's, reduced: no `draft`, no `paused`,
no position (the queue is per workspace, first come first served, one
attempt at a time per workspace key; `AF_CONCURRENCY` later). `blocked` is
a state because it is the one state the queue must not claim and a client
must show.

### 6.3 Triggers

- **Poll.** Every `AF_POLL_INTERVAL`, `ListIssues(repo, {State: open,
  Labels: [prefix+"triage"]})` and the same for `fix`. Each issue not
  already the input of an open job becomes a job with `source.kind:
  label`. The request label is removed when the job is created (§3), so a
  poll never sees the same request twice; a forge that lags is handled by
  the open-job check. `IssueList.Incomplete` is logged and the next poll
  continues.
- **Webhook.** `POST /hooks/:slug` with the forge's signature
  (`X-Hub-Signature-256` for GitHub, `X-Gitlab-Token` for GitLab, Forgejo's
  HMAC header), the subscription's secret, and a delivery id the service
  stores to drop replays. The events handled are the label events
  (`issues.labeled` on GitHub; the issue hook with a label change on
  GitLab); everything else answers 202 and is dropped. A webhook and a
  poll on the same workspace coexist: the poll is the catch-up for a lost
  delivery.
- **The API.** `POST /triage` and `/fix` from `af` or any client, with the
  caller's identity as `source.by`.
- **Hub.** A rebuild result on hub's SSE stream (`GET /events?workspace=`)
  for a patch the service registered (§5.3), when the subscription holds
  `audit:read`; else the service polls the rebuild it was told about.

### 6.4 Execution: one attempt

```
claim      the queue hands the worker a job; af:in-progress; a hub session (POST /sessions, archetype triage|fix, run_id YYYYMMDD_HHMMSS_6hex)
resolve    the workspace from hub with the subscription's PAT; its vars; the forge client for its host; the issue thread
clone      the workspace from hub_url (every ref, the hub PAT), or from git_url when hub has no clone; the base branch checked out; the attempt's branch created on the host
box        Provider.Create(Spec{Image, Env, Network, Resources, TTL}); Sync(LocalDir{Path: clone}) with .git; the gate's baseline inside, cached by tree hash
brief      af triage brief | af fix brief: the rendered document, byte-identical in its run-constant prefix
brain      Brain.Run(brief): the driver on the host over sb.Tools, or the harness inside via one execute; its submission arrives through af … submit inside the box, as a file under .agent-fox/
measure    the gate after, the revert check, the scope check, the protected-path revert; a refusal goes back to the brain as its next turn, within the attempt's bounds
export     sb.Export(LocalDir{clone}), .git left behind; the host classifies the tree and makes the commit with the program's trailer
land       push; the pull request or the patch registration; the region or the comment; the labels          (af fix land / af triage land)
report     the envelope and the events file stored as attempt artifacts; usage to the hub session; events to hub audit; the session completed
close      sb.Close; the job's state; the next job
```

A refusal at `measure` is an attempt that continues; a brain that ends
without a submission is `category: no_result`; a box that goes unavailable
is `sandbox_unavailable`, retryable once; a hub or forge write that fails
after the push is a warning on a landed run, as the old envelope ruled
(tracking never fails a done run).

### 6.5 State

SQLite, one file, the service's own: `subscriptions`, `jobs`, `attempts`,
`events` (sequence-numbered per job), `deliveries` (webhook ids). The
report file and the events file of an attempt are stored beside the
database under `$XDG_STATE_HOME/agent-fox/attempts/<id>/`. Nothing a
transcript would hold is stored; the brain's own transcript, when it keeps
one, is a path in the envelope and is never read by the service.

## 7. The CLI

`af` is hub's `afc` pattern: a thin client, the credential from
`~/.af/config.toml`, the server from `AF_URL` or `--server`, one JSON
object on stdout and the exit-code table of the old envelope (`0` done, `1`
failed, `2` usage, `3` needs_human, `4` unverified), so a script and a
model-driven caller read it the same way.

```
af login                                       afc login; the same file
af workspace subscribe <slug> [--prefix af:] [--poll 5m] [--webhook] [--hub-token-file …]
af workspace list | get <slug> | unsubscribe <slug>

af triage <issue|file|text> --workspace <slug>  [--context …] [--wait]     → 202 and a job id; --wait follows to a stop
af fix    <issue>           --workspace <slug>  [--context …] [--wait]

af jobs list [--workspace] [--state] | get <id> | watch <id> | cancel <id> | answer <id> "<text>"|--option B | retry <id>

af triage <issue|file|text> --local [--dir .] [--brain claude|agentkit] [--land …]
af fix    <issue>           --local [--dir .] …
```

**`--local` is the same pipeline with no service.** The CLI builds the
worker in-process: a `landlock` or `podman` provider on this machine, the
brain from the flag, the forge token from the environment the old tools
read (`GITHUB_TOKEN`, `GITLAB_TOKEN`), the checkout from `--dir`, and the
envelope on stdout. It is the "local CLI wrapping a model or agent harness"
of the brief, and it is the developer's loop for the skills: a change to
`af-fix` is tried with `af fix --local` before any service sees it. Hub is
optional in this mode: with `--workspace`, the local run still reports its
session and its audit events to hub; without, it reports nothing.

**The toolbelt is the same binary.** Inside a box, and for a person at a
shell with Claude Code open, `af triage brief`, `af triage submit`, `af fix
brief`, `af fix submit`, `af gate run`, `af guard check` are the commands
the architecture document names; the service's workers call the same Go
functions in-process. One implementation, three callers: the worker, the
box, the person.

## 8. Working with hub

| The service needs | Hub gives | How |
|---|---|---|
| who is calling | the user behind the bearer | `GET /api/v1/user` with the caller's token; cached 60 s by token hash |
| may they act on this workspace | ownership (later: org membership) | `GET /workspaces/:slug` with the caller's token; 404 is 404 |
| the repository, the branch, the mode, the integration branch, the upstream | the workspace record | `GET /workspaces/:slug` with the subscription's PAT |
| the per-workspace settings | variables, resolved workspace over org over user | `GET /workspaces/:slug/vars/resolved` |
| a clone with every ref, private by construction | the git server | `git clone <hub_url>` with the PAT as the HTTP Basic password |
| where a patch branch goes | `PATCH_BRANCH_SOURCE`, `PUSH_PATCHES_TO_ORIGIN` | the variables; the push target follows them (§5.1) |
| the patch registered and carried | the patch list, the rebuild | `POST /patches` (`if_not_exists`), `POST /sync`, `GET /rebuilds/:id`, `PATCH /patches/:id` for `upstream_pr_url` |
| a conflict to fix | the rebuild's `patch_results[].conflict_files` | polled, or from `GET /events` |
| cost per workspace | sessions and usage | `POST /sessions` (`archetype`, `model`, `run_id`), `POST /sessions/:id/usage` per model turn, `POST /sessions/:id/complete` |
| an audit trail a person can query beside hub's own | audit ingestion | `POST /workspaces/:slug/runs/:run_id/events/batch` with the attempt's events; the transcript endpoint stays empty, by design |

What the service does not take from hub: secrets (D4), the job queue (D9),
the sandbox (the hub tasks draft's baseline; the service brings its own
provider, which hub can point at later).

## 9. What this asks of hub

Small, and each is a hub spec, not this one:

1. **A workspace-bound credential a service may hold** (D3): confirm what
   the README's "workspace-scoped token" is, or add a PAT binding
   (`workspace: <slug>`) that the ownership checks honour. Until then a
   plain PAT with the §6 scopes works and sees every workspace its user
   owns, which is wider than it should be.
2. **Org-member reads of org workspaces** (D2), already named by the inbox
   draft as the prerequisite for any team use.
3. **`jobqueue` as a shared module** (D9), or the service copies it.
4. **Optional:** a secrets-resolve endpoint (D4 (b)).

Nothing in this PRD waits on 2 to 4 for a single-owner deployment, which
is the first release's target: one person, their workspaces, their forge.

## 10. Order of work

Each step is a spec package, lands with `make check` green, its docs
updated, and is usable on its own.

1. **The toolbelt and `--local`.** `af triage` and `af fix` end to end on
   one machine: the `Brain` interface with the `agentkit` and `claude`
   implementations, the briefs rendered from the skills, `af … submit`
   validation, the gate on the hands' runner, the revert check, landing on
   a `standard` repository as a pull request, the envelope. Sandbox on
   `landlock` and `podman`; offline on `local` with `faux` and a scripted
   harness. This is the old `triage` and `fix` rebuilt on the hands, and it
   is the part every later step calls.
2. **The service.** `agent-fox`: configuration, SQLite, the queue, hub
   authentication, subscriptions, the jobs API with SSE, one worker running
   step 1's pipeline over the configured provider, hub sessions and audit.
   `af` as its client. No triggers but the API.
3. **The label protocol and the triggers.** Labels created on subscribe,
   the poll, the webhook for GitHub and GitLab, `AF_AUTO_FIX`, `answer` and
   `retry` from the forge side (re-labelling after a comment).
4. **Carry patches.** §5 whole: the patch branch, the push target by
   authority, registration, the rebuild watched, the upstream pull
   request, the conflict job, hub's SSE as the source of rebuild results.
5. **Forgejo**, as an `issuex` backend with its webhook, and the GitHub App
   installation as a forge credential.
6. **Convergence with hub tasks**, when that draft is built: a task whose
   program is `af fix`, the service's queue replaced by hub's, the
   subscription replaced by a schedule. The job API of §6 is the contract
   that makes this a move, not a rewrite.

Steps 3 and 4 are independent once 2 has landed.

## 11. Acceptance criteria

Offline, in `make check`, with `httptest` forges and an `httptest` hub:

1. `PUT /workspaces/widgets` with a valid hub token and a PAT creates the
   seven labels on the forge (`CreateLabel` requests observed), and a
   second call changes nothing.
2. An issue labelled `af:triage` on the `httptest` GitHub produces, within
   one poll, a job whose attempt runs on `local` with `faux`, a body
   `UpdateIssue` whose text outside the region is byte-identical to the
   original, the request label removed, `af:ready` added, and a hub
   session opened, given usage, and completed. The same against the
   `httptest` GitLab.
3. A triage whose scripted brain cites a path not in the workspace gets the
   refusal as its next turn and the attempt ends with
   `rejected_path_calls: 1` and a diagnosis citing only real paths.
4. An issue labelled `af:fix` on a `standard` workspace produces a branch
   `fix/42-…` in the host clone, one commit carrying the program's
   trailer, a push to the forge, a `CreatePullRequest` whose body says
   `Closes` when the revert check failed and `Refs` when it passed, a
   `fix_summary` comment, and `af:landed`. The brain's own commit inside
   the box, when it made one, is not on the branch.
5. The same on a `carry_patch` workspace with `PATCH_BRANCH_SOURCE=hub`
   pushes `patch/42-…` to `hub_url`, registers it with `if_not_exists`,
   reads the rebuild to `completed`, and sets `af:landed`; with
   `PATCH_BRANCH_SOURCE=origin` it pushes to `git_url`, calls `/sync`, and
   never pushes to `hub_url`. A rebuild answering `conflict` with files
   creates a `fix` job whose brief names them.
6. A brain that ends with a `question` leaves the job `blocked`, the issue
   `af:needs-human` with one comment; `POST /jobs/:id/answer` starts a new
   attempt whose brief carries the answer as `--context`, and the second
   attempt's run-constant prefix is byte-identical to the first's.
7. A webhook delivery with a bad signature is 401 and creates nothing; a
   replayed delivery id is 202 and creates nothing; a label event for
   `af:fix` creates exactly the job a poll would have.
8. `af fix <issue> --local --dir ./repo` on a temporary repository with a
   scripted harness lands a branch and prints the envelope with exit 0; the
   same with a hub token and `--workspace` opens and completes a hub
   session; without, no request reaches the `httptest` hub.
9. The box never receives a hub token, a forge token or a git credential:
   a test over `Spec.Env` for every brain and every path asserts the
   allowlist (`PATH`, `HOME`, `TMPDIR`, the vendor key for a harness brain,
   nothing else).
10. A worker killed between `export` and `land` leaves the job `queued`
    with the attempt failed as retryable and the clone's branch intact; the
    next attempt reuses the branch and does not re-run the brain when the
    submission file is present and valid. *Spec time:* whether the second
    attempt re-measures the exported tree or trusts the stored verdict.

Measured, after the first release, against the old tools' numbers: cost
per `af:ready`, cost per `af:landed`, the share of `af:landed` pull
requests merged without a human commit, and the same two brains compared
on the same issues (D8).

## 12. What is given up, stated plainly

- **Two credentials stores.** The forge token and the vendor key live in
  the service's configuration, the git credential for hub's server is a
  PAT the service stores; hub's secrets are not used. One store is D4 (b)
  or (c), later.
- **Labels can lie for a moment.** The label is written after the job
  record; a forge write that fails leaves the record right and the label
  stale, with a warning in the envelope. The job API is the truth.
- **One attempt at a time per workspace.** Simpler than the tasks draft's
  position and concurrency, and enough for a team whose forge rate limits
  would object to more. Concurrency is a variable later.
- **A brain in a box has the whole network on Podman.** PRD 11 §9's all or
  nothing; the gateway's egress policy is where it narrows. The driver
  brain does not have this problem and is why triage runs on it.
- **The hub tasks draft's inbox, schedules, templates and chains are not
  here.** A `triage` then `fix` chain is `AF_AUTO_FIX`; everything else
  waits for the convergence.
- **The product website could not be read from the sandbox this draft was
  written in** (`fullsend.sh` is unreachable through the proxy), so the
  product framing above is the brief's, not the site's. *To check against
  the site before this is a spec.*

## Documentation

| Change | Update |
|---|---|
| the service, its configuration, its API | `docs/api.md` (new), `docs/configuration.md` (new) |
| `af` and its subcommands, `--local`, the exit codes | `docs/cli.md` (new) |
| the topology, the attempt pipeline, the label protocol, the carry-patch use | `docs/architecture.md`: the *to be written* sections it reserves, and a new section for the service |
| the decisions, once taken | one ADR per decision that changes the architecture document (D2, D4, D7, D8) |
| hub's asks | issues on hub, one per item of §9 |
| the skills | `skills/af-triage` (new), `skills/af-fix` (the brief's rule: submit through `af`, never commit) |
| setup and quickstart | `README.md` |
