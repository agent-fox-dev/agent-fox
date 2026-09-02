---
spec_id: '04'
spec_name: gitlab_platform
title: Gitlab Platform
status: draft
created_at: '2026-07-14T08:05:07.823707+00:00'
updated_at: '2026-07-14T08:15:01.132479+00:00'
owner: ''
source: interactive
schema_version: 1
---
# GitLab PlatformProtocol Implementation

## Intent

Enable agent-fox to manage issues, labels, and merge requests on GitLab-hosted repositories through the existing `PlatformProtocol` abstraction, so that teams using GitLab as their forge get the same autonomous fix-pipeline and spec-driven workflow capabilities currently available only to GitHub users.

## Overview

Implement `GitLabPlatform`, a concrete implementation of `PlatformProtocol`
in the `afissues` package, targeting the GitLab REST API (v4). This enables
agent-fox to operate against GitLab-hosted repositories with the same issue
management, label operations, and merge-request capabilities currently
available for GitHub.

This spec also owns all updates to the platform factory (`platform_factory.py`
in `agentfox.nightshift`), including routing for `type = "gitlab"` and a
stub for `type = "gitea"`. The `gitea_platform` spec delivers only the
`GiteaPlatform` class and replaces the stub import — it does not modify the
factory further.

## Goals

1. All 12 `PlatformProtocol` methods implemented in `GitLabPlatform` and
   covered by unit tests (branch coverage target: ≥90%).
2. Platform factory supports `"gitlab"` and `"gitea"` type values without
   breaking existing GitHub configuration — no changes required to calling code.
3. agent-fox nightshift workflows (issue creation, labelling, closing, MR
   opening) operate correctly against a real GitLab.com project with no
   GitHub-specific logic required by callers.
4. SSRF guard and HTTP retry utilities extracted into shared internal modules
   (`_ssrf.py`, `_http.py`) reused by both `GitHubPlatform` and
   `GitLabPlatform`.

## Non-Goals

- **GitLab GraphQL API** — REST API v4 only; no GraphQL client or queries.
- **OAuth or token rotation** — only static `PRIVATE-TOKEN` (Personal Access
  Token / project access token) is supported; no OAuth flows, token refresh,
  or rotation logic.
- **Group-level or instance-level operations** — only project-scoped issue,
  label, and MR operations; no group issues, group labels, or admin API calls.
- **GitLab CI/CD pipeline triggering** — no pipeline APIs.
- **Self-hosted GitLab with custom TLS certificates** — custom CA bundles and
  certificate pinning are out of scope; the SSRF guard transport uses the
  system trust store.
- **GitLab Epics or Milestones** — not part of `PlatformProtocol`.
- **Pagination beyond the first 100 results** — all list endpoints cap at
  `per_page=100`; cursor/keyset pagination is not implemented.
- **HTTP 429 rate-limit retry** — 429 responses are treated as other 4xx errors
  (raise `IntegrationError` immediately). Rate-limit handling is deferred to a
  future spec.
- **Integration smoke testing against a live GitLab instance** — acceptance is
  verified by unit tests with mocked HTTP responses only; live integration
  testing is deferred to a future CI hardening spec.

## Tech Stack

- Python 3.12+
- httpx (async HTTP client, declared dependency of `afissues`)
- `afissues.protocol` — `PlatformProtocol`, `IssueResult`, `IssueComment`
- `afissues.errors` — `IntegrationError`, `ConfigError`
- SSRF guard utilities from `afissues.github` (to be extracted into a shared
  internal module `afissues/_ssrf.py`)

## Background

The `afissues` package (created by spec `03_extract_platform_afissues`) houses
the platform abstraction layer. It currently contains `GitHubPlatform` as the
sole concrete implementation. The `PlatformProtocol` defines 12 abstract
methods plus `close()`. The platform factory in
`agentfox/nightshift/platform_factory.py` currently hardcodes GitHub as the
only supported platform type.

### GitLab API Differences from GitHub

GitLab uses different terminology, field names, and API patterns:

