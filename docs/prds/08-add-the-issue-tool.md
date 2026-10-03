# Add the `issue` tool

## Intent

`issuex` is the one path from this repository to a forge: a single `Client`
interface over GitHub and GitLab with twenty operations on repositories,
issues, comments, labels and pull or merge requests. Today only four of them
are reachable from a shell, and only as a side effect of running a pipeline:
`triage` creates or rewrites an issue, `fix` and `impl` open a pull request,
`fix` and `spec` post a comment. An agent that wants to read an issue thread,
list the open bugs, close an issue with a note, check a pull request's CI, or
merge it has to shell out to `gh` or `glab`, carry a forge client of its own,
or ask a person. None of those is forge-neutral, none produces the envelope
the other tools produce, and none honours the credentials and host variables
the four tools already read.

This PRD adds a fifth program, `issue`, that exposes every operation of
`issuex.Client` on the same terms as the four tools: flags on the command
line, credentials from the environment, one JSON object on stdout, the shared
exit codes, the report and events files, `--dry-run`, `--output` and
`--schema`. It is a primitive for a skill to call, not a pipeline: there is no
model in it, and a run is one forge call (two where the second is implied by
the first, such as reading a repository's default branch before opening a pull
request against it).

### Not the tool that was renamed

Until [commit `d8d8db5`](../../cmd/triage/main.go) the triage tool was called
`issue`: `cmd/issue` and the `issue` binary triaged a problem report and filed
a structured issue. That tool is now `triage`; its package is still
`issuetriage`. The name `issue` was freed by the rename, and this PRD reuses
it for something else: a tool whose subject is *the issue itself*, not a
diagnosis.

The old name survives in places that are deliberately history and in a few
that are not:

- ADRs 03 to 06 and PRDs 01 to 06 say `issue` where they mean `triage`. They
  are the historical record and stay as written.
- `docs/cli.md`'s warning-codes table attributes `tool_errors` and
  `rejected_path_calls` to `issue`, and the `--total-budget` row says an
  unsplit `spec` input "folds like `issue`". Those three mean `triage` and
  were missed by the rename; this spec corrects them, because once a tool
  called `issue` exists they would read as statements about it.
- Comments in `internal/toolio/app.go` (`SinglePhase`) and `cli.go`
  (`SinglePhaseBounds`) name `issue` as the one-phase tool. Same correction.

Nothing about `triage`, `issuetriage` or the `issue` *input kind* changes.

## Goals

- Every method of `issuex.Client` can be invoked from a shell, on GitHub and
  GitLab alike, with the forge, host and token resolved exactly as the four
  tools resolve them: `GITHUB_TOKEN`/`GH_TOKEN` and `GITHUB_API_URL`,
  `GITLAB_TOKEN` and `GITLAB_API_URL`, the input URL, the `origin` remote.
- The tool speaks the shared interface: the envelope, `ok`/`status`/`exit_code`,
  `error.category` with `retryable` and `fix_hint`, `warnings`, `artifacts`,
  `side_effects`, `timings`, the report and events files, `--detail`,
  `--output`, `--dry-run`, `--emit-events`, `--schema` and the golden file
  that keeps it honest.
- A write is refused before any request is sent when it cannot succeed: a
  missing token, an unparseable target, a flag the operation does not take.
- Text the forge returns that a stranger wrote (titles, bodies, comments,
  reviews, check summaries) is labelled `external` and listed in
  `untrusted_fields`, so a calling model is told what is data.
- The tool needs no model credential. `ANTHROPIC_API_KEY` is not read, not
  checked, and its absence is not an error.
- The four tools' behaviour is unchanged. What they share with `issue` is
  factored out of `internal/toolio`, not duplicated.

## Non-goals

- **A model phase.** `issue` never calls a model. Nothing in it needs
  judgment; everything it does is a REST call whose arguments the caller
  supplied.
- **A verb.** `issue` selects its operation with a flag, not a subcommand
  (design decision 1). The four tools gain no subcommand either, as
  [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) requires.
