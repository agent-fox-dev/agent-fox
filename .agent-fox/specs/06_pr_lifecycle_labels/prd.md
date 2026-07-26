---
spec_id: '06'
spec_name: pr_lifecycle_labels
title: PR Lifecycle Labels and Protocol Extensions
status: draft
created_at: '2026-07-26T09:31:34.031710+00:00'
updated_at: '2026-07-26T09:38:41.566197+00:00'
owner: Michael Kuehl
source: docs/adr/02-pr-feedback-loop.md
schema_version: 1
---
# PR Lifecycle Labels and Protocol Extensions

## Intent

Fix the premature issue-closing bug in nightshift's PR mode and extend the
platform protocol with PR lifecycle types and query methods needed for PR
monitoring.

## Background

Spec 02 (Configurable Merge Strategy) introduced `workspace.merge_strategy =
"pr"` mode, where nightshift pushes a fix branch and creates a pull request.
However, `_integrate_fix()` returns `"merged"` for PR mode, which causes
`_handle_result()` to close the issue with `af:fixed` immediately — before the
PR has been reviewed or passed CI.

The `PlatformProtocol` has no methods for querying PR state, CI check results,
or review comments. The only PR-related method is `create_pr()`, which returns
a bare URL string with no structured metadata.

This spec fixes the premature-close bug by introducing a new `"pr_created"`
integration status and an `af:pr` label for lifecycle tracking. It also extends
the protocol with PR query methods and types that a follow-on PR feedback loop
spec will consume.

## Goals

1. Issues with open PRs stay open until the PR actually merges — the
   premature-close bug is eliminated.
2. The `af:pr` label provides clear lifecycle tracking for issues that have
   associated PRs.
3. `PlatformProtocol` exposes PR state, CI checks, and review data through
   typed methods and frozen dataclasses.
4. `create_pr()` returns a structured `PrResult` with both URL and PR number.
5. A machine-readable tracking comment links issues to their PRs with attempt
   metadata.

## Non-Goals

- **PR monitoring or feedback loop** — querying PR state and reacting to CI
  failures or review comments is a separate follow-on spec
  (`pr_feedback_loop`), which declares a formal dependency on this spec.
- **GitLab or Gitea PR query implementations** — only GitHub is implemented;
  others raise `NotImplementedError`.
- **Auto-merging PRs** — merging remains a human decision.
- **Configuration changes** — no new config fields are introduced in this spec.
- **GitHub API failure-mode documentation** — the three new `GitHubPlatform`
  methods follow the same error-handling pattern as existing methods, deferring
  to the `_request()` helper for retries, auth, and rate limiting. No
  augmentation to `_request()` is required.
- **Attempt tracking beyond 1** — the `attempt` field in tracking comments is
  always `1` for initial PR creation in this spec. Attempt values > 1 are
  owned by the follow-on `pr_feedback_loop` spec.

## Solution

### 1. New label: `af:pr`

Add `LABEL_PR = "af:pr"` to `afissues.labels` with a `LabelSpec` in
`REQUIRED_LABELS`.

- **Color:** `#1d76db` (blue — distinct from the existing green/red/yellow
  labels).
- **Description:** `"Pull request created — awaiting merge"`.

The existing nightshift label bootstrap routine reads `REQUIRED_LABELS` and
creates missing labels at startup. Adding `af:pr` to `REQUIRED_LABELS` is
sufficient to ensure the label exists in the repo before the pipeline runs.

Issue label lifecycle when `merge_strategy` is `"pr"`:

```
af:fix  ──PR created──►  af:pr  ──PR merged──►  af:fixed (closed)
```

The `af:pr` label signals that an issue has an associated PR and should not be
re-processed by the fix pipeline.

### 2. Protocol dataclasses

Add four frozen dataclasses to `afissues.protocol`:

```python
@dataclass(frozen=True)
class PrResult:
    html_url: str
    number: int

@dataclass(frozen=True)
class PrState:
    number: int
    state: str        # "open" | "closed"
    merged: bool
    head_sha: str

@dataclass(frozen=True)
class CheckResult:
    name: str
    status: str              # "queued" | "in_progress" | "completed"
    conclusion: str | None   # "success" | "failure" | "timed_out" | etc.
    output_title: str        # Empty string when GitHub API returns null for output
    output_summary: str      # Empty string when GitHub API returns null for output

@dataclass(frozen=True)
class ReviewComment:
    user: str
    state: str        # "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED" | "COMMENTED"
    body: str
    submitted_at: str # ISO 8601 string as returned by the GitHub API (e.g. "2026-07-26T09:31:34Z")
```

