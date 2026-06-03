# PRD: Lint-Specs Cleanup

## Problem Statement

The `agent-fox lint-specs` command carries auto-fix functionality (`--fix` flag)
that adds complexity without proportional value. The fix workflow creates git
branches, commits changes, and rewrites spec files, all of which are risky
automated modifications to specification documents that should be authored by
humans or agents with full context. Meanwhile, the command produces no progress
feedback during long-running `--ai` analysis, leaving users uncertain whether
the tool is working.

Additionally, the `/af-spec` skill generates specs but relies on a manual
completeness checklist instead of invoking `agent-fox lint-specs` for
validation, creating a gap where specs pass the checklist but fail the linter.

## Goals

1. **Strip `--fix`:** Remove the `--fix` flag, the entire `fixers/` package, AI
   fix dispatch, git operations, and all associated tests. The command becomes
   lint-only.

2. **Align af-spec and lint-specs:** Update the `/af-spec` skill template to run
   `agent-fox lint-specs` as the automated validation step after generating
   specs. Keep the manual completeness checklist for items lint-specs cannot
   validate (glossary completeness, return value contracts, smoke test
   non-mock declarations), marking them clearly as manual-only.

3. **Progress display:** Add a spinner and phase-level status messages to the
   `lint-specs` CLI so users see what is happening, especially during the
   long-running `--ai` phase. Use the same `ProgressDisplay` pattern as the
   `code` command.

## Non-Goals

- Adding new validation rules to lint-specs.
- Changing the validation logic or severity levels.
- Modifying the AI validation prompts or models.

## Design Decisions

1. **Entire fixers/ package removed.** The `agent_fox/spec/fixers/` directory
   and all 8 modules within it are deleted. The `LintResult.fix_results` field
   is removed. The `_apply_ai_fixes` and `_apply_ai_fixes_async` functions in
   `lint.py` are removed. The `_build_known_specs` helper in `lint.py` is
   removed (it was only used by the fixer pipeline).

2. **`_MAX_REWRITE_BATCH` and `_MAX_UNTRACED_BATCH` constants removed.** These
   were only used by AI fix dispatch.

3. **Git operations removed from CLI.** The `_format_fix_summary`,
   `_git_current_branch`, `_create_fix_branch`, `_commit_fixes` functions and
   the `run_git_sync` import are removed from `lint_specs.py`.

4. **Progress callback threaded through `run_lint_specs`.** A new optional
   `progress_callback: Callable[[str], None] | None` parameter is added to
   `run_lint_specs()`. The CLI creates a `ProgressDisplay`, passes
   `progress.print_status` as the callback, and the backing module calls it at
   phase boundaries: discovery, static validation, AI validation.

5. **Skill template updated in both locations:** The template source at
   `agent_fox/_templates/skills/af-spec` and the installed copy at
   `.claude/skills/af-spec/SKILL.md` are updated identically.

6. **Manual checklist items preserved.** Three items that lint-specs cannot
   validate mechanically are kept in the af-spec skill checklist with a note
   marking them as manual-only: glossary completeness, return value contracts,
   smoke test non-mock declarations.

## Dependencies

This spec has no cross-spec dependencies.

## Source

Source: Input provided by user via interactive prompt.
