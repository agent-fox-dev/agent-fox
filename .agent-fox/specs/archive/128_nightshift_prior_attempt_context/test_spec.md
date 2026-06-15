# Test Specification: Night-Shift Prior Attempt Context

## Overview

Tests use an in-memory DuckDB database with pre-inserted session outcome rows
to verify query behavior. Format tests use pure function calls. Integration
tests use the CliRunner pattern with mock platform but real DuckDB and real
pipeline code.

## Test Cases

### TS-128-1: Query returns prior coder sessions

**Requirement:** 128-REQ-1.1
**Type:** unit
**Description:** Verify that query returns coder sessions for the given issue,
excluding the current run.

**Preconditions:**
- In-memory DuckDB with `session_outcomes` table.
- 3 rows inserted: 2 with `spec_name='fix-issue-42'`, `archetype='coder'`,
  different `run_id`s; 1 with the current `run_id`.

**Input:**
- `query_prior_attempts(conn, 'fix-issue-42', current_run_id='run_current')`.

**Expected:**
- List of 2 `PriorAttempt` instances.
- Neither has `run_id == 'run_current'`.

**Assertion pseudocode:**
```
result = query_prior_attempts(conn, "fix-issue-42", "run_current")
ASSERT len(result) == 2
ASSERT all(r.run_id != "run_current" for r in result)
```

### TS-128-2: Query groups by run, returns last session per run

**Requirement:** 128-REQ-1.2
**Type:** unit
**Description:** When a run has multiple coder sessions (retries), only the
last one (by created_at) is returned.

**Preconditions:**
- In-memory DuckDB.
- 3 coder sessions for run_id='run_A': attempts 1, 2, 3 with increasing
  created_at.

**Input:**
- `query_prior_attempts(conn, 'fix-issue-42', 'run_current')`.

**Expected:**
- 1 result (run_A), with the created_at of attempt 3.
- All run_ids in result are distinct.

**Assertion pseudocode:**
```
result = query_prior_attempts(conn, "fix-issue-42", "run_current")
ASSERT len(result) == 1
ASSERT result[0].run_id == "run_A"
ASSERT result[0].created_at == attempt_3_timestamp
```

### TS-128-3: Query respects max_results limit

**Requirement:** 128-REQ-1.2
**Type:** unit
**Description:** Query returns at most `max_results` entries.

**Preconditions:**
- In-memory DuckDB with 5 prior runs for the same issue.

**Input:**
- `query_prior_attempts(conn, 'fix-issue-42', 'run_current', max_results=3)`.

**Expected:**
- List of exactly 3 results (the 3 most recent).

**Assertion pseudocode:**
```
result = query_prior_attempts(conn, "fix-issue-42", "run_current", max_results=3)
ASSERT len(result) == 3
```

### TS-128-4: PriorAttempt dataclass fields

**Requirement:** 128-REQ-1.3
**Type:** unit
**Description:** Verify PriorAttempt has the correct fields.

**Preconditions:**
- PriorAttempt importable.

**Input:**
- Instantiate with all fields.

**Expected:**
- All fields accessible and correct types.

**Assertion pseudocode:**
```
pa = PriorAttempt(run_id="r1", created_at="2026-05-28T10:00:00",
                  status="failed", error_message="boom", model="claude-sonnet")
ASSERT pa.run_id == "r1"
ASSERT pa.status == "failed"
ASSERT pa.error_message == "boom"
ASSERT pa.model == "claude-sonnet"
```

### TS-128-5: Format produces markdown block

**Requirement:** 128-REQ-2.1, 128-REQ-2.2
**Type:** unit
**Description:** Verify format output contains heading and numbered entries.

**Preconditions:**
- List of 2 PriorAttempt instances.

**Input:**
- `format_prior_attempts(attempts)`.

**Expected:**
- Output starts with `## Prior Fix Attempts`.
- Contains `1.` and `2.` numbered entries.
- Each entry includes date, status, and model.

**Assertion pseudocode:**
```
result = format_prior_attempts(attempts)
ASSERT "## Prior Fix Attempts" IN result
ASSERT "1." IN result
ASSERT "2." IN result
ASSERT "failed" IN result
```

### TS-128-6: Format truncates long error messages

**Requirement:** 128-REQ-2.2
**Type:** unit
**Description:** Error messages longer than 500 chars are truncated.

**Preconditions:**
- PriorAttempt with error_message of 1000 characters.

**Input:**
- `format_prior_attempts([attempt_with_long_error])`.

**Expected:**
- The rendered error text is at most ~503 characters (500 + `...`).

