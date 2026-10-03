# Rebuild the inbox on hub and AgentKit

**Status:** superseded by
[add_tasks_to_the_hub.md](add_tasks_to_the_hub.md), which restarts from first
principles with the sandbox and multi-tenant workspace as hub baseline. Kept
for the findings about pizza-bot and the component map.

## Intent

[Pizza Bot](https://github.com/agent-fox-dev/pizza-bot) is an inbox for
long-running AI work: start or schedule a task, walk away, come back to
finished work in **Unread** and decisions waiting in **Action**. It is a
single-user, single-process TypeScript system on DeepAgents/LangGraph, with a
web app, an Electron shell and a terminal REPL as clients.

This PRD explores rebuilding that product on the agent-fox stack:

- **hub** supplies identity (GitHub OAuth, API keys, PATs, admin tokens),
  tenancy (users, orgs, workspaces), secrets and variables at three scopes, a
  durable SQLite job queue, an SSE event fan-out, a git server, and agent
  session accounting.
- **agentkit-go** supplies the agent: the loop, five wire APIs, tools with
  workspace containment, the `BeforeToolCall` authorization boundary, skills,
  MCP client and server, an append-only session log with resume, subagents,
  stop policies and budgets.
- **agent-fox** supplies finished pipelines (`spec`, `triage`, `fix`, `impl`,
  `issue`) that are exactly the kind of hour-long work an inbox exists to
  hold, plus the event and envelope conventions (PRDs 01 to 05) a run should
  report through.

Three asks drive it: a complete backend rewrite on hub, multi-tenancy with
OAuth through hub's existing authentication, and an inbox and work queue per
workspace rather than one global inbox.

## Findings: the client/server seam in pizza-bot today

**There is a clear transport seam.** Every client reaches the api-server only
over HTTP/SSE. The Electron shell forks the api-server as a sidecar and renders
the same React app; the CLI is a REPL over the SDK client; the browser build is
static. No client imports the runtime or a model binding, and an eslint rule
enforces it (`AGENTS.md`, "Layering discipline").

**It is not a neutral API contract.** The seam has two halves of different
quality:

| Half | What it is | How it is specified |
|---|---|---|
| Run streaming | Four Agent-Protocol routes (`POST /threads/:id/commands`, `.../runs/:run_id/cancel`, `.../stream/events`, `GET .../state`) carrying `@langchain/protocol` `ProtocolEvent` frames | By the `@langchain/langgraph-sdk` `Client`/`StreamController`: the client decodes LangChain's event vocabulary (channels `values`, `messages`, `updates`, `lifecycle`, `tools`, `tasks`, `input`, `custom`; interrupt frames; `Command({resume})` semantics) |
| Platform REST | About 75 routes: threads, folders, triggers, attachments, skills, plugins, MCP servers, providers, settings, memories, local folders, logs, lifecycle | A hand-written 1,050-line `ApiClient` class in `apps/web`, typed by importing `@pizza-bot/core` and `@pizza-bot/plugin-api`; a handful of zod wire schemas; no OpenAPI |

So a Go server would have to either speak LangChain's wire byte for byte, or
the web app's `protocol-stream-store.ts` and `projection/*` get rewritten. The
REST half is a TypeScript-shaped contract, discoverable only by reading the
Hono routes.

**There is no tenancy.** One shared bearer token (`PIZZA_API_TOKEN`) with full
access, one `PIZZA_DATA_ROOT` of three SQLite files, and the documented rule
"only run one API process for a given data root". Users, orgs, workspaces and
scoped credentials do not exist as concepts anywhere in the code.

**What is worth keeping is the product semantics, not the code:**

- The inbox model: threads with `unread` and `awaiting_action` flags; global
  **Unread** and **Action** smart views; folders as an orthogonal layer that
  never hides work from the global queues; pinned threads.
- The HITL vocabulary: `approve | edit | reject | respond`, declared per tool
  by the skill (`interruptOn`), with the invariant that pre-interrupt side
  effects must be idempotent because resume re-runs the step.
- Runs outlive clients: a run is started, the SSE connection may drop, and a
  reconnect replays buffered frames from a sequence number then tails live.
- Triggers: cron and webhook, each firing a run, tracked by a durable
  occurrence row (`pending → running → succeeded | failed | dead`) with
  missed-run recovery on restart and a per-occurrence attempt budget.
- Attachments stored by reference, inlined only at the model call.
- Skills as tool-scoped delegates whose progress renders in an Activity rail.
- A machine-readable error taxonomy (`AUTH_EXPIRED`, `TIMEOUT`, `RATE_LIMIT`,
  `CONTEXT_LENGTH`, `MODEL_UNAVAILABLE`, `STREAM_ERROR`, ...) so the UI can pick
  a recovery affordance.
- A protocol version advertised on `GET /` so a client detects an incompatible
  server before a decode error.

## Why not change pizza-bot in place

Four things in pizza-bot are load-bearing and would all have to go at once:
the graph runtime (DeepAgents), the wire (LangGraph SDK protocol), the
persistence (LangGraph checkpointer and store), and the single-tenant data
root that every store, route and the trigger scheduler assumes. The web app's
stream store is written against the SDK, so the frontend transport layer goes
with them. What survives is the UI component layer (vendored AI Elements) and
the semantics above. That is a rewrite with a reference implementation, not a
migration, and this PRD treats it as one.

## The shape of the replacement

### Process topology

```
                 ┌──────────────────────────────────────────────┐
  web client     │  hub  (control plane)                        │
  / CLI  ──HTTP──┤  identity · orgs · workspaces · secrets      │
       ──SSE────┤  threads · inbox · approvals · triggers      │
                 │  jobqueue (SQLite) · event log · SSE fan-out │
                 └──────────────┬───────────────────────────────┘
                                │ claims jobs, streams events back
                 ┌──────────────┴───────────────────────────────┐
                 │  worker  (data plane, N per deployment)      │
                 │  agentkit Agent per run · session JSONL      │
                 │  tools rooted at the workspace clone         │
                 │  MCP client pool · skills · subagents        │
                 │  agent-fox pipelines as built-in skills      │
                 └──────────────────────────────────────────────┘
```

Hub owns every record and every endpoint the clients see. Workers run the
agents, and a worker is a container. This is already the pattern hub's
audit-ingestion endpoints (`POST /api/v1/workspaces/:slug/runs/:run_id/events`)
and `GET /api/v1/events` were built for.

### The sandbox semantic

Both repositories already define the execution environment, and the worker
adopts it rather than inventing one:

- `quay.io/agentfox/sandbox` (`containers/sandbox/Containerfile` in hub and
  agent-fox): a RHEL 10 base with Go, Node, Rust and Python toolchains. The
  repository lives at `$WORKSPACE` (`/opt/app-root/workspace`); agent config,
  credentials and session state live in `$HOME` (`/opt/app-root/src`), so
  agent state never mixes into the code.
- `quay.io/agentfox/agents` and `quay.io/agentfox/tools` layer the coding
  agents and the four agent-fox tools on top of it.

The inbox worker is one more layer on `sandbox`: a `worker` image carrying the
AgentKit-based run loop and the agent-fox tools. The rules that follow:

- **One container per job.** Hub enqueues; a scheduler on the worker host
  starts a container for the job with the workspace-scoped token, the
  resolved secrets as environment, and the job id. The container clones the
  workspace from hub's own git server (`/git/<org>/<slug>.git`) into
  `$WORKSPACE`, runs, pushes the branch back, reports, and exits. Isolation,
  cancellation (stop the container) and credential scoping all come from the
  container boundary, which is what agent-fox PRD 04 asked of its job runner.
- **The container is the sandbox; the in-process guard is a floor.**
  AgentKit's `guard.Restricted` and agent-fox's shell classifier stay on, but
  they are not the security boundary. AgentKit's own PRD (OQ-8, option c) and
  agent-fox's CLI reference say the same: anything genuinely untrusted belongs
  in a container.
- **Session logs outlive the container.** `$HOME` state that must persist,
  which is the thread's session log and the run's event log, is written to a
  per-workspace volume mounted into every container for that workspace, and
  shipped to hub as events. A fresh container resumes a thread from the log.
- **No network beyond hub, the model vendor and declared MCP servers.** The
  container's egress policy is part of the job, not of the agent.
- **Scheduling.** The first worker host is a single machine running
  `podman`; the hub Kubernetes deployment (`deploy/`) gets a worker that
  starts jobs as pods later. The scheduler is a small loop over
  `jobqueue`, not a new service.

The single-user and local case is the same shape with one worker host on the
same machine as hub.

### Component map

| Pizza Bot today | Replacement | Exists? |
|---|---|---|
| `runtime-langgraph` (DeepAgents graph, middleware stack) | `agentkit.Agent` with middleware (`Budget`, `Retry`, `Caching`), stop policies, compaction | yes |
| Checkpointer (`checkpoints.sqlite`) | `session` JSONL log per thread; `NewAgentFromSession` resumes; a compaction is an entry, not a rewrite | yes |
| Store (`store.sqlite`, cross-thread memory) | per-workspace context files and skills under the workspace clone, under AgentKit's trust gate | partly: files yes, a "memory" tool no |
| `app.sqlite` (threads, folders, triggers, FTS, attachments) | hub SQLite tables: `threads`, `inbox_state`, `triggers`, `trigger_occurrences`, `attachments`; FTS5 in modernc sqlite | no |
| `ProtocolRunManager` (run registry, replay, cancel) | hub `jobqueue` for the run lifecycle + a per-run event log with sequence numbers + the audit `SSEManager` for live tail | partly: queue and SSE yes, replay-by-sequence no |
| `ProtocolEvent` wire | `core.Event` JSON form (`session.EventJSON`) carried over SSE with `seq`; one documented schema | partly: the JSON form exists, the SSE envelope does not |
| HITL interrupts (`interruptOn`, `Command({resume})`) | an **approval** primitive built on `BeforeToolCall` plus session resume (see "The run model") | **no** |
| Skills (`SKILL.md`, `interruptOn`, `mcp:server:tool` refs) | `skills` package manifests + `subagent.Definition` per skill; `interruptOn` becomes an approval policy on the tool | partly: manifests yes, the approval field no |
| Plugins (MCP servers + skills bundles) | `mcp` client pool + `plugins` registry | yes, modern MCP era only |
| `inference-providers` (Bedrock, Anthropic, Gemini, OpenAI, OpenRouter, Requesty, Ollama) | `catalog` + `provider/{anthropic,openai,openairesponses,google,ollama}` | **Bedrock missing**; OpenRouter/Requesty via the OpenAI-compatible path |
| Provider credentials in Electron `safeStorage` / env refs | hub secrets at user, org and workspace scope, resolved per run | yes |
| `PIZZA_API_TOKEN` shared bearer | hub API keys (interactive), PATs (scoped automation), workspace-scoped tokens, admin token | yes |
| Local folder grants (`/local/<id>/`) | the workspace clone at `$WORKSPACE` is the tool root; there are no host folder grants, a container sees only its mounts | by design |
| Trigger scheduler (cron, webhook) | jobqueue `available_at` for delayed jobs + a cron tick that enqueues occurrences + a webhook route | **cron missing** |
| `GET /threads/activity/events` (desktop notifications) | the same SSE stream filtered to terminal run events; browser notifications in v1 | yes |
| Web `ApiClient` + `protocol-stream-store` | a generated client from hub's OpenAPI 3.1 description + one SSE decoder | no |

### A run, end to end

1. A client (or a trigger, or an agent-fox pipeline) posts a message to a
   thread in a workspace. Hub appends the user message to the thread, marks
   nothing unread yet, and enqueues a `run` job keyed by the thread id.
   Per-key serialization in `jobqueue` guarantees one run per thread at a
   time; `group_key` = workspace slug gives per-workspace fairness.
2. A worker claims the job, opens a hub agent session (`POST /api/v1/sessions`,
   already built), resumes the thread's session log, resolves the model and
   credentials from workspace > org > user secrets, builds the `Agent` with the
   workspace's skills and MCP tools, and runs.
3. Every `core.Event` is appended to the run's event log with a monotonic
   `seq` and pushed to hub, which fans it out over SSE to subscribers of the
   thread, the workspace inbox, or the user's global inbox.
4. The run ends in one of four ways, each a jobqueue terminal state plus an
   inbox transition: **completed** (thread becomes Unread), **needs_human**
   (thread becomes Action, see approvals), **failed** (Unread with an error
   card and the error code), **cancelled** (no inbox change).
5. A client reconnecting passes the last `seq` it saw and receives the gap
   from the log, then the live tail. The log is the durable record; the SSE
   buffer is a cache.

## Goals

- Every pizza-bot inbox behaviour listed under "worth keeping" works
  unchanged from the user's point of view.
- A user signs in once through hub's OAuth and sees only the workspaces they
  own or are a member of. No client ever holds a provider API key.
- Each workspace has its own inbox (Unread, Action, All, folders) and its own
  work queue (runs, triggers, long pipelines), and the user has a global
  inbox that is the union over their workspaces.
- A run survives the client, the worker, and hub restarting: the session log
  and the job row are the truth, not a process.
- The client/server contract is a documented OpenAPI description plus one
  event schema, and the web client is generated from or
  conformance-tested against it, in that order of preference.
- agent-fox's pipelines are first-class inbox work: `impl 09` on a workspace
  is a job whose envelope lands in Unread, or in Action when it exits
  `needs_human`, with its `--context` answer posted from the thread.

## Non-goals

- Byte compatibility with pizza-bot's wire, data root, or plugin packages.
  Pizza-bot is the reference for behaviour, not a compatibility target.
- Keeping LangGraph, DeepAgents or any `@langchain/*` dependency anywhere.
- Horizontal scaling of hub itself. Hub stays one SQLite-backed process;
  workers scale out.
- A new UI design. The first client is the existing React app with its
  transport layer replaced; a redesign is its own PRD.
- The Electron desktop app and the terminal REPL. The web client is the only
  client in v1. The Electron shell's job (supervising an embedded backend)
  does not exist in this topology, and a desktop wrapper over the web client
  can come later without changing the server.
- Running an agent outside a container. There is no in-process worker mode.
- Replacing hub's carry-patch, merge-queue and git-server features, which
  stay as they are and are not touched by this work.

## Tenancy model

Hub already has the nouns. The mapping:

| Hub today | Inbox meaning |
|---|---|
| User | The person. Owns a global inbox view across their workspaces. |
| Org (personal org auto-created at first login) | The tenant boundary. Org secrets hold shared provider credentials; org vars hold shared defaults such as the model tier. |
| Workspace (slug, git URL, branch, owner, org) | The unit of work and isolation: one clone, one inbox, one work queue, its own skills, MCP servers, secrets and variables. |
| API key | Interactive clients after OAuth. |
| PAT with scopes | Automation: webhooks, CI, other agents. New scopes `threads:read`, `threads:write`, `runs:write`, `approvals:write`, `triggers:manage`. |
| Workspace-scoped token | What a worker holds while running one workspace's job, so a run can only report into its own workspace. |
| Admin token | Operator. Cannot create workspaces today; that stays. |

Three gaps in hub for a *team* inbox, found in `docs/permissions.md`. They
are hub work, specified in hub's own `.specs/` and landed **before** this PRD
starts, because every inbox query below assumes the first of them:

1. **Workspaces are owner-private.** Core CRUD checks `ws.OwnerID == auth.UserID`
   and answers 404 otherwise, even to org members. A shared team inbox needs
   org-member read access to org workspaces, with the owner keeping write and
   delete, and the git server following the same rule. This is the hub
   permission change the PRD depends on.
2. **Re-login mass-revokes every API key** for the user. A browser login on a
   second device logs the CLI out. The web client needs either a per-device
   key (drop the mass revoke, cap keys per user) or a short-lived session
   cookie minted from the key at callback time.
3. **No browser-first OAuth flow.** `POST /api/v1/auth/callback` is designed for
   `afc login` (CLI opens a browser, exchanges the code). A SPA needs the
   redirect target to be hub (or the SPA origin on an allowlist) and the key
   handed over without appearing in a URL. Straightforward, but new.

## Workspace inbox and work queue

A workspace gets three new resources.

**Threads.** A conversation: id, workspace slug, title, `folder_id`, `pinned`,
`unread`, `awaiting_action`, `source` (`user | cron | webhook | pipeline |
fork`), `parent_thread_id`, timestamps, and a pointer to its session log.
Folders are per workspace. The global views are computed, never stored:

- **Unread** = threads with `unread` across the workspaces the user can read.
- **Action** = threads with an open approval the user may answer.
- **All**, filtered by workspace, folder, or source.

**The work queue.** The workspace's view of `jobqueue`: queued, running,
waiting-on-human and recently finished jobs, each linked to its thread. Job
types: `run` (one agent turn on a thread), `trigger_occurrence` (a cron or
webhook firing that creates a thread then a run), `pipeline` (an agent-fox
tool invocation; the envelope is the result and the events file is the
transcript). The queue exposes cancel and requeue, which jobqueue has.

**Approvals.** A durable row: id, thread, run, tool name, arguments, allowed
decisions, created, decided, decision, decided-by. The Action view is a query
over open approvals, so an approval survives the worker, the run and hub
restarting, and two people in an org can see the same pending decision; the
first decision wins and the second answers 409.

Pizza-bot's rule that folders never hide work from the global queues carries
over one level up: a workspace never hides work from the user's global inbox.
With org-member reads in hub, a member's global inbox spans every workspace
in their orgs plus their personal ones; **Action** shows an approval to every
member who may answer it, and the first decision wins.

## The run model

**One agent per run, one session log per thread.** Each run resumes the
thread's log with `NewAgentFromSession`, appends to it, and ends. A thread's
transcript is the log folded; a fork is `ForkFrom(entry)`, which the session
package already supports and which replaces pizza-bot's checkpoint
time-travel.

**Approvals are a stop, not a block.** AgentKit's `BeforeToolCall` is
synchronous: it can allow, rewrite, or refuse a call, but it cannot park a
goroutine across a process restart. So an approval-gated tool call is handled
as agent-fox handles `needs_human`:

1. The interceptor sees a tool whose skill policy requires approval, writes
   the approval row, and returns a terminating refusal that names it.
2. The run ends with stop reason `needs_human`; the thread flips to Action.
3. A decision arrives (`POST .../approvals/:id`). `approve` re-enqueues a run
   whose first action is executing the recorded call with the recorded
   arguments and feeding the result back as the tool result; `edit` does the
   same with the edited arguments; `reject` and `respond` feed a tool result
   or a user message saying so and let the model continue.

This keeps pizza-bot's idempotency invariant (the step is re-run, not resumed
mid-flight) and needs nothing from AgentKit it does not have. It is the single
largest piece of new runtime code in the PRD.