| Concept | GitHub | GitLab |
|---------|--------|--------|
| Pull request | `pulls` | `merge_requests` |
| Comments | `comments` | `notes` |
| Issue body | `body` | `description` |
| Issue URL | `html_url` | `web_url` |
| Issue number | `number` | `iid` (project-internal) |
| Issue state "open" | `open` | `opened` |
| State change | `{"state": "closed"}` | `{"state_event": "close"}` |
| Sort field | `sort` param | `order_by` param |
| Sort direction | `direction` param | `sort` param |
| Label input | JSON array of strings | Comma-separated string |
| Label add/remove | Dedicated endpoints | `add_labels`/`remove_labels` on issue update |
| Color format | Bare hex (`12ec39`) | `#`-prefixed (`#12ec39`) |
| Auth header | `Authorization: Bearer {token}` | `PRIVATE-TOKEN: {token}` |
| Project ID | `{owner}/{repo}` in URL | Numeric ID or URL-encoded path |

## Data Model Field Mappings

### IssueResult

`IssueResult` is defined in `afissues.protocol` and is the authoritative source
of truth for required fields. The following GitLab API response fields map to
each `IssueResult` field. All other GitLab response fields are ignored.

| `IssueResult` field | Type | GitLab API field | Notes |
|---|---|---|---|
| `number` | `int` | `iid` | Project-internal issue ID |
| `title` | `str` | `title` | Direct mapping |
| `html_url` | `str` | `web_url` | Public URL of the issue |
| `body` | `str` (default `""`) | `description` | May be `null` in GitLab — default to `""` |
| `labels` | `tuple[str, ...]` (default `()`) | `labels` | GitLab returns a JSON array of strings; convert to tuple |

### IssueComment

`IssueComment` is defined in `afissues.protocol` and is the authoritative source
of truth for required fields. The following GitLab API response fields map to
each `IssueComment` field. All other GitLab response fields are ignored.

| `IssueComment` field | Type | GitLab API field | Notes |
|---|---|---|---|
| `id` | `int` | `id` | Note ID (not `iid`) |
| `body` | `str` | `body` | Direct mapping |
| `user` | `str` | `author.username` | GitLab note author's username |
| `created_at` | `str` | `created_at` | ISO 8601 string; passed through as-is |

## Functional Requirements

### GitLabPlatform Class

Implement `GitLabPlatform` in `packages/afissues/afissues/gitlab.py`.

`GitLabPlatform` is re-exported from `afissues/__init__.py` alongside
`GitHubPlatform`, following the same pattern. It is therefore part of the
package's public API surface and importable as `from afissues import GitLabPlatform`.

The module docstring must document the minimum required GitLab token scope:
**`api`** (full read/write API access). Tokens with only `read_api` will
receive 403 errors on write operations. Users should create a Personal Access
Token or project access token with the `api` scope.

#### Constructor and Attributes

- **`forge_type: str = "gitlab"`** — class attribute for forge identification.
- **Constructor:** `__init__(self, project_id: str, token: str, url: str = "gitlab.com")`.
  - `project_id` accepts the **raw, unencoded** path (e.g. `"group/subgroup/project"`).
    The constructor URL-encodes it internally when building API URLs (e.g.
    `"group%2Fsubgroup%2Fproject"`). This centralises encoding logic and keeps
    callers (including the factory) simple.
  - API base URL: `https://{url}/api/v4` (same pattern for gitlab.com and self-hosted).
  - Apply SSRF validation on `url` at construction time (reject private/loopback/link-local IPs).
  - **SSRF violation exception:** Raises `ConfigError` (matching `GitHubPlatform`'s
    behaviour from the shared `_check_address` function in `_ssrf.py`). This is
    semantically appropriate — a disallowed URL is a configuration mistake, not
    an API-level failure.
- **Auth headers:** `{"PRIVATE-TOKEN": token}`.
- **HTTP client:** Use `httpx.AsyncClient` with the same SSRF guard transport,
  timeout config, and retry logic as `GitHubPlatform`. A new client is created
  per request inside `request_with_retry` using `async with httpx.AsyncClient(...)`,
  consistent with `GitHubPlatform`'s `_request()` method.

#### Protocol Methods

All methods raise `afissues.errors.IntegrationError` on API errors (including
HTTP 429), with response text truncated to 500 characters.

