---
spec_id: "05"
spec_name: "envelope_decidable"
title: "Make the envelope decidable in one read"
status: "active"
created_at: "2026-09-30T08:47:25.239247Z"
updated_at: "2026-09-30T08:47:25.239247Z"
intent_hash: "b1ee2bd0a8b068ea3edba305137beb9b09dd926734c53126d4d285da5b9e449b"
schema_version: 2
source: "docs/prds/01-make-the-envelope-decidable.md"
---
## Intent

`spec`, `issue`, `fix` and `impl` are driven by programs, and increasingly the
program is a model calling one of them as a tool and reading the JSON on
stdout to decide what to do next. This spec closes the gaps where that
caller today has to know the manual — `docs/cli.md`, the exit-code table —
to act on a result correctly. It does not change the one-input/one-JSON-object
shape [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) fixed: every change
here is a field added to, or reshaped within, the envelope `internal/toolio`
already assembles in `Run.Envelope`, so that a caller reading only the
envelope can tell what happened, whether it is done, and what to do next.

## Goals

- Every program-driven invocation writes one JSON object to stdout, including
  a bare invocation with no input, whenever stdout is not a terminal.
- The envelope carries a `status` field naming the outcome in words, derived
  one-to-one from the exit code, and a `summary` field giving one sentence
  of what happened, supplied by the tool.
- `fix`'s `ambiguity`, `impl`'s `blocker` and `spec`'s deferred question all
  surface through one shared `needs_human` shape, and a `--context` flag
  feeds an answer back into the next run without re-typing the input.
- Every `error` says whether re-running unchanged could succeed
  (`retryable`), whether re-running the same input continues rather than
  restarts (`resumable`), and, where a mechanical remedy exists, which flag
  or variable to change (`fix_hint`).
- Every warning carries a stable `code`, a `severity` (`high`/`low`) and a
  `stage`, replacing today's plain strings.

## Non-goals

- New subcommands, or any change to the one-positional-input rule or the
  exit-code table.
- Changing any tool's `result` schema beyond moving `fix`'s `ambiguity` and
  `impl`'s `blocker` into `needs_human` (they stay in `result` for one more
  release, then are removed).
- Trimming or restructuring `result` payloads, or giving the four tools
  chainable identifiers — that is
  [PRD 02, "Trim and chain the results"](../prds/02-trim-and-chain-the-results.md).
- A machine-readable progress stream, envelope persistence beyond stdout,
  self-describing flag/result schemas, an envelope schema-version field, or
  marking which envelope strings are untrusted text — that is
  [PRD 03, "Make the tools observable and self-describing"](../prds/03-make-the-tools-observable-and-self-describing.md).
  This spec's breaking change to `warnings` (string → object) ships ahead of
  that version field; there is no transition period where both shapes exist.
- Removing `ok`.

## Background