**Events.** The wire event is AgentKit's `core.Event` in its JSON form,
wrapped in `{seq, run_id, thread_id, ts, event}`. Subagent events carry the
parent tool-call id so an Activity rail can nest them. Pipeline jobs emit
agent-fox's PRD 03 event types (`run_start`, `phase_start`, `turn`,
`tool_call`, `run_end`) under the same envelope, so one decoder serves both.

**Budgets and limits.** Per-run turn and cost ceilings come from workspace
vars with org defaults (`MAX_TURNS`, `MAX_BUDGET_USD`), enforced by
`stop.AfterTurns` and `middleware.Budget`; hub's session usage endpoints give
the per-workspace cost view pizza-bot never had.

## The API surface (sketch)

Everything under `/api/v1`, authenticated as hub does today, documented in
hub's `openapi.yaml`.

```
GET    /inbox?view=unread|action|all&workspace=&folder=      global, per user
GET    /events?thread=|workspace=|since=<seq>                 SSE, one stream for everything

GET    /workspaces/:slug/threads
POST   /workspaces/:slug/threads                              create (optionally with first message)
GET    /workspaces/:slug/threads/:id                           metadata + folded transcript
PATCH  /workspaces/:slug/threads/:id                           title, folder, pinned, unread
DELETE /workspaces/:slug/threads/:id
POST   /workspaces/:slug/threads/:id/fork
POST   /workspaces/:slug/threads/:id/messages                  append + enqueue a run  → {run_id}
POST   /workspaces/:slug/threads/:id/runs/:run_id/cancel
GET    /workspaces/:slug/threads/:id/events?since=<seq>        replay + tail

GET    /workspaces/:slug/approvals?open=true
POST   /workspaces/:slug/approvals/:id                          {decision, edited_args?, message?}

GET    /workspaces/:slug/queue                                  jobs with thread links
POST   /workspaces/:slug/queue/:job_id/cancel | requeue

GET|POST|PATCH|DELETE /workspaces/:slug/triggers
POST   /workspaces/:slug/triggers/:id/run                       manual fire
POST   /hooks/:workspace/:trigger_id                            webhook, signed

GET|POST|DELETE /workspaces/:slug/folders
POST   /workspaces/:slug/attachments  · GET /attachments/:id

GET    /workspaces/:slug/skills · /mcp-servers · /models       capability catalogs, read-mostly
```

