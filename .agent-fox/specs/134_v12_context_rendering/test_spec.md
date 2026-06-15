# Test Specification: v1.2 Context Assembly and Rendering

## Overview

Tests validate that context assembly, spec helpers, and the verification
checklist correctly detect v1.2 spec folders and render structured JSON
artifacts into markdown using afspec, while preserving the v1 path
unchanged.

## Test Cases

### TS-134-1: v1.2 format detection in assemble_context

**Requirement:** 134-REQ-1.1
**Type:** unit
**Description:** Verify that assemble_context detects a v1.2 spec folder
and uses afspec-based rendering.

**Preconditions:**
- A temp spec directory with valid v1.2 artifacts (prd.md with frontmatter,
  requirements.json, test_spec.json, tasks.json).
- A mock DuckDB connection returning no findings.

**Input:**
- Path to the v1.2 spec directory, task_group=1, conn=mock.

**Expected:**
- The assembled context contains "## Requirements", "## Test Specification",
  "## Tasks" sections.
- The content is rendered from afspec (not raw file reads).

**Assertion pseudocode:**
```
context = assemble_context(v12_spec_dir, 1, conn=mock_conn)
ASSERT "## Requirements" in context
ASSERT "## Test Specification" in context
ASSERT "## Tasks" in context
```

### TS-134-2: v1 format unchanged in assemble_context

**Requirement:** 134-REQ-1.2
**Type:** unit
**Description:** Verify that assemble_context uses raw markdown reads for
v1 spec folders.

**Preconditions:**
- A temp spec directory with v1 artifacts (requirements.md, design.md,
  test_spec.md, tasks.md).
- A mock DuckDB connection returning no findings.

**Input:**
- Path to the v1 spec directory, task_group=1, conn=mock.

**Expected:**
- The assembled context contains "## Requirements", "## Design",
  "## Test Specification", "## Tasks" sections.
- Content matches the raw file contents.

**Assertion pseudocode:**
```
context = assemble_context(v1_spec_dir, 1, conn=mock_conn)
ASSERT "## Requirements" in context
ASSERT "## Design" in context
ASSERT "## Test Specification" in context
ASSERT "## Tasks" in context
```

### TS-134-3: v1.2 architecture.md included when present

**Requirement:** 134-REQ-2.2
**Type:** unit
**Description:** Verify that architecture.md is read from disk and included
in the context for v1.2 specs.

**Preconditions:**
- A v1.2 spec directory with architecture.md present.
- A mock DuckDB connection.

**Input:**
- Path to the v1.2 spec directory with architecture.md.

**Expected:**
- The assembled context contains "## Architecture" section with the
  architecture.md content.

**Assertion pseudocode:**
```
context = assemble_context(v12_spec_dir_with_arch, 1, conn=mock_conn)
ASSERT "## Architecture" in context
ASSERT "architecture content" in context
```

### TS-134-4: v1.2 architecture.md omitted when absent

**Requirement:** 134-REQ-2.3
**Type:** unit
**Description:** Verify that missing architecture.md is silently omitted.

**Preconditions:**
- A v1.2 spec directory without architecture.md.

**Input:**
- Path to the v1.2 spec directory.

**Expected:**
- The assembled context does NOT contain "## Architecture".
- No warning is logged.

**Assertion pseudocode:**
```
context = assemble_context(v12_spec_dir_no_arch, 1, conn=mock_conn)
ASSERT "## Architecture" not in context
```

### TS-134-5: count_ts_entries with v1.2 test_spec.json

**Requirement:** 134-REQ-3.1
**Type:** unit
**Description:** Verify count_ts_entries loads test_spec.json and returns
the total test count.

**Preconditions:**
- A v1.2 spec directory with valid test_spec.json containing known numbers
  of test cases, property tests, edge case tests, and smoke tests.

**Input:**
- Path to the v1.2 spec directory.

**Expected:**
- Returns the sum of all test entry lists.

**Assertion pseudocode:**
```
# fixture: 3 test_cases, 2 property_tests, 1 edge_case_test, 1 smoke_test
count = count_ts_entries(v12_spec_dir)
ASSERT count == 7
```

### TS-134-6: count_ts_entries with v1 test_spec.md unchanged

**Requirement:** 134-REQ-3.2
**Type:** unit
**Description:** Verify count_ts_entries uses heading counting for v1 specs.

**Preconditions:**
- A v1 spec directory with test_spec.md containing known ### TS- headings.

**Input:**
- Path to the v1 spec directory.

**Expected:**
- Returns the count of ### TS- headings.

**Assertion pseudocode:**
```
# fixture: test_spec.md with 5 ### TS- headings
count = count_ts_entries(v1_spec_dir)
ASSERT count == 5
```

### TS-134-7: spec_has_existing_code checks architecture.md for v1.2

**Requirement:** 134-REQ-3.3
**Type:** unit
**Description:** Verify spec_has_existing_code reads architecture.md
instead of design.md for v1.2 specs.