The four tools share one shell, `internal/toolio` (`App`, `Run`, `Envelope`),
built per [ADR 03](../adr/03-rebuild-the-skills-as-tools.md). `App.Main`
parses flags, classifies the single input (`toolio.Resolve`), resolves the
model, and calls the tool's `Exec`, which returns `(exit code, result,
*ErrorInfo)`; `Run.Envelope` turns that into the JSON object `Emit` writes to
stdout. Warnings accumulate on `*Run` via `Warn(format, args...)` into a
`[]string`. Each tool's own package (`codefix`, `codeimpl`, `specgen`,
`issuetriage`) defines its own `Result` struct and its own error categories
(`agentrun.CategoryOf`, plus package-level `Category*` constants), all
mapped onto the shared exit-code table by `toolio.ExitCodeFor`.

Three gaps exist today, all confirmed in the code:

- `App.Main` treats a bare invocation (`input == ""`) as purely human-driven:
  it calls `fs.Usage()` and returns `ExitUsage` without ever building or
  emitting an envelope. A caller piping stdout to a parser gets nothing to
  parse.
- `fix`'s `Result.Ambiguity` (`codefix/types.go`), `impl`'s `Result.Blocker`
  (`codeimpl/types.go`) and `spec`'s `Result.OpenQuestions` (`specgen/prd.go`)
  are three unrelated shapes for "a person has to decide something," each
  inside `result`, with no flag to answer any of them.
- `ErrorInfo` (`internal/toolio/envelope.go`) has only `stage`, `category`
  and `message`. `docs/cli.md` documents what each category means for
  retrying, but the envelope itself does not carry it, and `warnings` is
  `[]string` with no severity or code, so `Run.Warn` calls of very different
  weight (`the input was truncated at 262144 bytes` vs. `the pull request
  could not be opened`) are indistinguishable to a caller that does not read
  every message.

The terminal test this spec reuses (`isTerminal`, in `internal/toolio/progress.go`)
is already package-private to `toolio`, so `App.Main` (`internal/toolio/app.go`,
same package) can call it directly; no relocation is needed, only a second
call site.

## Requirements

### 1. A bare invocation emits an envelope when stdout is not a terminal

`App.Main` classifies the input before dispatching. When the positional
argument is empty **or all whitespace** — both are folded into one check,
made before `Resolve` is called, so no network or model resolution happens
either way — the tool still prints help text and the flag list to stderr
(unchanged). In addition:

- When stdout is not a terminal (`isTerminal(stdout)` is false — true for a
  file, a pipe, or the `bytes.Buffer` a test redirects it to), the tool also
  writes one envelope to stdout: `ok: false`, `exit_code: 2`, `status:
  "usage"`, `error.stage: "usage"`, `error.category: "usage"`, and
  `error.message` naming the four accepted input shapes (the same message
  `Resolve`'s `ErrNoInput` path already uses: "no input: give a report, a
  file path, a GitHub or GitLab issue URL, or - to read stdin").
- When stdout is a terminal, behaviour is unchanged: nothing on stdout.
- `-h`/`--help` and `--version` are unaffected on both: they exit before this
  check is reached and never emit an envelope.

### 2. `status` and `summary`

Two new top-level fields, always present (never `omitempty`):

- `status` is one of `done`, `failed`, `usage`, `needs_human`, `unverified`,
  set in `Run.Envelope` from the exit code passed in (`0`→`done`, `1`→
  `failed`, `2`→`usage`, `3`→`needs_human`, `4`→`unverified`) — the same
  table `toolio.ExitCodeFor` already produces exit codes from, read the
  other way. No tool sets it directly, so it cannot disagree with `ok` or
  `exit_code`.
- `summary` is one sentence, capped at 200 characters (truncated by
  `Run.Envelope` if a tool exceeds it), in the tool's own vocabulary. A
  `Result` type supplies it by implementing a one-method interface (e.g.
  `Summary() string`); `Run.Envelope` calls it via a type assertion on the
  `result` value it already receives, the same way it already special-cases
  a typed-nil result. When the assertion fails (the tool supplies none) the
  shell falls back to `error.message` when `ok` is false, and to `"<tool>:
  done"` when `ok` is true.

  Guidance per tool, from the fields each `Result` already has:
  - `fix`: mentions the commit and branch when one was made, the verdict,
    and whether it landed (`fix: committed a3f9c1e on
    fix/issue-42-nil-map; checks regressed; not landed`); when it stopped on
    an ambiguity, that it asked a question.
  - `impl`: mentions `tasks_done`/`tasks_total`, the branch, and the verdict
    of the task or repair that stopped it, when one did.
  - `issue`: mentions `action`, `url`/`number`, `severity` and the count of
    `affected_files` (`issue: filed acme/widgets#57 (high severity, 4 files
    cited)`).
  - `spec`: mentions the package(s) written and, when `open_questions` is
    non-empty, how many there are; for a split, how many of how many scopes.

  If any `warnings` entry with `severity: "high"` is present on a run whose
  `ok` is `true`, `Run.Envelope` appends a fixed, deterministic clause to
  whatever summary was produced (from the tool or the fallback) noting the
  count and that the highest severity is `high` — this is decided centrally,
  not left to each tool, so it cannot be forgotten at a call site.

- Field order in the emitted JSON becomes: `tool`, `version`, `ok`, `status`,
  `exit_code`, `summary`, `error`, `needs_human`, `warnings`, `result`,
  `input`, `usage`, `model`, `duration_ms`, `started_at` — the `Envelope`
  struct's field order is rewritten to match, since Go's `encoding/json`
  emits object keys in struct-field order. `usage.phases` stays the last
  field of `UsageInfo`, unchanged.

### 3. One `needs_human` shape, fed by `--context`

A new top-level field, an object present only when `status` is
`needs_human` (`omitempty` otherwise):

```jsonc
"needs_human": {
  "question": "Does 'retry' mean the HTTP client's retry or the job queue's?",
  "options": [
    { "id": "A", "text": "the HTTP client's retry loop in client.go" },
    { "id": "B", "text": "the job queue's redelivery in worker.go" }
  ],
  "needed": "",
  "stage": "analyse",
  "resume": "fix <same input> --context \"<answer>\""
}
```

