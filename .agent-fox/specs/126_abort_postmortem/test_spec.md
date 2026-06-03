# Test Specification: Abort Post-Mortem Dump

## Overview

Tests validate that post-mortem files are generated with the correct schema
for the right trigger conditions, and that the feature degrades gracefully
on errors. Unit tests verify the pure functions (`should_dump`,
`build_postmortem`, `write_postmortem`), property tests verify invariants
across random states, and integration tests verify end-to-end wiring through
`run_code()` and `_print_summary()`.

## Test Cases

### TS-126-1: should_dump returns True for trigger statuses

**Requirement:** 126-REQ-1.1
**Type:** unit
**Description:** Verify that `should_dump()` returns True for each trigger
status.

**Preconditions:**
- An `ExecutionState` instance exists.

**Input:**
- Four states with `run_status` set to each of: `"stalled"`,
  `"block_limit"`, `"cost_limit"`, `"session_limit"`.

**Expected:**
- `should_dump()` returns `True` for all four.

**Assertion pseudocode:**
```
FOR EACH status IN ["stalled", "block_limit", "cost_limit", "session_limit"]:
    state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
    ASSERT should_dump(state) == True
```

### TS-126-2: should_dump returns False for non-trigger statuses

**Requirement:** 126-REQ-1.2, 126-REQ-1.3
**Type:** unit
**Description:** Verify that `should_dump()` returns False for completed,
interrupted, and running statuses.

**Preconditions:**
- An `ExecutionState` instance exists.

**Input:**
- Three states with `run_status` set to each of: `"completed"`,
  `"interrupted"`, `"running"`.

**Expected:**
- `should_dump()` returns `False` for all three.

**Assertion pseudocode:**
```
FOR EACH status IN ["completed", "interrupted", "running"]:
    state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
    ASSERT should_dump(state) == False
```

### TS-126-3: build_postmortem includes all required top-level keys

**Requirement:** 126-REQ-3.1, 126-REQ-3.2
**Type:** unit
**Description:** Verify that the output dict has all required keys and
schema_version is 1.

**Preconditions:**
- An `ExecutionState` with run_id, node_states, session_history populated.

**Input:**
- State with `run_id="20260603_100000_abc123"`, `run_status="stalled"`,
  `node_states={"a": "completed", "b": "blocked"}`,
  `blocked_reasons={"b": "test reason"}`,
  one `SessionRecord` in `session_history`.

**Expected:**
- Result dict has keys: `schema_version`, `run_id`, `run_status`,
  `started_at`, `completed_at`, `task_summary`, `cost_summary`,
  `blocked_tasks`, `session_history`.
- `schema_version` equals `1`.

**Assertion pseudocode:**
```
state = ExecutionState(...)
result = build_postmortem(state)
REQUIRED_KEYS = {"schema_version", "run_id", "run_status", "started_at",
                 "completed_at", "task_summary", "cost_summary",
                 "blocked_tasks", "session_history"}
ASSERT set(result.keys()) == REQUIRED_KEYS
ASSERT result["schema_version"] == 1
```

### TS-126-4: build_postmortem task_summary counts match node_states

**Requirement:** 126-REQ-3.3
**Type:** unit
**Description:** Verify task_summary counts are derived correctly from
node_states.

**Preconditions:**
- An `ExecutionState` with known node states.

**Input:**
- `node_states = {"a": "completed", "b": "blocked", "c": "pending",
  "d": "failed", "e": "completed", "f": "in_progress"}`

