# Test Specification: Lint-Specs Cleanup

## Overview

Tests verify that the `--fix` flag and all associated code are removed, that
progress display works correctly, and that the af-spec skill template is
updated. Tests use the same patterns as existing spec tests: Click CliRunner
for CLI tests, direct function calls for backing module tests, and filesystem
checks for code removal verification.

## Test Cases

### TS-127-1: CLI rejects --fix flag

**Requirement:** 127-REQ-1.1
**Type:** unit
**Description:** Verify that `--fix` is no longer accepted by the CLI.

**Preconditions:**
- Click CliRunner available.

**Input:**
- `["lint-specs", "--fix"]` as CLI arguments.

**Expected:**
- Exit code != 0.
- Output contains error text about unrecognized option.

**Assertion pseudocode:**
```
runner = CliRunner()
result = runner.invoke(main, ["lint-specs", "--fix"])
ASSERT result.exit_code != 0
```

### TS-127-2: run_lint_specs has no fix parameter

**Requirement:** 127-REQ-1.2
**Type:** unit
**Description:** Verify that `run_lint_specs()` does not accept a `fix` keyword.

**Preconditions:**
- `run_lint_specs` importable.

**Input:**
- Call `run_lint_specs(specs_dir, fix=True)`.

**Expected:**
- `TypeError` raised (unexpected keyword argument).

**Assertion pseudocode:**
```
ASSERT_RAISES TypeError:
    run_lint_specs(tmp_path, fix=True)
```

### TS-127-3: LintResult has no fix_results field

**Requirement:** 127-REQ-1.3
**Type:** unit
**Description:** Verify that `LintResult` does not have a `fix_results` attribute.

**Preconditions:**
- `LintResult` importable.

**Input:**
- Instantiate `LintResult()`.

**Expected:**
- `hasattr(result, "fix_results")` is `False`.

**Assertion pseudocode:**
```
result = LintResult()
ASSERT NOT hasattr(result, "fix_results")
```

### TS-127-4: fixers package deleted

**Requirement:** 127-REQ-1.4
**Type:** unit
**Description:** Verify that the `agent_fox/spec/fixers/` directory does not exist.

**Preconditions:**
- Repository root path known.

**Input:**
- Check filesystem path.

**Expected:**
- Path does not exist.

**Assertion pseudocode:**
```
fixers_dir = REPO_ROOT / "agent_fox" / "spec" / "fixers"
ASSERT NOT fixers_dir.exists()
```

### TS-127-5: CLI module has no git operations

**Requirement:** 127-REQ-2.1, 127-REQ-2.2
**Type:** unit
**Description:** Verify that `lint_specs.py` does not contain fix-related functions or git imports.

**Preconditions:**
- Source file readable.

**Input:**
- Read `agent_fox/cli/lint_specs.py` content.

**Expected:**
- No occurrences of `_format_fix_summary`, `_git_current_branch`,
  `_create_fix_branch`, `_commit_fixes`, `run_git_sync`.

**Assertion pseudocode:**
```
source = read("agent_fox/cli/lint_specs.py")
FOR EACH name IN ["_format_fix_summary", "_git_current_branch",
                   "_create_fix_branch", "_commit_fixes", "run_git_sync"]:
    ASSERT name NOT IN source
```

### TS-127-6: Backing module has no fix dispatch

**Requirement:** 127-REQ-3.1, 127-REQ-3.2
**Type:** unit
**Description:** Verify that `lint.py` has no fix dispatch functions or fixer imports.

**Preconditions:**
- Source file readable.

**Input:**
- Read `agent_fox/spec/lint.py` content.

**Expected:**
- No occurrences of `_apply_ai_fixes`, `_build_known_specs`,
  `agent_fox.spec.fixers`.

**Assertion pseudocode:**
```
source = read("agent_fox/spec/lint.py")
FOR EACH name IN ["_apply_ai_fixes", "_build_known_specs",
                   "agent_fox.spec.fixers"]:
    ASSERT name NOT IN source
```

