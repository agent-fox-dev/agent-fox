---
spec_id: '05'
spec_name: nightshift_knowledge_parity
title: Nightshift Knowledge Parity
status: draft
created_at: '2026-07-07T13:48:05.555992+00:00'
updated_at: '2026-07-07T13:59:10.442540+00:00'
owner: agent-fox
source: 'https://github.com/agent-fox-dev/agent-fox/issues/698'
schema_version: 1
---
# Night Shift Knowledge Parity

## Intent

Enable Night Shift fix sessions to accumulate and reuse institutional memory via the existing `KnowledgeProvider` infrastructure, so that repeated attempts on the same issue benefit from prior session context rather than starting blind every time.

## Background

**Night Shift** is agent-fox's automated overnight fix pipeline. It runs unattended fix sessions (`fix-issue-{N}`) against a queue of open issues: triaging each issue, invoking a `CoderReviewerLoop` to implement a fix, harvesting changed files, and emitting session events. Night Shift is distinct from the main spec-driven engine (`engine/session_lifecycle.py`) in that it manages its own pipeline logic in `fix_pipeline.py` and `coder_reviewer.py`.

**KnowledgeProvider** (`FoxKnowledgeProvider`) is agent-fox's cross-session institutional memory layer. It supports two operations:

- **`ingest()`** — stores session outcomes, file-based drift supersessions, and structured session summaries (rejected approaches, gotchas, assumptions) keyed by spec name and session ID.
- **`retrieve()`** — surfaces relevant prior knowledge (cross-group reviews, cross-spec drift findings, same-spec summaries) for a new session, gated by a `KnowledgeProviderConfig` that caps each retrieval category.

The main engine wires real data through both methods at the end of every session. Night Shift's integration, however, is effectively a stub — silently disabling the majority of the knowledge pipeline for all fix sessions. This spec corrects that by wiring real data through the existing protocol, with no changes to `FoxKnowledgeProvider` internals or the database schema.

## Problem

Night Shift's knowledge integration is effectively a stub. The `_ingest_knowledge` method in `fix_pipeline.py` always passes `touched_files=[]`, never passes a `summary`, and `_retrieve_knowledge` passes `task_group=None` — silently disabling three of five configured retrieval caps and all summary retrieval.

As a result:

1. **File-based drift supersession never runs** for Night Shift sessions because `touched_files` is always empty. Drift findings that the fix resolved remain active and get re-injected into future sessions.

2. **Session summaries are never stored** because no `summary` is ever passed to `_ingest_knowledge`. The `_store_summary` path in `FoxKnowledgeProvider.ingest()` is unreachable from Night Shift.

3. **Cross-group reviews, cross-spec drift, and same-spec summaries are never retrieved** because `_retrieve_knowledge` passes `task_group=None`, which causes `FoxKnowledgeProvider.retrieve()` to skip all three guarded query branches.

4. **`file_footprint` is never passed on retrieval**, so cross-spec drift queries that could surface relevant findings from other specs touching the same files are skipped.

Fix sessions therefore start blind every time — repeat attempts on the same issue, and future fixes to the same files, get no institutional memory.

## Goals

The following outcomes define success for this spec. All are observable after implementation and serve as the basis for acceptance testing:

1. **File-based drift supersession runs for fix sessions** — after a successful harvest, `supersede_drift_findings_by_files` and `supersede_stale_pre_code_findings` execute with the real set of changed files, not an empty list. Verified by confirming that drift findings resolved by a fix no longer appear in subsequent sessions for the same files.

2. **Session summaries are stored** — when `outcome.response` contains structured fields (rejected approaches, gotchas, assumptions), the extracted summary is persisted via `_store_summary` in `FoxKnowledgeProvider.ingest()`. Verified by querying the knowledge store after a Night Shift session and confirming a summary record exists for the session ID.

