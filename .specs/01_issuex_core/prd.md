---
spec_id: "01"
spec_name: "issuex_core"
title: "issuex: the forge-neutral client contract and its null implementation"
status: "active"
created_at: "2026-09-10T13:04:14.399435Z"
updated_at: "2026-09-10T13:04:14.399435Z"
intent_hash: "6aad7bed440eddcd2f806de853d83e8c7013c7ba08ea80c7f8e8d86fd6ecccf0"
schema_version: 2
source: "docs/drafts/forge_neutral_issue_pr_client.md"
---
## Intent

Give the agent-fox ecosystem one importable Go module, `issuex`, whose public
surface describes issue and pull-request work on a git forge without naming a
forge: one interface, one set of value types, one error taxonomy, one way to
build a client from options and the environment. This spec establishes that
contract and the only implementation that needs no network — the null client —
so that per-forge implementations and the migration off `internal/ghapi` are
additions to a fixed surface rather than negotiations about it.

## Goals

- A new Go module rooted at `issuex/`, importable from outside this repository,
  with no third-party dependencies.
- A `ForgeClient` interface covering repository, issue and pull-request
  operations, every method taking `context.Context` first and returning value
  structs.
- A complete set of forge-neutral value types (issue, comment, label, pull
  request, changed file, check, review, merge result, repository) that carry no
  wire-format detail.
- `New(Options) (ForgeClient, error)`: token, endpoint, HTTP client and user
  agent in, the interface out, with defaults filled from the environment.
- Forge resolution from the endpoint URL and the environment, deterministic and
  offline, with an explicit error when the answer is genuinely ambiguous.
- Reference parsing — `owner/repo`, nested GitLab group paths, the three git
  remote spellings, GitHub and GitLab issue/PR web URLs — for both forges.
- A typed error taxonomy that lets callers distinguish not-found,
  not-authenticated, rate-limited and not-mergeable with `errors.Is`/`errors.As`.
- A null implementation whose issue reads return zero values, whose issue writes
  are silent no-ops, and whose pull-request operations fail loudly.
- `make lint`, `make test` and `make check` cover the new module; the whole
  module's tests run with no network and no token.

## Non-goals

- The GitHub and the GitLab implementations, and the shared HTTP transport they
  will share (bounded reads, rate-limit retry, internal pagination). This spec
  fixes the option fields, constants and error types that govern them; the code
  that performs requests lands in the follow-on specs listed under
  "Recommended split".
- Replacing `internal/ghapi` in `specgen`, `issuetriage`, `codefix`,
  `internal/toolio` or `cmd/*`. `internal/ghapi` is untouched by this spec and
  keeps working exactly as it does today.
- Changes to the `hub` and `coder` repositories. This spec makes `issuex`
  importable; it does not import it anywhere.
- Tagging, releasing or publishing the module.
- Bitbucket, Azure DevOps, webhooks, GraphQL, git plumbing (clone, push, merge),
  OAuth or token acquisition, and user identity in options — all excluded for
  the module as a whole, not just for this spec.
- Running git as a subprocess. `internal/ghapi.DetectRepo` shells out to
  `git remote get-url origin`; `issuex` parses a remote string the caller
  supplies and executes nothing.

## Background

`internal/ghapi` (`client.go`, `ref.go`, ~600 lines plus tests) is the working
reference for this module. It is dependency-free, it decodes GitHub JSON
straight into exported structs, it bounds every response at `maxResponseBytes`
(8 MiB), it pages comments to `commentPageLimit` (5 pages of 100) and reports
the truncation on `Thread.Truncated`, it retries once on a rate limit whose
backoff is under two minutes, and it carries `HTTPError` with the status as a
number. Its package doc states the rule this module inherits: no model is given
a tool that reaches it — the REST calls are made by the pipelines, in Go, at
points that know why.

Two facts about the repository decide the module's shape.