- **New forge capabilities.** Inline review comments, GraphQL, reactions,
  assignment changes beyond what `UpdateIssueRequest` carries, milestones,
  projects and Bitbucket are outside `issuex` today and stay outside. A
  capability `issuex` gains later is a one-operation addition here.
- **Credentials on the command line.** No `--token` flag. A token in `argv`
  is visible to every process on the machine; the environment variables the
  four tools read are the only source.
- **Serving `issue` over MCP.** [PRD 04](04-serve-the-tools-over-mcp.md)
  lists the binaries its adapter runs; adding `issue` there is a one-line
  change to that PRD once both exist, not part of this one.
- **Pagination beyond `issuex`'s caps.** `ListIssues` returns at most
  `--limit` issues (default 100) and says when more exist; `ListComments`
  returns at most 500 comments and says when it cut. The tool reports both
  facts and does not page further.
- **Renaming `triage` or `issuetriage`, or changing the `issue` input kind.**

## Background: what the shell does that `issue` must not

Every tool is a `toolio.App` value run by `App.Main`. `Main` parses the flags,
handles `--version`, `--schema` and a bare invocation, opens the events sink,
and calls `execute`, which does, in order: `PreCheck`, resolve `--dir` into a
workspace, classify and fetch the positional input (an issue URL is read from
the forge here, comments and all), `CheckInput`, **resolve the model and check
its vendor credential**, build a `Runner`, then call `Exec`. The flag set
`Common.Register` installs is the same for every tool, and includes `--model`,
`--vendor`, `--effort`, `--max-turns`, `--budget`, `--phase-timeout`,
`--total-budget`, `--context`, `--trust-project`, `--show-text`, `--input-kind`
and `--preflight`.

Three things in that path are wrong for a tool without a model, and they are
the reason this PRD touches `internal/toolio` at all:

1. **The model is resolved on every run**, and its credential checked, before
   `Exec`. Through this path, `issue --op read <url>` would fail with an
   `auth` error naming `ANTHROPIC_API_KEY`.
2. **The positional is fetched as an input.** For `issue` the positional is a
   target to act on, not text to read: `acme/widgets` would be classified as
   text and warned about as a path that does not exist; an issue URL would be
   fetched with all its comments before a `close` that never needed them.
3. **A forge client that cannot be built falls back to `issuex.NewNoOp()`.**
   That is right for `spec` on a local file. It is wrong for `issue`: the
   no-op client's issue writes return zero values and no error, so a run
   that could not reach a forge would report `created` with number `0`.

The eleven model-run flags are also wrong to accept: `--schema` would
advertise `--budget` on a tool that spends nothing, and a caller passing
`--model` would be silently ignored rather than told.

## Functional requirements

### 1. The program

- `cmd/issue` is a fifth tool. It is built by `make build`, cross-built by
  `make build-all` (`TOOLS` gains `issue`), installed by `install.sh`
  (`TOOLS` default gains `issue`), and copied into the tools container.
- Its operations live in a package of their own, `issueops`, as the other
  tools' do (`issuetriage`, `codefix`, `codeimpl`, `specgen`): the target
  parsing, the per-operation flag validation, the dispatch onto
  `issuex.Client`, the `Result` type, and the artifacts and side-effect
  mapping. `cmd/issue/main.go` is the `toolio` value and an `os.Exit`, as the
  other four are.
- The envelope's `tool` is `issue`; the report and events files are
  `issue-<started_at>-<session_id>.json` and `.jsonl`; the progress prefix on
  stderr is `issue:`.

### 2. The operation is a flag: `--op`

- `--op <name>` selects the operation. It is required on every run that does
  work; a run without it is a usage error (exit 2) listing the operations. It
  is declared as an enum (`DeclareEnum`), so `--schema` lists every value.
