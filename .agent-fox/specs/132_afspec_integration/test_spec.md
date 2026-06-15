# Test Specification: afspec Library Integration

## Overview

Tests validate that the afspec dependency is importable, format detection
works correctly, discovery filters to v1.2 only, and afspec can load
discovered specs.

## Test Cases

### TS-132-1: afspec is importable

**Requirement:** 132-REQ-1.2
**Type:** unit
**Description:** Verify that `import afspec` succeeds and key symbols exist.

**Preconditions:**
- agent-fox environment is installed with dependencies.

**Input:**
- N/A (import test)

**Expected:**
- `afspec` module is importable.
- `afspec.load_spec`, `afspec.Spec`, `afspec.render_combined` are accessible.

**Assertion pseudocode:**
```
import afspec
ASSERT hasattr(afspec, "load_spec")
ASSERT hasattr(afspec, "Spec")
ASSERT hasattr(afspec, "render_combined")
```

### TS-132-2: afspec loads a valid v1.2 spec

**Requirement:** 132-REQ-1.3
**Type:** integration
**Description:** Verify that afspec.load_spec returns a populated Spec.

**Preconditions:**
- A temporary directory with valid v1.2 spec files (prd.md with frontmatter,
  requirements.json, test_spec.json, tasks.json).

**Input:**
- Path to the temporary spec directory.

**Expected:**
- Returns an `afspec.Spec` with non-empty `prd`, `requirements`, `test_spec`,
  `tasks` fields.

**Assertion pseudocode:**
```
spec = afspec.load_spec(tmp_dir)
ASSERT spec.prd.frontmatter.spec_id != ""
ASSERT len(spec.requirements.requirements) >= 0
ASSERT spec.tasks is not None
```

### TS-132-3: SpecFormat enum has expected values

**Requirement:** 132-REQ-2.1
**Type:** unit
**Description:** Verify SpecFormat enum exists with V1_MARKDOWN and V1_2_JSON.

**Preconditions:**
- None.

**Input:**
- N/A

**Expected:**
- `SpecFormat.V1_MARKDOWN` and `SpecFormat.V1_2_JSON` exist.

**Assertion pseudocode:**
```
from agent_fox.spec.discovery import SpecFormat
ASSERT SpecFormat.V1_MARKDOWN.value == "v1_markdown"
ASSERT SpecFormat.V1_2_JSON.value == "v1_2_json"
```

### TS-132-4: SpecInfo has format field

**Requirement:** 132-REQ-2.2
**Type:** unit
**Description:** Verify SpecInfo dataclass includes a format field.

**Preconditions:**
- None.

**Input:**
- Construct a SpecInfo with format=V1_2_JSON.

**Expected:**
- The format field is accessible and equals V1_2_JSON.

**Assertion pseudocode:**
```
info = SpecInfo(name="test", prefix=1, path=Path("/tmp"), has_tasks=True, has_prd=True, format=SpecFormat.V1_2_JSON)
ASSERT info.format == SpecFormat.V1_2_JSON
```

### TS-132-5: Format detection identifies v1.2 by requirements.json

**Requirement:** 132-REQ-3.1
**Type:** unit
**Description:** A folder with requirements.json is classified as V1_2_JSON.

**Preconditions:**
- A temp directory containing requirements.json.

**Input:**
- Path to the temp directory.

**Expected:**
- `_detect_format()` returns `SpecFormat.V1_2_JSON`.

**Assertion pseudocode:**
```
create tmp_dir with empty requirements.json
result = _detect_format(tmp_dir)
ASSERT result == SpecFormat.V1_2_JSON
```

### TS-132-6: Format detection identifies v1 by requirements.md

**Requirement:** 132-REQ-3.2
**Type:** unit
**Description:** A folder with requirements.md but no requirements.json is V1_MARKDOWN.

**Preconditions:**
- A temp directory containing requirements.md but not requirements.json.

**Input:**
- Path to the temp directory.

**Expected:**
- `_detect_format()` returns `SpecFormat.V1_MARKDOWN`.

**Assertion pseudocode:**
```
create tmp_dir with requirements.md (no requirements.json)
result = _detect_format(tmp_dir)
ASSERT result == SpecFormat.V1_MARKDOWN
```

### TS-132-7: Discovery excludes v1 markdown specs

**Requirement:** 132-REQ-3.3
**Type:** integration
**Description:** discover_specs returns only V1_2_JSON specs.

**Preconditions:**
- A temp specs root with two folders:
  - `01_legacy/` containing requirements.md, tasks.md, prd.md
  - `02_modern/` containing requirements.json, tasks.json, prd.md (with frontmatter)

**Input:**
- Path to the temp specs root.

**Expected:**
- Only `02_modern` appears in the result list.

**Assertion pseudocode:**
```
specs = discover_specs(tmp_root)
ASSERT len(specs) == 1
ASSERT specs[0].name == "02_modern"
ASSERT specs[0].format == SpecFormat.V1_2_JSON
```

### TS-132-8: Discovery checks tasks.json for has_tasks

**Requirement:** 132-REQ-3.4
**Type:** unit
**Description:** For v1.2 specs, has_tasks reflects tasks.json existence.

**Preconditions:**
- A v1.2 spec folder with tasks.json present.

**Input:**
- Path to the spec folder.

**Expected:**
- SpecInfo.has_tasks is True.

**Assertion pseudocode:**
```
specs = discover_specs(tmp_root_with_tasks_json)
ASSERT specs[0].has_tasks == True
```

### TS-132-9: afspec render_combined produces markdown