1. **`create_issue(title, body, labels)`** —
   `POST /api/v4/projects/{project_id}/issues`.
   - Request: `title`, `description` (mapped from `body`), `labels` (comma-separated string from list).
   - Response: map `iid` → `number`, `web_url` → `html_url`, `description` → `body` (default `""` if null), `labels` (array) → `labels` (tuple), `title` → `title`.
   - Success: 201.

2. **`list_issues_by_label(label, state, sort, direction)`** —
   `GET /api/v4/projects/{project_id}/issues`.
   - Query params: `labels` (string), `state` (mapped: `"open"` → `"opened"`;
     `"closed"` → `"closed"`; `"all"` → `"all"` — passed through unchanged),
     `order_by` (mapped from `sort`: `"created"` → `"created_at"`, `"updated"` → `"updated_at"`),
     `sort` (mapped from `direction`: `"asc"` → `"asc"`, `"desc"` → `"desc"` — passed through directly),
     `per_page=100`.
   - All three `state` values (`"open"`, `"closed"`, `"all"`) are valid protocol
     inputs. Only `"open"` requires translation to `"opened"`; `"closed"` and
     `"all"` are passed through to GitLab unchanged.
   - The protocol accepts exactly two `sort` values: `"created"` and `"updated"`.
     The `direction` values `"asc"` and `"desc"` are passed through unchanged
     as GitLab's `sort` parameter.
   - Do not include merge requests (GitLab issues endpoint returns only issues by default).
   - Success: 200.

3. **`add_issue_comment(issue_number, body)`** —
   `POST /api/v4/projects/{project_id}/issues/{iid}/notes`.
   - Request: `body`.
   - Success: 201.

4. **`assign_label(issue_number, label)`** —
   `PUT /api/v4/projects/{project_id}/issues/{iid}`.
   - Request: `add_labels` (comma-separated string, single label).
   - Success: 200.

5. **`close_issue(issue_number, comment)`** —
   If comment provided, first call `add_issue_comment`. Then
   `PUT /api/v4/projects/{project_id}/issues/{iid}` with `state_event=close`.
   - Success: 200.

6. **`remove_label(issue_number, label)`** —
   `PUT /api/v4/projects/{project_id}/issues/{iid}`.
   - Request: `remove_labels` (comma-separated string, single label).
   - Idempotent: GitLab silently ignores labels not present on the issue.
   - Success: 200.

7. **`list_issue_comments(issue_number)`** —
   `GET /api/v4/projects/{project_id}/issues/{iid}/notes`.
   - Query params: `sort=asc`, `order_by=created_at`, `per_page=100`.
   - Always filter client-side: exclude notes where `system == true`. The
     `activity_filter` query parameter is **not** sent — client-side filtering
     on `system: false` is universally compatible across all GitLab API versions
     and avoids runtime feature detection.
   - Map response fields to `IssueComment` as per the field mapping table above.
   - Success: 200.

8. **`get_issue(issue_number)`** —
   `GET /api/v4/projects/{project_id}/issues/{iid}`.
   - Map response fields to `IssueResult` as per the field mapping table above.
   - Success: 200.

9. **`update_issue(issue_number, body)`** —
   `PUT /api/v4/projects/{project_id}/issues/{iid}`.
   - Request: `description` (mapped from `body`).
   - Success: 200.

10. **`create_label(name, color, description)`** — `-> None`
    `POST /api/v4/projects/{project_id}/labels`.
    - Request: `name`, `color` (prepend `#` to bare hex), `description`.
    - Returns `None` always, including on 409 Conflict (label already exists).
    - Idempotent: treat 409 Conflict as success (no error raised).
    - This matches `GitHubPlatform.create_label()`, which also returns `None`.
    - Success: 201 or 409.

