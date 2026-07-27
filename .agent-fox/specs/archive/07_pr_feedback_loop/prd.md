---
spec_id: '07'
spec_name: pr_feedback_loop
title: PR Feedback Loop
status: draft
created_at: '2026-07-26T09:33:30.042969+00:00'
updated_at: '2026-07-26T09:54:00.031724+00:00'
owner: Michael Kuehl
source: docs/adr/02-pr-feedback-loop.md
schema_version: 1
---
# PR Feedback Loop

## Intent

Enable nightshift to monitor open pull requests and iterate on fixes based on
CI failures and reviewer feedback, closing the loop between PR creation and
merge.

## Background

When `workspace.merge_strategy` is `"pr"`, nightshift creates a pull request
but has no mechanism to monitor it afterward. If CI fails or a reviewer
requests changes, the fix stalls until a human notices.

Spec `06_pr_lifecycle_labels` introduces the `af:pr` label, protocol methods
for querying PR state (`get_pr_state`, `get_pr_checks`, `get_pr_reviews`),
structured tracking comments, and the `"pr_created"` integration status. This
spec builds on those foundations to add a complete PR monitoring and feedback
re-entry loop.

The timing gap is significant. CI pipelines commonly take 10–30 minutes; human
review may take hours. Nightshift has destroyed the worktree and moved on to
other issues long before feedback arrives.

## Goals

1. Nightshift detects merged PRs and closes the originating issue
   automatically.
2. Nightshift detects CI failures and reviewer change requests, and re-runs
   the coder with the failure context injected as `review_feedback`.
3. Feedback iterations are capped at a configurable limit (`max_pr_retries`,
   default 2) to prevent infinite retry loops. With the default, nightshift
   makes one original attempt plus up to 2 feedback iterations — 3 total
   pushes to the PR at most.
4. PR monitoring runs as a lower-priority work stream alongside the fix
   pipeline.
5. PR state changes (merged, closed, CI failure, review feedback) are logged
   and commented on the originating issue.

## Non-Goals

- **Auto-merging PRs** — merging remains a human decision. Auto-merge may be
  revisited as a separate opt-in feature.
- **GitLab or Gitea support** — only GitHub is supported for PR monitoring.
  Other platforms raise `NotImplementedError` for the protocol methods.
- **Reviewer invocation** — CI serves as the quality gate for feedback
  iterations; running the internal coder-reviewer loop would duplicate effort.
- **DuckDB-backed state** — comment-based state tracking is sufficient. A
  database-backed alternative is a future option.
- **Triage on re-entry** — the issue was already triaged on the first pass.
  Feedback iterations use a synthetic `TriageResult`.

## Dependency and Sequencing

This spec is **blocked on `pr_lifecycle_labels` (spec 06) being merged first**.
The two specs are created together for planning purposes but are implemented
sequentially — spec 06 lands first, then spec 07 builds on the types, protocol
methods, and tracking-comment utilities it introduces. `pr_feedback_loop` is
purely additive and does not supersede any part of `pr_lifecycle_labels`.

This ordering is enforced at the artifact-generation level: `spec generate`
must not be run for spec 07 until spec 06 has been merged.

## Solution

### 1. Configuration

Add to `NightShiftConfig` in `agentfox/core/config.py`:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pr_check_interval` | `int` | `900` | Seconds between PR status polls (min 60) |
| `max_pr_retries` | `int` | `2` | Max feedback iterations per PR |

Validators (same pattern as existing `clamp_interval_minimum`):

- `pr_check_interval`: clamp to minimum 60.
- `max_pr_retries`: clamp to range [0, 10].

### 2. PR feedback work stream

Add a `"pr-feedback"` entry in `build_streams()` in
`agentfox/nightshift/streams.py`, wrapping a new engine method
`_check_open_prs()`.

- **Interval:** `config.night_shift.pr_check_interval` (default 900s).
- **Enabled when:** `config.workspace.merge_strategy == "pr"` **and** platform
  type is not `"none"`.
- **Priority:** Add `"pr-feedback"` to `DaemonRunner`'s priority list, after
  `"fix-pipeline"`. Fix new issues first, then check existing PRs.

### 3. Engine dispatcher (`_check_open_prs`)

`_check_open_prs()` on `NightShiftEngine` is a thin async entry point,
following the same pattern as the existing `_drain_issues()` method:

- It is declared as `async def _check_open_prs(self) -> None`.
- It sequentially `await`s each `process_pr_issue()` call (no concurrent
  gather).
- It is invoked directly by the async event loop via the `EngineWorkStream`
  wrapper, with no thread executor involved.

Steps:

1. List all open issues labeled `af:pr` via
   `platform.list_issues_by_label(LABEL_PR)`. Issues are returned in
   **oldest-first order** (ascending by `created_at`), matching the default
   `sort=created, direction=asc` behaviour of `list_issues_by_label`. This
   prevents starvation of older PRs when the cap is reached.
2. Cap at 5 issues per poll cycle (hardcoded constant `_MAX_PR_CHECKS = 5`,
   defined in `engine.py` near `_check_open_prs()` where it is applied —
   it governs the dispatcher's behavior, not the feedback module's logic).
   Issues beyond the cap are deferred to the next cycle. This keeps API usage
   well within GitHub's 5,000 req/hour limit even at the shortest poll
   interval.
3. For each issue (sequentially), `await pr_feedback.process_pr_issue()`.
4. Increment `state.issue_checks_completed` for each issue processed.

### 4. PR check flow (`process_pr_issue`)

New module: `agentfox/nightshift/pr_feedback.py`

Main entry point: `async def process_pr_issue(issue: IssueResult, config: NightShiftConfig, platform: PlatformProtocol, pipeline: FixPipeline) -> None`.

For each open issue labeled `af:pr`:

1. **Parse tracking comment.** List issue comments via
   `platform.list_issue_comments(issue.number)`. Find the most recent comment
   matching `PR_TRACKING_PATTERN` (imported from `fix_pipeline`). "Most
   recent" is determined by taking the **last matching item in list order**,
   since `list_issue_comments` in `GitHubPlatform` returns comments in
   chronological order (oldest-first), consistent with the default GitHub API
   ordering (`sort=created, direction=asc`). This is efficient (single pass,
   no sorting) and correct given the API contract. Extract `pr_number` and
   `attempt`. If no matching comment is found or the comment is malformed:
   log a WARNING and **skip** the issue. Leave `af:pr` label in place for
   retry on the next poll cycle.

2. **Check PR state** via `platform.get_pr_state(pr_number)`:
   - **Merged** (`merged == True`) → add `af:fixed` label, remove `af:pr`
     label, close issue with comment `"PR #{n} merged."`. Log at INFO. Done.

     **Atomicity:** These three operations (`assign_label(af:fixed)`,
     `remove_label(af:pr)`, `close_issue`) are applied sequentially and are
     each idempotent at the GitHub API level. If any step raises a platform
     API exception mid-sequence, the issue is skipped for this cycle (logged
     at WARNING, consistent with Section 7 polling-phase error handling). On
     the next poll cycle, the issue will still match the `af:pr` query and
     the merged-PR handling will re-run all three steps. Steps that already
     succeeded are no-ops at the API level, so re-application is safe.

   - **Closed without merge** (`state == "closed"` and `merged == False`) →
     post comment `"PR #{n} was closed without merging. Removing af:pr label
     for manual triage."`, remove `af:pr`, leave issue open. Log at INFO.
     Done.
   - **Open** (`state == "open"`) → continue to step 3.

