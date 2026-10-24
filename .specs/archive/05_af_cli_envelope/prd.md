---
spec_id: "05"
spec_name: "af_cli_envelope"
title: "The af binary, its envelope and its run directory"
status: "active"
created_at: "2026-10-20T07:26:48.777724Z"
updated_at: "2026-10-20T07:26:48.777724Z"
intent_hash: "feaec2dc5848c34e654cedb0e94dd61e9f14f486dbfe253c6b57375978ace755"
schema_version: 2
source: "docs/prd/02-rebuild-triage-and-fix-on-the-toolbelt.md"
---
## Intent

Give agent-fox one command-line binary, `af`, in which every command speaks the same contract: it prints one JSON envelope on stdout with a fixed status and exit-code table, it stops with `needs_human` and resumes through `--context`, its `--dry-run` cannot write to the forge, its schema can be fetched with `--schema`, and its state lives in a git-ignored run directory under the checkout. The toolbelt commands and the two agents (`triage`, `fix`) of PRD 02 are built on this shell, so the shell owns the contract and the commands do not repeat it.

## Goals

1. `go build ./cmd/af` (and `make build`, which writes `bin/af`) produces `af`. `af --version` and `af --schema` print one JSON document each and exit 0. `af` with no arguments, or with an unknown command, prints a `usage` envelope and exits 2.
2. A command registered on the shell gets these without writing them itself: the envelope, the exit-code table, coded warnings, `needs_human` with a `resume` line, `--context`, `--dry-run`, `--timeout`, `--output`, `--emit-events`, `--schema`, signal handling and recovery from panics. A test command inside `internal/toolio` proves each one.
3. Every command that takes a positional `<issue|file|text|->` resolves it the same way, GitHub and GitLab issue and pull/merge-request URLs included, tested offline against `httptest` forges.
4. Under `--dry-run`, no write request reaches the forge, whatever the command does. The forge client the shell hands the command enforces this, an `httptest` server confirms it, and the envelope lists each refused write as a `planned` side effect.
5. A command that declares a run directory gets `.agent-fox/run/<id>/` under `--dir`. The directory is git-ignored without editing the repository's own `.gitignore`, and holds the envelope, the event stream and the ledger.
6. The output of `af --schema` matches a committed golden file, and a test fails if the envelope's Go type and its schema drift apart.
7. `make check` passes offline. `docs/cli.md`, `docs/configuration.md` and `docs/model-usage.md` exist and describe what this scope ships. `README.md` and `docs/architecture.md` are updated. `make doc` has been run and its output committed.

## Non-goals

- **Any work command.** Each lands in its own spec of this split, on this shell:
  - `af_ring1_facts`: `af lang detect`, `af gate run|compare`, `af revert-check`, `af guard check` and the repository map.
  - `af_brains`: the `Brain` interface, the `scripted` and `agentkit` brains, the `local` box and the submission protocol.
  - `af_triage`: `af triage` and `af triage brief|run|submit|land`.
  - `af_fix`: `af fix` and `af fix brief|run|submit|land`.
  - `af_boxes_claude`: the `landlock`, `podman` and `none` boxes, and the `claude` brain.
  
  In this scope the binary answers only `--version`, `--schema` and `help`.
- **Honouring `--dry-run` for git.** The shell exposes the flag to commands, and the specs that commit and push honour it. This scope enforces it only for the forge, because the forge is the one effect the shell hands out itself.
- **The service, hub, leases, labels as triggers, `--workspace`** (PRDs 03 to 05). **`impl`, `spec`, `review`.** **`af serve` (MCP).**
- **Changing `issuex`.** It is consumed from this module as it is.
- **A configuration file, a progress UI, or colourised output.** The shell writes nothing to stdout but the envelope (or the `--version`, `--schema` or help document), and nothing to stderr but help errors and a panic's stack.
- **A JSON Schema validation library.** The schema is a committed document, and drift is caught by comparing its `required` list with the Go struct's fields.
- **Interpreting `--context`.** The shell passes the values on verbatim. How a brief renders them is the brief's business (`af_triage`, `af_fix`).

## Background

The repository is being rebuilt from a clean state (`README.md`): the previous `triage`, `fix` and `impl` tools and the packages under them, `internal/toolio` among them, were removed, and `1e6458d` is the last commit that has them. What is in the tree today:

