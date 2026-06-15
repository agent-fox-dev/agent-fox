# Test Specification: Legacy Format Removal

## Overview

Tests verify that v1 modules are deleted, shared types are extractable from
their new location, all consumer imports are rewired, no v1 filename strings
remain in source, and the full test suite passes. Most tests are structural
assertions (file existence, import scanning, grep checks) rather than
behavioral tests.

## Test Cases

### TS-137-1: types.py exports TaskGroupDef

**Requirement:** 137-REQ-1.1
**Type:** unit
**Description:** `TaskGroupDef` is importable from `spec.types` and
constructable with the same field signature as the former `parser.py` version.

**Preconditions:** None.
**Input:** Construct a `TaskGroupDef` with all fields.
**Expected:** Object created with correct field values; object is frozen.

**Assertion pseudocode:**
```
from agent_fox.spec.types import SubtaskDef, TaskGroupDef
sub = SubtaskDef(id="1.1", title="test", completed=False)
group = TaskGroupDef(number=1, title="test", optional=False, completed=False,
                     subtasks=(sub,), body="", archetype=None)
ASSERT group.number == 1
ASSERT group.subtasks == (sub,)
```

### TS-137-2: types.py exports Finding and severity constants

**Requirement:** 137-REQ-1.2
**Type:** unit
**Description:** `Finding`, severity constants, `compute_exit_code`, and
`sort_findings` are importable from `spec.types`.

**Preconditions:** None.
**Input:** Create `Finding` objects, call utility functions.
**Expected:** All symbols importable; functions produce correct results.

**Assertion pseudocode:**
```
from agent_fox.spec.types import Finding, SEVERITY_ERROR, SEVERITY_WARNING,
    compute_exit_code, sort_findings
f = Finding(file="a.py", line=1, rule="r", message="m", severity=SEVERITY_ERROR)
ASSERT compute_exit_code([f]) != 0
ASSERT compute_exit_code([]) == 0
```

### TS-137-3: parser_v12 imports from types

**Requirement:** 137-REQ-1.3
**Type:** unit
**Description:** `parser_v12.py` imports shared types from `spec.types`.

**Preconditions:** None.
**Input:** Read `parser_v12.py` source.
**Expected:** Contains `from agent_fox.spec.types import`.

**Assertion pseudocode:**
```
content = read_file("agent_fox/spec/parser_v12.py")
ASSERT "from agent_fox.spec.types import" in content
```

### TS-137-4: parser.py deleted

**Requirement:** 137-REQ-2.1
**Type:** unit
**Description:** `parser.py` does not exist on disk.

**Preconditions:** None.
**Input:** Check filesystem.
**Expected:** File does not exist.

**Assertion pseudocode:**
```
ASSERT NOT exists("agent_fox/spec/parser.py")
```

### TS-137-5: validators/ deleted

**Requirement:** 137-REQ-3.1
**Type:** unit
**Description:** The `validators/` directory does not exist.

**Preconditions:** None.
**Input:** Check filesystem.
**Expected:** Directory does not exist.

**Assertion pseudocode:**
```
ASSERT NOT exists("agent_fox/spec/validators/")
```

### TS-137-6: ai_validation.py deleted

**Requirement:** 137-REQ-4.1
**Type:** unit
**Description:** `ai_validation.py` does not exist on disk.

**Preconditions:** None.
**Input:** Check filesystem.
**Expected:** File does not exist.

**Assertion pseudocode:**
```
ASSERT NOT exists("agent_fox/spec/ai_validation.py")
```

### TS-137-7: lint.py no validator imports

**Requirement:** 137-REQ-3.2
**Type:** unit
**Description:** `lint.py` does not import from `agent_fox.spec.validators`.

**Preconditions:** None.
**Input:** Read `lint.py` source.
**Expected:** No validator import strings found.

**Assertion pseudocode:**
```
content = read_file("agent_fox/spec/lint.py")
ASSERT "agent_fox.spec.validators" NOT in content
```

### TS-137-8: No parser imports in engine modules

**Requirement:** 137-REQ-5.3
**Type:** unit
**Description:** Engine modules do not import from `agent_fox.spec.parser`.

**Preconditions:** None.
**Input:** Read source of session_lifecycle.py, hot_load.py, engine.py,
dispatch.py.
**Expected:** No parser import strings found in any file.

**Assertion pseudocode:**
```
FOR EACH module IN [session_lifecycle, hot_load, engine, dispatch]:
    content = read_file(module)
    ASSERT "from agent_fox.spec.parser" NOT in content
```

### TS-137-9: No v1 filename strings in source

**Requirement:** 137-REQ-6.4
**Type:** unit
**Description:** No Python file in `agent_fox/` (except `fix/spec_gen.py`)
contains v1 filename strings as operational references.

**Preconditions:** None.
**Input:** Grep all `.py` files under `agent_fox/`.
**Expected:** Zero matches (excluding spec_gen.py and __pycache__).

**Assertion pseudocode:**
```
result = grep("requirements\.md|design\.md|test_spec\.md", "agent_fox/", "*.py")
matches = filter_out(result, ["spec_gen", "__pycache__"])
ASSERT len(matches) == 0
```

### TS-137-10: No _CORE_SPEC_FILES constant

**Requirement:** 137-REQ-6.3
**Type:** unit
**Description:** `session/context.py` does not contain `_CORE_SPEC_FILES`.

**Preconditions:** None.
**Input:** Read `context.py` source.
**Expected:** String not found.

**Assertion pseudocode:**
```
content = read_file("agent_fox/session/context.py")
ASSERT "_CORE_SPEC_FILES" NOT in content
```

## Property Test Cases

### TS-137-P1: Type identity preserved

