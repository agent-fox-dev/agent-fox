# Erratum 13: where the worker does something other than the spec says

**Spec:** `13_agent_fox_worker`. Each divergence below was decided during the survey or on the way, and is
recorded here because the spec stays as it was written. The code and the test of each are named so that a
reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. The tests that run the binary share their helpers with the moved tests by copy (task 1)

**Spec:** "Unit tests move with the code. Tests that build and run the binary stay in `cmd/af`" (R1).

**Code:** `smoke_test.go`, `schema_test.go` and the three signal and hook tests of the ring-1 commands
(`cmd/af/ring1_binary_test.go`) stay in `cmd/af`. They use about twenty helpers that the moved tests define
(`gitRepo`, `field`, `writeGate`, …), and two packages cannot share the helpers of their tests, so
`cmd/af/helpers_test.go` holds copies of them. Deviation key: `binary_tests_helpers_copied`.

## 2. `gateway.Dial` takes a context and options, and `gatewayd` is a gRPC server in a module of its own (task 2)

**Spec:** "Assumed: `gateway.Dial(url, token string) (sandbox.Provider, error)`" and "a `gatewayd.Server` that is
an `http.Handler`… served from `httptest.NewServer`" (Verified External API).

**Code:** `gateway.Dial(ctx, rawURL, gateway.Options{Token})` returns a `*gateway.Provider`
(`../agentkit-go/sandbox/gateway/dial.go:36`), and a plaintext `http://` URL is allowed only for a loopback
host. `gatewayd.New(gatewayd.Config{…})` makes a `*gatewayd.Server` that serves gRPC on a `net.Listener`
(`gatewayd/server.go:16`), and it is a separate Go module, so `go.mod` gains a `require` and a `replace` for
`../agentkit-go/sandbox/gatewayd`, as `../hub/go.mod` has them. The fake hub serves it on a loopback TCP port
(`internal/hub/fakehub/gateway.go`), in callback mode, where the token of a lease that is held and has time
left is the one that opens it. Test: `TestFakehubServes…_TS_13_80` (`internal/hub/fakehub/worker_test.go`).
Deviation key: `gatewayd_is_grpc`.

## 3. Hub's real API is not what the spec assumed for five of its calls (task 2)

**Spec:** "Hub's HTTP API. Every shape is **unverified**" (Verified External API).

**Code:** hub's OpenAPI document is at `../hub/docs/openapi.yaml`, and `internal/hub/calls.go` follows it where
it differs from the spec's assumption:

| Call | The spec assumed | Hub's |
|---|---|---|
| resolved variables | `{"vars": {NAME: value}}`, or a list of `{name, value}` | a list of `{key, value, origin}` (all three encodings are read) |
| the mode of a workspace | `mode` | `workspace_mode` (`mode` is read too) |
| post an events batch | `{events: [{seq, ts, type, data}]}` | a bare array of `{id, run_id, event_type, timestamp, payload}`, at most 1000, `payload` an object; the id is the run and the number of the event, so a batch posted twice adds nothing |
| post usage | `{model, input_tokens, output_tokens, cost_usd, turns}` | `{model, input_tokens, output_tokens, cache_read_tokens}`; the client sends the cost, the cost source, the brain and the turns beside them, which hub ignores |
| complete a session | the status of the envelope | `completed`, `failed` or `timeout` (`hub.SessionCompleted`, …); the worker maps the status of the envelope onto them |

The branch push route takes the branch as one path segment with each `/` escaped (`fix%2F42-nil-map`), which
`url.PathEscape` gives. The fake hub serves these shapes (`internal/hub/fakehub`), so that the client is tested
against what hub does. Tests: `TestEveryWorkerCallIs…_TS_13_11`, `TestBothEncodingsOf…_TS_13_19` and
`TestTheRecordTheVariablesAndTheBodiesOfTheCalls` (`internal/hub/calls_test.go`). Deviation key:
`hub_shapes_from_openapi`.

## 4. The test that the worker's calls leave the caches alone is in `internal/service/api` (task 2)