3. **Cross-group reviews, cross-spec drift, and same-spec summaries are retrieved** — `_retrieve_knowledge` passes `task_group="0"`, enabling all three guarded retrieval branches. Verified by confirming that the retrieval call returns a non-empty list when prior knowledge exists for the same spec (`fix-issue-{N}`) or overlapping files, and that this list is injected into the session context. A retrieval is considered successful when the structured log line emitted at retrieval time records a non-zero item count for at least one retrieval category — this log line is the observable signal for unattended runs. On a cold start (first ever run on a given issue number), the retrieval returning an empty list is a pass — it indicates correct wiring with no prior knowledge available, not a misconfiguration.

4. **Cross-spec drift from overlapping files is surfaced** — `triage.affected_files` is passed as `file_footprint` on retrieval, enabling cross-spec drift queries. Verified by confirming that findings from other specs touching the same files are returned when such overlap exists.

5. **No regressions in the pre-harvest ingestion path** — the existing pre-harvest `_ingest_knowledge` call (session-status-based finding supersession) continues to function unchanged.

## Solution

Wire real data through the existing `KnowledgeProvider` protocol methods. No new config surface, no new retrieval channels, no new tables — just pass the data that already exists through the parameters that already exist.

### 1. Pass real `touched_files` after harvest

`_harvest_and_push` already calls `harvest()` which returns a `list[str]` of changed file paths (this is the complete return type — `harvest()` does not return a `commit_sha`). Currently the return value is discarded (only checking for empty). Change `_harvest_and_push` to return the file list. Add a post-harvest call to `_ingest_knowledge` with the real `touched_files` so that `supersede_drift_findings_by_files` and `supersede_stale_pre_code_findings` run for fix sessions.

Because `harvest()` does not return a `commit_sha`, the `commit_sha` field is omitted from the post-harvest ingestion call for all fix sessions. This is acceptable — the field is optional and its absence does not affect other ingestion behavior.

The existing pre-harvest `_ingest_knowledge` call (inside `_emit_session_event`) continues to handle finding supersession based on `session_status` — that path does not need `touched_files`.

### 2. Extract and pass session summaries

Introduce a new shared utility module at `agentfox.knowledge.extraction` containing a simplified, standalone extraction function:

```python
def extract_session_summary(response: str) -> tuple[str | None, list, list, list]:
    ...
```

This function accepts the raw response string and returns a 4-tuple of `(summary_text, rejected_approaches, gotchas, assumptions)`. It is a simplification of the engine's existing private method `async def _extract_knowledge_and_findings(self, node_id: str, attempt: int, workspace: WorkspaceInfo, outcome_response: str = '') -> tuple[str | None, list, list, list]` — only the parameters needed for the extraction logic itself are retained (`response: str`). The `self`, `node_id`, `attempt`, and `workspace` parameters are not required for extraction and are dropped.

**`extract_session_summary` is a synchronous function.** The extraction logic is CPU-bound and does not require async execution. The engine's call site in `session_lifecycle.py` calls it directly — no `await`, no `asyncio.run()` wrapping. The private method being replaced was async only as a side-effect of being a method on an async class, not because its logic required it. The engine's call site is updated to call `extract_session_summary` without `await`; its external behavior (the returned 4-tuple) is unchanged.

Both the engine and Night Shift import `extract_session_summary` from `agentfox.knowledge.extraction`. This avoids code duplication and prevents divergence between the two callers over time.

Use `extract_session_summary` to parse the coder session's `outcome.response` for structured fields. Pass the extracted summary text, rejected approaches, gotchas, and assumptions through `_ingest_knowledge` so `_store_summary` in `FoxKnowledgeProvider.ingest()` stores them.

When the response does not contain structured fields or is malformed, `extract_session_summary` returns `(None, [], [], [])` and summary storage is skipped — matching the engine's existing behavior exactly.

### 3. Pass `task_group="0"` on retrieval

Change `_retrieve_knowledge` to pass `task_group="0"` instead of `None`. This matches the node ID convention (`fix-issue-{N}:0:coder`) already used throughout the pipeline. Enables:

- Cross-group review retrieval (capped by `max_cross_group_items`)
- Cross-spec drift retrieval (capped by `max_cross_spec_items`)
- Same-spec summary retrieval (capped by `max_summary_items`)

All three use existing `KnowledgeProviderConfig` caps — no new config.

### 4. Pass `task_description` on retrieval

Pass the triage summary/description field as the `task_description` argument to `KnowledgeProvider.retrieve()`. This is a required positional argument. The value is sourced from the triage output already available in the pipeline at the point `_retrieve_knowledge` is called. When triage is `None` or the description field is unavailable, pass an empty string (`""`) as a safe fallback.

### 5. Pass `file_footprint` on retrieval

Pass `triage.affected_files` as the `file_footprint` parameter to `_retrieve_knowledge` / `KnowledgeProvider.retrieve()`. This enables cross-spec drift queries that surface findings from other specs touching the same files. The data is already available from triage output.

When `triage` is `None` or `triage.affected_files` is empty or `None`, pass `None` (or omit the parameter) — cross-spec drift queries are skipped, matching current behavior. No error is raised and no special fallback is needed. Specifically, if `triage` itself is `None` (e.g., triage failed or was skipped), the pipeline proceeds with `file_footprint=None` rather than raising an `AttributeError`.

### 6. `spec_name` convention for fix sessions

The `spec_name` passed to both `KnowledgeProvider.retrieve()` and `KnowledgeProvider.ingest()` for fix sessions is `fix-issue-{N}`, where `N` is the issue number (e.g., `fix-issue-42`). This matches the existing session naming convention and ensures knowledge is correctly scoped per issue — records for different issue numbers are isolated from each other in the knowledge store.

`N` is sourced from an existing attribute on the pipeline object in `fix_pipeline.py` — no new constructor arguments or string-parsing of the session ID are required.

### 7. `CoderReviewerLoop` return value changes

`outcome.response` and `triage.affected_files` are threaded through `coder_reviewer.py` to `fix_pipeline.py` by adding fields to the existing return dataclass or namedtuple that `CoderReviewerLoop.run()` already returns. No new return type is introduced — the existing return object is extended with `response: str` and `affected_files: list[str]` fields (or equivalent). This minimises the blast radius of the change and keeps the call site in `fix_pipeline.py` backward-compatible.

**Default values for early-exit paths**: `response` defaults to `""` (empty string) and `affected_files` defaults to `[]` (empty list). These defaults apply whenever the loop exits before a coder session is run (e.g., triage fails, triage returns no affected files, or the loop is aborted). With these defaults in place, `fix_pipeline.py` call sites will never encounter an `AttributeError` — an empty `response` causes `extract_session_summary` to return `(None, [], [], [])` (summary storage skipped), and an empty `affected_files` causes `file_footprint=None` to be passed on retrieval (cross-spec drift queries skipped). Both are safe, no-op fallbacks.

### 8. Error handling for the post-harvest ingestion call

If the post-harvest `_ingest_knowledge` call raises (e.g., due to a database error), the error is caught, logged at `ERROR` level with the session ID and exception details, and the session continues. The session is still considered successful — a failure to persist knowledge metadata does not invalidate a completed fix. This matches the principle that knowledge ingestion is best-effort infrastructure, not a correctness gate.

For `_retrieve_knowledge`, exceptions are handled the same way as the current stub implementation: the exception is logged at `WARNING` level and an empty list is returned. This behavior is preserved unchanged.

The pre-harvest ingestion call (inside `_emit_session_event`) retains its existing error-handling behavior unchanged.

No timeout or async execution is required for the new synchronous `FoxKnowledgeProvider` calls — blocking is acceptable given Night Shift's unattended overnight nature, and the existing error-catch-and-log pattern covers failure cases sufficiently.

### 9. Pre-harvest and post-harvest ingestion call interaction

