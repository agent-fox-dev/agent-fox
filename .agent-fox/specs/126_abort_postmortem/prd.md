# PRD: Abort Post-Mortem Dump

## Problem

When `agent-fox code` exits non-successfully due to blocked tasks, stalls, cost
limits, or session limits, the user has no consolidated diagnostic information
about *why* the run was aborted. The CLI prints a short status line
(`Status: block_limit`) but the details — which tasks blocked, what errors
caused the blocks, how many sessions were consumed — are buried across audit
JSONL files and DuckDB tables. This forces the user into an investigation
session just to understand the failure before they can fix it and re-run.

## Solution

When `run_code()` returns an `ExecutionState` with a non-successful,
non-interrupted status (STALLED, BLOCK_LIMIT, COST_LIMIT, SESSION_LIMIT), the
system writes a single, self-contained JSON file to `.agent-fox/audit/` that
captures everything a consumer needs to diagnose the abort:

- Run identity and status
- Task-level summary (completed, pending, blocked, failed counts)
- Token and cost totals
- Every blocked task with its blocking reason
- The full session history (all attempts with errors, costs, timing, models)

The file uses a stable, versioned schema designed for programmatic consumption
(e.g., a future auto-recovery agent, CI dashboards, or scripted triage). Human
readers can inspect it with `jq`.

## Design Decisions

1. **Trigger scope — all non-completed exits except INTERRUPTED.** A user
   who hits Ctrl-C knows why the run stopped. All other non-completed exits
   (STALLED, BLOCK_LIMIT, COST_LIMIT, SESSION_LIMIT) warrant a diagnostic
   dump.

2. **Detail level — full diagnostic.** The post-mortem includes the complete
   session history, not just blocked task summaries. This avoids the
   follow-up investigation that the feature is designed to prevent.

3. **Consumer — machine-first.** The JSON schema is designed for programmatic
   consumption with stable field names, consistent types (always arrays, not
   null when empty), and a `schema_version` field for forward compatibility.
   Human readers use `jq`.

4. **File naming — `postmortem_{run_id}.json`.** Uses the orchestrator's
   existing run_id (`{YYYYMMDD}_{HHMMSS}_{hex}`) for correlation with audit
   event files in the same directory.

5. **Non-blocking generation.** If post-mortem writing fails for any reason,
   a warning is logged and the run continues to its normal exit. The feature
   must never turn a recoverable exit into a crash.

6. **run_id on ExecutionState.** The run_id is currently private to the
   Orchestrator. Adding it to ExecutionState makes it available to
   `run_code()` and downstream consumers without coupling them to the
   Orchestrator internals.

7. **CLI path display.** When a post-mortem is written, the CLI summary
   prints its path so the user knows where to find it.

## Source

Source: Input provided by user via interactive prompt