**Spec:** TS-13-12 is "a hub client with warm identity and visibility caches".

**Code:** the hub client has no cache. The two caches are the fields of `auth` in `internal/service/api`, which
nothing outside that package can read, so `TestTheWorkersCallsDoNotTouchTheIdentityOrVisibilityCaches_TS_13_12`
(`internal/service/api/workercalls_test.go`) is there.

## 5. The bounds are durations of the client, and the tests scale them (task 2)

**Spec:** "a hub client with an injectable clock" (TS-13-13), "with a fake clock" (TS-13-15).

**Code:** the bound of an ordinary call is `Client.HTTP.Timeout` (10 s, as the core's test already asserts) and
the bound of the lease and the push of a branch is `Client.LongTimeout` (5 minutes when zero). A test sets them
to tens of milliseconds and delays the fake hub's route for longer, which is the same experiment without a clock
to inject; the defaults are asserted as they are (`TestARequestIsBoundedTo…_TS_13_13`,
`TestTheLeaseAndTheBranchPushUseTheLongBound_TS_13_15`).

## 6. The commands a gateway box runs are recorded where its tools are called (task 4)

**Spec:** "the gateway's backend records every spec it creates and every command it executes"
(R10, TS-13-49, TS-13-50, TS-13-54).

**Code:** `gatewayd` relays the agent protocol to the box it holds without looking into it
(`gatewayd/relay.go`), so a backend that wraps the sandboxes it creates sees the specs and the
closes, and no command. The fake hub's backend records those
(`fakehub.Gateway.Created`, `Closed`), and the commands are recorded by wrapping the provider that
dials the gateway, on the side of the client, where the execute tool of the box is called
(`fakehub.Gateway.Instrument`, `Gateway.Commands`), in the same order as the gateway's own events.
A test of the worker that needs the commands installs that wrapper in the registry it gives the
attempt. Deviation key: `commands_recorded_at_the_tool`.

## 7. The box measures through `Config.InBox`, and the profile is named by it (task 4)

**Spec:** "In this mode the fix pipeline opens the box before its own preflight's detection" (R7).

**Code:** the mode is `afcmd.Config.InBox` (`InBox{Profile}`), which the worker sets and `af` never
does. With it `afcmd.fixLoop` opens the box first (`internal/afcmd/fix.go`, `fixAttempt`), then runs
the preflight, the brief and the loop in it, with the detection, the gate and the revert check of
`fix.BoxMeasurer` (`internal/fix/boxmeasure.go`) in place of the host's. `fix.PreflightParams.Detect`
is the new seam of the detection; `Gate` and `LoopParams.Conform` were there. The host's seams are
still `Config.Gate` and `Config.Conform`, which the tests give counters. Tests:
`TestDetectionBaselineGateAndRevertCheckRunInTheBoxAndNeverOnTheHost_TS_13_49` and the others of
`internal/afcmd/inbox_test.go`.

## 8. A lost box is retryable only for a box that says it is remote (task 4)

**Spec:** "A ring-1 command that cannot run because the box or the gateway was lost ends the loop
with a `sandbox` failure marked retryable" (R7).

**Code:** `box.Entry.Remote` (set for the gateway) makes `Session.Exec` return its failures with
`Retryable` true (`internal/box/exec.go`); under `podman` a box that cannot run a command is still a
failure that is not retryable, as the tests of spec 10 have it. `gateError` and the revert check of
`fix.Measure` return a `*toolio.Failure` of the measuring as it is, which they wrapped as `io`
before. Test: `TestABoxLostDuringMeasuringEndsTheLoopWithARetryableSandboxFailure_TS_13_56`, which
loses the gateway during the probe, the detection, the gate and the revert check.

## 9. The worker half of TS-13-56 waits for the worker (task 4)

**Spec:** TS-13-56 "the worker returns an error carrying `*toolio.Failure` … and no envelope".

**Code:** task 4 delivers the loop's side: the envelope is `failed`, category `sandbox`, retryable.
The worker that turns it into a transient error is task 8 (TS-13-58).