- `question`, `options` (omitted when there are none) and `needed` (omitted
  when there are options) come from the tool's `Result`: `fix` from
  `Ambiguity{Question, InterpretationA, InterpretationB}` (the two
  interpretations become options `A`/`B`); `impl` from `Blocker{Reason,
  Needed}` (`Reason`→`question`, `Needed`→`needed`, no options). A blocker
  raised because an upstream spec is not sealed or done — today
  `codeimpl`'s `checkUpstream`, which returns a bare `*Failure` with no
  `Blocker` — is changed to also set `result.Blocker` with `Needed` naming
  the upstream package, so this path populates `needs_human` the same way
  every other blocker does.
- `stage` is `error.Stage` on the same envelope — the pipeline step that
  raised the question — so it is never a second, possibly-inconsistent copy.
- `resume` is a fixed template, not the actual input (which may be
  arbitrarily large text): `"<tool> <same input> --context \"<answer>\""`,
  telling the caller the invocation *shape*, not a literal re-runnable
  command.
- `spec` never sets `needs_human`: it does not stop to ask, and
  `result.open_questions` is unaffected; a run with open questions reports
  their count in `summary` (requirement 2).
- `result.ambiguity` (`fix`) and `result.blocker` (`impl`) stay where they
  are for one release, so a caller mid-migration has one cycle to move to
  `needs_human`, then are removed.

A new shared flag, `--context <text>`, repeatable, registered alongside the
rest of `Common`'s flags:

- Every value is rendered into one labelled block, `## Additional context
  from the caller`, one paragraph per `--context` in the order given.
- This block is appended to what a phase's prompt shows the model. It is
  **not** appended to `Input.Body` itself: `Body` stays exactly what
  `Resolve` produced, because `Body` is also what decides identity —
  `specgen`'s split-plan matching hashes it (`planInputOf`,
  `specgen/split.go`) and `codeimpl.Locate` parses it to find a spec
  package. Baking `--context` into `Body` would change a text/stdin input's
  hash between the run that stops and the run that answers, so `spec` would
  start a second copy of the first package instead of resuming, and would
  corrupt `impl`'s package reference entirely (`impl 09 --context "…"` must
  still resolve to spec `09`). `Input` therefore gains a second field
  carrying the rendered block (empty when no `--context` was given), which
  each pipeline appends to its own prompt construction, independently of
  `Body`.
- `input.context_bytes` in the envelope is this block's length. It counts
  toward the 256 KB input bound (`toolio.MaxInputBytes`) alongside `Body`;
  when `len(Body) + len(context block) > MaxInputBytes`, the run is refused
  as a usage error (`category: "usage"`, exit 2) before anything is fetched,
  not silently truncated the way an oversized `Body` alone is.

### 4. `retryable`, `resumable` and `fix_hint` on every error

`ErrorInfo` gains three fields, all computed in `toolio` rather than by each
pipeline, so the three cannot drift from the category table:

- `retryable` (bool): true only for `api` and `aborted`. Every other
  category — `usage`, `input`, `auth`, `model`, `invalid_spec`, `blocked`,
  `ambiguous`, `empty_change`, `internal`, and the ones with a `fix_hint`
  (`budget`, `max_turns`, `no_result`, `unverified`, `git`, `forge`,
  `disk`) — is false: none of them can be fixed by running the identical
  command again.
- `resumable` (bool): whether re-running the *same input* continues the
  work rather than starting it over. This is not derivable from category
  alone, since `fix` and `impl` both fail from the `unverified` category
  with a branch on disk, and only one of them means anything by
  "continuing":
  - `fix`: always false. `codefix` names its branch with
    `gitx.UniqueBranchName`, which is not deterministic — re-running with
    the same input creates a new branch, never returns to the old one.
  - `impl`: true whenever `Result.Branch` is non-empty. `codeimpl` names its
    branch deterministically (`impl/<NN>-<slug>`) and sets `Result.Branch`
    as soon as preflight computes it — before the branch necessarily exists
    in git — so a second run on the same spec always lands on the same
    branch and continues from whatever was committed, whether the first run
    stopped in preflight (branch not yet created), the survey, a repair, or
    a task.
  - `spec`: true whenever `Result.SplitPlan` is non-empty (an unfinished
    split left a plan file behind); false for a single-package run, since
    nothing is recorded for one to resume from.
  - `issue`: always false; it has no notion of continuing a prior run.
  Each `Result` type reports this the same way it reports `summary`, through
  a one-method interface `Resumable() bool` that `Run.Envelope` type-asserts
  against.
