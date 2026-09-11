# Forge-Neutral Issue and Pull Request Client

## Intent

Three repositories in the agent-fox ecosystem — `hub`, `coder`, and `agent-fox`
— need to create, read, update, and close issues and pull requests on git forges
(GitHub, GitLab). Today, each repository either shells out to the `gh` CLI or
carries its own HTTP client code. In `agent-fox`, the `internal/ghapi` package is
a capable GitHub REST client, but it lives in `internal/` and is
GitHub-specific — neither `hub` nor `coder` can import it, and none of them can
talk to GitLab.

A shared, importable module (`issuex`) would give all three repositories a
single, forge-neutral interface for issue and PR operations. Platform-specific
details are handled behind the interface so that consumers write code once and
target any supported forge by changing client configuration.

## Goals

- Provide a single Go module that `hub`, `coder`, and `agent-fox` can import.
- Define a forge-neutral Go interface for issue and pull request operations.
- Ship both a GitHub and a GitLab implementation from the first release.
- Support enterprise/self-hosted instances (GitHub Enterprise, self-hosted
  GitLab) via endpoint URL configuration.
- Auto-detect the forge type (GitHub vs GitLab) from the endpoint URL, so
  callers do not specify the platform explicitly.
- Instantiate clients with explicit options: token, endpoint URL, user agent,
  and HTTP client override.
- Replace `agent-fox`'s internal `ghapi` usage with `issuex` once the module is
  stable.

## Non-goals

- Bitbucket, Azure DevOps, or other forges beyond GitHub and GitLab.
- Webhook handling or event subscription.
- Git operations (clone, push, pull, merge, rebase) — only the forge's REST API
  for issues and PRs.
- GraphQL APIs — REST only.
- OAuth flows or token acquisition — the caller supplies a token.
- User identity (name, email) in client options — the token determines identity
  for all forge operations.

## Functional Requirements

### Client Instantiation

- A client is created by calling a constructor that accepts an options struct:
  authentication token, base endpoint URL, HTTP client override, and user agent
  string.
- When the endpoint URL is empty, the client detects the forge type and endpoint
  from environment variables (`GITHUB_API_URL`, `GITHUB_TOKEN`, `GH_TOKEN` for
  GitHub; `GITLAB_API_URL`, `GITLAB_TOKEN` for GitLab).
- The client auto-detects the forge type from the endpoint URL. Known patterns:
  `api.github.com` or URLs containing `github` resolve to the GitHub
  implementation; URLs containing `gitlab` or responding to GitLab's
  `/api/v4/version` endpoint resolve to the GitLab implementation.
- When auto-detection is ambiguous, the constructor returns an error describing
  the ambiguity rather than guessing.
- The client exposes an `Authenticated() bool` method so callers can fail fast
  before expensive operations.
- The constructor returns the forge-neutral interface type, not a concrete
  struct, so the caller programs against the abstraction.

### Repository Operations

- Read repository metadata: default branch, visibility (private/public), archive
  status, and the caller's permission level (push, pull, admin).
- Detect repository owner/name from a git remote URL (supports HTTPS, SSH, and
  SCP-style remotes for both GitHub and GitLab hosts).
- Parse an "owner/repo" string from user input. For GitLab, "owner/repo" maps to
  the project's path, which may include nested groups (e.g.
  `group/subgroup/project`).

### Issue Operations

- **Create** an issue with title, body, and optional labels. Returns the issue
  number (or GitLab IID), title, body, URL, and labels.
- **Read** a single issue by number. Returns the same fields as create.
- **Update** an issue's title and/or body.
- **Close** an issue, with an optional closing comment. If a comment is provided,
  the comment is posted before the issue is closed.
- **List** issues with optional filters: label, state (`open`, `closed`, `all`),
  and owner (assignee). Filters are individually optional and combine with AND
  logic when more than one is specified. Supports sort and direction options.
  Pagination is handled internally — the client fetches pages behind the scenes
  and returns all matching results up to a configurable cap (default 100).
  When the cap is reached, a flag on the result signals that the list is
  incomplete. Returns a slice of issue results.
- **Add a comment** to an issue. Returns the comment URL.
- **List comments** on an issue in chronological order. Pagination is handled
  internally up to a configurable cap (default 500 comments — 5 pages of 100).
  When comments are truncated, a flag on the result reports this to the caller.
  Returns comment body, author login, timestamp, and URL.
- **Add labels** to an issue (additive — does not remove existing labels).
- **Remove a label** from an issue. Succeeds silently if the label is not present
  (idempotent).
- **Create a label** on the repository with name, color, and optional
  description. Succeeds silently if the label already exists (idempotent).

### Pull Request Operations