First, `internal/` cannot be imported by `hub` or `coder`, which is the stated
reason for the new module. Second, the root module cannot be imported by them
either. `go.mod` declares `module github.com/agent-fox-dev/agentfox` while the
repository is `github.com/agent-fox-dev/agent-fox` (`.git/config`), and it
requires `github.com/agentfox/agentkit-go v0.0.0` resolved by
`replace ... => ../coder`. A replace directive in a dependency's `go.mod` is
ignored by the consumer, `agentkit-go v0.0.0` is not on any proxy (there is no
`go.sum` entry for it), and `docs/development.md` documents this as a known
consequence of a module path that does not match its repository. Anything under
the root module is therefore unconsumable from outside. `issuex` must be its own
module with a path that matches the repository.

The consumers inside this repository set the bar for the contract.
`issuetriage/pipeline.go` needs `CreateIssue`, `UpdateIssue`, `Authenticated`
and `errors.Is(err, ErrNoToken)` before it spends money on a model.
`codefix/pipeline.go` needs `CreatePullRequest`, `AddComment`, `DetectRepo` and
`IssueRef.URL()`. `internal/toolio/input.go` needs an issue plus its comments,
with a comment-read failure reported rather than returned. Those call sites are
what the interface is measured against, even though this spec does not change
them.

Tooling: `make test` is `go test ./... -count=1` and `make lint` is
`test -z "$(gofmt -l .)"` plus `go vet ./...`, both rooted at the repository.
Neither `go test ./...` nor `go vet ./...` descends into a nested module, so the
Makefile has to name it. There is no Go CI workflow — `.github/workflows`
contains container builds only — so `make check` is the whole gate.

Everything in this spec uses the standard library only (`context`, `errors`,
`fmt`, `net/http`, `net/url`, `strconv`, `strings`, `time`), so there is no
external API surface to verify.

## Requirements

### 1. The module and its place in the build

Create `issuex/` with its own `go.mod` declaring the module path
`github.com/agent-fox-dev/agent-fox/issuex` — the repository's real path, so a
proxy can serve it and `hub` and `coder` can require it by version once it is
tagged (`issuex/vX.Y.Z`). The `go` directive is `1.22`. The module has no
`require` block: only the standard library may be imported, and no file in it
may execute a subprocess. The root module's `go.mod` is not modified by this
spec — nothing in the root module imports `issuex` yet.

`make test` gains `cd issuex && go test ./... -count=1`; `make test-fast` gains
the same with `-short`; `make lint` gains `cd issuex && go vet ./...`. The
existing `gofmt -l .` already walks the subdirectory and needs no change.
`make check` therefore fails if the new module fails to build, vet or test.
Every test in the module must pass with no network access and no credentials in
the environment; tests that read environment variables set them with
`t.Setenv`.

`issuex` is one package, laid out the way `internal/ghapi` is: a package doc,
the interface, the types, the errors, the options and detection, the reference
parsing, the null client, and a `*_test.go` beside each. An `issuex/README.md`
describes the module the way `afspec/README.md` describes that library.

### 2. The `ForgeClient` interface

One flat interface names every operation the module will ever perform.
`context.Context` is the first parameter of every method; every returned datum
is a value struct or a slice of value structs, never a pointer.

```go
type ForgeClient interface {
    Forge() Forge
    Authenticated() bool
    Close() error

    GetRepository(ctx context.Context, repo Repo) (Repository, error)

    CreateIssue(ctx context.Context, repo Repo, draft IssueDraft) (Issue, error)
    GetIssue(ctx context.Context, ref IssueRef) (Issue, error)
    UpdateIssue(ctx context.Context, ref IssueRef, update IssueUpdate) (Issue, error)
    CloseIssue(ctx context.Context, ref IssueRef, comment string) error
    ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error)
    AddComment(ctx context.Context, ref IssueRef, body string) (string, error)
    ListComments(ctx context.Context, ref IssueRef) (CommentList, error)
    AddLabels(ctx context.Context, ref IssueRef, labels []string) error
    RemoveLabel(ctx context.Context, ref IssueRef, label string) error
    CreateLabel(ctx context.Context, repo Repo, label Label) error

    CreatePullRequest(ctx context.Context, repo Repo, draft PullRequestDraft) (PullRequest, error)
    GetPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error)
    ListChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error)
    GetPullRequestState(ctx context.Context, ref IssueRef) (PullRequestState, error)
    ListChecks(ctx context.Context, ref IssueRef) ([]Check, error)
    ListReviews(ctx context.Context, ref IssueRef) ([]Review, error)
    AddReviewComment(ctx context.Context, ref IssueRef, body string) (string, error)
    MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error)
    ClosePullRequest(ctx context.Context, ref IssueRef) error
}
```

