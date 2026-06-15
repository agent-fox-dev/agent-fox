# Test Specification: Night-Shift Fix-Only Mode

## Overview

Tests verify that hunt-scan and spec-executor code has been completely
removed, that the fix pipeline is preserved, that config backward
compatibility holds, and that the test suite is clean.

## Test Cases

### TS-125-1: Hunt source modules deleted

**Requirement:** 125-REQ-1.1
**Type:** unit
**Description:** Verify all hunt-related source files are deleted.

**Preconditions:**
- Repository is at the post-change state.

**Input:**
- List of file paths that must not exist.

**Expected:**
- None of the listed files exist on disk.

**Assertion pseudocode:**
```
deleted = [
    "agent_fox/nightshift/hunt.py",
    "agent_fox/nightshift/critic.py",
    "agent_fox/nightshift/dedup.py",
    "agent_fox/nightshift/finding.py",
    "agent_fox/nightshift/ignore_filter.py",
    "agent_fox/nightshift/ignore.py",
]
FOR EACH path IN deleted:
    ASSERT NOT exists(path)
```

### TS-125-2: Categories directory deleted

**Requirement:** 125-REQ-1.2
**Type:** unit
**Description:** Verify the categories directory no longer exists.

**Preconditions:** None.

**Input:**
- Path `agent_fox/nightshift/categories/`.

**Expected:**
- Directory does not exist.

**Assertion pseudocode:**
```
ASSERT NOT exists("agent_fox/nightshift/categories/")
```

### TS-125-3: No dangling imports in nightshift package

**Requirement:** 125-REQ-1.3
**Type:** unit
**Description:** Verify no remaining nightshift module imports from deleted modules.

**Preconditions:**
- Deleted modules have been removed.

**Input:**
- All `.py` files under `agent_fox/nightshift/`.

**Expected:**
- No file contains `from agent_fox.nightshift.hunt`,
  `from agent_fox.nightshift.critic`, `from agent_fox.nightshift.dedup`,
  `from agent_fox.nightshift.finding`, `from agent_fox.nightshift.ignore_filter`,
  `from agent_fox.nightshift.ignore`, or `from agent_fox.nightshift.categories`.

**Assertion pseudocode:**
```
deleted_imports = ["hunt", "critic", "dedup", "finding", "ignore_filter", "ignore", "categories"]
FOR EACH py_file IN glob("agent_fox/nightshift/**/*.py"):
    content = read(py_file)
    FOR EACH mod IN deleted_imports:
        ASSERT f"from agent_fox.nightshift.{mod}" NOT IN content
```

### TS-125-4: Engine has no hunt methods

**Requirement:** 125-REQ-2.1
**Type:** unit
**Description:** Verify NightShiftEngine does not have hunt-scan methods.

**Preconditions:** None.

**Input:**
- `NightShiftEngine` class.

**Expected:**
- `_run_hunt_scan` and `_run_hunt_scan_inner` are not attributes.

**Assertion pseudocode:**
```
engine = NightShiftEngine(config, platform)
ASSERT NOT hasattr(engine, "_run_hunt_scan")
ASSERT NOT hasattr(engine, "_run_hunt_scan_inner")
```

### TS-125-5: Engine constructor rejects auto_fix and embedder

**Requirement:** 125-REQ-2.2
**Type:** unit
**Description:** Verify removed constructor parameters raise TypeError.

**Preconditions:** None.

**Input:**
- Attempt to construct NightShiftEngine with `auto_fix=True` or `embedder=mock`.

**Expected:**
- TypeError raised.

**Assertion pseudocode:**
```
ASSERT RAISES TypeError:
    NightShiftEngine(config, platform, auto_fix=True)
ASSERT RAISES TypeError:
    NightShiftEngine(config, platform, embedder=object())
```

### TS-125-6: Engine retains fix-pipeline methods

**Requirement:** 125-REQ-2.4
**Type:** unit
**Description:** Verify fix-pipeline methods are present on the engine.

**Preconditions:** None.

**Input:**
- `NightShiftEngine` class.