11. **`create_pr(title, body, head, base)`** —
    `POST /api/v4/projects/{project_id}/merge_requests`.
    - Request: `title`, `description` (mapped from `body`),
      `source_branch` (mapped from `head`), `target_branch` (mapped from `base`).
    - Return `web_url` from response.
    - Idempotent: on 409 Conflict (duplicate MR for same source/target), query
      the existing MR via:
      ```
      GET /api/v4/projects/{project_id}/merge_requests
          ?source_branch={head}&target_branch={base}&state=opened
      ```
      Return the `web_url` of the first result.
    - If the fallback GET itself returns a non-200 HTTP status, raise
      `IntegrationError` immediately. The error message must reference the
      original 409 context (e.g. `"409 duplicate MR returned; fallback GET
      failed with {status}"`).
    - If the fallback GET returns 200 but an empty list (no open MR found for
      the given source/target), raise `IntegrationError` with a descriptive
      message indicating that a 409 duplicate was returned but no existing open
      MR could be found. This matches the behavior of `GitHubPlatform.create_pr()`.
    - Success: 201 (or 409 handled as above).

12. **`close()`** — No-op; returns `None`. `GitLabPlatform` does not hold a
    persistent `httpx.AsyncClient` instance across calls — each request creates
    and closes its own client via `async with httpx.AsyncClient(...)` inside
    `request_with_retry`, consistent with `GitHubPlatform`'s `_request()`
    method. There is therefore nothing to flush or drain.

#### Non-Protocol Methods

13. **`search_issues(title_prefix, state) -> list[IssueResult]`** —
    `GET /api/v4/projects/{project_id}/issues` with `search` query param.
    - The `search` param is passed as the value of `title_prefix`. GitLab
      performs **full-text / substring matching** (not strict prefix matching)
      against issue titles and descriptions. The method does **not** apply
      additional client-side prefix filtering — callers should be aware that
      results may include issues whose titles do not strictly begin with
      `title_prefix`.
    - Map `state="open"` → `"opened"`.
    - Returns `list[IssueResult]`.
    - Raises `IntegrationError` on API errors (4xx, 5xx), consistent with all
      other methods.

14. **`check_credentials()`** —
    `GET /api/v4/projects/{project_id}`.
    - Raise `IntegrationError` on 401 or 403 only.
    - Return normally on all other statuses, including 404 (project not found)
      and 5xx server errors. This matches the existing `GitHubPlatform.check_credentials()`
      behavior exactly and avoids masking unrelated HTTP failures as
      credential errors.

### Remote URL Parsing

Implement `parse_remote(remote_url: str) -> tuple[str, str] | None` in
`afissues/gitlab.py`. Extracts `(namespace_path, project_name)` from GitLab
remote URLs.

Supports:
- HTTPS: `https://gitlab.com/group/subgroup/project.git`
- SSH: `git@gitlab.com:group/subgroup/project.git`

Returns `None` for any URL that does not match the expected patterns, including:
- Non-GitLab URLs (different hostname)
- Malformed or ambiguous URLs (e.g. HTTPS with no project path, URLs with port
  numbers, SSH URLs with non-standard hostnames)
- Any other input that does not conform to the supported formats above

This matches the contract of `parse_github_remote`, which returns `None` for
non-GitHub or unrecognisable URLs. Implementers must not raise exceptions for
unrecognised inputs — always return `None`.

The existing `parse_github_remote` in `afissues/github.py` is renamed to
`parse_remote` for consistency. All call sites within the monorepo are
updated in the same PR. The `parse_github_remote` alias is removed once all
internal call sites are updated — there are no external consumers of this
function (internal monorepo only), so no deprecation window is required.
The old name `parse_github_remote` will also remain in `afissues/__init__.py`
re-exports only during the transitional period of the same PR, and will be
removed as part of that PR's cleanup once all references are updated. Removal
of the transitional `parse_github_remote` re-export is covered by an acceptance
criterion in this spec.

### SSRF Protection

Extract the SSRF guard utilities from `afissues/github.py` into a shared
internal module `afissues/_ssrf.py`:
- `_validate_url(url: str) -> None`
- `_validate_transport_address(host: str) -> None`
- `_check_address(addr, url: str) -> None` — raises `ConfigError` on SSRF violations
- `SSRFGuardTransport(httpx.AsyncHTTPTransport)`

Both `GitHubPlatform` and `GitLabPlatform` import from `afissues/_ssrf`.
The underscore prefix marks this module as internal (not re-exported).

SSRF violations always raise `ConfigError` (not `IntegrationError`), since a
disallowed URL represents a configuration mistake rather than an API-level
failure. This is consistent with the existing `GitHubPlatform` behavior from
the shared `_check_address` function.