Each call to `FoxKnowledgeProvider.ingest()` is independent and additive. The pre-harvest call supplies session-status-based context keys (e.g., `session_status`); the post-harvest call supplies file- and summary-based keys (`touched_files` and `summary`). Partial context per call is safe — `ingest()` merges keys rather than overwriting the full record. Two ingestion calls per successful session is the same pattern the engine uses.

### 10. Observability

Add structured log lines at each new code path to make Night Shift's knowledge activity visible in production logs (Night Shift runs unattended, so silent failures must be detectable):

- **Post-harvest ingestion**: log the count of `touched_files` passed and whether a summary was extracted (`summary_extracted=True/False`).
- **Retrieval**: log the `task_group` value used and the count of items returned per retrieval category.
- **Error paths**: log at `ERROR` level with session ID and exception when the post-harvest ingestion call fails.

No new metrics infrastructure or feature flags are required for this phase.

### 11. Deployment safety

This change is safe to deploy at any time without a coordinated restart of the Night Shift runner. Night Shift sessions are short-lived and the new knowledge calls are purely additive — they extend existing call sites without altering session control flow or data written by pre-existing code paths. No in-flight session can reach a partially-wired state that causes incorrect behavior; at worst, a session that begins before deployment completes without the new knowledge wiring and a session begun after deployment gains it.

## Scope

This spec covers Phase 1 only: wiring real data through existing infrastructure. Phase 2 (replacing `CoderReviewerLoop` with shared engine infrastructure) is explicitly out of scope. Phase 2 is not yet scheduled and will be tracked in a separate spec and design document when prioritized.

## Non-Goals

- No new retrieval channels or database tables
- No new configuration surface
- No changes to `FoxKnowledgeProvider` internals
- No changes to the coder-reviewer loop structure
- No cross-file summary retrieval (future enhancement)
- No data retention or purge policy for fix-session knowledge records (future concern — not addressed in this phase)
- No timeout or async wrapping of knowledge calls (Night Shift is unattended and blocking is acceptable)
- Phase 2 (CoderReviewerLoop replacement with shared engine infrastructure) — not yet scheduled

## Files Affected

- `packages/agentfox/agentfox/nightshift/fix_pipeline.py` — main changes: `_ingest_knowledge`, `_retrieve_knowledge`, `_harvest_and_push`, `_emit_session_event`, and the calling code after harvest
- `packages/agentfox/agentfox/nightshift/coder_reviewer.py` — extend the existing return dataclass/namedtuple with `response: str` (default `""`) and `affected_files: list[str]` (default `[]`) fields so `outcome.response` and `triage.affected_files` are available to `fix_pipeline.py` at ingestion/retrieval time
- `packages/agentfox/agentfox/knowledge/extraction.py` *(new)* — shared utility module containing the new `extract_session_summary(response: str) -> tuple[str | None, list, list, list]` function, imported by both `engine/session_lifecycle.py` and `fix_pipeline.py`
- `packages/agentfox/agentfox/engine/session_lifecycle.py` — updated to call `extract_session_summary` from `agentfox.knowledge.extraction` directly (no `await`) instead of the local private async `_extract_knowledge_and_findings`; external behavior unchanged

## Testing Requirements

Unit tests are required for all changed methods. Integration-level verification is handled manually (see below).

**Mock strategy**: `FoxKnowledgeProvider` is mocked at the class level using `unittest.mock.patch` (or equivalent) targeting the import path used in `fix_pipeline.py`. This avoids a live database dependency in unit tests. The post-harvest ingestion error path is simulated by configuring the mock to raise an exception and asserting that the correct `ERROR`-level log entry is emitted and the session continues.

Specifically:

- **`fix_pipeline.py`**: unit tests for `_ingest_knowledge` (post-harvest call with real `touched_files` and summary; `commit_sha` absent from post-harvest call; mock raises exception → error is logged at `ERROR` level and session continues), `_retrieve_knowledge` (verifies `task_group="0"`, `task_description` from triage, and `file_footprint` are passed; verifies `None` file_footprint when `triage` is `None` or `triage.affected_files` is empty/None; verifies that when an exception is raised, it is logged at `WARNING` level and an empty list is returned), and `_harvest_and_push` (verifies `list[str]` file list is returned).
- **`coder_reviewer.py`**: unit tests verifying that `outcome.response` and `triage.affected_files` are correctly populated in the extended return object and available to `fix_pipeline.py` call sites. Also verifies that early-exit paths (e.g., triage failure) produce the expected defaults (`response=""`, `affected_files=[]`) rather than uninitialized fields.
- **`agentfox.knowledge.extraction`**: unit tests for `extract_session_summary` covering: (a) structured fields present — returns `(summary_text, rejected_approaches, gotchas, assumptions)` with non-None summary; (b) structured fields absent — returns `(None, [], [], [])`; (c) malformed response — returns `(None, [], [], [])` gracefully. Additionally, a unit test verifying that `engine/session_lifecycle.py` now calls `extract_session_summary` from `agentfox.knowledge.extraction` (without `await`) and produces the same outputs as before the refactor.

Unit tests do **not** assert on the content of structured log lines. Log output is considered an implementation detail; functional assertions on method call arguments and return values are the correctness gate. The structured log lines serve as a production observability aid for unattended runs and are verified manually.

Manual verification of end-to-end behavior (e.g., querying the knowledge store after a Night Shift run, inspecting structured log output for retrieval item counts) is the integration-level gate and does not require automated integration tests against a test database.

## Verified External API

### `agentfox.knowledge.fox_provider` (FoxKnowledgeProvider)

| Symbol | Signature | Notes |
|--------|-----------|-------|
| `retrieve()` | `(spec_name, task_description, task_group=None, session_id=None, file_footprint=None) -> list[str]` | `task_group` guards cross-group, cross-spec, and summary queries; `task_description` is a required positional argument — Night Shift passes the triage summary/description field, or `""` if triage is unavailable |
| `ingest()` | `(session_id, spec_name, context: dict) -> None` | Context keys: `session_status`, `touched_files`, `commit_sha`, `summary`, `archetype`, `task_group`, `attempt`, `rejected_approaches`, `gotchas`, `assumptions`; each call is additive — partial context per call is safe |

### `agentfox.workspace.harvest`

| Symbol | Signature | Notes |
|--------|-----------|-------|
| `harvest()` | `(repo_root, workspace, ...) -> list[str]` | Returns list of changed file paths only. `commit_sha` is NOT returned by this function and is omitted from all post-harvest ingest calls for fix sessions. |

### `afaudit.sink` (SessionOutcome)

| Symbol | Field | Notes |
|--------|-------|-------|
| `SessionOutcome.response` | `str` | Last assistant message text; available after `run_session` |

### `agentfox.knowledge.extraction` *(new)*

| Symbol | Signature | Notes |
|--------|-----------|-------|
| `extract_session_summary` | `(response: str) -> tuple[str \| None, list, list, list]` | Synchronous function. Returns `(summary_text, rejected_approaches, gotchas, assumptions)`. Returns `(None, [], [], [])` when structured fields are absent or response is malformed. Simplified from the engine's async `_extract_knowledge_and_findings` — `self`, `node_id`, `attempt`, and `workspace` parameters are not needed for extraction logic and are dropped. Called directly (no `await`) from both `session_lifecycle.py` and `fix_pipeline.py`. |

## Design Decisions

1. **`task_group` value for retrieval**: Use `"0"`, matching the existing node ID convention (`fix-issue-{N}:0:coder`). The `fix-issue-{N}` spec name already distinguishes fix sessions from spec-driven ones.

