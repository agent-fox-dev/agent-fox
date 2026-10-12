---
spec_id: "01"
spec_name: "issuex_core"
title: "Forge-Neutral Issue and Pull Request Client Core"
status: "active"
created_at: "2026-09-10T16:55:22.682974Z"
updated_at: "2026-09-10T16:55:22.682974Z"
intent_hash: "cc4cde5e19b0453ee5b2b58d1d53f0d7430e55a528437da4338938d94bff5fc6"
schema_version: 2
source: "docs/drafts/forge_neutral_issue_pr_client.md"
---
## Intent

Provide a public, forge-neutral Go package (`issuex`) defining the unified client interface, core domain models, standardized error hierarchy, git remote and issue URL detection, client options, and no-op implementation for git forge issue and pull request operations across GitHub and GitLab.

## Goals

- Define a single `Client` interface (aliased as `ForgeClient`) in `issuex` specifying repository, issue, and pull request operations using `context.Context` and value structs.
- Provide canonical data structures for repositories, issues, pull requests, comments, changed files, CI check runs, reviews, labels, and issue filtering options.
- Parse git remote URLs (HTTPS, SSH, SCP-style) and web URLs for both GitHub and GitLab (including GitLab nested group paths like `group/subgroup/project`).
- Auto-detect forge types (`github` vs `gitlab`) and endpoints from options and environment variables (`GITHUB_API_URL`, `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_API_URL`, `GITLAB_TOKEN`), returning a typed error on ambiguity.
- Provide a standardized error taxonomy (`HTTPError`, `ErrNoToken`, `ErrNotFound`, `ErrConflict`, `ErrRateLimited`, `ErrUnsupportedForge`, `ErrAmbiguousForge`) with status codes, error inspection helpers, and rate-limit backoff calculation.
- Implement a fully compliant `NoOpClient` that returns zero-values for issue reads, performs silent no-ops for issue writes, and returns typed errors for pull request operations.
- Provide transport utilities for rate-limit retry backoffs (capped at 2 minutes) and bounded response body reading (maximum 8 MB).

## Non-goals

- Implementing the GitHub REST API calls (deferred to `issuex_github`).
- Implementing the GitLab REST API calls (deferred to `issuex_gitlab`).
- Migrating existing callers in `agent-fox` (`cmd/issue`, `cmd/fix`, `cmd/impl`, `codefix`, `codeimpl`, `issuetriage`, `internal/toolio`) away from `internal/ghapi` (deferred to `issuex_agentfox_migration`).
- Support for git forges other than GitHub and GitLab (such as Bitbucket or Azure DevOps).
- GraphQL APIs, webhook processing, or git transport operations (clone, commit, push, fetch).
- OAuth token acquisition or interactive user authentication flows.

## Background

Today, `agent-fox` contains a GitHub REST client in `internal/ghapi/client.go` and `internal/ghapi/ref.go`. Because it lives in `internal/`, it cannot be imported by sibling ecosystem repositories such as `hub` and `coder`. Furthermore, existing callers (`codefix/pipeline.go`, `codeimpl/pipeline.go`, `issuetriage/pipeline.go`, `internal/toolio/input.go`, and `cmd/{fix,impl,issue}/main.go`) are tightly coupled to GitHub-specific endpoint structures and cannot interact with GitLab repositories or self-hosted GitLab instances.

The proposed `issuex` module provides a single shared abstraction so tools can interact with any supported forge using identical method signatures. To ensure the feature remains cohesive and manageable within spec limits, the work is split into four scopes:
1. `issuex_core` (this PRD): Core interfaces, models, remote/URL detection, error taxonomy, options parsing, transport safety utilities, and the no-op implementation.
2. `issuex_github`: GitHub REST client implementation of the interface.
3. `issuex_gitlab`: GitLab REST client implementation of the interface.
4. `issuex_agentfox_migration`: Replacing `internal/ghapi` across `agent-fox` with `issuex`.

## Requirements

### Requirement 1: Forge Client Interface Contract
The `issuex` package shall define the primary interface `Client` (with type alias `ForgeClient = Client`) containing the unified method signatures for repository, issue, and pull request operations:
- Every operation accepts `context.Context` as its first parameter.
- Return values use value types (structs and slices) rather than pointers.
- Lifecycle management includes `Authenticated() bool` to check for configured credentials without network calls, and `Close() error` to release underlying resources.
- Repository operations include:
  - `GetRepository(ctx context.Context, repo Repo) (Repository, error)`