The doc comment on each method states the contract implementations must honour,
including the ones this spec does not implement: `CloseIssue` posts the comment
before closing when the comment is non-empty; `AddLabels` is additive;
`RemoveLabel` and `CreateLabel` are idempotent and report success when the label
is already absent or already present; `ListIssues` and `ListComments` page
internally and set `Truncated` when they stop at the cap; `AddReviewComment`
posts a top-level review comment, not an inline one; `MergePullRequest` returns
an error satisfying `errors.Is(err, ErrNotMergeable)` when the forge refuses the
merge.

`Close` releases transport resources the client owns, is idempotent, returns
`nil` when there is nothing to release, and leaves the client usable.
`Authenticated` reports whether a credential was found, so a caller can refuse a
run in its first second — the pattern `issuetriage.checkWriteCredential` already
follows. `Forge` reports which forge the client talks to.

### 3. The neutral value types

The types are transport-free: no struct tags, no forge-specific field, no
pointer in a returned struct. Each implementation decodes into its own
unexported wire structs and maps into these.

`Forge` is a string type with `ForgeNone`, `ForgeGitHub` and `ForgeGitLab`; the
zero value means "not yet resolved" and is what `Options.Forge` uses to ask for
detection.

`Repo{Owner, Name string}` names one project. `Owner` may contain slashes, which
is how a nested GitLab group path (`group/subgroup/project`) is carried;
`Path()` renders `Owner + "/" + Name`, `String()` is `Path()`, and `Valid()`
reports that both halves are non-empty.

`IssueRef{Repo Repo; Number int; IsPullRequest bool; Forge Forge; Host string}`
identifies one issue or pull request. `Number` is the forge's user-visible
number — a GitHub issue/PR number or a GitLab IID. `String()` renders
`owner/repo#42`. `URL()` renders the canonical web URL: for GitHub
`https://{host}/{owner}/{repo}/{issues|pull}/{n}`, for GitLab
`https://{host}/{path}/-/{issues|merge_requests}/{n}`. An empty `Host` falls
back to `github.com` or `gitlab.com` per `Forge`, and an empty `Forge` is
treated as GitHub, which preserves `ghapi.IssueRef.URL()`'s current output.

The remaining types: `Repository{FullName, DefaultBranch string; Private,
Archived bool; Permissions Permissions}` with `Permissions{Push, Pull, Admin
bool}`; `User{Login string}`; `Label{Name, Color, Description string}`;
`Issue{Number int; Title, Body, State, URL string; Author User; Labels []string;
CreatedAt, UpdatedAt time.Time}`; `Comment{Body string; Author User; CreatedAt
time.Time; URL string}`; `PullRequest{Number int; Title, Body, State, URL, Head,
Base string; Draft bool}`; `PullRequestState{Number int; State string; Merged
bool; HeadSHA string}`; `ChangedFile{Filename, Status string; Additions,
Deletions int}`; `Check{Name, Status, Conclusion, Summary, URL string}`;
`Review{Author User; State, Body string; SubmittedAt time.Time; URL string}`.

