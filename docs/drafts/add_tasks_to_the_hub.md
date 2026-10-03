# Add tasks to the hub

**Status:** workshop draft, first pass. This restarts
[rebuild_the_inbox_on_hub_and_agentkit.md](rebuild_the_inbox_on_hub_and_agentkit.md)
from first principles. That draft's findings about pizza-bot still hold and
are not repeated here.

## Baseline

Three things are taken as given, because they are hub work specified
elsewhere and this PRD builds on them rather than defining them:

1. **Hub has a sandbox concept.** An agent runs in an isolated environment,
   a container or a microVM, that hub creates, hands a workspace clone, and
   tears down. Agents in different sandboxes cannot interfere with each
   other. The image lineage is the existing `quay.io/agentfox/sandbox`, with
   the repository at `$WORKSPACE` and agent state in `$HOME`.
2. **A workspace is cloned into a sandbox.** The sandbox clones from hub's
   own git server, works on a branch, and pushes back. Nothing runs against a
   shared checkout.
3. **Workspaces are multi-tenant.** A workspace belongs to an org; org
   members can read it and, with the right scope, act on it. Credentials are
   hub's existing API keys, PATs with scopes, and admin tokens; secrets and
   variables resolve workspace over org over user.

## Intent

Hub owns workspaces, identity, secrets, a git server, a merge queue and a job
queue, but it has no notion of *work an agent should do*. Today that work is
whatever a person types into a shell that happens to have the agent-fox tools
installed. The result lands on stdout and in a state directory on that
machine, and nobody else can see it, schedule it, or come back to it later.

This PRD adds **tasks** to the hub: structured units of agent work that live
in a workspace, are queued and executed in sandboxes in the background, have
a schema, a lifecycle and an event stream, can be scheduled, and are created,
managed and observed through one API that a web client or a desktop app
drives. The inbox that pizza-bot built around one runtime becomes a view over
that queue.

The first principle behind the design: **hub stores and observes; it does not
reason.** A task's execution is a program in a sandbox. For the built-in task
types that program is one of the agent-fox tools, which already produce
exactly one JSON envelope, a JSONL event stream, a schema for both, a
`needs_human` shape and a `next` suggestion. Hub needs no model client, no
agent loop and no prompt to run them. That keeps the engine replaceable: a
task type is a program plus a schema, and a different agent behind the same
schema is a different template, not a different hub.

## Goals

- A member of a workspace can create a task of a known type, submit it, walk
  away, and find its result in the hub later, from any client.
- Every task has one schema, declared by its template, for its input and its
  result. A client can render a creation form and a result card from the
  schema alone.
- Tasks run in the background, one sandbox per attempt, and survive the
  client, the sandbox host and hub restarting. The task row and its event
  log are the truth, not a process.
- A task that needs a person (a question, a failed check, an approval) stops
  cleanly, says so, and resumes from the answer without re-doing work it
  already did.
- Tasks can be created on a schedule, and one task can be made to follow
  another, so that the simple workflows that already exist as `next`
  suggestions (`spec` then `impl`, `triage` then `fix`) can be expressed
  without a separate workflow engine.
- The task API is the only interface. The CLI, the web client and a desktop
  app are all clients of it.

## Non-goals

- A workflow engine. v1 ships the primitives a workflow needs (`depends_on`,
  template `next` rules, result-to-input mapping) and executes only linear
  chains. Branching, fan-out and conditions are a later PRD on the same
  schema.
- Interactive chat with an agent. A task is submitted and runs to a stop;
  steering a running agent is not a task operation. A conversation that
  wants that is a different product surface and may come later as a task
  type whose program is a chat agent.
- A model client in hub. Hub never calls a model.
- Running a task outside a sandbox.
- Defining the sandbox API, the workspace multi-tenancy rules or the
  browser OAuth flow. They are the baseline.

## Concepts