- The operations, one per `issuex.Client` method, plus one that makes no
  forge call:

  | `--op` | `issuex.Client` method | Target | Writes |
  |---|---|---|---|
  | `auth` | `Authenticated` (and forge detection) | repository or none | no |
  | `repo` | `GetRepository` | repository | no |
  | `create` | `CreateIssue` | repository | yes |
  | `read` | `ReadIssue` | issue | no |
  | `update` | `UpdateIssue` | issue | yes |
  | `close` | `CloseIssue` | issue | yes |
  | `list` | `ListIssues` | repository | no |
  | `comment` | `AddComment` | issue | yes |
  | `comments` | `ListComments` | issue | no |
  | `label` | `AddLabels` | issue | yes |
  | `unlabel` | `RemoveLabel` | issue | yes |
  | `create-label` | `CreateLabel` | repository | yes |
  | `create-pr` | `CreatePullRequest` | repository | yes |
  | `pr` | `ReadPullRequest` | pull request | no |
  | `files` | `ReadChangedFiles` | pull request | no |
  | `pr-state` | `GetPRState` | pull request | no |
  | `checks` | `GetCIChecks` | pull request | no |
  | `reviews` | `GetPRReviews` | pull request | no |
  | `review-comment` | `PostReviewComment` | pull request | yes |
  | `merge` | `MergePullRequest` | pull request | yes |
  | `close-pr` | `ClosePullRequest` | pull request | yes |

  `Close` is lifecycle, called by the program after every run, and is not an
  operation.

- `auth` answers "what would this process talk to, and can it write" without
  a write: the forge type, the API base URL, whether a token is configured,
  and the repository the target resolves to. It makes no request, with one
  exception it reports when it happens: `issuex.NewWithOptions` probes
  `/api/v4/version` on a host whose name says neither `github` nor `gitlab`
  to decide whether it is a self-hosted GitLab.

### 3. The positional is the target

The single positional names what the operation acts on. Its shape decides, in
Go, before any request:

| The argument is | Then it is | Accepted by |
|---|---|---|
| a GitHub or GitLab issue or pull/merge-request URL | an issue or pull-request ref, the kind fixed by the URL's path (`/issues/` vs `/pull/` or `/-/merge_requests/`) | every operation; a repository operation uses the URL's repository |
| `owner/repo#N` or `group/subgroup/project#N` | a ref whose kind the operation decides | issue and pull-request operations |
| `owner/repo` or `group/subgroup/project` (`issuex.ParseRepo`) | a repository | repository operations |
| a bare positive integer `N` | a ref in the repository `--repo` names, else the `origin` remote of `--dir` | issue and pull-request operations |
| absent | the repository `--repo` names, else the `origin` remote of `--dir` | repository operations only |
| anything else | a usage error (exit 2) | — |

- A target whose kind does not fit the operation is a usage error naming the
  operation that would fit: `read` given a pull-request URL says to use `pr`;
  `merge` given an issue URL says it needs a pull request. The GitHub issues
  endpoint happens to accept a pull request's number; GitLab's does not and
  would return another object with the same `iid`. The tool is strict on both
  rather than lenient on one.
- `--repo` with a URL or an `owner/repo#N` target is a usage error: the
  target already names its repository.
- A repository operation with no positional and no resolvable `origin` is a
  usage error naming `--repo`, the same wording `triage` uses for its target.
- The envelope reports the target as `result.target` (§6), not as `input`:
  the tool read no input. `input` is absent from the envelope.
- The host of a URL must be one `issuex.ParseIssueURL` accepts: `github.com`,
  `gitlab.com`, any host containing those words, or the host of
  `GITHUB_API_URL`/`GITLAB_API_URL`. A GitHub Enterprise URL on an unrelated
  hostname is therefore accepted only once `GITHUB_API_URL` is set, exactly
  as for the four tools' issue inputs.

### 4. Forge, host and credential resolution

