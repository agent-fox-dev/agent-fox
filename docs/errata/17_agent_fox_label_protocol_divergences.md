# Erratum 17: agent-fox label protocol

Where the implementation of `17_agent_fox_label_protocol` differs from what the spec's text assumes, and why.

## 1. Hub's label route is as R3 assumed

Design Decision 20 asked for hub's label route to follow hub's OpenAPI document where it differs from R3. It does not
differ: `POST /api/v1/workspaces/{slug}/labels` (`ensureForgeLabel` in `../hub/docs/openapi.yaml`) takes `{name, color,
description}` with a PAT that has `forge:write`, answers `201` for a label it created and `200` for one that was there
(returned as the forge holds it, never changed), and `401`, `403`, `404`, `422`, `429`, `5xx` otherwise. `color` may
carry a `#`; the service sends six hex digits without one. Hub's `422` for a client it cannot build is a rejection, and
the subscribe answers it `422` `forge_forbidden` like any other refusal (`internal/service/api/subscribe.go`,
`labelFailure`; TS-17-17). The client also counts a relayed "already exists" refusal as present, as R3 says
(`internal/hub/calls.go`, `alreadyThere`; TS-17-22).

## 2. Where the "no runner" mark is written

R4 says a cleanup for a cancel that reaches no runner is marked "in the transaction that cancels the job". Whether a
runner of this process holds a running attempt is known only after that transaction: a claim registers its run before it
reads the job, so the layer looks for the run once the cancel has committed (`Service.cancel`). The mark is therefore
written in the cancel's transaction when no attempt is running, and otherwise right after it, before the route answers
(`Store.MarkCleanup`), so the answer still shows `labels.cleanup` `"pending"` (TS-17-29). A process that stops between
the two leaves `cleanup_wanted` set, and the attempt, reaped after the restart, marks the cleanup when it is recorded
cancelled with no hub session (`Store.EndAttempt`).

## 3. What a try that stays pending wrote

R4 says a finished cleanup records what its try wrote. A try that leaves the cleanup pending (hub became unavailable
half-way) records nothing, so that the next try still finds in `labels.set` the status labels the job's attempts set; it
reads the issue again and writes only what is still missing (`Store.EndCleanup`; TS-17-40, TS-17-43). The cleanup that
ends records what its last try wrote, as R5 says.

## 4. Two existing tests list the label step

TS-11-58 (`internal/service/api/subscribe_test.go`) and TS-11-81 (`cmd/agent-fox/smoke_test.go`) assert the exact hub
requests made with the token at subscribe. They now list the seven label creations after the gate and the user call,
which is the behaviour R2 adds.

## 5. TS-15-50 stands unchanged

R8 says the poll tests that assert that a job cancelled while queued or blocked holds nothing change to assert R7's hold.
The poll's harness builds the jobs layer without a cleaner, where R7 says such a job got no cleanup and holds nothing,
which is what TS-15-50 asserts, so it stands. The holds that R7 adds are asserted by TS-17-58 to TS-17-63 with the
hub-backed cleanup wired (`internal/service/poll/hold_test.go`, `withCleanup`).
