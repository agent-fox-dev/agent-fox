---
spec_id: '05'
spec_name: gitea_platform
title: Gitea Platform
status: draft
created_at: '2026-07-14T08:05:08.141086+00:00'
updated_at: '2026-07-14T08:47:50.570424+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Gitea Platform Implementation

## Intent

Enable agent-fox to operate against Gitea-hosted repositories by implementing `GiteaPlatform` — a concrete `PlatformProtocol` — so that teams self-hosting Gitea get the same autonomous fix-pipeline and spec-driven workflow capabilities as GitHub and GitLab users.

## Overview

Implement `GiteaPlatform`, a concrete implementation of `PlatformProtocol`
in the `afissues` package, targeting the Gitea REST API (v1). This enables
agent-fox to operate against Gitea-hosted repositories with the same issue
management, label operations, and pull-request capabilities currently
available for GitHub and GitLab.

## Goals

- All 12 protocol methods implemented and covered by unit tests with mocked HTTP responses.
- All non-protocol methods (`search_issues`, `check_credentials`), internal helpers (`_resolve_label_id`), and `parse_remote` also covered by unit tests.
- Gitea routing active and exercised in a `platform_factory` integration test.
- Platform factory correctly constructs `GiteaPlatform` when `type = "gitea"` is configured.

## Non-Goals

- Gitea webhooks and event-driven triggers.
- OAuth2 / Gitea application token authentication (only static tokens are supported).
- Gitea Actions CI integration.
- Gitea-specific PR review workflows (approvals, required reviews, protected branches).
- Support for Gitea versions older than v1.17.
- Pagination beyond a single page of results.

## Tech Stack

- Python 3.12+
- httpx (async HTTP client, declared dependency of `afissues`)
- `afissues.protocol` — `PlatformProtocol`, `IssueResult`, `IssueComment`
- `afissues.errors` — `IntegrationError`, `ConfigError`
- `afissues._ssrf` — shared SSRF guard utilities (extracted by the GitLab spec); exposes `_validate_url(url: str) -> None`, which raises `ConfigError` on disallowed URLs
- `afissues._http` — shared HTTP retry logic (extracted by the GitLab spec); interface defined by the GitLab spec — `GiteaPlatform` follows the same integration pattern as `GitLabPlatform` without deviation. Because the GitLab spec is a hard prerequisite and is merged first, the `_http` module and its interface will already exist when this spec is implemented — implementors should follow the pattern established in `gitlab.py` directly.

## Background

The `afissues` package houses the platform abstraction layer. After the
GitLab spec lands, it will contain `GitHubPlatform`, `GitLabPlatform`, and
shared SSRF/HTTP utilities. This spec adds the third implementation.

The platform factory in `agentfox/nightshift/platform_factory.py` will
already have routing for `type = "gitea"` (added by the GitLab spec) with
a guard for the not-yet-implemented class. This spec replaces that guard
with the real `GiteaPlatform` import.

### Gitea API Differences from GitHub

Gitea's API is modeled after GitHub's, so field names and URL structure are
largely similar. Key differences:

| Concept | GitHub | Gitea |
|---------|--------|-------|
| API prefix | `/api/v3` (GHE) or `api.github.com` | `/api/v1` |
| Issue list | Returns issues only | Returns issues AND PRs — must filter with `type=issues` |
| Label on create_issue | Array of name strings | Array of **numeric IDs** — requires name→ID resolution |
| Label on assign | POST with name strings | POST with numeric IDs |
| Label on remove | DELETE with label name in URL | DELETE with **numeric label ID** in URL |
| Sort params | `sort` + `direction` | Single `sort` param encoding both (e.g. `oldest`, `newest`) |
| PATCH success code | 200 | 201 (for issue update) |
| Color format | Bare hex (`12ec39`) | `#`-prefixed (`#12ec39`) |
| Duplicate PR | 422 | 409 |
| Auth header | `Authorization: Bearer {token}` | `Authorization: token {token}` |
| Label idempotency (create) | 422 "already_exists" | 422 (not natively idempotent — may create duplicates; check first) |

