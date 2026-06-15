# Test Specification: --dry-run Flag on code Command

## Overview

Test cases for the `--dry-run` flag on the `code` command. Tests verify that
the flag loads the persisted plan read-only, displays analysis output, skips
the orchestrator, enforces mutual exclusion with execution flags, and supports
JSON output. All CLI tests use Click's `CliRunner` with mocked database access.

## Test Cases

### TS-123-1: Dry-run displays analysis output

**Requirement:** 123-REQ-1.1
**Type:** unit
**Description:** `code --dry-run` loads the plan and displays analysis output
containing phase headings, critical path, and dependency edges.

**Preconditions:**
- DuckDB file exists at `DEFAULT_DB_PATH`.
- Persisted plan contains nodes A->B->C with intra-spec edges.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Output contains "Plan Analysis".
- Output contains "Phase 0".
- Output contains "Critical Path".
- Output contains "Dependency Edges".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 0
ASSERT "Plan Analysis" IN result.output
ASSERT "Phase 0" IN result.output
ASSERT "Critical Path" IN result.output
ASSERT "Dependency Edges" IN result.output
```

### TS-123-2: Dry-run does not invoke run_code

**Requirement:** 123-REQ-1.2
**Type:** unit
**Description:** `code --dry-run` never calls `run_code()`.

**Preconditions:**
- DuckDB file exists. Persisted plan has at least one node.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- `run_code` mock is never called.

**Assertion pseudocode:**
```
mock_rc = patch("agent_fox.cli.code.run_code")
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT mock_rc.call_count == 0
```

### TS-123-3: Dry-run filters completed nodes

**Requirement:** 123-REQ-1.3
**Type:** unit
**Description:** Completed nodes are excluded from the analysis output.

**Preconditions:**
- Persisted plan has nodes A (completed), B (pending), C (pending), with
  edges A->B, B->C.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Output does not contain node A's title.
- Output contains node B and C titles.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT "Task A" NOT IN result.output
ASSERT "Task B" IN result.output
ASSERT "Task C" IN result.output
```

### TS-123-4: Non-dry-run behavior unchanged

**Requirement:** 123-REQ-1.4
**Type:** unit
**Description:** `code` without `--dry-run` calls `run_code()` as before.

**Preconditions:**
- DuckDB file exists.

**Input:**
- CLI args: `["code"]`

**Expected:**
- `run_code` mock is called exactly once.

**Assertion pseudocode:**
```
mock_rc = patch("agent_fox.cli.code.run_code")
result = cli_runner.invoke(main, ["code"])
ASSERT mock_rc.call_count == 1
```

### TS-123-5: Mutual exclusion with --watch

**Requirement:** 123-REQ-2.1
**Type:** unit
**Description:** `--dry-run --watch` exits with code 1 and error message.

**Preconditions:**
- None (validation happens before plan loading).

**Input:**
- CLI args: `["code", "--dry-run", "--watch"]`

**Expected:**
- Exit code 1.
- Output contains "--watch".
- Output contains "incompatible" or "cannot be combined".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])
ASSERT result.exit_code == 1
ASSERT "--watch" IN result.output
```

### TS-123-6: Mutual exclusion with --debug

**Requirement:** 123-REQ-2.1
**Type:** unit
**Description:** `--dry-run --debug` exits with code 1 and error message.

**Preconditions:**
- None.

**Input:**
- CLI args: `["code", "--dry-run", "--debug"]`

**Expected:**
- Exit code 1.
- Output contains "--debug".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--debug"])
ASSERT result.exit_code == 1
ASSERT "--debug" IN result.output
```

### TS-123-7: Mutual exclusion with --parallel

**Requirement:** 123-REQ-2.1
**Type:** unit
**Description:** `--dry-run --parallel 4` exits with code 1 and error message.

**Preconditions:**
- None.

**Input:**
- CLI args: `["code", "--dry-run", "--parallel", "4"]`

**Expected:**
- Exit code 1.
- Output contains "--parallel".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--parallel", "4"])
ASSERT result.exit_code == 1
ASSERT "--parallel" IN result.output
```

### TS-123-8: Mutual exclusion with --force-clean

**Requirement:** 123-REQ-2.1
**Type:** unit
**Description:** `--dry-run --force-clean` exits with code 1 and error message.

**Preconditions:**
- None.

**Input:**
- CLI args: `["code", "--dry-run", "--force-clean"]`

**Expected:**
- Exit code 1.
- Output contains "--force-clean".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--force-clean"])
ASSERT result.exit_code == 1
ASSERT "--force-clean" IN result.output
```