3. **Check CI** via `platform.get_pr_checks(pr_number)`:

   The CI and review steps are **strictly sequential and mutually exclusive**:
   CI failure in this step triggers re-entry immediately and step 4 is never
   reached. Only if all CI checks pass (or no CI is configured) does step 4
   run. Consequently, `_collect_feedback()` always receives a single trigger
   source — CI failures **or** review feedback, never both simultaneously.

   - Any check with `status == "in_progress"` or `status == "queued"` → skip
     this issue, wait for next poll.
   - Any check with `conclusion == "failure"` or `conclusion == "timed_out"` →
     enter feedback re-entry (step 5). Both indicate actionable problems the
     coder could address. Log at INFO: `"Re-entry triggered for issue #{n},
     PR #{pr}: CI failure/timeout."`.
   - `conclusion` values `"cancelled"`, `"action_required"`, `"stale"` are
     treated as **skip** conditions — caused by external factors that re-coding
     cannot fix. If all checks are in these ambiguous states with none
     `"success"`, log at WARNING: `"Skipped issue #{n}, PR #{pr}: all checks
     in ambiguous state (cancelled/action_required/stale)."` and wait for
     next poll.
   - All checks have `conclusion == "success"` → continue to step 4.
   - Empty check list → proceed to step 4 (no CI configured).