- The client is built by `issuex.NewWithOptions` from the target's
  repository (its `Host` when the target was a URL), the `origin` remote of
  `--dir`, and the environment, in that order: the same `detectForge` the
  shell uses today, so `GITHUB_API_URL`, `GITHUB_TOKEN`, `GH_TOKEN`,
  `GITLAB_API_URL` and `GITLAB_TOKEN` mean here what
  [Configuration](../configuration.md#environment-variables) says they mean.
- **No no-op fallback.** A client that cannot be built is a failure, never a
  silent `NoOpClient`: `ErrAmbiguousForge` and `ErrUnsupportedForge` are
  reported as `category: "usage"` (nothing was fetched or written, and the
  remedy is an environment variable or `--repo`) with a `fix_hint` naming
  `GITHUB_API_URL` and `GITLAB_API_URL` in `env`.
- A write operation on a client whose `Authenticated()` is false is refused
  before any request, as `category: "auth"`, stage `auth`, with
  `fix_hint.env` of `GITHUB_TOKEN`, `GH_TOKEN` and `GITLAB_TOKEN` and a
  message naming the operation and the target. `issuex` would refuse the same
  call with `ErrNoToken`; checking first keeps the refusal free and the
  message the tool's own.
- A read operation runs unauthenticated when no token is set, as the four
  tools read a public issue. A 404 on an unauthenticated client carries
  `issuex`'s hint that the resource may be private.
- The model is never resolved. No vendor key is read.

### 5. Flags

`issue` takes the shell's flags and its own; it does not take the model-run
flags.

**Shared, kept:** `--dir` (only to find the `origin` remote; the file tools
are never built), `--dry-run`, `--detail`, `--report-file`, `--output`,
`--emit-events`, `--verbose`, `--quiet`, `--version`, `--schema`.

**Shared, not accepted:** `--model`, `--vendor`, `--effort`, `--max-turns`,
`--budget`, `--phase-timeout`, `--total-budget`, `--context`,
`--trust-project`, `--show-text`, `--input-kind`, `--preflight`. Each is a
usage error that names the tools that do accept it (`issue does not accept
--model; it is a flag of triage, fix, spec and impl`), through the same table
that today names another tool's flag. `--preflight` is not needed: for a tool
with no model phase, `--dry-run` already runs every check and stops before
the write (§7).

**Own flags.** Every one is checked against the operation in `PreCheck`,
before any request: a flag the operation does not take is a usage error
naming the operation and the flag.

| Flag | Operations | Effect |
|---|---|---|
| `--op <name>` | all | the operation (§2) |
| `--repo <owner/repo>` | all | the repository, when the target does not name one; `group/subgroup/project` for a nested GitLab path |
| `--title <text>` | `create` (required), `update`, `create-pr` (required) | the title |
| `--body <text>` | `create`, `update`, `close`, `comment`, `review-comment`, `create-pr` | the body; for `close`, the closing comment, posted before the close as `issuex.CloseIssue` does |
| `--body-file <path>` | the same as `--body` | the body read from a file, or from stdin when the path is `-`; exclusive with `--body`. The body is not classified or truncated: a body that is literally `-` or looks like a path is given with `--body` |
| `--label <a,b>` | `create`, `update`, `label`, `list` | labels; comma-separated, as `triage --label` |
| `--label <name>` | `unlabel`, `create-label` | one label |
| `--assignee <a,b>` | `create`, `update` | assignees; comma-separated |
| `--assignee <login>` | `list` | filter by assignee |
| `--state <open\|closed\|all>` | `list`, `update` | the filter (default `open`), or the state to set |
| `--sort`, `--direction` | `list` | passed to `IssueFilter` as given |
| `--limit <n>` | `list` | at most this many issues; default 100, the `issuex` default |
| `--color <hex>`, `--description <text>` | `create-label` | the label's colour and description |
| `--head <branch>` | `create-pr` (required) | the source branch |
| `--base <branch>` | `create-pr` | the target branch; default the repository's default branch, read with one `GetRepository` call |
| `--draft` | `create-pr` | open as a draft (`Draft: ` title prefix on GitLab, as `issuex` does) |
| `--method <merge\|squash\|rebase>` | `merge` | the merge method; default the repository's configured default, as `issuex` resolves it |
| `--commit-title`, `--commit-message`, `--sha` | `merge` | passed to `MergeOptions` as given |

A `comment` or `review-comment` without `--body` or `--body-file`, a `create`
without `--title`, a `create-pr` without `--title` or `--head`, an `update`
with nothing to change, and a `label` with no labels are each usage errors.
An empty body after reading `--body-file` is a usage error too.

`--body-file -` is the only use of stdin. The positional is never `-`.

### 6. The result

One `Result` type, with a fixed head and one section per operation; a section
is present only for the operation that fills it. Every string field carries a
trust tag (the existing `CheckTrust` test applies to the new type as it does
to the four).

- `op` (fact), `forge` (`github` or `gitlab`, fact), `api_url` (fact),
  `authenticated` (bool), `target` (`{repo, number, kind, url}`, all fact),
  `dry_run` (bool).
- `repository`: `issuex.Repository` as returned (`full_name`,
  `default_branch`, `private`, `archived`, `permissions`, and the merge
  policy flags). Field text is fact: it is the forge's structured response.
- `issue`: an `issuex.Issue`, for `create`, `read`, `update`. `title` and
  `body` are `external`; `labels`, `author.login`, `state`, `url`,
  timestamps are fact.
- `comments`: the thread's comments, for `read` and `comments`, each with
  `body` (`external`), `user.login`, `created_at` and `html_url` (fact), and
  `truncated` (bool). `read` also carries `rendered` (`external`): the thread
  as `toolio.RenderThread` renders it for a model, the same text the four
  tools would be given for this URL, so a skill that wants prose does not
  render its own.
- `issues`: the list, for `list`, with `incomplete` (bool).
- `pull_request`: an `issuex.PullRequest`, for `create-pr` and `pr`;
  `title` and `body` `external`, branch names, SHA, state and URL fact.
- `files`, `pr_state`, `checks` (`summary` `external`, the rest fact),
  `reviews` (`body` `external`, `author.login` and `state` fact), `merge`
  (`merged`, `sha`, `message` — `message` `external`, since it is the forge's
  free text).
- `comment_url`: the URL of a posted comment, for `comment` (fact).
  `review-comment` returns no URL from `issuex` and reports none.
- `would_write`: under `--dry-run` on a write operation, the request that
  would have been sent, as the `issuex` request struct serialises (§7).

`result.detail` records the view, as on every tool. The summary view
(`--detail summary`, the default) differs from the full one only where the
full one repeats what the caller did not ask for:

| Operation | Summary keeps |
|---|---|
| `list` | each issue's `number`, `title`, `state`, `labels`, `author`, `url`, `updated_at`; bodies are dropped |
| `read` | everything except `rendered` |
| every other | everything (nothing to trim) |

`summary` (the envelope's one sentence) names the operation and the target
and, for a write, what was written: `issue: created acme/widgets#43`,
`issue: read acme/widgets#42 (7 comments)`, `issue: merged acme/widgets#57
(squash)`.

`untrusted_fields` lists every non-empty `external` field of the view
printed, as it does for the four tools. For a `read` that is at least
`/result/issue/title`, `/result/issue/body` and one pointer per comment body.

### 7. `--dry-run`

- A read operation under `--dry-run` runs exactly as without it: reading is
  not a remote change, and the flag's one definition says "make no remote
  change".
- A write operation under `--dry-run` resolves the target, builds the client,
  checks nothing about the credential (as `triage --dry-run` does not), and
  stops before the request. It reports `result.would_write` and the artifact
  the write would have produced with `"dry_run": true`, and records no side
  effect. The report and events files are still written.
- The one implied read a write depends on still happens under `--dry-run`:
  `create-pr` without `--base` reads the default branch, and `merge` without
  `--method` reads the merge policy, so `would_write` shows the request as it
  would actually be sent.

### 8. Artifacts, side effects, timings and `next`

- `artifacts`: `create` yields `{kind: issue, url, number}`; `create-pr`
  yields `{kind: pull_request, url, number}`; `comment` yields
  `{kind: comment, url}` with no `role`; `merge` yields `{kind: commit, sha,
  branch: <base>}`. Every other operation yields only the `report_file` and
  `events_file` entries the shell adds. No new artifact kind: the set is
  closed, and a label is not an artifact.
- `side_effects`: one entry per write, recorded at the call site as the four
  tools record theirs. `action` is the existing `create_issue`,
  `update_issue`, `comment` or `open_pr` where one fits, and otherwise a new
  value: `close_issue`, `add_labels`, `remove_label`, `create_label`,
  `review_comment`, `merge_pr`, `close_pr`. `target` is the ref or the
  repository as the four tools render it. A failed write carries the warning
  code of the warning recorded for it.
- `timings`: one `{kind: forge, name: <op>}` entry per forge call, including
  the implied read (`name: repo`).
- `next`: empty. `issue` is a primitive; which tool runs next is the skill's
  decision, not something the tool can know from a read or a comment.

### 9. Errors, categories and exit codes

Exit codes are `0`, `1` and `2`; `--schema` lists those three. Stages are
`target` (parsing the positional and `--repo`, detecting the forge), `auth`
(the credential check before a write) and `call` (the request). Errors map
from `issuex` as follows; every category not listed is `forge`.

| `issuex` error | `category` | `retryable` | Note |
|---|---|---|---|
| `ErrNoToken`, or the §4 refusal | `auth` | no | `fix_hint.env` names the three token variables |
| `ErrAmbiguousForge`, `ErrUnsupportedForge` | `usage` (exit 2) | no | `fix_hint.env` names the two API-URL variables |
| `ErrNotFound` / HTTP 404 | `not_found` (new) | no | the message carries the private-repository hint when unauthenticated |
| `ErrConflict` / HTTP 405, 406, 409 | `conflict` (new) | no | a merge that cannot merge, a state that cannot change |
| `ErrRateLimited` | `rate_limited` (new) | **yes** | `issuex` has already waited up to two minutes and retried once; re-running later can succeed |
| any other `HTTPError` or transport error | `forge` | no | the message is `issuex`'s: method, path, status, forge message |

`not_found`, `conflict` and `rate_limited` are new values in the open
`category` set. `RetryableFor` gains `rate_limited`.

Two new warning codes, both `low`, both at stage `call`: `comments_truncated`
when `ListComments` or `ReadIssue` hit the 500-comment cap, and
`list_incomplete` when `ListIssues` hit `--limit`. The existing
`comments_unreadable` is recorded when `ReadIssue` returns a thread whose
`CommentsErr` is set, at its existing stage.

### 10. The shared shell

The shell gains a second way to run a tool, used by `issue` alone, so that
the four tools are untouched and nothing is copied:

- `toolio.App` can declare that it has no model phase. For such an App,
  `execute` runs `PreCheck`, resolves `--dir` to a directory (for `origin`),
  and calls `Exec` with `Deps` whose `Workspace`, `Runner` and `Model` are
  nil and whose `Forge` the tool builds itself (§4). It does not classify an
  input, does not resolve a model, and does not build a runner. Everything
  else in `Main` and `emit` is the same code: flag parsing, `--version`,
  `--schema`, the bare-invocation rule, the events sink, the report file,
  `--output`, the envelope. A spec may express this as a field on `App`, a
  second constructor, or a split of `execute`; what matters is that there is
  one `Main` and one `emit`.
- `Common` is split so that the model-run flags are registered separately
  from the shell flags, and `issue` registers only the shell's. The four
  tools register both and see no change in their flag sets or their
  `--schema` golden files.
- `toolFlags` and `toolOrder` gain `issue`, and the model-run flags are
  attributed to the four tools in the unsupported-flag table, so both
  directions of the message work: `fix does not accept --op; it is an issue
  flag`, and `issue does not accept --budget; it is a flag of triage, fix,
  spec and impl`.
- `Envelope.Tool`'s description, `Next.Tool`'s description and the
  `run_start` event's documentation name five tools. `run_start` for `issue`
  omits `model` and sets `input_kind` to the target's form from §3 (`url`,
  `ref`, `repo`, `number` or `default`). The four tools' streams do not
  change.

### 11. Tests

- **Every operation against both forges.** One `httptest` server per forge,
  as `issuex`'s own tests use, and a test per operation per forge that drives
  `cmd/issue`'s `App` end to end and asserts on the request the server saw
  and the envelope printed. Twenty operations, two forges.
- **Every execution path has a smoke test**, per `.specs/steering.md`: a read,
  a write, a write under `--dry-run`, a refused write (no token), a usage
  refusal before any request (bad target, wrong flag for the operation,
  `--repo` with a URL, missing `--op`), a `not_found`, a `conflict`, a
  `rate_limited` that is `retryable`, and the ambiguous-forge usage error.
  Each asserts that no request reached the server when none should have.
- **Target parsing.** A table over the six shapes of §3 and the kind check
  against each operation class.
- **No model.** A run with no vendor key in the environment and
  `AF_MODEL` unset succeeds; `model` and `usage` are absent from the
  envelope; `--model` is a usage error naming the four tools.
- **No no-op.** With no token, no `origin` and no API-URL variable, a write
  fails as `usage` with the API-URL `fix_hint`, and nothing reports
  `created`.
- **The golden file.** `cmd/issue/testdata/schema.golden.json`, in the
  existing `TestSchemaGolden`; the flags document lists `--op`'s enum and no
  model-run flag; the result document carries `x-trust` on every string
  field; `exit_codes` has three entries. The tests that enumerate "the four
  tools" (`toolFlagSetNames`, `TestTS09_25_EveryResultTypeFullyDescribed`,
  the schema-golden and schema-flag binaries tests) include `issue` where
  what they check applies to it, and say why where it does not.