Inputs and results that need optionality get their own structs:
`IssueDraft{Title, Body string; Labels []string}`;
`IssueUpdate{Title, Body *string}` where a nil field leaves the value unchanged
and a pointer to the empty string clears it;
`IssueFilter{State State; Label, Owner, Sort, Direction string; Limit int}`
where every field is optional, the set ones combine with AND, and `Limit > 0`
overrides `Options.IssueListCap` for that call;
`PullRequestDraft{Title, Body, Head, Base string; Draft bool}`;
`MergeOptions{Method MergeMethod; CommitTitle, CommitMessage, SHA string}` where
an empty `Method` means "the repository's configured default strategy";
`MergeResult{Merged bool; SHA, Message string}`.

`State` is a string type with `StateOpen`, `StateClosed` and `StateAll`; the
zero value means the forge's own default. `MergeMethod` is a string type with
`MergeMethodMerge`, `MergeMethodSquash` and `MergeMethodRebase`.

List results are structs, not multiple returns: `IssueList{Issues []Issue;
Truncated bool}` and `CommentList{Comments []Comment; Truncated bool}`, so a
caller that ignores truncation has to ignore a named field rather than a bare
`bool`.

### 4. Options, defaults and construction

```go
type Options struct {
    Forge            Forge
    Endpoint         string
    Token            string
    HTTPClient       *http.Client
    UserAgent        string
    MaxResponseBytes int
    IssueListCap     int
    CommentCap       int
}

func New(o Options) (ForgeClient, error)
func NewNull() ForgeClient
```

`New` resolves the options, then constructs. Resolution fills the endpoint and
token from the environment as described in requirement 5; an empty `UserAgent`
becomes `DefaultUserAgent` ("agent-fox"); a nil `HTTPClient` becomes
`&http.Client{Timeout: DefaultHTTPTimeout}` (30 seconds); a zero
`MaxResponseBytes`, `IssueListCap` or `CommentCap` becomes
`DefaultMaxResponseBytes` (8 MiB), `DefaultIssueListCap` (100) or
`DefaultCommentCap` (500). A negative value for any of the three is a usage
error naming the field — silently substituting a default for a number the caller
meant would hide the mistake. Trailing slashes are trimmed from the endpoint.
The exported constants also include `DefaultGitHubEndpoint`
("https://api.github.com", the value `ghapi.DefaultBaseURL` holds today) and
`DefaultGitLabEndpoint` ("https://gitlab.com/api/v4").

`New` returns the interface type, never a concrete struct. It returns a null
client when the resolved forge is `ForgeNone`, and — until the per-forge specs
land — an error satisfying `errors.Is(err, ErrForgeUnsupported)` naming the
forge when the resolved forge is `ForgeGitHub` or `ForgeGitLab`. That error is
what the follow-on specs replace; no implementation is stubbed with a panic in
the meantime.

`NewNull()` returns the null client directly, for callers that know there is no
forge and do not want to construct options to say so.

### 5. Forge resolution

`DetectForge(endpoint string) (Forge, bool)` decides from the URL alone, with no
network call, in this order: the path contains `/api/v4` → GitLab; the path
contains `/api/v3` or the host is `api.github.com` or `github.com` → GitHub; the
host contains `gitlab` → GitLab; the host contains `github` → GitHub; otherwise
`(ForgeNone, false)`.

`New` resolves the forge like this:

1. `Options.Forge` set (including `ForgeNone`) — used as given, detection
   skipped. An endpoint that is empty then defaults to that forge's default
   endpoint.
2. `Options.Endpoint` set — `DetectForge` decides. A URL it cannot classify is
   an error satisfying `errors.Is(err, ErrAmbiguousForge)`, quoting the endpoint
   and naming `Options.Forge` as the way to settle it.
3. Both `GITLAB_API_URL` and `GITHUB_API_URL` set in the environment —
   `ErrAmbiguousForge`, naming both variables.
4. Exactly one of `GITLAB_API_URL` or `GITHUB_API_URL` set — that forge, that
   endpoint.
5. Neither set: `GITLAB_TOKEN` present and no `GITHUB_TOKEN`/`GH_TOKEN` → GitLab
   at `DefaultGitLabEndpoint`; otherwise GitHub at `DefaultGitHubEndpoint`.
   GitHub is the documented tiebreak when credentials for both are present.