Provider configuration disappears as an API: a model is `vendor/model-or-tier`
in a variable, a credential is a secret, both at whichever scope the team
wants, resolved by the worker through AgentKit's credential resolution.

## Clients

**Web, and only web, in v1.** Keep `apps/web`'s components; replace
`api-client.ts`, `protocol-stream-store.ts` and `projection/*` with a
generated client and one event decoder. Add sign-in via redirect to hub,
workspace switching, the per-workspace inbox and queue, and the global inbox.
Browser notifications come off the same SSE stream filtered to terminal
events. Hub serves the built app, so one origin and no CORS configuration.

Later, not in scope: a desktop wrapper over the web client, and `afc inbox`,
`thread`, `run`, `approve` commands over the same API.

## What exists and what has to be built

| Area | hub | agentkit-go | agent-fox | New |
|---|---|---|---|---|
| OAuth, users, orgs, API keys, PATs, scopes | yes | | | browser flow; per-device keys; org-member workspace read |
| Workspaces, secrets, vars, git | yes | | | |
| Durable job queue with retry, per-key serialization, crash recovery | yes | | | cron tick; webhook route; occurrence rows |
| SSE fan-out with per-workspace filtering | yes (audit) | | | generalize beyond audit; `since` replay from a log |
| Agent loop, tools, guard, skills, MCP, subagents, budgets | | yes | | |
| Durable session log, resume, fork | | yes | | one log per thread, stored under the workspace |
| Approval / HITL | | `BeforeToolCall` only | `needs_human` + `--context` | the approval primitive and resume-with-decision |
| Long pipelines as jobs with envelopes and events | | | yes (PRDs 01–05) | job type wrapper; envelope → inbox card |
| Threads, folders, inbox flags, FTS | | | | all |
| Attachments by reference | | images normalized at history boundary | | storage + inlining at the model call |
| Model providers | | Anthropic, OpenAI (+compatible), OpenAI Responses, Google, Ollama | | **Bedrock** |
| Worker container | sandbox and agents images | | sandbox and tools images | `worker` image; job scheduler over podman; per-workspace state volume |
| Clients | | | | web transport layer, sign-in flow, workspace switching |

