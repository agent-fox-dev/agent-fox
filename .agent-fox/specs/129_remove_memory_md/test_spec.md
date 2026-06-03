# Test Specification: Remove docs/memory.md

## Overview

Tests verify that `docs/memory.md` is gone, that `init` no longer creates it,
and that no dangling references remain. Most tests are simple filesystem and
string-content assertions.

## Test Cases

### TS-129-1: File deleted from repository

**Requirement:** 129-REQ-1.1
**Type:** unit
**Description:** Verify `docs/memory.md` does not exist in the repo.

**Preconditions:**
- Repository root path known.

**Input:**
- Check filesystem path `REPO_ROOT / "docs" / "memory.md"`.

**Expected:**
- Path does not exist.

**Assertion pseudocode:**
```
ASSERT NOT (REPO_ROOT / "docs" / "memory.md").exists()
```

### TS-129-2: init does not create memory.md

**Requirement:** 129-REQ-2.1
**Type:** integration
**Description:** Verify `agent-fox init` does not create `docs/memory.md`.

**Preconditions:**
- Fresh tmp_git_repo fixture.

**Input:**
- Run `agent-fox init` via CliRunner.

**Expected:**
- `docs/memory.md` does not exist in tmp_git_repo.

**Assertion pseudocode:**
```
runner.invoke(main, ["init"])
ASSERT NOT (tmp_git_repo / "docs" / "memory.md").exists()
```

### TS-129-3: _DOCS_MEMORY_CONTENT removed

**Requirement:** 129-REQ-2.2
**Type:** unit
**Description:** Verify the constant is gone from init_project.py.

**Preconditions:**
- Source file readable.

**Input:**
- Read `agent_fox/workspace/init_project.py`.

**Expected:**
- No occurrence of `_DOCS_MEMORY_CONTENT`.

**Assertion pseudocode:**
```
source = read("agent_fox/workspace/init_project.py")
ASSERT "_DOCS_MEMORY_CONTENT" NOT IN source
```

### TS-129-4: Template does not reference memory.md

**Requirement:** 129-REQ-3.1
**Type:** unit
**Description:** Verify the agents_md template has no memory.md references.

**Preconditions:**
- Template file readable.

**Input:**
- Read `agent_fox/_templates/agents_md.md`.

**Expected:**
- No occurrence of `memory.md`.

**Assertion pseudocode:**
```
content = read("agent_fox/_templates/agents_md.md")
ASSERT "memory.md" NOT IN content
```

### TS-129-5: CLAUDE.md does not reference memory.md

**Requirement:** 129-REQ-3.2
**Type:** unit
**Description:** Verify CLAUDE.md has no memory.md references.

**Preconditions:**
- CLAUDE.md readable.

**Input:**
- Read `CLAUDE.md`.

**Expected:**
- No occurrence of `memory.md`.

**Assertion pseudocode:**
```
content = read("CLAUDE.md")
ASSERT "memory.md" NOT IN content
```

### TS-129-6: AGENTS.md does not reference memory.md

**Requirement:** 129-REQ-3.3
**Type:** unit
**Description:** Verify AGENTS.md has no memory.md references.

**Preconditions:**
- AGENTS.md readable.

**Input:**
- Read `AGENTS.md`.

**Expected:**
- No occurrence of `memory.md`.

**Assertion pseudocode:**
```
content = read("AGENTS.md")
ASSERT "memory.md" NOT IN content
```

### TS-129-7: Agent profile does not reference memory.md

**Requirement:** 129-REQ-3.4
**Type:** unit
**Description:** Verify agent.md profile has no memory.md references.

**Preconditions:**
- Profile file readable.

**Input:**
- Read `agent_fox/_templates/profiles/agent.md`.

**Expected:**
- No occurrence of `memory.md`.

**Assertion pseudocode:**
```
content = read("agent_fox/_templates/profiles/agent.md")
ASSERT "memory.md" NOT IN content
```

