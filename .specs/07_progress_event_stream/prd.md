---
spec_id: "07"
spec_name: "progress_event_stream"
title: "A machine-readable progress stream (`--events`, `--events-file`)"
status: "active"
created_at: "2026-09-30T09:50:37.741412Z"
updated_at: "2026-09-30T09:50:37.741412Z"
intent_hash: "debadd840f3f02bc753187ce5beac5f6253718daa89f11c751fa219e07677422"
schema_version: 2
source: "docs/prds/03-make-the-tools-observable-and-self-describing.md"
---
## Intent

`spec`, `issue`, `fix` and `impl` report progress as prefixed text on stderr,
meant for a person watching a terminal. A caller that is itself a program —
in particular a model running inside an agent framework, holding one of
these tools by a timeout — cannot tell a `fix` running for tens of minutes
or an `impl` running for hours from a hung process, and cannot see spend
accumulate as the run goes. This spec gives that caller a second, parseable
copy of the same progress: one JSON object per line, on the same stderr
stream when asked for, or duplicated to a file, built from a closed,
documented set of event types so a caller can write one parser against it
once. It changes nothing about what any tool does or about the default
human-readable progress a person watching a terminal sees.

## Goals

- A caller can ask a tool to emit its progress as one JSON object per line
  instead of human-readable text (`--events jsonl`), and can additionally —
  or instead — have that same stream written to a file
  (`--events-file <path>`) while stderr keeps its default, human form.
- The event stream lets a caller watch spend accumulate live, per model turn
  and per phase, without waiting for the run to finish.
- A caller that tails the stream can tell a working run from a hung one: a
  run produces at least one event every 15 seconds even when nothing else
  happened.
- The set of event types is closed and documented, so a caller writes one
  parser against it rather than against whatever string a tool happens to
  print this month.

## Non-goals

- New subcommands: this is a shared flag on the four existing tools, per
  [ADR 03](../adr/03-rebuild-the-skills-as-tools.md).
- A long-running server, an MCP server or a socket. Each tool is still one
  process with one input and one output; the event stream is a second
  *stream*, not a second channel of control.
- Changing the human-readable progress on stderr, which stays the default
  (`--events text`) and stays byte-for-byte what it is today.
- Persisting the final envelope to a file, self-describing flag/result
  schemas, an interface version field, and labelling which envelope strings
  are untrusted text — later scopes of this same split:
  `envelope_output_file`, `tool_self_schema` and `untrusted_text_labels`.
- Running the tools' pre-model checks alone, for free — `preflight_checks`,
  the split's last scope.
- Sanitising anything a run emits, machine-readable or not; that stays the
  caller's decision, unaffected by this spec.

## Background