**Note on label name strings vs. numeric IDs:** Some newer Gitea versions may accept label name strings for assignment, but this implementation uses numeric IDs exclusively to ensure maximum compatibility across all supported versions (v1.17+). This is the lowest-common-denominator approach.

## Dependencies and Sequencing

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 03_extract_platform_afissues | 10 | 1 | **Hard prerequisite — must be fully merged first.** `afissues` package must exist with protocol, errors, and GitHub modules. |
| 04_gitlab_platform | (shared SSRF/HTTP extraction) | 1 | **Hard prerequisite — must be fully merged first.** Shared `_ssrf.py` and `_http.py` modules and the platform factory Gitea routing stub must exist before Gitea implementation begins. |

Work on `gitea_platform` must not begin until both `extract_platform_afissues` and `gitlab_platform` are fully merged and released. These are strict sequencing gates, not soft dependencies.

## Functional Requirements

### Method Signatures and Async Contract

All `GiteaPlatform` protocol and non-protocol methods are `async def`. The constructor (`__init__`) is synchronous. SSRF validation is performed synchronously in the constructor. This matches `GitHubPlatform` exactly and requires `pytest-asyncio` (or equivalent) for unit tests.

```python
class GiteaPlatform:
    forge_type: str = "gitea"

    def __init__(self, owner: str, repo: str, token: str, url: str) -> None: ...

    async def create_issue(self, title: str, body: str, labels: list[str]) -> IssueResult: ...
    async def list_issues_by_label(self, label: str, state: str = "open", sort: str = "created", direction: str = "asc") -> list[IssueResult]: ...
    async def add_issue_comment(self, issue_number: int, body: str) -> None: ...
    async def assign_label(self, issue_number: int, label: str) -> None: ...
    async def close_issue(self, issue_number: int, comment: str | None = None) -> None: ...
    async def remove_label(self, issue_number: int, label: str) -> None: ...
    async def list_issue_comments(self, issue_number: int) -> list[IssueComment]: ...
    async def get_issue(self, issue_number: int) -> IssueResult: ...
    async def update_issue(self, issue_number: int, body: str) -> None: ...
    async def create_label(self, name: str, color: str, description: str = "") -> None: ...
    async def create_pr(self, title: str, body: str, head: str, base: str) -> str: ...
    def close(self) -> None: ...

    async def search_issues(self, title_prefix: str, state: str = "open") -> list[IssueResult]: ...
    async def check_credentials(self) -> None: ...
    async def _resolve_label_id(self, label_name: str) -> int: ...
```

Default parameter values match `PlatformProtocol` exactly:
- `list_issues_by_label`: `state="open"`, `sort="created"`, `direction="asc"`
- `close_issue`: `comment=None`
- `create_label`: `description=""`

### Data Model: IssueResult and IssueComment

`IssueResult` and `IssueComment` are defined in `afissues.protocol`. Their
fields and how they map from the Gitea API response are as follows:

**`IssueResult`** fields (all Gitea response field names match GitHub's, so
mapping is direct with no transformation unless noted):

| `IssueResult` field | Gitea JSON field | Notes |
|---------------------|-----------------|-------|
| `number` | `number` | Direct mapping |
| `title` | `title` | Direct mapping |
| `html_url` | `html_url` | Direct mapping |
| `body` | `body` | Direct mapping; defaults to `''` if absent/null |
| `labels` | `labels` | Array of label objects — extract `name` from each; defaults to `()` if absent |

**`IssueComment`** fields:

| `IssueComment` field | Gitea JSON field | Notes |
|----------------------|-----------------|-------|
| `id` | `id` | Direct mapping |
| `body` | `body` | Direct mapping |
| `user` | `user.login` | Extract `login` from the nested `user` object |
| `created_at` | `created_at` | Direct mapping |

Because Gitea uses the same field names as GitHub, no field aliasing is
required beyond the `user.login` → `user` extraction for comments.

### GiteaPlatform Class