**Expected:**
- `task_summary` = `{"total": 6, "completed": 2, "blocked": 1,
  "pending": 1, "failed": 1, "in_progress": 1}`

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="h", node_states=input_states)
result = build_postmortem(state)
ASSERT result["task_summary"]["total"] == 6
ASSERT result["task_summary"]["completed"] == 2
ASSERT result["task_summary"]["blocked"] == 1
ASSERT result["task_summary"]["pending"] == 1
ASSERT result["task_summary"]["failed"] == 1
ASSERT result["task_summary"]["in_progress"] == 1
```

### TS-126-5: build_postmortem cost_summary matches state totals

**Requirement:** 126-REQ-3.4, 126-REQ-5.2
**Type:** unit
**Description:** Verify cost_summary fields match ExecutionState aggregates.

**Preconditions:**
- An `ExecutionState` with known cost values.

**Input:**
- `total_cost=1.23`, `total_input_tokens=100000`,
  `total_output_tokens=50000`, `total_sessions=8`

**Expected:**
- `cost_summary` = `{"total_cost_usd": 1.23, "total_input_tokens": 100000,
  "total_output_tokens": 50000, "total_sessions": 8}`

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="h", node_states={}, total_cost=1.23,
                       total_input_tokens=100000, total_output_tokens=50000,
                       total_sessions=8)
result = build_postmortem(state)
ASSERT result["cost_summary"]["total_cost_usd"] == 1.23
ASSERT result["cost_summary"]["total_input_tokens"] == 100000
ASSERT result["cost_summary"]["total_output_tokens"] == 50000
ASSERT result["cost_summary"]["total_sessions"] == 8
```

### TS-126-6: build_postmortem blocked_tasks sorted and complete

**Requirement:** 126-REQ-4.1, 126-REQ-4.2
**Type:** unit
**Description:** Verify blocked tasks are included, sorted by node_id.

**Preconditions:**
- An `ExecutionState` with multiple blocked tasks.

**Input:**
- `node_states = {"z_task": "blocked", "a_task": "blocked", "m_task": "completed"}`
- `blocked_reasons = {"z_task": "cascade", "a_task": "review findings"}`

**Expected:**
- `blocked_tasks` = `[{"node_id": "a_task", "reason": "review findings"},
  {"node_id": "z_task", "reason": "cascade"}]`

**Assertion pseudocode:**
```
result = build_postmortem(state)
ASSERT len(result["blocked_tasks"]) == 2
ASSERT result["blocked_tasks"][0]["node_id"] == "a_task"
ASSERT result["blocked_tasks"][1]["node_id"] == "z_task"
```

### TS-126-7: build_postmortem session_history includes all records

**Requirement:** 126-REQ-5.1
**Type:** unit
**Description:** Verify all SessionRecords are serialized into
session_history with all required fields.

**Preconditions:**
- An `ExecutionState` with two SessionRecords.

**Input:**
- Two SessionRecords with different statuses and error messages.

**Expected:**
- `session_history` has 2 entries, each containing: `node_id`, `attempt`,
  `status`, `archetype`, `model`, `duration_ms`, `cost`, `error_message`,
  `timestamp`, `is_transport_error`, `is_budget_exhausted`,
  `is_non_retryable`.

**Assertion pseudocode:**
```
result = build_postmortem(state)
ASSERT len(result["session_history"]) == 2
FOR entry IN result["session_history"]:
    ASSERT "node_id" IN entry
    ASSERT "attempt" IN entry
    ASSERT "status" IN entry
    ASSERT "error_message" IN entry
    ASSERT "is_transport_error" IN entry
    ASSERT "is_budget_exhausted" IN entry
    ASSERT "is_non_retryable" IN entry
```

### TS-126-8: write_postmortem creates file with correct name and content

**Requirement:** 126-REQ-2.1, 126-REQ-2.2
**Type:** unit
**Description:** Verify file is written to the correct path with valid JSON.

**Preconditions:**
- A temporary directory as audit_dir.

**Input:**
- A post-mortem dict with `run_id="20260603_100000_abc123"`.

**Expected:**
- File exists at `{audit_dir}/postmortem_20260603_100000_abc123.json`.
- File contents parse as valid JSON equal to the input dict.

**Assertion pseudocode:**
```
path = write_postmortem(postmortem, tmp_dir)
ASSERT path.name == "postmortem_20260603_100000_abc123.json"
ASSERT path.exists()
parsed = json.loads(path.read_text())
ASSERT parsed == postmortem
```

### TS-126-9: write_postmortem creates audit directory if missing

**Requirement:** 126-REQ-2.3
**Type:** unit
**Description:** Verify the audit directory is created when it doesn't exist.

**Preconditions:**
- A path to a non-existent directory.

