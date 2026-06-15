# Test Specification: Legacy Format Removal

## Overview

Tests verify that shared types are correctly extracted, legacy modules are
fully deleted, all imports compile, no stale references remain, and the
full test suite passes after cleanup.

## Test Cases

### TS-136-1: types.py exports correct dataclasses

**Requirement:** 136-REQ-1.1
**Type:** unit
**Description:** Verify types.py contains TaskGroupDef, SubtaskDef, CrossSpecDep.

**Preconditions:**
- agent_fox/spec/types.py exists.

**Input:**
- Import the module.

**Expected:**
- All three dataclasses are importable and constructable.

**Assertion pseudocode:**
```
from agent_fox.spec.types import TaskGroupDef, SubtaskDef, CrossSpecDep
sub = SubtaskDef(id="1.1", title="test", completed=False, optional=False)
ASSERT sub.id == "1.1"
group = TaskGroupDef(number=1, title="test", optional=False, completed=False, subtasks=[sub], body="", archetype=None)
ASSERT group.number == 1
dep = CrossSpecDep(from_spec="01", from_group=1, to_spec="02", to_group=1, relationship="test")
ASSERT dep.from_spec == "01"
```

### TS-136-2: Types are import-compatible across modules

**Requirement:** 136-REQ-1.2
**Type:** integration
**Description:** Verify planner.py, builder.py, and parser_v12.py all use the same type.

**Preconditions:**
- All consumer modules updated.

**Input:**
- Import TaskGroupDef from types.py, builder.py's import, parser_v12.py's import.

**Expected:**
- All resolve to the same class object.

**Assertion pseudocode:**
```
from agent_fox.spec.types import TaskGroupDef as T1
# Verify builder and parser_v12 both use T1 (inspect their imports)
import agent_fox.graph.builder
import agent_fox.spec.parser_v12
# Both modules should import from agent_fox.spec.types
ASSERT "agent_fox.spec.types" in sys.modules
```

### TS-136-3: parser.py is deleted

**Requirement:** 136-REQ-2.1
**Type:** unit
**Description:** Verify parser.py does not exist on disk.

**Preconditions:**
- Spec implementation complete.

**Input:**
- Check filesystem.

**Expected:**
- File does not exist.

**Assertion pseudocode:**
```
import pathlib
parser_path = pathlib.Path("agent_fox/spec/parser.py")
ASSERT NOT parser_path.exists()
```

### TS-136-4: validators/ directory is deleted

**Requirement:** 136-REQ-3.1
**Type:** unit
**Description:** Verify the validators directory does not exist.

**Preconditions:**
- Spec implementation complete.

**Input:**
- Check filesystem.

**Expected:**
- Directory does not exist.

**Assertion pseudocode:**
```
validators_path = pathlib.Path("agent_fox/spec/validators")
ASSERT NOT validators_path.exists()
```

### TS-136-5: verification_checklist.py is deleted

**Requirement:** 136-REQ-3.2
**Type:** unit
**Description:** Verify verification_checklist.py does not exist.

**Preconditions:**
- Spec implementation complete.

**Input:**
- Check filesystem.

**Expected:**
- File does not exist.

**Assertion pseudocode:**
```
vc_path = pathlib.Path("agent_fox/spec/verification_checklist.py")
ASSERT NOT vc_path.exists()
```

### TS-136-6: ai_validation.py is deleted

**Requirement:** 136-REQ-3.3
**Type:** unit
**Description:** Verify ai_validation.py does not exist.

**Preconditions:**
- Spec implementation complete.

**Input:**
- Check filesystem.

**Expected:**
- File does not exist.

**Assertion pseudocode:**
```
ai_path = pathlib.Path("agent_fox/spec/ai_validation.py")
ASSERT NOT ai_path.exists()
```

### TS-136-7: Engine modules do not import from parser.py

**Requirement:** 136-REQ-4.1, 136-REQ-4.2, 136-REQ-4.3
**Type:** integration
**Description:** Verify engine modules import cleanly without parser.py.