All four are re-exported from `afissues.__init__`.

**`CheckResult` null output handling:** GitHub check runs may return `null` for
the `output` field before completion. When `output` is null, both
`output_title` and `output_summary` are set to the empty string `""`. This
keeps the type as `str` (not `Optional[str]`) and avoids None-handling
complexity in consumers. The follow-on spec's feedback formatting can skip
check runs with an empty `output_summary`.

**`ReviewComment.submitted_at` format:** The field holds the raw ISO 8601
string as returned by the GitHub API (e.g. `"2026-07-26T09:31:34Z"`). No
parsing is performed here; timezone-aware parsing is deferred to the consumer.

### 3. Protocol method extensions

Add three async methods to `PlatformProtocol`:

| Method | Returns | Purpose |
|--------|---------|---------|
| `get_pr_state(pr_number: int)` | `PrState` | Open/closed/merged status and head SHA |
| `get_pr_checks(pr_number: int)` | `list[CheckResult]` | CI check-run results for the PR's head commit |
| `get_pr_reviews(pr_number: int)` | `list[ReviewComment]` | Review verdicts and comments |

`get_pr_checks()` internally fetches the PR to obtain the head SHA, then
queries check runs for that SHA. The caller provides only `pr_number` for
interface simplicity.

### 4. GitHub API mappings

| Method | GitHub API endpoint |
|--------|-------------------|
| `get_pr_state` | `GET /repos/{owner}/{repo}/pulls/{n}` |
| `get_pr_checks` | `GET /repos/{owner}/{repo}/commits/{sha}/check-runs` |
| `get_pr_reviews` | `GET /repos/{owner}/{repo}/pulls/{n}/reviews` |

Each method uses the existing `_request()` helper in `GitHubPlatform` for auth
headers, retries, and error handling. Responses are mapped to the frozen
dataclasses. No augmentation to `_request()` is required — existing retry,
auth, and rate-limit behavior is sufficient for these endpoints.

For `get_pr_checks`: paginate if `total_count` exceeds the page size (GitHub
default is 30 per page). Collect all check runs across pages. In practice,
check-run counts are bounded by CI configuration, so unbounded pagination is
not a concern; a safety cap of 10 pages (300 check runs) may be added during
implementation at the implementer's discretion but is not required by this
spec.

For `get_pr_reviews`: return all reviews in submission order. The caller is
responsible for filtering (e.g., ignoring `DISMISSED` reviews).

### 5. NullPlatform stubs

`NullPlatform` implements all three methods by raising `NotImplementedError`,
consistent with `NullPlatform.create_pr()`. These methods should never be
reached in practice because the PR feedback work stream (defined in the
follow-on spec) is disabled when platform type is `"none"`.

### 6. `create_pr()` return type change

Change `PlatformProtocol.create_pr()` return type from `str` to `PrResult`.

`GitHubPlatform.create_pr()` currently extracts `html_url` from the API
response. It now also extracts `number` and returns
`PrResult(html_url=data["html_url"], number=data["number"])`.

**Idempotent 422 path:** When GitHub returns a 422 (PR already exists),
`GitHubPlatform.create_pr()` already recovers by calling:

```
GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=open
```

and taking the first result. This lookup logic is unchanged; the only
difference is that both `html_url` and `number` are now extracted from the
matched PR result, returning a `PrResult` instead of a bare `html_url` string.

**Caller updates:**

- `session_lifecycle.py`: access `result.html_url` where it previously used
  the bare string return value.
- `fix_pipeline.py`: access `result.html_url` for logging, `result.number`
  for the tracking comment.

### 7. Fix the premature-close bug

`_integrate_fix()` in `fix_pipeline.py` currently returns
`("merged", changed_files)` for PR mode, which causes `_handle_result()` to
close the issue with `af:fixed`.

Changes:

- `_integrate_fix()` returns `("pr_created", changed_files)` for PR mode.
- Add `self._pr_number: int | None = None` to `FixPipeline.__init__()`.
- After `create_pr()` succeeds, set `self._pr_number = result.number`.
- If `create_pr()` raises, `self._pr_number` remains `None`. In this case
  `_handle_result()` will not receive a `"pr_created"` status (the exception
  propagates before the status tuple is returned), so no partial state is
  exposed to `_handle_result()`.