**Input:**
- `audit_dir` pointing to `{tmp}/nonexistent/audit`.

**Expected:**
- The directory is created and the file is written successfully.

**Assertion pseudocode:**
```
audit_dir = tmp_path / "nonexistent" / "audit"
ASSERT NOT audit_dir.exists()
path = write_postmortem(postmortem, audit_dir)
ASSERT audit_dir.exists()
ASSERT path.exists()
```

### TS-126-10: CLI prints post-mortem path when present

**Requirement:** 126-REQ-6.1
**Type:** unit
**Description:** Verify `_print_summary()` outputs the post-mortem path.

**Preconditions:**
- An `ExecutionState` with `postmortem_path` set.

**Input:**
- `state.postmortem_path = ".agent-fox/audit/postmortem_123.json"`
- `state.node_states = {"a": "blocked"}`, `state.run_status = "stalled"`

**Expected:**
- stdout contains `"Post-mortem: .agent-fox/audit/postmortem_123.json"`.

**Assertion pseudocode:**
```
state = ExecutionState(..., postmortem_path=".agent-fox/audit/postmortem_123.json")
output = capture_stdout(_print_summary(state))
ASSERT "Post-mortem: .agent-fox/audit/postmortem_123.json" IN output
```

### TS-126-11: CLI does not print post-mortem path when absent

**Requirement:** 126-REQ-6.2
**Type:** unit
**Description:** Verify `_print_summary()` omits post-mortem line when
path is empty.

**Preconditions:**
- An `ExecutionState` with empty `postmortem_path`.

**Input:**
- `state.postmortem_path = ""`, `state.run_status = "completed"`

**Expected:**
- stdout does NOT contain `"Post-mortem:"`.

**Assertion pseudocode:**
```
state = ExecutionState(..., postmortem_path="")
output = capture_stdout(_print_summary(state))
ASSERT "Post-mortem:" NOT IN output
```

### TS-126-12: ExecutionState has run_id field

**Requirement:** 126-REQ-7.1
**Type:** unit
**Description:** Verify run_id field exists with correct default.

**Preconditions:**
- None.

**Input:**
- Default-constructed `ExecutionState`.

**Expected:**
- `state.run_id` equals `""`.

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="h", node_states={})
ASSERT state.run_id == ""
ASSERT hasattr(state, "run_id")
```

## Property Test Cases

### TS-126-P1: Trigger completeness

**Property:** Property 1 from design.md
**Validates:** 126-REQ-1.1
**Type:** property
**Description:** For any trigger status, should_dump returns True.

**For any:** `status` drawn from `{"stalled", "block_limit", "cost_limit",
"session_limit"}`
**Invariant:** `should_dump(ExecutionState(plan_hash="h", node_states={},
run_status=status))` is `True`.

**Assertion pseudocode:**
```
FOR ANY status IN sampled_from(["stalled", "block_limit", "cost_limit", "session_limit"]):
    state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
    ASSERT should_dump(state) == True
```

### TS-126-P2: No false triggers

**Property:** Property 2 from design.md
**Validates:** 126-REQ-1.2, 126-REQ-1.3
**Type:** property
**Description:** For any non-trigger status, should_dump returns False.

**For any:** `status` drawn from `{"completed", "interrupted", "running"}`
**Invariant:** `should_dump(ExecutionState(..., run_status=status))` is
`False`.

**Assertion pseudocode:**
```
FOR ANY status IN sampled_from(["completed", "interrupted", "running"]):
    state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
    ASSERT should_dump(state) == False
```

### TS-126-P3: Schema completeness

**Property:** Property 3 from design.md
**Validates:** 126-REQ-3.1, 126-REQ-3.2
**Type:** property
**Description:** For any valid ExecutionState, build_postmortem produces a
dict with all required keys and schema_version == 1.

**For any:** `ExecutionState` with arbitrary `node_states` (dict of str to
status strings), `session_history` (list of SessionRecords), `run_id`
(text), and numeric cost/token fields.
**Invariant:** All required keys present and `schema_version == 1`.

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    result = build_postmortem(state)
    REQUIRED = {"schema_version", "run_id", "run_status", "started_at",
                "completed_at", "task_summary", "cost_summary",
                "blocked_tasks", "session_history"}
    ASSERT REQUIRED.issubset(set(result.keys()))
    ASSERT result["schema_version"] == 1
```