**Expected:**
- `_drain_issues`, `_run_issue_check`, `_process_fix` are callable attributes.

**Assertion pseudocode:**
```
engine = NightShiftEngine(config, platform)
ASSERT callable(getattr(engine, "_drain_issues"))
ASSERT callable(getattr(engine, "_run_issue_check"))
ASSERT callable(getattr(engine, "_process_fix"))
```

### TS-125-7: SpecExecutorStream deleted

**Requirement:** 125-REQ-3.1
**Type:** unit
**Description:** Verify SpecExecutorStream is not importable from streams.

**Preconditions:** None.

**Input:**
- Attempt to import `SpecExecutorStream` from `agent_fox.nightshift.streams`.

**Expected:**
- ImportError raised.

**Assertion pseudocode:**
```
ASSERT RAISES ImportError:
    from agent_fox.nightshift.streams import SpecExecutorStream
```

### TS-125-8: build_streams returns single fix stream

**Requirement:** 125-REQ-3.3
**Type:** unit
**Description:** Verify build_streams() returns exactly one fix-pipeline stream.

**Preconditions:**
- Mock config and engine.

**Input:**
- Call `build_streams(config, engine=engine, budget=budget)`.

**Expected:**
- List of length 1 with a stream named "fix-pipeline" and `enabled=True`.

**Assertion pseudocode:**
```
streams = build_streams(config, engine=engine, budget=budget)
ASSERT len(streams) == 1
ASSERT streams[0].name == "fix-pipeline"
ASSERT streams[0].enabled == True
```

### TS-125-9: build_streams with no_fixes disables stream

**Requirement:** 125-REQ-3.E1
**Type:** unit
**Description:** Verify no_fixes=True produces a disabled stream.

**Preconditions:**
- Mock config and engine.

**Input:**
- Call `build_streams(config, no_fixes=True, engine=engine, budget=budget)`.

**Expected:**
- List of length 1 with `enabled=False`.

**Assertion pseudocode:**
```
streams = build_streams(config, no_fixes=True, engine=engine, budget=budget)
ASSERT len(streams) == 1
ASSERT streams[0].enabled == False
```

### TS-125-10: Config backward compatibility

**Requirement:** 125-REQ-5.4
**Type:** unit
**Description:** Verify NightShiftConfig ignores removed fields.

**Preconditions:** None.

**Input:**
- Dict with removed fields: `hunt_scan_interval`, `categories`,
  `quality_gate_timeout`, `spec_interval`, `enabled_streams`,
  `similarity_threshold`.

**Expected:**
- NightShiftConfig constructed without error.
- Retained fields have correct defaults.

**Assertion pseudocode:**
```
cfg = NightShiftConfig(
    hunt_scan_interval=3600,
    quality_gate_timeout=120,
    spec_interval=60,
    enabled_streams=["fixes"],
    similarity_threshold=0.9,
    categories={"dead_code": False},
)
ASSERT cfg.issue_check_interval == 900
ASSERT cfg.push_fix_branch == False
```

### TS-125-11: NightShiftCategoryConfig deleted

**Requirement:** 125-REQ-5.2
**Type:** unit
**Description:** Verify NightShiftCategoryConfig is not importable.

**Preconditions:** None.

**Input:**
- Attempt to import `NightShiftCategoryConfig` from `agent_fox.core.config`.

**Expected:**
- ImportError raised.

**Assertion pseudocode:**
```
ASSERT RAISES ImportError:
    from agent_fox.core.config import NightShiftCategoryConfig
```

### TS-125-12: init_project does not create .night-shift file

**Requirement:** 125-REQ-6.2
**Type:** unit
**Description:** Verify init_project no longer creates a `.night-shift` file.

**Preconditions:**
- A temporary project directory.

**Input:**
- Run init_project in the temp directory.

**Expected:**
- No `.night-shift` file exists in the project root.

**Assertion pseudocode:**
```
init_project(tmp_dir)
ASSERT NOT exists(tmp_dir / ".night-shift")
```

## Property Test Cases

### TS-125-P1: No dangling imports anywhere