## Risks and gaps found while reading

- **Bedrock.** Pizza-bot's primary provider; AgentKit has Anthropic direct and
  on Vertex, not on Bedrock (SigV4 signing is a new wire). Decide whether
  Bedrock is a launch requirement.
- **MCP era.** AgentKit speaks MCP `2026-07-28` only and refuses the legacy
  `initialize` handshake. Pizza-bot's bundled plugins and most third-party
  servers may not have migrated. This is a stated AgentKit decision, so the
  cost lands here: either a compatibility shim in the worker or a smaller
  plugin catalog at launch.
- **Approvals are new runtime code** in the hottest path. Mitigation: the
  stop-and-resume design above reuses `needs_human` semantics agent-fox already
  ships and tests offline against `provider/faux`.
- **SQLite as the only hub store.** One hub process; all event fan-out goes
  through it. Fine for a team, not for a fleet. Event logs should live beside
  the session log on the worker's disk and be shipped, not stored in the hub
  DB row by row.
- **Cross-tenant sync/reclone.** `docs/permissions.md` records that the sync
  and reclone handlers do not enforce ownership. Must be fixed in the hub
  prerequisite work before any multi-user exposure.
- **Container start cost per job.** A clone from hub's git server on every
  job is seconds for a normal repository and minutes for a large one. If it
  shows up, the per-workspace volume can keep a warm clone that the container
  fetches into, at the cost of cleaning it between jobs.

