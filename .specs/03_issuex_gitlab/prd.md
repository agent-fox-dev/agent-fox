---
spec_id: "03"
spec_name: "issuex_gitlab"
title: "Forge-Neutral Issue and Pull Request Client: GitLab Adapter"
status: "active"
created_at: "2026-09-10T17:05:09.819551Z"
updated_at: "2026-09-10T17:05:09.819551Z"
intent_hash: "d8f611c9491ee4c4bac0feb28d00aef88dc3fb772e2e4f59958a1842d4f84cae"
schema_version: 2
source: "docs/drafts/forge_neutral_issue_pr_client.md"
---
## Intent

Provide a GitLab REST API v4 adapter within the `issuex` package that implements the forge-neutral `Client` interface for project metadata, issue management, and merge request operations across gitlab.com and self-hosted GitLab instances.

## Goals

- Implement the complete `issuex.Client` (and `issuex.ForgeClient`) interface against GitLab REST API v4.
- Wire the GitLab client adapter into `issuex.NewWithOptions` and `issuex.New` so that resolving `ForgeTypeGitLab` automatically constructs and returns the GitLab client.
- Export an explicit constructor `NewGitLab(Options) (Client, error)` for callers targeting GitLab directly.
- Support self-hosted and GitLab Community/Enterprise Edition instances via configurable `BaseURL` (and `GITLAB_API_URL`), standardizing custom hostnames and subpaths to include the `/api/v4` prefix.
- Provide full issue operations: create, read with comments, update, close with optional comment, list with filters (state, labels, assignee, sort, direction), add comments, list comments with 500-comment truncation and system-note filtering, add labels, idempotent label removal, and idempotent label creation.
- Provide full merge request operations: create (with draft status), read metadata, list changed files with diff additions/deletions statistics, query MR state, list pipeline job checks, list reviews (aggregating approvals and discussion notes), post top-level review comments, merge with repository default strategy resolution, and close without merging.
- Integrate with `issuex` transport foundation for 8 MB response bounding, rate-limit backoff handling (HTTP 429 with single retry under 2 minutes), and standardized error classification (`ErrNoToken`, `ErrNotFound`, `ErrConflict`, `ErrRateLimited`, `HTTPError`).

## Non-goals

- Implementing the GitHub client adapter (delivered in `02_issuex_github`).
- Refactoring `agent-fox` commands and pipeline runners (`cmd/fix`, `cmd/impl`, `cmd/issue`, `codefix`, `codeimpl`, `issuetriage`, `internal/toolio`) to replace `internal/ghapi` (deferred to `issuex_agentfox_migration`).
- Defining core `Client` interfaces, shared models, error types, URL parsing, or no-op implementation (delivered in foundational spec `01_issuex_core`).
- Supporting git forges other than GitHub and GitLab (e.g., Bitbucket, Azure DevOps).
- GitLab GraphQL API support or webhook event handling.
- Git transport operations (clone, fetch, checkout, commit, push, rebase).
- Interactive OAuth token generation or user login workflows.

## Background

Today, `agent-fox` only supports GitHub via `internal/ghapi/client.go` and `internal/ghapi/ref.go`. Because this code lives in `internal/` and is hardcoded to GitHub endpoints, sibling tools (`hub` and `coder`) cannot import it, and no tool in the ecosystem can interact with repositories hosted on gitlab.com or self-hosted GitLab instances.

The foundational spec `01_issuex_core` established the public `issuex` package, defining domain models (`Repo`, `IssueRef`, `Issue`, `PullRequest`, `CheckRun`, `Review`, `Repository`, etc.), the unified `Client` interface, git remote/URL parsing for GitLab nested group paths (e.g., `group/subgroup/project`), error types, and transport safety utilities. In `01_issuex_core`, `NewWithOptions` returns `ErrUnsupportedForge` when GitLab is detected. Sibling spec `02_issuex_github` implemented the GitHub adapter.

This spec implements the GitLab client adapter within `issuex`, fulfilling the `Client` contract against GitLab REST API v4, handling GitLab-specific concepts (URL-encoded project paths, issue/MR IIDs, CI pipeline jobs, approvals, system notes, and merge strategies), and replacing the temporary `ErrUnsupportedForge` stub with an active GitLab adapter.

## Requirements

