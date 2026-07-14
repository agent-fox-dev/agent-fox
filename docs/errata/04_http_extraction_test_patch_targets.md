# Errata: HTTP Extraction Test Patch Target Changes (Spec 04)

**Spec:** 04 (`gitlab_platform`)
**Requirement:** 04-REQ-19.3
**Date:** 2026-07-14

## Divergence

04-REQ-19.3 states: "all existing `GitHubPlatform` unit tests continue to
pass **unchanged**."

This is contradicted by the implementation requirement that
`GitHubPlatform._request` delegates to `request_with_retry` in
`agentfox.platform._http`.  Once the retry logic moves to `_http.py`,
the `httpx.AsyncClient` and `asyncio.sleep` calls execute in the `_http`
module namespace, not `github`.  Existing tests that patched at
`agentfox.platform.github.httpx.AsyncClient` no longer intercept the
actual client creation.

## Resolution

Updated the mock patch target in five existing test files:

| File | Old `_TARGET` | New `_TARGET` |
|------|--------------|--------------|
| `test_github_retry.py` | `agentfox.platform.github.httpx.AsyncClient` | `agentfox.platform._http.httpx.AsyncClient` |
| `test_github_issues_rest.py` | (same) | (same) |
| `test_github_create_label.py` | (same) | (same) |
| `test_platform_extensions.py` | (same) | (same) |
| `test_merge_strategy_github_pr.py` | (same) | (same) |

`_SLEEP_TARGET` was similarly updated in `test_github_retry.py` and
`test_merge_strategy_github_pr.py` from
`agentfox.platform.github.asyncio.sleep` to
`agentfox.platform._http.asyncio.sleep`.

All test assertions, structure, and semantics are preserved — only the
mock target string changed to reflect the new module location of the
code under test.  All 185 non-gitea/non-gitlab platform tests pass.