### TS-126-P4: Blocked task fidelity

**Property:** Property 4 from design.md
**Validates:** 126-REQ-4.1, 126-REQ-4.E1
**Type:** property
**Description:** The blocked_tasks array has one entry per blocked node
in node_states.

**For any:** `ExecutionState` with arbitrary `node_states` containing some
"blocked" entries, and `blocked_reasons` that may or may not cover all
blocked nodes.
**Invariant:** `len(result["blocked_tasks"])` equals the count of "blocked"
entries in `node_states`. Each entry has non-empty `node_id` and `reason`.

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    result = build_postmortem(state)
    blocked_count = sum(1 for s in state.node_states.values() if s == "blocked")
    ASSERT len(result["blocked_tasks"]) == blocked_count
    FOR entry IN result["blocked_tasks"]:
        ASSERT len(entry["node_id"]) > 0
        ASSERT len(entry["reason"]) > 0
```

### TS-126-P5: Session history fidelity

**Property:** Property 5 from design.md
**Validates:** 126-REQ-5.1
**Type:** property
**Description:** session_history array length matches state.session_history.

**For any:** `ExecutionState` with 0-10 `SessionRecord` entries.
**Invariant:** `len(result["session_history"]) == len(state.session_history)`

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    result = build_postmortem(state)
    ASSERT len(result["session_history"]) == len(state.session_history)
```

### TS-126-P6: Cost summary accuracy

**Property:** Property 6 from design.md
**Validates:** 126-REQ-5.2
**Type:** property
**Description:** cost_summary fields equal state aggregate values.

**For any:** `ExecutionState` with arbitrary cost/token values.
**Invariant:** `cost_summary` matches state totals exactly.

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    result = build_postmortem(state)
    ASSERT result["cost_summary"]["total_cost_usd"] == state.total_cost
    ASSERT result["cost_summary"]["total_input_tokens"] == state.total_input_tokens
    ASSERT result["cost_summary"]["total_output_tokens"] == state.total_output_tokens
    ASSERT result["cost_summary"]["total_sessions"] == state.total_sessions
```

### TS-126-P7: File round-trip

**Property:** Property 7 from design.md
**Validates:** 126-REQ-2.2
**Type:** property
**Description:** Writing and reading back produces identical dict.

**For any:** Post-mortem dict built from any valid `ExecutionState`.
**Invariant:** `json.loads(path.read_text()) == postmortem`

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    pm = build_postmortem(state)
    path = write_postmortem(pm, tmp_dir)
    parsed = json.loads(path.read_text())
    ASSERT parsed == pm
```

### TS-126-P8: Task summary accuracy

**Property:** Property 8 from design.md
**Validates:** 126-REQ-3.3
**Type:** property
**Description:** task_summary.total equals len(node_states), and status
counts sum to total.

**For any:** `ExecutionState` with arbitrary `node_states`.
**Invariant:** `total == len(node_states)` and sum of all counts equals
`total`.

**Assertion pseudocode:**
```
FOR ANY state IN execution_state_strategy():
    result = build_postmortem(state)
    ts = result["task_summary"]
    ASSERT ts["total"] == len(state.node_states)
    count_sum = ts["completed"] + ts["pending"] + ts["blocked"] + ts["failed"] + ts["in_progress"]
    ASSERT count_sum <= ts["total"]  # may have other statuses like "deferred"
```

## Edge Case Tests

### TS-126-E1: Post-mortem generation failure is non-blocking

**Requirement:** 126-REQ-1.E1
**Type:** unit
**Description:** If build_postmortem raises, run_code still returns state.

**Preconditions:**
- `build_postmortem` is patched to raise `RuntimeError`.

**Input:**
- A mock orchestrator returning state with `run_status="stalled"`.

**Expected:**
- `run_code()` returns the state without crashing.
- `state.postmortem_path` is empty.
- A warning is logged.