**Assertion pseudocode:**
```
result = format_prior_attempts([long_error_attempt])
ASSERT "..." IN result
# Error portion should not exceed 503 chars
```

### TS-128-7: Context injected into task prompt

**Requirement:** 128-REQ-3.1
**Type:** unit
**Description:** When prior_context is non-empty, it appears in the task prompt
before the issue description.

**Preconditions:**
- Mock spec and triage result.

**Input:**
- `_build_coder_prompt(spec, triage, prior_context="## Prior Fix Attempts\n...")`.

**Expected:**
- Returned task_prompt contains `## Prior Fix Attempts` before the issue title.

**Assertion pseudocode:**
```
_, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context=ctx)
prior_idx = task_prompt.index("Prior Fix Attempts")
issue_idx = task_prompt.index("Fix the issue")
ASSERT prior_idx < issue_idx
```

### TS-128-8: Empty context leaves prompt unchanged

**Requirement:** 128-REQ-3.2
**Type:** unit
**Description:** When prior_context is empty string, prompt is unchanged.

**Preconditions:**
- Mock spec and triage result.

**Input:**
- `_build_coder_prompt(spec, triage, prior_context="")`.

**Expected:**
- Returned task_prompt does NOT contain `Prior Fix Attempts`.

**Assertion pseudocode:**
```
_, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context="")
ASSERT "Prior Fix Attempts" NOT IN task_prompt
```

### TS-128-9: Pipeline wires query into process_issue

**Requirement:** 128-REQ-4.1, 128-REQ-4.2
**Type:** unit
**Description:** process_issue calls query_prior_attempts with conn and run_id.

**Preconditions:**
- Mock platform, mock DuckDB connection.

**Input:**
- Invoke process_issue with a mock issue.

**Expected:**
- `query_prior_attempts` was called with the pipeline's conn, the correct
  spec_name, and the pipeline's run_id.

**Assertion pseudocode:**
```
with patch("...query_prior_attempts") as mock_query:
    mock_query.return_value = []
    pipeline.process_issue(issue)
ASSERT mock_query.called
ASSERT mock_query.call_args[1]["spec_name"] == "fix-issue-42"
ASSERT mock_query.call_args[1]["current_run_id"] == pipeline._run_id
```

## Edge Case Tests

### TS-128-E1: No prior sessions exist

**Requirement:** 128-REQ-1.E1
**Type:** unit
**Description:** Query returns empty list when no prior sessions exist.

**Preconditions:**
- Empty `session_outcomes` table.

**Input:**
- `query_prior_attempts(conn, 'fix-issue-99', 'run_current')`.

**Expected:**
- Empty list.

**Assertion pseudocode:**
```
result = query_prior_attempts(conn, "fix-issue-99", "run_current")
ASSERT result == []
```

### TS-128-E2: Database query failure

**Requirement:** 128-REQ-1.E2
**Type:** unit
**Description:** Query catches exceptions and returns empty list.

**Preconditions:**
- DuckDB connection that raises on execute.

**Input:**
- `query_prior_attempts(broken_conn, 'fix-issue-42', 'run_current')`.

**Expected:**
- Empty list returned (no exception raised).
- Warning logged.

**Assertion pseudocode:**
```
broken_conn = MagicMock()
broken_conn.execute.side_effect = Exception("connection error")
result = query_prior_attempts(broken_conn, "fix-issue-42", "run_current")
ASSERT result == []
```

### TS-128-E3: Format with empty list

**Requirement:** 128-REQ-2.3
**Type:** unit
**Description:** Format returns empty string for empty input.

**Preconditions:** None.

**Input:**
- `format_prior_attempts([])`.

**Expected:**
- `""`.

**Assertion pseudocode:**
```
ASSERT format_prior_attempts([]) == ""
```

## Property Test Cases

### TS-128-P1: Current run always excluded

**Property:** Property 1 from design.md
**Validates:** 128-REQ-1.1, 128-REQ-4.2
**Type:** property
**Description:** For any set of sessions, the current run never appears in results.

**For any:** Random set of session outcome rows with varying run_ids, one
designated as current.
**Invariant:** No returned PriorAttempt has run_id equal to current_run_id.

**Assertion pseudocode:**
```
FOR ANY sessions, current_run_id IN generate_sessions():
    insert_all(conn, sessions)
    result = query_prior_attempts(conn, spec_name, current_run_id)
    ASSERT all(r.run_id != current_run_id for r in result)
```

### TS-128-P2: One entry per run