**Property:** Property 1 from design.md
**Validates:** 137-REQ-1.1, 137-REQ-1.2
**Type:** property
**Description:** Shared types from `types.py` have identical field signatures
to their former locations.

**For any:** Valid field combination for TaskGroupDef, SubtaskDef, CrossSpecDep
**Invariant:** Construction succeeds and all fields are accessible.

**Assertion pseudocode:**
```
FOR ANY number, title, optional, completed, body, archetype:
    group = TaskGroupDef(number=number, title=title, optional=optional,
                         completed=completed, subtasks=(), body=body,
                         archetype=archetype)
    ASSERT group.number == number
    ASSERT group.title == title
```

### TS-137-P2: Full package importability

**Property:** Property 2 from design.md
**Validates:** 137-REQ-2.2, 137-REQ-3.1, 137-REQ-4.1
**Type:** property
**Description:** Every Python module in `agent_fox/` is importable.

**For any:** Module discovered by `pkgutil.walk_packages`
**Invariant:** `importlib.import_module(name)` does not raise `ImportError`.

**Assertion pseudocode:**
```
FOR ANY module IN walk_packages("agent_fox/"):
    ASSERT import_module(module) does not raise ImportError
```

## Edge Case Tests

### TS-137-E1: Import from deleted parser raises error

**Requirement:** 137-REQ-1.E1
**Type:** unit
**Description:** Importing from the deleted `parser.py` raises ImportError.

**Preconditions:** `parser.py` has been deleted.
**Input:** Attempt `from agent_fox.spec.parser import parse_tasks`.
**Expected:** `ImportError` or `ModuleNotFoundError` raised.

**Assertion pseudocode:**
```
result = subprocess("python -c 'from agent_fox.spec.parser import parse_tasks'")
ASSERT result.returncode != 0
ASSERT "ImportError" in result.stderr OR "ModuleNotFoundError" in result.stderr
```

### TS-137-E2: Import from deleted validators raises error

**Requirement:** 137-REQ-3.E1
**Type:** unit
**Description:** Importing from the deleted `validators/` raises ImportError.

**Preconditions:** `validators/` has been deleted.
**Input:** Attempt `from agent_fox.spec.validators import Finding`.
**Expected:** `ImportError` or `ModuleNotFoundError` raised.

**Assertion pseudocode:**
```
result = subprocess("python -c 'from agent_fox.spec.validators import Finding'")
ASSERT result.returncode != 0
```

### TS-137-E3: No deleted module imports in tests

**Requirement:** 137-REQ-7.2, 137-REQ-7.3, 137-REQ-7.4
**Type:** unit
**Description:** No test file imports from any deleted module.

**Preconditions:** None.
**Input:** Grep test files for deleted module imports.
**Expected:** Zero matches.

**Assertion pseudocode:**
```
patterns = "from agent_fox.spec.parser import|from agent_fox.spec.validators|from agent_fox.spec.ai_validation import"
result = grep(patterns, "tests/", "*.py")
matches = filter_out(result, ["__pycache__"])
ASSERT len(matches) == 0
```

## Integration Smoke Tests

### TS-137-SMOKE-1: Full test suite passes

**Execution Path:** Path 1 from design.md
**Description:** The complete test suite passes after all deletions and
rewiring.

**Setup:** All spec changes applied (types extracted, modules deleted,
imports rewired).
**Trigger:** `make test`
**Real components:** All agent_fox modules, all test files.
**Mockable:** None.
**Expected effects:** Zero test failures.

**Must NOT satisfy with:** Skipping or deleting tests to hide failures.

**Assertion pseudocode:**
```
result = subprocess("make test")
ASSERT result.returncode == 0
ASSERT "failed" NOT in result.stdout OR "0 failed" in result.stdout
```

### TS-137-SMOKE-2: lint-specs works after deletion

**Execution Path:** Path 1 from design.md
**Description:** `lint-specs` validates v1.2 specs without errors after
validator deletion.

**Setup:** A valid v1.2 spec exists in `.agent-fox/specs/`.
**Trigger:** Import and call `run_lint_specs()`.
**Real components:** `lint.py`, `discovery.py`, `afspec`.
**Mockable:** Filesystem (tmpdir with valid spec).
**Expected effects:** Returns findings list (possibly empty), no ImportError.

**Assertion pseudocode:**
```
# Create tmpdir with valid v1.2 spec
result = run_lint_specs(specs_dir=tmpdir)
ASSERT isinstance(result, list)
# No crash, no ImportError
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|---|---|---|
| 137-REQ-1.1 | TS-137-1 | unit |
| 137-REQ-1.2 | TS-137-2 | unit |
| 137-REQ-1.3 | TS-137-3 | unit |
| 137-REQ-1.E1 | TS-137-E1 | unit |
| 137-REQ-2.1 | TS-137-4 | unit |
| 137-REQ-2.2 | TS-137-P2 | property |
| 137-REQ-3.1 | TS-137-5 | unit |
| 137-REQ-3.2 | TS-137-7 | unit |
| 137-REQ-3.E1 | TS-137-E2 | unit |
| 137-REQ-4.1 | TS-137-6 | unit |
| 137-REQ-5.1 | TS-137-3 | unit |
| 137-REQ-5.3 | TS-137-8 | unit |
| 137-REQ-5.E1 | TS-137-9 | unit |
| 137-REQ-6.3 | TS-137-10 | unit |
| 137-REQ-6.4 | TS-137-9 | unit |
| 137-REQ-7.1 | TS-137-SMOKE-1 | integration |
| 137-REQ-7.2 | TS-137-E3 | unit |
| 137-REQ-7.3 | TS-137-E3 | unit |
| 137-REQ-7.4 | TS-137-E3 | unit |
| Property 1 | TS-137-P1 | property |
| Property 2 | TS-137-P2 | property |
