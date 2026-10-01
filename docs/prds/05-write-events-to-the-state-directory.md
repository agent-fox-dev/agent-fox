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
makes the state directory a single, overridable root.

## Goals

- Every run that writes a report also writes its events, with no flag.
- An event and a report from the same run share a `session_id`, readable with
  `jq` and nothing else.
- One flag, `--emit-events`, controls the live stream on stderr, and what it
  prints is the complete stream.
- One variable, `XDG_STATE_HOME`, relocates everything the tools write to the
  state directory, so a test can point it at a temp directory.

## Non-goals

- Changing the event types, their fields (other than `session_id` and the
  `run_end` addition below) or the 15-second heartbeat.
- Changing what the report file contains, other than `session_id`.
- Pruning, rotating or compressing either directory. Nothing prunes `runs/`
  today and nothing will prune `events/`.
- Reading events back (a `show` or `tail` subcommand). [ADR 03](../adr/03-rebuild-the-skills-as-tools.md)
  rules out new subcommands, and `jq` is enough.

## Functional requirements

### 1. The state directory

- `XDG_STATE_HOME`, when set and non-empty, **is** the directory the tools
  write into. Reports go to `$XDG_STATE_HOME/runs/` and events to
  `$XDG_STATE_HOME/events/`.
- When it is unset or empty it defaults to `$HOME/.local/state/agent-fox`
  (`os.UserHomeDir`). The two subdirectories are created as needed.
- A run that cannot resolve either records the existing
  `report_file_not_written` warning, and the new `events_file_not_written`
  one (§3), and carries on.
- `DefaultReportPath` and the new events path are both computed from one
  `stateDir()` function in `internal/toolio`, replacing `stateHome()`.

**This changes the meaning of `XDG_STATE_HOME`.** Today it names the parent
(`~/.local/state`) and `agent-fox/` is appended; after this PRD it names the
`agent-fox` directory itself. A user who exports `XDG_STATE_HOME=~/.local/state`
globally will find new files in `~/.local/state/runs` rather than
`~/.local/state/agent-fox/runs`. See open question 1.

### 2. `--emit-events` replaces `--events`

- `--events` is removed. `--emit-events` is a boolean flag. Without it,
  stderr carries the human progress, as the default does today. With it,
  stderr carries one JSON event per line instead of the human progress.
- **Complete stream.** The stream, on stderr and in the events file, is the
  same and is not filtered by other flags:
  - `tool_call` events are emitted for every model tool call, not only under
    `--verbose`;
  - `text` events (`phase`, `delta`) are emitted for the model's prose, not
    only under `--show-text`;
  - no event field is cut, abbreviated or dropped for size.

  `--verbose` and `--show-text` keep their meaning for the *human* progress
  only.
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
  `<state>/events/<tool>-<started_at>-<pid>.jsonl`, the same stem as the
  report with the extension `.jsonl`. The file is opened once, created
  exclusively and appended to with one whole line per write, so a killed
  process leaves a valid prefix, as `--events-file` does today.
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
- A caller correlates with:

  ```
  jq -r 'select(.type=="run_end") | .session_id' events/fix-*.jsonl
  jq -r '.session_id, .report_file' runs/fix-*.json
  ```

### 5. Tests do not touch the real state directory

- `TestMain` in `internal/toolio` sets `XDG_STATE_HOME` to a directory it
  creates, removes it on exit, and the same is done in every other package
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
  both subdirectories and the changed meaning of `XDG_STATE_HOME`; the
  environment variables table.
- `docs/prds/README.md`: this PRD in the table.
- `docs_test.go` asserts on `$XDG_STATE_HOME/agent-fox/runs`; update it with
  the docs.

## Open questions

1. **Reusing `XDG_STATE_HOME` with a different meaning.** The XDG convention
   is that it names the parent of every application's state directory, so a
   user who sets it for other tools gets `runs/` and `events/` beside
   unrelated directories, and a user who relied on the old behaviour has
   their files move. The alternative is to keep the XDG meaning and add an
   override that names the directory directly, say `AGENT_FOX_STATE_DIR`,
   which tests set. Recommended, but it departs from what was asked; this
   draft follows the request.
2. **Size of the always-on file.** A full stream includes every `text` delta
   and a heartbeat every 15 seconds. An `impl` run of several hours could
   write hundreds of megabytes. Options: leave it, or batch `text` deltas into
   one event per model turn (still complete, much smaller). Not decided here.
3. **`tool_call` content.** "Nothing gets shortened" is read here as "no
   event type is gated by another flag". If it also means a `tool_call`
   should carry the call's arguments and result, that is a larger change
   (they can contain file contents and secrets) and needs its own section.
4. **Session id in the filenames.** The files keep `<tool>-<started_at>-<pid>`.
   Putting the session id in the stem instead would let a caller join with
   `ls` alone, at the cost of longer names and a break with the pattern
   documented today.
5. **Old `events-file` users.** The flags are removed without a deprecation
   period. A caller still passing them gets a usage error (exit 2) before any
   work, which is loud but non-destructive.