The token, when `Options.Token` is empty, comes from `GITHUB_TOKEN` then
`GH_TOKEN` for GitHub, and from `GITLAB_TOKEN` for GitLab — resolved after the
forge is known, so a GitLab client never picks up a GitHub token. No token is
an error at construction time only when the caller asked for one; `New`
succeeds without a credential, and `Authenticated()` reports `false`.

### 6. Reference parsing

`ParseRepo(s string) (Repo, bool)` accepts two or more non-empty
slash-separated segments, trims surrounding whitespace and slashes, strips a
trailing `.git`, and rejects input containing `://`, whitespace, `..`, `:` or
`@`. With exactly two segments the result is `{Owner, Name}`; with more, every
segment but the last becomes `Owner`, which is how `group/subgroup/project`
reaches a GitLab client.

`ParseRemote(remote string) (Remote, bool)` handles the three spellings git
writes — `git@host:owner/repo.git`, `https://host/owner/repo(.git)` and
`ssh://git@host/owner/repo.git` — for both forges, including nested GitLab
paths, and returns `Remote{Repo Repo; Forge Forge; Host string}` with the forge
inferred from the host. A host that matches neither forge's host set returns
`false`; the module does not run git to obtain the remote.

`ParseIssueURL(s string) (IssueRef, bool)` recognises a GitHub issue or
pull-request URL (`/{owner}/{repo}/issues/{n}`, `/{owner}/{repo}/pull/{n}`) and
a GitLab one (`/{path}/-/issues/{n}`, `/{path}/-/merge_requests/{n}`), sets
`Forge`, `Host`, `Number` and `IsPullRequest`, and rejects everything else —
including a URL on a recognised host that points at a file, a commit or a
repository root. The scheme must be `http` or `https` and the number must be a
positive integer.

Host recognition follows `ghapi.GitHubHosts`: `GitHubHosts(apiURL string)`
returns `github.com` plus the Enterprise host named by `apiURL` with and without
its `api.` prefix, and `GitLabHosts(apiURL string)` does the same for
`gitlab.com`. `ParseRemote` and `ParseIssueURL` read `GITHUB_API_URL` and
`GITLAB_API_URL` from the environment; the unexported variants that take
explicit host sets are what the tests drive, as `ghapi/ref_test.go` already
does. Accepting an arbitrary host would let a URL on an unrelated forge be
turned into an `owner/repo` that happens to share a name, with the issue then
read from — or filed against — the wrong project.

### 7. The error taxonomy

Sentinels: `ErrNotAuthenticated`, `ErrNotFound`, `ErrRateLimited`,
`ErrNotMergeable`, `ErrNoForge`, `ErrAmbiguousForge`, `ErrForgeUnsupported`. All
messages are prefixed `issuex: `.

`HTTPError` carries `Forge`, `Method`, `Path`, `Status int`, `Message` and the
raw `RetryAfterHeader`, `RateLimitReset` and `RateLimitRemaining` header values.
`Error()` renders `"{method} {path}: {status} {message}"`. `Unwrap()` maps the
status onto a sentinel so `errors.Is` works without the caller knowing the
number: 404 → `ErrNotFound`; 401 → `ErrNotAuthenticated`; 429, or 403 with
`RateLimitRemaining == "0"` → `ErrRateLimited`; anything else → nil.
`RetryAfter() (time.Duration, bool)` reports how long to wait, and only for a
rate-limited status: a numeric `Retry-After` in seconds plus one second, else a
reset header holding a Unix timestamp converted to a duration plus one second
(a reset already in the past yields one second), else `(0, false)`. The
two-minute ceiling and the single retry are the transport's policy and are
applied by the implementations, not by this type.

`MaxResponseBytes`, `IssueListCap` and `CommentCap` are carried on `Options` for
those implementations; this spec fixes their names, defaults and validation
only.

### 8. The null implementation