### TS-129-8: af-fix skill template does not reference memory.md

**Requirement:** 129-REQ-4.1, 129-REQ-4.2
**Type:** unit
**Description:** Verify both af-fix skill locations have no memory.md references.

**Preconditions:**
- Skill files readable.

**Input:**
- Read template and installed skill.

**Expected:**
- No occurrence of `memory.md` in either file.

**Assertion pseudocode:**
```
template = read("agent_fox/_templates/skills/af-fix")
ASSERT "memory.md" NOT IN template
installed = read(".claude/skills/af-fix/SKILL.md")
ASSERT "memory.md" NOT IN installed
```

### TS-129-9: No init tests for memory.md

**Requirement:** 129-REQ-5.1
**Type:** unit
**Description:** Verify test_init.py has no memory.md test methods.

**Preconditions:**
- Test file readable.

**Input:**
- Read `tests/integration/test_init.py`.

**Expected:**
- No test method name containing `memory`.

**Assertion pseudocode:**
```
source = read("tests/integration/test_init.py")
ASSERT "test_init_creates_docs_memory_md" NOT IN source
ASSERT "test_reinit_preserves_existing_seed_files" NOT IN source
```

## Edge Case Tests

(No edge cases — this spec is purely subtractive with no conditional behavior.)

## Property Test Cases

### TS-129-P1: No dangling references anywhere

**Property:** Property 1 from design.md
**Validates:** 129-REQ-6.1
**Type:** property
**Description:** No tracked file references docs/memory.md except audits and specs.

**For any:** git-tracked `.py` or `.md` file outside `docs/audits/` and
`.agent-fox/specs/`
**Invariant:** File content does not contain `docs/memory.md`.

**Assertion pseudocode:**
```
FOR ANY py_or_md_file IN git_tracked_files():
    IF "docs/audits/" IN path OR ".agent-fox/specs/" IN path:
        SKIP
    content = read(py_or_md_file)
    ASSERT "docs/memory.md" NOT IN content
```

### TS-129-P2: init does not create memory.md

**Property:** Property 2 from design.md
**Validates:** 129-REQ-2.1
**Type:** unit
**Description:** init_project never creates docs/memory.md.

**Assertion pseudocode:**
```
init_project(tmp_path)
ASSERT NOT (tmp_path / "docs" / "memory.md").exists()
```

## Integration Smoke Tests

### TS-129-SMOKE-1: Full init without memory.md

**Execution Path:** Path 1 from design.md
**Description:** End-to-end init run does not produce docs/memory.md.

**Setup:** Fresh git repo in tmp_path. No mocks.

**Trigger:** `runner.invoke(main, ["init"])` via CliRunner.

**Expected side effects:**
- `.agent-fox/` directory created.
- `docs/memory.md` does NOT exist.

**Must NOT satisfy with:** Mocking `init_project` or `_ensure_seed_files`.

**Assertion pseudocode:**
```
runner = CliRunner()
result = runner.invoke(main, ["init"])
ASSERT result.exit_code == 0
ASSERT NOT (tmp_git_repo / "docs" / "memory.md").exists()
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 129-REQ-1.1 | TS-129-1 | unit |
| 129-REQ-2.1 | TS-129-2, TS-129-P2 | integration, unit |
| 129-REQ-2.2 | TS-129-3 | unit |
| 129-REQ-3.1 | TS-129-4 | unit |
| 129-REQ-3.2 | TS-129-5 | unit |
| 129-REQ-3.3 | TS-129-6 | unit |
| 129-REQ-3.4 | TS-129-7 | unit |
| 129-REQ-4.1 | TS-129-8 | unit |
| 129-REQ-4.2 | TS-129-8 | unit |
| 129-REQ-5.1 | TS-129-9 | unit |
| 129-REQ-6.1 | TS-129-P1 | property |
| Property 1 | TS-129-P1 | property |
| Property 2 | TS-129-P2 | unit |