### Requirement 1: GitLab Client Construction, Lifecycle, and Factory Wiring
- The `issuex` package shall provide a GitLab client adapter struct that implements every method of the `issuex.Client` interface.
- The package shall export `NewGitLab(o Options) (Client, error)` to construct a GitLab client directly:
  - If `o.BaseURL` is non-empty, the client trims trailing slashes. If the trimmed URL does not end with `/api/v4`, the adapter appends `/api/v4` to ensure valid endpoint routing. If `o.BaseURL` is empty, it uses the value of environment variable `GITLAB_API_URL` (normalized similarly), falling back to `"https://gitlab.com/api/v4"`.
  - If `o.Token` is non-empty, it is used for authentication. If `o.Token` is empty, it resolves from `GITLAB_TOKEN`.
  - If `o.HTTPClient` is provided, it is used as the underlying HTTP transport; otherwise, a default client with a 30-second timeout is used.
  - If `o.UserAgent` is non-empty, it is used as the `User-Agent` header value; otherwise, `"agent-fox"` is used.
- `NewWithOptions(o Options) (Client, error)` shall be updated so that when `o.NoOp` is false and the resolved forge type is `ForgeTypeGitLab`, it constructs and returns the GitLab client adapter instead of returning `ErrUnsupportedForge`.
- When `NewWithOptions` encounters an ambiguous URL that does not explicitly match `github` or `gitlab` domain strings, it shall issue a `GET /api/v4/version` request to probe for a GitLab self-hosted instance before returning `ErrAmbiguousForge`.
- `Authenticated() bool` shall return `true` when the client holds a non-empty authentication token and `false` otherwise.
- `Close() error` shall close idle HTTP connections on the underlying HTTP transport if supported (`CloseIdleConnections()`) and return `nil`.
- Outgoing HTTP requests shall include the following standard headers:
  - `Accept: application/json`
  - `User-Agent: <UserAgent>`
  - `PRIVATE-TOKEN: <Token>` (omitted when unauthenticated)
  - `Content-Type: application/json` on requests carrying a request body.

### Requirement 2: Project Metadata and URL-Encoded Path Resolution
- All GitLab project-scoped REST endpoints shall identify target projects using URL-encoded paths formatted as `url.PathEscape(repo.String())` with `/` replaced by `%2F` (e.g. `group%2Fsubgroup%2Fproject` or `owner%2Frepo`), allowing transparent access without requiring numeric project ID lookup calls.
- `GetRepository(ctx context.Context, repo Repo) (Repository, error)` shall execute `GET /projects/{project_path}`.
- If the project is found (HTTP 200), it shall populate and return a `Repository` struct with:
  - `FullName`: GitLab `path_with_namespace` (e.g. `"group/subgroup/project"`).
  - `DefaultBranch`: project default branch (e.g. `"main"`).
  - `Private`: boolean flag set to `true` when `visibility` is `"private"` or `"internal"`, and `false` when `"public"`.
  - `Archived`: boolean flag from GitLab `archived` field.
  - `Permissions`: `RepoPermissions` computed from `permissions.project_access.access_level` and `permissions.group_access.access_level`. Let `effective_level` be the maximum of both access levels:
    - `Pull`: `true` if `effective_level >= 20` (Reporter or higher), or if the project is not private.
    - `Push`: `true` if `effective_level >= 30` (Developer or higher).
    - `Admin`: `true` if `effective_level >= 40` (Maintainer or Owner).
- The client shall inspect the project's merge settings from the response (`merge_method`, `squash_option`) to inform pull request merge resolution.
- If `GetRepository` returns HTTP 404 on an unauthenticated client (`Authenticated() == false`), the error shall be wrapped with an explanatory hint indicating the project may be private and require setting `GITLAB_TOKEN`.

### Requirement 3: Issue Lifecycle and Retrieval
- `CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /projects/{project_path}/issues` with JSON body containing `title`, `description` (set to `req.Body`), and optional comma-separated `labels` string.
  - Return the created `Issue` struct with `Number: iid`, `Title: title`, `Body: description`, `State: "open"` (normalizing GitLab `"opened"`), `URL: web_url`, `Author: User{Login: author.username}`, `Labels: labels`, `CreatedAt`, `UpdatedAt`, `ClosedAt`, and `IsPR: false`.
- `ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error)`:
  - Execute `GET /projects/{project_path}/issues/{number}` using `ref.Number` as the issue `iid`.
  - If unauthenticated and status is 404, annotate the error with a private issue hint.
  - Parse the issue metadata into `IssueThread.Issue`, normalizing `state` (`"opened"` to `"open"`, `"closed"` to `"closed"`) and setting `IsPR: false`.
  - Fetch issue comments chronologically via `ListComments(ctx, ref)`.
  - If comment fetching succeeds, populate `IssueThread.Comments` and `IssueThread.Truncated`.
  - If comment fetching fails, record the error in `IssueThread.CommentsErr` without failing the overall `ReadIssue` invocation.
