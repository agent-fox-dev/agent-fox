---
spec_id: "02"
spec_name: "issuex_github"
title: "Forge-Neutral Issue and Pull Request Client: GitHub Adapter"
status: "active"
created_at: "2026-09-10T17:00:28.467132Z"
updated_at: "2026-09-10T17:00:28.467132Z"
intent_hash: "c0c6850674ecd701c4c9a75df1549582c75e27f001da0511bbefb1c31fd166c0"
schema_version: 2
source: "docs/drafts/forge_neutral_issue_pr_client.md"
---
## Intent

Provide a GitHub REST API adapter within the `issuex` package that implements the forge-neutral `Client` interface for repository metadata, issue management, and pull request operations across github.com and GitHub Enterprise instances.

## Goals

- Implement the full `issuex.Client` (and `issuex.ForgeClient`) interface for GitHub REST API v3 / 2022-11-28.
- Wire the GitHub client adapter into `issuex.NewWithOptions` and `issuex.New` so that resolving `ForgeTypeGitHub` automatically constructs and returns the GitHub client.
- Export an explicit constructor `NewGitHub(Options) (Client, error)` for callers targeting GitHub directly.
- Support GitHub Enterprise Server and GitHub Enterprise Cloud endpoints via configurable `BaseURL` (and `GITHUB_API_URL`), handling custom hostnames and path prefixes.
- Provide full issue operations: create, read with comments, update, close with optional comment, list with filters (state, labels, assignee, sort, direction) excluding pull requests, add comments, list comments with 500-comment truncation flag, add labels, idempotent label removal, and idempotent label creation.
- Provide full pull request operations: create (with draft flag), read metadata, list changed files, query PR state, list commit check runs, list reviews, post top-level review comments, merge with repo default strategy resolution, and close without merging.
- Integrate with `issuex` transport foundation for 8 MB response bounding, rate-limit backoff handling (HTTP 429 and HTTP 403 quota exhaustion with single retry under 2 minutes), and standardized error classification (`ErrNoToken`, `ErrNotFound`, `ErrConflict`, `ErrRateLimited`, `HTTPError`).

## Non-goals

- Implementing the GitLab client adapter (deferred to `issuex_gitlab`).
- Refactoring `agent-fox` commands and pipeline runners (`cmd/fix`, `cmd/impl`, `cmd/issue`, `codefix`, `codeimpl`, `issuetriage`, `internal/toolio`) to replace `internal/ghapi` (deferred to `issuex_agentfox_migration`).
- Defining the core `Client` interface, shared models, error types, URL parsing, or no-op implementation (completed in foundational spec `01_issuex_core`).
- Supporting git forges other than GitHub and GitLab (e.g., Bitbucket, Azure DevOps).
- GitHub GraphQL API support or webhook event handling.
- Git transport operations (clone, fetch, checkout, commit, push, rebase).
- Interactive OAuth token generation or user login workflows.

## Background

Today, `agent-fox` contains an internal GitHub REST client in `internal/ghapi/client.go` and `internal/ghapi/ref.go`. This client handles basic issue fetching, creation, updating, commenting, pull request creation, and file diff inspection. However, it is restricted to `internal/` (preventing sibling tools `hub` and `coder` from importing it), lacks critical pull request workflows (merging, check runs, review inspection, review commenting, closing), does not implement the unified `issuex.Client` interface, and cannot be used interchangeably with other forges.

The foundational spec `01_issuex_core` established the public `issuex` package, defining domain models (`Repo`, `IssueRef`, `Issue`, `PullRequest`, `CheckRun`, `Review`, `Repository`, etc.), the `Client` interface, remote URL parsing, error types, and the `NoOpClient`. In `01_issuex_core`, `NewWithOptions` returns `ErrUnsupportedForge` when GitHub is detected.

This spec implements the GitHub client adapter within `issuex`, fulfilling the `Client` contract against GitHub's REST API and replacing the temporary `ErrUnsupportedForge` stub with a production-ready GitHub client adapter.

## Requirements

### Requirement 1: GitHub Client Construction, Lifecycle, and Factory Wiring
- The `issuex` package shall provide a GitHub client adapter struct that implements every method of the `issuex.Client` interface.
- The package shall export `NewGitHub(o Options) (Client, error)` to construct a GitHub client directly:
  - If `o.BaseURL` is non-empty, the client uses `o.BaseURL` trimmed of any trailing slash. If `o.BaseURL` is empty, it uses the value of environment variable `GITHUB_API_URL`, falling back to `"https://api.github.com"`.
  - If `o.Token` is non-empty, it is used for authentication. If `o.Token` is empty, it resolves from `GITHUB_TOKEN`, falling back to `GH_TOKEN`.
  - If `o.HTTPClient` is provided, it is used as the underlying HTTP transport; otherwise, a default client with a 30-second timeout is used.
  - If `o.UserAgent` is non-empty, it is used as the `User-Agent` header value; otherwise, `"agent-fox"` is used.