Implement `GiteaPlatform` in `packages/afissues/afissues/gitea.py`.

#### Constructor and Attributes

- **`forge_type: str = "gitea"`** — class attribute for forge identification.
- **Constructor:** `def __init__(self, owner: str, repo: str, token: str, url: str) -> None` (synchronous).
  - `url` is required (no default) — Gitea is always self-hosted.
  - API base URL: `https://{url}/api/v1`.
  - Apply SSRF validation on `url` at construction time by calling
    `afissues._ssrf._validate_url(url)`. This function raises `ConfigError`
    directly if the URL is disallowed — no return value is used.
  - SSRF validation failure raises `ConfigError` (not `IntegrationError`) —
    SSRF failure is a configuration problem. This matches `GitHubPlatform`,
    which raises `ConfigError` from `_check_address`. The `ConfigError`
    propagates directly to the caller; `GiteaPlatform` does not catch or
    re-wrap it. Constructor unit tests should assert `ConfigError` is raised
    for invalid/disallowed URLs.
  - No validation or sanitization is applied to the `token` parameter. The
    token is used as-is in the `Authorization` header.
- **Auth headers:** `{"Authorization": f"token {token}"}`.
- **HTTP client:** Use the same integration pattern as `GitLabPlatform` for
  the shared `afissues._http` retry logic. The exact `_http` module interface
  (factory function, wrapper class, or decorator) is defined by the GitLab
  spec and will already be present when this spec is implemented (GitLab spec
  is a hard prerequisite). Follow the pattern established in `gitlab.py`
  directly without deviation.

#### Label ID Resolution

Gitea requires numeric label IDs for all label operations. The implementation
must include a label name→ID cache:

- **`async def _resolve_label_id(self, label_name: str) -> int`** — internal method that:
  1. Checks an in-memory cache (`dict[str, int]`).
  2. On cache miss, calls `GET /api/v1/repos/{owner}/{repo}/labels` to fetch
     all repo labels and populate the cache.
  3. Returns the numeric ID for the given name.
  4. Raises `IntegrationError` if the label does not exist after a full fetch
     (i.e., the label is genuinely absent). A second call within the same
     session for the same missing label **does not** re-fetch from the API —
     the `IntegrationError` is raised immediately based on the cached (complete)
     label list. The cache is only repopulated by `create_label`.
- The cache is populated lazily on first use and refreshed when a new label
  is created via `create_label`.

**Cache update on `create_label`:** When a label is successfully created,
insert only the new `name → id` pair into the existing cache dict. No full
re-fetch is performed. This is efficient and sufficient — labels deleted
externally between calls are not a supported invalidation scenario within a
single session.

#### Error Handling

- All methods raise `afissues.errors.IntegrationError` on API errors, with
  response text truncated to 500 characters.
- The 500-character truncation rule applies to all error-raising paths across
  all protocol and non-protocol methods. It does **not** apply to silent/
  idempotency success paths (e.g., 404 on `remove_label`, 409 on `create_pr`,
  or label-not-found on `remove_label`) where the implementation returns
  without raising.
- `check_credentials` raises `IntegrationError` on 401 or 403 only. All other
  status codes (including 5xx) result in a normal return. This matches
  `GitHubPlatform.check_credentials()` behavior exactly.
- `close()` is a no-op and performs no HTTP calls, so error handling does
  not apply.
- SSRF validation failure raises `ConfigError` at construction time (see
  Constructor section above).

#### Protocol Methods

1. **`async def create_issue(self, title: str, body: str, labels: list[str]) -> IssueResult`** —
   `POST /api/v1/repos/{owner}/{repo}/issues`.
   - Request: `title`, `body`. If `labels` provided, resolve each name to
     numeric ID via `_resolve_label_id` and pass as `labels` array of ints.
   - Response: map to `IssueResult` using the field mapping defined in the
     Data Model section. Labels in response are label objects — extract `name`
     field from each.
   - Success: any 2xx.