An unexported type returned by `NewNull()` and by `New` when the resolved forge
is `ForgeNone`. `Forge()` returns `ForgeNone`, `Authenticated()` returns
`false`, `Close()` returns nil.

Repository and issue reads — `GetRepository`, `GetIssue`, `ListIssues`,
`ListComments` — return the zero value of their result type and a nil error,
with `Truncated` false and no allocated slices. Issue writes — `CreateIssue`,
`UpdateIssue`, `CloseIssue`, `AddComment`, `ListComments`' write counterparts,
`AddLabels`, `RemoveLabel`, `CreateLabel` — are silent no-ops returning zero
values and a nil error, so a pipeline configured without a forge runs to
completion instead of erroring on every comment. `AddComment` returns an empty
URL.

The `ErrNotAuthenticated` rule that governs writes on a real client does not
apply here: the null client is not an unauthenticated client, it is the absence
of one, and its whole purpose is to swallow writes.

Every pull-request method — `CreatePullRequest`, `GetPullRequest`,
`ListChangedFiles`, `GetPullRequestState`, `ListChecks`, `ListReviews`,
`AddReviewComment`, `MergePullRequest`, `ClosePullRequest` — returns a zero
result and an error satisfying `errors.Is(err, ErrNoForge)`, naming the method.
A PR workflow that silently succeeds against no forge reports work that does not
exist; failing loudly is the safer answer.

### 9. Documentation

`issuex/doc.go` carries a package comment in the register of
`internal/ghapi/doc.go`: what the module is, why it is a separate module rather
than a package of the root module, and the invariant that no model is given a
tool that reaches it. `issuex/README.md` documents the interface, the options,
the environment variables, the error taxonomy and the `issuex/vX.Y.Z` tag
convention for consumers.

In `docs/`: `README.md` gains a row pointing at `issuex/README.md` beside the
existing `afspec` row; `development.md` gains `issuex/` in the repository layout
and in the package structure list, and records that `make test`/`make lint`
enter the nested module; `configuration.md` and `cli.md` gain `GITLAB_TOKEN` and
`GITLAB_API_URL` in their environment tables, marked as read by `issuex` only —
the three tools still use `internal/ghapi` and are GitHub-only until the
adoption spec lands.

## Design Decisions

1. **This spec delivers the contract and the null client, not a working GitHub
   client.** The input describes six functional areas (module and interface,
   GitHub issues, GitHub PRs, GitLab issues, GitLab MRs, adoption) — far past
   one spec's ten requirements. The interface is the piece everything else
   depends on, and the null client is the one implementation that can be
   complete and correct without a transport, so the first spec is exactly the
   part that must not change later.

2. **`issuex/` is a nested Go module with the path
   `github.com/agent-fox-dev/agent-fox/issuex`.** A package under the root
   module cannot be imported by `hub` or `coder`: the root module path
   (`github.com/agent-fox-dev/agentfox`) does not match the repository
   (`agent-fox-dev/agent-fox`), and it requires `agentkit-go v0.0.0` through a
   local `replace` that consumers do not inherit — the exact failure
   `docs/development.md` documents for `coder`. Matching the repository path
   makes the new module fetchable by tag with no `replace` at all.

3. **The `go` directive is `1.22`, not the root module's `1.26.5`.** The module
   uses no language or standard-library feature newer than 1.22, and a lower
   directive is what lets `hub` and `coder` adopt it without a toolchain bump.
   The repository's own toolchain still builds and tests it.

4. **The Makefile names the nested module explicitly.** `go test ./...` and
   `go vet ./...` do not cross a module boundary, so without `cd issuex && …` in
   `test`, `test-fast` and `lint`, the new code would be outside the only gate
   this repository has — there is no Go CI workflow.

5. **Forge detection is offline; there is no `/api/v4/version` probe.** The
   input proposes probing GitLab's version endpoint. That would put network I/O
   and a `context.Context` into the constructor, make `New` fail on a slow or
   air-gapped network, and make construction untestable without a server. URL
   inspection plus an explicit override answers the same question
   deterministically.

