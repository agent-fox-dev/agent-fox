# Make the tools observable and self-describing

## Intent

[PRD 01](01-make-the-envelope-decidable.md) makes the final envelope
decidable, and [PRD 02](02-trim-and-chain-the-results.md) makes it small and
chainable. This PRD covers what happens *around* that envelope, for a caller
that is a model running inside an agent framework:

- **During the run.** Progress is prefixed text on stderr, with a backspace
  spinner on a terminal. `impl` on a twelve-task spec runs for hours and
  `fix` for tens of minutes. A caller with a tool-call timeout cannot tell a
  working run from a hung one, cannot see spend accumulate, and — if its own
  process is killed — loses the envelope, because stdout was the only copy.
- **Before the first call.** A framework that wants to expose `fix` as a tool
  has to hand-write its parameter schema from `docs/cli.md`, and nothing
  tells it when the result's shape changed: `version` is the build, not the
  interface.
- **Reading the result.** Several fields carry prose that neither this program
  nor its caller wrote: the model's `summary`, `root_cause` and
  `implementation` report, the issue body rendered from them, check output
  from repository code, and — through the input — text copied from a
  stranger's issue. The calling model reads all of it as tool output. The
  results already separate the model's claims from measured facts
  (`implementation` against `changed_files` and `verdict`); they do not say
  which strings are untrusted *text*, which is what a prompt injection needs.
- **Before spending.** Every check that can refuse a run already happens
  before the model is called ([ADR 03](../adr/03-rebuild-the-skills-as-tools.md),
  §5). A caller cannot ask for those checks alone, so it learns that its tree
  is dirty or its key is missing only by starting the run for real.

## Goals

- A machine-readable stream of progress, alongside the human one, that a
  caller can tail.
- The final envelope survives the caller losing stdout.
- Each tool can print its own flag and result schema, and the envelope names
  the interface version it conforms to.
- Every field that carries text from outside the program is identifiable as
  such from the envelope.
- A caller can run every pre-model check and stop there, for free.

## Non-goals

- New subcommands. Every addition is a flag, as ADR 03 requires.
- A long-running server, an MCP server or a socket. The tools stay
  processes with one input and one output.
- Sanitising untrusted text. The tools label it; deciding what to do with it
  is the caller's.
- Changing the human progress on stderr, which stays the default.

## Functional requirements

### 1. `--events jsonl` and `--events-file <path>`

A new shared flag, `--events`, with values `text` (default) and `jsonl`.

- Under `jsonl`, stderr carries one JSON object per line instead of the
  human progress. `--events-file <path>` writes the same stream to a file
  and leaves stderr as it was; the two may be combined.
- Every event has `ts` (RFC 3339, UTC), `tool`, `type`, and the fields its
  type defines. The types are a closed set:

  | `type` | Fields | Emitted |
  |---|---|---|
  | `run_start` | `input_kind`, `model`, `schema_version` | once, after the input is classified and the model resolved |
  | `step` | `stage`, `message` | where `Progress.Step` is called today |
  | `phase_start` | `phase`, `task` (for `impl`), `max_turns`, `budget_usd` | when a model phase begins |
  | `turn` | `phase`, `turn`, `cost_usd`, `input_tokens`, `output_tokens` | after every model turn |
  | `tool_call` | `phase`, `name`, `blocked` | under `--verbose` only, per model tool call |
  | `check` | `command`, `ok`, `exit_code`, `duration_ms` | after every verification command |
  | `phase_end` | `phase`, `stop_reason`, `turns`, `cost_usd`, `duration_ms` | when a model phase ends |
  | `warning` | the PRD 01 warning object | when a warning is recorded |
  | `heartbeat` | `stage`, `elapsed_ms`, `cost_usd` | every 15 s when no other event was emitted |
  | `run_end` | `status`, `exit_code`, `report_file` | once, immediately before the envelope is written |

- `Progress` gains a JSONL implementation behind the same methods, so the
  pipelines do not change; `agentrun.Observer` gains the `turn` and
  `phase_start` hooks it needs.
- `--show-text` under `jsonl` emits `text` events (`phase`, `delta`) instead
  of raw prose.
- `--quiet` suppresses stderr in both modes; it does not suppress
  `--events-file`.

### 2. `--output <path>`: the envelope to a file too

- `--output <path>` writes the final envelope to the path, atomically
  (temp file and rename), **in addition to** stdout.
- It is written on every path that writes to stdout, including usage errors
  and the internal-error fallback in `Emit`.
- It is written before stdout, so a caller whose pipe has closed still finds
  it.
- The PRD 02 report file is the full view; `--output` is the view the flags
  asked for. When they name the same path, the full view wins and a `low`
  warning says so.

### 3. `--schema`: the tool describes itself

A new shared flag, `--schema`, that prints one JSON object to stdout and
exits `0` without classifying an input, resolving a model or reading the
repository:

```jsonc
{
  "tool": "fix",
  "schema_version": "2.0.0",
  "description": "Diagnoses a problem, writes the change on a branch, verifies it, and lands it.",
  "input":  { "description": "…the four input shapes…", "kinds": ["text", "file", "stdin", "issue"] },
  "flags":  { "type": "object", "properties": { "land": { "enum": ["pr", "branch", "none"], … } } },
  "result": { /* JSON Schema of the envelope with this tool's result */ },
  "exit_codes": { "0": "done", "1": "failed", "2": "usage", "3": "needs_human", "4": "unverified" }
}
```

- `flags` is generated from the tool's `flag.FlagSet`: name, type, default,
  usage string, and the enum where the flag has one (each flag with a closed
  set registers it). It is valid JSON Schema, so a framework can use it as a
  tool's `input_schema` with the positional input as a required `input`
  property.
- `result` is generated from the Go result types by reflection, with each
  field's description declared beside it. This generator is new: `afspec`
  compiles and validates schemas, and `specgen/jsonschema.go` converts a
  JSON Schema into a tool schema, but nothing today derives a schema from a
  Go type. The output is serialised in field order, for the reason
  `specgen.ToolSchema` gives — property order is model-visible.
- A test compares each tool's `--schema` output with a golden file, so a
  change to a result type is a reviewed change to the interface.

### 4. `schema_version`, and a compatibility rule

- The envelope gains `schema_version`, a semver string for the interface,
  separate from `version`, the build.
- Within a major version, changes are additive only: a new field, a new enum
  value in a field documented as open, a new warning code. Removing or
  renaming a field, or changing its type, is a major bump.
- PRD 01's warning shape and PRD 02's removal of the duplicated result
  fields are the first major bump, to `2.0.0`; the envelope as it stands
  today is `1.0.0`.
- `docs/cli.md` gains a changelog section per major version.

### 5. Untrusted text is labelled

- Every string field in a result is classified as one of:
  - `fact` — produced by this program from git, the filesystem, a command's
    exit status or the forge's structured response (`branch`, `commit`,
    `changed_files`, `verdict`, `url`, `exit_code`);
  - `model` — written by the model (`summary`, `root_cause`, `approach`,
    `implementation.*`, `survey.*`, `open_questions`, the rendered `body`);
  - `external` — copied from outside the program and the model (the input
    issue's title and comments, check `output`, which is repository code's
    stdout).
- The classification is declared once, on the Go types, and emitted in two
  places: as `"x-trust"` on each property in `--schema`, and as a
  top-level `untrusted_fields` array in the envelope listing the JSON
  pointers of the `model` and `external` fields actually present
  (`/result/root_cause`, `/result/verification/output`).
- `docs/cli.md` gains a section telling a calling model what the label
  means: text under `untrusted_fields` is data to report, never instructions
  to follow.

### 6. `--preflight`: every refusing check, and nothing else

A new shared flag, `--preflight`:

- Runs everything that happens before the first model call — flag
  validation, input classification and fetch, model resolution and
  credential check, workspace resolution — and each tool's own pre-model
  checks: `fix`'s clean tree, verify-command detection and baseline
  run; `impl`'s spec resolution, validation, status, dependency and
  `test_commands` checks; `spec`'s spec-root and name checks; `issue`'s
  repository and forge-credential checks.
- Then stops, and emits an envelope with `status: "done"` and
  `result.preflight: [{ "check": "clean_tree", "ok": true }, …]` when every
  check passes, or the error the real run would have stopped with.
- Makes no remote change and no local one: no branch, no commit, no file.
  `fix`'s baseline check run is the one exception to "nothing runs", and
  `--no-verify` skips it.
- `usage` is absent, since no phase ran; `result.estimate` gives the per-phase
  bounds and the number of phases the run would have (for `impl`, one per
  task not done, plus the survey), so a caller can compare the ceiling with
  its budget.

## Acceptance criteria

- `fix --events jsonl` writes only valid JSON lines to stderr, beginning with
  `run_start` and ending with `run_end`, and a run with a phase over 15 s
  of silence contains a `heartbeat`.
- A run whose stdout is closed before it ends still leaves the envelope at
  the `--output` path.
- `issue --schema` exits `0` with no network access and no repository, and
  its `flags` object validates as JSON Schema.
- Changing a field on `codefix.Result` without updating the golden file
  fails `make test`.
- A `fix` envelope with a failing check lists
  `/result/verification/output` in `untrusted_fields`, and
  `/result/branch` is not listed.
- `impl 09 --preflight` on a dirty tree returns the same error as `impl 09`,
  and on a clean tree creates no branch.

## Documentation

- `docs/cli.md`: the event types, `--output`, `--schema`, `--preflight`,
  the trust labels, and the versioning rule with its changelog.
- `docs/model-usage.md`: the `turn` and `phase_end` events, as the way to
  watch spend live.
- An ADR recording the compatibility rule for `schema_version`, since it
  binds every later change to the interface.