2. **`async def list_issues_by_label(self, label: str, state: str = "open", sort: str = "created", direction: str = "asc") -> list[IssueResult]`** —
   `GET /api/v1/repos/{owner}/{repo}/issues`.
   - Query params: `labels` (label name — Gitea accepts names here),
     `state` (`open`/`closed`), `type=issues` (exclude PRs),
     `sort` (map `sort`+`direction` → Gitea combined value:
     `created`+`asc` → `oldest`, `created`+`desc` → `newest`,
     `updated`+`asc` → `leastupdate`, `updated`+`desc` → `recentupdate`),
     `limit=50`.
   - **Unmapped sort combinations:** If the `sort`+`direction` pair is not
     in the 4-entry mapping table above (e.g., `sort='comments'` or any other
     unrecognized value), default silently to `newest` (`created`+`desc`).
     Do not raise an error.
   - Response: map each item to `IssueResult` using the field mapping defined
     in the Data Model section (identical to `get_issue` and `search_issues`).
   - Success: 200.

3. **`async def add_issue_comment(self, issue_number: int, body: str) -> None`** —
   `POST /api/v1/repos/{owner}/{repo}/issues/{index}/comments`.
   - Request: `body`.
   - Returns `None` (fire-and-forget). The response body is not parsed.
   - Success: any 2xx.

4. **`async def assign_label(self, issue_number: int, label: str) -> None`** —
   `POST /api/v1/repos/{owner}/{repo}/issues/{index}/labels`.
   - Request: `labels` — array containing the single numeric label ID
     (resolved via `_resolve_label_id`).
   - Success: any 2xx.

5. **`async def close_issue(self, issue_number: int, comment: str | None = None) -> None`** —
   If `comment` is not `None`, first call `await add_issue_comment(issue_number, comment)`.
   If `comment` is `None`, skip the `add_issue_comment` call entirely (no HTTP call is made
   for the comment). Then `PATCH /api/v1/repos/{owner}/{repo}/issues/{index}` with
   `state=closed`.
   - Success: any 2xx.

6. **`async def remove_label(self, issue_number: int, label: str) -> None`** —
   `DELETE /api/v1/repos/{owner}/{repo}/issues/{index}/labels/{label_id}`.
   - Resolve label name to ID via `_resolve_label_id`. If `_resolve_label_id`
     raises `IntegrationError` (label does not exist in the repo), catch the
     exception and **return `None` silently** — the label cannot be on the
     issue if it doesn't exist in the repo.
   - Idempotent: treat 404 and 422 (label not on issue) as success (return
     `None` silently without raising).
   - Success: 204 (or silent success on 404/422/missing label).

7. **`async def list_issue_comments(self, issue_number: int) -> list[IssueComment]`** —
   `GET /api/v1/repos/{owner}/{repo}/issues/{index}/comments`.
   - No `limit` parameter is sent. The endpoint returns all comments for the
     issue by default; if Gitea does support optional pagination on this
     endpoint in some versions, the single-page behavior is consistent with
     the non-goal of no multi-page pagination.
   - Returns comments in chronological order by default.
   - Map response items to `IssueComment` using the field mapping defined in
     the Data Model section (`user.login` → `user`).
   - Success: 200.

8. **`async def get_issue(self, issue_number: int) -> IssueResult`** —
   `GET /api/v1/repos/{owner}/{repo}/issues/{index}`.
   - Map response fields to `IssueResult` using the field mapping defined in
     the Data Model section.
   - Success: 200.

9. **`async def update_issue(self, issue_number: int, body: str) -> None`** —
   `PATCH /api/v1/repos/{owner}/{repo}/issues/{index}` with `body` field.
   - Success: any 2xx (Gitea may return 200 or 201 for PATCH).

10. **`async def create_label(self, name: str, color: str, description: str = "") -> None`** —
    `POST /api/v1/repos/{owner}/{repo}/labels`.
    - **Idempotency check:** Call `await _resolve_label_id(name)`. If it
      succeeds (returns an ID), the label already exists — return `None`
      silently without making a POST request. If it raises `IntegrationError`
      (label not found), proceed to create.
    - Request: `name`, `color` (prepend `#` to bare hex), `description`.
    - On successful creation, insert the new `name → id` pair (from the
      returned `id` field) into the existing label ID cache dict. No full
      cache re-fetch is performed.
    - Success: any 2xx.