| Noun | Meaning |
|---|---|
| **Task** | One unit of agent work in a workspace: a template, an input, a lifecycle state, zero or more attempts, and at most one result. |
| **Template** | A task type: the schema of the input and the result, the program that runs it, the sandbox profile, the policy, and the rules for what may follow. Built in, org-defined or workspace-defined. |
| **Attempt** | One execution of a task in one sandbox. A task has a new attempt on requeue and on resume after `needs_human`. The attempt is what the sandbox, the event log and the cost belong to. |
| **Event** | One line of progress from an attempt, sequence-numbered per task, stored, and streamed over SSE. |
| **Schedule** | A rule that creates tasks from a template and a bound input, on a cron expression or once at a time. |
| **Inbox** | A per-user view over tasks: what needs me, what finished since I looked, everything. Not a stored object. |

A **run** of an agent-fox tool is one attempt. A **job** in hub's `jobqueue`
is the scheduling record behind one attempt; it is not exposed.

## The task lifecycle

```
            submit            claim                  stop
  draft ───────────▶ queued ───────────▶ running ──────────┬──▶ done
    │                  │  ▲                 │              ├──▶ failed
    │ discard          │  │ answer/requeue  │              └──▶ blocked
    ▼                  │  └─────────────────┼──────────────────────┘
  (deleted)            │                    │ cancel
                       └────────────────────┴───────────▶ cancelled

  done | failed | cancelled ──archive──▶ archived
```

| State | Meaning | Who moves it there |
|---|---|---|
| `draft` | Being written. Input may be invalid. Not visible to the queue. | creator |
| `queued` | Valid against the template, accepted, waiting for a sandbox or for `depends_on`. | submit, requeue, answer, schedule |
| `running` | An attempt holds a sandbox. | the worker claiming the job |
| `blocked` | Stopped on purpose; a person must act. `blocked_reason` says why: `needs_human` (a question), `unverified` (work exists, checks fail), `approval` (a policy gate). | the attempt's envelope |
| `done` | Terminal success. `result` is set. | the attempt's envelope |
| `failed` | Terminal failure after the retry policy is exhausted. `error` is set. | the attempt's envelope, or the queue |
| `cancelled` | Stopped by a person. Sandbox torn down. | a member |
| `archived` | Hidden from default views. Only from a terminal state. | a member, or retention |

Rules:

- A task is **never edited after submit**. A different input is a new task,
  possibly with `parent_task_id` pointing back. This is what makes a task's
  events and result attributable.
- `blocked` is a state, not a flag, because it is the one state where the
  queue must *not* claim the task and the inbox must show it. Its three
  reasons share one answer path (below).
- `unverified` is `blocked`, not `done` with a warning, because agent-fox's
  exit 4 means "a person has to look" and the inbox has to put it where a
   person looks.
- Retries are the queue's business: a `failed` attempt with a retryable
  error is re-enqueued under the template's retry policy without the task
  leaving `queued`/`running`. Only the exhausted case reaches `failed`.
- `unread` is **per user**, not a state: a task has a `read_at` per member,
  set when they open it. Pizza-bot's Unread is "terminal since my `read_at`".

## Answering a blocked task

One operation, `POST /tasks/:id/answer`, with a body that matches the reason:

| `blocked_reason` | Body | What happens |
|---|---|---|
| `needs_human` | `{answer: string}` or `{option: "A"}` | A new attempt runs the same program on the same input with the answer appended as `--context`, exactly as the envelope's `needs_human.resume` says. |
| `unverified` | `{decision: "accept" \| "retry" \| "discard", context?}` | `accept` moves to `done` with the result as is; `retry` runs a new attempt with the context; `discard` moves to `cancelled`. |
| `approval` | `{decision: "approve" \| "reject", context?}` | `approve` runs a new attempt with the gate cleared for the recorded action; `reject` moves to `cancelled` or `done`, per the template. |

The answer is recorded on the task (`answers[]`, with who and when), so a
second person sees the first decision and a second answer gets 409. This is
pizza-bot's `approve | edit | reject | respond` folded onto agent-fox's
`needs_human` mechanism: the agent never waits inside the sandbox for a
person; it stops, the sandbox is torn down, and the answer starts a fresh
attempt.

