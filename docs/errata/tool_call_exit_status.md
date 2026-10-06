# Erratum: a shell command's exit status is not a tool error, and the events file's heartbeat is slower

Project-wide, so no numeric prefix. It records two deliberate departures from
the event stream as specs 07 and 12 define it, made for #194.

## `tool_call`: `ok` for a shell command that ran

**Was (12-REQ-8, TS-12-45, TS-12-47, TS-12-61):** `ok` followed the tool
result's error flag, "independent of any exit status", and `error` was present
exactly when `ok` was false. A shell command that ran and exited non-zero —
`grep` finding nothing, `ls` of a missing file — was recorded as `ok: false`
with its whole output, and AgentKit's `[exit 1]` trailer, as the `error`.

In the run that prompted the change, about a third of the 35 `ok: false`
events were of that shape, so a reader filtering on `ok: false` to find what
went wrong read command output instead.

**Is:** a shell command whose exit status could be read — it ran to an exit —
is `ok: true` whatever the status, and carries no `error`. `exit_code` says
how it ended. `ok: false` and `error` are left for what actually went wrong:
a refusal by the shell guard, a command that did not run to an exit (killed
by a signal, timed out, aborted, never started) and a tool that failed.
`error` is still present exactly when `ok` is false.

Code: `internal/agentrun/phase.go`, `Runner.toolCall`. Tests:
`TestAShellCommandThatExitedNonZeroIsOK` and
`TestExitCodeFromTheRealShellToolsResults` (`internal/agentrun`), TS-12-45's
fifth case, and TS-12-61's smoke assertions (`internal/toolio`).

## `heartbeat`: a longer window in the events file

**Was (07-REQ-7, TS-07-45):** a heartbeat after every 15 seconds of silence,
on every stream the run writes, the events file included. In the same run
heartbeats were 417 of the file's 1475 lines.

**Is:** the `--emit-events` stream keeps the 15-second window. The events file
gets a heartbeat only after 60 seconds with nothing written to it, which still
tells a supervisor tailing the file a long, silent phase from a dead run. So
the file and the stream are the same events except for heartbeats: the file
has fewer of them.

Code: `internal/toolio/events.go`, `eventsSink.slowHeartbeats`;
`internal/toolio/app.go` applies it to the events file. Test:
`TestHeartbeatsArePreflightAndSlowerInTheFile`. TS-07-45 still finds a
heartbeat in the file, with the window it shortens for both.