11. **`async def create_pr(self, title: str, body: str, head: str, base: str) -> str`** —
    `POST /api/v1/repos/{owner}/{repo}/pulls`.
    - Request: `title`, `body`, `head`, `base` (field names match GitHub).
    - Return `html_url` from response.
    - Idempotent: treat 409 (duplicate PR for same head/base) as success —
      query existing PR via `GET /pulls?head={head}&base={base}&state=open`
      and return its `html_url`.
    - If the follow-up GET returns zero results (i.e., no open PR found
      despite the 409), raise `IntegrationError` with a descriptive message
      indicating that a 409 duplicate was returned but no existing open PR
      could be found. This matches the GitLab spec behavior.
    - Success: any 2xx.

12. **`def close(self) -> None`** — No-op (synchronous). Gitea uses no persistent
    connection state, so no resource cleanup is required. This satisfies the
    `PlatformProtocol` interface contract.

#### Non-Protocol Methods

13. **`async def search_issues(self, title_prefix: str, state: str = "open") -> list[IssueResult]`** —
    `GET /api/v1/repos/{owner}/{repo}/issues` with `q` query param for
    title search, `type=issues`, `state`, `limit=50`.
    - The `title_prefix` value is passed directly as the `q` query parameter.
      The Gitea API `q` param performs a general keyword/substring search rather
      than a strict prefix match. The API result is returned as-is without
      client-side prefix filtering, consistent with `GitHubPlatform.search_issues()`
      which also trusts the search API result without client-side filtering.
    - Map response items to `IssueResult` using the field mapping defined in
      the Data Model section (identical to `get_issue` and `list_issues_by_label`).
    - Return `list[IssueResult]`.

14. **`async def check_credentials(self) -> None`** —
    `GET /api/v1/repos/{owner}/{repo}`.
    - Raise `IntegrationError` on 401 or 403 only.
    - Return normally for any other status code, including 5xx. This matches
      `GitHubPlatform.check_credentials()` behavior exactly.

### Remote URL Parsing

Implement `parse_remote(remote_url: str) -> tuple[str, str] | None` in
`afissues/gitea.py`. Extracts `(owner, repo)` from Gitea remote URLs.

Supports:
- HTTPS: `https://gitea.example.com/owner/repo.git`
- SSH: `git@gitea.example.com:owner/repo.git`

Because Gitea URLs have no distinctive hostname pattern (unlike `github.com`
or `gitlab.com`), this function accepts any hostname. The platform factory
determines which parser to call based on the configured `platform.type`.

Returns `(owner, repo)` or `None` if the URL cannot be parsed.

### Platform Factory Finalization

Update the Gitea routing in `agentfox/nightshift/platform_factory.py` to
replace the import guard (added by the GitLab spec) with the real import:

```python
from afissues.gitea import GiteaPlatform, parse_remote as parse_gitea_remote
```

No other factory changes are needed — the routing logic was already added
by the GitLab spec.

### Re-exports

`parse_remote` is **not** re-exported from `afissues/__init__.py` with an
aliased name. The platform factory imports it directly from the module:

```python
from afissues.gitea import GiteaPlatform, parse_remote as parse_gitea_remote
```

Update `afissues/__init__.py` to add `GiteaPlatform` to the existing re-exports
from `afissues.gitea`. Preserve all existing re-exports (e.g., `GitHubPlatform`,
`GitLabPlatform`) — only append the new `GiteaPlatform` entry. No top-level
alias for `parse_remote` is needed or added.

## Testing Requirements