**Assertion pseudocode:**
```
with patch("postmortem.build_postmortem", side_effect=RuntimeError("boom")):
    state = await run_code(config)
    ASSERT state.postmortem_path == ""
    ASSERT state.run_status == "stalled"
```

### TS-126-E2: Fallback run_id for empty state

**Requirement:** 126-REQ-1.E2
**Type:** unit
**Description:** When run_id is empty, build_postmortem uses a fallback.

**Preconditions:**
- An `ExecutionState` with `run_id=""`.

**Input:**
- State from `_stalled_result()` (empty run_id).

**Expected:**
- `build_postmortem()` returns a dict with a non-empty `run_id` string.

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="", node_states={}, run_id="")
result = build_postmortem(state)
ASSERT len(result["run_id"]) > 0
```

### TS-126-E3: Blocked task with missing reason

**Requirement:** 126-REQ-4.E1
**Type:** unit
**Description:** A blocked node not in blocked_reasons gets reason "unknown".

**Preconditions:**
- Node "x" has status "blocked" but no entry in `blocked_reasons`.

**Input:**
- `node_states={"x": "blocked"}`, `blocked_reasons={}`

**Expected:**
- `blocked_tasks` = `[{"node_id": "x", "reason": "unknown"}]`

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="h",
                       node_states={"x": "blocked"},
                       blocked_reasons={})
result = build_postmortem(state)
ASSERT result["blocked_tasks"] == [{"node_id": "x", "reason": "unknown"}]
```

### TS-126-E4: Empty session history produces valid output

**Requirement:** 126-REQ-5.E1
**Type:** unit
**Description:** An empty session_history produces empty arrays and zero
cost values.

**Preconditions:**
- An `ExecutionState` with empty session_history and zero costs.

**Input:**
- Default state with no sessions.

**Expected:**
- `session_history` = `[]`
- `cost_summary` all zeros.
- `blocked_tasks` = `[]`

**Assertion pseudocode:**
```
state = ExecutionState(plan_hash="h", node_states={}, run_status="stalled")
result = build_postmortem(state)
ASSERT result["session_history"] == []
ASSERT result["blocked_tasks"] == []
ASSERT result["cost_summary"]["total_cost_usd"] == 0.0
ASSERT result["cost_summary"]["total_sessions"] == 0
```

### TS-126-E5: File write failure is non-blocking

**Requirement:** 126-REQ-2.E1
**Type:** unit
**Description:** If file writing fails, warning is logged, no crash.

**Preconditions:**
- `audit_dir` is a path that cannot be written to.

**Input:**
- A valid post-mortem dict, `audit_dir` set to a read-only path.

**Expected:**
- `write_postmortem()` raises (or returns gracefully).
- The caller (run_code) catches the exception and continues.

**Assertion pseudocode:**
```
with patch("pathlib.Path.write_text", side_effect=PermissionError):
    # The try/except in run_code should catch this
    state = await run_code(config)
    ASSERT state.postmortem_path == ""
```

## Integration Smoke Tests

### TS-126-SMOKE-1: Post-mortem generated on stalled run

**Execution Path:** Path 1 from design.md
**Description:** Verify that a complete stalled run produces a post-mortem
file with correct content.

**Setup:** Mock the session runner factory to produce a failing result that
causes task blocking. Do NOT mock `postmortem.build_postmortem` or
`postmortem.write_postmortem` — these must be the real implementations.
Use a real `ExecutionState` and a temporary audit directory.

**Trigger:** Call `run_code()` with config that leads to a stalled state
(e.g., a single task that fails and exhausts retries).

**Expected side effects:**
- A file exists at `{audit_dir}/postmortem_{run_id}.json`.
- The file contains valid JSON with `schema_version: 1`.
- `state.postmortem_path` is a non-empty string.
- `state.run_status` is `"stalled"` or `"block_limit"`.

**Must NOT satisfy with:** Mocking `build_postmortem` or
`write_postmortem` — the real code path must execute.