- `UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PUT /projects/{project_path}/issues/{number}` with non-empty fields: `title`, `description` (from `req.Body`), and `state_event: "close"` or `"reopen"` if state modification is requested.
  - Return the updated `Issue` struct.
- `CloseIssue(ctx context.Context, ref IssueRef, comment string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - If `comment` is non-empty, call `AddComment(ctx, ref, comment)` first. If comment creation fails, return the error without closing the issue.
  - Execute `PUT /projects/{project_path}/issues/{number}` with `{"state_event": "close"}`.
- `ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error)`:
  - Execute `GET /projects/{project_path}/issues` with query parameters:
    - `state`: `"opened"` if `filter.State == "open"`, `"closed"` if `filter.State == "closed"`, or `"all"` if `filter.State == "all"` (default `"opened"`).
    - `labels`: comma-separated label names if `filter.Labels` is non-empty.
    - `assignee_username`: `filter.Assignee` if non-empty.
    - `order_by`: `"created_at"` or `"updated_at"` mapped from `filter.Sort` (default `"created_at"`).
    - `sort`: `filter.Direction` (`"asc"` or `"desc"`, default `"desc"`).
    - `per_page`: 100.
  - Paginate requests sequentially until matching results reach `filter.Limit` (default 100) or no more pages remain.
  - If total collected issues reach `filter.Limit` and additional issues remain on the server, set `IssueList.Incomplete = true`.

### Requirement 4: Issue Comments and Label Management
- `AddComment(ctx context.Context, ref IssueRef, body string) (string, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /projects/{project_path}/issues/{number}/notes` with `{"body": body}`.
  - Return the web URL of the comment, using the note's `web_url` if present, or constructing `{issue_url}#note_{note_id}`.
- `ListComments(ctx context.Context, ref IssueRef) (CommentList, error)`:
  - Execute `GET /projects/{project_path}/issues/{number}/notes?per_page=100&page={page}&sort=asc&order_by=created_at` starting at page 1.
  - Filter out system audit notes where `system == true` so only user comments are included.
  - Iterate up to a maximum cap of 5 pages (500 notes total).
  - Return `CommentList` containing parsed `Comment` structs (`Body: body`, `User: User{Login: author.username}`, `CreatedAt: created_at`, `HTMLURL: web_url`).
  - If comments hit the 500 cap and further comments exist on the server, set `CommentList.Truncated = true`.
- `AddLabels(ctx context.Context, ref IssueRef, labels []string) error`:
  - If `labels` is empty, return `nil` immediately without network calls.
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PUT /projects/{project_path}/issues/{number}` with `{"add_labels": strings.Join(labels, ",")}`. This operation is natively additive in GitLab.
- `RemoveLabel(ctx context.Context, ref IssueRef, label string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PUT /projects/{project_path}/issues/{number}` with `{"remove_labels": label}`.
  - Succeeds silently if the label is not currently present on the issue (idempotent removal).
- `CreateLabel(ctx context.Context, repo Repo, label Label) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Format color with leading `#` (e.g. `"#428BCA"`).
  - Execute `POST /projects/{project_path}/labels` with `name`, `color`, and optional `description`.
  - If GitLab responds with HTTP 409 (or HTTP 400 with message containing "already exists"), treat the call as successful and return `nil` (idempotent creation).

### Requirement 5: Merge Request Lifecycle and Diff Inspection
- `CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Format title: if `req.Draft` is true and `title` does not start with `"Draft: "`, prefix with `"Draft: "`.
  - Execute `POST /projects/{project_path}/merge_requests` with `source_branch: req.Head`, `target_branch: req.Base`, `title: title`, and `description: req.Body`.
  - Return `PullRequest` with `Number: iid`, `Title: title`, `Body: description`, `State: "open"`, `URL: web_url`, `Draft: draft`, `HeadBranch: source_branch`, `BaseBranch: target_branch`, `HeadSHA: sha`, and `Merged: false`.
- `ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error)`:
  - Execute `GET /projects/{project_path}/merge_requests/{number}` using `ref.Number` as the MR `iid`.
  - If unauthenticated and status is 404, annotate the error with a private repository hint.
  - Parse response into `PullRequest`: normalize `state` (`"opened"` to `"open"`, `"closed"` to `"closed"`, `"merged"` to `"merged"`), set `Merged: (state == "merged")`, `Draft: (draft || work_in_progress)`, `HeadBranch: source_branch`, `BaseBranch: target_branch`, `HeadSHA: sha`, `Author: User{Login: author.username}`, and timestamps.
- `ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error)`:
  - Execute `GET /projects/{project_path}/merge_requests/{number}/changes`.
  - For each change entry in `changes`:
    - `Filename`: `new_path` (or `old_path` if deleted).
    - `Status`: `"added"` if `new_file == true`, `"deleted"` if `deleted_file == true`, otherwise `"modified"`.
    - `Additions` and `Deletions`: calculated by counting lines in `diff` starting with `+` (excluding leading `+++`) and `-` (excluding leading `---`).
- `GetPRState(ctx context.Context, ref IssueRef) (PRState, error)`:
  - Execute `GET /projects/{project_path}/merge_requests/{number}`.
  - Return `PRState` populated with `Number: iid`, `State` (`"open"`, `"closed"`, or `"merged"`), `Merged: (state == "merged")`, and `HeadSHA: sha`.
- `ClosePullRequest(ctx context.Context, ref IssueRef) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `PUT /projects/{project_path}/merge_requests/{number}` with `{"state_event": "close"}`.

