---
spec_id: "06"
spec_name: "trim_and_chain_results"
title: "Trim the results and make them chain"
status: "active"
created_at: "2026-09-30T09:23:17.958363Z"
updated_at: "2026-09-30T09:23:17.958363Z"
intent_hash: "f566f488647d9e9a2d20e996b03589ff447a3a169afbe83fabc486a190ed06a5"
schema_version: 2
source: "docs/prds/02-trim-and-chain-the-results.md"
---
## Intent

A model that calls `issue`, `fix`, `spec` or `impl` as a tool pays, in its context window, for every byte of the envelope it gets back, and then has to search the rest for the two or three fields that decide its next step. This spec makes the default envelope carry only what a caller acts on, keeps the complete result available on disk without a re-run, gives every tool one uniform view of what it produced (`artifacts[]`) and what it changed outside the machine (`side_effects[]`), lets a result suggest its own next invocation (`next[]`), and closes two input-side gaps — a mistyped path that silently becomes a plausible-sounding report, and a shared-flag promise (`--dry-run`, `--total-budget`) kept for `Common`'s own flags and nowhere else. It does not change what any tool does; every field trimmed from the default view is still computed and still written in full to a report file.

## Goals

- The default (`--detail summary`) envelope for a passing `fix` run is under 3 KB with no command output in it, and every byte trimmed from it is still in a report file the envelope names, byte-for-byte identical to what `--detail full` would have printed for the same run.
- `artifacts[]` lists everything one run produced, in one shape across all four tools, built from facts the tool already holds (git, the forge client's response, the file it wrote) and never from a model's own report.
- `side_effects[]` lists every write to a forge or a remote, in the order it happened, so "did this touch GitHub" has one answer regardless of which tool ran.
- `next[]` names the invocation a caller would plausibly make next, runnable as given wherever the referenced thing (an issue URL, a spec directory, a pull request) is small and stable; never supplied by the model.
- A single-line, whitespace-free, path-shaped argument that does not exist as a file is flagged with a warning naming it, and `--input-kind file|text|issue|stdin` lets a caller demand strict classification and get a usage error (exit 2) before a token is spent.
- `--dry-run` and `--total-budget` are defined once, in `Common`, mean the same thing on every tool that accepts them, and a tool asked for a flag it does not support says so by name rather than emitting Go's generic "flag provided but not defined".

## Non-goals

- Changing what a tool does. Every field trimmed from the default envelope is still computed and still written to the report file.
- Removing the input-classification fallback: text that looks like a path stays text by default; `--input-kind` only makes strictness opt-in.
- Progress events, a machine-readable result schema, or trust labels on individual fields — that is a later spec ("Make the tools observable and self-describing").
- Serving the tools over MCP, or anything about a job store for a long-running call — that is a later spec too.
- Re-deriving `status`, `summary`, `needs_human`, `retryable`/`resumable`, `fix_hint` or structured `warnings`: this spec is built on top of `05_envelope_decidable` and only adds new warning codes and new fields beside the ones that spec defines.

## Background

The four tools share one shell, `internal/toolio` (`App`, `Run`, `Envelope`, `Resolve`), described in `docs/cli.md` and `docs/configuration.md`. Each tool's own package (`issuetriage`, `codefix`, `codeimpl`, `specgen`) defines a `Result` struct returned to `cmd/<tool>/main.go`'s `Exec`, which `Run.Envelope` puts under the JSON object's `result` key. Confirmed in the code:

- `issuetriage.Result` (`issuetriage/pipeline.go`) carries `action`, `url`, `number`, the rendered `body`, and every field `body` was rendered from (`problem`, `reproduction`, `root_cause`, `affected_files`, `suggested_fix`, `acceptance_criteria`, …) — the diagnosis appears twice.
- `codefix.Result` (`codefix/types.go`) carries two `checks.Result` values (`Baseline`, `Verification`), each with up to 40 lines of command output (`internal/checks/checks.go`'s `outputTailLines`), the diff stat, `Implementation` (the model's report) and every analysis field, always, whether or not a caller needs them.
- `codeimpl.Result` (`codeimpl/types.go`) carries one `TaskReport` per task, each with its own `GateResult` (a list of `checks.Result`), `Verdict`, `DiffStat` and `Submission`, plus the `Survey` brief — for a twelve-task spec, twelve gate outputs.
- A caller chaining tools has to know each one's result schema to find what to hand to the next: `url` on `issue`, `spec_dir`/`spec_id` on `spec`, `pull_request_url` on `fix` and `impl`.
- What a run changed outside the machine is spread over different fields per tool: `issuetriage.Result.Action`/`URL`, `codefix.Result.Pushed`/`Comments`/`PullRequestURL`, `codeimpl.Result` the same three. "What did this touch on GitHub" has a different shape in each.
- `toolio.Input` classifies a single argument in `Resolve` (`internal/toolio/input.go`) into `file`, `text`, `issue` or `stdin`, deliberately falling back to `text` for a path that does not exist (`readIfFile` and the comment above `Resolve` explain why: "the `widget/` package panics" is plausible prose). The only evidence a caller gets that this happened is `input.kind: "text"` in the envelope — nothing calls it out.
- `Common` (`internal/toolio/cli.go`) is the one flag set every tool's `Register` call shares — `--dir`, `--model`, `--vendor`, `--variant`, `--max-turns`, `--budget`, `--phase-timeout`, `--trust-project`, `--verbose`, `--quiet`, `--show-text`, `--version`. `--dry-run` is instead defined four times, once per `cmd/<tool>/main.go`, with different prose each time (`issue`: "make no change on GitHub or GitLab"; `fix`/`impl`: "make no REMOTE change: nothing is pushed…"; `spec`: "write nothing to disk or forge"). `--total-budget` exists only on `impl` (`codeimpl.Options.TotalBudgetUSD`, checked between phases and tasks by `overBudget` in `codeimpl/pipeline.go`); `issue`, `fix` and `spec` have no run-level spend ceiling at all.
- A flag one tool does not define, given anyway, produces Go's own `flag.Parse` error ("flag provided but not defined: -label"), surfaced verbatim by `App.Main` (`internal/toolio/app.go`) — accurate, but it does not tell a caller which of the four tools *does* accept it.

This spec is built directly on `05_envelope_decidable` ("Make the envelope decidable in one read"), whose `prd.md` is on disk and whose `tasks.json` is written but not yet implemented in the tree (`internal/toolio/envelope.go` today has none of `status`, `summary`, `needs_human`, structured `warnings`, `retryable`/`resumable` or `fix_hint`). This spec assumes that work lands first: it reuses `needs_human.resume`'s template convention for one of `next[]`'s entries, adds new `WarnCode` constants to the table that spec establishes, and relies on its "a warning of severity `high` appends a clause to `summary`" rule rather than re-implementing it.

## Requirements

### 1. `--detail summary|full` and `--report-file`

A new shared flag, `--detail`, registered on `Common`, with `summary` the default and `full` the other legal value; an unrecognized value is a usage error.

- Under `full`, `result` is exactly what the tool produces today, plus whatever `05_envelope_decidable` adds. Under `summary` (the default), `result` keeps only the fields listed below for that tool; every other field of the same `Result` value is what the report file (below) still carries in full.
- Every `Result` records which view was emitted, as a `detail` field carrying `"summary"` or `"full"`, present in both the full and the trimmed value.
- Kept in `issue`'s `summary`: `action`, `repo`, `url`, `number`, `title`, `severity`, `confidence`, `affected_files` (paths only — the citation, not the role or the rest of the diagnosis), `labels`, `rejected_path_calls`.
- Kept in `fix`'s `summary`: `stage`, `branch`, `base_branch`, `commit`, `changed_files`, `verdict`, `criteria_outcome`, `pull_request_url`, `dry_run`, and `verification` only when `checks.Compare`'s verdict for the run is not one `Verdict.Landable()` reports true for (i.e. the run regressed, stayed failing, or is otherwise not something a caller would accept without looking) — the failing command's tail output is what a caller needs to decide whether to retry, repair, or give up, and it is the one place the summary view is deliberately not smaller.
- Kept in `impl`'s `summary`: `stage`, the package fields (`spec_dir`, `spec_id`, `spec_name`, `title`, `status`), `branch`, the task counts (`tasks_total`, `tasks_done`, `tasks_skipped`, `tasks_remaining`), one entry per task with only `{id, outcome, commit, verdict}`, the top-level `verdict`, `pull_request_url`, and the top-level `verification` (the gate's checks, with their tail output) only when the run stopped on a verdict that is not landable — the same rule as `fix`, applied to `codeimpl`'s `GateResult` instead of `checks.Result`.
- Kept in `spec`'s `summary`: `spec_dir`, `spec_id`, `spec_name`, `status`, `artifacts` (the list of file names, unchanged), `validation` reduced to `{valid, error_count, errors}` (dropping `warning_count` and `warnings`), `traceability` reduced to `{criteria_uncovered, paths_uncovered, tests_unowned}` (dropping the two covered counts), `open_questions`, `split`. `follow_on_specs` and `split_plan` are dropped from the summary view — `split[]` already names every scope with its status, which is what a caller needs to know whether to run `spec` again.
- Every run writes the complete envelope — the `full` view of `result`, whatever `--detail` was actually given — to a report file: `--report-file <path>` when given, else `$XDG_STATE_HOME/agent-fox/runs/<tool>-<started_at>-<pid>.json` (falling back to `~/.local/state/agent-fox/runs/…` when `$XDG_STATE_HOME` is unset), with the parent directory created as needed. A run that cannot write it (a permission error, a read-only filesystem) records a `low` warning (a new `WarnCode`, stage `report`) rather than failing, and the envelope's `report_file` field is then omitted. `--dry-run` runs still write the report file: it is local state on the machine running the tool, not a remote change.
- `report_file` is a new top-level envelope field naming the path actually written, present whenever the write succeeded. It is written for every JSON object emitted to stdout, including a failed run and a post-classification usage error — the two purely human-driven paths (a bare invocation on a terminal, `-h`/`--help`, `--version`) that already print nothing to stdout continue to print nothing and write no report file.

