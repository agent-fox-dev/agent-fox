---
spec_id: '12'
spec_name: drift_finding_supersession
title: Drift Finding Supersession
status: draft
created_at: '2026-06-25T12:04:32.726233+00:00'
updated_at: '2026-06-25T12:07:23.417009+00:00'
owner: ''
source: https://github.com/agent-fox-dev/agent-fox/issues/623
schema_version: 1
---
# Supersede Resolved Drift Findings After Task Group Merge

## Summary

Implement file-based supersession of resolved drift findings after each task
group completes and merges successfully. Currently, drift findings from the
initial drift-review phase (group 0) are frozen and injected unchanged into
every subsequent coder session, even after earlier groups have resolved the
issues they describe. This wastes prompt space (~14k chars of stale context
across a typical spec run) and can confuse coders who see findings about
problems that no longer exist.

## Goals

The following measurable outcomes define success for this feature:

1. **Stale context reduction**: Reduce stale drift findings injected per coder
   session by ≥50% by group 4 of a typical spec run (relative to the baseline
   of no supersession).
2. **Resolution coverage**: Supersede ≥80% of resolved drift findings within
   one task group completion after the resolving merge.
3. **Zero false positives**: No drift finding is superseded unless at least one
   of its referenced files was confirmed as touched by the completing session.
   The false-positive supersession rate must be 0%.