### Requirement 6: Merge Request Pipeline Checks, Reviews, and Merge Execution
- `GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error)`:
  - Execute `GET /projects/{project_path}/merge_requests/{number}/pipelines?per_page=1` to retrieve the latest CI pipeline for the MR.
  - If no pipelines are returned, return an empty slice `[]CheckRun{}` and `nil`.
  - Execute `GET /projects/{project_path}/pipelines/{pipeline_id}/jobs?per_page=100` for the latest pipeline.
  - Map each job to `CheckRun`:
    - `Name`: `job.name`.
    - `Status`: map GitLab job status (`"running"`, `"pending"`, `"created"` to `"in_progress"`; `"success"`, `"failed"`, `"canceled"`, `"skipped"` to `"completed"`).
    - `Conclusion`: map GitLab status (`"success"` to `"success"`, `"failed"` to `"failure"`, `"canceled"` to `"cancelled"`, `"skipped"` to `"skipped"`).
    - `Summary`: `job.stage + ": " + job.status` (appending failure reason if non-empty).
    - `URL`: `job.web_url`.
- `GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error)`:
  - Query merge request approvals via `GET /projects/{project_path}/merge_requests/{number}/approvals`, mapping each entry in `approved_by` to `Review{Author: User{Login: user.username}, State: "approved", SubmittedAt: ...}`.
  - Query merge request discussion notes via `GET /projects/{project_path}/merge_requests/{number}/notes?per_page=100&sort=asc`, filtering for `system == false` and mapping each to `Review{Author: User{Login: author.username}, State: "commented", Body: body, SubmittedAt: created_at}`.
  - Return the combined slice of reviews in chronological order.
- `PostReviewComment(ctx context.Context, ref IssueRef, body string) error`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Execute `POST /projects/{project_path}/merge_requests/{number}/notes` with `{"body": body}` to record a top-level review comment.
- `MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error)`:
  - If `Authenticated()` is false, reject immediately with `ErrNoToken`.
  - Build merge payload:
    - `commit_message`: `opts.CommitMessage` (or `opts.CommitTitle`).
    - `sha`: `opts.SHA` if non-empty (ensuring head commit matches).
    - `squash`: if `opts.Method == MergeMethodSquash`, set `squash: true`; if `opts.Method == MergeMethodMerge`, set `squash: false`; if `opts.Method == MergeMethodDefault`, omit `squash` to let repository settings govern.
  - Execute `PUT /projects/{project_path}/merge_requests/{number}/merge`.
  - If GitLab responds with HTTP 405 (branch cannot be merged), HTTP 406 (merge conflicts), or HTTP 409 (SHA mismatch), return an error wrapping `ErrConflict`.
  - If merge succeeds (HTTP 200), return `MergeResult` with `Merged: true`, `SHA: resp.merge_commit_sha` (or `resp.sha`), and `Message: "merged"`.

### Requirement 7: GitLab REST Transport, Error Handling, and Rate Limiting
- The GitLab client shall route all HTTP requests through the shared transport foundation:
  - Response bodies shall be read through `io.LimitReader` bounded to a maximum of 8 MB (8,388,608 bytes).
  - HTTP 429 responses shall have backoff calculated via `RetryAfter()`. If backoff is <= 120 seconds, wait and retry once. If backoff > 120 seconds or retry fails, return an error wrapping `ErrRateLimited`.
  - The client adapter shall support an injectable sleep function (`func(time.Duration)`) defaulting to `time.Sleep` for unit testing of rate-limit backoff without delays.
