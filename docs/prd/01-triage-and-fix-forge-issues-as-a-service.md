# Triage and fix forge issues as a service

Status: **workshop draft, second pass, decisions settled** (2026-10-19).
Nothing here is implemented. The decisions in §0 were open in the first
pass and are now recorded with their outcomes; the rest of the document is
written to them. Details left to settle at spec time are marked *spec
time*. Next step: the hub asks in §9 become hub specs, and §10 step 1
becomes this repository's first spec package.

Builds on [`docs/architecture.md`](../architecture.md) (the brain, the
toolbelt and the hands), AgentKit
[PRD 11](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/11-run-tools-in-an-isolated-sandbox.md)
(sandboxes and the gateway) and
[PRD 12](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/12-simplify-the-embedder-contract.md)
(toolset, runner, `mcpserve`), and on hub's workspaces, git server, carry
patches, secrets, sessions and audit as they exist today. It changes
nothing in AgentKit. What it asks of hub is not small any more (§9): hub
becomes the owner of sandboxes and of every forge access, which is the
decision that shapes everything below.

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

- learns of issues a team has labelled for it, from hub's event stream or
  by polling through hub, or is asked outright from a CLI;
- runs the **triage** agent on an issue and writes the diagnosis back to
  it, marking it ready;
- runs the **fix** agent on a triaged issue, lands the change as a pull
  request or as a carry patch, and says so on the issue;
- does all of that in a sandbox hub created for it, through a forge
  connection hub holds, with hub as the authority on who the caller is,
  which repository a workspace is, where a landed branch goes and what it
  cost.

It is built on `agentkit-go` in the same shape as hub: **API first, with a
small CLI as its client.** Hub is the platform: identity, workspaces, the
git server, secrets, sandboxes, the forge. The service is the work: jobs,
attempts, briefs, brains, verdicts. The CLI holds a hub credential and
nothing else, and can run the same pipeline locally when there is no
server to talk to.

The one rule of `docs/architecture.md` stands and is the design constraint
for everything below: **the brain never touches git, the forge or hub
except through the program, and the program never runs a model except
through a brain it was handed.**

## 0. Decisions

Each was a question in the first pass. The outcome is recorded with the
reasoning that carried it, so it is not re-asked.

### D1. Two binaries, named like hub's: `agent-fox` the server, `af` the client

**Decided.** Hub is `hub` (the server) and `afc` (its CLI). agent-fox is
`agent-fox` (`cmd/agent-fox`: the API, the queue, the workers) and `af`
(`cmd/af`: the client, and the local runner). `af` is also the name
`docs/architecture.md` gives the toolbelt a brain calls (`af gate run`,
`af fix land`, …); that is deliberate: the toolbelt commands are the ring-1
and ring-2 commands the service's workers run, and the same binary serves
them to a brain in a box and to a person at a shell. One binary a brain is
handed, one binary a person installs.

### D2. Hub is the identity provider; agent-fox has no users of its own

**Decided, with a cache.** Every request to the service carries a hub
credential (`Authorization: Bearer <api key or PAT>`), and the service
validates it by asking hub (`GET /api/v1/user`). The answer is cached by
the SHA-256 of the token for a defined TTL, `hub.auth_cache_ttl`, default
60 seconds, bounded to at most 15 minutes; a 401 from hub evicts the entry
at once, and a negative answer is cached for the same TTL so a bad token
cannot make the service hammer hub. Which workspaces a caller may act on
is hub's answer too: the service looks a workspace up with the caller's
own token (`GET /api/v1/workspaces/:slug`), cached the same way, and a 404
from hub is a 404 from the service. No user table, no OAuth callback, no
admin token of its own; `af login` is `afc login`'s credential file
(`~/.af/config.toml`), read as is.

Hub's ownership model today is owner-private: an org member cannot see a
workspace they do not own. The inbox draft already names org-member reads
as hub work to land first; this PRD inherits the dependency (§9).

### D3. The service holds a hub PAT per subscribed workspace, for the work it does when nobody is calling

**Decided.** A poll, an event from hub and a rebuild it watches all happen
with no caller on the line. For those the service needs a hub credential
bound to the workspace: a PAT the subscriber mints and hands over at
subscribe time, with exactly the scopes §8 lists. The service stores it
encrypted at rest under a key from its own configuration, and uses it for
every background action on that workspace. The caller's own token is used
for everything a caller asks for directly.

Whether hub's "workspace-scoped token" (its README's third credential
type) is this PAT with a workspace binding is *to confirm with hub*; if it
is, it is the credential this decision wants, minted by hub.

### D4. Hub owns the sandbox and the forge; the service never holds a vendor key or a forge token

**Decided: option (c), extended.** Hub already holds the workspace's forge
credential (`GIT_PAT`, the reserved secret it clones with) and stores every
other secret a team has; its API never returns a value, by design. Rather
than teach the service to hold a second copy of those credentials, hub
gains two capabilities and the service consumes them:

- **Sandboxes.** Hub creates, lists and destroys sandboxes on the one
  provider its deployment is configured with (D10), through a new API and
  `afc sandbox` subcommands. A sandbox is created for a workspace from a
  **profile** (image, resources, network, TTL) and hub resolves the
  secrets that profile names (`ANTHROPIC_API_KEY` at org or workspace
  scope, say) into the box's environment. The caller gets a **lease**: the
  gateway's URL, a token good for that one sandbox, its expiry. The vendor
  key enters the box and nothing else; the service's worker never sees it.
- **The forge.** Hub exposes issues, labels, comments and pull requests on
  a workspace's repository, over `issuex` with the workspace's own forge
  credential, through a new API and `afc issue` and `afc pr` subcommands.
  The service reads and writes the forge only through hub. Forge webhooks
  arrive at hub too (it has the external URL and the credential to verify
  them) and are relayed on hub's event stream (§6.3).

The consequence for the brains: **every brain runs inside the box**, the
AgentKit driver included. The driver is in `af`, which is in the image, so
`af triage run` in the box builds `agentkit.New` over `tools.For` on the
workspace directly, no proxy in between. The architecture document's
"borrowed hands" placement of the driver (on the host, over `Box.Tools`)
is given up for the service, because the host is where the key is not;
`af --local` keeps it as an option for a developer's machine.