6. **`Options.Forge` is added as the escape hatch.** The input says callers
   never name the platform, but a constructor that can fail with "ambiguous" and
   offers no way to resolve it in code forces the caller to edit the
   environment. The field is optional and detection remains the default path.

7. **Ambiguity is an error only where the input is genuinely two-valued.** An
   unclassifiable endpoint URL, and both `GITHUB_API_URL` and `GITLAB_API_URL`
   set, are errors. Having tokens for both forges and no API URL is not: GitHub
   wins, which preserves what `ghapi.NewWithOptions` does today and keeps the
   existing tools' behaviour unchanged when they migrate.

8. **`New` returns `ErrForgeUnsupported` for a forge whose implementation has
   not landed.** The alternative — shipping GitHub and GitLab types whose
   methods are `panic("not implemented")` — puts stubs into a released module
   and would be caught by this project's own dead-code audit. An explicit,
   tested error is honest about what the module can do at each step.

9. **`IssueRef` keeps `ghapi`'s name and shape**, gaining `Forge` and `Host`.
   `codefix`, `issuetriage` and `internal/toolio` all pass `ghapi.IssueRef`
   around and call `.URL()` on it; keeping the shape makes the adoption diff
   mechanical. GitLab IIDs occupy `Number`, which is what the input's "issue
   number (or GitLab IID)" describes.

10. **`Repo.Owner` may contain slashes, and `ParseRepo` accepts three or more
    segments.** GitLab nested groups have no second field to live in, and a
    parallel `ProjectPath` type would double every signature. This is a
    behaviour change from `ghapi.ParseRepo`, which requires exactly two
    segments: `a/b/c` is now valid and means owner `a/b`. The `--repo` flags in
    `cmd/issue` and `cmd/fix` keep whatever validation they have until the
    adoption spec revisits them.

11. **The neutral types carry no JSON tags.** `ghapi` decodes GitHub responses
    directly into its exported structs, which is why its tests can encode a
    `ghapi.Issue` as a fixture. Two forges cannot share one wire shape, so each
    implementation will own unexported wire structs and map into these. The cost
    is that migrated tests write JSON literals instead of encoding library
    types; the benefit is that the public types never drift toward one forge.

12. **Optional update fields are `*string`; returned data is never a pointer.**
    The input's "return types are value structs" is about results. `ghapi`'s
    "an empty title leaves the existing one" cannot express clearing a body, so
    `IssueUpdate` uses pointers, where nil means unchanged.

13. **Lists return a struct with a `Truncated` field.** `ghapi.readComments`
    returns `([]Comment, bool, error)` and the caller has to remember what the
    bool means. `IssueList` and `CommentList` name it, and give later specs
    somewhere to add a cursor or a total without changing the signature.

14. **`HTTPError` and the sentinels ship in this spec even though the transport
    that produces them does not.** They are the compile-time contract `hub`,
    `coder` and the tools match on, and `Unwrap` and `RetryAfter` are pure
    functions of the struct's fields, so they are fully testable here.
    Constructing an `HTTPError` from an `*http.Response` belongs with the
    transport.

15. **`issuex` never runs a subprocess.** `ghapi.DetectRepo` shells out to
    `git remote get-url origin`; a library that `hub` and `coder` embed should
    not. `ParseRemote` takes the string, and this repository already has
    `internal/gitx` to produce it.

16. **The single flat `ForgeClient` interface is kept**, as the input decided,
    even though the null client's behaviour splits cleanly along the issue/PR
    line. All three consumers need both halves, and one interface is one mock.

17. **No `Thread` aggregate.** `ghapi.Thread` bundles an issue, its comments,
    a truncation flag and a non-fatal `CommentsErr`, and `internal/toolio`
    depends on that "report the comment failure, do not return it" behaviour.
    That is a policy of the caller, not of the client, so `issuex` exposes
    `GetIssue` and `ListComments` and the adoption spec keeps the aggregate on
    the agent-fox side.
