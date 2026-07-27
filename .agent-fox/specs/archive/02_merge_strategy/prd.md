---
spec_id: '02'
spec_name: merge_strategy
title: Configurable Merge Strategy
status: draft
created_at: '2026-07-13T16:36:26.622884+00:00'
updated_at: '2026-07-13T16:48:19.578701+00:00'
owner: Michael Kuehl
source: interactive
schema_version: 1
---
# Configurable Merge Strategy

## Intent

Enable teams using agent-fox to adopt code-review workflows by making the post-session merge behavior configurable, so that both solo developers and team environments can use agent-fox without being locked into a single integration pattern.

## Background

agent-fox currently hardcodes a single integration workflow: after a coding session completes, the feature branch is squash-merged directly into the integration branch (default: `main`) and pushed to origin. This works well for solo developers but does not fit teams that require code review via pull requests before merging, or developers who want to inspect feature branches before integrating them.

The `create_pr()` method was previously part of `PlatformProtocol` but was removed in a legacy internal spec (predating the current spec system; requirements 65-REQ-4.1 through 65-REQ-4.3 are referenced in existing code). This spec re-introduces `create_pr()` as a first-class protocol method to support the new `"pr"` merge strategy mode.

## Goals

1. Users can configure `workspace.merge_strategy` without errors; Pydantic validation accepts all three valid values (`"direct"`, `"branch"`, `"pr"`) and rejects invalid ones.
2. All three modes produce the correct Git/GitHub state, verified by automated tests covering the full range of mode × pipeline combinations (`af code` and `nightshift`). Test design is fully delegated to the implementor; the spec generation step will produce detailed test contracts.
3. Zero regressions in `"direct"` mode behavior for existing users — existing workflows are unaffected by the introduction of this field.

## Non-Goals

- **GitLab / Bitbucket PR creation** — Only GitHub is supported. Other platforms are out of scope.
- **Auto-merging of PRs** — The feature creates PRs but does not automatically merge them.
- **Branch protection rule enforcement** — The feature does not inspect or enforce repository branch protection settings.
- **Multiple remote support** — Only the configured `origin` remote is targeted.
- **Config file migration tooling** — No migration script or upgrade path is required; backward compatibility is handled via Pydantic defaults (see Configuration section).

## Solution

Add a `workspace.merge_strategy` configuration field with three modes:

- **`"direct"`** (default) — Current behavior. Squash-merge the feature branch into the integration branch and push.
- **`"branch"`** — Keep the feature branch locally without merging. The user can inspect, push, or create a PR manually.
- **`"pr"`** — Push the feature branch to origin and open a GitHub Pull Request against the integration branch.

The setting is respected by both `af code` (session lifecycle) and `nightshift` (fix pipeline).

## Behavior

### `"direct"` mode (default)

No change from current behavior. Feature branches are squash-merged into the integration branch and pushed to origin.

### `"branch"` mode

- After a successful coding session, the feature branch is left as-is.
- No squash-merge into the integration branch.
- The feature branch stays local-only (not pushed to origin).
- **For `af code` sessions:** An `INFO` log line is emitted (`Merge strategy is 'branch' — feature branch '{branch_name}' kept locally.`) and a CLI summary is printed to stdout with the branch name. This branch name is included in the standard session summary that `af code` already prints. The session exits with code `0`.
- **For nightshift:** The originating issue is NOT closed. A comment is added to the issue with the following exact content:

  ```
  Fix branch created: `{branch_name}`. Merge strategy is set to `branch` — please review and merge manually.
  ```

### `"pr"` mode

- After a successful coding session, the feature branch is pushed to origin.
- A GitHub Pull Request is created against the integration branch using `platform.create_pr()`.
- **PR title format:**
  - For `af code` sessions: `{spec_name}: {task_group_title}` — e.g., `merge_strategy: Config and validation`
  - For `nightshift` fix sessions: `Fix #{issue_number}: {issue_title}` — e.g., `Fix #42: Login fails on empty password`