**Preconditions:**
- A v1.2 spec directory with requirements.json and architecture.md
  containing a `(modified)` file reference to an existing file.

**Input:**
- Path to the v1.2 spec directory.

**Expected:**
- Returns True (the referenced file exists).

**Assertion pseudocode:**
```
# architecture.md references an existing file marked (modified)
result = spec_has_existing_code(v12_spec_dir)
ASSERT result == True
```

### TS-134-8: v1.2 verification checklist extracts tasks from JSON

**Requirement:** 134-REQ-4.1
**Type:** unit
**Description:** Verify _audit_task_checkboxes loads tasks.json and
extracts subtask state.

**Preconditions:**
- A v1.2 spec directory with valid tasks.json containing groups and
  subtasks with known completion states.

**Input:**
- Path to the v1.2 spec directory.

**Expected:**
- Returns SubtaskAuditEntry list matching the tasks.json content.

**Assertion pseudocode:**
```
entries = _audit_task_checkboxes(v12_spec_dir)
ASSERT len(entries) > 0
ASSERT entries[0].subtask_id matches tasks.json subtask ID
ASSERT entries[0].checked matches tasks.json subtask state
```

### TS-134-9: v1.2 verification checklist extracts requirements from JSON

**Requirement:** 134-REQ-4.2
**Type:** unit
**Description:** Verify scan_requirement_test_coverage loads
requirements.json and extracts requirement IDs.

**Preconditions:**
- A v1.2 spec directory with valid requirements.json containing known
  requirement IDs.
- A tests directory with test files referencing some of those IDs.

**Input:**
- Path to the v1.2 spec directory and tests directory.

**Expected:**
- Returns RequirementMapping list with IDs from requirements.json.

**Assertion pseudocode:**
```
mappings = scan_requirement_test_coverage(v12_spec_dir, tests_dir)
ASSERT len(mappings) > 0
ASSERT any(m.requirement_id == "134-REQ-1.1" for m in mappings)
```

## Property Test Cases

### TS-134-P1: v1 path produces identical output

**Property:** Property 2 from design.md
**Validates:** 134-REQ-1.2
**Type:** property
**Description:** For v1 spec folders, assemble_context output is unchanged
by the v1.2 code path addition.

**For any:** v1 spec folder (containing only .md files, no .json artifacts)
**Invariant:** The assembled context with the updated code is identical to
what the pre-change code would produce.

**Assertion pseudocode:**
```
FOR ANY v1_spec_dir with markdown files:
    context_new = assemble_context(v1_spec_dir, 1, conn=mock_conn)
    # compare against expected output from raw file reads
    expected = build_expected_v1_context(v1_spec_dir)
    ASSERT context_new == expected
```

### TS-134-P2: v1.2 rendering preserves section order

**Property:** Property 1 from design.md
**Validates:** 134-REQ-2.1
**Type:** property
**Description:** v1.2 rendered context always has sections in the canonical
order: Requirements, Test Specification, Tasks.

**For any:** valid v1.2 spec folder
**Invariant:** The index of "## Requirements" < index of
"## Test Specification" < index of "## Tasks" in the assembled context.

**Assertion pseudocode:**
```
FOR ANY valid v12_spec_dir:
    context = assemble_context(v12_spec_dir, 1, conn=mock_conn)
    idx_req = context.index("## Requirements")
    idx_ts = context.index("## Test Specification")
    idx_tasks = context.index("## Tasks")
    ASSERT idx_req < idx_ts < idx_tasks
```

## Edge Case Tests

### TS-134-E1: LoadError fallback in assemble_context

**Requirement:** 134-REQ-1.E1
**Type:** unit
**Description:** When afspec.load_spec raises LoadError, assemble_context
falls back to raw markdown reads.

**Preconditions:**
- A v1.2 spec directory with malformed requirements.json that causes
  LoadError, plus some .md files present for fallback.

**Input:**
- Path to the malformed v1.2 spec directory.

**Expected:**
- A warning is logged mentioning the load failure.
- Whatever .md files exist are read and included in context.

**Assertion pseudocode:**
```
context = assemble_context(malformed_v12_dir, 1, conn=mock_conn)
# Should not raise
ASSERT "## " in context  # some section was rendered from fallback .md files
ASSERT warning_logged("LoadError" or similar)
```

### TS-134-E2: Empty render_individual artifact omitted

**Requirement:** 134-REQ-2.E1
**Type:** unit
**Description:** When render_individual returns empty string for an
artifact, that section is omitted.

**Preconditions:**
- A v1.2 spec where one artifact renders to empty string (mock or
  fixture with minimal content).

**Input:**
- Path to the v1.2 spec directory.

**Expected:**
- The empty artifact's section header does not appear in context.

**Assertion pseudocode:**
```
# Mock render_individual to return empty string for "tasks"
context = assemble_context(v12_spec_dir, 1, conn=mock_conn)
# "## Tasks" should be absent if tasks rendered empty
ASSERT "## Tasks" not in context
```