## Decisions

Taken:

| # | Question | Decision |
|---|---|---|
| 1 | Inbox inside the hub binary, or a separate service? | Inside hub. It needs the job queue, the SSE manager, the secrets store and apikit auth, and all four are in-process APIs there. |
| 2 | Where agents run | In workers, one container per job, on the sandbox image lineage ("The sandbox semantic"). No in-process mode. |
| 3 | Client | Keep the React app; replace its transport layer. |
| 4 | Team inbox | In scope, and the hub ownership change (org-member workspace reads) lands first as hub work. |
| 5 | Desktop | Not in v1. The web client is the only client. |

Still recommendations:

| # | Question | Recommendation |
|---|---|---|
| 6 | Tenant boundary: org or workspace? | Org is the tenant; workspace is the isolation unit. Secrets and models default at org, override at workspace. |
| 7 | Wire for events | AgentKit's `core.Event` JSON with a `{seq, run_id, thread_id}` envelope; agent-fox's PRD 03 events under the same envelope for pipeline jobs. One decoder in the client. |
| 8 | HITL mechanism | Stop-and-resume (approval row + `needs_human` + re-enqueue). Do not try to park a goroutine, and never keep a container alive waiting for a person. |
| 9 | Memory | Drop the cross-thread "store" for v1; per-workspace context files under AgentKit's trust gate are the durable memory. Revisit if users miss it. |
| 10 | Bedrock | Not in v1 unless it is the deployment target; `openai`-compatible gateways cover OpenRouter and Requesty today. |
| 11 | Where the session log lives | On the per-workspace state volume, under an `$XDG_STATE_HOME`-style layout as agent-fox PRD 05 does, shipped to hub as events. Hub stores metadata and the inbox flags, not transcripts. |
| 12 | Container scheduling | Start with one worker host and `podman`; pods on the existing hub Kubernetes deployment later. |

