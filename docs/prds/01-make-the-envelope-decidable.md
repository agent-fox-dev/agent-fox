# Make the envelope decidable in one read

## Intent

The four tools — `spec`, `issue`, `fix`, `impl` — are built to be driven by a
program, and increasingly that program is a model: an agent that calls `fix`
as a tool, reads the JSON on stdout, and decides what to do next. The
interface [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) fixed is already
the right shape for that caller — one input, one JSON object, progress on
stderr, one exit-code table — and this PRD does not change the shape.

It closes the gaps where a model caller today has to *know the manual* to act
on a result correctly:

- A bare invocation writes nothing to stdout. `fix "$ISSUE"` with an empty
  variable is a bare invocation, so the calling agent sees empty output and an
  exit code, and nothing to parse.
- Exits `3` and `4` arrive as `ok: false` with an `error` object, in the same
  form as exit `1`. A deliberate stop that asks a question, a change that
  exists but is unverified, and a crash look alike until the caller consults
  the exit-code table.
- The three tools that can hand a decision back to a person each say so
  differently: `fix` as `result.ambiguity {question, interpretation_a,
  interpretation_b}`, `impl` as `result.blocker {reason, needed}`, and `spec`
  as `result.open_questions [{question, decision, why_unsure}]`. There is no
  way to feed an answer back in except by rewriting the input.
- `error.category` "says whether re-running could help", but only in
  `docs/cli.md`. The envelope does not say it, and it does not say *what* to
  change: which flag, which variable.
- `warnings` is `[]string`. A truncated input, a reverted edit to the spec
  package and a comment that could not be posted are all one sentence each,
  and an `ok: true` run carrying a consequential warning reads as a clean one.

The goal is that a caller reading only the envelope — no docs, no exit-code
table — can tell what happened, whether it is done, and what to do next.

## Goals

- Every program-driven invocation writes one JSON object to stdout, including
  the ones with no input.
- One field names the outcome in words; one sentence says what happened.
- One shape, shared by every tool, for "a person has to decide something",
  and one flag to supply the answer on the next run.
- Every error says whether retrying could help, and what to change when a
  flag or a variable is the remedy.
- Every warning carries a stable code and a severity.

## Non-goals

- New subcommands, or any change to the one-positional-input rule.
- Changing the exit-code table or the error-category vocabulary. Both stay;
  the new fields are derived from them.
- Changing any tool's `result` schema beyond moving the human-decision fields
  (below). Output size and chaining are
  [PRD 02](02-trim-and-chain-the-results.md); progress events and
  self-description are [PRD 03](03-make-the-tools-observable-and-self-describing.md).
- Removing `ok`. It stays the one field a caller *has* to read.

## Functional requirements

### 1. No-input invocations emit an envelope when stdout is not a terminal

Today `App.Main` treats a bare invocation (no positional argument) as a human
path: help text to stderr, nothing to stdout, exit `2`.

- When stdout **is not a terminal**, a bare invocation writes the usage
  envelope — `ok: false`, `exit_code: 2`, `error.stage: "usage"`,
  `error.category: "usage"` — with a message naming the four accepted input
  shapes. The help text still goes to stderr.
- When stdout **is a terminal**, behaviour is unchanged: help on stderr,
  nothing on stdout, exit `2`.
- An explicit `-h`/`--help` is unchanged on both: it is a request for text,
  exits `0`, and writes nothing to stdout.
- `--version` is unchanged.
- An argument that is present but empty or all whitespace (`fix ""`) is
  treated as a bare invocation, not as text.

The terminal test is the one `progress.go` already uses (`isTerminal`), moved
where both callers can reach it.

### 2. `status` and `summary` at the top of the envelope

Two new top-level fields:

- `status` — one of `done`, `failed`, `usage`, `needs_human`, `unverified`,
  derived from the exit code one-to-one (`0`, `1`, `2`, `3`, `4`). It is
  set in `Run.Envelope` from the code, as `ok` is, so no tool can set it
  inconsistently.
- `summary` — one sentence, under 200 characters, saying what the run did in
  the tool's own terms. Each tool supplies it from its result, not from the
  model: for example `fix: committed a3f9c1e on fix/issue-42-nil-map;
  checks regressed; not landed`, or `issue: filed acme/widgets#57 (high
  severity, 4 files cited)`. When a tool supplies none, the shell falls back
  to `error.message` on failure and to `"<tool>: done"` on success.

Field order in the emitted JSON is: `tool`, `version`, `ok`, `status`,
`exit_code`, `summary`, `error`, `needs_human`, `warnings`, `result`,
`input`, `usage`, `model`, `duration_ms`, `started_at`. The fields a caller
decides on come first, so a truncated read still has them; `model` and
`usage.phases` go last.

### 3. One `needs_human` object, and `--context` to answer it

A new top-level field, present exactly when `status` is `needs_human`:

```jsonc
"needs_human": {
  "question": "Does 'retry' mean the HTTP client's retry or the job queue's?",
  "options": [
    { "id": "A", "text": "the HTTP client's retry loop in client.go" },
    { "id": "B", "text": "the job queue's redelivery in worker.go" }
  ],
  "needed": "",                       // what the person must supply when there are no options
  "stage": "analyse",
  "resume": "fix <same input> --context \"<answer>\""
}
```