### HTTP Request Helper

Extract the retry-with-backoff `_request` method pattern into a shared
internal module `afissues/_http.py`. Both platforms call this standalone async
function directly — no base class or mixin is introduced. **Do not** modify
`GitHubPlatform`'s internal `_request` method signature beyond what is needed
to delegate to `request_with_retry`.

**Function signature:**

```python
# afissues/_http.py
async def request_with_retry(
    method: str,
    url: str,
    *,
    timeout: httpx.Timeout,
    transport: httpx.AsyncHTTPTransport | None = None,
    max_retries: int = 3,
    backoff_base: float = 1.0,
    **kwargs,
) -> httpx.Response:
    ...
```

The `**kwargs` are intentionally open-ended to fully match `httpx.AsyncClient`
method signatures. The function delegates to `getattr(client, method)(url, **kwargs)`
internally, so any keyword argument accepted by `httpx.AsyncClient.request()`
(e.g. `json`, `params`, `headers`, `content`) may be passed through. No
enumeration of permitted kwargs is imposed by this spec.

**Retry semantics (shared by both platforms):**
- Retry on `httpx.ConnectTimeout`, `httpx.ConnectError`, `httpx.ReadTimeout`.
- Maximum 3 attempts with exponential backoff (`backoff_base` seconds base,
  doubling each attempt: 1 s, 2 s, 4 s).
- HTTP-level errors (4xx, 5xx, including 429) are **not** retried — returned
  to the caller as-is for the platform method to handle.
- `transport` is passed to a freshly constructed `httpx.AsyncClient` for each
  call (via `async with httpx.AsyncClient(...)`), ensuring the SSRF guard
  transport is applied.

### Platform Factory Update

Update `agentfox/nightshift/platform_factory.py`. This spec owns all factory
changes. The `gitea_platform` spec will only replace the Gitea stub import
with the real `GiteaPlatform` class — it will not otherwise modify the factory.

#### Gitea Module Import Strategy

The factory uses **lazy imports** for `afissues.gitea` inside the Gitea branch
only (not at module level). This is wrapped in a `try/except ImportError` that
raises a clear `ConfigError` if `afissues/gitea.py` does not yet exist. This
approach ensures:
- The factory module loads successfully for all other platform types even
  when `afissues/gitea.py` is absent.
- `afissues/gitea.py` (and its `parse_remote`) is introduced by the
  `gitea_platform` spec, not by this spec. The factory only needs to handle
  the import failure gracefully.

Example pattern for the Gitea branch:
```python
try:
    from afissues.gitea import GiteaPlatform, parse_remote as gitea_parse_remote
except ImportError:
    raise ConfigError(
        "The Gitea platform is not yet available. "
        "Install the afissues package with Gitea support."
    )
```

#### Configuration Schema

The factory reads from the platform config object (typically loaded from the
agent-fox config file). The relevant fields are:

| Config field | Type | Required | Description |
|---|---|---|---|
| `platform.type` | `str` | Yes | One of `"github"`, `"gitlab"`, `"gitea"` |
| `platform.url` | `str` | No (GitLab default: `"gitlab.com"`) | Forge host URL (required for Gitea — no default) |
| `platform.project_id` | `str` | No | Explicit raw project path; used as fallback if `parse_remote` returns `None` |
| `platform.owner` | `str` | No | Explicit owner; used as Gitea fallback if `parse_remote` returns `None` |
| `platform.repo` | `str` | No | Explicit repo; used as Gitea/GitHub fallback if `parse_remote` returns `None` |

Environment variables:

| Variable | Platform | Description |
|---|---|---|
| `GITHUB_TOKEN` | GitHub | Personal/installation access token |
| `GITLAB_TOKEN` | GitLab | Personal or project access token (`api` scope required) |
| `GITEA_TOKEN` | Gitea | Personal access token |

#### Factory Routing