This means `issuex` moves out of this repository (D9).

### D5. Labels are the trigger and the visible status; the service's own records are the state

**Decided.** A team asks for triage or a fix by putting a label on an
issue, and reads the outcome as a label, because that is what a forge
renders in every list, board and notification. The labels are a *protocol
the service owns*, created by hub's forge API on subscribe, under a prefix
the workspace may change (`af:` by default), and the truth about a job is
the service's job record, which the labels mirror. A label a person
removes or adds by hand is read as a request, never as state the service
must reconcile.

### D6. Triage replaces the issue body with its findings, and keeps the original report inside

**Decided, reversing the first pass.** The triage agent reads the person's
text and replaces the body with its structured findings: the diagnosis
rendered by Go from the submission, then the original report verbatim in
a closing section (`## Original report`, collapsed where the forge
renders `<details>`), so nothing a person wrote is lost and the fix agent
and every later reader open the diagnosis first. A second triage replaces
the diagnosis and keeps the original report from the first. The title is
kept; *spec time*: whether a submission may propose one when the original
is empty or a bare stack frame. The CLI's text input, which has no issue
yet, creates one with the same body and the report as its original.

The first pass appended a marked region below the person's text, out of
the previous PRD 12's rule that a program never rewrites a person's body.
The rule is dropped for triage because the issue is the triage agent's
deliverable: the person asked for it by labelling, and the previous
`triage --overwrite` did exactly this.

### D7. A fix lands as a pull request on a standard workspace and as a carry patch on a carry-patch workspace

**Decided.** Hub's workspace mode decides. On a `standard` workspace the
branch goes to the forge and a pull request is opened against the
workspace's branch, `Closes` or `Refs` the issue, as the old `fix` did. On
a `carry_patch` workspace the branch is a patch, `patch/<n>-<slug>`, pushed
where `PATCH_BRANCH_SOURCE` says (the fork in `origin` mode, hub's git
server in `hub` mode), registered with `POST /patches` (`if_not_exists`),
rebuilt by hub, and an upstream pull request is opened only when the
workspace asks for it (`AF_FIX_UPSTREAM_PR=true`). The service watches the
rebuild and turns a `conflict` into a fix job of its own (§5.3).

### D8. Two brains in the first release: the AgentKit driver for triage, a headless harness for fix

**Decided.** Triage is read-only and tool-light: six read tools and a
terminating `file_diagnosis` tool. That is the AgentKit driver's home
ground, and under D4 it runs in the box as `af triage run`, whole path
testable offline with `provider/faux`. Fix is where a harness's own tools
pay for themselves: Claude Code headless inside the box, from the tools
image, with the `af-fix` skill as its brief, as `docs/architecture.md` has
it for unattended runs. Both go through the one `Brain` interface,
`AF_BRAIN` on the workspace picks, and a second harness (`pi`, `codex`,
`opencode`) is a row, not a design change. The first measurement (§11)
compares the two on the same issues.

### D9. `jobqueue` and `issuex` become standalone repositories

**Decided.** Hub's `internal/jobqueue` (SQLite, retry policy, per-key and
group serialization, crash recovery) is extracted to
`github.com/agent-fox-dev/jobqueue` and consumed by hub and by the service
as a module. `issuex` is extracted from this repository to
`github.com/agent-fox-dev/issuex`, consumed by hub for its forge API (D4)
and by `af --local`, which talks to a forge directly with the developer's
own token. The service's job API (§6) is written so that a hub task can
later be its caller: a task's program is `af fix`, and what hub adds is
the scheduling before and the carrying after. That convergence (the hub
tasks draft) is §10's last step, not its first.

### D10. One sandbox provider per hub deployment

**Decided.** The provider is hub's configuration, not the service's:
`podman` on the host hub runs on, or an `agentkit-gateway` hub points at;
`landlock` is for `af --local` on a developer's Linux machine and for CI.
Hub chooses once; a workspace that needs a different image names a profile
(`AF_SANDBOX_PROFILE`), and the provider refuses what it cannot honour, as
`CheckSpec` already does. The service never sees a provider; it sees a
lease.

### D11. Triggers reach the service through hub

**Recommended, new in this pass; follows from D4.** A forge webhook is
verified by the party that holds the forge credential and has a public
URL, which is hub. Hub receives it at `POST /hooks/forge/:slug`, verifies
the signature with the subscription's secret, and emits an audit event
(`forge.issue.labeled`, `forge.pull_request.merged`, …) on its SSE stream.
The service subscribes to `GET /api/v1/events?workspace=` with the
workspace's PAT and acts on the label events; a poll through hub's issues
API is the catch-up for a lost delivery or a dropped stream. The service
listens on no public endpoint for the forge. *Workshop: whether hub should
relay webhooks at all, or the service keeps its own `/hooks/:slug` and
verifies with a secret the subscriber hands it.* The recommendation keeps
the forge entirely hub's.

## Goals

1. A person labels an issue `af:triage` on GitHub or GitLab and, with no
   other action, the issue's body becomes a cited diagnosis with a
   severity and a confidence, the original report kept inside, and the
   label becomes `af:ready` or `af:needs-human`.
2. A person labels a triaged issue `af:fix` and, with no other action, a
   branch is pushed, a pull request is opened (or a carry patch registered
   and rebuilt), the issue says where the work is, and the label says
   `af:landed`, `af:needs-human` or `af:failed`.
3. The same two things happen from a CLI, for a person with an issue URL,
   a file or a sentence, against the service or, with `--local`, on their
   own machine with no service at all. The CLI never embeds a model.
4. Every attempt runs in a sandbox hub created and will destroy. The
   service's worker holds a hub PAT and a sandbox lease, and nothing else:
   no vendor key, no forge token, no git credential beyond the PAT hub's
   git server accepts. The brain inside holds the vendor key and nothing
   else. What the brain produces crosses the boundary as an exported tree
   and a validated submission, and the worker commits, pushes and asks hub
   to write to the forge.
5. Hub is the source of truth for the caller's identity, the workspace's
   repository and branch, the carry-patch configuration, the landed
   branch's registration, the sandbox, the forge connection, the attempt's
   cost and its audit trail. The service duplicates none of those.