**Property:** Property 2 from design.md
**Validates:** 125-REQ-1.3, 125-REQ-1.E1, 125-REQ-7.2
**Type:** property
**Description:** No source or test file in the repo imports from a deleted module.

**For any:** Python file tracked by git
**Invariant:** The file does not contain import statements referencing
deleted module names.

**Assertion pseudocode:**
```
deleted_modules = ["hunt", "critic", "dedup", "finding", "ignore_filter",
                   "ignore", "categories"]
FOR ANY py_file IN git_tracked_python_files:
    content = read(py_file)
    FOR EACH mod IN deleted_modules:
        ASSERT f"from agent_fox.nightshift.{mod}" NOT IN content
        ASSERT f"agent_fox.nightshift.{mod}" NOT IN content
```

### TS-125-P2: Config backward compat for any removed field set

**Property:** Property 3 from design.md
**Validates:** 125-REQ-5.1, 125-REQ-5.4
**Type:** property
**Description:** Any combination of removed fields can be passed to
NightShiftConfig without error.

**For any:** subset of removed field names with arbitrary values
**Invariant:** NightShiftConfig(**subset) constructs successfully.

**Assertion pseudocode:**
```
FOR ANY subset OF {"hunt_scan_interval": int, "categories": dict,
                   "quality_gate_timeout": int, "spec_interval": int,
                   "enabled_streams": list, "similarity_threshold": float}:
    cfg = NightShiftConfig(**subset)
    ASSERT cfg.issue_check_interval == 900
```

### TS-125-P3: build_streams always returns exactly one stream

**Property:** Property 4 from design.md
**Validates:** 125-REQ-3.3, 125-REQ-3.E1
**Type:** property
**Description:** build_streams always returns exactly one fix-pipeline stream.

**For any:** valid config object and boolean no_fixes flag
**Invariant:** len(result) == 1 and result[0].name == "fix-pipeline"

**Assertion pseudocode:**
```
FOR ANY no_fixes IN {True, False}:
    streams = build_streams(config, no_fixes=no_fixes, engine=engine, budget=budget)
    ASSERT len(streams) == 1
    ASSERT streams[0].name == "fix-pipeline"
    ASSERT streams[0].enabled == (NOT no_fixes)
```

## Edge Case Tests

### TS-125-E1: CLI rejects --auto flag

**Requirement:** 125-REQ-4.1
**Type:** unit
**Description:** Verify --auto is no longer accepted.

**Preconditions:** None.

**Input:**
- Invoke CLI with `["night-shift", "--auto"]`.

**Expected:**
- Non-zero exit code with usage error.

**Assertion pseudocode:**
```
result = cli_runner.invoke(app, ["night-shift", "--auto"])
ASSERT result.exit_code != 0
ASSERT "no such option" IN result.output.lower() OR "Error" IN result.output
```

### TS-125-E2: CLI rejects --no-specs flag

**Requirement:** 125-REQ-4.1
**Type:** unit
**Description:** Verify --no-specs is no longer accepted.

**Preconditions:** None.

**Input:**
- Invoke CLI with `["night-shift", "--no-specs"]`.

**Expected:**
- Non-zero exit code with usage error.

**Assertion pseudocode:**
```
result = cli_runner.invoke(app, ["night-shift", "--no-specs"])
ASSERT result.exit_code != 0
```

### TS-125-E3: CLI rejects --no-hunts flag

**Requirement:** 125-REQ-4.1
**Type:** unit
**Description:** Verify --no-hunts is no longer accepted.

**Preconditions:** None.

**Input:**
- Invoke CLI with `["night-shift", "--no-hunts"]`.

**Expected:**
- Non-zero exit code with usage error.

**Assertion pseudocode:**
```
result = cli_runner.invoke(app, ["night-shift", "--no-hunts"])
ASSERT result.exit_code != 0
```

### TS-125-E4: CLI rejects --specs-dir flag

**Requirement:** 125-REQ-4.1
**Type:** unit
**Description:** Verify --specs-dir is no longer accepted.

**Preconditions:** None.