1. Add `"gitlab"` and `"gitea"` to `_SUPPORTED_PLATFORMS`.
2. Route `platform_type == "gitlab"` to `GitLabPlatform`:
   - Token from `GITLAB_TOKEN` environment variable.
   - Resolve project identifier: call `afissues.gitlab.parse_remote` on the
     git remote URL; if it returns `(namespace, project)`, combine as
     `"namespace/project"` (raw, unencoded — the constructor handles encoding).
     If `parse_remote` returns `None`, fall back to `platform.project_id` from
     config. If neither is available, raise `ConfigError`.
   - URL from `platform.url` config (default `"gitlab.com"`).
3. Route `platform_type == "gitea"` to `GiteaPlatform` (stub):
   - Use lazy import inside the branch, wrapped in `try/except ImportError`
     raising `ConfigError` if `afissues.gitea` is not yet available (see
     "Gitea Module Import Strategy" above).
   - Token from `GITEA_TOKEN` environment variable.
   - Owner/repo from git remote URL using `afissues.gitea.parse_remote`; fall
     back to `platform.owner` / `platform.repo` from config if `parse_remote`
     returns `None`; raise `ConfigError` if neither is available.
   - URL from `platform.url` config (required — no default, since Gitea is
     always self-hosted). Raise `ConfigError` if not provided.
4. Update both `create_platform()` and `create_platform_safe()`.
5. Change return type annotations from `GitHubPlatform` to
   `PlatformProtocol` (or a union type compatible with all implementations).

## Test Requirements

Unit tests for `GitLabPlatform` live in
`packages/afissues/tests/unit/test_gitlab.py` (or split across
`test_gitlab_*.py` files by concern if needed). Tests follow the same
conventions as the existing GitHub platform tests:

- **Framework:** pytest
- **Mocking strategy:** `unittest.mock` patching of `httpx` responses,
  consistent with the existing `GitHubPlatform` test suite. Do not introduce
  new HTTP mocking libraries (e.g., `respx`, `pytest-httpx`) unless they are
  already used elsewhere in the `afissues` test suite.
- **Coverage target:** ≥90% branch coverage for `afissues/gitlab.py`.
- Tests must not make live HTTP calls to GitLab.com or any external service.
- **`GitHubPlatform` regression:** The existing `GitHubPlatform` unit tests
  must continue to pass unchanged after the refactor that delegates
  `GitHubPlatform._request()` to `request_with_retry`. No new tests are
  required for `GitHubPlatform` itself — preservation of the existing suite
  is the acceptance signal.

## Acceptance Criteria

- [ ] `GitLabPlatform` implements all 12 `PlatformProtocol` methods plus
  `search_issues` and `check_credentials`.
- [ ] Unit tests achieve ≥90% branch coverage for `afissues/gitlab.py`, using
  mocked HTTP responses (pytest + `unittest.mock`); tests live in
  `packages/afissues/tests/unit/test_gitlab.py` (or `test_gitlab_*.py`).
- [ ] All existing `GitHubPlatform` unit tests pass unchanged after the
  `_request` → `request_with_retry` delegation refactor.
- [ ] Platform factory routes `"gitlab"` to `GitLabPlatform` and `"gitea"` to
  its lazy-import guard stub; existing `"github"` routing is unchanged and all
  existing tests pass.
- [ ] SSRF guard (`_ssrf.py`) and HTTP retry (`_http.py`) are extracted and
  imported by both `GitHubPlatform` and `GitLabPlatform`; no duplication of
  that logic remains.
- [ ] `parse_github_remote` is renamed to `parse_remote` in `afissues/github.py`,
  all monorepo call sites are updated in the same PR, and the transitional
  `parse_github_remote` re-export is removed from `afissues/__init__.py` in
  the same PR.
- [ ] `parse_remote` in `afissues/gitlab.py` returns `None` for any URL that
  does not match the expected HTTPS or SSH patterns; it never raises an
  exception for unrecognised inputs.
- [ ] Module docstring for `afissues/gitlab.py` documents the required `api`
  token scope.
- [ ] `list_issue_comments` sends `per_page=100` and filters system notes
  client-side (`system == true`); `activity_filter` is not sent to the API.
- [ ] `list_issues_by_label` maps `state="open"` → `"opened"` and passes
  `"closed"` and `"all"` through to GitLab unchanged.
- [ ] `list_issues_by_label` maps `sort` values `"created"` → `"created_at"`
  and `"updated"` → `"updated_at"`, and passes `direction` values `"asc"`/`"desc"`
  through directly as GitLab's `sort` parameter.