### TS-127-7: Progress callback invoked at phases

**Requirement:** 127-REQ-4.2, 127-REQ-4.3
**Type:** unit
**Description:** Verify that `run_lint_specs()` calls the progress callback at
each major phase.

**Preconditions:**
- Specs directory with at least one valid spec.

**Input:**
- Call `run_lint_specs(specs_dir, progress_callback=mock_callback)`.

**Expected:**
- `mock_callback` called at least twice (discovery + validation).

**Assertion pseudocode:**
```
callback = Mock()
run_lint_specs(specs_dir, progress_callback=callback)
ASSERT callback.call_count >= 2
```

### TS-127-8: Progress callback None is safe

**Requirement:** 127-REQ-4.E1
**Type:** unit
**Description:** Verify that omitting progress_callback works identically.

**Preconditions:**
- Specs directory with at least one valid spec.

**Input:**
- Call `run_lint_specs(specs_dir)` (no callback).

**Expected:**
- Returns `LintResult` without error.

**Assertion pseudocode:**
```
result = run_lint_specs(specs_dir)
ASSERT isinstance(result, LintResult)
```

### TS-127-9: Documentation updated

**Requirement:** 127-REQ-6.1
**Type:** unit
**Description:** Verify that CLI reference docs do not mention --fix.

**Preconditions:**
- Docs file readable.

**Input:**
- Read `docs/cli-reference.md` content.

**Expected:**
- The lint-specs section does not contain `--fix`.

**Assertion pseudocode:**
```
content = read("docs/cli-reference.md")
lint_section = extract_section(content, "lint-specs")
ASSERT "--fix" NOT IN lint_section
```

## Edge Case Tests

### TS-127-E1: CLI error message for --fix

**Requirement:** 127-REQ-1.E1
**Type:** unit
**Description:** Verify that `--fix` produces a clear error message.

**Preconditions:**
- Click CliRunner available.

**Input:**
- `["lint-specs", "--fix"]`.

**Expected:**
- Exit code != 0.
- Output or stderr mentions "no such option" or similar.

**Assertion pseudocode:**
```
result = runner.invoke(main, ["lint-specs", "--fix"])
ASSERT result.exit_code != 0
ASSERT "no such option" IN result.output.lower() OR "unrecognized" IN result.output.lower()
```

### TS-127-E2: Progress callback None behaves identically

**Requirement:** 127-REQ-4.E1
**Type:** unit
**Description:** Verify that omitting progress_callback produces the same
LintResult as passing None explicitly.

**Preconditions:**
- Specs directory with at least one valid spec.

**Input:**
- Call `run_lint_specs(specs_dir)` and `run_lint_specs(specs_dir, progress_callback=None)`.

**Expected:**
- Both return LintResult with identical findings and exit_code.

**Assertion pseudocode:**
```
result_default = run_lint_specs(specs_dir)
result_none = run_lint_specs(specs_dir, progress_callback=None)
ASSERT result_default.exit_code == result_none.exit_code
ASSERT len(result_default.findings) == len(result_none.findings)
```

## Property Test Cases

### TS-127-P1: No fixer imports anywhere

**Property:** Property 1 from design.md
**Validates:** 127-REQ-1.4, 127-REQ-3.2
**Type:** property
**Description:** No tracked .py file imports from agent_fox.spec.fixers.

**For any:** git-tracked .py file in the repository
**Invariant:** The file content does not contain `from agent_fox.spec.fixers`
or `import agent_fox.spec.fixers`.

**Assertion pseudocode:**
```
FOR ANY py_file IN git_tracked_py_files():
    content = read(py_file)
    ASSERT "agent_fox.spec.fixers" NOT IN content
```

### TS-127-P2: CLI rejects --fix

**Property:** Property 2 from design.md
**Validates:** 127-REQ-1.1, 127-REQ-1.E1
**Type:** unit
**Description:** The CLI always rejects the --fix flag.

**Assertion pseudocode:**
```
result = runner.invoke(main, ["lint-specs", "--fix"])
ASSERT result.exit_code != 0
```