- `issuex/`: the forge client. It provides `Client`, `NewWithOptions`, `NewNoOp`, `ParseIssueURL`, `ParseRepo`, the error classifiers `IsNotFound`, `IsNoToken` and `IsRateLimited`, and `MaxBodyLength`/`BodyLimiter`. An unauthenticated 404 is already annotated "resource may be private" by the shared transport.
- `internal/gitx/`: hermetic git. Nothing in this scope calls it.
- There is no `cmd/` directory and no `internal/toolio`. `docs/cli.md`, `docs/configuration.md` and `docs/model-usage.md` do not exist, although `docs/architecture.md` (§The three bindings) refers to "the exit-code table `docs/cli.md` already documents".
- The `Makefile` defines `VERSION`, `COMMIT` and `BUILD_TIME`, but `build` only runs `go build ./...`. `make check` is `lint` (gofmt plus `go vet`) and then `test`.

The old `internal/toolio` is not readable from the tree. What is known of its contract comes from three sources:

- **The archived spec `.specs/archive/04_issuex_agentfox_migration/prd.md`:**
  - `Input{Kind, Origin, Body, Truncated, Issue *issuex.IssueRef, Thread *issuex.IssueThread}`, with `KindIssue = "issue"`.
  - `Resolve(ctx, arg, stdin, forge)`.
  - `RenderThread(ref, thread)`, with its exact line format.
  - Forge client construction: the issue URL's host first, then `issuex.New`, falling back to `NewNoOp`.
  - The `forge` failure category.
  - Credential messages that name `GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN`.
- **`docs/prd/01`, `docs/prd/03`, `docs/prd/05` and `docs/drafts/add_tasks_to_the_hub.md`:**
  - The status set and exit codes: `0` done, `1` failed, `2` usage, `3` needs_human, `4` unverified.
  - `needs_human.resume`.
  - `error {category, message, retryable}`, and `fix_hint {var, suggest}`.
  - `artifacts`, `side_effects`, `next`, and `usage {cost_usd, input_tokens, output_tokens}`.
  - The event types `run_start`, `step`, `tool_call`, `run_end` and heartbeat.
  - The `--output` and `--emit-events` flags.
  - An example `schema_version` of `"3.0.0"`.
- **PRD 01 §6.4:** the run-id format `YYYYMMDD_HHMMSS_<6hex>`.

This PRD writes that contract down concretely, so that the spec and its tests do not depend on history nobody can read.

## Requirements

### 1. The binary and the command shell

