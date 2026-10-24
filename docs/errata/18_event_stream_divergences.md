# Erratum 18: hub's event stream as the immediate trigger

Where the implementation of `18_agent_fox_event_stream` differs from what the spec's text assumes, and why.

## 1. Hub's events are its audit events, in their own shape (D-1)

The PRD's *Verified External API* marks the event stream unverified and assumes frames whose `data` is
`{"id", "event_type", "workspace_slug", "timestamp", "payload"}`, with the issue in `payload.issue` and the label in
`payload.label`. Hub's code (`../hub/internal/audit`) serves something else, and the client follows hub:

- A frame is `event: audit_event`, and its `data` is hub's `HubEvent`: `id`, `event_type`, the actor's fields,
  `resource_type`, `resource_id`, `action`, `workspace`, `metadata` and `timestamp`. A forge event has `resource_type`
  `"issue"`, `resource_id` the issue number, and `metadata` with `number`, `label` (the label's name, for
  `hub.forge.issue.labeled`), `labels` and `url` (`audit/types.go`, `EventForgeIssueLabeled`, `EventForgeIssueClosed`).
- A heartbeat is a frame `event: heartbeat` with `data` `{"timestamp": …}`, every 30 seconds (`audit/sse.go`,
  `HeartbeatInterval`). It is activity, and no event.
- The event types are the PRD's: `hub.forge.issue.labeled` and `hub.forge.issue.closed`.

`decodeEvent` (`internal/hub/events.go`) reads hub's shape first and the PRD's as well: `workspace` or `workspace_slug`;
the issue from `metadata` or `payload` (`issue`, `issue.number` or `number`), else from `resource_id` when
`resource_type` is `"issue"`; the label from `label` or `label.name`; the frame's `id:` and `event:` lines when the JSON
lacks an id or a type. The fake hub sends hub's own frames (`internal/hub/fakehub/events.go`, `eventFrame`), and
TS-18-9 decodes every shape. A 403 names the scope `audit:read`, which hub's audit routes require
(`audit/auth.go`, `requireAuditRead`).

## 2. A newer record is ordered by its id as well as its time (D-8)

The spec says that a cycle makes no job for, and a complete listing settles nothing of, a record made after the listing
was asked for, and compares times. The poll's records carry the time of the poll's clock, which tests freeze: two records made under a clock that stands still have
the same time, and a record the stream wrote after the listing was asked for looked as old as the listing. The listing
therefore also notes the last record id when it is asked for (`Records.Last`, `listing.Before`), and a record is newer
when its id is above that or its time is later (`listing.newer`, `internal/service/poll/handler.go`). Spec 15's TS-15-43,
which makes its records under a frozen clock, passes with it; TS-18-50 and TS-18-51 test the rule.

## 3. The job document's field lists gained `cancel_cause` (D-7)

TS-12-9, TS-12-10 and TS-12-14 (`internal/service/jobs/finish_test.go`, `read_test.go`) pin the exact fields of the job
document. R8 adds `cancel_cause`, so their lists gained it; nothing else about them changed.

## 4. Spec 15's timing tests run without the stream (D-9)

TS-15-4, TS-15-6, TS-15-7 and TS-15-52 (`internal/service/poll/supervisor_test.go`, `failure_test.go`) follow the loop's
timing by counting the waits registered on the fake clock. The stream registers its own waits on the same clock (its
backoff and its idle limit), which made the counts wrong. They run with `withoutStream()`, a test option that sets the
supervisor's unexported `streamless` seam, so that they still test the loop alone; every test of this spec runs with
the stream.

## 5. Test seams

- The supervisor's `beforeEvent` hook is called before an event is acted on, so a test can hold the handling of an event
  while the idle limit's clock moves (TS-18-28).
- The jobs layer exports, to its own tests only (`export_test.go`), `AfterLook`, a hook between the look at a job and its
  cancel, with which TS-18-58 makes a job change state in that moment.
- The tests of the stream move a fake clock themselves and wait for the stream to register its next wait before they
  move it (`nextWait`), since the stream and the loops share the clock.