Unit tests must cover:
- All 12 `PlatformProtocol` methods with mocked HTTP responses (using `pytest-asyncio` for async test functions).
- `search_issues` and `check_credentials` (non-protocol methods).
- `_resolve_label_id`: cache hit, cache miss (populates cache), label not found (raises `IntegrationError`), and behavior on subsequent calls for a known-missing label (no re-fetch, `IntegrationError` raised immediately).
- `parse_remote`: valid HTTPS URL, valid SSH URL, unparseable URL (returns `None`).
- Idempotency paths:
  - `create_label`: label already exists (`_resolve_label_id` returns an ID → no POST made).
  - `create_label` cache update: after successful creation, the new `name → id` pair is in the cache and a subsequent `_resolve_label_id` call does not trigger a re-fetch.
  - `remove_label`: 404 response, 422 response, and `_resolve_label_id` raising `IntegrationError` (label absent from repo) — all return `None` silently.
  - `create_pr`: 409 with matching open PR found (returns `html_url`), and 409 with no PR found (raises `IntegrationError`).
- Sort mapping fallback in `list_issues_by_label` for an unmapped `sort`+`direction` combination (silently defaults to `newest`).
- Constructor SSRF validation: assert `ConfigError` is raised when `afissues._ssrf._validate_url` raises (i.e., invalid or disallowed URL supplied).
- `add_issue_comment` returns `None`.
- `close_issue` with `comment=None`: assert that no HTTP call is made for a comment (only the PATCH state=closed call is made).
- `close_issue` with a non-None `comment`: assert that `add_issue_comment` is called before the PATCH.
- Unit test file location: follow the existing convention of the `afissues` test suite (co-located with the other platform tests in that package).
- Platform factory integration test: `type = "gitea"` constructs `GiteaPlatform` correctly. **This test lives in the `agentfox/nightshift` test suite, co-located with `platform_factory.py`** (i.e., in `agentfox/nightshift/tests/` or the equivalent test directory for that module).

## Design Decisions

1. **Package location:** `GiteaPlatform` lives in `afissues/gitea.py`,
   consistent with `github.py` and `gitlab.py`.

2. **All methods are async:** All protocol and non-protocol methods are
   `async def`. The constructor is synchronous. SSRF validation is
   synchronous. This matches `GitHubPlatform` exactly and requires
   `pytest-asyncio` for unit tests.

3. **Label ID cache:** A lazy in-memory `dict[str, int]` cache avoids
   repeated label-listing API calls. After a full fetch, a missing label does
   not trigger re-fetching on subsequent calls within the same session —
   `IntegrationError` is raised immediately. The cache is refreshed only by
   inserting a single new entry when `create_label` succeeds.

4. **Label idempotency for `create_label`:** Calls `_resolve_label_id` first
   and catches `IntegrationError` to detect "label not found" (then proceeds
   to create). If `_resolve_label_id` succeeds, the label exists and `create_label`
   returns silently. This reuses the cache infrastructure rather than
   introducing a separate existence-check code path. Unlike GitHub (422
   "already_exists") and GitLab (409 Conflict), Gitea may create duplicate
   labels, so this pre-check is necessary.

5. **`remove_label` catches `IntegrationError` from `_resolve_label_id`:**
   If `_resolve_label_id` raises `IntegrationError` because the label does
   not exist in the repo, `remove_label` catches it and returns `None`
   silently. This is the simplest approach and correctly handles the case
   where a label cannot be on an issue if it doesn't exist in the repo.

6. **Default parameter values match `PlatformProtocol`:** `state="open"`,
   `sort="created"`, `direction="asc"`, `comment=None`, `description=""`.
   These defaults are defined in the protocol signature and must be mirrored
   exactly to maintain behavioral consistency across platforms.

7. **`add_issue_comment` returns `None`:** Fire-and-forget. The response body
   is not parsed. This matches the `PlatformProtocol.add_issue_comment()`
   return type of `None`.

8. **Sort mapping:** Gitea combines sort field and direction into a single
   param. The implementation maintains a mapping dict for the translation.
   Unmapped combinations (including valid `sort` values not in the 4-entry
   table, such as `sort='comments'`) default silently to `newest`.

9. **`type=issues` filter:** Always passed on list endpoints to exclude PRs,
   since Gitea treats PRs as a subtype of issues.