**Assertion pseudocode:**
```
state = await run_code(config_with_failing_task)
ASSERT state.run_status in ("stalled", "block_limit")
ASSERT state.postmortem_path != ""
path = Path(state.postmortem_path)
ASSERT path.exists()
parsed = json.loads(path.read_text())
ASSERT parsed["schema_version"] == 1
ASSERT parsed["run_status"] == state.run_status
ASSERT len(parsed["blocked_tasks"]) > 0
```

### TS-126-SMOKE-2: CLI displays post-mortem path on non-successful run

**Execution Path:** Path 2 from design.md
**Description:** Verify _print_summary outputs the post-mortem path.

**Setup:** An `ExecutionState` with `postmortem_path` set and
`run_status="block_limit"`. Do NOT mock `_print_summary` — the real
function must execute.

**Trigger:** Call `_print_summary(state)`.

**Expected side effects:**
- stdout contains `"Post-mortem:"` followed by the file path.

**Must NOT satisfy with:** Mocking `_print_summary` or `click.echo`.

**Assertion pseudocode:**
```
state = ExecutionState(..., postmortem_path="/tmp/postmortem_abc.json",
                       run_status="block_limit",
                       node_states={"a": "blocked"})
output = capture_click_output(_print_summary, state)
ASSERT "Post-mortem: /tmp/postmortem_abc.json" IN output
```

### TS-126-SMOKE-3: No post-mortem on completed run

**Execution Path:** Path 3 from design.md
**Description:** Verify that a successful run does not produce a
post-mortem file.

**Setup:** Mock the session runner to produce all-passing results. Use
a real Orchestrator with a simple single-task graph. Temporary audit
directory.

**Trigger:** Call `run_code()` with config that leads to a completed state.

**Expected side effects:**
- No `postmortem_*.json` file exists in the audit directory.
- `state.postmortem_path` is empty.
- `state.run_status` is `"completed"`.

**Must NOT satisfy with:** Mocking `should_dump` — the real function
must evaluate the condition.

**Assertion pseudocode:**
```
state = await run_code(config_with_passing_task)
ASSERT state.run_status == "completed"
ASSERT state.postmortem_path == ""
postmortem_files = list(audit_dir.glob("postmortem_*.json"))
ASSERT len(postmortem_files) == 0
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 126-REQ-1.1 | TS-126-1 | unit |
| 126-REQ-1.2 | TS-126-2 | unit |
| 126-REQ-1.3 | TS-126-2 | unit |
| 126-REQ-1.E1 | TS-126-E1 | unit |
| 126-REQ-1.E2 | TS-126-E2 | unit |
| 126-REQ-2.1 | TS-126-8 | unit |
| 126-REQ-2.2 | TS-126-8 | unit |
| 126-REQ-2.3 | TS-126-9 | unit |
| 126-REQ-2.E1 | TS-126-E5 | unit |
| 126-REQ-3.1 | TS-126-3 | unit |
| 126-REQ-3.2 | TS-126-3 | unit |
| 126-REQ-3.3 | TS-126-4 | unit |
| 126-REQ-3.4 | TS-126-5 | unit |
| 126-REQ-3.5 | TS-126-6 | unit |
| 126-REQ-3.6 | TS-126-7 | unit |
| 126-REQ-4.1 | TS-126-6 | unit |
| 126-REQ-4.2 | TS-126-6 | unit |
| 126-REQ-4.E1 | TS-126-E3 | unit |
| 126-REQ-5.1 | TS-126-7 | unit |
| 126-REQ-5.2 | TS-126-5 | unit |
| 126-REQ-5.E1 | TS-126-E4 | unit |
| 126-REQ-6.1 | TS-126-10 | unit |
| 126-REQ-6.2 | TS-126-11 | unit |
| 126-REQ-7.1 | TS-126-12 | unit |
| 126-REQ-7.2 | TS-126-SMOKE-1 | integration |
| Property 1 | TS-126-P1 | property |
| Property 2 | TS-126-P2 | property |
| Property 3 | TS-126-P3 | property |
| Property 4 | TS-126-P4 | property |
| Property 5 | TS-126-P5 | property |
| Property 6 | TS-126-P6 | property |
| Property 7 | TS-126-P7 | property |
| Property 8 | TS-126-P8 | property |