- **Trust labels.** `CheckTrust` over `issueops.Result` reports nothing
  unlabelled; `UntrustedFields` on a `read` result lists the issue body and
  every comment body and no `fact` field.
- **Docs.** The doc tests that walk `docs/cli.md`'s shared-flags table and
  per-tool sections cover the `issue` section and the five-tool header.
- **The four tools are unchanged.** Their golden files do not change in this
  spec; a diff to any of them is a regression.

## Interface change

Per [ADR 06](../adr/06-version-the-envelope-interface.md):

- **Envelope: additive, no version bump.** A new `tool` value. `input`,
  `model` and `usage` are already optional (`omitempty`) and are simply absent
  for `issue`. Three new `error.category` values and two new warning codes,
  in sets documented as open. New `side_effects[].action` values: the field's
  description today reads "one of create_issue, update_issue, comment, push,
  open_pr" without saying whether the set is closed; this PRD amends it to say
  the set is open, which is what the four tools' callers already have to
  assume for `category` and `code`. If a reviewer reads the current wording
  as a closed promise, the alternative is a major bump, and that decision is
  made against the golden-file diff as ADR 06 provides.
- **Artifact kinds: unchanged.** The set stays closed and is not widened.
- **Events: additive.** `run_start` without `model` occurs only in `issue`'s
  stream; the four tools' streams are byte-for-byte what they were.