10. **Generic `parse_remote` name:** Exports `parse_remote()` matching the
    convention from the GitLab spec. No aliased re-export in `__init__.py`;
    the platform factory aliases it at the import site.

11. **No default URL:** Gitea is always self-hosted, so `url` is a required
    constructor parameter with no default value.

12. **Color prefix:** `GiteaPlatform.create_label()` prepends `#` to the bare
    hex color value internally, same as GitLab. `LabelSpec` is not modified.

13. **Accept any 2xx for POST/PATCH methods:** All POST and PATCH methods
    accept any 2xx status code as success. This is the most resilient approach
    given Gitea's inconsistent status codes across versions (e.g., PATCH may
    return 200 or 201 depending on the endpoint and Gitea version).

14. **Numeric IDs only for labels:** Label name strings are not used for
    assignment or removal operations. Numeric IDs are always resolved via
    `_resolve_label_id` to ensure compatibility with Gitea v1.17+, the
    minimum supported version.

15. **No pagination:** Only a single page of results is fetched on list
    endpoints (`limit=50` where applicable). `list_issue_comments` uses no
    limit parameter; this is consistent with the non-goal of no multi-page
    pagination regardless of whether the endpoint technically supports it.
    Pagination support is deferred as an explicit non-goal.

16. **`close()` is a no-op (synchronous):** Gitea has no persistent connection state.
    The method is implemented synchronously to satisfy `PlatformProtocol` only.

17. **`create_pr` 409 with no existing PR:** If the follow-up GET after a
    409 returns zero results, `IntegrationError` is raised. This matches the
    GitLab spec behavior and avoids silently returning `None` as an `html_url`.

18. **`check_credentials` error scope:** Only 401 and 403 raise
    `IntegrationError`. All other responses (including 5xx) return normally.
    This matches `GitHubPlatform.check_credentials()` exactly.

19. **SSRF failure raises `ConfigError`:** SSRF validation is performed by
    calling `afissues._ssrf._validate_url(url)`, which raises `ConfigError`
    directly on disallowed URLs. `GiteaPlatform` does not catch or re-wrap
    this error. This is consistent with `GitHubPlatform._check_address` behavior.

20. **No token validation:** The `token` constructor parameter is used as-is
    in the `Authorization: token {token}` header. No format validation or
    sanitization is applied.

21. **HTTP client pattern follows `GitLabPlatform`:** The exact interface for
    `afissues._http` retry logic (factory function, wrapper class, or
    decorator) is defined by the GitLab spec and will already exist when this
    spec is implemented. `GiteaPlatform` uses the same integration pattern as
    `GitLabPlatform` without deviation — implementors should reference `gitlab.py`
    directly.

22. **IssueResult field mapping is direct:** Gitea uses the same JSON field
    names as GitHub (`number`, `title`, `html_url`, `body`, `labels`), so
    no aliasing is required. The only transformation is extracting `name`
    from each label object in the `labels` array, and extracting `login`
    from the nested `user` object for `IssueComment.user`.

23. **`search_issues` trusts API result without client-side filtering:**
    The `title_prefix` parameter is passed as the `q` query param directly.
    Because Gitea's `q` param is a keyword/substring search (not a prefix
    match), results may include issues where the title contains the prefix
    string somewhere other than the start. No client-side filtering is applied,
    consistent with `GitHubPlatform.search_issues()` behavior.

24. **`close_issue` skips comment call when `comment=None`:** When `comment`
    is `None`, no HTTP call is made for a comment — only the PATCH
    `state=closed` call is made. This is explicit and covered by a dedicated
    unit test case.

25. **`__init__.py` re-exports are additive:** Adding `GiteaPlatform` to
    `afissues/__init__.py` preserves all existing re-exports (`GitHubPlatform`,
    `GitLabPlatform`, etc.). Only the new `GiteaPlatform` entry is appended.

26. **Owner:** This spec is executed by the autonomous agent pipeline; no
    human owner is assigned.