- `_handle_result()` handles the new `"pr_created"` status by:
  1. Checking for idempotency: if `af:pr` is already present on the issue,
     skip label changes (see section 7a).
  2. Adding the `af:pr` label.
  3. Removing the `af:fix` label.
  4. Posting the structured tracking comment (see section 8).
  5. Leaving the issue **open** (no close call).

#### 7a. Idempotency of `_handle_result()` for `"pr_created"`

If `_handle_result()` is called with `"pr_created"` and `af:pr` is already
present on the issue (e.g., a retry after a partial failure where the process
crashed after label assignment but before the tracking comment was posted):

- **Skip label changes** — the `assign_label` and `remove_label` calls are
  already idempotent at the GitHub API level (assigning an already-present
  label is a no-op), so no special guard code is strictly required. However,
  skipping is acceptable as an optimization.
- **Always post a new tracking comment** — even on retry, a new tracking
  comment is posted. This ensures a visible record of the retry attempt in the
  issue history and matches nightshift's append-only comment behavior.

### 8. Structured tracking comment

Machine-readable comment posted on the issue after PR creation:

```
<!-- af:pr-tracking pr_number=42 attempt=1 -->
Pull request created: https://github.com/owner/repo/pull/42
```

The `attempt` value is always `1` when posted by this spec's implementation of
`_handle_result()`. The follow-on `pr_feedback_loop` spec owns the logic for
computing and incrementing attempt values beyond 1.

Utilities added as module-level functions in `fix_pipeline.py` (importable by
the follow-on `pr_feedback` module):

```python
import re

PR_TRACKING_PATTERN = re.compile(
    r"<!-- af:pr-tracking pr_number=(\d+) attempt=(\d+) -->"
)

def parse_tracking_comment(body: str) -> tuple[int, int] | None:
    """Extract (pr_number, attempt) from a tracking comment, or None."""

def format_tracking_comment(
    pr_number: int, attempt: int, pr_url: str, message: str
) -> str:
    """Format a tracking comment body."""
```

Each feedback iteration posts a **new** comment (never edits). The most recent
matching comment is authoritative. This provides a visible timeline of attempts
in the issue history.

## Testing

Detailed test contracts — including a regression test for the premature-close
bug fix and round-trip tests for the tracking comment utilities — are produced
by the `spec generate` step (`test_spec.json`). The following test expectations
are normative for this spec:

- **Premature-close regression test:** Assert that when `_integrate_fix()`
  returns `"pr_created"`, `_handle_result()` does **not** close the issue and
  does **not** apply `af:fixed`.
- **Tracking comment round-trip:** Assert that
  `parse_tracking_comment(format_tracking_comment(42, 1, url, msg))` returns
  `(42, 1)`.
- **`CheckResult` null output:** Assert that a GitHub API response with
  `output: null` produces `output_title=""` and `output_summary=""`.
- **Idempotency:** Assert that calling `_handle_result()` with `"pr_created"`
  when `af:pr` is already present does not raise and still posts a tracking
  comment.

## Integration Points

### `afissues` package

- `afissues/protocol.py`: 4 new dataclasses + 3 new protocol methods + 3 new
  NullPlatform stubs + `create_pr()` return type change.
- `afissues/github.py`: 3 new methods on `GitHubPlatform` + `create_pr()`
  return type change.
- `afissues/labels.py`: `LABEL_PR` constant + `REQUIRED_LABELS` entry.
- `afissues/__init__.py`: re-export all new symbols.

### `agentfox` package

- `fix_pipeline.py`: `_integrate_fix()` returns `"pr_created"`,
  `_handle_result()` handles it, tracking comment utilities added,
  `self._pr_number` instance attribute.
- `session_lifecycle.py`: update `create_pr()` caller to use
  `result.html_url`.

## Tech Stack

- Python 3.12
- httpx for GitHub REST API (existing in `GitHubPlatform`)
- `re` module for tracking comment parsing

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 02_merge_strategy | all | 1 | Assumes `create_pr()` exists on protocol and `_integrate_fix()` handles PR mode |
| 03_extract_platform_afissues | all | 1 | Protocol types and labels live in `afissues` package |

## Verified External API

