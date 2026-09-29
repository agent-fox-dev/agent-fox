# Trim the results and make them chain

## Intent

A model that calls one of the tools pays for every byte of the envelope in
its context window, and then has to find the two or three fields that decide
its next step among the rest. Today the results are written for completeness:

- `issue` returns the rendered issue `body` **and** every field it was
  rendered from (`problem`, `reproduction`, `root_cause`, `affected_files`,
  `suggested_fix`, `acceptance_criteria`, …), so the diagnosis appears twice.
- `fix` returns two `checks.Result` values, each with up to 40 lines of
  command output, plus the diff stat, the model's `implementation` report and
  the analysis fields.
- `impl` returns one entry per task, each with its own gate result, verdict,
  diff stat and the model's submission, plus the survey brief.

A caller that wants to go on — `issue` then `fix`, `spec` then `impl` — must
also know each tool's result schema to find the thing to hand over: `url`
on `issue`, `spec_dir` or `spec_id` on `spec`, `pull_request_url` on `fix`
and `impl`. And the things the run did to the outside world are spread over
`action`, `url`, `comments`, `pushed` and `pull_request_url`, so "what did
this change on GitHub" has a different answer in each tool.

Two input-side papercuts belong here too, because both make a caller act on a
result that is not what it thinks:

- A mistyped path (`fix ./bug-reprot.md`) is text. The shared input rules say
  so deliberately — "the `widget/` package panics" is a plausible report —
  but the only evidence is `input.kind: "text"`, which a caller has to think
  to read. The run proceeds, on the file's name, and spends a budget.
- The shared-flag promise ("a caller that can drive one can drive all") is
  kept for the flags in `Common` and nowhere else. `--total-budget` exists
  only on `impl`, and `--dry-run` exists on all four with four different
  meanings.

## Goals

- The default envelope carries what a caller decides on, and nothing it has
  to skip.
- The full result is always available, in a file, without re-running.
- One array lists what the run produced, in one shape across the tools, and
  one array lists what it changed outside the machine.
- A result can suggest the next invocation in a form a caller can run.
- A mistyped path is noticed, and a caller that wants strictness can have it.
- `--dry-run` and `--total-budget` mean the same thing on every tool that
  accepts them.

## Non-goals

- Changing what a tool does. Every field removed from the default envelope
  is still computed and still written to the full report.
- Removing the input-classification fallback. Text that looks like a path
  stays text by default.
- Progress events, a result schema, or trust labels on fields
  ([PRD 03](03-make-the-tools-observable-and-self-describing.md)).
- The top-level `status`, `summary`, `needs_human` and structured warnings
  ([PRD 01](01-make-the-envelope-decidable.md)); this PRD assumes them.

## Functional requirements

### 1. `--detail summary|full`, and `--report-file`

A new shared flag, `--detail`, with `summary` as the default.

- Under `full`, the envelope is exactly what it is today (plus PRD 01's
  fields).
- Under `summary`, each tool's `result` keeps a documented subset. The
  subsets are:

  | Tool | Kept in `summary` |
  |---|---|
  | `issue` | `action`, `repo`, `url`, `number`, `title`, `severity`, `confidence`, `affected_files` (paths only), `labels`, `rejected_path_calls` |
  | `fix` | `stage`, `branch`, `base_branch`, `commit`, `changed_files`, `verdict`, `criteria_outcome`, `pull_request_url`, `dry_run`, and `verification` **only when it failed** |
  | `spec` | `spec_dir`, `spec_id`, `spec_name`, `status`, `artifacts`, `validation` (errors only), `traceability` (the uncovered and unowned lists only), `open_questions`, `split` |
  | `impl` | `stage`, the package fields, `branch`, the task counts, per task `{id, outcome, commit, verdict}`, `verdict`, `pull_request_url`, and the failing gate's output when the run stopped on one |

- The failing-check output is kept in `summary` on purpose: it is what the
  caller needs to decide whether to retry, repair, or give up.
- `result.detail` records which view was emitted.
- Every run writes the full envelope to a report file. The path is
  `--report-file <path>` when given; otherwise
  `$XDG_STATE_HOME/agent-fox/runs/<tool>-<started_at>-<pid>.json`
  (falling back to `~/.local/state/…`), and a run that cannot write it
  records a `low` warning rather than failing. The path is in the envelope as
  `report_file`.
- `--dry-run` runs still write the report file: it is local state, not a
  remote change.

### 2. `artifacts[]`: what the run produced

A new top-level array, present whenever the run produced anything:

```jsonc
"artifacts": [
  { "kind": "branch",       "name": "fix/issue-42-nil-map", "base": "main" },
  { "kind": "commit",       "sha": "a3f9c1e", "branch": "fix/issue-42-nil-map" },
  { "kind": "pull_request", "url": "https://github.com/acme/widgets/pull/58", "number": 58 },
  { "kind": "spec_package", "path": ".specs/09_agent_mode", "id": "09", "valid": true }
]
```