**Requirement:** 132-REQ-4.2
**Type:** integration
**Description:** render_combined returns non-empty markdown from a loaded spec.

**Preconditions:**
- A valid v1.2 spec loaded via afspec.load_spec.

**Input:**
- The loaded Spec object.

**Expected:**
- Non-empty string containing markdown content.

**Assertion pseudocode:**
```
spec = afspec.load_spec(tmp_dir)
md = afspec.render_combined(spec)
ASSERT len(md) > 0
ASSERT "# " in md
```

## Property Test Cases

### TS-132-P1: Format detection is deterministic

**Property:** Property 1 from design.md
**Validates:** 132-REQ-3.1, 132-REQ-3.2
**Type:** property
**Description:** Format detection always returns the same result for the same file set.

**For any:** combination of files (requirements.json present/absent, requirements.md present/absent)
**Invariant:** `_detect_format(dir)` returns the same value on repeated calls

**Assertion pseudocode:**
```
FOR ANY file_set IN {requirements.json, requirements.md} combinations:
    create tmp_dir with file_set
    result1 = _detect_format(tmp_dir)
    result2 = _detect_format(tmp_dir)
    ASSERT result1 == result2
    IF "requirements.json" IN file_set:
        ASSERT result1 == V1_2_JSON
    ELSE:
        ASSERT result1 == V1_MARKDOWN
```

### TS-132-P2: Discovery returns only v1.2 specs

**Property:** Property 2 from design.md
**Validates:** 132-REQ-3.3
**Type:** property
**Description:** No v1 spec ever appears in discovery results.

**For any:** mix of v1 and v1.2 spec folders in a specs root
**Invariant:** every SpecInfo in the result has format == V1_2_JSON

**Assertion pseudocode:**
```
FOR ANY spec_folders IN mixed_format_generator:
    create tmp_root with spec_folders
    results = discover_specs(tmp_root)
    FOR EACH info IN results:
        ASSERT info.format == SpecFormat.V1_2_JSON
```

## Edge Case Tests

### TS-132-E1: Folder with neither requirements file is skipped

**Requirement:** 132-REQ-2.E1
**Type:** unit
**Description:** A spec folder missing both requirements files is excluded.

**Preconditions:**
- A spec folder `01_empty/` with only prd.md and tasks.json.

**Input:**
- Path to specs root containing the folder.

**Expected:**
- The folder does not appear in discover_specs results.

**Assertion pseudocode:**
```
create tmp_root with 01_empty/ (prd.md, tasks.json, no requirements.*)
specs = discover_specs(tmp_root)
ASSERT len(specs) == 0
```

### TS-132-E2: JSON takes precedence when both formats present

**Requirement:** 132-REQ-3.E1
**Type:** unit
**Description:** When both requirements.md and requirements.json exist, JSON wins.

**Preconditions:**
- A spec folder with both requirements.md and requirements.json.

**Input:**
- Path to specs root.

**Expected:**
- Format is V1_2_JSON.

**Assertion pseudocode:**
```
create tmp_root with 01_both/ (requirements.md, requirements.json, prd.md, tasks.json, test_spec.json)
specs = discover_specs(tmp_root)
ASSERT specs[0].format == SpecFormat.V1_2_JSON
```

### TS-132-E3: Malformed JSON raises LoadError

**Requirement:** 132-REQ-4.E1
**Type:** unit
**Description:** afspec.load_spec raises LoadError for malformed JSON.

**Preconditions:**
- A spec folder with invalid JSON in requirements.json.

**Input:**
- Path to the spec folder.

**Expected:**
- afspec.LoadError is raised.

**Assertion pseudocode:**
```
create tmp_dir with malformed requirements.json
ASSERT_RAISES afspec.LoadError:
    afspec.load_spec(tmp_dir)
```

## Integration Smoke Tests

### TS-132-SMOKE-1: Discovery to load end-to-end

**Execution Path:** Path 1 + Path 2 from design.md
**Description:** Discover a v1.2 spec folder and load it via afspec.

**Setup:** Temp specs root with one v1.2 spec folder containing valid artifacts.
Mockable: filesystem is real (tmpdir).

**Trigger:** `discover_specs(tmp_root)` then `afspec.load_spec(info.path)`

**Expected side effects:**
- discover_specs returns exactly one SpecInfo with format V1_2_JSON
- afspec.load_spec returns a Spec with populated artifacts

**Must NOT satisfy with:** No mocking of discovery or afspec.load_spec.

**Assertion pseudocode:**
```
specs = discover_specs(tmp_root)
ASSERT len(specs) == 1
spec = afspec.load_spec(specs[0].path)
ASSERT spec.prd.frontmatter.spec_id != ""
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 132-REQ-1.2 | TS-132-1 | unit |
| 132-REQ-1.3 | TS-132-2 | integration |
| 132-REQ-2.1 | TS-132-3 | unit |
| 132-REQ-2.2 | TS-132-4 | unit |
| 132-REQ-3.1 | TS-132-5 | unit |
| 132-REQ-3.2 | TS-132-6 | unit |
| 132-REQ-3.3 | TS-132-7 | integration |
| 132-REQ-3.4 | TS-132-8 | unit |
| 132-REQ-4.1 | TS-132-2 | integration |
| 132-REQ-4.2 | TS-132-9 | integration |
| 132-REQ-2.E1 | TS-132-E1 | unit |
| 132-REQ-3.E1 | TS-132-E2 | unit |
| 132-REQ-4.E1 | TS-132-E3 | unit |
| Property 1 | TS-132-P1 | property |
| Property 2 | TS-132-P2 | property |