### `afissues` (local, v4.2.6)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `PlatformProtocol` | `afissues.protocol` | `class PlatformProtocol(Protocol)` | Adding 3 new methods |
| `NullPlatform` | `afissues.protocol` | `class NullPlatform` | Adding 3 stubs |
| `IssueResult` | `afissues.protocol` | `@dataclass(frozen=True)` | Existing, unchanged |
| `IssueComment` | `afissues.protocol` | `@dataclass(frozen=True)` | Existing, unchanged |
| `GitHubPlatform` | `afissues.github` | `class GitHubPlatform` | Adding 3 new methods + return type change on `create_pr` |
| `create_pr` | `afissues.protocol` | `async (*, title, body, head, base) -> str` | Return type changes to `PrResult` |
| `LABEL_FIX` | `afissues.labels` | `str = "af:fix"` | Existing |
| `LABEL_FIXED` | `afissues.labels` | `str = "af:fixed"` | Existing |
| `REQUIRED_LABELS` | `afissues.labels` | `list[LabelSpec]` | Adding `af:pr` entry |
| `IntegrationError` | `afissues.errors` | `class IntegrationError(AfIssuesError)` | Existing, used by new GitHub methods |

### `agentfox` (local, v4.2.6)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `FixPipeline._integrate_fix` | `agentfox.nightshift.fix_pipeline` | `async (issue, spec, workspace) -> tuple[str, list[str]]` | Status changes from `"merged"` to `"pr_created"` for PR mode |
| `FixPipeline._handle_result` | `agentfox.nightshift.fix_pipeline` | `async (issue, spec, harvest_result) -> None` | Adding `"pr_created"` handler |
| `build_pr_body` | `agentfox.nightshift.fix_pipeline` | `def (...) -> str` | Existing, unchanged |

## Design Decisions

1. **Breaking change to `create_pr()` return type is acceptable.** This is an
   internal monorepo with no external consumers (established in spec 03). All
   callers are known and updated atomically. Parsing the PR number from the URL
   would be fragile and GitHub-specific.

2. **PR number propagated via instance state.** `self._pr_number` on
   `FixPipeline` avoids changing the `_integrate_fix()` return type. This
   matches the pipeline's existing pattern of sharing state via instance
   attributes. If `create_pr()` raises, `self._pr_number` remains `None` and
   `_handle_result()` is never called with `"pr_created"`, so no partial state
   is exposed.

3. **New comment each time, never edit.** The "most recent matching comment"
   parsing pattern requires sequential comments. Append-only comments provide a
   visible timeline and match nightshift's existing comment behavior.

4. **NullPlatform raises NotImplementedError for all three new methods.**
   Consistent with `NullPlatform.create_pr()`. Returning fake data could mask
   bugs. The PR feedback stream is disabled for NullPlatform, so these paths
   should never be reached.

5. **`get_pr_checks()` internally fetches the head SHA.** The caller provides
   only `pr_number` for interface simplicity. The extra API call is negligible
   at 15-minute polling intervals with at most 5 PRs per cycle.

6. **Tracking comment utilities live in `fix_pipeline.py`.** They are defined
   as module-level functions, importable by the follow-on `pr_feedback` module.
   This avoids creating a new utility file for two small functions.

7. **Dependency IDs use ordinal prefixes (`02_merge_strategy`,
   `03_extract_platform_afissues`).** These match the spec directory names used
   by the spec CLI and are the canonical format for dependency references.

8. **`get_pr_checks()` pagination is unbounded in spec.** Check-run counts are
   bounded in practice by CI configuration. A safety cap of 10 pages (300
   check runs) may be applied during implementation at the implementer's
   discretion; it is not mandated here.

9. **`pr_feedback_loop` dependency sequencing.** `pr_feedback_loop` declares a
   formal dependency on this spec in its own Dependencies table. No reciprocal
   reference is needed here; the relationship is documented in Non-Goals for
   context.

10. **`attempt` is always 1 in this spec.** The `format_tracking_comment()`
    function accepts `attempt` as a parameter to support the follow-on
    `pr_feedback_loop` spec, which owns the logic for computing and
    incrementing attempt values. This spec always passes `attempt=1`.

11. **`CheckResult` fields are non-optional strings.** Using `str` rather than
    `Optional[str]` for `output_title` and `output_summary` keeps consumer
    code simple. The empty string convention is consistent and documented on
    the dataclass.

12. **`ReviewComment.submitted_at` is a raw ISO 8601 string.** Raw strings are
    simpler and avoid timezone-aware parsing complexity. The follow-on spec
    performs any required parsing.

13. **Idempotency via GitHub API no-ops.** The GitHub API already treats
    assigning an existing label and removing an absent label as no-ops.
    `_handle_result()` relies on this behavior rather than adding guard logic.
    A new tracking comment is always posted to ensure a complete audit trail.