**Property:** Property 2 from design.md
**Validates:** 128-REQ-1.2
**Type:** property
**Description:** All run_ids in the result are distinct.

**For any:** Random set of session outcome rows with multiple sessions per run.
**Invariant:** `len(set(r.run_id for r in result)) == len(result)`.

**Assertion pseudocode:**
```
FOR ANY sessions IN generate_multi_session_runs():
    insert_all(conn, sessions)
    result = query_prior_attempts(conn, spec_name, "other_run")
    run_ids = [r.run_id for r in result]
    ASSERT len(set(run_ids)) == len(run_ids)
```

### TS-128-P3: Result bounded by max_results

**Property:** Property 3 from design.md
**Validates:** 128-REQ-1.2
**Type:** property
**Description:** Result length never exceeds max_results.

**For any:** N runs (1 <= N <= 20) and max_results M (1 <= M <= 10).
**Invariant:** `len(result) <= M`.

**Assertion pseudocode:**
```
FOR ANY n_runs IN range(1, 21), max_results IN range(1, 11):
    insert_n_runs(conn, n_runs)
    result = query_prior_attempts(conn, spec_name, "other", max_results=max_results)
    ASSERT len(result) <= max_results
```

### TS-128-P4: Empty in, empty out

**Property:** Property 4 from design.md
**Validates:** 128-REQ-2.3, 128-REQ-3.2
**Type:** property
**Description:** format_prior_attempts([]) always returns "".

**For any:** (unconditional)
**Invariant:** `format_prior_attempts([]) == ""`

**Assertion pseudocode:**
```
ASSERT format_prior_attempts([]) == ""
```

### TS-128-P5: Fail-open on query error

**Property:** Property 5 from design.md
**Validates:** 128-REQ-1.E2
**Type:** unit
**Description:** Any exception in the query returns empty list.

**For any:** Exception types (RuntimeError, duckdb.CatalogException, etc.)
**Invariant:** Function returns `[]` and does not raise.

**Assertion pseudocode:**
```
FOR ANY exc_type IN [RuntimeError, CatalogException, IOError]:
    mock_conn.execute.side_effect = exc_type("test")
    result = query_prior_attempts(mock_conn, "fix-issue-1", "run")
    ASSERT result == []
```

## Integration Smoke Tests

### TS-128-SMOKE-1: Full pipeline with prior attempts

**Execution Path:** Path 1 from design.md
**Description:** End-to-end test that prior attempt context appears in the
coder's task prompt when processing an issue with history.

**Setup:** In-memory DuckDB with session_outcomes rows for
`fix-issue-42` from a prior run. Mock platform (GitHub API). Real
FixPipeline with real _build_coder_prompt.

**Trigger:** Call the prompt-building portion of the pipeline for issue #42.

**Expected side effects:**
- The task prompt contains `## Prior Fix Attempts`.
- The prior attempt's error message appears in the prompt.

**Must NOT satisfy with:** Mocking `query_prior_attempts` or
`_build_coder_prompt`.

**Assertion pseudocode:**
```
# Insert prior session into real DuckDB
insert_session(conn, spec_name="fix-issue-42", run_id="old_run",
               archetype="coder", status="failed", error_message="merge conflict")
# Build prompt using real pipeline code
prior = query_prior_attempts(conn, "fix-issue-42", "new_run")
ctx = format_prior_attempts(prior)
_, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context=ctx)
ASSERT "Prior Fix Attempts" IN task_prompt
ASSERT "merge conflict" IN task_prompt
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 128-REQ-1.1 | TS-128-1 | unit |
| 128-REQ-1.2 | TS-128-2, TS-128-3 | unit |
| 128-REQ-1.3 | TS-128-4 | unit |
| 128-REQ-1.E1 | TS-128-E1 | unit |
| 128-REQ-1.E2 | TS-128-E2 | unit |
| 128-REQ-2.1 | TS-128-5 | unit |
| 128-REQ-2.2 | TS-128-5, TS-128-6 | unit |
| 128-REQ-2.3 | TS-128-E3 | unit |
| 128-REQ-3.1 | TS-128-7 | unit |
| 128-REQ-3.2 | TS-128-8 | unit |
| 128-REQ-4.1 | TS-128-9 | unit |
| 128-REQ-4.2 | TS-128-9 | unit |
| Property 1 | TS-128-P1 | property |
| Property 2 | TS-128-P2 | property |
| Property 3 | TS-128-P3 | property |
| Property 4 | TS-128-P4 | property |
| Property 5 | TS-128-P5 | unit |