- The kinds are a closed set: `issue`, `pull_request`, `comment`, `branch`,
  `commit`, `spec_package`, `report_file`.
- Each tool builds the array from facts it already holds (git, the forge
  client's responses, the files written), never from a model's report.
- A `--dry-run` run lists what it would have produced with `"dry_run": true`
  on each entry that did not happen.
- The existing result fields (`url`, `pull_request_url`, `spec_dir`, …) stay;
  `artifacts` is the uniform view over them.

### 3. `side_effects[]`: what the run changed outside the machine

A new top-level array listing every write to a forge or a remote, in the
order they happened:

```jsonc
"side_effects": [
  { "action": "push",            "target": "origin fix/issue-42-nil-map", "ok": true },
  { "action": "open_pr",         "target": "acme/widgets#58",            "ok": true },
  { "action": "comment",         "target": "acme/widgets#42",            "ok": false,
    "warning": "comment_not_posted" }
]
```

- `action` is one of `create_issue`, `update_issue`, `comment`, `push`,
  `open_pr`.
- Recorded at the one place each write happens (the `issuex` call site or
  the push), so a write cannot happen without an entry.
- Empty under `--dry-run`. A caller answering "did this touch GitHub?" reads
  one array.

### 4. `next[]`: the invocation a caller would make next

An optional top-level array of suggested follow-ups, each runnable as given:

```jsonc
"next": [
  { "tool": "fix", "input": "https://github.com/acme/widgets/issues/57",
    "flags": ["--dir", "/src/widgets"], "why": "the issue was filed and labelled af:fix" }
]
```

- `issue` suggests `fix` on the issue it filed.
- `spec` suggests `impl` on the first valid package, and — when a split is
  unfinished — `spec` on the same input.
- `impl` suggests itself on the same spec when it parked, and `--repair`
  when it parked on a red baseline without it.
- `fix` suggests nothing on success, and itself with `--context` when it
  stopped on an ambiguity (PRD 01's `needs_human.resume` is the same string).
- Suggestions are derived from the result in Go. They are never the model's.

### 5. A mistyped path is reported, and strictness is opt-in

- When an input classified as `text` is a single line, contains no
  whitespace, and either contains a path separator or ends in a file
  extension, the run records a `high` warning `input_looks_like_path`
  naming the path that does not exist, and the `summary` repeats it.
- A new shared flag, `--input-kind file|text|issue|stdin`, makes the
  classification strict: an argument that does not resolve to the named kind
  is a usage error (exit `2`), before anything is fetched or a token spent.
  `--input-kind file` on a missing path says the path does not exist; on a
  directory, that it is a directory.
- `impl` accepts `--input-kind text` as the default for its spec references,
  and documents that a directory argument is a reference.

### 6. The shared flags are shared

- `--dry-run` moves into `Common` with one definition: **make no remote
  change** — no push, no forge write. Each tool documents what it still does
  locally: `issue` nothing; `spec` nothing (it also writes no files, as
  today); `fix` and `impl` the branch and commits. The help text for each
  tool states the local part.
- `--total-budget` moves into `Common`: a ceiling on the run's total spend
  across every phase. On single-phase tools it is equivalent to `--budget`
  and both may be given (the lower wins). `spec` enforces it across the
  scopes of a split, stopping between scopes as `impl` stops between tasks.
- A flag a tool does not support is rejected with a usage error naming the
  tool, rather than being absent from its flag set, so a caller sending the
  same flag set to every tool gets a clear answer.

## Acceptance criteria

- A default `fix` envelope for a passing run is under 3 KB with no command
  output in it; the same run's report file holds the full envelope,
  byte-for-byte what `--detail full` prints.
- A failing `fix` run keeps the failing check's output tail in the default
  envelope.
- `issue --dry-run` lists an `issue` artifact with `dry_run: true` and an
  empty `side_effects`.
- A `fix --land pr` run's `side_effects` holds `push` then `open_pr` then
  `comment`, in that order; one whose comment post returned 403 has that
  entry with `ok: false`.
- `issue`'s `next[0]` is a `fix` invocation on the filed issue's URL.
- `fix ./missing.md` records `input_looks_like_path`; `fix ./missing.md
  --input-kind file` exits `2` without calling the model.
- `spec --total-budget 3` on a four-scope split stops between scopes with a
  `budget` error once the total passes 3.

## Documentation

- `docs/cli.md`: `--detail`, `--report-file`, `--input-kind`, the moved
  `--dry-run` and `--total-budget`, the three new arrays, and each tool's
  summary subset.
- `docs/configuration.md`: the report-file location.
