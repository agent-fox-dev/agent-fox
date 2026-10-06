---
spec_id: "17"
spec_name: "issue_tool"
title: "Add the issue tool"
status: "active"
created_at: "2026-10-06T17:46:08.886275Z"
updated_at: "2026-10-06T17:46:08.886275Z"
intent_hash: "ab63e82be2939cec7ad95c707f149bd65e33134a56be49181f3779c76bddfe3c"
schema_version: 2
source: "docs/prds/08-add-the-issue-tool.md"
---
## Intent

Add a fifth tool, `issue`, that exposes every operation of `issuex.Client` as a command-line program on the same terms as the four existing tools (`triage`, `fix`, `spec`, `impl`): flags on the command line, credentials from the environment, one JSON envelope on stdout, shared exit codes, report and events files, `--dry-run`, `--output` and `--schema`. The tool has no model phase and makes one forge call per invocation.

## Goals

- Every method of `issuex.Client` (21 operations, `Close` excluded) can be invoked from a shell, on GitHub and GitLab alike, with forge, host and token resolved exactly as the four tools resolve them via `issuex.NewWithOptions`.
- The tool produces the shared envelope (`ok`, `status`, `exit_code`, `error.category` with `retryable` and `fix_hint`, `warnings`, `artifacts`, `side_effects`, `timings`), report file, events file, and supports `--detail`, `--output`, `--dry-run`, `--emit-events` and `--schema` with the same semantics.
- A write is refused before any request when it cannot succeed: missing token, unparseable target, a flag the operation does not take.
- Text the forge returned from external authors (titles, bodies, comments, reviews, check summaries) is labelled `external` and listed in `untrusted_fields`.
- The tool needs no model credential. `ANTHROPIC_API_KEY` is not read and its absence is not an error.
- The four existing tools' behaviour, flag sets, envelopes and `--schema` golden files are unchanged.
- `internal/toolio` gains a no-model-phase execution path without duplicating the envelope, exit-code or emit logic.

## Non-goals

- **A model phase.** `issue` never calls a model. Nothing in it needs judgment.
- **A verb / subcommand.** The operation is selected with `--op`, not a subcommand, per ADR 03's "every addition is a flag" rule.
- **New forge capabilities.** Inline review comments, GraphQL, reactions, milestones, Bitbucket, or anything outside `issuex.Client` today.
- **Credentials on the command line.** No `--token` flag.
- **Serving `issue` over MCP.** PRD 04 lists the binaries its adapter runs; adding `issue` is a later one-line change there.
- **Pagination beyond `issuex`'s caps.** `ListIssues` returns at most `--limit` issues; `ListComments` returns at most 500. The tool reports both limits and does not page further.
- **Renaming `triage` or `issuetriage`, or changing the `issue` input kind.**
- **Tracking-issue workflow (PRD 12 `links`).** PRD 12 introduces `internal/tracking` and a `links` section on `read` results. That package does not exist yet. When PRD 12 is implemented, `issue --op read` gains a `links` section using `internal/tracking`'s matchers. This spec does not implement `links`.

## Background

### The existing shell

Every tool is a `toolio.App` value run by `App.Main`. `Main` parses flags, handles `--version`, `--schema` and a bare invocation, opens the events sink, then calls `execute`, which runs `PreCheck`, resolves `--dir`, classifies and fetches the positional input (reading an issue URL from the forge with comments), runs `CheckInput`, **resolves the model and checks its vendor credential**, builds a `Runner`, then calls `Exec`. The flag set `Common.Register` installs includes model-run flags: `--model`, `--vendor`, `--effort`, `--max-turns`, `--budget`, `--phase-timeout`, `--total-budget`, `--context`, `--trust-project`, `--show-text`, `--input-kind`, `--preflight`, `--repo-map-tokens`.

Three things in that path are wrong for a tool without a model:

1. **The model is resolved on every run**, and its credential checked, before `Exec`. Through this path, `issue --op read <url>` would fail with an `auth` error naming `ANTHROPIC_API_KEY`.
2. **The positional is fetched as an input.** For `issue` the positional is a target to act on, not text to read: a URL would be fetched with all comments before a `close` that never needed them.
3. **A forge client that cannot be built falls back to `issuex.NewNoOp()`.** For `issue`, a no-op client's writes returning zero values would silently report success.

### Stale references

