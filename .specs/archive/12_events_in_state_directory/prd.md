---
spec_id: "12"
spec_name: "events_in_state_directory"
title: "Always write the event stream to the state directory, joined to the report by a session id"
status: "active"
created_at: "2026-10-01T15:28:32.945469Z"
updated_at: "2026-10-01T15:28:32.945469Z"
intent_hash: "9aec9ecbeac557e0d3fdb5fcca6d919a60ba0917ac2c9a386ab69cfba460cb46"
schema_version: 2
source: "docs/prds/05-write-events-to-the-state-directory.md"
---
## Intent

Every run of `spec`, `issue`, `fix` and `impl` that writes a report file also leaves its complete event stream beside it in the state directory, and the two can be joined, in both directions, by a `session_id` that is unique across hosts and reboots. Live event output on stderr is one boolean switch and shows exactly what the file holds. The state directory is relocated by tests through the `XDG_STATE_HOME` variable the tools already honour, so a test run never writes to a developer's real state.

## Goals

- Every run that gets as far as opening the event sink writes `<state>/events/<tool>-<started_at>-<session_id>.jsonl` with no flag. A failure to create it is a `low` warning, never a failed run.
- Each run has a `session_id` (32 lowercase hex characters from `crypto/rand`). It is the fourth key of every event, a top-level key of the envelope on stdout and in the report file, and the last part of both file names. `jq` and `ls` are enough to pair an events file with its report.
- `run_end` carries `report_file`, and the envelope's `artifacts` carries an `events_file` entry, so each file names the other.
- `--emit-events` (boolean) replaces `--events text|jsonl` and `--events-file`. With it, stderr carries the JSON stream instead of the human progress. The stream on stderr is identical to the file's and is not filtered by `--verbose` or `--show-text`.
- `tool_call` records the call's arguments and outcome. `text` is one event per model turn, carrying the whole prose.
- After `make test`, nothing exists under the real `$HOME/.local/state/agent-fox`. The check runs in every package whose tests call `App.Main`.

## Non-goals

- New event types, or event-field changes beyond those listed here: `session_id` on the header, `run_end.report_file`, the richer `tool_call`, and the batched `text` event. The 15-second heartbeat stays as it is.
- Changes to the report file's content other than `session_id` and the `events_file` artifact entry.
- Pruning, rotating or compressing `runs/` or `events/`.
- A `show`, `tail` or other subcommand to read events back. ADR 03 rules out new subcommands, and `jq` is enough.
- Recording a tool call's *result* (file contents read, command output). Only arguments and outcome are recorded.
- Renaming, migrating or deleting reports already in `runs/` under their pid-based names.
- A new environment variable for the state directory, or any change to how `XDG_STATE_HOME` is read. A `--events-dir` flag is not added either.
- Making `--report-file` move the events file. The events file always lives under the state directory.
- Updating `docs/prds/04-serve-the-tools-over-mcp.md`. It is a draft PRD with no implementation in `cmd/`, and it still names `--events-file`. Whoever builds it reads this spec.

## Background

Today (`internal/toolio`):

