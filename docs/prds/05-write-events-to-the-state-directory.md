# Write events to the state directory

## Intent

Every run already leaves a report file in `runs/` ([PRD 02](02-trim-and-chain-the-results.md))
and can leave a JSONL event stream ([PRD 03](03-make-the-tools-observable-and-self-describing.md)).
The two are not designed together, and three things follow:

- **The event stream is opt-in per call.** It exists only if the caller
  remembered `--events-file <path>`. A run that went wrong yesterday has an
  envelope in `runs/` and, usually, nothing that says how it got there.
- **Events and reports cannot be joined.** An event carries `ts`, `tool` and
  `type`; a report is named `<tool>-<started_at>-<pid>.json`. Matching them
  means guessing from timestamps and a pid that is only unique on one host
  between reboots.
- **The state directory is not isolated from tests.** `XDG_STATE_HOME` is read
  as the *parent* of `agent-fox/runs`, and the unit tests in
  `internal/toolio` that build their own `App` (`tool`, `testapp`) never set
  it. A developer running `make test` finds `tool-…json` and `testapp-…json`
  among the reports of real runs.

This PRD makes the event stream a permanent record next to the report, ties
the two together with a session id, simplifies the flags that control it, and
makes sure tests write to a temporary state directory.

## Goals

- Every run that writes a report also writes its events, with no flag.
- An event and a report from the same run share a `session_id`, readable with
  `jq` and nothing else.
- One flag, `--emit-events`, controls the live stream on stderr, and what it
  prints is the complete stream.
- Tests and CI relocate everything the tools write to the state directory
  through `XDG_STATE_HOME`, which the tools already honour. Nothing else
  changes about that variable, so other applications and services are not
  affected.

## Non-goals

- Adding event types, or changing any event's fields beyond `session_id`,
  `run_end.report_file`, the richer `tool_call` and the batched `text` event
  below. The 15-second heartbeat stays.
- Changing what the report file contains, other than `session_id`.
- Pruning, rotating or compressing either directory. Nothing prunes `runs/`
  today and nothing will prune `events/`.
- Reading events back (a `show` or `tail` subcommand). [ADR 03](../adr/03-rebuild-the-skills-as-tools.md)
  rules out new subcommands, and `jq` is enough.

## Functional requirements

### 1. The state directory

The state directory does not change. It stays what it is today:
`$XDG_STATE_HOME/agent-fox` when `XDG_STATE_HOME` is set and non-empty, else
`$HOME/.local/state/agent-fox` (`os.UserHomeDir`). Reports go to its `runs/`
subdirectory, events to its new `events/` subdirectory; both are created as
needed.

- `XDG_STATE_HOME` keeps the meaning the XDG convention gives it: the parent of
  every application's state directory. No new variable is introduced, and a
  user's own setting of it keeps working as before. Only tests and CI set it,
  to a temporary directory (§5).
- `DefaultReportPath` and the new events path are both computed from the one
  existing `stateHome()`, so the two directories cannot diverge.
- A run that cannot resolve a home directory records the existing
  `report_file_not_written` warning and the new `events_file_not_written`
  one (§3), and carries on.

### 2. `--emit-events` replaces `--events`

- `--events` is removed. `--emit-events` is a boolean flag. Without it,
  stderr carries the human progress, as the default does today. With it,
  stderr carries one JSON event per line instead of the human progress.
- **Complete stream.** The stream, on stderr and in the events file, is the
  same and is not filtered by other flags:
  - `tool_call` events are emitted for every model tool call, not only under
    `--verbose`;
  - `text` events are emitted for the model's prose, not only under
    `--show-text`. A `text` event carries the whole prose of one model turn,
    not one event per streamed delta: its fields are `phase`, `turn` and
    `text` (`delta` is gone, since the content is no longer a fragment). It
    is emitted when the turn ends, next to that turn's `turn` event. This
    keeps the stream complete and a long `impl` run's file small;
  - no event field is cut, abbreviated or dropped for size.

  `--verbose` and `--show-text` keep their meaning for the *human* progress
  only; `--show-text` still streams the prose live in the default text mode.
