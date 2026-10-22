# Erratum 11: where the service core does something other than the spec says

**Spec:** `11_agent_fox_service_core`. Each divergence below was decided during the survey, on the
way, or by the review of the finished branch, and is recorded here because the spec stays as it was
written. The code and the test of each are named so that a reader can check what is delivered.
Where a commit declares the divergence, its `Deviation:` trailer key is named.

Hub could not be run where this was written. Its API was read from `hub/docs/openapi.yaml` and
`hub/docs/api.md`, and the service was exercised against `internal/hub/fakehub`.

## 1. Hub's user object names the login `username`, not `login` (task 4)

**Spec:** Design Decision 35 and *Verified External API* have the user's identifier as `id` and an
optional `login`, "to confirm against hub's published API description before implementation".

**Hub:** `User` in `docs/openapi.yaml` has `id` (a UUID string) and `username`; it has no `login`.
The assumption about `id` holds.

**Code:** the hub client reads the login from `login`, which is the spec's name and what the fake
hub sends, and else from `username` (`internal/hub/client.go:198`). Nothing in this release uses the
login; it is read so that a later use does not find it empty.

**Test:** TS-11-32 (`internal/hub/client_test.go:258`, the row with `username`).

## 2. The generated doc of a command has no `package main` (task 11)

**Spec:** TS-11-77 asserts that `docs/generated/cmd/agent-fox.txt` contains `package main`.

**Code:** `go doc -all`, which `make doc` runs on the import path, prints no package clause for a
command: `docs/generated/cmd/af.txt` has none either, and the file starts with the package comment,
`Command agent-fox is …`. The committed file is exactly what `make doc` writes.

**Test:** TS-11-77 asserts that the file starts with `Command agent-fox `
(`docs/service_docs_test.go:163`).

## 3. A route the mux would answer in plain text is answered in the envelope (task 5)

**Spec:** an unknown route is `404 not_found` (11-REQ-7.3). It says nothing of a wrong method, of a
path that is not canonical, or of an unknown path under `/api/v1`.

**Code:** `net/http`'s mux answers an unknown route and a wrong method in plain text, and redirects a
path that is not in canonical form. `internal/service/api/server.go:115` (`routed`) turns them into the
envelope: `404 not_found` for an unknown route, including one under `/api/v1`, which is not a route and
is answered without authentication; `405 method_not_allowed` with the `Allow` header for a method the
route does not take; and `404 not_found`, with no redirect, for a path that is not canonical
(`server.go:142`, `canonical`). A request for the slugs `.` and `..` is such a path, so it gets `404`
and not the `400 invalid_request` that the slug rule would give.

**Test:** `TestErrorsAreEnvelopesIncludingTheOnesTheMuxWouldWriteInPlainText`
(`internal/service/api/server_test.go`), TS-11-49 (`internal/service/api/errors_test.go`).

## 4. Two requests are refused that the spec does not mention (tasks 6, 7)

**Spec:** a header that is missing, is not `Bearer`, or has an empty token is `401` without a call to
hub (11-REQ-6.1); `hub_token` must be non-empty (11-REQ-8.3).

**Code:** a bearer token that has white space inside (`Bearer a b`) is not a token, and is answered
the same `401` without a call to hub (`internal/service/api/auth.go:141`). A `hub_token` with a control
character cannot be sent as a header value; without a rule it would surface as hub being unavailable
(`502`), so it is `400 invalid_request` with no call to hub (`internal/service/api/subscribe.go:36`).

**Test:** `TestBearerTokenReadsTheHeaderTheWayTheRuleSays` (`auth_test.go:153`), TS-11-37 (the header
`Bearer two tokens`), TS-11-57 (a token with a newline and with a NUL). Commit trailer key: none; the
rules were added in task 7 and by the review of the branch.

## 5. The XDG variables are read as unset when they are empty or relative (task 1)

**Spec:** `$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` are used "when unset" otherwise `$HOME/.config` and
`$HOME/.local/state` (11-REQ-2.1, 11-REQ-2.3).

**Code:** a variable that is empty, or that is not an absolute path, is treated as unset, as the XDG
Base Directory specification says a relative path is to be (`internal/service/config/config.go:147`,
`xdgDir`). `docs/cli.md` and `docs/configuration.md` say so.