- `App.Main` (`app.go`) builds the sink in `openEvents`. The sink writes to the file named by `--events-file` (opened `O_TRUNC`, mode 0644, and an unopenable path is a usage error) and to stderr when `--events jsonl` and not `--quiet`. With neither destination the sink is inactive, and `eventsSink.Active()` is false.
- The sink is built after flag parsing, `--detail`/`--total-budget`/`--input-kind`/`--events` validation, `--version`, `--schema`, the bare-invocation check and `--output` validation. Every earlier usage failure goes through `a.emit`, so it writes a report and no events. The bare invocation off-terminal does the same.
- `emit` computes the report path with `reportPath` → `DefaultReportPath(tool, started, os.Getpid())` (`report.go`), producing `<tool>-<started_at>-<pid>.json`. `WriteReport` creates the directory with 0755 and the file with 0644. `stateHome()` returns `$XDG_STATE_HOME` if non-empty, else `$HOME/.local/state`, and the code appends `agent-fox/runs`.
- `eventHeader` is `ts`, `tool`, `type`. `TextEvent` is `phase`, `delta`. `ToolCallEvent` is `phase`, `name`, `blocked`. `RunEndEvent` is `status`, `exit_code`. The header comment in `events.go` says the only `omitempty` is `phase_start.task`.
- `Progress` (`progress.go`) uses `sink() != nil` ("a sink is active") as a stand-in for "stderr carries JSONL" in three places. `Begin` prints nothing and returns a no-op. `Raw` emits a `text` event instead of writing prose. `ToolCall` is gated on `Verbose()`. These assumptions hold only while the sink exists solely when the caller asked for events. Once the sink is always active, they would silence the default human progress. This is the main behavioural risk of the change.
- `agentrun.Runner.trace` (`internal/agentrun/phase.go`) calls `Observer.ToolCall(phase, name, blocked)` once per call from the `core.ToolResultEvent`, and only if the observer's `Verbose()` is true. It calls `Observer.Raw(delta)` per `core.TextDeltaEvent` only when `Config.ShowText` is set. `Observer` is the interface `toolio.Progress` satisfies. `hooks_test.go` has a recording fake of it.
- `ValidEvents`, `EventsText`, `EventsJSONL` and the `events` enum declaration live in `cli.go`. `cmd/fix/main.go` has a table of value-taking flags for `normalizeArgs` that lists `events` and `events-file`. `toolflags.go` rewrites "flag provided but not defined" errors into "<tool> does not accept --x; it is a <tool> flag".
- The envelope has no session id. `SchemaVersion` is `2.0.0`. PRD 03 says that within a major version changes are additive only, and that removing a field is a major bump. Each `cmd/*/testdata/schema.golden.json` pins the `--schema` output, including the flags and the envelope properties.
- Tests: many already do `t.Setenv("XDG_STATE_HOME", t.TempDir())`, but several tests that reach `App.Main` do not (for example `TestTS06_8…`, `TestTS06_10…` in `report_test.go`, which pass `--report-file` only). `internal/toolio/app_test.go` has a `TestMain` that unsets model env vars. `cmd/fix/main_test.go` has a `TestMain` that builds binaries. `cmd/impl`, `cmd/issue` and `cmd/spec` have none. `internal/schematest` is a precedent for a shared test-helper package. With the events file always written, every such test would write events to the real `$HOME/.local/state/agent-fox/events`.
- Docs: `docs/cli.md` (Shared flags rows, "Machine-readable progress: the event types", warnings table, artifact kinds, the schema-version changelog), `docs/configuration.md` ("Report files", the environment variables table), `docs/model-usage.md` (line 46 mentions `--events jsonl` / `--events-file`), `docs_test.go` (`TS-06-63`: heading "## Report files", `$XDG_STATE_HOME/agent-fox/runs`) and `docs_events_test.go` (`TS-07-42`: the strings `--events`, `--events-file`, `jsonl`). `docs/prds/README.md` already lists this PRD.

## Requirements

### R1. Session id

`NewRun` generates a `session_id` before anything else, once per run. It is 16 bytes from `crypto/rand`, hex-encoded to 32 lowercase characters. Nothing is derived from the pid, the clock or the host. It adds no dependency. A `Run` exposes it, and every component that needs it (the sink, the envelope, the file-name functions) reads it from there. A run that returns before the sink exists, such as a flag-parse failure that still emits an envelope, still has a `session_id` in that envelope.

### R2. Where session_id and the file names appear

- Every event's shared header becomes `ts`, `tool`, `session_id`, `type`, in that key order. The sink stamps it from the run's session id.
- The envelope gains a required top-level `session_id` string, placed right after `schema_version`. It is identical on stdout and in the report file, and it is present on every envelope, including usage-error ones. The `--schema` document and its golden files describe it.
- `run_end` gains `report_file`. It is the path the report was written to, or the empty string when it was not written. It is always present. It is emitted from the same code that decides the envelope's `report_file`, so the two cannot disagree.
- The envelope's `artifacts` gains the kind `events_file` with a `path`, appended after `report_file`. The kind is added to the artifact-kind enum in the `--schema` description. It appears in stdout and in the report file, and under `--dry-run`. It is present only when the events file was actually created.
- `DefaultReportPath` takes the session id string in place of the pid: `<tool>-<started_at>-<session_id>.json`, with the same colon-free timestamp. A new `DefaultEventsPath` returns `<tool>-<started_at>-<session_id>.jsonl` under `events/`. Both resolve the base directory through the one existing `stateHome()`, so the two directories cannot diverge. When `stateHome()` fails, `emit` records the existing `report_file_not_written` warning (stage `report`) and the events open records `events_file_not_written` (R4). The run carries on.