- Issue operations include:
  - `CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error)`
  - `ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error)`
  - `UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error)`
  - `CloseIssue(ctx context.Context, ref IssueRef, comment string) error`
  - `ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error)`
  - `AddComment(ctx context.Context, ref IssueRef, body string) (string, error)`
  - `ListComments(ctx context.Context, ref IssueRef) (CommentList, error)`
  - `AddLabels(ctx context.Context, ref IssueRef, labels []string) error`
  - `RemoveLabel(ctx context.Context, ref IssueRef, label string) error`
  - `CreateLabel(ctx context.Context, repo Repo, label Label) error`
- Pull request operations include:
  - `CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error)`
  - `ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error)`
  - `ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error)`
  - `GetPRState(ctx context.Context, ref IssueRef) (PRState, error)`
  - `GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error)`
  - `GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error)`
  - `PostReviewComment(ctx context.Context, ref IssueRef, body string) error`
  - `MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error)`
  - `ClosePullRequest(ctx context.Context, ref IssueRef) error`

### Requirement 2: Domain Data Models
The package shall define domain data types that unify GitHub and GitLab representations:
- `Repo`: Represents a repository target with `Owner string`, `Name string`, and optional `Host string`. `Repo.String()` returns `Owner/Name`. For GitLab, `Owner` may contain nested group segments (e.g. `group/subgroup`). `Repo.Valid()` reports whether both `Owner` and `Name` are non-empty.
- `IssueRef`: Identifies an issue or pull request, containing `Repo Repo`, `Number int`, and `IsPullRequest bool`. `IssueRef.String()` returns `owner/repo#number`. `IssueRef.URL()` returns the canonical web URL when host or endpoints are known.
- `Issue`: Represents an issue with `Number int`, `Title string`, `Body string`, `State string` (`"open"` or `"closed"`), `URL string`, `Author User`, `Labels []string`, `CreatedAt time.Time`, `UpdatedAt time.Time`, `ClosedAt *time.Time`, and `IsPR bool`.
- `IssueThread`: Represents an issue and its comments, containing `Issue Issue`, `Comments []Comment`, `Truncated bool`, and `CommentsErr error`.
- `PullRequest`: Represents a pull request or merge request with `Number int`, `Title string`, `Body string`, `State string` (`"open"`, `"closed"`, `"merged"`), `URL string`, `Draft bool`, `Merged bool`, `HeadSHA string`, `HeadBranch string`, `BaseBranch string`, `Author User`, and timestamps.
- `ChangedFile`: Contains `Filename string`, `Status string` (`"added"`, `"modified"`, `"deleted"`), `Additions int`, and `Deletions int`.
- `PRState`: Represents lightweight PR status with `Number int`, `State string`, `Merged bool`, and `HeadSHA string`.
- `CheckRun`: Represents a CI check or pipeline job with `Name string`, `Status string`, `Conclusion string`, `Summary string`, and `URL string`.
- `Review`: Represents a review entry with `Author User`, `State string` (`"approved"`, `"changes_requested"`, `"commented"`), `Body string`, and `SubmittedAt time.Time`.
- `Label`: Contains `Name string`, `Color string`, and `Description string`.
- `Repository`: Contains `FullName string`, `DefaultBranch string`, `Private bool`, `Archived bool`, and `Permissions RepoPermissions` (`Push bool`, `Pull bool`, `Admin bool`).
- `IssueFilter`: Configures issue filtering with `State string` (`"open"`, `"closed"`, `"all"`), `Labels []string`, `Assignee string`, `Sort string`, `Direction string`, and `Limit int` (default 100).
- `IssueList`: Contains `Issues []Issue` and `Incomplete bool` indicating if results were capped.
- `CommentList`: Contains `Comments []Comment` and `Truncated bool` (capped at 500 comments).
- `MergeOptions`: Configures pull request merging with `Method MergeMethod` (`MergeMethodDefault`, `MergeMethodMerge`, `MergeMethodSquash`, `MergeMethodRebase`), `CommitTitle string`, `CommitMessage string`, and `SHA string`.
- `MergeResult`: Returns `Merged bool`, `SHA string`, and `Message string`.

