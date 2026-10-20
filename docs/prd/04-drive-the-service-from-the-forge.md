# Drive the service from the forge

Status: **proposed** (2026-10-20). Nothing here is implemented. This is
step 4 of [PRD 01](01-triage-and-fix-forge-issues-as-a-service.md)'s
order of work (§10): the label protocol whole, hub's event stream as the
trigger, the automatic follow-ups, and answering from the forge side. It
builds on [PRD 03](03-run-the-agent-fox-service.md)'s service and on hub
PRD 02's webhook ingress and `hub.forge.*` events. Written to PRD 01's
decisions D5 and D11.

## Intent

PRD 03's service is driven by its API and by a poll. A team does not
drive a service; it labels an issue. This PRD makes the label protocol
of PRD 01 §3 complete and immediate: the service creates its labels when
it subscribes, learns of a label within seconds from hub's event stream
rather than minutes from a poll, follows a triage with a fix when the
workspace asks for it, and lets a person answer a stopped job, or retry a
failed one, by writing a comment and putting the request label back,
without ever opening `af`.

## Goals

1. Subscribing a workspace creates the seven labels on its forge through
   hub, with their descriptions, and leaves existing ones as they are.
2. A request label added on the forge starts a job within the stream's
   latency, and a poll catches what the stream missed; the two never
   create two jobs for one request.
3. `AF_AUTO_FIX=true` turns a triage that ends `af:ready` into a fix job
   with no person involved, and the chain is visible on the job records
   and the issue.
4. A person answers a `needs_human` stop from the forge: a comment, then
   the request label again; the service takes the comments since its
   question as the answer and runs the next attempt. A person retries an
   `af:failed` job the same way.
5. Every label the service sets mirrors the job record exactly, one
   status label at a time, and a label a person moves by hand is a
   request or nothing, never state the service undoes.

## Non-goals

- **Webhooks to the service.** Hub receives them (D11); the service has
  no public endpoint for the forge.
- **Labels as the only record.** The job API stays the truth.
- **Reconciling labels a person removed.** Removing `af:in-progress` by
  hand changes nothing; the running attempt runs on.
- **Rebuild events.** PRD 05 consumes those on the same stream.

## 1. Labels on subscribe

`PUT /workspaces/:slug` (PRD 03) gains the step: for each label of PRD 01
§3 under the prefix, `POST /workspaces/:slug/labels {name, color,
description}` through hub's forge API; a 200 (exists) is fine, a 201
creates. A forge that refuses label creation fails the subscribe with the
forge's answer (`forge_forbidden`), as PRD 01 §3 requires. Changing the
prefix (`AF_LABEL_PREFIX`) later does not rename labels; a resubscribe
creates the new set.

| Label | Colour | Description |
|---|---|---|
| `af:triage` | `#0e8a16` | Ask agent-fox to triage this issue |
| `af:fix` | `#0e8a16` | Ask agent-fox to fix this issue |
| `af:in-progress` | `#fbca04` | agent-fox is working on this issue |
| `af:ready` | `#1d76db` | Triaged: the diagnosis is in the body |
| `af:needs-human` | `#d93f0b` | agent-fox stopped with a question; see the last comment |
| `af:landed` | `#0e8a16` | A branch and a pull request (or a carry patch) exist |
| `af:failed` | `#b60205` | agent-fox could not finish; see the last comment |

## 2. The event stream

For each subscription the service keeps one SSE connection to
`GET /api/v1/events?workspace=<slug>` on hub, with the workspace PAT
(which carries `audit:read`). It acts on:

| Event | Action |
|---|---|
| `hub.forge.issue.labeled` with `label` = a request label | a job (`source.kind: event`, `source.by: <hub event id>`), the label removed through hub |
| `hub.forge.issue.labeled` with a request label on an issue that has an open job | nothing; the label is removed (a second request while one runs is absorbed) |
| `hub.forge.issue.closed` with an open job | the job is cancelled with `category: issue_closed`; the box released; no label set |
| anything else | ignored |

- **Dedup with the poll**: a job's input is the issue; "an open job for
  this issue" is the one check both paths make, under the service's
  per-workspace lock, so a label the stream and the next poll both see
  makes one job. Hub event ids already acted on are kept in `hub_events`
  for 7 days.