- `fix_hint` (object, optional): a machine-usable remedy, present only for
  the categories below, built from the category plus values `toolio`
  already has access to (the resolved per-phase `Bounds` set at flag-parse
  time, and the resolved model) or that a pipeline attaches:

  | Category | `fix_hint` | Source of `current`/details |
  |---|---|---|
  | `budget` | `{flag: "--budget", current, suggest}` | the run's resolved per-phase `MaxBudgetUSD` |
  | `max_turns` | `{flag: "--max-turns", current, suggest}` | the run's resolved per-phase `MaxTurns` |
  | `no_result` | the same shape as whichever of the two above the phase actually stopped at | `agentrun.NoResultError` already knows the `RunStopReason` (`RunStopBudgetExceeded` vs. `RunStopMaxTurns`); it is exposed on the error so `toolio` can choose |
  | `auth` | `{env: [...]}` | the vendor's credential variable names (`agentrun`'s per-vendor table, already assembled once for the error message in `CheckCredentials`) or, for a forge credential, `GITHUB_TOKEN`/`GH_TOKEN` or `GITLAB_TOKEN` depending on which forge was targeted |
  | `model` | `{flag: "--model", valid: ["SIMPLE", "STANDARD", "ADVANCED"]}` | static — `agentrun`'s tier constants |
  | `usage` | `{flag: "--overwrite"}` (or the equivalent culprit flag) | only when the message names exactly one flag whose removal is the fix (`issue --overwrite` combined with `--repo`/`--label`; `--no-verify` combined with `--verify`); omitted for every other usage error |

  `suggest` is double `current`. `impl`'s `--total-budget` reaches its own
  ceiling through the same `budget` category as a per-phase `--budget`
  ceiling; they are told apart because the total-budget check is the one
  call site that reports its failure at the stage literally named
  `"budget"` (`failf("budget", agentrun.CategoryBudget, …)` in
  `codeimpl/pipeline.go`) — no phase is ever named that — so `fix_hint` for
  that specific `(category, stage)` pair names `--total-budget` and the
  run's `TotalBudgetUSD` instead.

### 5. Structured warnings

`Warnings []string` becomes `Warnings []Warning`, where `Warning` is:

```jsonc
{ "code": "input_truncated", "severity": "high", "stage": "input",
  "message": "the input was truncated at 262144 bytes" }
```