**Preconditions:**
- parser.py deleted, engine modules updated.

**Input:**
- Import all engine modules.

**Expected:**
- No ImportError raised.

**Assertion pseudocode:**
```
import agent_fox.engine.session_lifecycle
import agent_fox.engine.hot_load
import agent_fox.engine.engine
import agent_fox.engine.dispatch
# No ImportError means success
```

### TS-136-8: Graph modules import types from spec/types.py

**Requirement:** 136-REQ-4.4
**Type:** unit
**Description:** Verify graph modules import from the new location.

**Preconditions:**
- Consumer modules updated.

**Input:**
- Import graph modules.

**Expected:**
- No ImportError; types are from spec.types.

**Assertion pseudocode:**
```
import agent_fox.graph.planner
import agent_fox.graph.builder
import agent_fox.spec.parser_v12
# All import successfully
```

### TS-136-9: No stale markdown references in source

**Requirement:** 136-REQ-5.2
**Type:** integration
**Description:** Grep confirms no old spec filenames remain (excluding fix/spec_gen.py).

**Preconditions:**
- All deletions and updates complete.

**Input:**
- Run grep for old filenames.

**Expected:**
- Zero matches outside fix/spec_gen.py.

**Assertion pseudocode:**
```
result = shell("grep -rn 'requirements\\.md\\|design\\.md\\|test_spec\\.md' agent_fox/ --include='*.py' | grep -v spec_gen | grep -v __pycache__")
ASSERT result.returncode != 0  # grep returns 1 when no matches
```

### TS-136-10: Full test suite passes

**Requirement:** 136-REQ-6.1
**Type:** integration
**Description:** make check passes with zero failures after all changes.

**Preconditions:**
- All deletions, updates, and test cleanups complete.

**Input:**
- Run make check.

**Expected:**
- Exit code 0.

**Assertion pseudocode:**
```
result = shell("make check")
ASSERT result.returncode == 0
```

## Property Test Cases

### TS-136-P1: No dangling imports in package

**Property:** Property 2 from design.md
**Validates:** 136-REQ-2.1, 136-REQ-3.1, 136-REQ-4.1, 136-REQ-4.2, 136-REQ-4.3
**Type:** property
**Description:** Every Python file in agent_fox/ can be imported without ImportError.

**For any:** Python file in agent_fox/ (discovered via pkgutil.walk_packages)
**Invariant:** importing the module does not raise ImportError

**Assertion pseudocode:**
```
FOR ANY module_name IN walk_packages("agent_fox"):
    importlib.import_module(module_name)
    # No ImportError raised
```

### TS-136-P2: No old-format references in source

**Property:** Property 3 from design.md
**Validates:** 136-REQ-5.2
**Type:** property
**Description:** No Python source file contains old spec filename strings.

**For any:** Python file in agent_fox/ (excluding fix/spec_gen.py)
**Invariant:** file does not contain "requirements.md", "design.md", or "test_spec.md"

**Assertion pseudocode:**
```
FOR ANY py_file IN glob("agent_fox/**/*.py", exclude="fix/spec_gen.py"):
    content = read(py_file)
    ASSERT "requirements.md" NOT IN content
    ASSERT "design.md" NOT IN content
    ASSERT "test_spec.md" NOT IN content
```

## Edge Case Tests

### TS-136-E1: Import from deleted parser raises ImportError

**Requirement:** 136-REQ-1.E1
**Type:** unit
**Description:** Attempting to import from parser.py raises ImportError.

**Preconditions:**
- parser.py deleted.

**Input:**
- `from agent_fox.spec.parser import parse_tasks`

**Expected:**
- ImportError raised.

**Assertion pseudocode:**
```
ASSERT_RAISES ImportError:
    from agent_fox.spec.parser import parse_tasks
```

### TS-136-E2: Legacy test files cleaned up