**Input:**
- Invoke CLI with `["night-shift", "--specs-dir", "/tmp"]`.

**Expected:**
- Non-zero exit code with usage error.

**Assertion pseudocode:**
```
result = cli_runner.invoke(app, ["night-shift", "--specs-dir", "/tmp"])
ASSERT result.exit_code != 0
```

## Integration Smoke Tests

### TS-125-SMOKE-1: Fix-pipeline drain loop works end-to-end

**Execution Path:** Path 1 from design.md
**Description:** Verify the fix-pipeline stream works through the full
drain loop from CLI to fix processing.

**Setup:** Mock platform to return one af:fix issue, then zero issues
on re-poll. Mock FixPipeline.process_issue to return FixMetrics. Real
engine, real streams, real DaemonRunner.

**Trigger:** Call `DaemonRunner.run()` with the single fix-pipeline stream.

**Expected side effects:**
- `FixPipeline.process_issue` called once with the issue.
- Engine state shows `issues_fixed >= 1`.
- Drain loop terminates (re-poll returns empty).

**Must NOT satisfy with:** Mocking `_drain_issues()` or
`_run_issue_check()` — these must execute for real.

**Assertion pseudocode:**
```
platform = MockPlatform(issues=[mock_issue], repoll_returns=[])
engine = NightShiftEngine(config, platform)
budget = SharedBudget(max_cost=10.0)
streams = build_streams(config, engine=engine, budget=budget)
runner = DaemonRunner(config, platform, streams, budget, pid_path)
# Set shutdown after first cycle
runner.request_shutdown()
await runner.run()
ASSERT engine.state.issues_fixed >= 1
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 125-REQ-1.1 | TS-125-1 | unit |
| 125-REQ-1.2 | TS-125-2 | unit |
| 125-REQ-1.3 | TS-125-3, TS-125-P1 | unit, property |
| 125-REQ-1.E1 | TS-125-P1 | property |
| 125-REQ-2.1 | TS-125-4 | unit |
| 125-REQ-2.2 | TS-125-5 | unit |
| 125-REQ-2.4 | TS-125-6 | unit |
| 125-REQ-3.1 | TS-125-7 | unit |
| 125-REQ-3.3 | TS-125-8, TS-125-P3 | unit, property |
| 125-REQ-3.4 | TS-125-8 | unit |
| 125-REQ-3.E1 | TS-125-9, TS-125-P3 | unit, property |
| 125-REQ-4.1 | TS-125-E1, TS-125-E2, TS-125-E3, TS-125-E4 | unit |
| 125-REQ-4.2 | TS-125-8 | unit |
| 125-REQ-4.3 | TS-125-1 (implicitly, class in nightshift.py) | unit |
| 125-REQ-4.4 | TS-125-3 | unit |
| 125-REQ-5.1 | TS-125-10, TS-125-P2 | unit, property |
| 125-REQ-5.2 | TS-125-11 | unit |
| 125-REQ-5.3 | TS-125-10 | unit |
| 125-REQ-5.4 | TS-125-10, TS-125-P2 | unit, property |
| 125-REQ-5.E1 | TS-125-10 | unit |
| 125-REQ-6.1 | TS-125-3 | unit |
| 125-REQ-6.2 | TS-125-12 | unit |
| 125-REQ-7.1 | TS-125-1 (test files) | unit |
| 125-REQ-7.2 | TS-125-P1 | property |
| 125-REQ-7.3 | TS-125-SMOKE-1 | integration |
| 125-REQ-8.1 | (manual doc review) | — |
| 125-REQ-8.2 | (manual doc review) | — |
| 125-REQ-8.3 | (manual doc review) | — |
| 125-REQ-8.4 | (manual doc review) | — |
| 125-REQ-8.5 | (manual doc review) | — |
| 125-REQ-8.6 | (manual doc review) | — |
| Property 1 | TS-125-SMOKE-1 | integration |
| Property 2 | TS-125-P1 | property |
| Property 3 | TS-125-P2 | property |
| Property 4 | TS-125-P3 | property |
| Property 5 | TS-125-E1 through TS-125-E4 | unit |