## Schema

### Task

```jsonc
{
  "id": "tsk_…",
  "workspace": "widgets",
  "template": "fix",                 // template id; "freeform" for a prompt
  "title": "Fix nil-map panic in loop.go",
  "input": { /* validated against template.input_schema */ },
  "state": "blocked",
  "blocked_reason": "needs_human",   // only in blocked
  "priority": 0,                     // higher first within a workspace
  "labels": ["bug"],
  "created_by": "usr_…",
  "created_at": "…", "submitted_at": "…", "finished_at": null,
  "source": { "kind": "user" | "schedule" | "task" | "api", "id": "…" },
  "parent_task_id": null,
  "depends_on": ["tsk_…"],           // all must be done before claim
  "schedule_id": null,
  "attempt_count": 2,
  "current_attempt": "att_…",
  "needs_human": { /* the envelope's object, when blocked on it */ },
  "answers": [ { "by": "usr_…", "at": "…", "body": { … } } ],
  "result": { /* validated against template.result_schema; done only */ },
  "error": { "category": "…", "message": "…", "retryable": false },
  "artifacts": [ /* the envelope's artifacts: branch, commit, pull_request, report */ ],
  "side_effects": [ /* the envelope's side effects */ ],
  "next": [ /* the envelope's suggestions, resolved to templates when possible */ ],
  "usage": { "cost_usd": 1.84, "input_tokens": …, "output_tokens": … },
  "read_at": "…"                     // for the calling user; null = unread
}
```

`input`, `result`, `needs_human`, `artifacts`, `side_effects` and `next` are
taken from the agent-fox envelope without translation for the built-in
templates. The envelope's `schema_version` is recorded on the attempt, so a
result's shape is always known.

### Template

```jsonc
{
  "id": "fix",                       // built-in: fix | impl | spec | triage | freeform
  "scope": "builtin" | "org" | "workspace",
  "name": "Fix a problem",
  "description": "…",
  "input_schema":  { /* JSON Schema */ },
  "result_schema": { /* JSON Schema */ },
  "program": {
    "kind": "agentfox",              // v1: agentfox | command
    "tool": "fix",
    "args": ["{{input.problem}}", "--land", "{{policy.land}}", "--effort", "{{input.effort}}"],
    "env": { "GITHUB_TOKEN": "secret:GITHUB_TOKEN" }
  },
  "sandbox": { "image": "quay.io/agentfox/tools:latest", "cpu": 2, "memory_mb": 4096,
               "timeout_s": 7200, "network": "vendor+forge" },
  "policy": {
    "land": "pr" | "branch" | "none",
    "forge_write": true,
    "max_budget_usd": 5.0, "max_turns": 40,
    "approval": [ "open_pr" ]        // actions that stop the task in blocked:approval
  },
  "retry": { "max": 2, "retryable_categories": ["network", "rate_limit"] },
  "next": [ { "template": "impl", "when": "result.status == 'done'",
              "input": { "spec": "{{result.spec_dir}}" }, "auto": false } ],
  "created_by": "usr_…", "created_at": "…"
}
```

- Built-in templates are generated from the tools' own `--schema` output at
  build time, so the schema a client renders is the schema the program
  validates. They cannot be edited; an org **derives** one by copying and
  changing `policy`, `sandbox`, defaults in `input_schema`, or `next`.
- `program.kind: command` runs any program on the sandbox image with the
  same contract: input as a JSON file, result as a JSON file, events as
  JSONL. This is how a team adds a task type that is not an agent-fox tool
  without touching hub.
- `freeform` is a built-in template whose input is `{prompt, effort,
  trust_project}` and whose program is a general coding agent on the
  `agents` image that honours the same contract. Its `result_schema` is
  the minimum (`summary`, `artifacts`). Which agent runs it is a decision
  below.
- `{{…}}` is the one piece of logic hub has: a template substitutes values
  from `input`, `policy`, `workspace` and, in `next`, `result`. No
  expressions beyond lookups and the `when` comparisons the chain rule
  needs.

