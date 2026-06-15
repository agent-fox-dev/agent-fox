# Requirements Document

## Introduction

This spec removes the `--fix` flag and all auto-fix machinery from the
`lint-specs` command, adds progress display for user feedback, and updates the
`/af-spec` skill to use `lint-specs` for validation.

## Glossary

- **EARS**: Easy Approach to Requirements Syntax -- a structured pattern for
  writing testable requirements using keywords like SHALL, WHEN, WHILE, IF.
- **ProgressDisplay**: The Rich-based progress UI component (`agent_fox.ui.progress`)
  that shows a spinner with status text and permanent milestone lines.
- **fixers package**: The `agent_fox/spec/fixers/` directory containing modules
  for auto-fixing lint findings.
- **progress callback**: A callable `(str) -> None` that receives phase-level
  status messages during lint execution.
- **af-spec skill**: The Claude Code skill template that generates specification
  packages (prd.md, requirements.md, design.md, test_spec.md, tasks.md).

## Requirements

### Requirement 1: Remove --fix CLI flag

**User Story:** As a developer, I want the lint-specs command to only report
findings without attempting auto-fixes, so that spec modifications remain under
human or agent control.

#### Acceptance Criteria

1. [127-REQ-1.1] WHEN the user runs `agent-fox lint-specs`, THE CLI SHALL NOT
   accept a `--fix` flag. Click SHALL reject `--fix` as an unrecognized option.
2. [127-REQ-1.2] THE `run_lint_specs()` function SHALL NOT accept a `fix`
   parameter.
3. [127-REQ-1.3] THE `LintResult` dataclass SHALL NOT have a `fix_results`
   field.
4. [127-REQ-1.4] THE `agent_fox/spec/fixers/` package SHALL NOT exist in the
   source tree.

#### Edge Cases

1. [127-REQ-1.E1] WHEN the user runs `agent-fox lint-specs --fix`, THE CLI
   SHALL exit with a non-zero code and display an error message indicating
   `--fix` is not a recognized option.

### Requirement 2: Remove fix-related code from CLI handler

**User Story:** As a maintainer, I want all fix-specific code removed from the
CLI handler so the module is simpler and has no dead code.

#### Acceptance Criteria

1. [127-REQ-2.1] THE `lint_specs.py` CLI module SHALL NOT contain functions for
   git branch creation, committing fixes, or formatting fix summaries.
2. [127-REQ-2.2] THE `lint_specs.py` CLI module SHALL NOT import `run_git_sync`
   from `agent_fox.workspace.git`.

### Requirement 3: Remove fix dispatch from backing module

**User Story:** As a maintainer, I want the backing module (`lint.py`) to
contain only linting logic, with no fix dispatch code.

#### Acceptance Criteria

1. [127-REQ-3.1] THE `lint.py` module SHALL NOT contain `_apply_ai_fixes`,
   `_apply_ai_fixes_async`, or `_build_known_specs` functions.
2. [127-REQ-3.2] THE `lint.py` module SHALL NOT import from
   `agent_fox.spec.fixers`.

### Requirement 4: Progress display for lint-specs

**User Story:** As a user running `lint-specs --ai`, I want to see a spinner
and status messages so I know the command is working during long-running AI
analysis.

#### Acceptance Criteria

1. [127-REQ-4.1] WHEN `lint-specs` is run in a TTY (not in JSON mode and not
   quiet), THE CLI SHALL display a spinner with phase-level status messages.
2. [127-REQ-4.2] THE `run_lint_specs()` function SHALL accept an optional
   `progress_callback` parameter of type `Callable[[str], None] | None`
   (default `None`).
3. [127-REQ-4.3] WHEN a progress callback is provided, THE `run_lint_specs()`
   function SHALL call it with a status message at each major phase:
   discovering specs, running static validation, and running AI validation.
4. [127-REQ-4.4] WHEN `lint-specs` is run with `--json` or `--quiet`, THE CLI
   SHALL suppress the progress display.

#### Edge Cases

1. [127-REQ-4.E1] IF `progress_callback` is `None`, THEN `run_lint_specs()`
   SHALL execute identically to a run with a callback, just without emitting
   progress messages.

### Requirement 5: Update af-spec skill to use lint-specs

**User Story:** As a spec author using the `/af-spec` skill, I want the skill
to automatically run `agent-fox lint-specs` to validate generated specs, so
issues are caught immediately.

#### Acceptance Criteria

1. [127-REQ-5.1] THE af-spec skill template SHALL include a validation step
   that instructs the agent to run `agent-fox lint-specs` on the generated spec
   folder after all five documents are written.
2. [127-REQ-5.2] THE af-spec skill template SHALL instruct the agent to fix any
   lint errors or warnings before presenting the spec to the user.
3. [127-REQ-5.3] THE af-spec skill template's completeness checklist SHALL
   clearly mark items not validated by lint-specs as "(manual check)".

### Requirement 6: Update documentation

**User Story:** As a user reading the CLI reference, I want accurate
documentation that reflects the removal of `--fix`.

#### Acceptance Criteria

1. [127-REQ-6.1] THE `docs/cli-reference.md` entry for `lint-specs` SHALL NOT
   mention `--fix`, auto-fix, git branch creation, or criteria rewriting.
2. [127-REQ-6.2] THE `docs/cli-reference.md` entry for `lint-specs` SHALL
   document that the command displays a progress spinner during execution.