- `cmd/af` is `package main`. It registers commands on a `toolio.App` and exits with the code that `App.Main(ctx, args, stdin, stdout, stderr) int` returns. All logic lives in `internal/toolio`, so every behaviour below can be tested without a subprocess.
- A command is registered under its path, one or more words (`"triage"`, `"gate run"`). The shell picks the longest registered path that matches the leading non-flag arguments.
- Each command parses its own `flag.FlagSet` with `ContinueOnError`. The flag package's own output is discarded, and a parse error becomes a `usage` envelope. Flags may come before or after the positional argument (`af fix ./bug.md --dir repo` and `af fix --dir repo ./bug.md` are the same), and `--` ends flag parsing. A command takes at most one positional argument unless it declares otherwise; an extra one is a `usage` error.
- With no arguments, or with an unknown command, the shell prints a `usage` envelope whose `tool` is `"af"` and whose `error.message` lists the registered commands.
- `-h`, `--help` and `af help [command]` print plain-text help (the command list, or one command's synopsis and flags) on stdout and exit 0. This is the only output that is not JSON.
- `af --version` prints `{"name":"af","version":…,"commit":…,"build_time":…,"go_version":…}` and exits 0. The values come from `-ldflags -X` variables in `package main`. When those are unset, they come from `runtime/debug.ReadBuildInfo` (the main module's version, `vcs.revision` and `vcs.time`), and failing that, `version` is `"dev"` and the others are empty.
- `make build` additionally writes `bin/af`, built with `-ldflags` from the Makefile's existing `VERSION`, `COMMIT` and `BUILD_TIME`. `bin/` is already git-ignored.

### 2. The envelope

Every command invocation other than `--version`, `--schema` and help writes exactly one JSON object to stdout, indented by two spaces and followed by a newline. Nothing else is ever written to stdout. The fields appear in this order, and every field is always present:

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | string | `"5.0.0"` |
| `tool` | string | The command path (`"triage"`, `"gate run"`), or `"af"` when no command was resolved |
| `run_id` | string or null | The run directory's id, or null when the command has none |
| `status` | string | `done`, `failed`, `usage`, `needs_human` or `unverified` |
| `exit_code` | integer | The code the process exits with (§3) |
| `summary` | string | One line, with no newline |
| `input` | object | `{kind, origin, truncated}`. `kind` is one of `issue`, `file`, `text`, `stdin` or `none`. `origin` is the URL or path, or `""` |
| `result` | object or null | The command's own result. Its schema is the command's (§9) |
| `needs_human` | object or null | §4. Non-null if and only if `status` is `needs_human` |
| `error` | object or null | `{category, message, retryable, fix_hint}`. Non-null if and only if `status` is `failed` or `usage` |
| `warnings` | array | `[{code, message}]`, in the order they were raised |
| `artifacts` | array | `[{kind, path?, url?, name?, number?}]`. Paths are absolute |
| `side_effects` | array | `[{kind, target, state, detail?}]`. `state` is one of `done`, `planned` or `failed` |
| `next` | array | `[{argv, reason}]`: suggested follow-up commands |
| `usage` | object | `{brain, cost_usd, cost_source, input_tokens, output_tokens, turns}`. `cost_source` is one of `measured`, `reported` or `none`. With no brain, `brain` is `""`, the numbers are 0 and `cost_source` is `none` |
| `started_at`, `finished_at` | string | RFC 3339, UTC, with milliseconds |
| `duration_ms` | integer | Wall time of the invocation |

Arrays are `[]`, never null. `fix_hint` is null, or an object with any of the keys `flag`, `env`, `var` and `suggest`.

Before printing to stdout, the shell writes the same envelope to `<run_dir>/envelope.json` when there is a run directory, and to `--output <path>` when that flag is given (every command accepts it). If one of those copies cannot be written, the stdout envelope gets the warning `envelope_unwritable` or `output_unwritable`. Its status does not change, and the stdout envelope is the authoritative one.

### 3. Status, exit codes and failures

| `status` | Exit code | When |
|---|---|---|
| `done` | 0 | The command did what it was asked |
| `failed` | 1 | It could not, for a reason given in `error` |
| `usage` | 2 | The invocation was wrong: flags, arguments or input form |
| `needs_human` | 3 | It stopped on a question (§4) |
| `unverified` | 4 | Work exists but could not be verified. At least one warning says what was not verified |

How a command reports, and what the shell does with it:

- A command returns either an outcome (status `done`, `unverified` or `needs_human`, with a summary, a result, and a question if there is one) or an error.
- An error that is a `*toolio.Failure{Category, Message, Retryable, FixHint}` becomes `error`. Its category `usage` gives status `usage`; any other category gives status `failed`.
- Any other non-nil error becomes `failed` with category `internal`.
- A panic in a command is recovered. It becomes `failed`/`internal`, and the stack goes to stderr and, when there is one, to `<run_dir>/panic.txt`.
- **SIGINT and SIGTERM** cancel the command's context. If the command has not returned within 10 seconds, the shell writes `failed` with category `cancelled` and exits 1 anyway.
- **Timeouts.** A command that opts into `--timeout <duration>` (with a per-command default; 0 means no deadline) runs under that deadline. Exceeding it gives `failed` with category `timeout`, `retryable: false`, and `fix_hint {flag: "--timeout", suggest: <twice the value>}`. A duration that does not parse, or is negative, is a `usage` error.
- The shell defines these categories: `usage`, `auth`, `forge`, `io`, `cancelled`, `timeout` and `internal`. Later specs add their own, and `docs/cli.md` lists them all.
- **Warnings.** A warning code matches `^[a-z][a-z0-9_]*$`. An exact duplicate (same code, same message) is recorded once. A warning never changes the status: tracking never fails a run that is done.
- When the shell sets a status itself (usage, internal, cancelled, timeout), it fills `summary` with a one-line description.
- If writing to stdout fails, the process still exits with the status's code.

### 4. `needs_human` and `--context`

- `needs_human` is `{question, options, resume}`:
  - `options` is `[{id, label}]`, with ids `"A"`, `"B"`, … in order (at most 26). It may be empty, which means free text is expected.
  - `resume` is `{argv, flag}`. `argv` is the invocation as received, with `argv[0]` replaced by `"af"`, and `flag` is `"--context"`. Running `argv` with `flag <answer>` appended is the resume. Any `--context` values already in `argv` stay there, so answers accumulate.
- A command that opts in accepts `--context <text>`, repeatable. The values reach the command verbatim, as an ordered list, and the shell gives them no other meaning. An empty value is a `usage` error.
- The shell rejects an outcome that is inconsistent: status `needs_human` with an empty question, or a question attached to any other status. That is a command bug, and it becomes `failed`/`internal`.

### 5. The positional input

For a command that declares an input, the positional argument is classified in this order. The first rule that matches wins.

1. `-`: read from stdin until EOF. Kind `stdin`, origin `""`.
2. `issuex.ParseIssueURL` accepts it: kind `issue`, origin the issue's `IssueRef.URL()`. The thread is read with the run's forge client (§6) via `ReadIssue`, and `IsPullRequest` is set when either the URL or `thread.Issue.IsPR` says so.
3. It starts with `http://` or `https://` but is not an issue URL: a `usage` error. The message names the accepted forms, and `fix_hint` names `GITHUB_API_URL` and `GITLAB_API_URL` for self-hosted forges.
4. It names an existing regular file, relative to the process's working directory: kind `file`, origin the absolute path. A file that has a NUL byte in its first 8 KiB is a `usage` error ("not a text file"). A directory is a `usage` error. A file that exists but cannot be read is `failed`/`io`.
5. Anything else is kind `text`, origin `""`, after trimming whitespace.

A missing argument, or input that is empty after trimming, is a `usage` error.

The resolved `toolio.Input` carries:

- `Kind` and `Origin`.
- `Body`, the rendered text. For an issue, this is `RenderThread(ref, thread)`:
  - The heading line is `GitHub|GitLab|Forge <issue|pull request> <ref> (state: <state>)`, with the forge chosen from the host: `gitlab` in it gives GitLab, `github` in it or an empty host gives GitHub, and any other host gives Forge.
  - Then the lines `Title:`, `Author:` and `Labels:` (the labels line is omitted when there are none).
  - Then the trimmed body.
  - Then each comment as `\n--- comment by <login> ---\n<body>\n`.
  - Then `\n[... further comments not read ...]\n` when the thread was truncated.
- `Truncated`, `Issue` and `Thread`.

**Truncation.** `Body` is capped at 131072 bytes, cut at a UTF-8 boundary and followed by `\n[... input truncated at 131072 bytes ...]\n`. A cut, or a truncated thread, sets `input.truncated` and raises the warning `input_truncated`. If `thread.CommentsErr` is set, the warning `comments_unreadable` is raised.

**Read errors:**

| Error | Result |
|---|---|
| A 404 while the client is unauthenticated | `failed`/`auth`. The message says the issue may be private; `fix_hint {env: "GITHUB_TOKEN"}`, or `{env: "GITLAB_TOKEN"}` for a GitLab host |
| A 404 while authenticated | `failed`/`forge`, not retryable |
| A rate limit, or a transport error | `failed`/`forge`, `retryable: true` |

### 6. The forge client and `--dry-run`

The shell builds a forge client only for a command that declares it uses one. It does so through `App.NewForge` (default `issuex.NewWithOptions`; tests inject their own), with `UserAgent: "af/<version>"` and `Dir` set to `--dir`:

1. The input is an issue URL: `Options{Repo: ref.Repo}`.
2. Otherwise, the command opts into `--repo` and it is given: `issuex.ParseRepo`, where an invalid value is a `usage` error, then `Options{Repo: parsed}`. If `--repo` and an issue URL are both given and name different repositories, that is a `usage` error.
3. Otherwise `Options{}`, so that the environment, then the origin of `--dir`, decide.

If construction fails with `ErrAmbiguousForge` or `ErrUnsupportedForge`:

- When the input is an issue URL, that is a `usage` error carrying issuex's message, with `fix_hint {env: "GITHUB_API_URL"}`.
- Otherwise the command gets `issuex.NewNoOp()` and an invalid `Run.Target`. Commands that need a forge refuse in their own preflight.

`Run.Target` is the resolved repository. The client is closed when the command returns.

With `--dry-run` (opt-in per command), the client handed to the command is `toolio.DryRunForge(inner)`:

- Read methods pass through: `Authenticated`, `Close`, `GetRepository`, `ReadIssue`, `ListIssues`, `ListComments`, `ReadPullRequest`, `ReadChangedFiles`, `GetPRState`, `GetCIChecks` and `GetPRReviews`.
- `MaxBodyLength` reports the inner client's limit through `issuex.MaxBodyLength`.
- Every write method sends nothing, returns an error wrapping `toolio.ErrDryRun`, and appends a side effect `{kind: <method in snake_case>, target: <IssueRef.String() or Repo.String()>, state: "planned"}`. The write methods are `CreateIssue`, `UpdateIssue`, `CloseIssue`, `AddComment`, `AddLabels`, `RemoveLabel`, `CreateLabel`, `CreatePullRequest`, `PostReviewComment`, `MergePullRequest` and `ClosePullRequest`.

`Run.DryRun` tells the command it is a dry run, so it can skip writes deliberately. The decorator is the backstop that guarantees no write request is sent.

### 7. The run directory, the events and the ledger

**The run directory.** A command that declares one gets `<dir>/.agent-fox/run/<id>/` before its `Run` is called.

- `<dir>` is `--dir` (default `.`), made absolute. If it does not exist, or is not a directory, that is a `usage` error.
- `<id>` is `YYYYMMDD_HHMMSS_<6 lowercase hex>`: UTC time plus random bytes. If the directory already exists, a new suffix is drawn, up to 5 times; then `failed`/`io`.
- `<dir>/.agent-fox/.gitignore` with content `*\n` is created if it is missing, opened exclusively so that a concurrent run cannot clobber it. The repository's own `.gitignore` is never edited.
- The run directory is created under `--dry-run` too: it is ignored local state, and the rendered brief is what a dry run is for.
- `toolio.OpenRunDir(dir, id)` opens an existing run, for the `submit` and `land` commands of later specs. An id that does not match the format (which guards against path traversal), or a run that does not exist, is a `usage` error.
- The envelope gets `run_id`, an artifact `{kind: "run_dir", path}`, and, when a command calls `RunDir.WriteReport`, `{kind: "report", path}` for `report.md`.

**Events** are JSON lines `{seq, ts, run_id, type, data}`, with `seq` starting at 1, flushed per line.

- They are written to `<run_dir>/events.jsonl`, and appended to `--emit-events <path>` when that flag is given (every command accepts it).
- The shell emits `run_start` (`{tool, argv, input_kind}`), one `warning` per warning, and `run_end` (`{status, exit_code, duration_ms}`). Commands emit their own types (`step`, `tool_call`, `heartbeat`, …) through `Run.Emit`.
- If an event cannot be written, the warning `events_unwritable` is raised once and the run goes on.

**The ledger** is `<run_dir>/ledger.jsonl`, append-only lines `{seq, ts, kind, data}`, written with `RunDir.Record(kind, data)` and read back with `RunDir.Ledger()`. It is where later commands record what they measured. The shell itself records nothing in it.

### 8. Help, version and schema take precedence

When `--schema`, `--version` or `--help` appears anywhere on a command line, the shell answers it without resolving input, building a forge client or creating a run directory.

### 9. `--schema`

- `af --schema` prints the envelope's JSON Schema (draft 2020-12): every field of §2, with `schema_version` as `const: "5.0.0"`, and `required` listing every field in envelope order. `result` is open. Each registered command's result schema appears under `$defs`, keyed by the command path with spaces replaced by `_`.
- `af <command> --schema` prints the same schema narrowed to that command: `tool` is `const` the command path, and `result` is null or the command's own schema.
- The base schema is a file embedded with `embed`, `internal/toolio/schema/envelope.schema.json`. The golden files `internal/toolio/testdata/schema.golden.json` (no commands registered) and `cmd/af/testdata/schema.golden.json` (the real binary) are committed, and regenerated with `go test ./... -update`.
- A test fails when the schema's `required` list differs, in names or in order, from the JSON tags of `toolio.Envelope`.

### 10. Documentation

The steering directive requires these updates in the same change:

- **`docs/cli.md`** (new): `af` and its global flags; the envelope field by field; the status and exit-code table; the error categories and warning codes; `needs_human` and `--context`; `--dry-run` semantics; `--schema`, `--version` and help; the run directory layout.
- **`docs/configuration.md`** (new): the environment variables `af` reads through `issuex` (`GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`, `GITHUB_API_URL`, `GITLAB_API_URL`) and the `.agent-fox/` directory.
- **`docs/model-usage.md`** (new): states that `af` runs no model and reads no vendor key in this release.
- **`docs/architecture.md`**: fill the "run directory layout and the ledger file formats" item of *Sections to be written*, and correct the claim that `docs/cli.md` already exists.
- **`README.md`**: add `cmd/af/` and `internal/toolio/` to *What is here*, and `bin/af` to *Building*.
- Run `make doc` and commit `docs/generated/`.

## Design Decisions

1. **Split the input into six specs, and make this the first.** The PRD covers six areas that can each be built and tested independently: the shell, ring 1, the brains, triage, fix, and the boxes. That is well over 10 requirements and 12 tasks, and every later scope needs the envelope first.
2. **Define the envelope here rather than "revive as it was".** `1e6458d` is not readable from the tree, so the field set and order are written down from the old contract as the archived spec and PRDs 01, 03 and 05 record it. If the implementer recovers the old code and finds a field missing here, it is reported in an erratum, not added silently.
3. **`schema_version` is `"5.0.0"`.** PRD 02 asks for the next major version after the old one. The old tools' last value was `"4.0.0"` (`1e6458d:internal/toolio/envelope.go`, bumped from 3.1.0 on 2026-10-14), not the hub tasks draft's example `"3.0.0"`, which was written before it. This envelope removes, renames and retypes fields of the old one (`docs/errata/05_old_envelope_fields.md` lists them), which the old interface rule counts as breaking, so the next major is `"5.0.0"` and a consumer that reads `schema_version` can tell the two shapes apart.
4. **Every field is always present; arrays are `[]` and inapplicable objects are `null`.** A consumer (hub, the service, a model) never has to tell "missing" from "empty", and Go's struct order fixes the field order.
5. **Indented JSON on stdout.** People read it in a terminal, and every JSON parser accepts it.
6. **Help is plain text on stdout with exit 0, the one non-JSON output.** A person asked for it. `--version` and `--schema` stay JSON because tools consume them.
7. **The stdlib `flag` package, with interspersed flags handled by the shell.** The steering prefers the standard library, `go.mod` has no CLI framework, and PRD 02's own examples put flags after the positional argument.
8. **A command path of one or more words is the `tool` value.** That gives `triage` and `fix` for the loops, as PRD 02 asks, and stays unambiguous for `gate run` and `triage submit`.
9. **A non-`Failure` error and a panic are both `failed`/`internal`.** The process always prints one envelope; a crash without one breaks every caller's parser.
10. **`--timeout` maps to `timeout`, not `budget`, and is not retryable.** The same input under the same bound will most likely time out again. The `fix_hint` says what to raise instead.
11. **`needs_human.resume` is `{argv, flag}`, not a shell string.** An argv cannot be mis-quoted, and the answer is unknown when the envelope is printed.
12. **`--context` is verbatim and accumulates.** The shell holds no state between runs, and how an answer such as "B" is presented to a brain belongs to the brief.
13. **An `http(s)` argument that is not an issue URL is a `usage` error, not text.** Treating a mistyped or self-hosted URL as a problem description would spend a model run on a URL string.
14. **An existing file name wins over text.** That matches PRD 02's `af fix ./bug.md` example. Paths resolve against the process's working directory, as a shell user expects.
15. **The input cap is 128 KiB, with a visible marker and a warning.** That is bounded for a brief, and twice GitHub's largest body (65536), so an issue body plus its comments usually fits whole.
16. **`--repo` that conflicts with an issue URL is a `usage` error.** Reading the issue and writing to another repository would need two clients and a policy this scope has no use for.
17. **Under dry run, reads are allowed, writes return `ErrDryRun`, and the run directory is created.** PRD 02's "sends no request to the forge" can only mean writes, since a triage dry run must read the issue to diagnose it. An error is safer than a fake success: a command cannot report a pull request number that does not exist. The run directory is ignored local state.
18. **The dry-run guarantee is a client decorator, not a convention.** It is the one place every forge write passes, so a test at an `httptest` server proves it for every command, present and future.
19. **The run directory's own `.agent-fox/.gitignore` contains `*`.** It is ignored without touching the repository's `.gitignore`, as PRD 02 §2 says. Opening the file exclusively makes concurrent runs safe.
20. **The run directory goes under `--dir` as given, with no git root discovery.** That keeps the shell free of git, and later specs check that `--dir` is a checkout where it matters.
21. **Artifact paths are absolute.** A caller in another working directory, such as the service or a hub runner, can use them as they are.
22. **Drift between the schema and the struct is caught by comparing `required` with the struct's JSON tags.** That needs no new dependency, which the steering asks for.
23. **No `docs/api.md` in this scope.** This scope adds no REST endpoint and no public Go package: `internal/toolio` is internal and `cmd/af` is `main`.
24. **`issuex` is consumed as code, with no spec dependency recorded.** Its specs live in `.specs/archive/` and are fully implemented, and this scope changes nothing in it.

## Verified External API

This scope uses only the standard library and `issuex`. `issuex` lives in this module today (`github.com/agent-fox-dev/agentfox/issuex`) and moves to its own module later (PRD 01, D9). Every signature below was read in the repository:

| Symbol | Signature | File |
|---|---|---|
| `issuex.Client` | interface: `Authenticated() bool`, `Close() error`, `GetRepository`, `CreateIssue(ctx, Repo, CreateIssueRequest) (Issue, error)`, `ReadIssue(ctx, IssueRef) (IssueThread, error)`, `UpdateIssue(ctx, IssueRef, UpdateIssueRequest) (Issue, error)`, `CloseIssue(ctx, IssueRef, string) error`, `ListIssues`, `AddComment(ctx, IssueRef, string) (string, error)`, `ListComments`, `AddLabels(ctx, IssueRef, []string) error`, `RemoveLabel(ctx, IssueRef, string) error`, `CreateLabel(ctx, Repo, Label) error`, `CreatePullRequest(ctx, Repo, CreatePullRequestRequest) (PullRequest, error)`, `ReadPullRequest`, `ReadChangedFiles`, `GetPRState`, `GetCIChecks`, `GetPRReviews`, `PostReviewComment(ctx, IssueRef, string) error`, `MergePullRequest(ctx, IssueRef, MergeOptions) (MergeResult, error)`, `ClosePullRequest(ctx, IssueRef) error` | `issuex/client.go` |
| `issuex.NewWithOptions` | `func NewWithOptions(o Options) (Client, error)` | `issuex/factory.go` |
| `issuex.Options` | `struct{ BaseURL, Token string; HTTPClient *http.Client; UserAgent string; NoOp bool; Repo Repo; RemoteURL string; Dir string }` | `issuex/options.go` |
| `issuex.NewNoOp` | `func NewNoOp() Client` | `issuex/noop.go` |
| `issuex.NewGitHub` / `NewGitLab` | `func NewGitHub(o Options) (Client, error)` (the same for GitLab) | `issuex/github.go`, `issuex/gitlab.go` |
| `issuex.ParseIssueURL` | `func ParseIssueURL(s string) (IssueRef, bool)` | `issuex/parse.go` |
| `issuex.ParseRepo` | `func ParseRepo(s string) (Repo, bool)` | `issuex/parse.go` |
| `issuex.IssueRef` | `struct{ Repo Repo; Number int; IsPullRequest bool }`, with `String()` and `URL()` | `issuex/types.go` |
| `issuex.IssueThread` | `struct{ Issue Issue; Comments []Comment; Truncated bool; CommentsErr error }` | `issuex/types.go` |
| `issuex.IsNotFound` / `IsRateLimited` / `IsNoToken` | `func(err error) bool` | `issuex/errors.go` |
| `issuex.ErrAmbiguousForge` / `ErrUnsupportedForge` | sentinel `error` values | `issuex/errors.go` |
| `issuex.MaxBodyLength` / `BodyLimiter` | `func MaxBodyLength(c Client) int`; `interface{ MaxBodyLength() int }` | `issuex/bodylimit.go` |