`Run.Warn`'s signature becomes `Warn(code WarnCode, severity string, format
string, args ...any)`. `WarnCode` is a declared string type; `stage` is not
a parameter — it comes from a single `map[WarnCode]string` table in
`toolio` keyed by every declared `WarnCode` constant, so a code cannot be
used without a stage recorded for it, and a test walks the declared
constants against the table (and, separately, against `grep`-style source
scan of `Warn(` call sites — the acceptance criterion below) to catch a
constant added without a table entry or a call site holding a bare string
instead of a constant.

`severity` is `"high"` when the warning means the `ok: true` (or, on `fix`/
`impl`, the landed/parked) result is not quite what it appears to be — the
input was cut, a criterion came back unmet, a change under the spec package
was reverted, a park or a state write silently failed — and `"low"`
otherwise (informational: a comment could not be posted, a plan file
survives after being fully written).

Every existing `Run.Warn` call site across `internal/toolio`, `codefix`,
`codeimpl`, `specgen` and `issuetriage` is converted to pass a declared
`WarnCode`. The table (not exhaustive — a call site that fits none of these
gets a new constant, added to the table, not invented ad hoc):

| code | severity | stage | tool(s) |
|---|---|---|---|
| `input_truncated` | high | input | shared |
| `comments_unreadable` | low | input | shared |
| `no_verify_command` | high | preflight | fix, impl |
| `criteria_unmet` | high | implement | fix |
| `commit_not_parked` | high | park | fix, impl |
| `checkout_not_restored` | low | park | fix, impl |
| `pull_request_not_opened` | high | land | fix, impl |
| `comment_not_posted` | low | report | fix, spec |
| `spec_edit_reverted` | high | task | impl |
| `state_not_saved` | high | park | impl |
| `draft_package` | low | preflight | impl |
| `upstream_missing` | low | preflight | impl |
| `parked_attempt_discarded` | low | preflight | impl |
| `spec_validation_warning` | low | preflight | impl |
| `specs_dir_unreadable` | low | preflight | spec |
| `project_language_unknown` | low | preflight | spec |
| `name_flag_ignored` | low | usage | spec |
| `scope_renamed` | low | prd | spec |
| `scope_count_mismatch` | low | prd | spec |
| `split_plan_foreign` | low | split | spec |
| `split_plan_unreadable` | low | split | spec |
| `split_plan_stale` | high | split | spec |
| `split_plan_update_failed` | high | split | spec |
| `split_plan_not_removed` | low | split | spec |
| `architecture_not_written` | low | write | spec |
| `activation_failed` | high | activate | spec |
| `rejected_path_calls` | low | analyse | issue |

### 6. Documentation

- `docs/cli.md`: the output example gains `status`, `summary` and
  `needs_human`; the error-category table gains a `retryable` column and a
  new "warning codes" table; `--context` is documented under shared flags.
- `README.md`: the exit-code sentence ("Exit codes are shared: `0` done, `1`
  failed, `2` usage, `3` a person has to answer something, `4` work exists
  but the checks do not pass.") is extended to name the matching `status`
  value for each code.

## Design Decisions

1. **Bare-invocation detection folds whitespace-only input into the
   zero-argument case, before `Resolve` runs.** `fix ""` and `fix "   "`
   both stop at `App.Main`, never reaching `toolio.Resolve`, so neither
   spends a model resolution or a network call. Reusing `isTerminal` needs
   no relocation — `App.Main` and `Progress` are already the same package
   (`internal/toolio`).
2. **`status` is derived centrally in `Run.Envelope` from the exit code, in
   both directions.** `toolio.ExitCodeFor` already maps category→exit code;
   `status` is exit code→name over the same five values, so a tool cannot
   report a `status` inconsistent with `ok`/`exit_code` any more than it can
   report `ok: true` with an `error` set.
3. **`summary`, `resumable` and `needs_human`'s source data are read off the
   tool's `Result` via one-method interfaces, type-asserted in
   `Run.Envelope`/`Emit`**, the same pattern `internal/toolio/app.go` already
   uses for `StageError` (`Stage()`/`Category()` on a pipeline's error type).
   This keeps `toolio` from importing `codefix`/`codeimpl`/`specgen`, and
   keeps each tool's `Result` the single place its own shape lives.
4. **`fix`'s `resumable` is hardcoded false; `impl`'s is `Result.Branch !=
   ""`.** Both tools have a `Branch` field, but only `impl` names it
   deterministically from the spec; `fix`'s `gitx.UniqueBranchName` means a
   second run never lands on the first run's branch, so "resumable" would
   be actively misleading if derived from field presence alone.
5. **The impl `--total-budget` ceiling is distinguished from a per-phase
   `--budget` ceiling by stage name (`"budget"`), not a new field.** Both
   fail with `category: "budget"`; `codeimpl`'s total-budget check is the
   only site that names its stage `"budget"` today, so it doubles as the
   discriminator. This is flagged as an open question below: it depends on
   no phase ever being named `"budget"`, which is true today but not
   enforced by the type system.
6. **`Warn` takes an explicit `severity` argument rather than deriving it
   purely from `code`.** The PRD's own examples show `code` fixed per cause
   but a handful of causes (e.g. `spec_edit_reverted`) could plausibly carry
   different severities depending on whether the revert itself succeeded;
   keeping `severity` a call-site argument avoids a second table doing
   double duty as both a stage lookup and a severity lookup, and keeps the
   enforcement test simple (every constant has a stage; nothing asserts a
   fixed severity per constant).
7. **`--context` augments the model-facing prompt, never `Input.Body`.**
   `Body` is the identity `specgen`'s split-plan matching and `codeimpl`'s
   spec-locating both hash or parse; mutating it would either start a
   second, unwanted split-plan copy or break `impl`'s spec resolution
   outright. `Input` gains a second, separate field for the rendered
   context block.
8. **`needs_human.stage` is read from `error.Stage`, not recomputed.** Every
   call site that raises an ambiguity or a blocker already reports a stage
   on the `ErrorInfo`; duplicating it into `needs_human` from a second
   source would be one more place the two could disagree.
9. **`checkUpstream` (`codeimpl/pipeline.go`) is changed to set
   `result.Blocker`** so the upstream-not-done stop populates `needs_human`
   the same way the survey and task blockers already do, rather than being
   a fourth, structurally different "stops with exit 3" path.
10. **The warning code table is given as a first cut covering every
    `Run.Warn` call site found in the repository today, not declared
    closed.** A call site this spec's implementation finds that does not
    fit gets a new constant added to the table; none gets a bare string.

## Dependencies

None. No existing spec under `.specs/` covers the envelope, `internal/toolio`,
or any of the four tools' result shapes.

## Open Questions