- **Flags:** the four tools' flag sets are unchanged. `issue`'s is new, and
  its golden file records it.
- `docs/cli.md`'s interface-versions section gets an entry recording the
  fifth tool and the three additive changes, under 3.0.0.

## Design decisions

1. **The operation is a flag, not a verb.** `issue --op read <target>`, not
   `issue read <target>`. Three reasons. ADR 03 and PRD 03 fix "every addition
   is a flag" for the tools' interface, and the one place a verb exists
   (`af mcp`, PRD 04) is on the umbrella program, not a tool. `--schema`
   describes a flat flag set, so a flag with an enum is listed and validated
   for free, and an MCP adapter built on `--schema` sees one tool with one
   input schema; a verb would need a per-verb schema the shell has no notion
   of. And `SplitArgs`, the unsupported-flag table and the bare-invocation
   rule all assume one positional. The cost is one more word in a skill's
   examples.
2. **The positional is the target, and the body is a flag.** The four tools'
   positional is "the thing to read from", and for `read` that would be the
   URL, but for `comment` it would be the body and the URL would become a
   flag. One rule for every operation — the positional is what you act on —
   is worth more to a skill author than symmetry with `triage`. The body
   comes through `--body` or `--body-file`, and is never classified by shape:
   a body is content, and a body that happens to look like a path must be
   sent as written.