4. **Check reviews** via `platform.get_pr_reviews(pr_number)`:
   - Filter out reviews with `state == "DISMISSED"` (dismissed reviews
     nullify the previous verdict, matching GitHub's merge-blocking behavior).
   - If the latest remaining review has `state == "CHANGES_REQUESTED"` →
     enter feedback re-entry (step 5). Log at INFO: `"Re-entry triggered for
     issue #{n}, PR #{pr}: reviewer requested changes."`.
   - Otherwise (no active reviews, latest is `APPROVED` or `COMMENTED`) →
     skip, PR is healthy and waiting for human merge.

5. **Feedback re-entry** (see section 5 below).

### 5. Feedback re-entry

When CI fails or a reviewer requests changes:

1. **Check retry limit.** Read `attempt` from the tracking comment. If
   `attempt > config.night_shift.max_pr_retries`, log at INFO: `"Retry limit
   reached for issue #{n}, PR #{pr} (attempt {attempt}/{max_pr_retries + 1}).
   Needs manual attention."` and post a comment:
   ```
   Feedback retry limit reached (attempt {attempt}/{max_pr_retries + 1}).
   This PR needs manual attention.
   ```
   Leave `af:pr` label in place — a human needs to take over. Stop.

   **Attempt counter semantics:**
   - `attempt=1` is set by spec 06 when the tracking comment is first posted
     (i.e., the original PR creation).
   - Feedback iterations increment the attempt on each re-entry: the first
     feedback iteration reads `attempt=1` and posts `attempt=2`; the second
     reads `attempt=2` and posts `attempt=3`; and so on.
   - The re-entry guard is: `if attempt > max_pr_retries → stop`.
   - With `max_pr_retries=2`: feedback runs when `attempt=1` (→ posts 2) and
     `attempt=2` (→ posts 3), then stops at `attempt=3`. That is 2 feedback
     iterations + 1 original = 3 total pushes, matching the ADR.
   - With `max_pr_retries=0`: a feedback trigger at `attempt=1` evaluates
     `1 > 0 → True` and stops immediately. No feedback iterations ever run.

2. **Collect feedback.** `_collect_feedback(trigger: Literal["ci", "review"], ci_failures: list[CheckResult], review_comments: list[ReviewComment]) -> str` formats the context as markdown:
   - For CI failures (`trigger="ci"`): list each failed check's `name`,
     `output_title`, and `output_summary` under a `## CI Failures` heading.
   - For review change requests (`trigger="review"`): include reviewer `user`,
     `body`, and `state` under a `## Review Feedback` heading.
   - Because CI and review re-entry are mutually exclusive paths (see step 3
     above), `_collect_feedback()` always receives exactly one trigger source
     and formats a single section. The `trigger` parameter makes the intent
     explicit and prevents accidental dual-section output.

3. **Fetch and create worktree from the fix branch.**

   a. **Fetch:** Run `git fetch origin <branch>` before creating the worktree
      to ensure the local ref is current (the previous fix-pipeline worktree
      was destroyed). Fetch failure is treated as a worktree creation error:
      ERROR logged, issue skipped for this cycle, labels left intact. This is
      consistent with the general error handling for feedback iteration
      operations (Section 7).

   b. **Create worktree:** Use `git worktree add` targeting the fix branch
      HEAD (not the integration branch). The fix branch name is derived from
      the issue using `sanitise_branch_name(issue.title, issue.number)`.

   **Worktree path:** The feedback worktree is created in the **same parent
   directory as existing fix pipeline worktrees**, using a `feedback-` prefix
   in the directory name: `worktrees/feedback-{issue_number}`. This keeps all
   nightshift worktrees co-located and uses the same cleanup patterns as the
   fix pipeline's `_setup_workspace()`. The worktree base directory is read
   from the same config path used by the fix pipeline.

   This logic is encapsulated in `_setup_feedback_worktree()` in
   `pr_feedback.py` (not reusing `_setup_workspace()`, which targets the
   integration branch).

4. **Run coder session with feedback.** Construct a synthetic `TriageResult`:
   - `summary`: issue title
   - `affected_files`: files changed in the PR, computed via
     `git diff --name-only <integration_branch> <fix_branch>` on the fix
     branch worktree. The integration branch name is read from
     `config.workspace.integration_branch`. If the diff command fails or
     returns an empty list, `affected_files` defaults to `[]` and a WARNING
     is logged.
   - `criteria`: empty list (`[]`)
   - `assessed_complexity`: `None`
   - `issue_body`: original issue body (from `issue.body`)

   Invoke the coder via `_build_coder_prompt()` with the collected feedback
   passed as the `review_feedback` parameter. The `prior_context` and
   `knowledge_context` parameters are passed as **empty strings** (`""`).
   Feedback re-entry is a targeted fix iteration, not a fresh investigation —
   the `review_feedback` parameter carries the actionable context (CI
   failures, review comments), and the synthetic `TriageResult` supplies the
   issue body. Prior attempt context and knowledge retrieval add complexity
   without clear value for a focused re-run. No triage step. No reviewer
   step — CI serves as the quality gate.

   Run the coder session using `_run_coder_session()` from the pipeline,
   passing the **same model ID used by the original fix pipeline** (read from
   the nightshift model configuration via the same config path that
   `_run_coder_session` uses). No new config field is needed.

5. **Post updated tracking comment** with the attempt count incremented using
   `format_tracking_comment()` from `fix_pipeline`. The `pr_url` parameter is
   passed as an empty string (`""`) for feedback iteration comments — it is
   only meaningful in the initial tracking comment posted by spec 06, and the
   human-readable message is self-descriptive without it:
   ```
   <!-- af:pr-tracking pr_number=42 attempt=2 -->
   CI feedback iteration 2: re-ran coder with failure context.
   ```

   **The tracking comment is posted before the force-push.** This ordering
   ensures the attempt counter is persisted before any changes are pushed. If
   `add_issue_comment()` or `format_tracking_comment()` raises during this
   step, the force-push is skipped and the error is treated as a feedback
   iteration failure (ERROR logged, issue skipped, worktree cleaned up via
   `finally`). The old attempt counter remains in place on the issue, so the
   next poll cycle will re-trigger re-entry with the correct counter — no
   retry is silently lost.

   If the tracking comment succeeds but the subsequent force-push fails, the
   attempt counter is already incremented. This is acceptable: the next cycle
   will see the new counter and either retry (if under the limit) or surface
   the limit to the operator. The counter advancing without a successful push
   is a conservative trade-off that avoids indefinite re-triggering.

   Log at INFO: `"Feedback iteration {attempt} complete for issue #{n},
   PR #{pr}."`.

6. **Handle no-change result.** After the coder session completes but before
   the force-push, compute the diff on the worktree. If the diff is empty
   (no file changes):
   - **Skip the force-push.**
   - Post an additional comment to the issue:
     ```
     Feedback iteration {attempt}: coder produced no changes.
     This PR needs manual attention if the problem persists.
     ```
   - **The attempt counter tracking comment was already posted** in step 5
     (with `pr_url=""`), so the retry is consumed regardless of whether
     changes were produced. A no-change result means the coder cannot address
     the feedback, so consuming the retry prevents indefinite polling.
   - Log at WARNING: `"Feedback iteration {attempt} for issue #{n}, PR #{pr}:
     coder produced no changes."`.
   - Proceed to step 7 (cleanup).

7. **Auto-commit and force-push.** If the coder produced changes, commit any
   changes on the fix branch using the existing
   `_auto_commit_pending_changes()` helper with the commit message format:
   ```
   fix: <issue title> [nightshift feedback #{attempt}]
   ```
   The attempt number in the commit message makes the git log history readable
   and helps operators identify which push was a feedback iteration vs the
   original fix. Then `git push --force` the fix branch to origin. The PR
   updates automatically.

   `git push --force` (not `--force-with-lease`) is used because nightshift
   destroyed the previous worktree — the local ref is stale and
   `--force-with-lease` would fail. The fix branch is explicitly
   nightshift-owned (the `fix/{N}-{slug}` naming makes this clear).

8. **Destroy the worktree.** Clean up the git worktree by calling
   `_cleanup_feedback_worktree()`. This function is also called in a `finally`
   block during error paths (see Section 7). If the worktree directory does
   not exist (e.g., because `git worktree add` failed before creating
   anything), `_cleanup_feedback_worktree()` **silently no-ops** and logs at
   DEBUG level: `"Feedback worktree not found for issue #{n} — skipping
   cleanup."`. This prevents a secondary exception in the `finally` block from
   masking the original failure. The next poll cycle will re-check CI on the
   updated PR.

### 6. Structured Logging and Observability

The PR feedback loop emits structured log lines to allow operators to monitor
loop health without additional counters on `NightShiftState`. No new fields
are added to `NightShiftState` beyond the existing `issue_checks_completed`.

**INFO level** (normal state transitions):
- PR merged: `"PR #{pr} merged for issue #{n}. Closed with af:fixed."`
- PR closed without merge: `"PR #{pr} closed without merge for issue #{n}. Removed af:pr."`
- Re-entry triggered (CI): `"Re-entry triggered for issue #{n}, PR #{pr}: CI failure/timeout."`
- Re-entry triggered (review): `"Re-entry triggered for issue #{n}, PR #{pr}: reviewer requested changes."`
- Retry limit reached: `"Retry limit reached for issue #{n}, PR #{pr} (attempt {attempt}/{max+1}). Needs manual attention."`
- Feedback iteration complete: `"Feedback iteration {attempt} complete for issue #{n}, PR #{pr}."`

**DEBUG level** (low-level operational events):
- Feedback worktree missing during cleanup: `"Feedback worktree not found for issue #{n} — skipping cleanup."`

**WARNING level** (skips and non-fatal anomalies):
- Missing or malformed tracking comment: `"Skipped issue #{n}: no valid tracking comment found. Will retry next cycle."`
- Ambiguous CI state: `"Skipped issue #{n}, PR #{pr}: all checks in ambiguous state (cancelled/action_required/stale)."`
- Platform API error (polling phase): `"Skipped issue #{n}: platform API error during polling — {exc}. Will retry next cycle."`
- Coder produced no changes: `"Feedback iteration {attempt} for issue #{n}, PR #{pr}: coder produced no changes."`
- `affected_files` diff failure: `"git diff --name-only failed for issue #{n}, PR #{pr} — defaulting affected_files to []."`

**ERROR level** (feedback iteration failures):
- Fetch failure: `"Error in feedback iteration for issue #{n}, PR #{pr}: git fetch failed — {exc}."`
- Worktree creation failure: `"Error in feedback iteration for issue #{n}, PR #{pr}: git worktree add failed — {exc}."`
- Force-push failure: `"Error in feedback iteration for issue #{n}, PR #{pr}: git push --force failed — {exc}."`
- Coder session exception: `"Error in feedback iteration for issue #{n}, PR #{pr}: coder session raised — {exc}."`
- Auto-commit failure: `"Error in feedback iteration for issue #{n}, PR #{pr}: auto-commit failed — {exc}."`
- Tracking comment post failure: `"Error in feedback iteration for issue #{n}, PR #{pr}: failed to post tracking comment — {exc}."`

### 7. Error Handling

#### Platform API errors (polling phase)

If `get_pr_state`, `get_pr_checks`, `get_pr_reviews`, `list_issue_comments`,
or any other platform API call raises an exception during the polling phase
(transient 5xx, rate-limit 429, auth error, network timeout):

- **Log at WARNING level** with the issue number, PR number, and exception
  message (see Section 6 for the log line format).
- **Skip the issue** for this poll cycle. No comment is posted to the issue.
- Leave all labels intact. The issue will be retried on the next poll cycle.

This also applies to mid-sequence failures during label transitions (e.g.,
`assign_label(af:fixed)` succeeds but `close_issue` then raises). The issue
is skipped and retried on the next cycle. All three operations (`assign_label`,
`remove_label`, `close_issue`) are idempotent at the GitHub API level, so
re-application on the next cycle is safe — steps that already succeeded are
no-ops.

Rationale: comment spam from transient API errors would be worse than silent
recovery. Rate-limit and transient 5xx errors are self-healing. Persistent
auth errors will surface as stale `af:pr` labels that operators can inspect.

#### Fix branch not found on origin

If `git fetch origin <branch>` or `git worktree add` fails because the fix
branch no longer exists on origin (e.g., a human deleted the branch), the
failure is treated as a worktree creation error: log at ERROR level and skip
the issue for this cycle. Labels are left intact.
`_cleanup_feedback_worktree()` is called in the `finally` block and silently
no-ops if the directory was never created. This is consistent with the general
worktree creation failure path — no special-casing is needed.

#### Feedback iteration failures (re-entry phase)

If a critical feedback-iteration operation fails — including `git fetch origin
<branch>` (e.g., network error, branch not found), `git worktree add` (e.g.,
branch name conflict, disk space), `format_tracking_comment()` or
`add_issue_comment()` during tracking comment posting, `git push --force`
(e.g., network error, protected-branch policy), coder session error, or
auto-commit failure:

- **Log the error** at ERROR level with the issue number, PR number, and full
  exception details (see Section 6 for log line formats).
- **Skip the issue** for this poll cycle. No comment is posted to the issue
  (to avoid spam from transient failures).
- Leave all labels intact (including `af:pr`). The issue will be retried on
  the next poll cycle.

This is consistent with the missing-tracking-comment behavior: skip and retry
next cycle. Worktree creation failures and force-push failures are typically
transient (network issues, temporary disk pressure). If the worktree was
partially created before the failure, the cleanup step
(`_cleanup_feedback_worktree`) is attempted in a `finally` block to avoid
leaving orphaned worktrees. `_cleanup_feedback_worktree()` silently no-ops
if the worktree directory does not exist (see Section 5 step 8).

**Tracking comment failure:** If `add_issue_comment()` raises during step 5
of the feedback re-entry (posting the updated tracking comment), the
force-push is skipped, the error is logged at ERROR, and the issue is skipped
for this cycle. The old attempt counter remains on the issue; the next poll
cycle will re-trigger re-entry with the correct counter. If the tracking
comment succeeds but the force-push subsequently fails, the incremented
attempt counter is already persisted — this is acceptable and conservative:
the next cycle retries (or hits the limit) without risk of indefinite
re-triggering.

### 8. Label Lifecycle and Fix-Pipeline Guard

The `af:fix` and `af:pr` labels are mutually exclusive by design — an issue
cannot carry both simultaneously. The fix pipeline selects issues via
`list_issues_by_label(LABEL_FIX)`, and spec 06's premature-close fix removes
`af:fix` and adds `af:pr` when a PR is created. When a PR is closed without
merge, `af:pr` is removed and the issue is left open (without `af:fix`); the
fix pipeline will only re-pick it up if `af:fix` is re-added by a subsequent
labeling action.

No additional guard code is needed in the fix pipeline — the label lifecycle
itself prevents double-processing.

### 9. Module structure

`agentfox/nightshift/pr_feedback.py` contains:

- `process_pr_issue(issue: IssueResult, config: NightShiftConfig, platform: PlatformProtocol, pipeline: FixPipeline) -> None` — main entry point, orchestrates the full check flow
- `_check_pr_state()` — queries and interprets PR state (merged/closed/open)
- `_check_ci_status()` — interprets CI check results per the rules in
  section 4 step 3
- `_check_reviews()` — filters dismissed reviews, checks latest active review
- `_collect_feedback(trigger: Literal["ci", "review"], ci_failures: list[CheckResult], review_comments: list[ReviewComment]) -> str` — formats CI failures or review comments as a markdown string (always a single section, as CI and review re-entry are mutually exclusive paths)
- `_run_feedback_iteration()` — fetch, worktree setup, coder session, tracking comment, force-push, worktree cleanup
- `_setup_feedback_worktree()` — runs `git fetch origin <branch>` then creates
  a worktree at `worktrees/feedback-{issue_number}` from the fix branch HEAD
- `_cleanup_feedback_worktree()` — removes the worktree (called in `finally`
  block to prevent orphaned worktrees on failure); silently no-ops at DEBUG
  level if the directory does not exist

Module-level constants defined in `pr_feedback.py`:

```python
_FEEDBACK_ITERATION_MESSAGE = "CI feedback iteration {attempt}: re-ran coder with failure context."
_NO_CHANGES_MESSAGE = "Feedback iteration {attempt}: coder produced no changes. This PR needs manual attention if the problem persists."
_RETRY_LIMIT_MESSAGE = "Feedback retry limit reached (attempt {attempt}/{max_attempts}). This PR needs manual attention."
_FEEDBACK_COMMIT_MESSAGE = "fix: {issue_title} [nightshift feedback #{attempt}]"
```

`_MAX_PR_CHECKS = 5` is defined in `engine.py` near `_check_open_prs()`,
where it is applied. It governs the dispatcher's behavior, not the feedback
module's logic.

These constants are not user-configurable.

The module receives `config`, `platform`, and a reference to the
`FixPipeline` instance (for access to `_build_coder_prompt()`,
`_run_coder_session()`, `_auto_commit_pending_changes()`, and related
helpers). It does not subclass `FixPipeline` — it composes with it.

Tracking comment utilities (`parse_tracking_comment`,
`format_tracking_comment`, `PR_TRACKING_PATTERN`) are imported from
`fix_pipeline.py` (defined in spec `06_pr_lifecycle_labels`).

## Integration Points

### `agentfox/core/config.py`

`NightShiftConfig`: add `pr_check_interval` and `max_pr_retries` fields with
validators.

### `agentfox/nightshift/streams.py`

`build_streams()`: add `"pr-feedback"` `EngineWorkStream` wrapping
`engine._check_open_prs`, with interval from `pr_check_interval` and enabled
conditional on `merge_strategy == "pr"` and platform type.

### `agentfox/nightshift/engine.py`

`NightShiftEngine`: add `_check_open_prs()` as an `async def` thin dispatcher
method, following the same pattern as `_drain_issues()`. Sequentially
`await`s each `process_pr_issue()` call; invoked directly by the event loop
via `EngineWorkStream`. `_MAX_PR_CHECKS = 5` is defined as a module-level
constant in this file, adjacent to `_check_open_prs()`.

### `agentfox/nightshift/daemon.py`

`DaemonRunner`: add `"pr-feedback"` to the priority order list, after
`"fix-pipeline"`.

### `agentfox/nightshift/pr_feedback.py`

New module — all PR check flow and feedback re-entry logic.

## Tech Stack

- Python 3.12
- asyncio for async platform calls (`_check_open_prs` and `process_pr_issue`
  are `async def` coroutines invoked directly by the event loop)
- Pydantic v2 for config validation
- git CLI for fetch, worktree management, and force-push
- `re` module for tracking comment parsing (imported from fix_pipeline)

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 06_pr_lifecycle_labels | all | 1 | **Hard prerequisite — must be merged before spec 07 is generated.** Requires af:pr label, PrResult, PrState, CheckResult, ReviewComment types, protocol methods (get_pr_state, get_pr_checks, get_pr_reviews), tracking comment utilities |
| 02_merge_strategy | all | 1 | Assumes merge_strategy config field and PR creation path exist |

## Verified External API

### `afissues` (local, v4.2.6)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `PlatformProtocol.get_pr_state` | `afissues.protocol` | `async (pr_number: int) -> PrState` | From spec 06 |
| `PlatformProtocol.get_pr_checks` | `afissues.protocol` | `async (pr_number: int) -> list[CheckResult]` | From spec 06 |
| `PlatformProtocol.get_pr_reviews` | `afissues.protocol` | `async (pr_number: int) -> list[ReviewComment]` | From spec 06 |
| `PlatformProtocol.list_issues_by_label` | `afissues.protocol` | `async (label: str, ...) -> list[IssueResult]` | Existing; default `sort=created, direction=asc` used for oldest-first ordering |
| `PlatformProtocol.list_issue_comments` | `afissues.protocol` | `async (issue_number: int) -> list[IssueComment]` | Existing; returns comments oldest-first (GitHub API default) |
| `PlatformProtocol.assign_label` | `afissues.protocol` | `async (issue_number: int, label: str) -> None` | Existing |
| `PlatformProtocol.remove_label` | `afissues.protocol` | `async (issue_number: int, label: str) -> None` | Existing |
| `PlatformProtocol.close_issue` | `afissues.protocol` | `async (issue_number: int, comment: str \| None) -> None` | Existing |
| `PlatformProtocol.add_issue_comment` | `afissues.protocol` | `async (issue_number: int, body: str) -> None` | Existing |
| `LABEL_PR` | `afissues.labels` | `str = "af:pr"` | From spec 06 |
| `LABEL_FIXED` | `afissues.labels` | `str = "af:fixed"` | Existing |
| `LABEL_FIX` | `afissues.labels` | `str = "af:fix"` | Existing |

### `agentfox` (local, v4.2.6)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `parse_tracking_comment` | `agentfox.nightshift.fix_pipeline` | `(body: str) -> tuple[int, int] \| None` | From spec 06 |
| `format_tracking_comment` | `agentfox.nightshift.fix_pipeline` | `(pr_number, attempt, pr_url, message) -> str` | From spec 06; `pr_url` passed as `""` for feedback iteration comments |
| `PR_TRACKING_PATTERN` | `agentfox.nightshift.fix_pipeline` | `re.Pattern` | From spec 06 |
| `TriageResult` | `agentfox.nightshift.fix_pipeline` | `@dataclass(frozen=True)` | Existing — constructing synthetic instances |
| `FixPipeline._build_coder_prompt` | `agentfox.nightshift.fix_pipeline` | `(spec, triage, review_feedback, prior_context, knowledge_context) -> tuple[str, str]` | Existing; `prior_context=""` and `knowledge_context=""` passed for feedback re-entry |
| `FixPipeline._run_coder_session` | `agentfox.nightshift.fix_pipeline` | `async (workspace, spec, system_prompt, task_prompt, model_id) -> object` | Existing; model_id sourced from same nightshift config path as original fix pipeline |
| `FixPipeline._auto_commit_pending_changes` | `agentfox.nightshift.fix_pipeline` | Existing commit helper | Existing; commit message format for feedback iterations: `"fix: {issue_title} [nightshift feedback #{attempt}]"` |
| `sanitise_branch_name` | `agentfox.nightshift.spec_builder` | `(title: str, issue_number: int \| None) -> str` | Existing |
| `NightShiftEngine` | `agentfox.nightshift.engine` | Class | Adding `_check_open_prs()` as `async def` and `_MAX_PR_CHECKS = 5` constant |
| `NightShiftState` | `agentfox.nightshift.engine` | `@dataclass` | Has `issue_checks_completed` counter; no new fields added |
| `build_streams` | `agentfox.nightshift.streams` | `(config, *, engine, budget) -> list[WorkStream]` | Adding pr-feedback stream |
| `EngineWorkStream` | `agentfox.nightshift.streams` | Class | Used to wrap `_check_open_prs` |
| `DaemonRunner` | `agentfox.nightshift.daemon` | Class | Priority list updated |
| `NightShiftConfig` | `agentfox.core.config` | Pydantic model | Adding 2 fields |
| `WorkspaceConfig` | `agentfox.core.config` | Pydantic model | Reading `merge_strategy` and `integration_branch` |

## Design Decisions

1. **Synthetic TriageResult for re-entry.** Feedback iterations use a minimal
   TriageResult (issue title as summary, PR changed files as affected_files,
   empty criteria). The real guidance comes from the `review_feedback`
   parameter. This avoids storing or retrieving original triage results.

2. **`git push --force` (not `--force-with-lease`).** The previous worktree
   was destroyed, so the local ref is stale and `--force-with-lease` would
   fail. The fix branch is nightshift-owned (`fix/{N}-{slug}` naming). Human
   commits on the branch are lost — documented in consequences.

3. **Concurrent cap hardcoded at 5; constant lives in `engine.py`.** Not
   configurable. This is a rate-limiting safety measure, not a user-facing
   feature. `_MAX_PR_CHECKS = 5` is defined in `engine.py` adjacent to
   `_check_open_prs()` because it governs the dispatcher's capping behavior,
   not the feedback module's logic. Keeps the config surface small. Can be
   promoted to config later if needed.

4. **CI check interpretation.** `"failure"` and `"timed_out"` trigger
   re-entry (actionable by the coder). `"cancelled"`, `"action_required"`,
   `"stale"` are skip conditions (external factors). This keeps the coder
   focused on problems it can actually fix.

5. **Dismissed reviews are filtered out.** A `DISMISSED` review nullifies the
   previous `CHANGES_REQUESTED`, matching GitHub's own merge-blocking
   behavior. The check looks at the latest non-dismissed review.

6. **Missing tracking comment → skip, don't remove label.** Removing `af:pr`
   would permanently orphan the PR. A missing comment is likely transient.
   The issue retries on the next poll cycle. Persistent issues are surfaced
   to humans via the stale `af:pr` label.

7. **Sequential PR processing.** Issues are processed sequentially (not
   concurrently) within a single poll cycle. This simplifies error handling
   and avoids concurrent worktree conflicts. The 5-issue cap and 15-minute
   interval make parallelism unnecessary.

8. **Module composes with FixPipeline, does not subclass.** `pr_feedback.py`
   receives a `FixPipeline` reference and calls its methods directly. This
   keeps the feedback module focused and avoids complicating the inheritance
   hierarchy.

9. **New PR for re-opened issues.** If a PR is closed without merge and the
   issue is re-triaged by the fix pipeline (which could happen since `af:pr`
   is removed), a completely new fix cycle starts — new branch, new PR, new
   tracking comment with `attempt=1`.

10. **Error handling: skip and retry, no comment spam.** Both platform API
    errors and feedback iteration failures (fetch, worktree, force-push,
    tracking comment) result in a log entry and a skip to the next cycle, with
    labels left intact. This is consistent across all failure modes. Operators
    diagnose persistent issues via stale `af:pr` labels, not comment noise.

11. **Oldest-first issue ordering prevents starvation.** `list_issues_by_label`
    is called with `sort=created, direction=asc`. When the 5-issue cap is
    reached, newer issues are deferred — older PRs are never starved.

12. **`max_pr_retries=0` disables feedback entirely.** With `max_pr_retries=0`,
    the guard `attempt > 0` triggers immediately on the first feedback event
    (when `attempt=1`), so no feedback iterations ever execute. This is the
    documented and intended behaviour for teams that want monitoring-only
    (merged/closed detection) without automated re-coding.

13. **`pr_url` is empty string in feedback iteration tracking comments.**
    `format_tracking_comment` accepts `pr_url=""` for feedback iteration
    comments. The `pr_url` parameter is only meaningful in the initial
    tracking comment posted by spec 06. Feedback iteration messages are
    self-descriptive without the URL.

14. **No-change result consumes a retry.** If the coder produces no diff,
    nightshift skips the force-push, posts a no-changes comment, and the
    attempt counter was already incremented (by the tracking comment posted
    before the force-push in step 5). A no-change result means the coder
    cannot address the feedback, so consuming the retry prevents indefinite
    polling. Operators are notified via the issue comment.

15. **Model ID for feedback iterations is inherited from nightshift config.**
    No per-iteration model config is needed. The model ID is read from the
    same nightshift model configuration path used by `_run_coder_session`
    in the original fix pipeline.

16. **`affected_files` uses `git diff --name-only` against integration branch.**
    The exact command is `git diff --name-only <integration_branch> <fix_branch>`
    run inside the feedback worktree. The integration branch name is read from
    `config.workspace.integration_branch`. If the command fails or returns
    empty output, `affected_files` defaults to `[]` and a WARNING is logged.

17. **Observability via structured log lines, no new state counters.**
    The PR feedback loop emits INFO/WARNING/ERROR/DEBUG log lines for all
    state transitions and failure modes (see Section 6). No new fields are
    added to `NightShiftState`. Operators can monitor loop health by filtering
    logs on issue/PR numbers.

18. **Label exclusivity eliminates fix-pipeline double-processing risk.**
    `af:fix` and `af:pr` are mutually exclusive. The fix pipeline's
    `list_issues_by_label(LABEL_FIX)` cannot select an issue carrying only
    `af:pr`. No additional guard code is required.

19. **`prior_context` and `knowledge_context` are empty strings for feedback
    re-entry.** Feedback iterations are targeted fix runs — the actionable
    signal comes from `review_feedback` and the original issue body in the
    synthetic `TriageResult`. Prior attempt context and knowledge retrieval
    add complexity without clear value for a focused re-run.

20. **Feedback worktree path follows fix pipeline convention with `feedback-`
    prefix.** The worktree is created at `worktrees/feedback-{issue_number}`,
    co-located with the fix pipeline's worktrees. `_cleanup_feedback_worktree()`
    silently no-ops (at DEBUG level) if the directory does not exist, preventing
    secondary exceptions in `finally` blocks from masking the original failure.

21. **Label transition atomicity via idempotent retry.** The three operations
    for merged-PR handling (`assign_label(af:fixed)`, `remove_label(af:pr)`,
    `close_issue`) are applied sequentially with no rollback on partial failure.
    All three are idempotent at the GitHub API level. If a mid-sequence failure
    occurs, the issue is skipped for the current cycle and all three operations
    are re-applied on the next poll cycle — already-completed steps are no-ops.

22. **Fix branch not found on origin is a standard worktree error.** If a
    human deletes the fix branch, `git fetch` or `git worktree add` fails and
    the error is treated identically to any other fetch/worktree creation
    failure: ERROR logged, issue skipped, labels intact,
    `_cleanup_feedback_worktree()` no-ops in `finally`. No special-casing
    is needed.

23. **Most recent tracking comment selected by last-in-list position.**
    `list_issue_comments` returns comments oldest-first (GitHub API default).
    The most recent tracking comment matching `PR_TRACKING_PATTERN` is the
    last matching item in the returned list. This is a single-pass O(n)
    operation and correct given the API ordering guarantee.

24. **CI and review re-entry are strictly sequential and mutually exclusive.**
    CI failure in step 3 triggers re-entry immediately; step 4 (review check)
    is only reached if all CI checks pass or no CI is configured.
    `_collect_feedback()` therefore always receives a single trigger source
    and formats a single markdown section. The `trigger` parameter
    (`Literal["ci", "review"]`) makes the intent explicit and prevents
    accidental dual-section output.

25. **`git fetch` before `git worktree add`.** After the original fix-pipeline
    worktree is destroyed, the local clone may have a stale or missing ref for
    the fix branch. `_setup_feedback_worktree()` runs `git fetch origin
    <branch>` before `git worktree add` to ensure the ref is current. Fetch
    failure is treated as a worktree creation error (ERROR logged, issue
    skipped, labels intact) — consistent with general feedback iteration error
    handling.

26. **Tracking comment posted before force-push.** Persisting the incremented
    attempt counter before pushing changes ensures correctness on failure: if
    the comment fails, the push is skipped and the old counter is retried next
    cycle; if the push fails after the comment succeeds, the counter is already
    incremented (conservative but safe — the next cycle retries or hits the
    limit). This ordering prevents indefinite re-triggering caused by a lost
    retry.

27. **Commit message format for feedback iterations.**
    `_auto_commit_pending_changes()` uses `"fix: {issue_title} [nightshift
    feedback #{attempt}]"` for feedback iteration commits, making them
    distinguishable from the original fix commit in git log history.

28. **`_collect_feedback()` signature is explicit about trigger source.**
    `_collect_feedback(trigger: Literal["ci", "review"], ci_failures:
    list[CheckResult], review_comments: list[ReviewComment]) -> str` returns
    a markdown string. The `trigger` parameter encodes the mutually exclusive
    source, so the function always formats exactly one section and never
    combines CI and review feedback in a single call.

## Testing Strategy

`spec generate` will produce a `test_spec.json` with detailed test contracts
covering the following scenarios. Full coverage of all CI-check interpretation
branches and error paths is expected.

### PR state handling
- Merged PR: `af:fixed` label added, `af:pr` removed, issue closed with comment. INFO logged.
- Closed-without-merge PR: `af:pr` removed, issue left open, comment posted. INFO logged.
- Open PR: falls through to CI and review checks.
- Mid-sequence platform API failure during merged PR handling (e.g., `close_issue` raises after `assign_label` succeeds): issue skipped at WARNING, retried next cycle; idempotent re-application confirmed.

### CI check interpretation (all 7 branches)
1. At least one check `status == "in_progress"` or `"queued"` → skip.
2. At least one check `conclusion == "failure"` → enter re-entry. INFO logged.
3. At least one check `conclusion == "timed_out"` → enter re-entry. INFO logged.
4. All checks `conclusion == "cancelled"` / `"action_required"` / `"stale"`,
   none `"success"` → skip. WARNING logged.
5. All checks `conclusion == "success"` → proceed to review check.
6. Mixed: some success, one failure → re-entry triggered.
7. Empty check list → proceed to review check (no CI configured).

### Review filtering
- Latest non-dismissed review is `CHANGES_REQUESTED` → re-entry. INFO logged.
- Latest non-dismissed review is `APPROVED` → skip (healthy PR).
- Latest non-dismissed review is `COMMENTED` → skip.
- All reviews `DISMISSED` → no active review, skip.
- No reviews → skip.

### Mutually exclusive CI/review re-entry paths
- CI failure present AND review `CHANGES_REQUESTED`: re-entry triggered by CI
  in step 3; step 4 never reached. `_collect_feedback()` called with
  `trigger="ci"` only.
- All CI checks pass AND review `CHANGES_REQUESTED`: re-entry triggered by
  review in step 4. `_collect_feedback()` called with `trigger="review"` only.

### Retry limit enforcement
- `attempt=1`, `max_pr_retries=2` → re-entry runs.
- `attempt=2`, `max_pr_retries=2` → re-entry runs.
- `attempt=3`, `max_pr_retries=2` → stop, post limit-reached comment. INFO logged.
- `attempt=1`, `max_pr_retries=0` → stop immediately, no re-entry.

### Tracking comment parsing edge cases
- No tracking comment found → WARNING logged, skip, leave `af:pr`.
- Malformed tracking comment → WARNING logged, skip, leave `af:pr`.
- Multiple tracking comments → last item in list order is used (oldest-first API ordering).

### No-change result handling
- Coder produces empty diff → force-push skipped, no-changes comment posted,
  attempt counter already incremented (tracking comment was posted before
  diff check), WARNING logged.
- Coder produces changes → force-push executed, commit message includes
  `[nightshift feedback #{attempt}]`.

### Fetch and worktree lifecycle
- `git fetch origin <branch>` succeeds, worktree created at
  `worktrees/feedback-{issue_number}`, coder run, tracking comment posted,
  force-push, cleanup → iteration complete.
- `git fetch origin <branch>` fails (network error, branch not found on origin)
  → ERROR logged, issue skipped, labels intact,
  `_cleanup_feedback_worktree()` no-ops silently (DEBUG logged).
- `git worktree add` failure (disk space, branch conflict) after successful
  fetch → ERROR logged, issue skipped, labels intact,
  `_cleanup_feedback_worktree()` no-ops silently (DEBUG logged).
- Force-push failure → ERROR logged, issue skipped, labels intact, worktree
  cleaned up via `finally`.
- `_cleanup_feedback_worktree()` called when directory does not exist → silent
  no-op, DEBUG log emitted, no exception raised.

### Tracking comment failure handling
- `add_issue_comment()` raises during step 5 (tracking comment post) →
  ERROR logged, force-push skipped, worktree cleaned up, issue skipped; old
  attempt counter persists; next cycle re-triggers with correct counter.
- Tracking comment succeeds, force-push subsequently fails → incremented
  counter already persisted; ERROR logged; next cycle retries or hits limit.

### Commit message format
- Feedback iteration commit message verified as
  `"fix: {issue_title} [nightshift feedback #{attempt}]"`.

### `affected_files` computation
- `git diff --name-only` returns a list of files → `affected_files` populated.
- `git diff --name-only` fails or returns empty → `affected_files = []`,
  WARNING logged.

### Coder prompt construction
- `_build_coder_prompt()` called with `prior_context=""` and
  `knowledge_context=""` during feedback re-entry — confirmed by inspecting
  call arguments in mock.

### `_collect_feedback()` output
- `trigger="ci"`: output contains `## CI Failures` section, no `## Review
  Feedback` section.
- `trigger="review"`: output contains `## Review Feedback` section, no
  `## CI Failures` section.

### Error paths
- Platform API raises exception during `get_pr_state` → WARNING logged, skip,
  no comment posted.
- Platform API raises exception during `get_pr_checks` → WARNING logged, skip.
- Platform API raises exception during `get_pr_reviews` → WARNING logged, skip.
- Platform API raises exception mid-sequence during merged PR transition →
  WARNING logged, skip, idempotent retry on next cycle.
- Coder session raises exception during feedback iteration → ERROR logged,
  skip, labels intact, worktree cleaned up.

### Async execution model
- `_check_open_prs` is `async def`; `process_pr_issue` calls are sequentially
  awaited (not gathered). Verified by confirming no concurrent worktree
  creation occurs across issues in a single poll cycle.