### Attempt

```jsonc
{
  "id": "att_…", "task_id": "tsk_…", "number": 2,
  "sandbox_id": "sbx_…",
  "state": "running" | "stopped",
  "stop": { "status": "done" | "failed" | "needs_human" | "unverified" | "usage",
            "exit_code": 3, "category": "…" },
  "schema_version": "3.0.0",
  "started_at": "…", "finished_at": null,
  "usage": { … },
  "report_file": "…", "events_file": "…"   // artifact references in hub's blob store
}
```

### Schedule

```jsonc
{
  "id": "sch_…", "workspace": "widgets",
  "template": "triage", "input": { "problem": "{{schedule.trigger.body}}" },
  "cron": "0 7 * * 1-5", "timezone": "Europe/Berlin",   // or "run_at": "…"
  "enabled": true,
  "overlap": "skip" | "queue",
  "missed": "run_once" | "skip",
  "last_fired_at": "…", "last_task_id": "tsk_…"
}
```

A **webhook** is a schedule with no `cron` and a signed URL; its firing body
is available to the input mapping. It is the same object because the only
thing both do is create a task from a bound input.

## Execution

1. **Submit.** Hub validates `input` against the template, stores the task
   as `queued`, and enqueues a `task.attempt` job keyed by the task id with
   `group_key` = workspace. A task with unmet `depends_on` is stored
   `queued` but not enqueued; the completion of the last dependency
   enqueues it.
2. **Claim.** A worker takes the job, asks hub for a sandbox from the
   template's profile with the workspace cloned, and records the attempt
   with the sandbox id. Per-workspace concurrency is a workspace variable
   (`TASK_CONCURRENCY`, default 2); the queue honours it by `group_key`.
3. **Run.** Inside the sandbox a small runner (`af-runner`, shipped on the
   image) receives the task, resolves the program from the template, writes
   the input, and starts the program with `--emit-events`, `--output` and a
   task-scoped hub token. It tails the events, posts them to hub in batches
   with the task's next sequence number, and posts the envelope when the
   program exits. The runner is the only process that talks to hub; the
   agent never sees the hub token's scope beyond "this task".