- **PR body:** auto-generated via `build_pr_body()` using the template defined in the [PR Body Template](#pr-body-template) section below. The PR title and body are constructed by the caller (session lifecycle or fix pipeline) and passed explicitly as parameters to `create_pr()` and `build_pr_body()`. See [af code Session Context](#af-code-session-context) for the source of title/body field values.
- **On success:** The PR URL is included in the existing session summary output that `af code` already prints (for interactive sessions). An `INFO` log line is also emitted. The session exits with code `0`.
- For nightshift: the originating issue is left open. The PR body includes `Fixes #N` so GitHub auto-closes the issue when the PR is merged.
- Requires `platform.type = "github"` and `GITHUB_PAT` to be configured. Both are validated lazily at the point of PR creation (not at startup or session start). If either is absent, the system falls back to `"branch"` mode with a warning (see Fallback & Error Handling).

### Zero-Change Sessions

Sessions that produce no changed files are short-circuited upstream before reaching the merge strategy logic. `harvest()` returns an empty list when there are no new commits, and the caller already exits the pipeline before PR creation is attempted. The merge strategy code therefore never needs to handle a zero-changed-files case.

If `get_changed_files()` returns an empty list despite this upstream short-circuit (e.g., due to a git diff edge case or branch state anomaly), the system proceeds normally. An empty **Changed Files** section in the PR body is acceptable and does not warrant special handling.

### PR Body Template

`build_pr_body()` in `fix_pipeline.py` is updated to produce the following Markdown template. Fields and sections are conditional as noted.

#### Concrete Function Signature

```python
def build_pr_body(
    *,
    spec_name: str | None = None,
    task_group_id: str | None = None,
    task_group_title: str | None = None,
    changed_files: list[str],
    issue_number: int | None = None,
    issue_title: str | None = None,
) -> str:
```

All callers in `session_lifecycle.py` and `fix_pipeline.py` must use this signature. The function is a pure rendering function with no dependency on session internals. Parameter semantics:

- **`spec_name`** — Used in the Summary section for `af code` sessions. `None` for nightshift fix sessions.
- **`task_group_id`** / **`task_group_title`** — Used to render the Task Group section. Both must be non-`None` together for `af code` sessions; both are `None` for nightshift fix sessions (section is omitted).
- **`changed_files`** — The list of changed file paths. May be empty (produces an empty Changed Files section).
- **`issue_number`** / **`issue_title`** — Used in the Summary section and `Fixes #N` line for nightshift fix sessions. Both must be non-`None` together for nightshift; both are `None` for `af code` sessions.

The caller is responsible for ensuring consistent parameter sets. If parameters are in an unexpected combination (e.g., `spec_name` and `issue_number` both provided), the function behavior is implementation-defined — the spec does not define a conflict-resolution rule for this case, as it represents a caller-side programming error.

#### Template

```markdown
## Summary

{spec_name}
```
*(For nightshift fix sessions, replace `{spec_name}` with `Fix #{issue_number}: {issue_title}`)*

```markdown
## Task Group

{task_group_id}: {task_group_title}
```
*(The **Task Group** section is included only for `af code` sessions; omitted for nightshift fix sessions.)*

```markdown
## Changed Files

- {file1}
- {file2}
- ...

Fixes #{N}
```
*(The `Fixes #N` line is included only for nightshift fix sessions that originate from an issue.)*

**Full example — `af code` session:**
```markdown
## Summary

merge_strategy

## Task Group

task-001: Config and validation

## Changed Files

- config.py
- config_gen.py
```

**Full example — nightshift fix session:**
```markdown
## Summary

Fix #42: Login fails on empty password

## Changed Files

- auth/login.py
- tests/test_login.py

Fixes #42
```

### af code Session Context

For `af code` sessions, the PR title and body fields are sourced as follows and passed **explicitly as parameters** to `build_pr_body()` and `create_pr()` by the session lifecycle caller:

- **`spec_name`** — from the spec metadata available in the session context.
- **`task_group_id`** and **`task_group_title`** — from the node ID (which encodes `spec:group`) and spec metadata available in the session context.

The session lifecycle and fix pipeline callers are responsible for constructing the title and body before invoking `create_pr()`. There is no fallback for absent values — if these fields are unavailable, it indicates a caller-side programming error.

### Changed Files Source

The **Changed Files** list in the PR body is derived at PR creation time by calling the existing `get_changed_files(repo_root, feature_branch, integration_branch)` function from the `workspace` module. This computes the diff between the feature branch and the integration branch — the same approach used by `harvest()` internally. This call is made in both `"branch"` and `"pr"` modes to populate `touched_files` in the return tuple.

### Fallback & Error Handling

**Platform availability guard:** The guard uses `create_platform_safe()` from the existing `platform_factory` module. It returns `None` when `GITHUB_PAT` is missing or `platform.type` is not `"github"`. The guard is implemented as an inline check in the calling code:

```python
platform = create_platform_safe(config, project_root)
if platform is None:
    # log WARNING and fall back to "branch" mode
```

This check occurs before any branch push is attempted.

**Unconfigured platform in `"pr"` mode:** `GITHUB_PAT` and `platform.type` are validated lazily — only at the point of PR creation, not at startup or session start. If `create_platform_safe()` returns `None`, the system falls back to `"branch"` mode and logs a warning before the branch push is attempted. No partial state is left.

**Branch push failure:** If the git push of the feature branch to origin fails (step 3 in the operation sequence below — e.g., due to network error, auth failure, or rejected push), the exception is raised and handled by the existing pipeline error handling in `session_lifecycle.py` and `fix_pipeline.py`. No special handling is added for push failures; they propagate through the standard error path.

**Partial failure — branch pushed but PR creation fails:** If the feature branch is successfully pushed to origin but the subsequent GitHub PR creation API call fails (i.e., `create_pr()` raises `IntegrationError`), the system:
1. Logs an error including the remote branch URL (e.g., `https://github.com/{owner}/{repo}/tree/{branch}`) so the user can create the PR manually.
2. Falls back to `"branch"` mode semantics for the remainder of the pipeline (i.e., no harvest/squash-merge, issue is not closed).
3. **For nightshift:** In addition to logging the error, posts the branch-mode issue comment to the originating issue (`Fix branch created: '{branch_name}'. Merge strategy is set to 'branch' — please review and merge manually.`) so the user knows where to find the branch.
4. Does not attempt to delete or roll back the remote branch.

**Nightshift job completion on partial failure:** When `"pr"` mode partially fails in nightshift (branch pushed, PR creation failed, branch-mode comment posted), the nightshift job is marked as complete (success). No retry is triggered. The branch is preserved and the issue comment provides recovery information.

**Duplicate PR (HTTP 422 — PR already exists):** If GitHub returns a `422` response with an error message indicating a PR already exists for the head branch, `create_pr()` treats this as a success. It queries the existing PR URL (e.g., via `GET /repos/{owner}/{repo}/pulls?head={head}&base={base}`) and returns it. This mirrors the idempotent handling pattern already established by `create_label()` for `422 already_exists` responses. All other `422` responses (and all other non-201 responses not matching the duplicate-PR condition) raise `IntegrationError`.

**No retry behavior:** `create_pr()` makes a single attempt only (aside from the idempotent duplicate-PR lookup). Failures are handled by the partial-failure fallback above.

**Operation sequence for `"pr"` mode (explicit ordering):**
1. Call `create_platform_safe(config, project_root)`.
2. If `None` → log `WARNING` → fall back to `"branch"` mode. *(No branch push has occurred.)*
3. If platform is available → push feature branch to origin. *(If push fails, raise exception and let the existing pipeline error handling deal with it.)*
4. Call `get_changed_files(repo_root, feature_branch, integration_branch)` to build the Changed Files list.
5. Call `build_pr_body()` with all required parameters.
6. Call `platform.create_pr(title=..., body=..., head=..., base=...)`.
7. If `create_pr()` raises `IntegrationError` → log `ERROR` with remote branch URL → for nightshift, also post branch-mode issue comment → fall back to `"branch"` mode semantics. *(Nightshift job is still marked complete; no retry triggered.)*
8. On success → log `INFO` with PR HTML URL → include PR URL in session summary output.

## Configuration

```toml
[workspace]
merge_strategy = "direct"  # "direct" | "branch" | "pr"
```

### Pydantic Field Definition

The field is added to `WorkspaceConfig` in `config.py` with the following exact definition:

```python
merge_strategy: Literal['direct', 'branch', 'pr'] = 'direct'
```

This is placed alongside `integration_branch` and `force_clean` in the `WorkspaceConfig` model.

**Backward compatibility:** If the field is absent from an existing config file, Pydantic's default value mechanism silently defaults to `"direct"`. No migration script or special handling is required. This is standard Pydantic v2 behavior.

The field is added to `_VISIBLE_SECTIONS` (via the `workspace` section) and `_PROMOTED_DEFAULTS` in `config_gen.py` so it appears in the generated config template. The exact key names and placement within those structures follow the same pattern used for `integration_branch` and `force_clean` in that file.

## Return Value Contract

`_harvest_and_integrate()` (in `session_lifecycle.py`) and `_integrate_fix()` (in `fix_pipeline.py`) preserve the **existing return type** across all three modes:

```python
tuple[str, str | None, list[str], bool]
# (status, error_message, touched_files, is_non_retryable)
```

Behavior per mode:

| Mode | `status` | `error_message` | `touched_files` | `is_non_retryable` |
|---|---|---|---|---|
| `"direct"` | `'completed'` | `None` | from `harvest()` | `False` |
| `"branch"` | `'completed'` | `None` | from `get_changed_files()` | `False` |
| `"pr"` (success) | `'completed'` | `None` | from `get_changed_files()` | `False` |
| `"pr"` (partial failure) | `'completed'` | `None` | from `get_changed_files()` | `False` |

**Note:** In `"pr"` mode, the PR URL is logged at `INFO` level and included in the session summary output; it is **not** returned in the tuple. The return type is unchanged to avoid breaking callers.

**Note:** In `"branch"` and `"pr"` modes, `get_changed_files(repo_root, feature_branch, integration_branch)` is always called to populate `touched_files`, regardless of whether the list is empty. An empty result is returned as-is.

## Tech Stack

- Python 3.12
- Pydantic v2 for config validation
- `aiohttp` for GitHub REST API calls (existing `GitHubPlatform`)
- Click for CLI framework

## Integration Points

### Session lifecycle (`session_lifecycle.py`)

`_harvest_and_integrate()` currently always calls `harvest()`. With this change:

- `"direct"`: calls `harvest()` as before.
- `"branch"`: skips `harvest()` entirely, returns the feature branch name and changed files (from `get_changed_files()`).
- `"pr"`: skips `harvest()`, pushes the feature branch to origin, calls `platform.create_pr()`, returns the PR URL and changed files.

### Fix pipeline (`fix_pipeline.py`)

`_integrate_fix()` currently always calls `_harvest_and_push()`. The same three-way branching applies. Additionally:

- `"direct"`: closes the issue with a comment (current behavior).
- `"branch"`: adds the branch comment (see `"branch"` mode above), does NOT close the issue.
- `"pr"`: creates a PR with `Fixes #N` in the body, does NOT close the issue. On partial failure (branch pushed, PR creation fails): logs the error AND posts the branch-mode issue comment.

`build_pr_body()` in this file is updated to implement the PR Body Template and concrete signature defined above.

### Platform protocol (`protocol.py`)

Re-add `create_pr()` to `PlatformProtocol` (previously removed in legacy spec 65; requirements 65-REQ-4.1 through 65-REQ-4.3). The method signature is:

```python
async def create_pr(
    self,
    *,
    title: str,
    body: str,
    head: str,
    base: str,
) -> str:
    """Create a pull request and return its HTML URL.

    Raises:
        IntegrationError: On API failure (non-201 response from GitHub,
            excluding 422 'PR already exists' which is handled as success).
    """
```

The method is only called when `merge_strategy = "pr"` and platform availability has been confirmed by the `create_platform_safe()` guard.

### GitHub platform (`github.py`)

Implement `create_pr()` using `POST /repos/{owner}/{repo}/pulls`. The implementation:
- Uses the existing `aiohttp` session in `GitHubPlatform`.
- Makes a single attempt (no retry).
- On HTTP `201`: returns the `html_url` field from the GitHub API response body.
- On HTTP `422` with an error message indicating a PR already exists for the head branch: queries the existing PR URL via `GET /repos/{owner}/{repo}/pulls?head={head}&base={base}` and returns it (idempotent behavior, matching `create_label()` pattern).
- Raises `IntegrationError` on any other non-201 HTTP response.

### NullPlatform / non-GitHub platforms (`protocol.py` stub)

`NullPlatform` (and any other non-GitHub platform stub) implements `create_pr()` by raising `NotImplementedError`. This path should never be reached in practice because `create_pr()` is always preceded by the `create_platform_safe()` platform availability check, which returns `None` for any non-GitHub or unconfigured platform, causing the caller to fall back before `create_pr()` is ever invoked.

```python
async def create_pr(self, *, title: str, body: str, head: str, base: str) -> str:
    raise NotImplementedError(
        "create_pr() called on NullPlatform — this should never be reached. "
        "Ensure platform availability is checked via create_platform_safe() before calling create_pr()."
    )
```

## Observability

During normal (non-error) operation, the following log output is emitted so users can observe which merge strategy is active:

- **`"direct"` mode:** No additional log output beyond existing harvest/merge log lines.
- **`"branch"` mode:** Log line at `INFO` level: `Merge strategy is 'branch' — feature branch '{branch_name}' kept locally.` Additionally, for interactive `af code` sessions, the branch name is included in the standard session summary printed to stdout. Exit code is `0`.
- **`"pr"` mode (success):** Log line at `INFO` level: `Pull request created: {pr_html_url}`. The PR URL is also included in the existing session summary output printed to stdout for `af code` sessions. Exit code is `0`.
- **`"pr"` mode (fallback due to missing platform):** Log line at `WARNING` level: `Merge strategy is 'pr' but platform is not configured — falling back to 'branch' mode.`
- **`"pr"` mode (partial failure — push succeeded, PR creation failed):** Log line at `ERROR` level: `PR creation failed. Branch available at: https://github.com/{owner}/{repo}/tree/{branch_name}`. For nightshift, the branch-mode issue comment is also posted.

PR creation failures (partial failure) do **not** change the exit code from `0`; the session is still considered complete. For nightshift, the job is marked complete with no retry triggered. The error is surfaced via the log line, the fallback to `"branch"` mode semantics, and (for nightshift) the branch-mode issue comment.

## Design Decisions

1. **Branch mode is local-only.** The feature branch is not pushed to origin. Users who want to push can do so manually. This keeps the mode simple and avoids requiring platform credentials.

2. **Nightshift respects the setting.** Both `af code` and `nightshift` honor `workspace.merge_strategy`. This avoids surprising behavior where the daemon uses a different workflow than manual runs.

3. **Issue stays open in PR mode.** When nightshift creates a PR, the originating issue is left open. The PR body includes `Fixes #N` so GitHub closes the issue automatically when the PR is merged. This preserves the standard GitHub audit trail.

4. **PR body is auto-generated.** `build_pr_body()` in `fix_pipeline.py` is updated to produce a consistent Markdown body using the template defined in this spec. The Task Group section is present only for `af code` sessions; the `Fixes #N` line is present only for nightshift fix sessions originating from an issue.

5. **Fallback for unconfigured platform.** In `"pr"` mode, `create_platform_safe()` is called before any branch push is attempted. If it returns `None`, the system falls back to `"branch"` mode and logs a warning. This ensures no partial state is created.

6. **Partial push failure is non-destructive.** If the branch is pushed but PR creation fails, the remote branch is intentionally left in place. Rolling it back could cause data loss, and the user can recover by creating the PR manually using the logged URL. For nightshift, the branch-mode issue comment is additionally posted so the user knows where to find the branch.

7. **No config migration needed.** Pydantic's default value handling silently supplies `"direct"` for any existing config file that omits `merge_strategy`. This is consistent with how `integration_branch` and `force_clean` were introduced.

8. **Single-attempt API calls.** `create_pr()` makes one attempt only. There is no retry logic. Failures are handled by the existing partial-failure fallback, keeping the implementation simple.

9. **NullPlatform raises NotImplementedError.** `create_pr()` is always preceded by the `create_platform_safe()` guard, so `NullPlatform.create_pr()` raising `NotImplementedError` is a correct defensive contract violation signal, not a primary error-handling path. This also satisfies the `-> str` return type contract declared in `PlatformProtocol`.

10. **Title and body parameters are caller-constructed.** The session lifecycle and fix pipeline callers are responsible for building the PR title and body from their available context (spec metadata, task group, issue data) before calling `build_pr_body()` and `create_pr()`. This keeps `build_pr_body()` a pure rendering function with no dependency on session internals.

11. **Changed files derived from git diff.** The Changed Files list is computed at PR creation time via the existing `get_changed_files(repo_root, feature_branch, integration_branch)` function in the `workspace` module, consistent with `harvest()`'s internal approach. An empty result is acceptable and does not require special handling.

12. **Zero-change sessions are pre-empted upstream.** The merge strategy logic never needs to handle a zero-changed-files case; `harvest()` returns an empty list for sessions with no new commits and the pipeline already short-circuits before reaching PR creation. If `get_changed_files()` returns an empty list anyway (edge case), the system proceeds normally with an empty Changed Files section.

13. **Return type is unchanged.** `_harvest_and_integrate()` preserves `tuple[str, str | None, list[str], bool]` across all three modes. The PR URL is surfaced via logs and session summary output rather than the return value, avoiding any breaking change to callers.

14. **Duplicate PRs are handled idempotently.** A GitHub `422` response for an already-existing PR is treated as success by querying and returning the existing PR URL. This mirrors `create_label()`'s established pattern and makes nightshift retries safe.

15. **`build_pr_body()` signature is concrete and specified.** The function signature is fully defined in this spec (see PR Body Template section). All callers must use the specified keyword-only signature. This eliminates interface ambiguity between `session_lifecycle.py` and `fix_pipeline.py`.

16. **PR creation failure does not affect exit code.** A failed PR creation (partial failure) results in `"branch"` mode semantics and an error log, but the session still exits with code `0`. For nightshift, the job is marked complete with no retry. The branch is preserved and accessible.

17. **Branch push failures use existing error handling.** If the git push to origin fails before PR creation is attempted (e.g., network error, auth failure, rejected push), the exception propagates through the standard pipeline error handling in `session_lifecycle.py` and `fix_pipeline.py`. No special handling is added for this case.

18. **Nightshift partial failure posts branch-mode comment.** When `"pr"` mode partially fails in nightshift (branch pushed, PR creation failed), the branch-mode issue comment is posted in addition to logging the error. This ensures the user can locate the branch and create the PR manually, even without access to logs.

19. **Test design fully delegated.** Goal #2 specifies automated test coverage for all mode × pipeline combinations, but full test design is delegated to the implementor. Detailed test contracts will be produced by the spec generation step.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| `platform.type = "github"` | Runtime configuration | Required for `"pr"` mode; absence triggers fallback to `"branch"` via `create_platform_safe()` |
| `GITHUB_PAT` environment variable | Runtime secret | Required for `"pr"` mode GitHub API authentication; validated lazily at PR creation time via `create_platform_safe()` |
| `GitHubPlatform` (`github.py`) | Internal implementation | Provides the `create_pr()` implementation via `aiohttp`; handles duplicate-PR 422 idempotently |
| `PlatformProtocol` (`protocol.py`) | Internal interface | `create_pr()` must be re-added to the protocol definition |
| `NullPlatform` (`protocol.py`) | Internal stub | Must implement `create_pr()` as `raise NotImplementedError(...)` |
| `create_platform_safe()` (`platform_factory`) | Internal utility | Used as the platform availability guard before any `create_pr()` call |
| `get_changed_files()` (`workspace`) | Internal utility | Used to derive the Changed Files list and `touched_files` return value in `"branch"` and `"pr"` modes |
| Legacy spec 65 | Historical reference | `create_pr()` was removed in this legacy spec; no active dependency, referenced in existing code comments only |