## Open questions for the author

- Should pizza-bot's skill format (`SKILL.md` with `interruptOn` and
  `mcp:server:tool` refs) be readable as-is by the worker, so existing skills
  port without editing, or do skills move to AgentKit's manifest format with
  an approval field added?
- Is the webhook trigger a hub-level inbound hook (one URL per trigger, signed)
  or is it the existing forge push hook extended? They have different callers.

## Suggested phasing

0. **Hub prerequisites**, as hub specs: org-member read access to org
   workspaces (REST and git server); the sync and reclone ownership fix;
   per-device API keys or a session cookie so a browser login does not
   revoke the CLI; a browser-first OAuth callback.
1. **Contract first.** OpenAPI for the inbox routes and the event envelope
   schema. Conformance tests against `httptest` servers, as hub does today.
2. **Threads and runs.** Thread records in hub; the `worker` image on the
   sandbox lineage; a podman scheduler that runs one container per job,
   clones from hub's git server, runs one AgentKit agent and streams events.
   No approvals, no triggers. The web client's transport layer replaced; a
   thread round-trips in the browser.
3. **Inbox views and approvals.** Unread and Action per workspace and
   globally; the stop-and-resume approval primitive; the Activity rail from
   subagent events.
4. **Triggers and pipelines.** Cron tick and webhook route; agent-fox tools
   as `pipeline` jobs with envelopes rendered as inbox cards. Attachments,
   FTS and folders land wherever they fit in 2 to 4.
