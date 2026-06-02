# PRD: Strip night-shift to fix-only mode

## Problem

The `night-shift` command currently runs three independent work streams:

1. **spec-executor** — discovers and executes new specs from `.agent-fox/specs/`
2. **fix-pipeline** — polls for `af:fix`-labelled issues and runs the fix
   pipeline (triage → coder → reviewer)
3. **hunt-scan** — scans the codebase for maintenance issues, consolidates
   findings with an LLM critic, deduplicates, and creates GitHub issues

The spec-executor and hunt-scan streams are being removed. Night-shift should
become a fix-only daemon: it polls for `af:fix` issues and processes them
through the fix pipeline. Nothing else.

## Scope

### In scope

1. **Remove the hunt-scan stream and all supporting modules.** Delete the
   following source modules and their tests:
   - `agent_fox/nightshift/hunt.py`
   - `agent_fox/nightshift/critic.py`
   - `agent_fox/nightshift/dedup.py`
   - `agent_fox/nightshift/finding.py`
   - `agent_fox/nightshift/ignore_filter.py`
   - `agent_fox/nightshift/ignore.py`
   - `agent_fox/nightshift/categories/` (entire directory)

2. **Remove the spec-executor stream.** Delete `SpecExecutorStream` from
   `streams.py` and remove all spec-discovery wiring from `nightshift.py`
   CLI (the `_SpecBatchRunner` class, `_discover_fn`, `_known_specs`,
   `_orch_factory` setup).

3. **Simplify the CLI.** Remove flags that are no longer meaningful:
   `--auto`, `--no-specs`, `--no-fixes`, `--no-hunts`, `--specs-dir`.

4. **Simplify the engine.** Remove `_run_hunt_scan()`,
   `_run_hunt_scan_inner()`, the `auto_fix` constructor parameter,
   `_hunt_scan_in_progress` flag, and the `embedder` parameter. Remove
   imports of deleted modules.

5. **Simplify the streams module.** Remove `SpecExecutorStream`,
   remove the hunt-scan `EngineWorkStream` from `build_streams()`, and
   remove parameters that are no longer needed (`no_specs`, `no_hunts`,
   `auto`, `discover_fn`, `orch_factory`).

6. **Remove unused config fields.** Delete from `NightShiftConfig`:
   `hunt_scan_interval`, `categories` (and `NightShiftCategoryConfig`),
   `quality_gate_timeout`, `spec_interval`, `enabled_streams`,
   `similarity_threshold`, and their validators. The model already uses
   `extra="ignore"` so existing config files with these fields will
   continue to parse without error.

7. **Clean up init_project.** Remove the `.night-shift` ignore-file
   seed from `agent_fox/workspace/init_project.py` and its import of
   `agent_fox.nightshift.ignore`.

8. **Delete tests** for deleted modules. Update tests for modified modules.

9. **Update documentation.** Revise `docs/architecture/04-night-shift.md`,
   `docs/cli-reference.md`, and `docs/config-reference.md` to reflect
   fix-only behavior.

### Out of scope

- Changing the fix pipeline behavior (triage, coder-reviewer loop,
  staleness detection, drain logic, dependency ordering).
- Removing labels from GitHub or changing `agent-fox init` label creation
  (except removing `.night-shift` file creation).
- Removing modules used by the fix pipeline: `staleness.py`,
  `reference_parser.py`, `dep_graph.py`, `triage.py`, `fix_pipeline.py`,
  `coder_reviewer.py`, `cost_helpers.py`, `daemon.py`,
  `spec_builder.py`.

## Design Decisions

1. **Delete modules rather than disconnect.** Hunt modules form a clean
   cluster with no cross-dependencies into the fix pipeline. Deleting them
   removes ~2,500 lines of dead code and ~30 test files.

2. **Remove config fields rather than deprecate.** `NightShiftConfig` uses
   `extra="ignore"`, so unknown fields in existing config files are silently
   discarded. No backward-compatibility risk.

3. **Keep label creation in `init`.** The `af:hunt`, `af:ignore`, and
   `af:no-change` labels may still be useful for manual workflows or
   future features. Don't remove them.

4. **Skip spec number 124.** The reverted spec-124 work touched many of
   the same files. Using 125 avoids confusion in git history and memory
   references.

5. **Staleness detection stays.** `check_staleness()` is used by the fix
   pipeline after each successful fix to close issues rendered obsolete.
   This is fix-related functionality.

## Source

Source: Input provided by user via interactive prompt