- [ ] `create_pr` correctly handles 409 by querying the existing MR via
  `GET /merge_requests?source_branch=…&target_branch=…&state=opened`; raises
  `IntegrationError` immediately if the fallback GET returns a non-200 status
  (referencing the original 409 in the message); raises `IntegrationError` if
  the fallback GET returns 200 but an empty list.
- [ ] SSRF violations in the `GitLabPlatform` constructor raise `ConfigError`
  (not `IntegrationError`), consistent with `GitHubPlatform`.
- [ ] Platform factory raises `ConfigError` with a descriptive message when
  neither git remote parsing nor explicit config fields supply a required
  project identifier.
- [ ] Platform factory raises `ConfigError` with a clear message when the Gitea
  platform is requested but `afissues.gitea` cannot be imported; the factory
  module itself loads without error for all other platform types.
- [ ] `IssueResult` and `IssueComment` objects are constructed using only the
  fields listed in the field mapping tables; no extra GitLab-specific fields
  are leaked into the protocol layer.
- [ ] `GitLabPlatform` is re-exported from `afissues/__init__.py` alongside
  `GitHubPlatform`.
- [ ] `create_label` returns `None` always (including on 409 Conflict); method
  is typed `-> None`.
- [ ] `check_credentials` raises `IntegrationError` only on 401/403; all other
  statuses (including 404 and 5xx) return normally.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 03_extract_platform_afissues | 10 | 1 | `afissues` package must exist with protocol, errors, and GitHub modules before GitLab implementation begins. This spec cannot proceed until spec 03 is merged. |
| gitea_platform | — | — | This spec adds the Gitea factory stub; `gitea_platform` replaces the stub with the real class. `gitea_platform` must not modify the factory independently. |

## Design Decisions

1. **Package location:** `GitLabPlatform` lives in `afissues/gitlab.py`, not in
   `agentfox`. This follows the architecture established by spec 03.

2. **Public API surface:** `GitLabPlatform` is re-exported from
   `afissues/__init__.py` alongside `GitHubPlatform`, making it part of the
   package's public interface. The internal modules `_ssrf.py` and `_http.py`
   are not re-exported.

3. **Shared SSRF guard:** The SSRF utilities are extracted from `github.py` into
   `_ssrf.py` to avoid duplicating security-critical code. The underscore prefix
   signals internal-only. SSRF violations raise `ConfigError` (not
   `IntegrationError`), since an invalid URL is a configuration mistake.

4. **Shared HTTP retry:** The retry logic is extracted to `_http.py` as a
   standalone async function (`request_with_retry`). Both platforms call it
   directly — no base class or mixin is introduced. This minimises changes to
   `GitHubPlatform`'s internals. The `**kwargs` are open-ended, delegating
   directly to `httpx.AsyncClient` method signatures.

5. **Generic `parse_remote` name:** All platform modules export `parse_remote()`
   for consistency. `parse_github_remote` is removed in the same PR as the
   rename, since there are no external consumers.

6. **Factory ownership:** `gitlab_platform` owns all factory changes (including
   the Gitea stub); `gitea_platform` only delivers the `GiteaPlatform` class and
   replaces the stub import. This ensures the factory file is touched only once
   across both specs, avoiding merge conflicts.

7. **Color prefix:** `GitLabPlatform.create_label()` prepends `#` to the bare
   hex color value internally. `LabelSpec` is not modified.

8. **Project identification and URL-encoding:** `parse_remote` returns the raw
   path (e.g. `"group/subgroup/project"`). The factory passes this raw path to
   the `GitLabPlatform` constructor unchanged. The constructor URL-encodes it
   internally when building API URLs (e.g. `"group%2Fsubgroup%2Fproject"`).
   This keeps the factory simple and centralises encoding logic in one place.

9. **System notes filtering:** `list_issue_comments` always filters client-side
   by `system == false` and applies `per_page=100` consistent with all other
   list endpoints. The `activity_filter` API parameter is never sent, for
   universal compatibility across GitLab API versions without runtime feature
   detection.