The `SinglePhase` field comment on `App` (app.go line 121) and the `SinglePhaseBounds` method comment (cli.go line 350) say "issue" where they mean "triage" — left over from the rename at commit `d8d8db5`. In `docs/cli.md`, the warning-codes table attributes `tool_errors` and `rejected_path_calls` to `issue` (meaning `triage`), and the `--total-budget` row mentions an unsplit `spec` input "folds like `issue`" (also meaning `triage`). These are corrected by this spec because once a tool called `issue` exists they would read as statements about it.

### The tool pattern

Each existing tool's domain logic lives in its own package (`issuetriage`, `codefix`, `codeimpl`, `specgen`), with its `Result` type, pipeline function, artifacts, summary and trust tags. The `cmd/<tool>/main.go` file constructs a `toolio.App` value and calls `os.Exit(app.Main(...))`. The `issue` tool follows the same pattern with a new `issueops` package.

## Requirements

### 1. Program and package structure

`cmd/issue/main.go` is a fifth tool built by `make build` and cross-built by `make build-all` (the `TOOLS` variable gains `issue`). It is installed by `install.sh` (the `TOOLS` default gains `issue`) and copied into the tools container. The tool's operations live in `issueops/` at the repository root, following the pattern of `issuetriage/`, `codefix/`, `codeimpl/` and `specgen/`. The `cmd/issue/main.go` entry point constructs a `toolio.App` value and calls `os.Exit`. The envelope's `tool` field is `"issue"`; report and events files are named `issue-<started_at>-<session_id>.json` and `.jsonl`; the progress prefix on stderr is `issue:`.

### 2. Operation selection via `--op`

`--op <name>` selects the operation. It is required on every run that does work; a run without it is a usage error (exit 2) listing the operations. It is declared as an enum (`DeclareEnum`), so `--schema` lists every value. The 21 operations, one per `issuex.Client` method: `auth`, `repo`, `create`, `read`, `update`, `close`, `list`, `comment`, `comments`, `label`, `unlabel`, `create-label`, `create-pr`, `pr`, `files`, `pr-state`, `checks`, `reviews`, `review-comment`, `merge`, `close-pr`. `auth` makes no forge call; it reports the forge type, API URL, authentication status and resolved repository. A host that names neither `github` nor `gitlab` is not probed: `issuex.NewWithOptions` returns `ErrAmbiguousForge` as documented in the unclassified-host-probe erratum. `Close` (the lifecycle method) is called by the program after every run and is not an operation.

### 3. Target parsing

The single positional names what the operation acts on. Its shape is decided in Go before any request. A GitHub or GitLab issue or pull/merge-request URL is an issue or PR ref with the kind fixed by the URL path. `owner/repo#N` or `group/subgroup/project#N` is a ref whose kind the operation decides. `owner/repo` or `group/subgroup/project` (via `issuex.ParseRepo`) is a repository. A bare positive integer `N` is a ref in the repository `--repo` names, else the `origin` remote of `--dir`. An absent positional means the repository `--repo` names or the `origin` remote, accepted only by repository operations. Anything else is a usage error (exit 2). A target whose kind does not fit the operation is a usage error naming the operation that would fit. `--repo` with a URL or an `owner/repo#N` target is a usage error. A repository operation with no positional and no resolvable `origin` is a usage error naming `--repo`. The envelope reports the target as `result.target`, not as `input`; the `input` field is absent. The host of a URL must be one `issuex.ParseIssueURL` accepts.

### 4. Forge and credential resolution

The client is built by `issuex.NewWithOptions` from the target's repository host, the `origin` remote of `--dir`, and the environment, in that order — the same `detectForge` path the four tools use, so `GITHUB_API_URL`, `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_API_URL` and `GITLAB_TOKEN` have the same meaning. There is no no-op fallback: a client that cannot be built is a failure. `ErrAmbiguousForge` and `ErrUnsupportedForge` are reported as `category: "usage"` (exit 2) with a `fix_hint` naming `GITHUB_API_URL` and `GITLAB_API_URL`. A write operation on a client whose `Authenticated()` is false is refused before any request, as `category: "auth"`, with `fix_hint.env` naming the three token variables. A read operation runs unauthenticated when no token is set. The model is never resolved and no vendor key is read.

### 5. Flag set