### TS-127-P3: LintResult has no fix_results

**Property:** Property 3 from design.md
**Validates:** 127-REQ-1.3
**Type:** unit
**Description:** LintResult instances never have fix_results.

**Assertion pseudocode:**
```
result = LintResult()
ASSERT NOT hasattr(result, "fix_results")
```

### TS-127-P4: Progress callback optional

**Property:** Property 4 from design.md
**Validates:** 127-REQ-4.2, 127-REQ-4.E1
**Type:** unit
**Description:** run_lint_specs works with and without callback.

**Assertion pseudocode:**
```
result_no_cb = run_lint_specs(specs_dir)
result_with_cb = run_lint_specs(specs_dir, progress_callback=lambda s: None)
ASSERT type(result_no_cb) == type(result_with_cb)
```

## Integration Smoke Tests

### TS-127-SMOKE-1: Full lint pipeline without --ai

**Execution Path:** Path 1 from design.md
**Description:** End-to-end lint-specs run produces findings without fix code.

**Setup:** Create a temp specs directory with one spec that has a known
structural issue (e.g., missing `## Glossary` section in requirements.md).
Mock nothing.

**Trigger:** `runner.invoke(main, ["lint-specs"])` via CliRunner.

**Expected side effects:**
- Exit code 0 or 1 depending on findings.
- Output contains finding text.
- No git operations performed.

**Must NOT satisfy with:** Mocking `run_lint_specs` or `validate_specs`.

**Assertion pseudocode:**
```
result = runner.invoke(main, ["lint-specs"])
ASSERT result.exit_code IN (0, 1)
ASSERT "findings" IN result.output.lower() OR "No findings" IN result.output
```

### TS-127-SMOKE-2: Full lint pipeline with progress display

**Execution Path:** Path 1 from design.md
**Description:** End-to-end lint run invokes progress callback.

**Setup:** Temp specs directory with one spec. Patch ProgressDisplay to capture
calls.

**Trigger:** `runner.invoke(main, ["lint-specs"])` via CliRunner.

**Expected side effects:**
- ProgressDisplay.start() called.
- ProgressDisplay.stop() called.

**Must NOT satisfy with:** Mocking `run_lint_specs`.

**Assertion pseudocode:**
```
with patch("agent_fox.cli.lint_specs.ProgressDisplay") as mock_cls:
    mock_progress = MagicMock()
    mock_cls.return_value = mock_progress
    result = runner.invoke(main, ["lint-specs"])
ASSERT mock_progress.start.called
ASSERT mock_progress.stop.called
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 127-REQ-1.1 | TS-127-1 | unit |
| 127-REQ-1.2 | TS-127-2 | unit |
| 127-REQ-1.3 | TS-127-3 | unit |
| 127-REQ-1.4 | TS-127-4 | unit |
| 127-REQ-1.E1 | TS-127-E1 | unit |
| 127-REQ-2.1 | TS-127-5 | unit |
| 127-REQ-2.2 | TS-127-5 | unit |
| 127-REQ-3.1 | TS-127-6 | unit |
| 127-REQ-3.2 | TS-127-6 | unit |
| 127-REQ-4.1 | TS-127-SMOKE-2 | integration |
| 127-REQ-4.2 | TS-127-7 | unit |
| 127-REQ-4.3 | TS-127-7 | unit |
| 127-REQ-4.4 | TS-127-SMOKE-2 | integration |
| 127-REQ-4.E1 | TS-127-8, TS-127-E2 | unit |
| 127-REQ-5.1 | TS-127-9 | unit |
| 127-REQ-5.2 | TS-127-9 | unit |
| 127-REQ-5.3 | TS-127-9 | unit |
| 127-REQ-6.1 | TS-127-9 | unit |
| 127-REQ-6.2 | TS-127-9 | unit |
| Property 1 | TS-127-P1 | property |
| Property 2 | TS-127-P2 | unit |
| Property 3 | TS-127-P3 | unit |
| Property 4 | TS-127-P4 | unit |