**Requirement:** 136-REQ-2.E1
**Type:** unit
**Description:** No test file imports from deleted modules.

**Preconditions:**
- Test cleanup complete.

**Input:**
- Grep test files for parser.py and validators/ imports.

**Expected:**
- Zero matches.

**Assertion pseudocode:**
```
result = shell("grep -rn 'from agent_fox.spec.parser import\\|from agent_fox.spec.validators' tests/ --include='*.py'")
ASSERT result.returncode != 0
```

### TS-136-E3: fix/spec_gen.py left intact

**Requirement:** 136-REQ-5.E1
**Type:** unit
**Description:** fix/spec_gen.py still exists and can be imported.

**Preconditions:**
- Cleanup complete.

**Input:**
- Check file exists and import it.

**Expected:**
- File exists; no ImportError.

**Assertion pseudocode:**
```
ASSERT pathlib.Path("agent_fox/fix/spec_gen.py").exists()
import agent_fox.fix.spec_gen  # no error
```

## Integration Smoke Tests

### TS-136-SMOKE-1: Full package importability

**Execution Path:** Path 1 from design.md
**Description:** All agent_fox modules import without error after cleanup.

**Setup:** No mocking — real package structure.

**Trigger:** Walk and import all modules in agent_fox package.

**Expected side effects:**
- Every module imports successfully.
- No ImportError for any module.

**Must NOT satisfy with:** Skipping modules that previously imported from
deleted files.

**Assertion pseudocode:**
```
import pkgutil, importlib
for importer, name, ispkg in pkgutil.walk_packages(["agent_fox"], prefix="agent_fox."):
    importlib.import_module(name)
```

### TS-136-SMOKE-2: lint-specs works after validator deletion

**Execution Path:** Path 3 from design.md
**Description:** agent-fox lint-specs runs without referencing deleted validators.

**Setup:** A temp directory with a valid v1.2 spec.

**Trigger:** Run lint-specs on the spec.

**Expected side effects:**
- Command completes without ImportError.
- Uses afspec validation, not the deleted validators.

**Must NOT satisfy with:** Mocking the lint-specs command itself.

**Assertion pseudocode:**
```
result = shell("agent-fox lint-specs path/to/v12/spec")
ASSERT "ImportError" NOT IN result.stderr
ASSERT result.returncode IN (0, 1)  # 0=clean, 1=findings
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 136-REQ-1.1 | TS-136-1 | unit |
| 136-REQ-1.2 | TS-136-2 | integration |
| 136-REQ-1.E1 | TS-136-E1 | unit |
| 136-REQ-2.1 | TS-136-3 | unit |
| 136-REQ-2.2 | TS-136-10 | integration |
| 136-REQ-2.E1 | TS-136-E2 | unit |
| 136-REQ-3.1 | TS-136-4 | unit |
| 136-REQ-3.2 | TS-136-5 | unit |
| 136-REQ-3.3 | TS-136-6 | unit |
| 136-REQ-3.4 | TS-136-SMOKE-2 | integration |
| 136-REQ-3.E1 | TS-136-4 | unit |
| 136-REQ-4.1 | TS-136-7 | integration |
| 136-REQ-4.2 | TS-136-7 | integration |
| 136-REQ-4.3 | TS-136-7 | integration |
| 136-REQ-4.4 | TS-136-8 | unit |
| 136-REQ-4.E1 | TS-136-E1 | unit |
| 136-REQ-5.1 | TS-136-9 | integration |
| 136-REQ-5.2 | TS-136-9 | integration |
| 136-REQ-5.3 | TS-136-9 | integration |
| 136-REQ-5.E1 | TS-136-E3 | unit |
| 136-REQ-6.1 | TS-136-10 | integration |
| 136-REQ-6.2 | TS-136-E2 | unit |
| 136-REQ-6.E1 | TS-136-E2 | unit |
| Property 2 | TS-136-P1 | property |
| Property 3 | TS-136-P2 | property |