**Test:** TS-11-9 and TS-11-11 set the variable to a directory and unset it, and
`TestAnEmptyOrRelativeXDGVariableIsAsIfUnset` (`internal/service/config/config_test.go`) sets it to the empty
string and to a relative path that does hold a config.

## 6. A second signal does not cut the drain short (task 10)

**Spec:** 11-REQ-1.7 has the service wait up to 30 seconds for the requests in flight, close the
database and exit 0.

**Code:** the first version of `serve` let a second SIGINT or SIGTERM end the process the usual way. It
would have exited by signal, with a request cut off and the database not closed, which the criterion does
not allow. The signals are now taken until the service returns (`cmd/agent-fox/run.go:40`); SIGKILL is
how an operator ends it sooner. Found by the review of the branch.

**Test:** TS-11-7 sends the signal twice, 100 ms apart, in the SIGTERM and SIGINT cases
(`cmd/agent-fox/start_test.go`, the subtests `terminated twice` and `interrupt twice`).

## 7. Only the body of a 200 is read from hub (task 4, review)

**Spec:** the hub client reads at most 1 MiB of a response body (11-REQ-5.2); an answer is classified by
its status (11-REQ-5.4).

**Code:** the body of a 200 is read, up to 1 MiB, because it is the only one used. A 401, 403, 404 or
any other status is classified by the status alone, so that an error page of any size does not turn a
refusal into "unavailable" (`internal/hub/client.go:153`).

**Test:** `TestTheBodyOfAnErrorAnswerIsNotWhatDecidesIt` (`internal/hub/client_test.go`), TS-11-31.

## 8. The fake hub answers 404 for a workspace a token may not see (task 4)

**Spec:** the step of task 4 has the fake answer 403 for a known token that does not see the workspace.

**Code:** hub's own answer is 404 (*not found or forbidden*, `openapi.yaml`), so the fake answers 404 by
default; `Hub.HiddenStatus` changes it (`internal/hub/fakehub/fakehub.go:59`). The client maps 403 and
404 alike, and the tests that need a 403 make the fake fail the route with it.

**Test:** TS-11-71, TS-11-60.

## 9. One table has a column the spec does not name (task 3)

**Spec:** 11-REQ-4.6 lists what a subscription records and leaves the columns to design.

**Code:** `af_subscriptions` has a `revision`, 1 for a new row and one more for each replacement
(`internal/service/store/migrations.go`). The one `INSERT … ON CONFLICT DO UPDATE … RETURNING` statement
that stores a subscription uses it to say whether the row is new, which the `201` and `200` of
`PUT` need; no second statement and no comparison of times is involved. The subscription view does not
show it.

**Test:** `TestUpsertCreatesThenReplacesTheRowInFull` (`internal/service/store/store_test.go`).

## 10. Some tests are in two parts (tasks 1, 3, 7, 8, 10, 12)

TS-11-10, 13, 14, 18, 19, 27 and 28 have a half that is about the function (the loader, the key loader,
the store) and a half about the exit code of the binary. The first is owned by the task that builds the
function, and the second by task 10, which wires the start. TS-11-62 is a property about what is
written and what is read; the writing half (`PUT`, `internal/service/api/subscribe_test.go:489`) is in
task 7 and the reading half (`GET`, the list and `DELETE`,
`internal/service/api/workspaces_test.go:543`) in task 8. TS-11-23 runs in process, with the log in a
buffer; TS-11-53, which reads the stderr of the binary, covers the same code from outside.

## 11. Tests that passed before the code existed (tasks 4, 5, 6, 8)

Four groups of tests passed against the stubs that were written to see the others fail, because what
they assert holds trivially for a stub (a client whose every call is "unavailable" is also the answer that
TS-11-34 and TS-11-36 expect of one; a stub that logs nothing leaks nothing; a cache that holds nothing
stays within its bound). They were checked by mutation instead, with mutants that break each property;
every mutant is killed. The commits declare them with the `Deviation:` trailer key
`test_first_tests_passed_on_stub`: TS-11-34, TS-11-36 and the echo test of the fake hub (task 4);
the log test (task 5); the concurrent subtest of TS-11-46 (task 6); and TS-11-63, 69, 23 and 54
(task 8).