- **Reconnect** with backoff from 1 s to 60 s; a reconnect does not
  replay (hub's stream has no `since`), so the poll stays on at
  `AF_POLL_INTERVAL` as the catch-up, and a workspace may set the interval
  longer when the stream is reliable. A stream down for longer than the
  interval is logged once.
- **Heartbeats** keep the connection; 60 s without one reconnects.

## 3. Automatic fix

With `AF_AUTO_FIX=true`, a triage job ending `af:ready` makes the service
add `af:fix` through hub, which is a request like any other: the next
event (or poll) creates the fix job with `source.kind: triage` and
`parent_job_id` the triage job. The chain shows on both job records and
on the issue's comment (`fix_summary` names the triage). Off by default:
a chain that spends money without a click is a workspace's choice.

## 4. Answering and retrying from the forge

- **Answering.** A job in `blocked` has posted one comment with its
  question (PRD 01 §4). A person replies in a comment and adds the
  request label (`af:triage` or `af:fix`) again. The service sees the
  label on an issue whose open job is `blocked`, takes every comment
  since its question comment (by timestamp, excluding its own), and
  starts the next attempt with them as `--context`, exactly as
  `POST /jobs/:id/answer` would with that text. With no new comment the
  label is removed and the question comment gets one line: "no answer
  found since the question; comment, then add the label again".
- **Retrying.** A job in `failed` with the request label added again
  starts a new attempt (`POST /jobs/:id/retry`'s effect), with the
  comments since the failure comment as `--context` when there are any.
- **A triaged issue edited and re-labelled** `af:triage` is a new triage
  job; the diagnosis is replaced and the original report kept (PRD 01
  D6).

## 5. The labels mirror the record

One function sets the status: it computes the status label from the
job's state (`running` → `af:in-progress`; `done` triage → `af:ready` or
`af:needs-human`; `done` fix → `af:landed`; `blocked` →
`af:needs-human`; `failed` → `af:failed`; `cancelled` → the request label
restored, no status label), and in one hub call sequence removes the
other status labels and adds this one. A failed label write is a warning
on the job (`label_not_set`) and a retry on the next transition; the
record is right either way.

## 6. Acceptance criteria

Offline, against the `httptest` hub of PRD 03 with a scriptable event
stream:

1. `PUT /workspaces/widgets` creates seven labels through hub (the
   requests observed, with the descriptions); a second `PUT` creates
   none; a forge answering 403 to label creation fails the subscribe
   with `forge_forbidden`.
2. A `hub.forge.issue.labeled {label: af:fix}` event on the stream
   creates one fix job within one second and removes the label through
   hub; the same event id again, and a poll finding the same issue still
   labelled (the fake forge lags), create no second job.
3. With `AF_AUTO_FIX=true`, a triage ending `af:ready` adds `af:fix`, and
   the resulting fix job has `source.kind: triage` and `parent_job_id`;
   with it unset, nothing is added.
4. A blocked job, a comment after its question, and `af:fix` again: a
   second attempt runs with the comment in its brief's context; with no
   comment, the label is removed and the question comment gains the
   one-line reply.
5. `af:fix` on a failed job starts a new attempt; `af:triage` on a
   triaged issue starts a new triage that replaces the diagnosis and
   keeps the original report.
6. Every transition leaves exactly one status label on the issue, and a
   cancelled job leaves the request label and no status label; a forge
   write failing on a transition leaves the job record correct with
   `label_not_set` and the next transition sets the right label.
7. The stream dropped and reconnected: a label added during the gap is
   found by the next poll and runs once.

## 7. What is given up, stated plainly

- **Hub's stream has no replay**, so the poll stays. A stream-only
  service would lose a label during a reconnect.
- **The answer is "every comment since the question"**, which may
  include chatter. The brief fences it as caller context, which the
  brain already treats as material, not instruction.
- **Seven labels on every subscribed repository**, in the service's
  colours. A team that wants its own names changes the prefix, not the
  names.

## Documentation

| Change | Update |
|---|---|
| the labels, the stream, answering and retrying from the forge | `docs/architecture.md` (the label protocol section), `README.md` (one paragraph: how a team uses it) |
| `AF_AUTO_FIX`, `AF_POLL_INTERVAL`'s new role | `docs/configuration.md` |
| the subscribe step and `label_not_set` | `docs/api.md` |