- **Create** a pull request (GitHub) / merge request (GitLab) from a head branch
  into a base branch, with title, body, and draft flag. Returns the PR number and
  URL.
- **Read** a pull request's metadata: number, title, body, state, head/base
  branches, draft status, URL.
- **Read changed files** for a pull request: filename, status
  (added/modified/deleted), additions, and deletions.
- **Get PR state**: number, state (open/closed/merged), merged flag, head SHA.
- **Get CI checks**: list of check-run results (GitHub check runs / GitLab
  pipeline jobs) with name, status, conclusion, and output summary.
- **Get PR reviews**: list of reviews with author, state
  (approved/changes_requested/commented), body, and timestamp.
- **Post a review comment** on a pull request with a body. This is a top-level
  review comment, not an inline file-level comment.
- **Merge** a pull request via the forge API. The merge method defaults to the
  repository's configured default merge strategy. The caller may override this
  with an explicit method (merge commit, squash, or rebase). Returns the merge
  commit SHA on success. Fails with a typed error if the PR is not mergeable
  (conflicts, required checks not passing, etc.).
- **Close** a pull request without merging.

### Error Handling

- All write operations return an explicit "not authenticated" error when the
  client has no token, so the caller can fail before spending resources.
- HTTP errors carry the status code as a number, the forge's error message, and
  rate-limit headers (where applicable).
- Rate-limited requests (HTTP 429, or GitHub's 403 with exhausted rate limit) are
  retried once after the server-specified backoff. Backoffs longer than 2 minutes
  are not retried.
- A 404 on a private repository when unauthenticated includes a hint that the
  resource may be private.
- Response bodies are bounded to a configurable maximum (default 8 MB) — no
  unbounded reads from the server.
- Typed error values allow callers to distinguish not-found, not-authenticated,
  rate-limited, and merge-conflict failures via `errors.Is` or `errors.As`.

### No-Op Implementation

- A null/no-op implementation of the interface is provided for contexts where no
  forge is configured.
- Issue read operations on the null implementation return zero-value results.
- Issue write operations on the null implementation are silent no-ops.
- PR operations (create, merge, reviews, checks) on the null implementation
  return errors — PR workflows require a real forge, and failing loudly is safer
  than silently succeeding.

### Interface Design

- The module defines a single Go interface for all issue and PR operations.
- The interface uses `context.Context` as the first parameter of every method.
- Return types are value structs (not pointers), keeping the data types simple
  and copyable.
- The interface includes a `Close() error` method for implementations that hold
  resources (HTTP connection pools, etc.).

## Technical Boundaries

- Go 1.22+ (matches the rest of the agent-fox ecosystem).
- Dependency-free for the HTTP transport (`net/http` only, no third-party HTTP
  libraries).
- The module lives at `issuex/` in the `agent-fox` repository, as a public
  (non-internal) package.
- JSON encoding/decoding uses `encoding/json` from the standard library.
- The module must be testable without network access — implementations accept an
  `*http.Client` override so tests can inject an `httptest` server.
- GitLab uses project IDs internally but the client accepts `owner/repo` (or
  `group/subgroup/project`) path notation transparently, resolving it to the
  numeric ID as needed.

## Dependencies

- Go standard library (`net/http`, `encoding/json`, `context`, `fmt`, `errors`,
  `time`, `strconv`, `strings`, `net/url`).
- No external dependencies.
- Consumed by: `agent-fox` (replacing `internal/ghapi`), `hub`, `coder`.

## Design Decisions

- **Single interface**: The module uses one flat `ForgeClient` interface rather
  than smaller composable interfaces. All three consumers (`agent-fox`, `hub`,
  `coder`) need both issue and PR operations, making composition unnecessary
  overhead. A single interface is simpler to mock in tests and keeps the
  constructor signature clean.
- **Internal pagination**: List methods (issues, comments) handle pagination
  internally and return all results up to a cap. Callers do not deal with
  cursors or page tokens. This matches the existing `ghapi` pattern and keeps
  the interface simple — the cap prevents runaway memory use.
- **Comment truncation at 500**: The default cap of 500 comments (5 pages × 100)
  carries over from `ghapi`. This covers the vast majority of issues; the few
  that exceed it are signaled via a truncation flag so the caller can decide
  what to do.
- **Merge method defaults to repo setting**: When merging a PR, the client
  queries the repository's configured default merge strategy and uses it unless
  the caller explicitly overrides. This respects per-repo conventions and avoids
  forcing every caller to know the "right" merge method.
- **Issue list filters combine with AND**: Label, state, and owner filters are
  individually optional. When multiple are specified, they narrow the result set
  (AND logic), matching how both the GitHub and GitLab APIs work natively.