- `fix` fills it from `Ambiguity`: `question`, and the two interpretations as
  options `A` and `B`.
- `impl` fills it from `Blocker`: `reason` becomes `question`, `needed`
  becomes `needed`, no options. A blocker raised because an upstream spec is
  not done names that spec in `needed`.
- `result.ambiguity` and `result.blocker` stay in the result for one release
  and are then removed, so a caller has one place to look.
- `spec` never sets it. `spec` does not stop to ask, and this PRD does not
  change that: `result.open_questions` stays where it is, and the `summary`
  of a run with open questions says how many there are.

A new shared flag, `--context <text>`, repeatable:

- Every `--context` value is appended to the input as a labelled
  `## Additional context from the caller` section before the model sees it.
- It does **not** change what the input is for resume matching: `spec`'s
  split plan still matches a text or stdin input by the input's content
  alone, and `impl`'s branch is still chosen from the spec. Answering a
  question must not start a second copy of the work.
- `input.context_bytes` in the envelope records how much was added.
- Its size counts toward the 256 KB input bound; when it would exceed it,
  the invocation is a usage error, not a truncation.

### 4. Errors say whether to retry, and what to change

`ErrorInfo` gains three fields:

- `retryable` (bool) — whether re-running *unchanged* could succeed. Derived
  from `category` by one table in `toolio`: `api` and `aborted` are
  retryable; `usage`, `input`, `auth`, `model`, `invalid_spec`, `blocked`,
  `ambiguous`, `empty_change` and `internal` are not; `budget`, `max_turns`,
  `no_result`, `unverified`, `git`, `forge` and `disk` are not retryable
  unchanged (see `fix_hint`).
- `resumable` (bool) — whether re-running the same input continues rather
  than restarts. True for `spec` when a split plan was left behind, and for
  `impl` whenever its branch exists. False otherwise.
- `fix_hint` (object, optional) — a machine-usable remedy, for the
  categories where one is known:

  | Category | `fix_hint` |
  |---|---|
  | `budget` | `{ "flag": "--budget", "current": 5.0, "suggest": 10.0 }` |
  | `max_turns` | `{ "flag": "--max-turns", "current": 150, "suggest": 300 }` |
  | `no_result` (at a ceiling) | the same, for whichever ceiling was hit |
  | `auth` | `{ "env": ["ANTHROPIC_API_KEY"] }`, naming the variables the resolved vendor or forge reads |
  | `model` | `{ "flag": "--model", "valid": ["SIMPLE", "STANDARD", "ADVANCED"] }` |
  | `usage` | `{ "flag": "--overwrite" }` naming the offending flag, when there is one |

  `suggest` is double `current`. It is a hint, not a promise; the message
  still says what happened.

`--total-budget` on `impl` gets its own `budget` hint when it is the ceiling
that was reached.

### 5. Structured warnings

`warnings` becomes an array of objects:

```jsonc
"warnings": [
  { "code": "input_truncated", "severity": "high", "stage": "input",
    "message": "the input was truncated at 262144 bytes" }
]
```

- `code` is a stable snake_case identifier from a table in `toolio`, one per
  distinct cause (`input_truncated`, `comments_unreadable`,
  `comment_not_posted`, `spec_edit_reverted`, `criteria_unmet`,
  `draft_package`, `upstream_missing`, `split_plan_foreign`,
  `scope_renamed`, …). Warnings are raised with `Run.Warn(code, severity,
  format, args...)`; the existing call sites are converted, and a new code
  is added to the table rather than invented at the call site.
- `severity` is `high` when the warning means the result is not what it
  appears to be (the input was cut, a criterion was reported unmet, a
  change to the spec package was reverted), and `low` otherwise.
- `stage` is the pipeline step that raised it.
- When any `high` warning is present on an `ok: true` run, the `summary`
  says so.

This is a breaking change to the envelope. It ships under the schema version
bump described in PRD 03, and the old string form is not kept alongside:
two shapes for one field is the problem this PRD removes.

## Acceptance criteria

- `fix < /dev/null > out.json` (no positional argument, stdout a file)
  exits `2` and `out.json` holds one envelope with `status: "usage"`.
- The same invocation on a terminal prints help to stderr and nothing to
  stdout.
- For every exit code, `status` matches the table in §2, in every tool.
- A `fix` run that stops on an ambiguity has `needs_human` with two options,
  and running it again with `--context "B"` reaches the analysis phase with
  the answer in the prompt.
- An `impl` run stopped by an upstream spec has `needs_human.needed` naming
  that spec.
- A `spec` split run with a text input, stopped on scope 2 and re-run with
  `--context`, resumes from scope 2.
- A run that stops at its budget has `error.fix_hint.flag == "--budget"`.
- No call site passes a warning without a code from the table (enforced by a
  test over the table and the call sites' constants).

## Documentation

- `docs/cli.md`: the output section, the error table (with `retryable`), the
  warning codes, `--context` under shared flags.
- `README.md`: the exit-code paragraph mentions `status`.
