# Errata: 03_extract_platform_afissues

Divergences between specification 03 and the actual codebase discovered during
task group 1 (test writing).

## 1. PlatformProtocol.close() is async, not synchronous

**Spec claim (03-REQ-2.1):** "12 async methods and a synchronous `close()`
method."

**Actual code (protocol.py:165):** `async def close(self) -> None: ...` —
`close()` is async.  All 12 public methods on `PlatformProtocol` are
coroutines; there are zero synchronous public methods.

**Impact:** TS-03-6 assertion `not inspect.iscoroutinefunction(PlatformProtocol.close)`
would fail against the actual code.  Tests were written to match the actual
async signature.

## 2. _request() re-raises raw httpx exceptions, not IntegrationError

**Spec claim (03-REQ-3.E2, TS-03-E4):** "retries up to `_MAX_RETRIES` times
(3), then raises `IntegrationError` from `afissues.errors` with
`retryable=True`."

**Actual code (github.py:229):** After exhausting retries, the method executes
`raise last_exc` which re-raises the original httpx transport exception
(`ConnectTimeout`, `ConnectError`, or `ReadTimeout`).  `IntegrationError` is
raised by higher-level methods for HTTP status code errors (4xx/5xx), not for
transport-level failures.

**Impact:** TS-03-E4 assertions expecting `except IntegrationError` would never
catch the actual exception.  Tests were written to expect raw httpx exceptions.

## 3. Platform test file count is 9, not 10

**Spec claim (03-REQ-9.1, TS-03-31):** "10 unit test files relocated from
`packages/agentfox/tests/unit/platform/`."

**Actual directory:** Contains 9 `test_*.py` files:
- test_github_create_label.py
- test_github_issues_rest.py
- test_github_rest.py
- test_github_retry.py
- test_github_ssrf.py
- test_merge_strategy_github_pr.py
- test_merge_strategy_protocol.py
- test_platform_config.py
- test_platform_extensions.py

**Impact:** TS-03-31 `assert len(unit_tests) == 10` must be changed to
`assert len(unit_tests) == 9` (or updated when the test suite is built for
task group 3).

## 4. Additional helper symbols in github.py not listed in spec

**Spec claim (03-REQ-3.3):** Lists `_SSRFGuardTransport`, `_validate_github_url`,
`_validate_transport_address`, `_check_address`, `_GITHUB_TIMEOUT`, and
`_MAX_RETRIES` as the helpers to move.

**Actual code:** The module also defines `_RETRYABLE_ERRORS` (tuple of httpx
exception types), `_RETRY_BACKOFF` (float = 1.0), `_MAX_ERROR_TEXT` (int = 500),
and `_truncate_response()` (helper function used in 15+ methods).  These are
required for `GitHubPlatform` to function and must be moved with the rest.

**Impact:** Tests for TS-03-12 were extended to also verify the presence of
`_RETRYABLE_ERRORS` and `_truncate_response`.

## 5. conftest.py fixture imports from agentfox.core.config

**Spec claim (03-REQ-9.2):** "Update fixture imports to reference `afissues.*`."

**Actual code:** The `platform/conftest.py` fixture `platform_config` imports
from `agentfox.core.config.PlatformConfig`, which is not part of `afissues`.
The fixture is also unused by any of the 9 test files.

**Impact:** The fixture import cannot simply be changed to an `afissues`
import since `PlatformConfig` lives in `agentfox.core.config`.  The unused
fixture can be removed or left with its original import path.