### R3. The state directory

The state directory is unchanged: `$XDG_STATE_HOME/agent-fox` when `XDG_STATE_HOME` is non-empty, else `$HOME/.local/state/agent-fox` via `os.UserHomeDir`. Reports go to `runs/`, events to `events/`, both created as needed. `XDG_STATE_HOME` keeps its XDG meaning and no new variable is introduced. `--report-file` overrides the report path only. The events file keeps its default path, whose stem is the report's default stem (`<tool>-<started_at>-<session_id>`), even when `--report-file` is given.

### R4. The events file is always written

- The file is opened once, where `openEvents` builds the sink today. It is created exclusively (`O_CREATE|O_EXCL|O_WRONLY|O_APPEND`) with mode `0600`. Each event is one whole-line `Write`, so a killed process leaves a valid prefix. No existing file is ever truncated.
- The `events/` directory is created with mode `0700`. Its parents (`agent-fox/` and the state home) are created with `0755` as before, so `runs/` is not made private by accident. An `events/` directory that already exists keeps its mode. The report keeps its current modes (directory `0755`, file `0644`).
- The file exists for every run that reaches that point. It does not exist for `-h`/`--help`, `--version`, `--schema`, a bare invocation on a terminal, or any usage error refused before the sink is built: a flag-parse failure (including the removed-flag errors in R5), an invalid `--detail`, `--total-budget` or `--input-kind`, a bare invocation off-terminal, or an invalid `--output`. Those still write a report, as today, whose `artifacts` has no `events_file`. No empty events file is created. A run past that point (a later usage error, a failing input, a refused preflight, `--preflight`, `--dry-run`) gets the file, and its last line is `run_end`.
- If the path cannot be resolved or the file cannot be created (permissions, read-only filesystem, a collision), the run records a `low` warning `events_file_not_written` at stage `report`. It does not fail and does not change the exit code. `--emit-events` still works. The code is added to the warning-code list and the code-to-stage table in `warncode.go`, and to the warnings table in `docs/cli.md`. The warning is recorded after the sink is attached to the run, so it is also emitted as a `warning` event on stderr under `--emit-events`. When the file could not be created and `--emit-events` is not set, the sink has no writer and is inactive, and no heartbeat ticker starts. The run is otherwise unaffected.
- The heartbeat now runs for the life of every run that has an events file.

### R5. `--emit-events` replaces `--events` and `--events-file`

- `--emit-events` is a shared boolean flag. Without it, stderr carries the human progress exactly as the default does today. With it, stderr carries one JSON event per line and no human line, which is what `--events jsonl` did. `--quiet` silences stderr under both settings and never affects the file.
- `--events`, `--events-file`, `Common.Events`, `Common.EventsFile`, `ValidEvents`, `EventsText`, `EventsJSONL` and the `events` enum are removed. `Common` gains `EmitEvents bool`. The `events` and `events-file` entries leave the value-taking-flag table in `cmd/fix/main.go`. `--emit-events` is not added there, because it takes no value.
- A caller still passing either removed flag gets a usage error (exit 2, `stage: "usage"`) through the existing "flag not defined" path, with these messages:
  - `--events-file was removed; events are always written to <state>/events/`
  - `--events was renamed --emit-events`

  `<state>/events/` is literal text. The messages are produced from the same hook that rewrites other tools' flags (`unsupportedFlagMessage`). Both the `--flag value` and `--flag=value` spellings are covered. Because Go's `flag` stops at the first unknown flag, only the first one encountered is named. There is no deprecation period. As with every flag-parse failure, a report is written and no events file is.

### R6. The stream is complete and independent of the human flags