3. **The target may be absent for a repository operation.** `issue --op list`
   in a checkout should list that repository's issues; requiring
   `acme/widgets` to be typed when `origin` already says so would be the
   papercut `SplitArgs` exists to avoid. The bare-invocation rule still
   holds when `--op` is also absent.
4. **No `NoOpClient`, ever.** The fallback exists so that `spec ./idea.md`
   works in a directory with no remote. `issue` has no local-only path, and a
   no-op write that reports success is the one outcome a forge tool must not
   have.
5. **Strict target kinds.** GitHub's leniency about issue numbers versus pull
   request numbers is a GitHub fact. The tool is forge-neutral, so it refuses
   on both what GitLab would get wrong on one.
6. **`--dry-run` instead of `--preflight`.** `--preflight` exists to run the
   checks without paying for the model. `issue` has no model, and
   `--dry-run` on a write already runs every check and stops before the
   request. One flag that means one thing beats two that mean the same.
7. **`rendered` on `read`.** The thread rendering the four tools feed to a
   model is a pure function already; giving it to a skill costs nothing and
   means a skill that wants to hand the thread to a model gets the same text
   `triage` would.
8. **Three new categories rather than `forge` for everything.** A skill that
   can branch on `not_found` and `conflict` without parsing a message, and
   that is told `rate_limited` is retryable, is the point of a decidable
   envelope ([PRD 01](01-make-the-envelope-decidable.md)).