`issue` takes the shell flags it needs and its own; it does not take the model-run flags. **Shared, kept:** `--dir`, `--dry-run`, `--detail`, `--report-file`, `--output`, `--emit-events`, `--verbose`, `--quiet`, `--version`, `--schema`. **Shared, not accepted:** `--model`, `--vendor`, `--effort`, `--max-turns`, `--budget`, `--phase-timeout`, `--total-budget`, `--context`, `--trust-project`, `--show-text`, `--input-kind`, `--preflight`, `--repo-map-tokens`. Each rejected flag is a usage error naming the tools that accept it, through the `unsupportedFlagMessage` table.

**Own flags:** `--op` (all operations), `--repo` (repository when the target does not name one), `--title` (for `create`, `update`, `create-pr`), `--body` (for `create`, `update`, `close`, `comment`, `review-comment`, `create-pr`), `--body-file` (same as `--body`, exclusive with it; reads from a file or stdin when path is `-`), `--label` (comma-separated for `create`, `update`, `label`, `list`; single for `unlabel`, `create-label`), `--assignee` (comma-separated for `create`, `update`; single for `list`), `--state` (for `list`, `update`), `--sort`, `--direction` (for `list`), `--limit` (for `list`, default 100), `--color`, `--description` (for `create-label`), `--head` (for `create-pr`, required), `--base` (for `create-pr`, default the repo's default branch), `--draft` (for `create-pr`), `--method` (for `merge`), `--commit-title`, `--commit-message`, `--sha` (for `merge`).

Every own flag is checked against its operation in `PreCheck` before any request: a flag the operation does not take is a usage error. A `comment` or `review-comment` without `--body` or `--body-file`, a `create` without `--title`, a `create-pr` without `--title` or `--head`, an `update` with nothing to change, and a `label` with no labels are each usage errors. An empty body after reading `--body-file` is a usage error too.

### 6. Result type and output

One `Result` type in `issueops/`, with a fixed head and one section per operation; a section is present only for the operation that fills it. Every string field carries a `trust` tag. The head includes: `op` (fact), `forge` (fact), `api_url` (fact), `authenticated` (bool), `target` (fact), `dry_run` (bool). Operation-specific sections mirror `issuex` types with appropriate trust labels: titles and bodies are `external`, structural fields (state, URL, timestamps, login, labels) are `fact`. `CheckRun.Summary` and `Review.Body` are `external`; `MergeResult.Message` is `external`.

`result.detail` records the view. The summary view (`--detail summary`, the default) differs from full only for `list` (drops issue bodies) and `read` (drops `rendered` thread text). `summary` (the envelope's one-line sentence) names the operation and target: e.g. `issue: created acme/widgets#43`. `untrusted_fields` lists every non-empty `external` field.

A `read` carries `rendered` (`external`): the thread as `toolio.RenderThread` renders it for a model, so a skill gets the same text the four tools would see.

### 7. Dry-run, artifacts, side effects and timings

**Dry-run:** A read operation under `--dry-run` runs exactly as without it. A write under `--dry-run` resolves the target, builds the client, and stops before the request; it reports `result.would_write` (the request struct) with `"dry_run": true`, and records no side effect. Implied reads still happen (e.g. `create-pr` reads default branch).

**Artifacts:** `create` yields `{kind: issue, url, number}`, `create-pr` yields `{kind: pull_request, url, number}`, `comment` yields `{kind: comment, url}`, `merge` yields `{kind: commit, sha, branch}`. Other operations yield only `report_file` and `events_file`. No new artifact kind.

**Side effects:** One entry per write, using existing actions where they fit (`create_issue`, `update_issue`, `comment`, `open_pr`) and new values where needed: `close_issue`, `add_labels`, `remove_label`, `create_label`, `review_comment`, `merge_pr`, `close_pr`. `kind` is never set on an `issue` write.

**Timings:** One `{kind: forge, name: <op>}` entry per forge call, including implied reads.

**Next:** Empty. `issue` is a primitive; which tool runs next is the caller's decision.

### 8. Error categories and exit codes

Exit codes are `0`, `1` and `2`; `--schema` lists those three. Stages are `target` (parsing), `auth` (credential check) and `call` (the request). Error mapping from `issuex`: `ErrNoToken` or the pre-write credential refusal → `auth` (not retryable); `ErrAmbiguousForge`/`ErrUnsupportedForge` → `usage` (exit 2, not retryable); `ErrNotFound` / HTTP 404 → `not_found` (new, not retryable); `ErrConflict` / HTTP 405, 406, 409 → `conflict` (new, not retryable); `ErrRateLimited` → `rate_limited` (new, retryable); any other `HTTPError` or transport error → `forge` (not retryable). `RetryableFor` in `envelope.go` gains `rate_limited`. Two new warning codes, both `low`, at stage `call`: `comments_truncated` (when `ListComments` or `ReadIssue` hit 500-comment cap) and `list_incomplete` (when `ListIssues` hit `--limit`). The existing `comments_unreadable` is recorded when `ReadIssue` returns a thread with `CommentsErr` set.

### 9. Shell changes to `internal/toolio`

`toolio.App` gains a way to declare that it has no model phase. For such an App, `execute` runs `PreCheck`, resolves `--dir` to a directory, and calls `Exec` with `Deps` whose `Workspace`, `Runner`, `Model` and `Index` are nil and whose `Forge` the tool builds itself. It does not classify an input, resolve a model, or build a runner. Everything else in `Main` and `emit` (flag parsing, `--version`, `--schema`, the bare-invocation rule, the events sink, report file, `--output`, the envelope) is the same code path.

`Common.Register` is split so the model-run flags are registered separately, and `issue` registers only the shell subset. The four tools register both and see no change. `toolFlags` and `toolOrder` gain `"issue"`, and the model-run flags are attributed to the four tools in the unsupported-flag table. `Envelope.Tool`'s description and the `run_start` event documentation name five tools. `run_start` for `issue` omits `model` and sets `input_kind` to the target's form (`url`, `ref`, `repo`, `number` or `default`).

The `SinglePhase` field comment on `App` and the `SinglePhaseBounds` method comment on `Common` are corrected from "issue" to "triage". In `docs/cli.md`, the warning-codes table entries attributing `tool_errors` and `rejected_path_calls` to `issue` are corrected to `triage`, and the `--total-budget` row's "folds like `issue`" is corrected to "folds like `triage`".

## Design Decisions

1. **The operation is a flag, not a verb.** `issue --op read <target>`, not `issue read <target>`. ADR 03 fixes "every addition is a flag" for the tools' interface. `--schema` describes a flat flag set, so a flag with an enum is listed and validated for free, and an MCP adapter built on `--schema` sees one tool with one input schema. `SplitArgs`, the unsupported-flag table and the bare-invocation rule all assume one positional.
2. **The positional is the target, and the body is a flag.** The four tools' positional is "the thing to read from", and for `read` that would be the URL, but for `comment` it would be the body and the URL would become a flag. One rule for every operation — the positional is what you act on — is simpler.
3. **The target may be absent for a repository operation.** `issue --op list` in a checkout should list that repository's issues, without requiring the user to type `acme/widgets` when `origin` already says so.
4. **No `NoOpClient`, ever.** The fallback exists so `spec ./idea.md` works with no remote. `issue` has no local-only path; a no-op write reporting success is the one outcome a forge tool must not have.
5. **Strict target kinds.** GitHub's leniency about issue numbers vs pull request numbers is a GitHub fact. The tool is forge-neutral, so it refuses on both what GitLab would get wrong on one.
6. **`--dry-run` instead of `--preflight`.** `--preflight` exists to run checks without paying for the model. `issue` has no model; `--dry-run` on a write already runs every check and stops before the request.
7. **`rendered` on `read`.** The thread rendering the four tools feed to a model is a pure function (`toolio.RenderThread`); giving it to a skill costs nothing and saves re-rendering.
8. **Three new categories rather than `forge` for everything.** A skill that can branch on `not_found` and `conflict` without parsing a message, and that is told `rate_limited` is retryable, is the point of a decidable envelope (PRD 01).
9. **Shell split, not a second shell.** The envelope, exit codes, report file, events, `--output` and `--schema` are what make this a fifth tool rather than a fifth way to call a forge. They stay one implementation.
10. **`links` deferred to PRD 12.** PRD 12 introduces tracking-issue grammar and an `internal/tracking` package that does not exist yet. Adding a `links` section to `read` results depends on that package. This spec does not implement it; when PRD 12 lands, `issue --op read` gains `links` as a single addition.
11. **`side_effects[].action` is an open set.** The field's current description lists five values without saying whether the set is closed. This spec amends it to say the set is open, which is what callers already assume for `category` and `code`. Under ADR 06 this is additive and needs no version bump.
12. **No `next` entries.** `issue` is a primitive. A `create` could suggest `fix`, but the body is whatever the caller wrote and not every issue is a bug. A `read` could suggest tools based on the issue's content, but that requires judgment the tool does not have without PRD 12's `links`. Left empty.
13. **`run_start` for `issue` sets `input_kind` to the target form.** The existing field on the event means "how the input was classified". For `issue`, which has no input classification, the target's shape (`url`, `ref`, `repo`, `number`, `default`) is used instead, keeping the field informative without inventing a new event field.

## Dependencies

| Spec | Reason |
|---|---|
| `01_issuex_core` | `issuex.Client` interface and all types the tool dispatches to |
| `02_issuex_github` | GitHub adapter registered via `issuex.RegisterAdapter` |
| `03_issuex_gitlab` | GitLab adapter registered via `issuex.RegisterAdapter` |
| `05_envelope_decidable` | Envelope structure, exit codes, `error.category`, `retryable`, `fix_hint` |
| `09_tool_self_schema` | `--schema`, `DeclareEnum`, `BuildFlagsDocument`, `BuildResultDocument`, golden-file testing |
| `10_untrusted_text_labels` | Trust tags, `CheckTrust`, `UntrustedFields` |

## Verified External API

All external API symbols are from packages inside this repository.

| Symbol | Signature | Source |
|---|---|---|
| `issuex.Client` | Interface with 21 methods + `Authenticated() bool` + `Close() error` | `issuex/client.go` L7–L38 |
| `issuex.NewWithOptions` | `func(Options) (Client, error)` | `issuex/factory.go` L19 |
| `issuex.ParseRepo` | `func(string) (Repo, bool)` | `issuex/parse.go` L14 |
| `issuex.ParseIssueURL` | `func(string) (IssueRef, bool)` | `issuex/parse.go` L151 |
| `issuex.DetectRepo` | `func(string) (Repo, bool)` | `issuex/parse.go` L138 |
| `issuex.ErrNoToken` | `var error` | `issuex/errors.go` L15 |
| `issuex.ErrNotFound` | `var error` | `issuex/errors.go` L18 |
| `issuex.ErrConflict` | `var error` | `issuex/errors.go` L21 |
| `issuex.ErrRateLimited` | `var error` | `issuex/errors.go` L24 |
| `issuex.ErrAmbiguousForge` | `var error` | `issuex/errors.go` L30 |
| `issuex.ErrUnsupportedForge` | `var error` | `issuex/errors.go` L27 |
| `issuex.IsNotFound` | `func(error) bool` | `issuex/errors.go` L84 |
| `issuex.IsConflict` | `func(error) bool` | `issuex/errors.go` L100 |
| `issuex.IsRateLimited` | `func(error) bool` | `issuex/errors.go` L116 |
| `issuex.IsNoToken` | `func(error) bool` | `issuex/errors.go` L136 |
| `issuex.Options` | Struct: `BaseURL, Token, HTTPClient, UserAgent, NoOp, Repo, RemoteURL` | `issuex/options.go` L8 |
| `issuex.ForgeTypeGitHub` | `const ForgeType = "github"` | `issuex/detect.go` L18 |
| `issuex.ForgeTypeGitLab` | `const ForgeType = "gitlab"` | `issuex/detect.go` L20 |
| `toolio.App` | Struct with `Name, Version, Usage, Flags, Exec, PreCheck, ...` fields | `internal/toolio/app.go` L107–L160 |
| `toolio.Common` | Struct with shared flags | `internal/toolio/cli.go` L52–L110 |
| `toolio.DeclareEnum` | `func(*flag.FlagSet, string, []string)` | `internal/toolio/flagschema.go` L18 |
| `toolio.Usagef` | `func(string, ...any) error` | `internal/toolio/app.go` L37 |
| `toolio.RenderThread` | `func(issuex.IssueRef, issuex.IssueThread) string` | `internal/toolio/input.go` L264 |
| `toolio.UntrustedFields` | `func(any) []string` | `internal/toolio/untrusted.go` L22 |
| `toolio.CheckTrust` | `func(reflect.Type) []string` | `internal/toolio/trust.go` L16 |
| `toolio.RetryableFor` | `func(string) bool` | `internal/toolio/envelope.go` (currently checks `"api"` and `"aborted"`) |
| `toolio.WarnCode` | `type string` | `internal/toolio/warncode.go` L5 |
| `toolio.SideEffect` | Struct: `Action, Target, Kind, URL, OK, Warning` | `internal/toolio/envelope.go` |
| `toolio.Artifact` | Struct with `MarshalJSON` | `internal/toolio/envelope.go` |