- GitLab error response JSON payloads shall be parsed to extract structured error details:
  - If `message` is a plain string, use it; if `message` is a JSON object with field validation arrays (e.g. `{"message": {"title": ["can't be blank"]}}`), format into `"field: error"`; if `error` / `error_description` fields are present, format as `"<error>: <error_description>"`.
  - Map errors to `HTTPError` retaining `Method`, `Path`, `Status`, `Message`, and rate-limit headers.
  - Sentinel wrapping: HTTP 404 responses shall wrap `ErrNotFound`; HTTP 405, 406, and 409 responses shall wrap `ErrConflict`; unauthenticated write operations shall return `ErrNoToken`.

## Design Decisions

1. **Direct implementation in `package issuex`**: The GitLab adapter is implemented directly in `package issuex` across `gitlab.go`, `gitlab_issues.go`, `gitlab_mr.go`, and `gitlab_test.go`, avoiding circular package dependencies when `issuex.NewWithOptions` constructs provider clients.
2. **URL-encoded project path routing**: GitLab REST API v4 accepts URL-encoded project paths (`owner%2Frepo` or `group%2Fsubgroup%2Fproject`) across all project endpoints. Encoding slashes as `%2F` via a shared `projectPath(Repo) string` helper avoids an extra API roundtrip to resolve a numeric project ID.
3. **Internal IID mapping for issues and merge requests**: GitLab entities have both an internal project-scoped `iid` and a global database `id`. `IssueRef.Number`, `Issue.Number`, and `PullRequest.Number` map to GitLab's `iid`, matching the numbers displayed in GitLab web URLs and UI references (`#42` / `!42`).
4. **Filtering out system notes from comments and reviews**: GitLab stores both user discussion comments and automated system events ("closed", "assigned to...", "changed title...") as "notes". The adapter explicitly filters out notes where `system == true` in `ListComments` and `GetPRReviews` so only genuine human or bot commentary is returned.
5. **Normalizing GitLab state representations**: GitLab represents open entities as `"opened"`, whereas GitHub and the forge-neutral domain model use `"open"`. The GitLab adapter maps `"opened"` to `"open"` on ingress and converts `"open"` to `"opened"` on list query filters.
6. **Synthesizing changed files diff statistics**: GitLab's `/merge_requests/:iid/changes` endpoint does not always provide separate addition and deletion counts per file across all GitLab versions. The adapter parses the unified diff chunk (`diff`) counting added (`+`) and deleted (`-`) lines, ensuring accurate diff statistics regardless of GitLab server version.
7. **Mapping CI pipeline jobs to `CheckRun`**: GitLab executes CI/CD as pipelines containing stage jobs. `GetCIChecks` queries the latest pipeline attached to the merge request (`GET /projects/:id/merge_requests/:iid/pipelines`) and fetches its constituent jobs (`GET /projects/:id/pipelines/:id/jobs`), mapping job name, stage status, and failure reason to forge-neutral `CheckRun` records.
8. **Combining approvals and top-level notes for PR reviews**: Because GitLab does not have a single unified GitHub-style review submission model, `GetPRReviews` queries both merge request approvals (`/approvals`, mapped to `State: "approved"`) and top-level discussion notes (`/notes`, mapped to `State: "commented"`), giving callers complete visibility into review activity.
9. **Standardizing API base URL for self-hosted instances**: When custom self-hosted base URLs are supplied (e.g. `https://gitlab.example.com`), the constructor trims trailing slashes and ensures the `/api/v4` suffix is appended if omitted, guaranteeing consistent API routing without user configuration errors.
10. **Authentication header preference**: GitLab REST API v4 supports `PRIVATE-TOKEN: <token>` as well as `Authorization: Bearer <token>`. The adapter emits `PRIVATE-TOKEN: <token>` when authenticated, ensuring universal compatibility across GitLab Personal, Project, and Group Access Tokens.

## Dependencies

| Spec | Status | Reason |
|---|---|---|
| `01_issuex_core` | active | Supplies `Client` interface, domain data models, options configuration, forge detection, error hierarchy, and shared transport foundation that this adapter implements. |
| `02_issuex_github` | active | Shares the `issuex` package and `NewWithOptions` client factory dispatch logic, where `NewGitLab` is registered alongside `NewGitHub`. |