### Requirement 3: Git Remote and URL Parsing
The package shall provide parsing functions to resolve repository references from user input and git configurations:
- `ParseRepo(s string) (Repo, bool)`: Parses an owner/repo or group/project string. For paths with multiple slashes (e.g. `a/b/c`), the final path segment is assigned to `Name` and all preceding segments joined by `/` are assigned to `Owner`.
- `ParseRemote(remote string) (Repo, bool)`: Parses git remote URLs across HTTPS (`https://host/owner/repo.git`), SSH (`ssh://git@host/owner/repo.git`), and SCP-style syntax (`git@host:owner/repo.git`). Supports multi-segment paths for GitLab remotes. Returns `Repo` with `Host`, `Owner`, and `Name`.
- `DetectRepo(dir string) (Repo, bool)`: Runs `git -C <dir> remote get-url origin` and passes the output to `ParseRemote`. If the command fails or remote is missing, returns false without error.
- `ParseIssueURL(s string) (IssueRef, bool)`: Parses GitHub web issue URLs (`https://{host}/{owner}/{repo}/issues/{id}`) and PR URLs (`.../pull/{id}`), as well as GitLab web issue URLs (`https://{host}/{group}/{project}/-/issues/{id}`) and MR URLs (`.../-/merge_requests/{id}`). Returns `IssueRef` with `IsPullRequest` set accordingly.

### Requirement 4: Forge Detection and Options Configuration
The constructor `NewWithOptions(o Options) (Client, error)` and shorthand `New(userAgent string) (Client, error)` shall instantiate the client based on `Options`:
- `Options` fields: `BaseURL string`, `Token string`, `HTTPClient *http.Client`, `UserAgent string`, and `NoOp bool`.
- If `NoOp` is true, the constructor returns an instance of `NoOpClient`.
- If `BaseURL` is set, forge type is determined: URLs containing `github` or `api.github.com` resolve to `ForgeTypeGitHub`; URLs containing `gitlab` resolve to `ForgeTypeGitLab`. If the URL host is ambiguous, the constructor returns an error wrapped in `ErrAmbiguousForge`.
- If `BaseURL` is empty, environment variables are inspected:
  - If `GITHUB_API_URL`, `GITHUB_TOKEN`, or `GH_TOKEN` is present and no GitLab environment variables are present, forge resolves to `ForgeTypeGitHub` with default URL `https://api.github.com` and token from `GITHUB_TOKEN` (fallback `GH_TOKEN`).
  - If `GITLAB_API_URL` or `GITLAB_TOKEN` is present and no GitHub environment variables are present, forge resolves to `ForgeTypeGitLab` with default URL `https://gitlab.com/api/v4` and token from `GITLAB_TOKEN`.
  - If variables for both forges or neither forge are present, the constructor inspects git origin remote via `DetectRepo(".")`. If git origin remote matches a recognized host, the corresponding forge is used. If still unresolved, constructor returns `ErrAmbiguousForge`.
- When a real forge type (`github` or `gitlab`) is detected in `issuex_core`, before the provider adapters are introduced, the constructor returns `ErrUnsupportedForge` indicating that the detected provider is not yet loaded.
- Default `HTTPClient` timeout is 30 seconds when not overridden. Default `UserAgent` is `"agent-fox"`.

### Requirement 5: Standardized Error Types and Classification
The package shall provide typed errors allowing callers to handle specific failure modes with `errors.Is` and `errors.As`:
- `HTTPError`: Represents an HTTP error response, containing `Method string`, `Path string`, `Status int`, `Message string`, `RetryAfterHeader string`, `RateLimitReset string`, and `RateLimitRemaining string`.
- `HTTPError.Error() string`: Formats the error as `"<method> <path>: <status> <message>"`.
- `HTTPError.RetryAfter() (time.Duration, bool)`: Calculates the backoff duration when status is 429, or 403 with `X-RateLimit-Remaining: 0`. It checks numeric seconds in `Retry-After` header or epoch timestamp in `X-RateLimit-Reset` header, adds a 1-second buffer, and returns false if no valid header is present.
- Sentinel errors:
  - `ErrNoToken`: Returned by write operations when unauthenticated.
  - `ErrNotFound`: Indicates a resource was not found (HTTP 404).
  - `ErrConflict`: Indicates a state conflict or unmergeable PR (HTTP 409 / merge conflict).
  - `ErrRateLimited`: Indicates a rate-limit error where backoff exceeded limits or retries exhausted.
  - `ErrUnsupportedForge`: Returned when a configured or detected forge type is not supported.
  - `ErrAmbiguousForge`: Returned when forge type cannot be determined from URL, environment, or git remote.