- The stream on stderr and in the file is the same, and the other flags do not filter it. `tool_call` events are emitted for every model tool call, not only under `--verbose`. `text` events are emitted for model prose, not only under `--show-text`. No event field is cut, abbreviated or dropped for size, and event content is not sanitised.
- `--verbose` and `--show-text` keep their meaning for the human progress only. `--show-text` still streams the prose live on stderr in the default mode, unless `--quiet` or `--emit-events` is set.
- `Progress` must stop using "the sink is active" as a proxy for "stderr carries JSONL". The sink is now nearly always active, so one explicit setting, `--emit-events`, decides the stderr behaviour:
  - `Begin` (the spinner and "(duration)" lines) is silent only under `--emit-events` or `--quiet`.
  - `Raw` writes live prose when `--show-text` is set and neither `--emit-events` nor `--quiet` is. It never emits an event.
  - `Step` and `Detail` already behave this way.
  
  A run with no `--emit-events` produces the same stderr bytes as before this change, apart from what R5 and the removed flags change.

### R7. `text` is one event per model turn

`text` carries `phase`, `turn` and `text`. `delta` is gone. The Runner accumulates the prose of a turn, always and not only under `--show-text`. Consecutive text blocks within a turn are joined with `"\n"`, as the live stream separates them, with no trailing newline added. When the turn ends it emits one `text` event immediately before that turn's `turn` event, with the same `turn` number. A turn with no prose emits no `text` event. Prose still buffered when a phase ends without a turn end (cancellation, a stream error) is emitted once before `phase_end`, with the `turn` number of the interrupted turn (the last completed turn plus one). `Observer` gains a method for this, and `Progress` implements it.

### R8. `tool_call` says what happened

`tool_call` keeps `phase`, `name`, `blocked` and gains:

- `arguments`: the arguments the model sent for the call, as JSON, verbatim apart from insignificant whitespace. If the model's input is empty it is `{}`. If it is not valid JSON, it is the raw text as a JSON string, so that the event is still valid and the whole line is never lost.
- `ok`: true when the call's result was not an error result. A call the shell guard refused has `ok` false. This is not the same as the program's exit status.
- `exit_code`: present only for a call that ran a program (the shell tools `execute`, `run_command`, `powershell` as named in `guard.go`) and whose exit status could be read from the call's result. It is absent for a refused call, for a non-shell tool, and when the status cannot be read from the result. A value is never guessed or defaulted to 0.
- `error`: present only when `ok` is false. It is the text of the error result the call returned, and for a refused call it is the guard's message.

The Runner emits it once per call, when the result arrives, as today. Arguments come from the `ToolCallEndEvent` of the same call. They are matched to the result by tool-call id and not by tool name, because calls can run in parallel. The `Observer.ToolCall` signature carries a struct with these fields, and the `Verbose()` check in `Runner.toolCall` is removed. `ToolCallEvent` gains `omitempty` only on `exit_code` and `error`. The comment in `events.go` about the single `omitempty` is updated. The call's result content is never recorded.

### R9. Tests do not touch the real state directory

- A small helper package, `internal/statetest`, provides one function used from `TestMain` in `internal/toolio`, `cmd/fix`, `cmd/impl`, `cmd/issue` and `cmd/spec`: the existing ones are extended, and the three missing ones are created. It does three things:
  - It sets `XDG_STATE_HOME` to a directory it creates (`os.MkdirTemp`).
  - It runs `m.Run()`.
  - It removes the directory and then compares the listing of the real default state directories against a listing taken before the run.
- The real directories are `$HOME/.local/state/agent-fox` resolved with `XDG_STATE_HOME` ignored, plus the developer's own `$XDG_STATE_HOME/agent-fox` if one was set. Any new file under `runs/` or `events/` makes the package's test run fail with a message naming the files. When no home directory can be resolved, the comparison is skipped.
- Tests that assert on a directory's contents keep their own `t.Setenv("XDG_STATE_HOME", t.TempDir())`. Tests that deliberately unset it (`TestTS06_7`) keep pointing `HOME` at a temp dir.
- Tests that start a built binary inherit the isolated environment. Those that build their own `cmd.Env` already set `XDG_STATE_HOME`.

### R10. Interface version, existing tests, documentation