4. **Stop.** Hub maps the envelope's `status` onto the task state (`done`,
   `failed`, `blocked:needs_human`, `blocked:unverified`; `usage` is a
   template bug and lands in `failed` with `category: usage`). The sandbox is
   torn down; its `$HOME` state that must survive (the report and events
   files, the agent's session log) is uploaded as attempt artifacts first.
5. **After.** Cost is recorded on the attempt and summed on the task; hub's
   existing session usage endpoints serve the per-workspace cost view. If
   the template has `next` rules with `auto: true` whose `when` holds, hub
   creates the follow-up task as `queued` with `source.kind: task` and
   `parent_task_id` set. Rules with `auto: false` surface in the result card
   as one-click creation.

Cancellation stops the sandbox; the attempt stops with `category:
cancelled`. Hub restarting re-claims jobs the way `jobqueue` already does; a
worker restarting finds attempts whose sandbox is gone and fails them as
retryable.

## The API

All under `/api/v1`, hub authentication, in `openapi.yaml`.

```
GET    /task-templates                               built-in + org + workspace, merged
POST   /orgs/:slug/task-templates                    derive or define; same at /workspaces/:slug/task-templates
GET|PATCH|DELETE /orgs/:slug/task-templates/:id

POST   /workspaces/:slug/tasks                       {template, title?, input, state: draft|queued, priority?, labels?, depends_on?}
GET    /workspaces/:slug/tasks?state=&template=&label=&since=&cursor=
GET    /workspaces/:slug/tasks/:id
PATCH  /workspaces/:slug/tasks/:id                   draft only: input, title, labels, priority
DELETE /workspaces/:slug/tasks/:id                   draft only
POST   /workspaces/:slug/tasks/:id/submit
POST   /workspaces/:slug/tasks/:id/cancel
POST   /workspaces/:slug/tasks/:id/requeue           from failed | cancelled; new attempt
POST   /workspaces/:slug/tasks/:id/answer            from blocked; body per reason
POST   /workspaces/:slug/tasks/:id/archive
POST   /workspaces/:slug/tasks/:id/read              sets read_at for the caller
GET    /workspaces/:slug/tasks/:id/attempts
GET    /workspaces/:slug/tasks/:id/events?since=<seq> replay then tail (SSE)
GET    /workspaces/:slug/tasks/:id/artifacts/:name   report, events, session log, diffs
POST   /workspaces/:slug/tasks/:id/next              {index} creates the suggested follow-up

GET    /inbox?view=attention|unread|all&workspace=&cursor=
GET    /events?workspace=&task=                      the existing SSE stream, task events added

POST   /workspaces/:slug/schedules · GET · GET|PATCH|DELETE /:id · POST /:id/fire
POST   /hooks/:schedule_id                           signed webhook firing

POST   /tasks/:id/events                             runner only, task-scoped token, batch with seq
POST   /tasks/:id/stop                               runner only: the envelope
```

Scopes: `tasks:read`, `tasks:write` (create, submit, cancel, requeue,
archive), `tasks:answer` (separate, because answering is the one action that
spends money and takes a decision on someone else's task), `templates:manage`,
`schedules:manage`. API keys have them all on workspaces they can see.
The runner's **task-scoped token** is a new credential: it may post events
and the stop envelope for one task id and read nothing else. It is minted at
claim and expires with the attempt.

## Inbox views

Computed over tasks the caller can read, never stored:

- **Attention**: `blocked`, any reason. Sorted by `submitted_at`. This is
  pizza-bot's Action.
- **Unread**: `done | failed` with `finished_at > read_at` (or no `read_at`).
  Pizza-bot's Unread.
- **Running**: `queued | running`, with the latest event's `stage` as a
  one-line status.
- **All**: everything not `archived`, filterable by workspace, template,
  label, state, creator.

The global inbox spans every workspace the user can read; the workspace
inbox is the same query with one filter. A task never disappears from the
global views because of which workspace it is in.

## Simple workflows, foreseen

The schema carries three things so that a workflow is a set of tasks, not a
new object:

- `depends_on` on a task: gating in the queue.
- `next` on a template: a rule for what to create when a task stops, with
  an input mapping from `result`, and `auto` to decide whether a person
  clicks or hub does.
- `parent_task_id` and `source.kind: task`: lineage, so a chain renders as
  one thread in the inbox.

v1 executes linear chains only: `spec` then `impl`, `triage` then `fix`,
`fix` then a `freeform` follow-up. A failed or blocked link stops the chain;
answering the link resumes it. A later PRD adds a **workflow template**
(a list of task templates with edges) and fan-out, on the same three fields.

## Observability

- Events are agent-fox's PRD 03 types (`run_start`, `step`, `phase_start`,
  `turn`, `tool_call`, `text`, `run_end`, heartbeat) under a hub envelope
  `{seq, task_id, attempt_id, ts, event}`. A `command` program may emit the
  same types or just `step`.
- Every attempt's report file and events file are artifacts. The agent's
  session log is an artifact when the program produces one.
- hub's audit store records task state transitions with the actor, and
  the runner's event batches, so "who answered this and when" is queryable
  without the task row.
- Usage per attempt feeds hub's existing session usage endpoints and the
  per-workspace cost summary.

## What exists and what has to be built

| Area | Exists | New |
|---|---|---|
| Durable queue with retry, per-key serialization, crash recovery, `available_at` | hub `jobqueue` | `group_key` concurrency limit; a cron tick that enqueues schedule firings |
| SSE fan-out with per-workspace filtering | hub audit `SSEManager` | task channel; `since`-replay from the stored event log |
| Envelope, events, schemas, `needs_human`, `next`, `--context`, `--output` | agent-fox tools (PRDs 01–05) | nothing in the tools; built-in templates generated from `--schema` |
| Sandbox image lineage | `sandbox`, `agents`, `tools` images | `af-runner` on the images; the sandbox API is baseline |
| Identity, scopes, secrets, vars | hub | `tasks:*`, `templates:manage`, `schedules:manage`; the task-scoped token |
| Session usage and cost | hub sessions endpoints | attempts report into them |
| Tables | | `task_templates`, `tasks`, `task_attempts`, `task_events`, `task_reads`, `task_answers`, `schedules`, `schedule_firings` |
| Freeform program | `agents` image (pi, opencode, claude) | the contract adapter for the chosen agent, or a freeform agent-fox tool |
| Clients | `afc` | `afc task` and `afc inbox`; the web client; later a desktop app |

## Decisions to make

| # | Question | Recommendation |
|---|---|---|
| 1 | Task and attempt as separate records? | Yes. Requeue, retry and answer each start a new attempt, and cost, sandbox and events belong to the attempt. A task with one flat `run` field cannot represent its own history. |
| 2 | Who talks to hub from inside the sandbox? | A runner shipped on the image, holding a task-scoped token. Hub never execs into a sandbox, and the agent never holds a credential wider than its task. |
| 3 | `unverified`: `done` with a flag, or `blocked`? | `blocked`. The inbox has to show it where decisions are made, and the answer path (`accept`, `retry`, `discard`) is a decision. |
| 4 | What runs `freeform`? | A freeform agent-fox tool built on AgentKit that honours the envelope contract, so freeform tasks are as observable as the others. Fallback: the `agents` image with an adapter around `pi`, which gives a transcript but no envelope. Name and scope of that tool are a separate agent-fox PRD. |
| 5 | Where do templates live: API only, or also files in the repository? | API only in v1. Repository-authored templates (`.af/tasks/*.toml`) are attractive but mean a repository authors a program hub runs; that needs the trust gate agent-fox already has for `AGENTS.md`, and is a later addition. |
| 6 | Automatic `next` in v1? | Yes, but off by default (`auto: false`) on every built-in template. Chains that spend money without a click are an org's choice when deriving a template. |
| 7 | Schedule overlap default | `skip`: a scheduled task whose previous firing is still `queued` or `running` does not fire again. `queue` is opt-in. |
| 8 | Per-workspace concurrency | A workspace variable, default 2. Sandboxes isolate work; the limit exists for cost and for the forge, not for correctness. |
| 9 | Landing | The template policy's `land` maps to the tools' `--land`. Routing a task's branch through hub's merge queue instead of a forge PR is a `land: merge_queue` value added later; nothing in the schema prevents it. |
| 10 | Approval gates | Only `open_pr` and `push` in v1, implemented by running the program with `--land none`, stopping `blocked:approval` on a `done` envelope with a branch artifact, and on `approve` running a second attempt that lands the existing branch. No in-agent interception. |

## Open questions

- Should a `draft` be shareable, so that a person prepares a task and another
  submits it, or is a draft private to its creator?
- Is `priority` a number members set freely, or a small enum (`low`,
  `normal`, `high`) so that one workspace cannot starve another in the same
  org? The queue orders by `available_at` today; priority is new to it.
- Does archiving keep artifacts, or does retention delete the events and
  report after a period? hub's audit retention exists and could own this.
- Is the desktop app in this PRD's horizon at all, or is "web client or
  desktop app" a statement about the API being client-neutral?

## Phasing

1. **Schema and API.** Tables, OpenAPI, templates generated from `--schema`,
   the state machine with conformance tests, no execution: a task can be
   created, submitted, and moved by hand through an admin endpoint.
2. **Execution.** `af-runner`, the task-scoped token, the claim loop over
   `jobqueue` against the sandbox API, events and the stop envelope. The
   four built-in templates run end to end. `afc task` drives it.
3. **Inbox and answering.** Views, `read_at`, `answer` for all three
   reasons, the web client over the API.
4. **Schedules and chains.** Cron tick, webhook firing, `depends_on` gating,
   `next` with `auto`.
5. **Freeform and derived templates.** The freeform program, org and
   workspace templates, `command` programs.