### TS-123-9: JSON output

**Requirement:** 123-REQ-3.1
**Type:** unit
**Description:** `code --dry-run` with `--json` outputs a valid JSON object
with all required keys.

**Preconditions:**
- DuckDB file exists. Persisted plan has nodes and edges.

**Input:**
- CLI args: `["--json", "code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Output parses as valid JSON.
- JSON contains keys: `nodes`, `edges`, `order`, `metadata`, `phases`,
  `critical_path`, `grouped_edges`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])
ASSERT result.exit_code == 0
data = json.loads(result.output)
ASSERT "nodes" IN data
ASSERT "edges" IN data
ASSERT "order" IN data
ASSERT "metadata" IN data
ASSERT "phases" IN data
ASSERT "critical_path" IN data
ASSERT "grouped_edges" IN data
```

### TS-123-10: Daemon guard bypassed in dry-run

**Requirement:** 123-REQ-4.1
**Type:** unit
**Description:** `code --dry-run` succeeds even when the daemon PID check
would report ALIVE.

**Preconditions:**
- Daemon PID check returns ALIVE.
- DuckDB file exists. Persisted plan has nodes.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Analysis output displayed (not blocked by daemon).

**Assertion pseudocode:**
```
monkeypatch daemon check to return ALIVE
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 0
ASSERT "Plan Analysis" IN result.output
```

### TS-123-11: Daemon guard enforced without dry-run

**Requirement:** 123-REQ-4.2
**Type:** unit
**Description:** `code` without `--dry-run` is blocked by active daemon.

**Preconditions:**
- Daemon PID check returns ALIVE.

**Input:**
- CLI args: `["code"]`

**Expected:**
- Exit code 1.
- Output contains "daemon" or "night-shift".

**Assertion pseudocode:**
```
monkeypatch daemon check to return ALIVE
result = cli_runner.invoke(main, ["code"])
ASSERT result.exit_code == 1
ASSERT "daemon" IN result.output.lower() OR "night-shift" IN result.output.lower()
```

## Edge Case Tests

### TS-123-E1: Missing DB file in dry-run

**Requirement:** 123-REQ-1.E1
**Type:** unit
**Description:** `code --dry-run` with no DB file exits with code 1 and
mentions `plan`.

**Preconditions:**
- `DEFAULT_DB_PATH.exists()` returns False.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Exit code 1.
- Output contains "plan" (tells user to run `agent-fox plan`).

**Assertion pseudocode:**
```
mock DEFAULT_DB_PATH.exists() to return False
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 1
ASSERT "plan" IN result.output.lower()
```

### TS-123-E2: Empty plan in dry-run

**Requirement:** 123-REQ-1.E2
**Type:** unit
**Description:** `code --dry-run` with empty persisted plan displays message.

**Preconditions:**
- DuckDB file exists. `load_plan()` returns a TaskGraph with no nodes.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Output contains "No tasks in plan."

**Assertion pseudocode:**
```
mock load_plan to return TaskGraph(nodes={}, edges=[], order=[])
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 0
ASSERT "No tasks in plan" IN result.output
```

### TS-123-E3: All nodes completed in dry-run

**Requirement:** 123-REQ-1.E3
**Type:** unit
**Description:** `code --dry-run` with all nodes completed displays message.

**Preconditions:**
- DuckDB file exists. All nodes in persisted plan have status COMPLETED.

**Input:**
- CLI args: `["code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Output contains "All tasks completed."

**Assertion pseudocode:**
```
mock load_plan to return graph where all nodes are COMPLETED
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 0
ASSERT "All tasks completed" IN result.output
```

### TS-123-E4: Multiple incompatible flags

**Requirement:** 123-REQ-2.E1
**Type:** unit
**Description:** `--dry-run --watch --debug` lists all incompatible flags.

**Preconditions:**
- None.

**Input:**
- CLI args: `["code", "--dry-run", "--watch", "--debug"]`

**Expected:**
- Exit code 1.
- Output contains "--watch" and "--debug".

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--watch", "--debug"])
ASSERT result.exit_code == 1
ASSERT "--watch" IN result.output
ASSERT "--debug" IN result.output
```