Compliance with all three goals is validated by the integration test described
in the [Testing Requirements](#testing-requirements) section.

## Problem

Drift findings are created by the drift-review agent (group 0) and stored in
the `drift_findings` table. They are queried via
`query_active_drift_findings()` with `include_prereview=True` and injected into
every subsequent coder session. However, no mechanism exists to mark drift
findings as resolved when a task group's merge addresses the referenced files.

The existing `supersede_injected_findings()` function handles supersession for
`review_findings` and `verification_results` but does not include
`drift_findings`. Even if it did, the blanket supersession approach (supersede
all injected findings on session completion) would over-supersede for drift
findings — since all group-0 drift findings are injected into every subsequent
group, completing any single group would retire all drift findings, including
unresolved ones.

### Evidence

In a real session (spec 07, `nightshift_standalone_cli`), 12 drift findings
were injected identically into groups 2-7. By group 6, only 2-3 of the 12 were
still relevant — the other 9-10 described state that earlier groups had already
fixed (directories created, files deleted, imports removed, tests migrated,
workspace entries added).

## Proposed Solution

After each task group completes and its changes merge successfully, compare the
merge's touched files against active drift findings' `artifact_ref` values.
Supersede any drift finding whose referenced file or path was modified by the
merge.

### Database Schema Reference

The `drift_findings` table was introduced in migration v4 (`migrations.py`,
lines 107–125) and includes the following relevant columns:

| Column | Type | Nullable | Notes |
|---|---|---|---|
| `id` | TEXT | NOT NULL | Primary key (UUID string) |
| `spec_name` | TEXT | NOT NULL | Identifies the spec owning the finding |
| `task_group` | TEXT | NOT NULL | Task group that generated the finding (e.g., `"0"`) |
| `artifact_ref` | TEXT | NULL | Structured file or directory reference (may be null) |
| `superseded_by` | TEXT | NULL | Session node_id or dismissal timestamp; null = active. Type fixed from UUID to TEXT in migration v25 |

No schema migration is required for this feature. The `superseded_by` column
already exists with the correct TEXT type.

### Matching Rules

1. **Structured matching via `artifact_ref`**: For drift findings with a
   non-null `artifact_ref`:
   - Strip line number suffixes (e.g., `:42`) and normalize whitespace
   - If the normalized ref ends with `/` (directory reference), use **prefix
     matching**: the finding matches if any touched file's path starts with the
     ref
   - Otherwise, use **exact path matching**: the finding matches if any touched
     file's path equals the normalized ref
2. **Unmatched findings persist**: Drift findings with null `artifact_ref` (or
   whose `artifact_ref` does not match any touched file) remain active. They
   will be superseded when a future drift review runs and
   `insert_drift_findings()` supersedes all active findings for the same
   `(spec_name, task_group)` composite key.

### Integration Point

The supersession check runs in the result handler's post-merge success path
(`result_handler.py`), after the merge is confirmed and the session outcome is
recorded. The inputs are:

- The session's `touched_files` from the `SessionRecord` (already available)
- Active drift findings for the spec, queried from the `drift_findings` table
  via a new private helper (see [New Function](#new-function) below)

**Only coder session completions trigger this check.** Reviewer and verifier
sessions do not modify code and therefore cannot resolve drift findings.

#### Call Site and Error Boundary

`supersede_drift_findings_by_files()` is called from the coder-session success
path in `result_handler.py`, immediately after the session outcome is recorded
and `supersede_injected_findings()` returns. The call is wrapped in a
`try/except` with a **warning log** on failure; exceptions from supersession
are **swallowed locally** and never allowed to affect session outcome recording.
This mirrors the existing pattern in `fox_provider.ingest()`, which wraps
`supersede_injected_findings()` in a `try/except` with a warning log, and is
consistent with the architecture's "graceful degradation everywhere" principle
(Section 14 of `architecture.md`).

### Edge Cases and Error Handling

- **`touched_files` is null or empty**: Treat as an empty list — no drift
  findings are superseded. Log a debug message. This aligns with the
  "graceful degradation everywhere" design principle documented in the
  architecture. This covers both the case where a merge touches no files and
  the case where `touched_files` failed to populate on the `SessionRecord`.
- **DuckDB query failure during supersession**: Log a warning and swallow the
  exception locally (see [Call Site and Error Boundary](#call-site-and-error-boundary)
  above). The session outcome has already been recorded before supersession
  runs, so the failure is safe to absorb.
- **All `artifact_ref` values are null**: No findings are superseded in this
  invocation. The findings persist and will be retired by a future drift
  review's `insert_drift_findings()` call.

### Supersession Marker

Superseded drift findings have their `superseded_by` column set to the
completing session's `node_id` (e.g., `my_spec:3`), consistent with the
existing supersession convention for review findings and verification results.

### New Function

Add a `supersede_drift_findings_by_files()` function to `review_store.py` that:

1. Accepts a DuckDB connection, spec name, list of touched file paths, and
   supersession marker (node_id)
2. If `touched_files` is null or empty, logs a debug message and returns 0
   without querying the database
3. Queries **all** active drift findings for the spec across all task groups
   using a new private helper function (see below) — bypassing the
   `include_prereview` / task-group-scoped logic of the public
   `query_active_drift_findings()` API
4. For each finding with a non-null `artifact_ref`, applies the matching rules
5. Supersedes matching findings by setting `superseded_by`
6. Returns the count of superseded findings
7. Logs which findings were superseded (finding ID and `artifact_ref`) for
   observability

#### Private Helper Query

A private helper function within `review_store.py` fetches all active drift
findings for a spec regardless of task group:

```sql
SELECT id, artifact_ref
FROM drift_findings
WHERE spec_name = ?
  AND superseded_by IS NULL
```

This bypasses the `include_prereview` flag and task-group filter present in
`query_active_drift_findings()`, which are designed for injection — not for
supersession. The helper should not be exported as part of the public
`review_store` API.

#### Why Not Reuse `query_active_drift_findings()`?

`query_active_drift_findings(spec_name, include_prereview=True)` applies
task-group-scoped logic; `include_prereview=True` adds group `"0"` to the
task_group filter rather than returning all groups unconditionally. For
supersession, findings from any task group (not just group 0) must be
evaluated, so a dedicated query is required.

### Architecture Documentation

Update `docs/architecture.md` (Sections 5.4 and 10.3-10.4) and
`docs/architecture/05-knowledge-system-architecture.md` (Sections 4.1, 8, and
9) to document:

- The drift finding supersession lifecycle (file-based, post-merge) as distinct
  from review finding supersession (injection-based, blanket)
- The matching rules and fallback behavior for findings without `artifact_ref`
- The updated finding lifecycle diagram showing two supersession paths

## Testing Requirements

Both unit tests and integration tests are required for this feature.

### Unit Tests

All unit tests should live adjacent to `review_store.py` and cover the
`supersede_drift_findings_by_files()` function's internal logic in isolation:

| Test case | Description |
|---|---|
| Exact path match | A finding with `artifact_ref = "src/foo.py"` is superseded when `touched_files` contains `"src/foo.py"` |
| Exact path non-match | A finding with `artifact_ref = "src/foo.py"` is **not** superseded when `touched_files` contains only `"src/bar.py"` |
| Prefix / directory match | A finding with `artifact_ref = "packages/nightshift/"` is superseded when `touched_files` contains any file starting with `"packages/nightshift/"` |
| Line number stripping | A finding with `artifact_ref = "src/foo.py:42"` normalizes to `"src/foo.py"` and matches correctly |
| Null `artifact_ref` passthrough | A finding with `artifact_ref = NULL` is never superseded and the function returns 0 for that finding |
| Empty `touched_files` | Passing an empty list returns 0 and makes no database writes; a debug message is logged |
| Null `touched_files` | Passing `None` is treated identically to an empty list |
| Multiple matches | Multiple findings matching the same touched file are all superseded; the returned count is correct |

### Integration Tests

One integration test must cover the full end-to-end flow, mirroring the
`nightshift_standalone_cli` evidence case to directly validate the measurable
goals:

1. **Seed** the `drift_findings` table with **12 findings**: 10 with
   `artifact_ref` values that match the session's `touched_files`, and 2 with
   `artifact_ref` values that do not match (or are null).
2. **Simulate** a successful coder session merge triggering the post-merge hook
   in `result_handler.py` (touching the 10 referenced files).
3. **Assert** that exactly 10 findings have `superseded_by` set to the
   session's `node_id`.
4. **Assert** that the remaining 2 findings have `superseded_by = NULL`
   (still active).
5. **Assert** that subsequent calls to `query_active_drift_findings()` return
   only the 2 non-matching findings, confirming superseded findings are
   excluded from future coder session injection.

This scenario directly validates:
- **≥80% resolution coverage** (10/12 = 83% superseded in one pass)
- **0% false positives** (2 non-matching findings remain untouched)
- **≥50% stale context reduction** is an indirect consequence of the above

## Tech Stack

- Python 3.12+
- DuckDB (embedded database, existing `review_store.py` data access layer)
- Existing `result_handler.py` session lifecycle hooks

## Performance

No indexing changes are required. DuckDB is a columnar analytical database that
performs well on full table scans at the scale of the `drift_findings` table.
Drift findings are generated once per spec (group 0), producing typically 5–20
findings per spec. Even with hundreds of concurrent specs, the table would hold
thousands of rows at most — well within DuckDB's scan performance envelope.

## Out of Scope

- Re-running the drift review after each group (Option B — more expensive,
  requires an LLM call per group completion)
- Time-based decay of drift findings (Option C — imprecise, would retire both
  resolved and unresolved findings based on age)
- Re-activation of superseded findings if a later group re-introduces the
  problem (handled by future drift reviews)
- Adding drift findings to the blanket `supersede_injected_findings()` function
  (would over-supersede since all group-0 findings are injected into every
  subsequent session)
- Changes to the drift finding creation or injection flow (only the
  supersession/retirement path changes)
- Assigning an owner within this spec (owner assignment happens outside the
  spec process)
- Feature flags or kill switches for disabling supersession at runtime — a code
  rollback is acceptable given the low risk. The matching rules are
  deterministic and conservative (`artifact_ref`-only, no description parsing),
  making the false-positive risk minimal.
- Schema migrations — `superseded_by` already exists on `drift_findings` with
  the correct TEXT type (migration v25)

## Design Decisions

1. **File-based matching only, not blanket supersession**: Drift findings
   describe pre-existing codebase issues, not issues in the coder's own output.
   Unlike review findings (where blanket supersession on session completion is
   appropriate because the coder was tasked with addressing them), drift
   findings from group 0 are injected into all groups via
   `include_prereview=True`. Blanket supersession would retire all drift
   findings when any single group completes, hiding unresolved ones from later
   groups. File-based matching retires only findings whose referenced files
   were touched.

2. **`artifact_ref`-only matching, no description parsing**: Parsing free-text
   descriptions for file paths would be fragile and produce false positives.
   The `artifact_ref` field exists specifically for structured file references.
   Findings without `artifact_ref` persist until superseded by a future drift
   review's normal `insert_drift_findings()` mechanism (which supersedes all
   active findings for the same spec/task_group composite key when new findings
   are inserted).

3. **Prefix matching for directory references**: A drift finding referencing
   `packages/nightshift/` should match any file created under that path. The
   trailing `/` in `artifact_ref` distinguishes directory references from file
   references. File references use exact matching after stripping line numbers.

4. **Use `touched_files` from SessionRecord**: The session outcome already
   includes the list of files touched during the session. This avoids
   re-computing the diff from git and uses the authoritative source that the
   orchestrator already maintains.

5. **Graceful degradation on empty/null `touched_files`**: Rather than raising
   an error or attempting a database query with an empty file list, the function
   short-circuits and returns 0 with a debug log. This preserves the session
   lifecycle even if `touched_files` was not populated, consistent with the
   architecture's graceful degradation principle.

6. **Swallow exceptions locally in `result_handler.py`**: Supersession is a
   best-effort enrichment step. The session outcome is recorded before
   supersession runs, so a supersession failure must never surface to the
   caller. This mirrors the existing `try/except` + warning-log pattern around
   `supersede_injected_findings()` in `fox_provider.ingest()`.

7. **Dedicated private query instead of reusing `query_active_drift_findings()`**:
   The existing public API applies task-group-scoped logic intended for
   injection, not supersession. A private helper with a simple
   `WHERE spec_name = ? AND superseded_by IS NULL` query is more correct and
   avoids coupling supersession logic to injection-oriented filtering.

8. **No re-activation mechanism**: If a superseded drift finding's condition is
   re-introduced by a later group, it will be caught by the next drift review
   rather than by re-activating the superseded finding. Re-activation would add
   significant complexity (tracking state transitions, detecting regressions)
   with marginal benefit.

9. **Scope to coder sessions only**: Only coder session completions should
   trigger drift finding supersession. Reviewer and verifier sessions do not
   modify code and therefore cannot resolve drift findings.

10. **No feature flag or kill switch**: The matching logic is deterministic,
    conservative, and easily auditable via the supersession log. The risk of
    silent over-suppression is low, and a code rollback is a sufficient
    mitigation strategy given the team's deployment cadence.

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/623