### TS-134-E3: count_ts_entries returns 0 on load failure

**Requirement:** 134-REQ-3.E1
**Type:** unit
**Description:** When test_spec.json exists but loading fails,
count_ts_entries returns 0.

**Preconditions:**
- A spec directory with malformed test_spec.json.

**Input:**
- Path to the spec directory.

**Expected:**
- Returns 0.
- A warning is logged.

**Assertion pseudocode:**
```
count = count_ts_entries(malformed_spec_dir)
ASSERT count == 0
ASSERT warning_logged
```

### TS-134-E4: Verification checklist returns empty on JSON load failure

**Requirement:** 134-REQ-4.E1
**Type:** unit
**Description:** When tasks.json or requirements.json cannot be loaded,
the corresponding checklist function returns an empty list.

**Preconditions:**
- A v1.2 spec directory with malformed tasks.json.

**Input:**
- Path to the malformed spec directory.

**Expected:**
- _audit_task_checkboxes returns empty list.
- A warning is logged.

**Assertion pseudocode:**
```
entries = _audit_task_checkboxes(malformed_spec_dir)
ASSERT entries == []
ASSERT warning_logged
```

## Integration Smoke Tests

### TS-134-SMOKE-1: End-to-end v1.2 context assembly

**Execution Path:** Path 1 from design.md
**Description:** Assemble full context from a valid v1.2 spec folder with
all artifacts, architecture.md, steering, memory facts, and a mock DB.

**Setup:**
- Temp v1.2 spec directory with valid prd.md (frontmatter),
  requirements.json, test_spec.json, tasks.json, architecture.md.
- Mock DuckDB connection with no findings.
- Memory facts list with one entry.
- Project root with empty steering.md.

**Trigger:** `assemble_context(spec_dir, 1, memory_facts=["fact1"], conn=mock, project_root=tmp_root)`

**Expected side effects:**
- Assembled context contains "## Requirements", "## Test Specification",
  "## Tasks", "## Architecture", "## Memory Facts".
- No warnings logged.

**Must NOT satisfy with:** No mocking of afspec.load_spec or render_individual.

**Assertion pseudocode:**
```
context = assemble_context(v12_dir, 1, memory_facts=["fact1"], conn=mock, project_root=root)
ASSERT "## Requirements" in context
ASSERT "## Test Specification" in context
ASSERT "## Tasks" in context
ASSERT "## Architecture" in context
ASSERT "## Memory Facts" in context
ASSERT "fact1" in context
```

### TS-134-SMOKE-2: End-to-end v1.2 verification checklist

**Execution Path:** Path 5 from design.md
**Description:** Build a complete verification checklist from a v1.2 spec
folder with tasks.json and requirements.json.

**Setup:**
- Temp v1.2 spec directory with valid tasks.json (2 groups, 4 subtasks)
  and requirements.json (3 requirements).
- Tests directory with a test file referencing one requirement ID.
- Mock DuckDB connection.

**Trigger:** `build_verification_checklist(spec_dir, mock_conn, tests_dir=tests_dir)`

**Expected side effects:**
- VerificationChecklist has task_audit entries matching tasks.json subtasks.
- VerificationChecklist has requirement_coverage entries matching
  requirements.json requirement IDs.
- One requirement is marked covered, others uncovered.

**Must NOT satisfy with:** No mocking of afspec.load_spec.

**Assertion pseudocode:**
```
checklist = build_verification_checklist(v12_dir, mock_conn, tests_dir=tests)
ASSERT len(checklist.task_audit) == 4
ASSERT len(checklist.requirement_coverage) == 3
covered = [m for m in checklist.requirement_coverage if m.covered]
ASSERT len(covered) >= 1
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 134-REQ-1.1 | TS-134-1 | unit |
| 134-REQ-1.2 | TS-134-2 | unit |
| 134-REQ-1.E1 | TS-134-E1 | unit |
| 134-REQ-2.1 | TS-134-1 | unit |
| 134-REQ-2.2 | TS-134-3 | unit |
| 134-REQ-2.3 | TS-134-4 | unit |
| 134-REQ-2.E1 | TS-134-E2 | unit |
| 134-REQ-3.1 | TS-134-5 | unit |
| 134-REQ-3.2 | TS-134-6 | unit |
| 134-REQ-3.3 | TS-134-7 | unit |
| 134-REQ-3.E1 | TS-134-E3 | unit |
| 134-REQ-4.1 | TS-134-8 | unit |
| 134-REQ-4.2 | TS-134-9 | unit |
| 134-REQ-4.E1 | TS-134-E4 | unit |
| Property 1 | TS-134-P2 | property |
| Property 2 | TS-134-P1 | property |
| Path 1 (end-to-end) | TS-134-SMOKE-1 | integration |
| Path 5 (checklist) | TS-134-SMOKE-2 | integration |