### 2. `artifacts[]`: what the run produced

A new top-level array, present whenever the run produced anything, over a closed set of kinds: `issue`, `pull_request`, `comment`, `branch`, `commit`, `spec_package`, `report_file`.

- `{kind: "branch", name, base}` — the branch created (`fix`, `impl`).
- `{kind: "commit", sha, branch}` — one entry per commit the run made, including a parked `wip:` commit and, for `impl`, a repair's commit.
- `{kind: "pull_request", url, number}` — from the forge's own response, never assembled from a URL template.
- `{kind: "comment", url}` — one entry per comment actually posted (`fix`'s `Comments`, `spec`'s `CommentURL`).
- `{kind: "issue", url, number}` — the issue `issue` created or updated.
- `{kind: "spec_package", path, id, valid}` — one entry per package `spec` wrote (the first plus every entry of `follow_on_specs`), `valid` from that package's own `Validation.Valid`.
- `{kind: "report_file", path}` — added once the report file (requirement 1) is written, so "what did this run leave behind" is answered by this one array too.
- Every tool builds this array from facts it already holds on its own `Result` type — `Branch`, `Commit`, `PullRequestURL`, `Comments`, `SpecDir`, `Validation.Valid`, and so on — never from `Implementation`, `Submission`, or any other field that is the model's own account of its work.
- Under `--dry-run`, an entry for something that would have happened but did not carries `"dry_run": true` — a `pull_request` or `comment` entry for a `fix`/`impl` dry run that reached `--land=pr`, or an `issue` entry with an empty `url`/`number` for `issue --dry-run`. An entry for something that *did* happen locally under a dry run (the branch, the commit — `fix`/`impl`'s `--dry-run` still writes those) carries no `dry_run` marker, because it is not hypothetical.
- The existing result fields (`url`, `pull_request_url`, `spec_dir`, …) are unchanged; `artifacts` is a second, uniform view over the same facts, not a replacement.

### 3. `side_effects[]`: what the run changed outside the machine

A new top-level array, empty (`omitempty`) when the run made no remote write, listing every write to a forge or a remote in the order it happened: `{action, target, ok, warning?}`.

- `action` is one of `create_issue`, `update_issue`, `comment`, `push`, `open_pr`.
- `target` names what was written to: `"origin <branch>"` for a push, `"<owner>/<repo>#<number>"` for a comment or an issue update, `"<owner>/<repo>"` for a created issue before it has a number, `"<owner>/<repo>#<pr-number>"` for a pull request.
- `ok` is `true` when the write succeeded; `false` alongside a `warning` naming the `WarnCode` the tool already recorded for that failure (`pull_request_not_opened`, `comment_not_posted`, …, per `05_envelope_decidable`'s warning table) — the two never disagree, because both come from the same call site.
- Recorded at the one place each write actually happens — `issuetriage.Write`'s two forge calls, `codefix`/`codeimpl`'s `git.Push`, `openPullRequest`/`LandPRChanges`, and `postComment`, `specgen`'s PRD-comment write — as a call alongside the `Run.Warn` the same site already makes on failure, so a write cannot happen without an entry and a failed write cannot appear silently `ok: true`.
- Empty under `--dry-run`: every one of the call sites above is already skipped or short-circuited under `--dry-run` today, so no code path exists that could record one.

### 4. `next[]`: the invocation a caller would make next

An optional top-level array of suggested follow-ups, `{tool, input, flags[], why}`, derived from the tool's own `Result` in Go — never the model's — and omitted when there is nothing to suggest.

- `issue`: when an issue was actually filed or updated (`Action != "none"`), suggests `fix` on its URL: `{tool: "fix", input: "<the issue URL>", flags: [], why: "the issue was filed and labelled …"}` (naming the labels when there were any). Nothing under `--dry-run`, since there is no URL yet.
- `spec`: when at least one written package validates, suggests `impl` on the first one that does: `{tool: "impl", input: "<its spec_dir>", flags: [], why: "the package validates and is ready to implement"}`. When the split is unfinished (`split_plan` non-empty, i.e. `Resumable()` per `05_envelope_decidable` is true), also suggests `spec` again on the same input, using the literal origin (a file path or issue URL) when the input had one, and the placeholder `"<same input>"` — the same string `needs_human.resume` uses — when the input was raw text or stdin, since that body may be arbitrarily large.
- `impl`: when the run parked (`stage: "parked"`, exit 4), suggests itself on the same spec: `{tool: "impl", input: "<spec_dir>", flags: [], why: "the run parked; re-running continues from the last landed task"}`; when it parked with a red baseline (`baseline`'s checks include a failing one) and no repair was attempted (`result.repair` is absent), the suggestion's `flags` includes `"--repair"` and the `why` says the baseline needs repairing first.
- `fix`: nothing on success. When it stopped on an ambiguity (`category: "ambiguous"`), suggests itself with `--context`, rendered **exactly as `needs_human.resume`** — the placeholder input shape, not the tool's literal argument — because a report's text may be too large to repeat and `05_envelope_decidable` already established that convention for this exact case; this is the one `next[]` entry that is not directly runnable as given, and it is deliberately identical to `needs_human.resume` so the two never disagree.
- Every other suggestion above names a stable, small reference (an issue URL, a spec directory) and is runnable exactly as printed.

### 5. A mistyped path is reported, and strictness is opt-in

- When `Resolve` classifies the input as `text`, and the raw argument is a single line, contains no whitespace character, and either contains `/` or ends in a short alphanumeric extension-shaped suffix (`\.[A-Za-z0-9]{1,6}$`), and `os.Stat` on it fails (the path does not exist — an existing directory is not flagged: it is already a legitimate reference for `impl`, and a plausible one elsewhere), the run records a `high`-severity warning `input_looks_like_path` (stage `input`) naming the path. Under `--detail summary`, `fix`'s and `impl`'s kept `verification`/`baseline` are unaffected by this warning; the warning itself is always in the top-level `warnings` array regardless of `--detail`, and (per `05_envelope_decidable`) an `ok: true` run carrying it gets the deterministic high-severity clause appended to `summary` automatically — no per-tool code needed beyond declaring the code.
- A new shared flag, `--input-kind file|text|issue|stdin`, forces the classification instead of letting `Resolve` guess, and does every check before a file is opened, a URL is fetched, or a model is resolved:
  - `file`: the argument must be a readable regular file; a missing path is refused naming that it does not exist, a directory naming that it is one — both before `Resolve`'s ordinary file-vs-text fallback ever runs.
  - `text`: the argument is used verbatim as the input body; no file-existence or issue-URL check happens even when a same-named file or a parseable URL exists, so a caller that means "this string, literally" is never second-guessed.
  - `issue`: the argument must parse as a GitHub or GitLab issue/PR URL; one that does not is refused before any HTTP request.
  - `stdin`: the argument must be `-`; anything else is refused.
  - A mismatch is a usage error, exit `2`, exactly like every other pre-flight refusal — no token spent, nothing fetched.
- `impl` accepts `--input-kind text` for its ordinary spec references (a directory, an id, a name); forcing it explicitly guarantees a directory argument, or a bare id that happens to also name a file elsewhere on disk, is read as a reference rather than shadowed by `Resolve`'s own file check — `codeimpl.Locate` already treats a `text`-kind body as a reference to try against the spec root, so no change is needed there, only in what `Resolve` does before handing it the body.

### 6. The shared flags are shared

- `--dry-run` is registered once, on `Common`, with the one definition: **make no remote change** — no push, no write to a forge. Each tool's own help text (the `usage` constant in its `cmd/<tool>/main.go`, and its section of `docs/cli.md`) states what it still does locally: `issue` nothing; `spec` nothing (it writes no files under `--dry-run` either, as today); `fix` and `impl` the branch and the commits.
- `--total-budget` is registered once, on `Common`: a ceiling, in dollars, on the run's total spend across every phase; zero (the default) means no ceiling beyond the existing per-phase `--budget`.
  - `issue` has one phase, so `--total-budget` and `--budget` bound the same spend; when both are given, the lower wins, applied as the one phase's effective ceiling.
  - `fix` has two phases (analyse, implement) with one boundary between them; the cumulative spend is checked at that boundary the same way `codeimpl`'s `overBudget` checks it between tasks, stopping with `category: "budget"` before the implementation phase starts if the ceiling is already passed.
  - `spec` enforces it between the scopes of a split — before each scope's PRD phase begins, the same point `codeimpl` checks between tasks — stopping with `category: "budget"` and the split plan left in place to resume from. An input that is one spec's worth of work (no split) has no interior boundary, so it behaves like `issue`'s single-phase fold.
  - `impl` is unchanged: it already enforces `--total-budget` between phases and tasks.
- A flag one tool's `Exec` does not use, given anyway, is rejected with a usage error that names both the flag and which of the four tools does accept it (when it is one of the other three tools' own flags), rather than Go's `flag.Parse`'s generic "flag provided but not defined". A flag that belongs to none of the four tools keeps that generic message.

### 7. Documentation

- `docs/cli.md`: a new "`--detail`, `--report-file`" entry in the shared-flags table; each tool's section gains its `summary` subset (as a table, mirroring requirement 1) and the local meaning of `--dry-run`; `--input-kind` is documented under shared flags with its four values and the pre-fetch refusal it produces; `--total-budget` moves from `impl`'s flag table into the shared one, with the per-tool granularity from requirement 6; the three new arrays (`artifacts`, `side_effects`, `next`) get one worked example each in "The output".
- `docs/configuration.md`: a new paragraph under a "Report files" heading (or appended to an existing section) naming the default location, the `$XDG_STATE_HOME` / `~/.local/state` fallback, and that `--report-file` overrides it.

## Design Decisions

1. **The summary/full split is per-tool `Result` logic, not a generic reflection-based trimmer.** Each documented subset removes or restructures fields in ways a generic "keep these JSON keys" filter cannot express (`affected_files` becomes paths-only; `validation`/`traceability` drop specific sub-fields; `verification` is included or omitted based on the run's own verdict). Each `Result` type supplies its trimmed view through a small method `Run.Envelope`/`Emit` type-asserts for, the same pattern `05_envelope_decidable` already establishes for `Summary() string`, `Resumable() bool` and the `needs_human` source — kept consistent rather than inventing a second mechanism.
2. **The report file always holds the full view, independent of what `--detail` produced for stdout.** A caller who ran with `--detail summary` and later wants the rest does not have to re-run with `--detail full` to get an identical document — the file already is that document. This is why "byte-for-byte what `--detail full` prints" is stated as an invariant to test, not an implementation accident.
3. **The report file's timestamp component is not `started_at`'s literal RFC3339 string.** RFC3339 contains `:`, which is legal in a POSIX filename but awkward to tab-complete and glob; the file name uses a colon-free variant of the same instant (`20060102T150405Z`-shaped), while the envelope's own `started_at` field is unaffected.
4. **`artifacts[]` and `next[]` are read off each `Result`'s already-established fact fields, not computed independently inside each pipeline.** `Result.Branch`, `.Commit`, `.PullRequestURL`, `.Comments`, `.SpecDir`, `.Validation.Valid` and the rest are already facts the pipeline derived from git or the forge, never from `Implementation`/`Submission`/`Survey`; reading them a second time to build `artifacts`/`next` cannot introduce a claim the model made.
5. **`side_effects[]` is recorded live, on `*toolio.Run`, at the same call sites that already call `Run.Warn` on failure — not derived after the fact from the finished `Result`.** A push, a comment post and an `open_pr` call each happen once, in one function; recording the outcome there is the only way the array's `ok`/`warning` pair can never disagree with what `Run.Warn` already says about the same event.
6. **`next[]`'s `input` is literal wherever the reference is small and stable (an issue URL, a spec directory), and the `needs_human.resume` placeholder (`"<same input>"`) wherever the original input was raw text or stdin.** This mirrors `05_envelope_decidable`'s own reasoning for `resume`: a report can be up to 256 KB, and repeating it inside a JSON array is not "runnable as given" in any useful sense. `fix`'s ambiguity suggestion is the one entry that uses the placeholder even for a small, stable input (an issue URL or file path), because it must stay textually identical to `needs_human.resume` rather than merely equivalent to it.
7. **"Verification only when it failed" is `!checks.Verdict.Landable()` for `fix`, and `!landable(verdict, …)` (`codeimpl/gate.go`) for `impl`'s top-level gate** — the same predicate each pipeline already uses to decide whether to land, reused rather than re-derived, so "kept in the summary" and "landed" can never disagree about which run counts as failing.
8. **A mistyped-path warning does not fire for an existing directory.** `impl` already treats a directory argument as a legitimate spec reference, and elsewhere a directory is a plausible thing to cite in a report; only a path that does not exist at all is a plausible typo.
9. **`--input-kind`'s four forced modes each skip `Resolve`'s ordinary auto-classification entirely, rather than running it and then checking the result matches.** Running the auto-classifier first and rejecting a mismatch afterward would still fetch a URL or read a file before refusing on some inputs (e.g. `--input-kind text` on an argument that is also a readable file), which the requirement explicitly rules out ("before anything is fetched or a token spent").
10. **The unsupported-flag rejection is a better error message on the existing `flag.Parse` failure path, not a restructuring of flag registration.** Each tool keeps registering only the flags its own `Exec` uses; `App.Main`, on a "flag provided but not defined" parse error, checks a small static table of which of the four tools defines that flag name and, when it finds one, reports "`<this tool>` does not accept `--x`; it is a `<other tool>` flag" instead of Go's generic message. This avoids requiring every tool's `flag.FlagSet` to know about flags it will never use.
11. **`--total-budget`'s single-phase fold (`issue`, and an unsplit `spec` input) applies `min(--budget, --total-budget)` as that one phase's effective per-phase ceiling**, rather than adding a second, redundant "check after the only phase" code path — the two ceilings are mathematically the same spend for a tool with one phase, so there is nothing to check between phases that per-phase enforcement does not already check.
12. **This spec adds new `WarnCode` constants (`input_looks_like_path`, `report_file_not_written`) to the table `05_envelope_decidable` defines, rather than inventing a parallel warnings mechanism.** Severity and stage for each are fixed at the constant's declaration, per that spec's own rule that a code cannot be used without a table entry.

## Dependencies

| Spec | Reason |
|---|---|
| `05_envelope_decidable` | Supplies `status`, `summary`, `needs_human` (whose `resume` template `next[]` reuses for `fix`'s ambiguity suggestion and, when unsplit, `spec`'s own resume suggestion), `Resumable()`, and the structured `Warning{code, severity, stage}` shape and its high-severity summary clause, which the new `input_looks_like_path` and `report_file_not_written` codes plug into. |

## Open Questions