### TS-123-E5: Empty plan JSON output

**Requirement:** 123-REQ-3.E1
**Type:** unit
**Description:** `--dry-run --json` with all-completed plan outputs valid JSON
with empty collections.

**Preconditions:**
- All nodes COMPLETED.

**Input:**
- CLI args: `["--json", "code", "--dry-run"]`

**Expected:**
- Exit code 0.
- Valid JSON with `nodes` = {}, `edges` = [], `order` = [].

**Assertion pseudocode:**
```
mock load_plan with all-completed graph
result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])
data = json.loads(result.output)
ASSERT data["nodes"] == {}
ASSERT data["edges"] == []
ASSERT data["order"] == []
```

## Property Test Cases

### TS-123-P1: No orchestrator invocation

**Property:** Property 1 from design.md
**Validates:** 123-REQ-1.1, 123-REQ-1.2
**Type:** property
**Description:** `code --dry-run` never calls `run_code()` regardless of plan
content.

**For any:** plan with 0 to 20 nodes in arbitrary status combinations
**Invariant:** `run_code` is never called when `--dry-run` is set.

**Assertion pseudocode:**
```
FOR ANY graph IN graphs_with_random_statuses(0, 20):
    mock load_plan to return graph
    mock run_code
    result = cli_runner.invoke(main, ["code", "--dry-run"])
    ASSERT mock_run_code.call_count == 0
```

### TS-123-P2: Completed node exclusion

**Property:** Property 2 from design.md
**Validates:** 123-REQ-1.3
**Type:** property
**Description:** Analysis output contains only non-completed node IDs.

**For any:** plan with 1 to 10 nodes where some subset is COMPLETED
**Invariant:** No completed node ID appears in the output text.

**Assertion pseudocode:**
```
FOR ANY graph, completed_ids IN graphs_with_partial_completion(1, 10):
    mock load_plan to return graph
    result = cli_runner.invoke(main, ["code", "--dry-run"])
    FOR id IN completed_ids:
        ASSERT id NOT IN result.output
```

### TS-123-P3: Mutual exclusion enforcement

**Property:** Property 3 from design.md
**Validates:** 123-REQ-2.1, 123-REQ-2.E1
**Type:** property
**Description:** Any combination of `--dry-run` with execution flags exits 1.

**For any:** non-empty subset of {--watch, --debug, --force-clean, --parallel 1}
**Invariant:** Exit code is 1 and load_plan is never called.

**Assertion pseudocode:**
```
FOR ANY flags IN non_empty_subsets(["--watch", "--debug", "--force-clean", "--parallel 1"]):
    mock load_plan
    result = cli_runner.invoke(main, ["code", "--dry-run"] + flags)
    ASSERT result.exit_code == 1
    ASSERT mock_load_plan.call_count == 0
```

### TS-123-P4: Read-only database access

**Property:** Property 5 from design.md
**Validates:** 123-REQ-1.1, 123-REQ-1.2
**Type:** property
**Description:** `code --dry-run` never calls `save_plan()`.

**For any:** plan with 0 to 10 nodes
**Invariant:** `save_plan` is never called.

**Assertion pseudocode:**
```
FOR ANY graph IN graphs(0, 10):
    mock load_plan, mock save_plan
    result = cli_runner.invoke(main, ["code", "--dry-run"])
    ASSERT mock_save_plan.call_count == 0
```

### TS-123-P5: Daemon guard bypass

**Property:** Property 6 from design.md
**Validates:** 123-REQ-4.1
**Type:** property
**Description:** `code --dry-run` succeeds regardless of daemon state.

**For any:** daemon state in {ALIVE, ABSENT, STALE}
**Invariant:** When `--dry-run` is set, exit code is not 1 due to daemon.

**Assertion pseudocode:**
```
FOR ANY state IN [ALIVE, ABSENT, STALE]:
    monkeypatch daemon check to return state
    mock load_plan with valid graph
    result = cli_runner.invoke(main, ["code", "--dry-run"])
    ASSERT result.exit_code == 0
```

## Integration Smoke Tests