6. The service is observable without a transcript: every job has a
   lifecycle a client can poll or stream, every attempt has its envelope
   and its events, and `needs_human` is answered through the API, not by
   editing the input.
7. Everything but the containers and the forges runs offline in
   `make check`: hub as an `httptest` server over its documented API
   (sandbox leases pointing at an in-process `gatewayd.Server` over
   `local`, as `examples/gateway` does), the brain on `provider/faux` or
   a scripted harness.

## Non-goals

- **`impl`, `spec` and `review`.** This PRD is the two agents the
  product's front door needs. The spec pipeline follows the same shape
  (the hub tasks draft's `spec` then `impl` chain) and is its own PRD.
- **A web client.** The API is client-neutral; the CLI is the first
  client; the inbox and a browser are the inbox draft's and hub's.
- **A model client in hub, or in the service outside a brain.** Hub never
  calls a model; the service calls one only through `Brain.Run`, which
  runs in a box.
- **A second forge client.** `issuex`, in its own repository, is the forge
  seam. Forgejo is a new `issuex` backend (the Gitea API), scheduled in
  §10, not a change to this design.
- **Steering a running attempt.** An attempt runs to a stop; `needs_human`
  is a stop and an answer is a new attempt, as the hub tasks draft decides
  for the same reason: the agent never waits in a sandbox for a person.
- **Multi-tenant isolation beyond hub's.** The service trusts hub's
  answer about who may see a workspace. It adds no tenancy of its own.
- **A GitHub App, GitLab OAuth app or Forgejo app.** Hub authenticates to
  forges with the workspace's credential. An app installation is a hub
  credential type later, on the same seam.
- **Resuming a brain's transcript.** An attempt that dies is a new
  attempt; the branch and the commits on it are the durable state.
- **Specifying hub's sandbox and forge APIs.** §9 says what the service
  needs from them; their shape is hub's spec.

## Background: what exists

| Need | Exists in | What |
|---|---|---|
| A forge client | agent-fox `issuex` (to be extracted, D9) | `CreateIssue`, `ReadIssue`, `UpdateIssue`, `ListIssues` with a label filter, `AddLabels`, `RemoveLabel`, `CreateLabel`, `CreatePullRequest`, `GetCIChecks`, `GetPRReviews`; GitHub and GitLab, `httptest`-tested |
| Hermetic git | agent-fox `internal/gitx` | branch, commit, push from Go with a clean environment |
| Skills | agent-fox `skills/` | `af-fix`, `af-bug-hunter`, `af-code-quality`, … : the briefs a harness brain reads |
| The sandbox | agentkit-go `sandbox` | `Spec`, `Provider`, `Sandbox`, `Open`; `podman`, `landlock`, `local`, `gateway`; `LocalDir` sync and export over rsync, `.git` never exported |
| The gateway | agentkit-go `sandbox/gatewayd` | `agentkit-gateway`: bearer tokens, each token sees only its own sandboxes, TTL and count ceilings, Podman behind it |
| Tools without a model | agentkit-go root | `Toolset`, `NewRunner`, `Runner.Call`; `mcpserve` to hand them to a harness over MCP |
| The driver | agentkit-go root | `agentkit.New`, `Config.Prefix`, `MaxCostUSD`, `Effort`; `provider/faux` for tests |
| A harness in a box | agentkit-go `examples/harness` | Claude Code and Pi headless with `--tools ""` and the served tools, measured before and after |
| Workspaces, git server, carry patches | hub | `POST /workspaces`, `hub_url`, `PATCH_BRANCH_SOURCE`, `POST /patches` with `if_not_exists`, rebuild jobs with per-patch `conflict_files`, `patch-status` |
| Identity | hub (apikit) | API keys, PATs with scopes, `GET /api/v1/user`, `afc login` |
| Secrets | hub | user, org and workspace scopes; `GIT_PAT` reserved; resolved only inside hub |
| Cost and audit | hub `internal/audit` | `POST /sessions`, `/sessions/:id/usage`, `/complete`; `POST /workspaces/:slug/runs/:run_id/events`; `GET /events` (SSE) |
| A durable queue | hub `internal/jobqueue` (to be extracted, D9) | SQLite, retry policy, per-key and group serialization, crash recovery |
| The envelope | the previous implementation (`1e6458d`, `internal/toolio`) | `status`, `summary`, `needs_human` with `--context`, `artifacts`, `side_effects`, `next`, warning codes, the exit-code table |
| The triage contract | the previous `issuetriage` | read-only tools, a terminating `file_issue` tool, every cited path resolved against the workspace, `rejected_path_calls`, `--overwrite` |
| The fix contract | the previous `codefix` | checks before and after, the verdict table, the revert check deciding `Closes` versus `Refs`, parking as `wip:` |

What does not exist: a process that runs any of it unattended, a record of
a job, a trigger of any kind, hub's sandbox and forge APIs, a credential
bound to a workspace that a service may hold, a Forgejo backend, the two
standalone modules, and the `af` toolbelt commands the architecture
document names (`af fix start/submit/land`, `af guard check`), which this
PRD's spec is the first to build.

## 1. Vocabulary

| Term | Meaning |
|---|---|
| **Service** | The `agent-fox` process: API, queue, workers, the hub client. |
| **Workspace** | A hub workspace: a repository, a branch, an owner, a mode (`standard` or `carry_patch`), and, once subscribed, the service's settings for it. The service never creates one. |
| **Subscription** | The service's record that it watches a workspace: the label prefix, the background PAT, the resolved settings. |
| **Job** | One unit of work on one workspace: `triage` or `fix`, an input, a lifecycle, zero or more attempts, at most one result. |
| **Attempt** | One execution of a job in one sandbox with one brain. Cost, events, the envelope, the lease and the hub session belong to the attempt. |
| **Lease** | What hub hands the worker for one sandbox: the gateway's URL, a token for that sandbox alone, an expiry. |
| **Profile** | A named sandbox shape hub holds: image, resources, network, TTL, the secrets to inject. |
| **Brain** | What runs the model and its loop, inside the box: the AgentKit driver (`af … run`) or a harness (Claude Code, Pi, …). The `Brain` interface of `docs/architecture.md`. |
| **Brief** | The document a brain works from: the skill, the issue, the facts the program measured. |
| **Submission** | What a brain hands back through the toolbelt: a diagnosis (`af triage submit`) or an implementation report (`af fix submit`). Never parsed from the brain's output. |
| **Label protocol** | The request and status labels under the workspace's prefix (§3). |

## 2. The shape

```
 forge (GitHub · GitLab · Forgejo)
      ▲  issues, labels, comments, PRs, webhooks       (only hub talks to it)
      │
 hub (the platform)   identity · workspaces · git server · secrets · patches · sessions · audit
                      sandboxes: create from a profile, inject secrets, lease, destroy   (new)
                      forge: issues, labels, comments, PRs for a workspace; webhooks in, events out   (new)
      ▲  validate caller · workspace · vars · lease a box · read/write issues · register a patch · session · events
      │
 agent-fox (the service)   one process; local or on a server
   api      /api/v1  jobs, workspaces, answers, events (SSE)      ← af, a web client, a hub task later
   triggers hub's event stream · a poll through hub's issues API · the API
   queue    jobqueue (standalone module), one attempt at a time per workspace key
   worker   the ring-2 program: clone from hub, lease a box, sync in, brief, brain, submit, gate, export, commit, push, ask hub to write the forge
      │
      │ gateway.Dial(lease.url, lease.token)
      ▼
 box (agentkit sandbox, hub's provider)   the tree (with .git) · agentkit-sandboxd · af (ring 1 and the driver) · the harness · the vendor key
```

Three facts about the picture:

- **Credentials never go down, and the worker holds almost none.** Hub
  holds the forge credential, the secrets and the provider's credentials.
  The worker holds the workspace's hub PAT and a sandbox lease. The box
  holds the vendor key, injected by hub from its secrets, and nothing
  else. Two hops of trust, both hub's.
- **Effects never come up.** A brain's commit inside the box stays inside;
  `Export` drops `.git`; the worker classifies the exported tree against
  its baseline and makes the commit that counts. The push, the pull
  request, the patch registration and every label are worker actions
  after the brain has stopped, and the forge ones go through hub.
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
| `af:ready` | status | the service | triaged: a diagnosis with confidence `confirmed` or `probable` is the body |
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
  table, through hub's forge API; a label that exists is left as it is. A
  workspace whose forge credential may not create labels fails to
  subscribe with the forge's answer, not later.
- **A label is never the only record.** The job record (§6) says what
  happened; the label is its mirror on the forge. The service reconciles
  on its own writes only; it does not watch for labels a person moved and
  undo them.
- `af:ready` is the triaged mark (over `af:validated`): the label is read
  by the person deciding whether to spend a fix on it, and "ready" is that
  question.

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

**The brain.** `af triage run` inside the box: the AgentKit driver over
`tools.For` on the workspace, with the six read tools and `code_search`
when the index is built; no tool that writes, no shell. The terminating
tool is `file_diagnosis` with the schema the previous `triage` validated:
`severity`, `confidence` (`confirmed` | `probable` | `suspected` |
`not_reproducible`), `root_cause`, `affected_files` (each resolved against
the workspace before the submission is accepted; a path not there is an
error back to the brain, counted in `rejected_path_calls`),
`suggested_fix`, `acceptance_criteria`, `question` when the brain could not
decide between readings. The vendor key is in the box's environment from
hub; the box's network is the vendor's and nothing else where the
provider can say so.

**The effects**, all worker-side after the brain stops, all forge writes
through hub:

1. The body is replaced (D6): the diagnosis rendered by Go from the
   submission, then `## Original report` with the body as it was. For a
   text or file input, an issue is created with that body, titled from
   the submission.
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

**Input.** An issue on the subscribed repository. With a triaged body the
brief starts from the diagnosis; without one the brain analyses first, as
the old `fix`'s analyse phase did, so a fix on an untriaged issue is
allowed and is simply more expensive.

**The brief.** The issue, the `af-fix` skill, the gate commands the program
detected (`af lang detect`) with their baseline result, the repository
map, and the rule that ends the skill: never commit, never push, never
edit the issue; when done, `af fix submit` with the report.

**The box and the brain.** The tree is synced in with `.git`, so `git log`
and `git diff` inside are true. The brain is the workspace's `AF_BRAIN`
(D8): a harness in the box with the vendor key from hub in its
environment, or the driver as `af fix run`. The harness's own hook
(`af guard check` in the image) is best-effort; the gate is the invariant.

**The gate**, worker-run inside the box over the proxy `execute`, keyed by
tree hash: the project's checks before any change and after the
submission, compared as the old `fix` compared them: `pass`,
`pass_was_already_failing`, `regressed`, `still_failing`. Then the revert
check: the non-test files put back, the tests left in, the checks run once
more; failing means the tests prove the fix (`Closes`), passing means they
do not (`Refs`, with a `fix_not_proven` warning).

**Landing**, worker-side, per the workspace mode (D7, §5):

- `standard`: branch `fix/<N>-<slug>` from the workspace's branch, one
  commit with the program's trailer, push to hub's git server (hub's
  `sync_mode` and the forge credential carry it to the forge; *spec
  time*: whether hub pushes a non-patch branch to `origin` today, or the
  worker pushes to `git_url` with a credential hub lends it for the push),
  then a pull request through hub's forge API whose body is rendered from
  facts (the verdict, the checks, the revert check, `Closes`/`Refs
  <issue url>`), draft when the verdict did not land.
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
pushes (no hub token, no forge token in the box), never makes a commit
that counts (`.git` is not exported), never reaches the service's state
(the job record is on the worker). The toolbelt refuses, with a structured
reason the brain reads on its next turn, a submission that cites a path it
did not read, a report that names no commit subject, or a tree that
touched a protected path (`.specs/`, `.github/workflows/`, the forge's own
config files).

## 5. The carry-patch workflow

Hub's carry-patch workspace is the lifecycle of an agent's output on a
fork: an ordered list of patch branches, an integration branch rebuilt as
upstream plus the patches, merged patches detected and retired, conflicts
named by file, resolutions remembered with rerere. The service uses it in
three places and is careful about one.

### 5.1 A fix is a patch

On a `carry_patch` workspace a landed fix is registered, not merged:

1. The branch is `patch/<N>-<slug>`, cut from the current upstream base
   (`refs/remotes/upstream/HEAD` as the last sync left it; the worker
   reads `upstream_head_sha` from the workspace and clones from hub's git
   server, which has every ref).
2. The push target follows `PATCH_BRANCH_SOURCE`, read from
   `GET /workspaces/:slug/vars/resolved`: `hub` means `hub_url` with the
   PAT; `origin` means the fork at `git_url`, which the worker cannot
   reach with a credential of its own under D4. *Spec time, hub's:* either
   `PUSH_PATCHES_TO_ORIGIN=true` forwarding (the worker pushes to hub, hub
   forwards to the fork before writing its ref, which is what that
   variable already does in `origin` mode) is the required configuration
   for a service-driven `origin` workspace, or hub lends a short-lived
   push credential in the lease. The recommendation is the first: it
   exists.
3. `POST /workspaces/:slug/patches {branch_name, description,
   upstream_pr_url?, if_not_exists: true}`. The description is the issue's
   title and URL. Position is appended; a workspace may pin it with
   `AF_PATCH_POSITION=first`.
4. In `origin` mode the service calls `POST /workspaces/:slug/sync`, which
   brings the fork's tip in and enqueues the rebuild; in `hub` mode the
   push hook already enqueued it. Either way the response carries
   `rebuild_job_id`.
5. The service watches `GET /workspaces/:slug/rebuilds/:id` to a terminal
   state, or hub's event stream. `completed` with the patch `success` is
   `af:landed`; the comment names the patch, its position and the
   integration branch's new head.

### 5.2 The upstream pull request

When `AF_FIX_UPSTREAM_PR=true`, and only then, the service asks hub to
open a pull request on the upstream repository from the fork's branch
(hub's forge API with `head: <fork-owner>:patch/…`, `base` the upstream's
default branch, the upstream credential hub already resolves for sync),
records its URL as the patch's `upstream_pr_url` (`PATCH /patches/:id`),
and from then on hub's sync detects the merge, squash included, and
retires the patch. Off by default: a team that carries internal patches
does not want every fix proposed upstream.

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
did not cause (an upstream advance): the service reads the rebuild's
result from hub's event stream and acts on a `conflict` in a patch it
registered. Patches it did not register are not its business unless
`AF_FIX_FOREIGN_CONFLICTS=true`.

### 5.4 What the service never does on a carry-patch workspace

Commit to the integration branch; push to the upstream remote; push a
registered patch branch to hub in `origin` mode without forwarding on;
submit a rebuild while one is running (hub answers 409; the service waits
on the running one); touch the trunk. These are the rules hub's own agent
guidance (`docs/examples/AGENTS_carry_patch.md`) gives a person, and the
program enforces them on the brain by giving it no way to do any of them.

## 6. The service

### 6.1 Configuration

`config.toml` in the working directory or `$XDG_CONFIG_HOME/agent-fox/`,
with `${VAR}` expansion, the same shape as hub's. Under D4 and D10 it is
short: no forge, no provider, no vendor key.

```toml
[server]
listen = "127.0.0.1:8090"
external_url = "https://fox.example.com"

[hub]
url = "https://hub.example.com"
auth_cache_ttl = "60s"           # D2; at most 15m

[database]
path = "agent-fox.db"            # SQLite: subscriptions, jobs, attempts, events; the jobqueue tables beside them

[secrets]
key_file = "/etc/agent-fox/key"  # encrypts the stored workspace PATs at rest

[workers]
count = 2                        # attempts in flight across all workspaces
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
| `AF_POLL_INTERVAL` | `5m`; `0` disables | how often the subscription polls the forge through hub |
| `AF_SANDBOX_PROFILE` | hub's default profile | the profile hub creates the box from |
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
PUT    /workspaces/:slug                        subscribe: {label_prefix?, hub_token}; creates the labels through hub
GET    /workspaces/:slug                        the subscription, its resolved settings, its label state
DELETE /workspaces/:slug                        unsubscribe; labels are left in place
POST   /workspaces/:slug/triage                 {issue} | {text, title?} | {file: {name, content}}; → 202 {job}
POST   /workspaces/:slug/fix                    {issue, context?}                                → 202 {job}
GET    /workspaces/:slug/jobs?kind=&state=&issue=&cursor=
GET    /jobs/:id                                the job with its attempts' summaries
POST   /jobs/:id/cancel                         stops the attempt, releases the lease (hub destroys the box), restores the request label
POST   /jobs/:id/answer                         {answer} | {option}: a new attempt with --context, from blocked
POST   /jobs/:id/retry                          from failed
GET    /jobs/:id/events?since=<seq>             replay from the stored log, then tail (SSE)
GET    /jobs/:id/attempts/:n/envelope           the attempt's envelope, full detail
GET    /jobs/:id/attempts/:n/report             the report file
```

No `/hooks` route: the forge's webhooks are hub's (D11).

A job:

```jsonc
{
  "id": "job_…", "workspace": "widgets", "kind": "fix",
  "input": { "issue": "https://github.com/acme/widgets/issues/42" },
  "source": { "kind": "label" | "event" | "api" | "rebuild" | "triage", "by": "usr_… | label af:fix | hub event id" },
  "state": "queued" | "running" | "blocked" | "done" | "failed" | "cancelled",
  "blocked": { "reason": "needs_human", "question": "…", "options": [ … ], "answer_with": "POST /jobs/job_…/answer" },
  "attempt_count": 1, "current_attempt": 1,
  "result": { /* the envelope's result: summary view */ },
  "artifacts": [ { "kind": "branch", … }, { "kind": "pull_request", … }, { "kind": "patch", "id": "…", "position": 3 } ],
  "side_effects": [ … ],
  "labels": { "set": "af:landed", "removed": ["af:fix", "af:in-progress"] },
  "usage": { "cost_usd": 1.84, "input_tokens": …, "output_tokens": … },
  "hub": { "session_id": "…", "run_id": "20261019_141205_a1b2c3", "sandbox_id": "sbx_…" },
  "created_at": "…", "started_at": "…", "finished_at": "…"
}
```

The lifecycle is the hub tasks draft's, reduced: no `draft`, no `paused`,
no position (the queue is per workspace, first come first served, one
attempt at a time per workspace key). `blocked` is a state because it is
the one state the queue must not claim and a client must show.

### 6.3 Triggers

- **Hub's event stream.** For each subscription, one SSE connection to
  `GET /api/v1/events?workspace=<slug>` with the workspace's PAT. The
  events acted on: `forge.issue.labeled` with a request label (a job;
  `source.kind: event`), `hub.rebuild.completed` and `hub.rebuild.failed`
  for a patch the service registered (§5.3). A dropped stream is
  reconnected with backoff; what was missed is caught by the poll.
- **Poll.** Every `AF_POLL_INTERVAL`, hub's issues API with the request
  labels as the filter (`GET /workspaces/:slug/issues?state=open&labels=af:triage`
  and the same for `fix`). Each issue not already the input of an open
  job becomes a job with `source.kind: label`. The request label is
  removed when the job is created (§3), so a poll never sees the same
  request twice; a forge that lags is handled by the open-job check.
- **The API.** `POST /triage` and `/fix` from `af` or any client, with the
  caller's identity as `source.by`.

A webhook and the poll on the same workspace coexist; the poll is the
catch-up for a lost delivery, and a workspace whose forge cannot deliver
webhooks to hub runs on the poll alone.

### 6.4 Execution: one attempt

```
claim      the queue hands the worker a job; af:in-progress through hub; a hub session (POST /sessions, archetype triage|fix, run_id YYYYMMDD_HHMMSS_6hex)
resolve    the workspace from hub with the subscription's PAT; its vars; the issue thread through hub's forge API
clone      the workspace from hub_url (every ref, the PAT as the HTTP Basic password); the base branch checked out; the attempt's branch created on the worker
lease      POST /workspaces/:slug/sandboxes {profile, purpose: triage|fix, ttl}: hub creates the box on its provider with the profile's image, the resolved secrets in Env, the network, and returns {id, gateway_url, token, expires_at}
box        gateway.Dial(lease.url, lease.token); the sandbox by id; Sync(LocalDir{Path: clone}) with .git; the gate's baseline inside, cached by tree hash
brief      af triage brief | af fix brief: the rendered document, byte-identical in its run-constant prefix, synced in under .agent-fox/
brain      Brain.Run(brief): one execute that starts af … run (the driver) or the harness inside the box; its submission arrives through af … submit inside, as a file under .agent-fox/
measure    the gate after, the revert check, the scope check, the protected-path revert; a refusal goes back to the brain as its next turn, within the attempt's bounds
export     sb.Export(LocalDir{clone}), .git left behind; the worker classifies the tree and makes the commit with the program's trailer
land       push to hub's git server; the pull request or the patch registration through hub; the body, the comment, the labels through hub's forge API   (af fix land / af triage land)
report     the envelope and the events file stored as attempt artifacts; usage to the hub session; events to hub audit; the session completed
release    DELETE /workspaces/:slug/sandboxes/:id (hub destroys the box; the lease's expiry destroys it anyway); the job's state; the next job
```

A refusal at `measure` is an attempt that continues; a brain that ends
without a submission is `category: no_result`; a box that goes unavailable
is `sandbox_unavailable`, retryable once with a new lease; a hub or forge
write that fails after the push is a warning on a landed run, as the old
envelope ruled (tracking never fails a done run). A lease hub refuses
(capacity, an unknown profile) is a retryable failure with hub's reason.

*Spec time, with hub:* whether hub creates the sandbox and the worker
attaches by id (the gateway today gives a sandbox to the token that
created it; a per-lease token that hub mints and the gateway accepts is
the natural fit), or hub returns a lease that lets the worker create the
box itself under hub's profile. Either keeps the properties above; the
first keeps `afc sandbox list` honest about who owns what.

### 6.5 State

SQLite, one file, the service's own: `subscriptions`, `jobs`, `attempts`,
`events` (sequence-numbered per job), `hub_events` (the ids of hub events
already acted on), beside the `jobqueue` module's tables. The report file
and the events file of an attempt are stored under
`$XDG_STATE_HOME/agent-fox/attempts/<id>/`. Nothing a transcript would
hold is stored; the brain's own transcript, when it keeps one, is a path
in the envelope and is never read by the service.

## 7. The CLI

`af` is hub's `afc` pattern: a thin client, the credential from
`~/.af/config.toml`, the server from `AF_URL` or `--server`, one JSON
object on stdout and the exit-code table of the old envelope (`0` done, `1`
failed, `2` usage, `3` needs_human, `4` unverified), so a script and a
model-driven caller read it the same way.

```
af login                                       afc login; the same file
af workspace subscribe <slug> [--prefix af:] [--hub-token-file …]
af workspace list | get <slug> | unsubscribe <slug>

af triage <issue|file|text> --workspace <slug>  [--context …] [--wait]     → 202 and a job id; --wait follows to a stop
af fix    <issue>           --workspace <slug>  [--context …] [--wait]

af jobs list [--workspace] [--state] | get <id> | watch <id> | cancel <id> | answer <id> "<text>"|--option B | retry <id>

af triage <issue|file|text> --local [--dir .] [--brain claude|agentkit] [--sandbox landlock|podman|none]
af fix    <issue>           --local [--dir .] [--land pr|branch|none] …
```

**`--local` is the same pipeline with no service and no hub.** The CLI
builds the worker in-process: a `landlock` or `podman` provider on this
machine (or `none`, the checkout itself, when a person is their own
boundary), the brain from the flag, the forge through `issuex` directly
with the token the old tools read (`GITHUB_TOKEN`, `GITLAB_TOKEN`), the
vendor key from the environment, the checkout from `--dir`, and the
envelope on stdout. It is the "local CLI wrapping a model or agent harness"
of the brief, and it is the developer's loop for the skills: a change to
`af-fix` is tried with `af fix --local` before any service sees it. Hub is
optional in this mode: with `--workspace`, the local run still reports its
session and its audit events to hub; without, it reports nothing. `--local`
is the one place the driver may run on the host over proxy tools (the
architecture document's borrowed hands), because the host is the person's.

**The toolbelt is the same binary.** Inside a box, and for a person at a
shell with Claude Code open, `af triage brief`, `af triage run`, `af triage
submit`, `af fix brief`, `af fix run`, `af fix submit`, `af gate run`, `af
guard check` are the commands the architecture document names; the
service's workers call the same Go functions in-process for the ring-2
steps. One implementation, three callers: the worker, the box, the person.

## 8. Working with hub

| The service needs | Hub gives | How |
|---|---|---|
| who is calling | the user behind the bearer | `GET /api/v1/user` with the caller's token; cached for `auth_cache_ttl` by token hash |
| may they act on this workspace | ownership (later: org membership) | `GET /workspaces/:slug` with the caller's token; 404 is 404 |
| the repository, the branch, the mode, the integration branch, the upstream | the workspace record | `GET /workspaces/:slug` with the subscription's PAT |
| the per-workspace settings | variables, resolved workspace over org over user | `GET /workspaces/:slug/vars/resolved` |
| a clone with every ref, private by construction | the git server | `git clone <hub_url>` with the PAT as the HTTP Basic password; push the same way |
| a sandbox with the vendor key inside | the sandbox API (new) | `POST /workspaces/:slug/sandboxes` → a lease; `DELETE …/:id`; `afc sandbox list` for a person |
| the issue, its labels, the body rewrite, the comment, the pull request | the forge API (new) | `GET/POST/PATCH /workspaces/:slug/issues[/:n]`, `…/issues/:n/comments`, `…/issues/:n/labels`, `POST /workspaces/:slug/labels`, `GET/POST /workspaces/:slug/pulls[/:n]`; `afc issue`, `afc pr` for a person |
| a label event, a rebuild result | the event stream, with the forge's webhooks relayed (new) | `GET /api/v1/events?workspace=`; `POST /hooks/forge/:slug` is hub's ingress |
| where a patch branch goes | `PATCH_BRANCH_SOURCE`, `PUSH_PATCHES_TO_ORIGIN` | the variables; the push target follows them (§5.1) |
| the patch registered and carried | the patch list, the rebuild | `POST /patches` (`if_not_exists`), `POST /sync`, `GET /rebuilds/:id`, `PATCH /patches/:id` for `upstream_pr_url` |
| cost per workspace | sessions and usage | `POST /sessions` (`archetype`, `model`, `run_id`), `POST /sessions/:id/usage` per model turn, `POST /sessions/:id/complete` |
| an audit trail a person can query beside hub's own | audit ingestion | `POST /workspaces/:slug/runs/:run_id/events/batch` with the attempt's events |

The subscription's PAT therefore carries: `workspaces:read`, `vars:read`,
`git:read`, `git:write`, `patches:write`, `rebuilds:read`,
`workspaces:sync` (for §5.1 step 4 in `origin` mode), `sessions:write`,
`audit:write`, `audit:read` (the event stream), and the two new scopes
hub's sandbox and forge APIs will register (*to be named by hub*;
`sandboxes:write` and `forge:write` are the obvious ones).

What the service does not take from hub: the job queue (D9, a shared
module instead), and the job record itself, which is the service's until
the hub tasks convergence (§10).

## 9. What this asks of hub

Each is a hub spec, written there, in this order, because the service's
step 2 (§10) cannot run without the first two:

1. **A sandbox API and `afc sandbox`.** Profiles in hub's configuration
   (image, resources, network, TTL ceiling, the secret names to inject),
   one provider per deployment (`podman` on hub's host, or an
   `agentkit-gateway` by URL and token), `POST /workspaces/:slug/sandboxes`
   returning a lease, `GET` to list, `DELETE` to destroy, TTL enforced by
   hub as well as by the provider, `afc sandbox create|list|destroy`, and
   a new scope. The secrets a profile names are resolved workspace over
   org over user and placed in `Spec.Env`; they never appear in any
   response. *Spec time there:* per-lease gateway tokens (the gateway
   gains a token-minting seam, or hub holds the gateway token and relays
   the Agent service itself, becoming a gateway of its own).
2. **A forge API and `afc issue`, `afc pr`.** Over `issuex` with the
   workspace's `GIT_PAT` (and the upstream credential for §5.2): the
   endpoints in §8, label creation, body update, comments, pull requests,
   a webhook ingress `POST /hooks/forge/:slug` with per-workspace secrets
   and replay protection, and the relayed events on the SSE stream. A new
   scope.
3. **`jobqueue` and `issuex` as standalone repositories** (D9), hub
   consuming both by version.
4. **A workspace-bound credential a service may hold** (D3): confirm what
   the README's "workspace-scoped token" is, or add a PAT binding
   (`workspace: <slug>`) that the ownership checks honour. Until then a
   plain PAT with the §8 scopes works and sees every workspace its user
   owns, which is wider than it should be.
5. **Org-member reads of org workspaces** (D2), already named by the inbox
   draft as the prerequisite for any team use.
6. **Hub pushes a non-patch branch to the forge**, or lends a credential
   for it (§4.2, the `standard` landing). *To confirm against hub's
   archive-time push, which already pushes every local branch to
   `origin`; a per-branch push on request is the same code behind an
   endpoint.*

Items 1 to 3 gate the service; 4 to 6 gate team use and the `standard`
landing respectively, and a single-owner deployment with carry-patch
workspaces is the first release's target.

## 10. Order of work

Each step is a spec package, lands with `make check` green, its docs
updated, and is usable on its own.

1. **The toolbelt and `--local`** (this repository, starts now, needs
   nothing from hub). `af triage` and `af fix` end to end on one machine:
   the `Brain` interface with the `agentkit` and `claude` implementations,
   the briefs rendered from the skills, `af … submit` validation, the gate
   on the hands' runner, the revert check, landing on a `standard`
   repository as a pull request through `issuex`, the envelope. Sandbox on
   `landlock` and `podman`; offline on `local` with `faux` and a scripted
   harness. This is the old `triage` and `fix` rebuilt on the hands, and
   it is the part every later step calls. `issuex` is consumed from its
   new repository as soon as it exists; until then from here.
2. **Hub: sandboxes, the forge, the two modules** (hub, §9 items 1 to 3).
   In parallel with step 1.
3. **The service.** `agent-fox`: configuration, SQLite, the queue from the
   module, hub authentication with the cache, subscriptions, the jobs API
   with SSE, one worker running step 1's pipeline over a hub lease, every
   forge write through hub, hub sessions and audit. `af` as its client.
   Triggers: the API and the poll.
4. **The label protocol and the event stream.** Labels created on
   subscribe, hub's relayed webhook events as the trigger, `AF_AUTO_FIX`,
   `answer` and `retry` from the forge side (re-labelling after a
   comment).
5. **Carry patches.** §5 whole: the patch branch, the push target by
   authority, registration, the rebuild watched on the event stream, the
   upstream pull request, the conflict job.
6. **The `standard` landing through hub** (§9 item 6), org-member reads,
   the bound credential: the first team deployment.
7. **Forgejo**, as an `issuex` backend with its webhook in hub's ingress.
8. **Convergence with hub tasks**, when that draft is built: a task whose
   program is `af fix`, the service's queue replaced by hub's, the
   subscription replaced by a schedule. The job API of §6 is the contract
   that makes this a move, not a rewrite.

Steps 4 and 5 are independent once 3 has landed.

## 11. Acceptance criteria

Offline, in `make check`, with an `httptest` hub serving its documented
API plus the sandbox and forge endpoints of §9 (the sandbox lease pointing
at an in-process `gatewayd.Server` over `local`), and the brain on `faux`
or a scripted harness:

1. `PUT /workspaces/widgets` with a valid hub token and a PAT creates the
   seven labels through hub's forge API (the requests observed), and a
   second call changes nothing.
2. A `forge.issue.labeled` event for `af:triage` on the `httptest` hub's
   stream produces a job whose attempt leases a box, runs `af triage run`
   inside it on `faux`, replaces the body through hub with a diagnosis
   whose `## Original report` section is byte-identical to the original
   body, removes the request label, adds `af:ready`, opens a hub session,
   reports usage and completes it, and releases the lease. The same job
   from a poll when the stream is silent.
3. A triage whose scripted brain cites a path not in the workspace gets the
   refusal as its next turn and the attempt ends with
   `rejected_path_calls: 1` and a diagnosis citing only real paths.
4. An `af:fix` event on a `standard` workspace produces a branch
   `fix/42-…` in the worker's clone, one commit carrying the program's
   trailer, a push to the `httptest` hub's git server, a pull request
   through hub's forge API whose body says `Closes` when the revert check
   failed and `Refs` when it passed, a `fix_summary` comment, and
   `af:landed`. The brain's own commit inside the box, when it made one,
   is not on the branch.
5. The same on a `carry_patch` workspace with `PATCH_BRANCH_SOURCE=hub`
   pushes `patch/42-…` to `hub_url`, registers it with `if_not_exists`,
   reads the rebuild to `completed`, and sets `af:landed`; with
   `PATCH_BRANCH_SOURCE=origin` and `PUSH_PATCHES_TO_ORIGIN=true` it pushes
   to `hub_url` and calls `/sync`, and with `origin` and no forwarding it
   refuses at preflight naming the variable. A rebuild answering
   `conflict` with files creates a `fix` job whose brief names them.
6. A brain that ends with a `question` leaves the job `blocked`, the issue
   `af:needs-human` with one comment; `POST /jobs/:id/answer` starts a new
   attempt whose brief carries the answer as `--context`, and the second
   attempt's run-constant prefix is byte-identical to the first's.
7. A token validated once is not sent to hub again within
   `auth_cache_ttl` (the `httptest` hub counts `GET /user`); after the TTL
   it is; a 401 evicts it at once.
8. `af fix <issue> --local --dir ./repo` on a temporary repository with a
   scripted harness and an `httptest` forge lands a branch and prints the
   envelope with exit 0; with a hub token and `--workspace` it opens and
   completes a hub session; without, no request reaches the `httptest`
   hub.
9. The worker never holds a vendor key or a forge token: a test over the
   worker's environment and over every request it sends asserts that the
   only credentials are the subscription's PAT and the lease token; the
   box's `Spec.Env`, observed at the `httptest` hub's sandbox endpoint,
   holds the profile's secrets and nothing of the worker's.
10. A worker killed between `export` and `land` leaves the job `queued`
    with the attempt failed as retryable and the clone's branch intact; the
    next attempt reuses the branch and does not re-run the brain when the
    submission file is present and valid; the orphaned lease is released
    on the worker's next start. *Spec time:* whether the second attempt
    re-measures the exported tree or trusts the stored verdict.

Measured, after the first release, against the old tools' numbers: cost
per `af:ready`, cost per `af:landed`, the share of `af:landed` pull
requests merged without a human commit, and the two brains compared on
the same issues (D8).

## 12. What is given up, stated plainly

- **The service cannot run without hub.** The first pass could triage with
  a forge token and a Podman socket; this one needs hub for the box and
  for every forge byte. `af --local` is the standalone path, and it is a
  developer's, not a deployment's.
- **Hub grows two subsystems** (sandboxes, the forge) before the service
  can run at all, and takes on webhook ingress. That is the price of one
  credential store, and it is what makes hub the platform the brief asked
  for.
- **The driver loses borrowed hands in the service.** Every brain runs in
  the box with the vendor's network open, because the key is only there.
  The `NetworkNone` triage of the first pass survives only under
  `--local`. A gateway whose backend filters egress narrows it back.
- **Labels can lie for a moment.** The label is written after the job
  record; a forge write that fails leaves the record right and the label
  stale, with a warning in the envelope. The job API is the truth.
- **One attempt at a time per workspace.** Simpler than the tasks draft's
  position and concurrency, and enough for a team whose forge rate limits
  would object to more. Concurrency is a variable later.
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
| the topology, the attempt pipeline, the label protocol, the carry-patch use, the driver in the box | `docs/architecture.md`: the *to be written* sections it reserves, a new section for the service, and the brain table's placement column |
| the decisions, once implemented | one ADR per decision that changes the architecture document (D4, D6, D7, D8, D9) |
| hub's asks | one issue on hub per item of §9, this PRD linked |
| `issuex`'s move | this repository's `README.md` and `AGENTS.md` layout; the new repository's README |
| the skills | `skills/af-triage` (new), `skills/af-fix` (the brief's rule: submit through `af`, never commit) |
| setup and quickstart | `README.md` |