- `text.delta` and the `--events`/`--events-file` flags are removed, so the interface version rises to `3.0.0` (`SchemaVersion`), per PRD 03's compatibility rule. The envelope-version changelog in `docs/cli.md` gets a `3.0.0` entry listing what changed. Each `cmd/*/testdata/schema.golden.json` is regenerated and reviewed.
- Existing tests that use the removed flags, the pid file name, the `delta` field, the `--verbose` gating of `tool_call` or the "sink only when requested" behaviour are rewritten to the new behaviour. They include those in `internal/toolio` (`app_test.go`, `events_test.go`, `heartbeat_test.go`, `progress_test.go`, `flagschema_test.go`, `schemaflag_test.go`, `smoke_test.go`, `report_test.go`), `internal/agentrun/hooks_test.go`, `cmd/fix/preflight_detail_test.go`, `docs_events_test.go` and `docs_test.go`. Byte-comparison tests that normalise run-specific fields (`TS-06-13`) also drop `session_id`, which differs between two runs.
- Documentation changes in the same change, per `.specs/steering.md`:
  - `docs/cli.md`:
    - the Shared flags rows for `--events`/`--events-file`, replaced by an `--emit-events` row, and the `--report-file` row's default name;
    - the "Machine-readable progress" section: the header with `session_id`, the `run_end` row, `tool_call`, `text`, the removal of the `--verbose`/`--show-text` gates, the usage-error paragraph and the worked example;
    - the `--schema` paragraph that names the removed flags;
    - the warnings table (`events_file_not_written`) and the artifact kinds (`events_file`);
    - the `3.0.0` changelog entry.
  - `docs/configuration.md`: "Report files" becomes "State directory" (and `docs_test.go`'s heading assertion changes with it). It covers both subdirectories, the new names, the `0700`/`0600` modes, and the `XDG_STATE_HOME` row saying tests set it.
  - `docs/model-usage.md` line 46: the way to watch spend now says `--emit-events` or the events file.
  - `docs/prds/README.md` already lists PRD 05.

## Design Decisions

1. **The session id is generated in `NewRun`, 128 bits from `crypto/rand`, hex-encoded.** The input asks for this. It needs no dependency and is unique across hosts and reboots, which the pid is not. With Go 1.26, `rand.Read` does not return an error, so `NewRun` keeps its signature.
2. **Where the events file is opened stays where the sink is built today, so "exactly the runs that have a report" in the input is narrowed.** Literally, every usage failure before the sink (parse failure, bad `--detail`, bare off-terminal) writes a report today. The input's own list excludes flag-parse failures and asks for no empty files, and the docs already say such runs emit no events. The rule is therefore "every run that reaches the sink gets an events file". A report whose run had no events file simply has no `events_file` artifact.
3. **`events_file_not_written` is recorded after the sink is attached to the run.** It is then also visible as a `warning` event on stderr under `--emit-events` and in the envelope. The file itself cannot carry it, because it does not exist.
4. **`--report-file` does not move the events file.** The input defines one events location, and a caller who gives an explicit report path still finds events by `ls events/*-<session_id>.jsonl`. The stem is the report's default stem.
5. **Only `events/` is `0700`; its parents stay `0755`.** `MkdirAll(…, 0700)` would also create `agent-fox/` as `0700` when `events/` is the first thing created. That would make `runs/` unreadable to other users by accident.
6. **The human-progress code must be re-keyed to `--emit-events`.** `Progress` treats "an active sink" as "JSONL on stderr" in `Begin`, `Raw` and `ToolCall`. With an always-active sink these would silently remove the spinner and live prose from the default mode. R6 makes the dependency explicit, because it is the likeliest regression.
7. **`text` is flushed once before `phase_end` if a phase ends mid-turn.** The input says the stream is complete and `text` is emitted at turn end, but is silent on an interrupted turn. Losing the last prose of a failing phase would defeat the purpose of a post-mortem record.
8. **Text blocks within a turn are joined with `"\n"`.** This reproduces what the live stream prints, since `TextEndEvent` produces a newline there.
9. **`ok` means "the call's result was not an error result", independent of `exit_code`.** The input leaves open whether a shell call with a non-zero exit is "successful". Which of the two the shell tool reports as an error is a property of the SDK, so `ok` follows the SDK's error flag and `exit_code` reports the status.
10. **`exit_code` is omitted when it cannot be read, never defaulted to 0.** The shell tool's result format is in a module outside this repository and could not be read (see Verified External API). A wrong 0 in a post-mortem record is worse than a missing field.
11. **`arguments` that are not valid JSON become a JSON string.** `json.RawMessage` with invalid content makes `json.Marshal` fail, and the sink currently drops a line it cannot marshal. A model that sends malformed arguments is exactly the case worth recording.
12. **Calls are matched to their arguments by tool-call id.** The loop runs tool calls concurrently (`blockCounter` already exists for that reason), so matching by name would mix up two calls to the same tool.
13. **The interface version goes to `3.0.0`.** PRD 03 makes removal of a field a major bump, and `text.delta` is removed. The envelope and event additions on their own would be minor.
14. **The "nothing created under the real directory" check is a before/after listing in `TestMain`, in one shared helper.** The input's wording (resolve the path in a test that only inspects it) cannot observe what other tests did. A listing taken around `m.Run()` can. It is a shared package so that five `TestMain`s do not diverge, with `internal/schematest` as precedent.
15. **A removed flag is reported from the existing parse-failure hook.** That gives the same exit code and envelope shape as other wrong flags and needs no second code path. Only the first unknown flag is named, because `flag.FlagSet.Parse` stops there.
16. **Heartbeat, `--quiet`, `--dry-run`, `--preflight` and the two-stream identity needed no new decision.** They follow the existing behaviour. `--quiet` leaves the file alone, `--dry-run` still writes it, and the stderr stream and the file are fed by the same sink.

## Dependencies

| Spec | Relationship | Why |
|---|---|---|
| `07_progress_event_stream` | modifies | Owns `--events`, `--events-file`, the event types, the sink and heartbeat. Its flags are replaced and `text`, `tool_call` and the header change. |
| `06_trim_and_chain_results` | modifies | Owns the report file, `DefaultReportPath`, the `artifacts` kinds and `report_file_not_written`. The file name changes and an artifact kind is added. |
| `09_tool_self_schema` | modifies | Owns `--schema`, the golden files and `schema_version`. Flags, envelope properties, the artifact enum and the version change. |
| `05_envelope_decidable` | depends on | The envelope fields and `run_end.status`/`exit_code` derivation that `session_id` and `run_end.report_file` sit beside. |
| `08_envelope_output_file` | depends on | `--output` validation ordering and the `output_matches_report_file` rule must stay as they are. |
| `10_untrusted_text_labels` | depends on | `arguments` and `text` carry unsanitised model text, as the stream already does. The result-field labelling is untouched. |
| `11_preflight_checks` | depends on | `--preflight` runs reach the sink and therefore write an events file. |

## Verified External API

The only external package involved is `github.com/agentfox/agentkit-go`, which `go.mod` replaces with `../agentkit-go`, outside this repository. Its source could not be read, so every signature below is **unverified** and is taken from existing call sites and tests in this repository.

| Symbol | Used as in the repo | Status |
|---|---|---|
| `core.ToolCallEndEvent{Block core.ToolUseBlock}` | `phase.go` reads `v.Block.Name` and `string(v.Block.Input)`; `hooks_test.go` builds `core.ToolUseBlock{Name: …}` | unverified; the type of `Input` is a byte slice or string, since `string(...)` compiles |
| `core.ToolUseBlock.ID` (the tool-call id) | not used anywhere in the repo | unverified, assumed; needed by R8. If it is named differently, the spec must use whatever the SDK provides to pair a result with its call |
| `core.ToolResultEvent{Message core.ToolResultMessage}` with `ToolName`, `IsError`, `Content` (`Content.Text()`) | `phase.go`, `hooks_test.go` | unverified, as used |
| `core.ToolResultMessage.ToolCallID` | not used in the repo | unverified, assumed, same caveat as `ToolUseBlock.ID` |
| `core.ToolExecutionEndEvent{Name, IsError, ElapsedMS}` | `phase.go`, `hooks_test.go` | unverified, as used. Whether it or the result carries a shell exit status is **not known** (R8, decision 10) |
| `core.TextDeltaEvent{Delta}`, `core.TextEndEvent`, `core.TurnEndEvent{Usage}` | `phase.go` | unverified, as used. The ordering assumed is that a turn's text events precede its `TurnEndEvent` |

Standard library: `crypto/rand.Read`, `encoding/hex.EncodeToString`, `os.OpenFile` with `O_EXCL|O_APPEND`, `os.MkdirAll`, `os.UserHomeDir`, `encoding/json.RawMessage`.
