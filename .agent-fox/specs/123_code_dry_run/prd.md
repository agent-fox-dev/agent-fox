# PRD: --dry-run Flag on code Command

## Problem

The `agent-fox code` command always loads the persisted plan from DuckDB and
launches the full orchestrator, which sets up infrastructure (knowledge DB,
sinks, session runners, platform connections), performs workspace health checks,
and dispatches coding sessions to Claude agents. There is no way for a user to
preview which specs and tasks the `code` command is about to work on without
actually running the orchestrator.

Users want a lightweight preview mode to see the current plan state -- which
tasks are pending, which are completed, which are ready for dispatch, and the
execution order -- without triggering any agent sessions, database writes, or
workspace modifications.

## Feature

Add a `--dry-run` flag to the `code` command. When set, the command loads the
persisted plan from DuckDB (read-only), computes the same analysis as
`plan --dry-run` (parallelism phases, critical path, dependency edges), and
displays the results. It does **not** start the orchestrator, set up
infrastructure, run health checks, or dispatch any coding sessions.

The output reuses the same analysis functions and formatter already implemented
for `plan --dry-run` (spec 122): `compute_phases()`, `critical_path()`,
`group_edges()`, and `format_plan_analysis()`.

### Flag Behavior

- `--dry-run` is a boolean flag (default off).
- Composable with `--json` -- produces structured JSON output of the analysis.
- When `--dry-run` is set, the orchestrator is **not** started.
- No infrastructure is set up (no knowledge DB sinks, no session runners, no
  platform connections, no workspace health checks).
- The plan is loaded read-only from DuckDB. No writes occur.
- Completed nodes are filtered out of the analysis (same as `plan --dry-run`).
- Exit code 0 on success, 1 on error (missing plan DB, load failure).
- `--dry-run` is mutually exclusive with `--watch`, `--debug`,
  `--force-clean`, and `--parallel`. If combined, the command prints an error
  and exits with code 1.

### What is Displayed

1. **Plan summary** -- specs, task count, review node count, dependency count.
2. **Parallelism phases** -- groups of tasks that can execute concurrently.
3. **Critical path** -- the longest dependency chain.
4. **Dependency edges** -- all edges grouped by type (intra-spec, cross-spec).

This is identical to the `plan --dry-run` output, but sourced from the
persisted plan rather than a freshly-built plan.

### API Support

The `run_code()` function in `engine/run.py` does **not** gain a `dry_run`
parameter. The dry-run logic is entirely in the CLI layer (`cli/code.py`)
because it is a pure read-only display concern that does not involve the
orchestrator at all.

## Design Decisions

1. **Reuse existing analysis infrastructure.** The `compute_phases()`,
   `critical_path()`, `group_edges()`, and `format_plan_analysis()` functions
   from spec 122 are already implemented and tested. The `code --dry-run`
   flag reuses them directly rather than duplicating analysis logic.

2. **Load from persisted plan, not rebuild.** Unlike `plan --dry-run` which
   builds a fresh plan from specs, `code --dry-run` loads the persisted plan
   from DuckDB. This shows the user exactly what the orchestrator would see,
   including any archetype nodes injected during the last `plan` run.

3. **Filter completed nodes.** Completed nodes are removed from the analysis
   output so the user sees only remaining work. This matches `plan --dry-run`
   behavior (122-REQ-1.4).

4. **CLI-only concern.** The dry-run logic lives entirely in `cli/code.py`.
   No changes to `engine/run.py` or the orchestrator. This keeps the engine
   layer clean and avoids adding conditional branches to the orchestration
   loop.

5. **Mutual exclusion with execution flags.** `--dry-run` is incompatible
   with `--watch`, `--debug`, `--force-clean`, and `--parallel` because those
   flags only make sense when actually running the orchestrator. Combining
   them is a user error.

6. **Same exit codes.** `--dry-run` uses exit code 0 on success and 1 on
   error, consistent with `plan --dry-run`.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 122_plan_analyze | 2 | 1 | Reuses `compute_phases()`, `critical_path()`, `group_edges()`, `format_plan_analysis()` from group 2 where the analyzer module and formatter were implemented |

## Source

Source: Input provided by user via interactive prompt