- `NewWithOptions(o Options) (Client, error)` shall be updated so that when `o.NoOp` is false and the detected forge type resolves to `ForgeTypeGitHub`, it constructs and returns the GitHub client instead of returning `ErrUnsupportedForge`.
- `Authenticated() bool` shall return `true` when the client holds a non-empty authentication token and `false` otherwise.
- `Close() error` shall close idle HTTP connections on the underlying HTTP transport if supported (`CloseIdleConnections()`) and return `nil`.
- All outgoing HTTP requests shall include the following standard headers:
  - `Accept: application/vnd.github+json`
  - `X-GitHub-Api-Version: 2022-11-28`
  - `User-Agent: <UserAgent>`
  - `Authorization: Bearer <Token>` (omitted when unauthenticated)
  - `Content-Type: application/json` on requests with a request body.

### Requirement 2: Repository Metadata and Merge Policy Inspection
- `GetRepository(ctx context.Context, repo Repo) (Repository, error)` shall execute `GET /repos/{owner}/{name}`.
- If the repository is found (HTTP 200), it shall populate and return a `Repository` struct with:
  - `FullName`: GitHub repository full name (e.g. `"owner/name"`).
  - `DefaultBranch`: repository default branch name (e.g. `"main"`).
  - `Private`: boolean flag indicating private repository status.
  - `Archived`: boolean flag indicating archived status.
  - `Permissions`: `RepoPermissions` populated with `Push`, `Pull`, and `Admin` booleans from GitHub's `permissions` map.
- The client shall inspect the repository's merge strategy settings from GitHub's response (`allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`) to inform pull request merge resolution.
- If the request returns HTTP 404 on an unauthenticated client (`Authenticated() == false`), the error shall be wrapped with an explanatory hint indicating the repository may be private and require authentication.

### Requirement 3: Issue Lifecycle and Retrieval
- `CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/issues` with JSON body containing `title`, `body`, and optional `labels`.
  - Return the created `Issue` struct with `Number`, `Title`, `Body`, `State`, `URL`, `Author`, `Labels`, `CreatedAt`, `UpdatedAt`, and `IsPR: false`.
- `ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error)`:
  - Execute `GET /repos/{owner}/{name}/issues/{number}`.
  - If unauthenticated and status is 404, annotate the error with a private issue hint.
  - Parse the issue metadata into `IssueThread.Issue`. If the response payload contains a non-nil `pull_request` key, set `Issue.IsPR = true`.
  - Fetch issue comments chronologically via `ListComments(ctx, ref)`.
  - If comment fetching succeeds, populate `IssueThread.Comments` and `IssueThread.Truncated`.
  - If comment fetching fails, record the error in `IssueThread.CommentsErr` without failing the overall `ReadIssue` invocation.
- `UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PATCH /repos/{owner}/{name}/issues/{number}` with non-empty fields (`title`, `body`, and `state` if specified).
  - Return the updated `Issue` struct.