### TS-123-SMOKE-1: Full dry-run text output

**Execution Path:** Path 1 from design.md
**Description:** End-to-end dry-run with text output using mocked DB returns
complete analysis.

**Setup:** Mock `open_knowledge_store` and `load_plan` to return a graph with
3 nodes (A->B->C, one intra-spec edge chain). Mock `discover_specs`. Do NOT
mock `compute_phases`, `critical_path`, `group_edges`, or
`format_plan_analysis` -- these are real.

**Trigger:** `cli_runner.invoke(main, ["code", "--dry-run"])`

**Expected side effects:**
- Exit code 0.
- Output contains "Plan Analysis", "Phase 0", "Critical Path",
  "Dependency Edges", "A -> B".

**Must NOT satisfy with:** Mocking `compute_phases`, `critical_path`,
`group_edges`, or `format_plan_analysis`.

**Assertion pseudocode:**
```
knowledge_db = MockKnowledgeDB()
mock open_knowledge_store to return knowledge_db
mock load_plan to return 3-node chain graph
mock discover_specs to return [SpecInfo("test")]
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT result.exit_code == 0
ASSERT "Plan Analysis" IN result.output
ASSERT "Phase 0" IN result.output
ASSERT "Critical Path" IN result.output
```

### TS-123-SMOKE-2: Full dry-run JSON output

**Execution Path:** Path 2 from design.md
**Description:** End-to-end dry-run with JSON output using mocked DB returns
valid structured JSON.

**Setup:** Same as SMOKE-1 but with `--json` flag. Do NOT mock analyzer
functions.

**Trigger:** `cli_runner.invoke(main, ["--json", "code", "--dry-run"])`

**Expected side effects:**
- Exit code 0.
- Output is valid JSON with all required keys.
- `phases` contains at least one phase entry.
- `critical_path` is a non-empty list.

**Must NOT satisfy with:** Mocking analyzer or formatter functions.

**Assertion pseudocode:**
```
mock open_knowledge_store, load_plan, discover_specs
result = cli_runner.invoke(main, ["--json", "code", "--dry-run"])
ASSERT result.exit_code == 0
data = json.loads(result.output)
ASSERT len(data["phases"]) >= 1
ASSERT len(data["critical_path"]) >= 1
```

### TS-123-SMOKE-3: Incompatible flags rejected

**Execution Path:** Path 3 from design.md
**Description:** End-to-end validation that incompatible flags are rejected
before any DB access.

**Setup:** Do NOT mock any DB or plan loading functions.

**Trigger:** `cli_runner.invoke(main, ["code", "--dry-run", "--watch"])`

**Expected side effects:**
- Exit code 1.
- Error output mentions "--watch".
- No DB access occurs.

**Must NOT satisfy with:** Mocking the validation logic itself.

**Assertion pseudocode:**
```
# No mocks needed -- validation happens before DB access
result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])
ASSERT result.exit_code == 1
ASSERT "--watch" IN result.output
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 123-REQ-1.1 | TS-123-1 | unit |
| 123-REQ-1.2 | TS-123-2 | unit |
| 123-REQ-1.3 | TS-123-3 | unit |
| 123-REQ-1.4 | TS-123-4 | unit |
| 123-REQ-1.E1 | TS-123-E1 | unit |
| 123-REQ-1.E2 | TS-123-E2 | unit |
| 123-REQ-1.E3 | TS-123-E3 | unit |
| 123-REQ-2.1 | TS-123-5, TS-123-6, TS-123-7, TS-123-8 | unit |
| 123-REQ-2.E1 | TS-123-E4 | unit |
| 123-REQ-3.1 | TS-123-9 | unit |
| 123-REQ-3.E1 | TS-123-E5 | unit |
| 123-REQ-4.1 | TS-123-10 | unit |
| 123-REQ-4.2 | TS-123-11 | unit |
| Property 1 | TS-123-P1 | property |
| Property 2 | TS-123-P2 | property |
| Property 3 | TS-123-P3 | property |
| Property 5 | TS-123-P4 | property |
| Property 6 | TS-123-P5 | property |
| Path 1 | TS-123-SMOKE-1 | integration |
| Path 2 | TS-123-SMOKE-2 | integration |
| Path 3 | TS-123-SMOKE-3 | integration |