Every tool shares one shell, `internal/toolio` (`App`, `Run`, `Envelope`,
built per ADR 03). Progress today is `*toolio.Progress`
(`internal/toolio/progress.go`): `Step` prints one line, `Detail` prints one
line under `--verbose`, `Raw` streams the model's own prose under
`--show-text`, and `Begin`/the returned closure print a labelled span with a
backspace spinner on a terminal. `Progress` implements
`agentrun.Observer` (`Detail(format, args)`, `Raw(s)`), which
`internal/agentrun/phase.go`'s `Runner.Run` drives while streaming a
phase's `core.Event`s (`Runner.trace`): today it reads
`ToolCallEndEvent`, `ToolExecutionEndEvent`, `ToolResultEvent`,
`TextDeltaEvent`, `TextEndEvent` and `ErrorEvent`, and reports a finished
phase as `agentrun.Result{Turns, StopReason, Usage, Elapsed, Blocked}`. A
phase's cost and tokens are visible today only once it ends, folded into the
envelope's `usage.phases[]` by each pipeline's own call to `toolio.Run.AddPhase`
(e.g. `codeimpl.recordPhase`, `codefix`'s equivalent). Warnings accumulate on
`*toolio.Run` via `Warn(format, args...)`, unrelated to `Progress`.

A verification command is run through `checks.Run` (`internal/checks/checks.go`),
wrapped at exactly two call sites: `codefix.runChecks` (baseline and
post-change) and `codeimpl.runGate` (before and after every task, and the
repair loop). Both already call `o.Progress.Begin(...)` around the run and
have the resulting `checks.Result` in hand immediately after.

A phase's shell calls are authorized by `agentrun.Guard`
(`internal/agentrun/guard.go`), whose `GuardOptions.OnBlock(string)` fires on
a refusal; every tool call, blocked or not, already passes through
`Runner.trace`'s `ToolCallEndEvent`/`ToolExecutionEndEvent` handling, gated
today by `--verbose` the same way `Detail` is.

`agentrun.Phase.Name` is the cache key across a whole run for a given phase
(`docs/model-usage.md`: "`impl`'s three phase names are the same for every
task and every run … so `impl/implement` is one cache key across a whole
spec rather than one per task"), so a per-task label for `impl`'s
`implement` phase is not derivable from `Phase.Name` and needs its own,
separate field.

None of `--events`, `--events-file`, or any event type exists in the tree
today; this spec adds all of it.

## Requirements

### 1. `--events text|jsonl` and `--events-file <path>`

Two new flags, registered on `Common` (`internal/toolio/cli.go`) alongside
the rest of the shared flags:

- `--events` takes `text` (default) or `jsonl`; any other value is a usage
  error (`exit 2`), validated in `App.Main` right after flag parsing —
  before `--version`, the bare-invocation check, or anything is fetched —
  the same place a caller learns of any other malformed flag combination.
- `--events-file <path>` names a file to receive the JSONL event stream
  (opened once, truncated, each line flushed as it is written so a killed
  process keeps whatever was already emitted). It is independent of
  `--events`: the file always carries the JSONL stream when given, whatever
  `--events` says about stderr, so `fix --events text --events-file run.jsonl`
  is a legal, useful combination (a human watches the terminal; a
  supervisor tails the file).
- `--quiet` suppresses stderr under both `--events` values, exactly as it
  does today for the human form; it has no effect on `--events-file`.

### 2. The event envelope and the closed set of types

Every event is one JSON object with `ts` (RFC 3339, UTC — the same
`time.Now().UTC().Format(time.RFC3339)` convention `Envelope.StartedAt`
already uses), `tool` (the program name, as `Envelope.Tool` already is),
`type`, and the fields below. `type` is a closed, documented set; nothing
outside it is ever emitted, and a field not listed for a type is never
present on it (no stray `omitempty` leftovers from a shared struct):

| `type` | Fields | Emitted |
|---|---|---|
| `run_start` | `input_kind`, `model` | once, after the input is classified and the model resolved |
| `step` | `stage`, `message` | where `Progress.Step` is called today |
| `phase_start` | `phase`, `task` (present only for `impl`'s per-task phase), `max_turns`, `budget_usd` | when a model phase begins |
| `turn` | `phase`, `turn`, `cost_usd`, `input_tokens`, `output_tokens` | after every model turn |
| `tool_call` | `phase`, `name`, `blocked` | under `--verbose` only, per model tool call |
| `check` | `command`, `ok`, `exit_code`, `duration_ms` | after every verification command |
| `phase_end` | `phase`, `stop_reason`, `turns`, `cost_usd`, `duration_ms` | when a model phase ends |
| `warning` | `code`, `severity`, `stage`, `message` — `05_envelope_decidable`'s warning object | when a warning is recorded |
| `heartbeat` | `stage`, `elapsed_ms`, `cost_usd` | every 15 s when no other event was emitted |
| `run_end` | `status`, `exit_code` | once, immediately before the envelope is written |

`run_start.model` is the same triple `Envelope.Model` already carries
(`spec`, `id`, `vendor`); `run_end.status` is `05_envelope_decidable`'s
envelope `status` field, read off the same exit code that decides it there.
`run_start.schema_version` and `run_end.report_file`, named in the table
this split's PRD started from, are added by `tool_self_schema` and
`envelope_output_file` respectively, once those fields exist on the
envelope — additive fields on an existing event type, which the
compatibility rule `tool_self_schema` establishes is exactly the kind of
change that does not need a new major version.

### 3. `run_start` and `run_end`: wired directly in `toolio`

Both are emitted by the shared shell, not by a pipeline, so no tool can omit
one or get the order wrong:

- `run_start` fires in `App.execute`, right after `e.run.SetModel(...)` and
  `e.progress.Detail("model: …")` today — the same point the input is known
  and the model is resolved, before `agentrun.Config`/`Runner` are built.
- `run_end` fires in `App.Main`, immediately before `Emit(stdout,
  run.Envelope(...))` is called — after the exit code and result are known,
  before a byte of the envelope is written.
- Neither fires on the two purely human-driven paths (`-h`/`--help`, a bare
  invocation on a terminal) that already write nothing to stdout: there is
  no run to report on.

### 4. `Progress` gains a JSONL sink behind its existing methods

`Progress`'s existing methods keep their call sites unchanged in every
pipeline; what each one *does* changes under `--events jsonl` or
`--events-file`:

- `Step` gains a leading `stage string` parameter (every call site across
  `internal/toolio`, `codefix`, `codeimpl`, `specgen` and `issuetriage`
  updated to pass the same stage vocabulary `Result.Stage`/`error.Stage`
  already use). The printed human line is unchanged
  (`[tool] message`, stage not shown); under an active JSONL sink it also
  emits a `step` event carrying `stage` and the formatted `message`.
- `Begin` is unchanged on a terminal or under `--verbose` text output. Under
  an active JSONL sink it prints nothing and returns a no-op end function —
  the same shape `Begin` already returns under `--quiet` — because the
  spans it wraps (a model phase, a check) already have precise events
  (`phase_start`/`phase_end`, `check`); a third, unstructured rendering of
  the same interval would only be redundant.
- `Detail` is unaffected: it has no event counterpart (the closed set
  deliberately does not carry every `--verbose` trace line), so under an
  active JSONL sink it prints nothing and nothing is lost from the envelope
  or the report — only from a stream nothing here promises to carry it on.
- `Raw` is unaffected under `--events text`. Under an active JSONL sink and
  `--show-text`, it emits a `text` event (`phase`, `delta`) instead of raw
  prose; without `--show-text` it already does nothing, unchanged.
- A new `Progress.Check(res checks.Result)` is called at the two existing
  choke points immediately after `checks.Run` returns — `codefix.runChecks`
  and `codeimpl.runGate` — turning `Result.Command/OK/ExitCode/DurationMS`
  directly into a `check` event; nothing about `checks.Result` changes.

### 5. `agentrun.Observer` gains the phase and turn hooks

`Observer` (`internal/agentrun/phase.go`) gains three methods beyond
`Detail`/`Raw`, all invoked by `Runner.Run` itself so no pipeline changes:

- `PhaseStart(phase, task string, maxTurns int, budgetUSD float64)`, called
  once at the top of `Runner.Run`, before the agent is built, with
  `r.cfg.Bounds.maxTurns()`/`.maxBudget()` — the ceilings the run actually
  resolved, not the flag defaults. `task` is empty for every phase except
  `impl`'s per-task `implement` phase; `agentrun.Phase` gains an optional
  `Task string` field for this alone, set only at `codeimpl`'s per-task
  phase construction (`codeimpl/phases.go`), because `Phase.Name` is
  deliberately the same cache key across every task and cannot double as
  the task label.
- `PhaseEnd(phase, stopReason string, turns int, costUSD float64,
  durationMS int64)`, called once as `Runner.Run` returns, from the same
  `agentrun.Result` it already assembles — a second read of facts the
  runner already has, not a new computation.
- `Turn(phase string, turn int, costUSD float64, inputTokens,
  outputTokens int64)`, called once per model turn from inside
  `Runner.trace`'s existing event loop. The turn boundary and the values
  available depend on what `agentkit-go`'s stream actually exposes per
  turn (see Design Decision 4 and the open question below); at minimum,
  `turn` is a monotonically increasing counter per phase and `phase` is
  always correct, because both come from `Runner.Run`'s own state rather
  than from the stream.
- `ToolCall(phase, name string, blocked bool)`: `Runner.trace`'s
  `ToolCallEndEvent` case calls it with `blocked: false`;
  `agentrun.Guard`'s `OnBlock` (already invoked with `counter.inc()` and
  `r.detail(...)`) is extended to also report the blocked call, correlated
  by tool name, with `blocked: true`. Emitted — as an event or as today's
  `Detail` line — only when `Progress.Verbose()` is true, unchanged from
  today's gating.

`Detail` and `Raw` keep their existing two-argument signatures; the three
new methods are additive to the interface, so every existing caller of
`agentrun.Config{Observer: someProgress}` keeps compiling once `Progress`
implements the wider interface.

### 6. `warning` events

`toolio.Run` gains a reference to the same `Progress` (or the narrower sink
interface it needs) set once in `App.Main` right after both are constructed,
so `Run.Warn` — after `05_envelope_decidable` lands and it carries `code`,
`severity` and `stage` — also emits a `warning` event through it. A `Run`
built without one attached (as today's tests already build bare `Run`
values) is unaffected: the call is a no-op the same way every method on a
nil `*Run` already is.

### 7. `heartbeat`

The JSONL sink runs one ticker for the run's lifetime once either
`--events jsonl` or `--events-file` is active: every second, it checks
whether 15 seconds have passed since the last event of any type was
emitted (`run_start` counts; a `heartbeat` counts as the reset for the next
one), and if so emits `heartbeat` with the last `stage` a `step` or
`phase_start` event named, `elapsed_ms` since the run started, and the run's
accumulated `cost_usd` so far — read off the same running total
`toolio.Run` maintains for `usage.phases[]`, not a second computation. The
ticker stops once `run_end` is emitted.

### 8. Documentation

- `docs/cli.md` gains an "event types" section: the flag table entries for
  `--events`/`--events-file`, the ten-row type table above, and a worked
  multi-line example.
- `docs/model-usage.md` gains a short section pointing at `turn` and
  `phase_end` as the way to watch spend live, next to the existing
  `usage.phases[]` paragraph.

## Design Decisions

1. **`--events-file` always carries the JSONL stream, independent of what
   `--events` does to stderr.** The two answer different questions — "what
   does a person watching the terminal see" and "what does a supervisor
   tail" — and forcing them to agree would make `--events-file` useless
   under the (human) default.
2. **`Step` gains a leading `stage` parameter rather than deriving `stage`
   from context.** `Result.Stage`/`error.Stage` already name every point in
   a pipeline in the same vocabulary; passing it explicitly at each call
   site keeps the `step` event's `stage` consistent with the envelope's own
   error stage by construction, rather than inventing a second "current
   stage" tracker that could drift from it. The printed human line is
   unchanged, so this is invisible under the default `--events text`.
3. **`Begin` and `Detail` have no event equivalents.** `Begin` wraps
   intervals (a phase, a check) that already get a precise event pair
   elsewhere; `Detail` is `--verbose` tracing prose, not data a caller
   parses. The closed type set in the input this spec was built from lists
   nine (ten, with `text`) types deliberately, and adding one for "whatever
   `Detail` happened to print" would reopen it.
4. **`Turn`'s exact boundary and the availability of `cost_usd`/token
   fields depend on what `agentkit-go`'s event stream exposes per model
   turn, which this PRD could not verify from installed source** (see
   `## Verified External API`). The hook is specified so that at worst it
   degrades to a turn counter with zeroed cost/token fields, never to a
   requirement that cannot be met at all; the implementer confirms the
   real per-turn usage event against the vendored SDK, which is reachable
   from the actual working tree even though it was not reachable here.
5. **`agentrun.Phase` gains an optional `Task` field rather than
   `PhaseStart` deriving the task from `Phase.Name`.** `Phase.Name` is
   deliberately the stable cache key across every task of an `impl` run
   (`docs/model-usage.md`); overloading it with a per-task suffix would
   fragment the very cache the shared name exists to protect.
6. **`toolio.Run` holds a reference to the events sink, set once at
   construction in `App.Main`, rather than `Progress` reaching into
   `Run`'s warnings.** `Run` already owns the one place a warning is
   recorded (`Warn`); giving it the sink to call keeps the emission a
   single, unavoidable side effect of recording the warning, the same
   guarantee `05_envelope_decidable` relies on for the envelope's own
   `warnings` array.
7. **The heartbeat reads `Run`'s accumulated cost rather than recomputing
   it.** `toolio.Run` already sums `PhaseInfo.CostUSD` for `usage.phases[]`;
   a heartbeat is idle-run telemetry, not a place to introduce a second
   source of truth for spend.
8. **`schema_version` on `run_start` and `report_file` on `run_end` are
   deferred to the scopes that define those envelope fields**
   (`tool_self_schema`, `envelope_output_file`), added as new fields on an
   existing event type once they exist — the additive-only shape this
   split's own compatibility rule (from `tool_self_schema`) is meant to
   cover, demonstrated on the first two fields it applies to.

## Dependencies

| Spec | Reason |
|---|---|
| `05_envelope_decidable` | Supplies the `Warning{code, severity, stage, message}` shape the `warning` event mirrors verbatim, and the envelope's `status` vocabulary `run_end.status` is read from. |
| `06_trim_and_chain_results` | Not read from directly by this scope, but `run_end`'s eventual `report_file` field (added by `envelope_output_file`, this split's second scope) names the report file that spec defines; noted here because this scope's `run_end` type is the one a later scope extends. |

## Verified External API

This spec's event hooks sit inside `internal/agentrun` and call into
`github.com/agentfox/agentkit-go`'s `core` package, which is a sibling Go
module (`replace … => ../agentkit-go` in `go.mod`) outside this session's
readable root — it could not be opened to confirm symbols beyond what
`internal/agentrun/phase.go` already uses in this repository. Confirmed
from that usage:

- `core.Event` — an interface `Runner.trace` switches on by concrete type:
  `core.ToolCallEndEvent{Block.Name}`, `core.ToolExecutionEndEvent{Name,
  IsError, ElapsedMS}`, `core.ToolResultEvent{Message.IsError,
  Message.Content.Text()}`, `core.TextDeltaEvent{Delta}`,
  `core.TextEndEvent`, `core.ErrorEvent{Message}`.
- `core.RunResult{TurnCount int, StopReason core.RunStopReason, Usage
  core.Usage}`, returned by `stream.RunResult()`.
- `core.Usage{InputTokens, OutputTokens int64-compatible, CostUSD float64}`
  — field names confirmed from `codeimpl.recordPhase`'s
  `res.Usage.CostUSD`/`.InputTokens`/`.OutputTokens` and
  `toolio.FormatTokens(int64(res.Usage.InputTokens))`.
- `core.RunStopReason` values `core.RunStopMaxTurns`,
  `core.RunStopBudgetExceeded`, `core.RunStopAborted`,
  `core.RunStopToolTerminate`, `core.RunStopPolicy`.

**NOT FOUND (inaccessible, not merely absent):** a stream event carrying
per-turn usage (input/output tokens, cost) at a model-turn boundary. No such
type is referenced anywhere in this repository today, and the SDK's own
source could not be read from this session to check for one. The `Turn`
hook (Requirement 5) is written so it degrades gracefully — a turn counter
with zeroed cost/token fields — if no such event exists; confirming the
real name and wiring it precisely is left to the implementation, which runs
with full access to the vendored module.

## Open Questions