10. **Token scope:** The minimum required GitLab token scope is `api`. This is
    documented in the module docstring. The spec does not support OAuth or token
    rotation — only static personal/project access tokens.

11. **Pagination cap:** All list endpoints (including the notes endpoint) use
    `per_page=100` and do not implement cursor or keyset pagination. This
    matches the existing `GitHubPlatform` behavior and is an explicit non-goal.

12. **Rate limiting (429):** HTTP 429 responses are treated identically to
    other 4xx errors — `IntegrationError` is raised immediately, no retry.
    Rate-limit handling is deferred to a future spec.

13. **`close()` is a no-op:** `GitLabPlatform` does not maintain a persistent
    `httpx.AsyncClient` across calls. Each `request_with_retry` invocation
    creates and closes its own client via `async with httpx.AsyncClient(...)`,
    consistent with `GitHubPlatform`'s `_request()` method. There is therefore
    nothing to clean up in `close()`.

14. **`search_issues` title matching:** GitLab's `search` query parameter
    performs full-text/substring matching, not strict prefix matching. The
    method passes `title_prefix` directly as the `search` value without
    additional client-side filtering. Callers should be aware that results may
    not be strictly prefix-matched. `IntegrationError` is raised on API errors,
    consistent with all other methods.

15. **Factory fallback for missing remote:** If `parse_remote` returns `None`,
    the factory falls back to an explicit `project_id` (GitLab) or
    `owner`/`repo` (Gitea/GitHub) field in the platform config. If neither is
    available, `ConfigError` is raised with a descriptive message. This mirrors
    the existing GitHub factory fallback behaviour.

16. **`sort`/`direction` mapping for `list_issues_by_label`:** The protocol
    accepts exactly `"created"` and `"updated"` as `sort` values, mapping to
    GitLab's `order_by` values `"created_at"` and `"updated_at"` respectively.
    The `direction` values `"asc"` and `"desc"` are passed through unchanged as
    GitLab's `sort` parameter.

17. **`check_credentials` error handling:** Only HTTP 401 and 403 raise
    `IntegrationError`. All other statuses (including 404, 500, etc.) return
    normally. This matches `GitHubPlatform.check_credentials()` exactly and
    avoids conflating authentication failures with unrelated HTTP errors.

18. **`create_label` return type:** `create_label` is typed `-> None` and
    returns `None` always, including when a 409 Conflict is received. This
    matches `GitHubPlatform.create_label()`.

19. **`create_pr` empty-list fallback and error handling:** If the 409 fallback
    GET returns a non-200 status, `IntegrationError` is raised immediately with
    a message referencing the original 409 context. If the fallback GET returns
    200 but an empty list, `IntegrationError` is raised with a descriptive
    message. This matches `GitHubPlatform.create_pr()` behavior.

20. **Test infrastructure:** Tests use pytest + `unittest.mock` (patching
    `httpx` responses), following the existing `afissues` test suite
    conventions. No new mocking libraries are introduced.

21. **`parse_remote` edge-case contract:** `parse_remote` returns `None` for
    any URL that does not match the expected HTTPS or SSH patterns. It never
    raises an exception. This includes malformed URLs, URLs with port numbers,
    HTTPS URLs with no project path, and SSH URLs with non-standard hostnames.
    This matches the contract of `parse_github_remote`.

22. **`list_issues_by_label` state mapping:** All three protocol state values
    are valid. `"open"` maps to GitLab's `"opened"`. `"closed"` and `"all"`
    are passed through to GitLab unchanged, as GitLab's API accepts these
    values directly.

23. **Factory Gitea lazy import:** The factory avoids importing `afissues.gitea`
    at module level. The import is deferred to inside the Gitea branch and
    wrapped in `try/except ImportError`. This ensures the factory module loads
    successfully for GitHub and GitLab even when `afissues/gitea.py` does not
    yet exist. `afissues/gitea.py` is introduced by the `gitea_platform` spec,
    not by this spec.

24. **`GitHubPlatform` refactor regression safety:** The existing
    `GitHubPlatform` unit test suite is the acceptance signal for the
    `_request` → `request_with_retry` delegation refactor. No behavioural
    changes to `GitHubPlatform` are intended or permitted beyond the internal
    delegation.