- Error inspection helper functions:
  - `IsNotFound(err error) bool`
  - `IsConflict(err error) bool`
  - `IsRateLimited(err error) bool`
  - `IsNoToken(err error) bool`

### Requirement 6: Shared Transport Foundation
The package shall provide internal transport helper functions used across forge implementations:
- Response bodies are read with `io.LimitReader` bounded to a maximum of 8 MB (`8 << 20` bytes) to prevent out-of-memory errors on large issue threads or diffs.
- Requests check for rate-limiting responses (429, or 403 with exhausted quota). Backoff duration is calculated via `RetryAfter()`. If the duration is less than or equal to 2 minutes, the transport waits and retries the request once. Durations exceeding 2 minutes return `ErrRateLimited` immediately without retrying.
- The transport accepts an injectable sleep function (`func(time.Duration)`) so rate-limiting backoff can be verified in unit tests without wall-clock delays.
- A 404 response on a client where `Authenticated() == false` wraps the error with an explanatory message noting that the resource may be private and require authentication.

### Requirement 7: No-Op Client Implementation
The package shall provide a complete implementation of `Client` via `NewNoOp()`:
- `Authenticated()` returns `false`.
- `Close()` returns `nil`.
- `GetRepository(ctx, repo)` returns a zero-value `Repository` and `nil`.
- Issue read operations (`ReadIssue`, `ListIssues`, `ListComments`) return zero-value results (`IssueThread{}`, `IssueList{}`, `CommentList{}`) and `nil`.
- Issue write operations (`CreateIssue`, `UpdateIssue`, `CloseIssue`, `AddComment`, `AddLabels`, `RemoveLabel`, `CreateLabel`) return zero values and `nil` without performing any actions.
- All pull request operations (`CreatePullRequest`, `ReadPullRequest`, `ReadChangedFiles`, `GetPRState`, `GetCIChecks`, `GetPRReviews`, `PostReviewComment`, `MergePullRequest`, `ClosePullRequest`) return an explicit error stating that pull request operations require an active forge client.

## Design Decisions

1. **Spec split into four distinct scopes**: The complete input spans interface definition, domain modeling, two distinct REST forge implementations (GitHub and GitLab), and refactoring 11 packages across `agent-fox`. We split this into `issuex_core` (this foundational spec), `issuex_github`, `issuex_gitlab`, and `issuex_agentfox_migration` to remain within the 10-requirement and 8-task spec budget.
2. **Interface naming as `Client` with `ForgeClient` alias**: In `package issuex`, `Client` is standard Go idiom (yielding `issuex.Client`), while `type ForgeClient = Client` provides clarity and aligns with the draft terminology.
3. **Representation of nested GitLab paths in `Repo`**: `Repo` maintains `Owner` and `Name` strings where `Owner` captures all leading path segments (`group/subgroup`) and `Name` captures the project name, enabling seamless support for both GitHub (`owner/repo`) and GitLab nested groups without additional structs.
4. **Parameter structs for issue and pull request creation**: Methods use parameter structs (`CreateIssueRequest`, `CreatePullRequestRequest`, `UpdateIssueRequest`) instead of positional strings to ensure API extensibility without breaking caller contracts when adding optional fields in later scopes.
5. **Issue thread comments error isolation**: `ReadIssue` populates `IssueThread.CommentsErr` if reading issue comments fails while issue metadata retrieval succeeds, matching `internal/ghapi` behavior so triage tools can continue analyzing the primary problem description.
6. **Centralized transport safety utilities in core**: Response size bounding (8 MB) and rate-limit backoff calculations are implemented in `issuex_core` utilities rather than duplicated within GitHub and GitLab client adapters.
7. **Explicit errors for PR operations on `NoOpClient`**: While issue operations in `NoOpClient` act as silent no-ops, PR operations return explicit errors because code generation, review, and merging pipelines must fail fast rather than assume branches were merged.
8. **Pluggable forge registration dispatch**: `NewWithOptions` in `issuex_core` implements forge detection and environment resolution; when GitHub or GitLab is detected prior to their adapter specs, it returns `ErrUnsupportedForge` cleanly signaling the missing adapter.
9. **Origin remote fallback for detection**: When base URL and environment variables do not indicate a forge, the constructor checks the git origin remote in the current working directory before returning an ambiguity error, reducing required configuration in typical CLI workflows.
10. **Zero external HTTP dependencies**: Transport is built strictly on standard library `net/http` and `encoding/json` with no third-party HTTP framework dependencies, preserving compatibility with `agent-fox` ecosystem constraints.