- **`tool_call` says what happened.** Besides `phase`, `name` and `blocked`,
  it gains:
  - `arguments`: the call's arguments, verbatim, as the JSON the model sent;
  - `ok`: whether the call succeeded;
  - `exit_code`: present for a call that runs a program (the shell), the
    program's exit status;
  - `error`: present only when `ok` is false, the error message the call
    returned (for a call the shell guard refused, the guard's message).

  The call's *result* (file contents read, command output) is not recorded.
  `arguments` can still hold text the model wrote into a file or a command
  line, and event content is not sanitised, as before.
- `--quiet` silences stderr under both settings, as now. It does not affect
  the file.
- An invalid combination is a usage error, as before: there is no value to
  validate any more, so `ValidEvents` and the `EventsText`/`EventsJSONL`
  constants go.

### 3. `--events-file` is removed; events are always written

- `--events-file` is removed. Passing it, or `--events`, is a usage error that
  names the replacement, in the form PRD 03 already uses for a flag a tool
  does not accept: `--events-file was removed; events are always written to
  <state>/events/` and `--events was renamed --emit-events`.
- Every run that writes a report also writes
  `<state>/events/<tool>-<started_at>-<session_id>.jsonl`, the same stem as the
  report (§4) with the extension `.jsonl`. The file is opened once, created
  exclusively and appended to with one whole line per write, so a killed
  process leaves a valid prefix, as `--events-file` does today. Because
  `arguments` can carry sensitive text, the `events/` directory is created
  with mode `0700` and the file with `0600`; the report keeps its modes.
- The file exists for exactly the runs that have a report: not for
  `-h`/`--help`, `--version`, a bare invocation on a terminal, or a usage
  error refused before anything was fetched (a flag-parse failure). No empty
  files.
- A run that cannot create the file (permissions, read-only filesystem)
  records a `low` `events_file_not_written` warning in the `report` stage and
  continues. It does not fail, and `--emit-events` still works. This replaces
  the current behaviour, where an unopenable `--events-file` is a usage error.
- The envelope's `artifacts` gains a kind, `events_file`, naming the path,
  next to `report_file`. `--dry-run` still writes both.

### 4. `session_id`

- Each run generates a `session_id` once, in `NewRun`, before anything else:
  128 random bits from `crypto/rand`, rendered as 32 lowercase hex characters.
  No new dependency. It is not derived from the pid or the clock, so it is
  unique across hosts and reboots.
- It is present on:
  - **every event**, as the fourth key of the shared header
    (`ts`, `tool`, `session_id`, `type`);
  - **the envelope**, as a top-level `session_id`, on stdout and in the
    report file alike.
- `run_end` gains `report_file` (the path, empty when the report was not
  written), so the last line of an events file names its report. The report
  names the events file through its `artifacts` entry. Together they join in
  both directions.
- **The session id is in the file names.** The report's name changes from
  `<tool>-<started_at>-<pid>.json` to `<tool>-<started_at>-<session_id>.json`,
  and the events file uses the same stem, so `ls` alone pairs them. The pid is
  dropped from the names (it stays out of the envelope, where it never was).
  `DefaultReportPath` takes the session id in place of the pid.
- A caller correlates with:

  ```
  ls runs/*-<session_id>.json events/*-<session_id>.jsonl
  jq -r '.session_id, .report_file' runs/fix-*.json
  ```

### 5. Tests do not touch the real state directory

- `TestMain` in `internal/toolio` sets `XDG_STATE_HOME` to a directory it
  creates and removes on exit; the same is done in every other package
  whose tests call `App.Main` (`cmd/*`, the pipelines' smoke tests). The
  per-test `t.Setenv("XDG_STATE_HOME", t.TempDir())` calls stay where a test
  asserts on the directory's contents.
- A test fails if, after the package's tests run, anything was created under
  the real `$HOME/.local/state/agent-fox`. This is checked by resolving the
  default path with `XDG_STATE_HOME` unset in a test that only inspects it, not
  by running anything against it.

## Documentation to update

Per `.specs/steering.md`, in the same change as the implementation:

- `docs/cli.md`: the Shared flags rows for `--events`/`--events-file`, the
  "Machine-readable progress" section (header, `run_end`, the removal of the
  `--verbose`/`--show-text` gates on the stream, the worked example), the
  warnings table (`events_file_not_written`), and the artifacts kinds.
- `docs/configuration.md`: "Report files" becomes "State directory", covering
  both subdirectories, the new file names and the modes of the events file;
  the environment variables table says tests set `XDG_STATE_HOME`.
- `docs/prds/README.md`: this PRD in the table.
- `docs_test.go` asserts on `$XDG_STATE_HOME/agent-fox/runs` and the report
  file name pattern; update it with the docs, and the report-path tests in
  `internal/toolio/report_test.go` for the session id in the name.

## Decisions

Settled while drafting, recorded so the implementation does not reopen them:

1. `XDG_STATE_HOME` keeps its XDG meaning; only tests and CI change it.
2. `text` events are batched per model turn.
3. `tool_call` records arguments and the outcome, never the call's result.
4. The session id replaces the pid in both file names.

## Open questions

1. **Old `--events` / `--events-file` users.** The flags are removed without a
   deprecation period. A caller still passing them gets a usage error
   (exit 2) before any work, which is loud but non-destructive.
2. **Existing files.** Reports already in `runs/` keep their pid-based names.
   Nothing renames them.