9. **Shell split, not a second shell.** The envelope, exit codes, report and
   events files, `--output` and `--schema` are what make this a fifth tool
   rather than a fifth way to call a forge. They stay one implementation.

## Documentation

- `README.md`: five programs in the one-interface block, with a one-line
  entry for `issue`; the modules table's `cmd/` row and the new `issueops`
  package; the quick-start example gains an `issue` line.
- `docs/cli.md`: the header block ("Five tools, one interface"); a shared-flags
  note saying which flags `issue` does not take and why; an `## issue` section
  with the operation table, the target table, the own-flags table, the result
  sections and their summary view, the error table and worked examples for
  a read, a write and a dry run; the new categories in the category table;
  the new warning codes and the corrected `triage` attributions in the
  warning-codes table; the new `side_effects[].action` values and the
  open-set wording; the `run_start` note; the interface-versions entry.
- `docs/configuration.md`: a sentence under Quick start and under
  Environment variables that `issue` needs a forge token and no model key;
  the `--dir` row of "What the model may read" does not apply and says so.
- `docs/development.md`: `cmd/issue` and `issueops` in the layout and the
  package list; the "four tools" counts.
- `docs/model-usage.md`: one line that `issue` has no phase and does not
  appear in its tables.
- `containers/tools/README.md`, `install.sh`'s comment and `Makefile`: the
  fifth tool.
- `docs/prds/README.md`: this PRD in the proposed table; the "four tools"
  wording where it describes the present.
- `docs/prds/04-serve-the-tools-over-mcp.md` is not edited here; a note in
  this PRD's open questions records that it should list `issue` once both
  exist.
- An ADR records the decision that a tool may have no model phase and what
  the shell guarantees for one, and the flag-not-verb decision with its
  reasons.

## Open questions

- **`next` after `create`.** `triage` suggests `fix` on the issue it filed.
  `issue --op create` could do the same, but the body is whatever the caller
  wrote, and not every issue is a bug. Left empty here; a spec may add it if
  the skills that use the tool want it.
- **Comments on a merge request.** `issuex.ListComments` reads issue notes
  only, so on GitLab the comments of a merge request are reachable only as
  the `commented` entries of `reviews`. That is an `issuex` gap, not this
  tool's; it is noted so a skill author is not surprised.
- **PRD 04.** When `issue` exists, the MCP adapter should expose it as a
  fifth tool with `readOnlyHint` false, `destructiveHint` true (`merge`,
  `close`), `idempotentHint` false and `openWorldHint` true, and its policy
  (§5 of PRD 04) should be able to deny the write operations as it denies
  pushes. That is a one-row change to PRD 04 and is not made here.
