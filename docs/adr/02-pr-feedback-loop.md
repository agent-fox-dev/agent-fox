# ADR 02: PR Feedback Loop for Nightshift

## Status

Proposed

## Context

When `workspace.merge_strategy` is `"pr"`, nightshift pushes the fix
branch, creates a pull request, and immediately closes the issue with
`af:fixed`.  The PR has not been reviewed or passed CI at this point —
the close is premature.  Worse, there is no mechanism to monitor the
PR afterward: if CI fails or a reviewer requests changes, the fix
stalls until a human notices.

The timing gap is significant.  CI pipelines commonly take 10–30
minutes; human review may take hours.  Nightshift has destroyed the
worktree and moved on to other issues long before feedback arrives.

The platform protocol (`PlatformProtocol`) has no methods for querying
PR state, CI check results, or review comments.  The only PR-related
method is `create_pr()`, which is fire-and-forget.

## Decision

### 1. New label: `af:pr`

Add `LABEL_PR = "af:pr"` to `afissues.labels` with a `LabelSpec` in
`REQUIRED_LABELS`.

Issue label lifecycle when `merge_strategy` is `"pr"`:

```
af:fix  ──PR created──►  af:pr  ──PR merged──►  af:fixed (closed)
                            │
                            ├── CI fails ──► feedback re-entry (stays af:pr)
                            ├── changes requested ──► feedback re-entry (stays af:pr)
                            ├── retries exhausted ──► comment for human (stays af:pr)
                            └── PR closed w/o merge ──► remove af:pr (stays open)
```

### 2. Fix the premature-close bug

`_integrate_fix()` in `fix_pipeline.py` currently returns `"merged"`
for PR mode, which causes `_handle_result()` to close the issue.

Change the return value to `"pr_created"` and include the PR number.
`_handle_result()` handles the new status by:

- Adding `af:pr`, removing `af:fix`.
- Posting a structured tracking comment (see section 7).
- Leaving the issue open.

This requires `create_pr()` to return a `PrResult(html_url, number)`
dataclass instead of a bare URL string.

### 3. Platform protocol extensions

Add three methods to `PlatformProtocol`:

| Method | Returns | Purpose |
|--------|---------|---------|
| `get_pr_state(pr_number)` | `PrState` | open/closed/merged status and head SHA |
| `get_pr_checks(pr_number)` | `list[CheckResult]` | CI check-run results |
| `get_pr_reviews(pr_number)` | `list[ReviewComment]` | Review verdicts and comments |

New frozen dataclasses in `afissues.protocol`:

- **`PrResult`** — `html_url: str`, `number: int`
- **`PrState`** — `number: int`, `state: str`, `merged: bool`,
  `head_sha: str`
- **`CheckResult`** — `name: str`, `status: str`, `conclusion: str | None`,
  `output_title: str`, `output_summary: str`
- **`ReviewComment`** — `user: str`, `state: str`, `body: str`,
  `submitted_at: str`

GitHub API mappings:

- `get_pr_state` → `GET /repos/{owner}/{repo}/pulls/{n}`
- `get_pr_checks` → `GET /repos/{owner}/{repo}/commits/{sha}/check-runs`
  (head SHA from `get_pr_state`)
- `get_pr_reviews` → `GET /repos/{owner}/{repo}/pulls/{n}/reviews`

`NullPlatform` returns empty lists.  GitLab and Gitea implementations
may initially raise `NotImplementedError`; full support is a separate
follow-up.

### 4. PR feedback work stream

Add a `"pr-feedback"` entry in `build_streams()` wrapping a new
engine method `_check_open_prs()`.

- **Polling interval:** `night_shift.pr_check_interval` (default 900 s,
  minimum 60 s).
- **Enabled when:** `merge_strategy == "pr"` and platform type is not
  `"none"`.
- **Priority:** lower than `"fix-pipeline"` — fix new issues first,
  then check existing PRs.

### 5. PR check flow (`_check_open_prs`)

For each open issue labeled `af:pr`:

1. Parse the PR number from the structured tracking comment.
2. `get_pr_state()`:
   - **Merged** → add `af:fixed`, remove `af:pr`, close issue.  Done.
   - **Closed without merge** → post comment, remove `af:pr`, leave
     open for human triage.  Done.
   - **Open** → continue.
3. `get_pr_checks()`:
   - Any check `in_progress` or `queued` → skip, wait for next poll.
   - Any check with `conclusion == "failure"` → enter feedback
     re-entry.
   - All checks pass → check reviews.
4. `get_pr_reviews()`:
   - Latest review is `CHANGES_REQUESTED` → enter feedback re-entry.
   - Otherwise → skip, PR is healthy and waiting for merge.

### 6. Feedback re-entry

When CI fails or a reviewer requests changes:

1. Parse the attempt count from the tracking comment.  If
   `attempt > max_pr_retries` (default 2), post a comment requesting
   manual intervention and stop.  With the default, nightshift makes
   one original attempt plus up to 2 feedback iterations — 3 total
   pushes to the PR at most.
2. Collect feedback: failed check names, output summaries, and/or
   review comments.  Format as markdown.
3. Create a worktree from the **existing fix branch** (not from the
   integration branch).
4. Run a coder session with the feedback injected via the same
   `review_feedback` parameter path used by the coder-reviewer loop.
   No triage — the issue was already triaged on the first pass.
5. Auto-commit and force-push the fix branch.  The PR updates
   automatically.
6. Post an updated tracking comment with the attempt count
   incremented.
7. Destroy the worktree.  The next poll cycle will re-check CI.

Extract this logic into a new module
`agentfox.nightshift.pr_feedback` to keep `engine.py` focused on
issue dispatch.

### 7. Structured tracking comment

Machine-readable comment posted on the issue after PR creation and
after each feedback iteration:

```
<!-- af:pr-tracking pr_number=42 attempt=1 -->
Pull request created: https://github.com/owner/repo/pull/42
```

On retry:

```
<!-- af:pr-tracking pr_number=42 attempt=2 -->
CI feedback iteration 2: re-ran coder with failure context.
```

The PR number and attempt count are parsed from the most recent
matching comment via regex.

### 8. Configuration

Add to `NightShiftConfig`:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pr_check_interval` | `int` | `900` | Seconds between PR status polls (min 60) |
| `max_pr_retries` | `int` | `2` | Max feedback iterations per PR (3 total pushes) |

## Consequences

- **Closes the feedback loop:** nightshift can iterate on a PR based
  on CI failures and review comments instead of fire-and-forget.
- **Fixes the premature-close bug:** issues with open PRs stay open
  until the PR actually merges.
- **`af:pr` label** provides clear lifecycle tracking and prevents the
  fix pipeline from re-processing issues that already have PRs.
- **Force-push** rewrites the fix branch.  If a human pushes commits
  to the branch between polls, those commits are lost.  This is
  acceptable for automated fix branches but should be documented.
- **Comment-based state** is resilient to daemon restarts (no new
  storage required) but fragile if comments are edited or deleted
  externally.  A DuckDB-backed alternative is a future option.
- **Protocol surface grows** by three methods and four dataclasses.
  GitLab and Gitea implementations can be deferred behind
  `NotImplementedError`.
- **No impact on `"direct"` or `"branch"` strategies** — the
  `pr-feedback` stream is disabled unless `merge_strategy` is `"pr"`.

## Resolved Questions

- **Reviewer before force-push?** No.  CI serves as the quality gate
  for feedback iterations; running the internal reviewer would
  duplicate effort.
- **Auto-merge on approval?** No.  Merging remains a human decision.
  Auto-merge may be revisited as a separate opt-in feature.
- **Rate limiting?** Cap concurrent PR checks at 5 per poll cycle
  (default).  Issues beyond the cap are deferred to the next cycle.
  This keeps API usage well within GitHub's 5 000 req/hour limit
  even at the shortest poll interval.