- `CloseIssue(ctx context.Context, ref IssueRef, comment string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - If `comment` is non-empty, call `AddComment(ctx, ref, comment)` first. If comment creation fails, return the error without closing the issue.
  - Execute `PATCH /repos/{owner}/{name}/issues/{number}` with `{"state": "closed"}`.
- `ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error)`:
  - Execute `GET /repos/{owner}/{name}/issues` with query parameters:
    - `state`: `"open"`, `"closed"`, or `"all"` (default `"open"`).
    - `labels`: comma-separated label names if `filter.Labels` is non-empty.
    - `assignee`: `filter.Assignee` if non-empty.
    - `sort`: `filter.Sort` if non-empty (e.g. `"created"`, `"updated"`).
    - `direction`: `filter.Direction` if non-empty (`"asc"` or `"desc"`).
    - `per_page`: 100.
  - Paginate requests sequentially until matching results reach `filter.Limit` (default 100) or no more pages are returned.
  - GitHub's issues endpoint returns both issues and pull requests; `ListIssues` shall exclude items with a non-nil `pull_request` key so only genuine issues are returned.
  - If total collected issues reach `filter.Limit` and additional issues remain on the server, set `IssueList.Incomplete = true`.

### Requirement 4: Issue Comments and Label Operations
- `AddComment(ctx context.Context, ref IssueRef, body string) (string, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/issues/{number}/comments` with `{"body": body}`.
  - Return the `html_url` string of the created comment.
- `ListComments(ctx context.Context, ref IssueRef) (CommentList, error)`:
  - Execute `GET /repos/{owner}/{name}/issues/{number}/comments?per_page=100&page={page}` starting at page 1.
  - Iterate up to a maximum cap of 5 pages (500 comments total).
  - Return `CommentList` containing parsed `Comment` structs (`Body`, `User`, `CreatedAt`, `HTMLURL`).
  - If comments hit the 500 cap and the 5th page returned a full 100 items, set `CommentList.Truncated = true`.
- `AddLabels(ctx context.Context, ref IssueRef, labels []string) error`:
  - If `labels` is empty, return `nil` immediately without network calls.
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/issues/{number}/labels` with `{"labels": labels}`. This operation is additive on GitHub.
- `RemoveLabel(ctx context.Context, ref IssueRef, label string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `DELETE /repos/{owner}/{name}/issues/{number}/labels/{label}` with URL path escaping for the label name.
  - If GitHub responds with HTTP 404 (label was not present on the issue), treat the call as successful and return `nil` (idempotent removal).
- `CreateLabel(ctx context.Context, repo Repo, label Label) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/labels` with `name`, `color` (stripping leading `#`), and optional `description`.
  - If GitHub responds with HTTP 422 containing validation code `"already_exists"`, treat the call as successful and return `nil` (idempotent creation).

### Requirement 5: Pull Request Lifecycle and File Changes
- `CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/pulls` with `title`, `body`, `head`, `base`, and `draft` boolean.
  - Return the created `PullRequest` struct (`Number`, `Title`, `Body`, `State`, `URL`, `Draft`, `HeadBranch`, `BaseBranch`, `HeadSHA`, `Merged: false`).
- `ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error)`:
  - Execute `GET /repos/{owner}/{name}/pulls/{number}`.
  - If unauthenticated and status is 404, annotate the error with a private repository hint.
  - Parse the JSON response into `PullRequest` including `Number`, `Title`, `Body`, `State`, `URL`, `Draft`, `Merged`, `HeadBranch` (`head.ref`), `BaseBranch` (`base.ref`), `HeadSHA` (`head.sha`), `Author`, and timestamps.
- `ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error)`:
  - Execute `GET /repos/{owner}/{name}/pulls/{number}/files?per_page=100`.
  - Parse results into `[]ChangedFile` containing `Filename`, `Status` (mapped to `"added"`, `"modified"`, `"deleted"`), `Additions`, and `Deletions`.
- `GetPRState(ctx context.Context, ref IssueRef) (PRState, error)`:
  - Execute `GET /repos/{owner}/{name}/pulls/{number}`.
  - Return `PRState` populated with `Number`, `State` (`"open"`, `"closed"`, or `"merged"` when `merged: true`), `Merged` boolean, and `HeadSHA`.
- `ClosePullRequest(ctx context.Context, ref IssueRef) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PATCH /repos/{owner}/{name}/pulls/{number}` with `{"state": "closed"}`.

### Requirement 6: Pull Request Checks, Reviews, and Merge Execution
- `GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error)`:
  - Retrieve the PR's head commit SHA by calling `ReadPullRequest(ctx, ref)` (or caching head SHA).
  - Execute `GET /repos/{owner}/{name}/commits/{head_sha}/check-runs?per_page=100`.
  - Map each entry to `CheckRun`: `Name` (`name`), `Status` (`status`), `Conclusion` (`conclusion`), `Summary` (`output.summary` or `output.title`), and `URL` (`html_url`).
- `GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error)`:
  - Execute `GET /repos/{owner}/{name}/pulls/{number}/reviews?per_page=100`.
  - Map each review to `Review`: `Author` (`user.login`), `State` (normalized lowercase: `"approved"`, `"changes_requested"`, `"commented"`, `"dismissed"`), `Body` (`body`), and `SubmittedAt` (`submitted_at`).
- `PostReviewComment(ctx context.Context, ref IssueRef, body string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /repos/{owner}/{name}/pulls/{number}/reviews` with `{"event": "COMMENT", "body": body}` to record a top-level review comment.
- `MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Resolve the merge method:
    - If `opts.Method` is `MergeMethodMerge`, use `"merge"`.
    - If `opts.Method` is `MergeMethodSquash`, use `"squash"`.
    - If `opts.Method` is `MergeMethodRebase`, use `"rebase"`.
    - If `opts.Method` is `MergeMethodDefault` (or empty): query repository merge settings (`GET /repos/{owner}/{name}`); if `allow_merge_commit` is true, use `"merge"`; else if `allow_squash_merge` is true, use `"squash"`; else if `allow_rebase_merge` is true, use `"rebase"`; otherwise omit `merge_method` in the payload to let GitHub apply its default.
  - Execute `PUT /repos/{owner}/{name}/pulls/{number}/merge` with JSON payload containing `commit_title`, `commit_message`, `sha` (if specified), and `merge_method` (if resolved).
  - If GitHub responds with HTTP 405 (not mergeable) or HTTP 409 (merge conflict / head SHA mismatch), return an error wrapping `ErrConflict`.
  - If merge succeeds (HTTP 200), return `MergeResult` with `Merged: true`, `SHA: resp.sha`, and `Message: resp.message`.

### Requirement 7: GitHub REST Transport, Error Handling, and Rate Limiting
- The GitHub client shall route all HTTP requests through the shared transport foundation established in `01_issuex_core`:
  - Response bodies shall be read through `io.LimitReader` bounded to a maximum of 8 MB (8,388,608 bytes).
  - HTTP responses with status 429 or status 403 where `X-RateLimit-Remaining: 0` shall have their backoff calculated via `RetryAfter()`. If backoff is <= 120 seconds, the client shall wait and retry the request once. If backoff > 120 seconds or retry fails, return an error wrapping `ErrRateLimited`.
  - The client adapter shall support an injectable sleep function (`func(time.Duration)`) defaulting to `time.Sleep` for deterministic, zero-delay unit testing of rate-limit backoff.
- GitHub error response JSON payloads shall be parsed to extract structured error details:
  - Format: `<message>` plus specific field error entries from `errors` array (e.g. `"Validation Failed (name.already_exists)"`).
  - Errors shall be mapped to `HTTPError` retaining `Method`, `Path`, `Status`, `Message`, and rate-limit headers.
  - Sentinel wrapping: HTTP 404 responses shall wrap `ErrNotFound`; HTTP 409 and 405 shall wrap `ErrConflict`; unauthenticated write operations shall return `ErrNoToken`.

## Design Decisions

1. **Implementation within `package issuex` directly**: Rather than introducing an isolated subpackage like `issuex/github` which would produce a circular dependency when `issuex.NewWithOptions` creates provider clients, the GitHub adapter is implemented directly in `package issuex` across `github.go`, `github_issues.go`, `github_pr.go`, and `github_test.go`.
2. **Filtering out pull requests in `ListIssues`**: GitHub's `/repos/{owner}/{repo}/issues` endpoint natively returns both issues and pull requests; the adapter filters out entries with a `pull_request` key so callers receive only genuine issues, matching user expectations and separating issue vs PR workflows.
3. **Repository merge policy fallback for `MergeMethodDefault`**: When merging a pull request without an explicit merge method (`MergeMethodDefault`), the adapter queries the repository's allowed merge strategies (`allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`) and selects the first enabled strategy (merge commit > squash > rebase), ensuring PR merges follow repository configuration without forcing callers to know each repository's settings.
4. **Idempotent label mutations**: `RemoveLabel` treats HTTP 404 as success (label not present), and `CreateLabel` treats HTTP 422 with `already_exists` code as success, guaranteeing that labeling workflows do not fail when reconciling desired label states.
5. **Top-level review comments via PR reviews endpoint**: `PostReviewComment` issues a `POST /repos/{owner}/{repo}/pulls/{number}/reviews` with event `"COMMENT"`, distinguishing formal PR code reviews from standard issue comments (`AddComment`).
6. **Resolving PR Head SHA for CI checks**: Because `GetCIChecks` receives an `IssueRef` without a commit SHA, the client fetches the PR metadata to determine the current `head.sha` before querying `/commits/{head_sha}/check-runs`.
7. **Partial comment error resilience in `ReadIssue`**: If fetching comments fails after successfully retrieving an issue, `ReadIssue` returns the populated issue and records the failure in `IssueThread.CommentsErr`, preserving existing triage tool resilience from `ghapi`.
8. **Enterprise endpoint normalization**: The constructor accepts custom enterprise REST base URLs (e.g. `https://ghe.example.com/api/v3`) from options or `GITHUB_API_URL`, trimming trailing slashes while preserving API subpaths.
9. **Injectable sleep for rate-limit testing**: The GitHub client adapter accepts an injectable `sleep func(time.Duration)` internally, allowing test suites to verify rate-limit backoff and single-retry logic without real wall-clock delays.
10. **Zero external HTTP dependencies**: Transport is built strictly on standard library `net/http` and `encoding/json` with no third-party HTTP client libraries, maintaining consistency with ecosystem requirements.

## Dependencies

| Spec | Status | Reason |
|---|---|---|
| `01_issuex_core` | active | Supplies `Client` interface, domain data models, options configuration, forge detection, error hierarchy, and shared transport foundation that this adapter implements. |