2. **`spec_name` for fix sessions**: Use `fix-issue-{N}` (e.g., `fix-issue-42`), matching the existing session naming convention. `N` is sourced from an existing attribute on the pipeline object — no string-parsing of the session ID or new constructor arguments are needed. This scopes all knowledge records to a specific issue number, preventing cross-contamination between different issues.

3. **Summary extraction mechanism**: Introduce `extract_session_summary(response: str) -> tuple[str | None, list, list, list]` in `agentfox.knowledge.extraction`. This is a simplified, **synchronous**, standalone extraction of the engine's existing async private method — retaining only the parameters needed for extraction logic. The extraction logic is CPU-bound and does not require async execution. Both the engine and Night Shift import from this shared location, calling it directly without `await`. Returns `(None, [], [], [])` when structured fields are absent or the response is malformed.

4. **Ingestion timing for `touched_files`**: Return `changed_files` (`list[str]`) from `_harvest_and_push` and add a post-harvest `_ingest_knowledge` call. `commit_sha` is omitted from the post-harvest call because `harvest()` does not return it. The pre-harvest call (in `_emit_session_event`) continues to handle finding supersession. Two ingestion calls per successful session is the same pattern the engine uses; calls are additive and independent.

5. **`task_description` on retrieval**: Night Shift passes the triage summary/description field as `task_description` to `KnowledgeProvider.retrieve()`. When triage is `None` or the description field is unavailable, `""` is used as a safe fallback.

6. **`file_footprint` on retrieval**: Pass `triage.affected_files`. When `triage` is `None` (e.g., triage failed or was skipped) or `triage.affected_files` is empty or `None`, pass `None` — cross-spec drift queries are skipped, matching current behavior. No `AttributeError` is raised; the pipeline proceeds normally.

7. **`CoderReviewerLoop` return value defaults**: Extend the existing return dataclass or namedtuple with `response: str` (default `""`) and `affected_files: list[str]` (default `[]`) fields. No new return type is introduced. These defaults ensure early-exit paths (triage failure, aborted loop) never produce uninitialized fields — `fix_pipeline.py` call sites are always safe to read.

8. **Post-harvest ingestion error handling**: Catch exceptions, log at `ERROR` level with session ID and exception details, and continue. Session success is not gated on knowledge ingestion. Retrieval exceptions are caught, logged at `WARNING` level, and an empty list is returned — preserving the current stub behavior unchanged. No timeout or async execution is required — blocking is acceptable for Night Shift's unattended use case.

9. **`commit_sha` in post-harvest ingest context**: Omitted — `harvest()` returns only `list[str]` and does not expose a commit SHA. No additional plumbing is introduced to retrieve it from another source.

10. **Observability**: Structured log lines at each new code path (touched_files count, summary_extracted flag, task_group used, retrieval item counts per category, errors). No new metrics infrastructure or feature flags in this phase. Retrieval log lines serve as the primary observable signal for Goal 3 verification in unattended runs. Unit tests do not assert on log line content — log output is treated as an implementation detail.

11. **Mock strategy for tests**: `FoxKnowledgeProvider` is mocked at the class level via `unittest.mock.patch` targeting the import path used in `fix_pipeline.py`. Error paths are simulated by configuring the mock to raise and asserting on log output.

12. **Summary scope**: Same-issue retrieval only (keyed on `fix-issue-{N}`). Cross-file summary retrieval is a future enhancement.

13. **Deployment safety**: No coordinated restart required. Sessions are short-lived and new calls are additive — deployment can occur at any time without risk to in-flight sessions.

14. **Data retention**: Out of scope for this phase. Knowledge records accumulated by fix sessions are not purged by any mechanism introduced here. Retention policy will be addressed in a future spec if store growth becomes a concern.

15. **Cold-start retrieval behavior**: On the first ever Night Shift run for a given issue number (`fix-issue-{N}`), `_retrieve_knowledge` returning an empty list is expected and correct — it indicates no prior knowledge exists, not a misconfiguration. Acceptance criteria for Goal 3 apply only to subsequent runs where prior knowledge has been stored.
