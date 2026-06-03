# PRD: Remove Dead `--debug` Flag from `code` Command

## Problem

The `code` command exposes a `--debug` flag described as "Enable debug audit
trail (JSONL + DuckDB tool signals)." In practice, the flag does nothing:

- **DuckDB tool telemetry** is always-on since fix #282. The `DuckDBSink`
  accepts a `debug` parameter for API compatibility but never reads it.
- **JSONL audit trail** (AgentTraceSink) is unconditionally registered in
  `_setup_infrastructure` regardless of the `--debug` flag.
- **The `self._debug` field** in `DuckDBSink` is stored but never referenced
  in any write path.

The flag adds dead plumbing through four layers (`code_cmd` → `run_code` →
`_setup_infrastructure` → `DuckDBSink`) and inflates the dry-run mutual
exclusion logic with a branch that can never have a user-visible effect.

## Goal

Remove the `--debug` flag and all its plumbing so the codebase accurately
reflects behavior (audit/telemetry is always-on) and the CLI surface is not
cluttered by a no-op option.

## Scope

### In scope

1. **CLI flag removal** — delete the `--debug` Click option from `code_cmd`.
2. **Internal API cleanup** — remove the `debug` parameter from `run_code()`,
   `_setup_infrastructure()`, and `DuckDBSink.__init__()`.
3. **Dry-run conflict simplification** — remove `--debug` from the
   `_check_dry_run_conflicts` function and its tests.
4. **Stale docstring/comment cleanup** — update `DuckDBSink` class/module
   docstrings, `SessionSink` protocol method docstrings, and integration test
   descriptions that reference "debug mode" or `debug=True`.
5. **Documentation** — update `docs/cli-reference.md` to remove the `--debug`
   row and its mention in the dry-run mutual exclusion paragraph.
6. **Test cleanup** — remove `TestDebugFlag` class, remove
   `TestMutualExclusionDebug` class, update parametrized flag-combo tests,
   update DuckDB sink tests and property tests that pass `debug=`, and
   update integration smoke test docstrings.

### Out of scope

- Changing audit/telemetry behavior (remains always-on).
- Modifying archived specs (123, 103, 11, etc.) — they are historical records.
- Adding new flags or functionality.

## Design Decisions

1. **Sink protocol docstrings** — The `SessionSink.record_tool_call` and
   `record_tool_error` docstrings say "May be a no-op in non-debug mode."
   This language is stale. Decision: update them to say "Record a tool
   invocation" / "Record a tool error" without debug-mode caveats, since
   behavior is always-on.

2. **Spec 123 (dry-run) requirements.md** — This spec references `--debug` in
   its mutual-exclusion requirements. Decision: leave spec 123 as-is. Specs
   are point-in-time documents; the code is what matters. The divergence is
   expected — spec 131 supersedes the relevant `--debug` parts.

3. **DuckDB sink module docstring** — Line 1 says "tool signals (debug-only)"
   which is already wrong post-#282. Decision: update to "tool signals
   (always-on)" as part of this spec.

4. **Integration smoke tests** — `test_agent_trace_smoke.py` references
   `debug=True` in docstrings and comments but does not actually pass `debug`
   to any function being tested. Decision: update the stale docstrings and
   comments to remove debug references; no functional test changes needed.

5. **DuckDB sink tests** — Tests pass `debug=True` and `debug=False` to verify
   behavior is identical regardless. After removal, the parameter no longer
   exists. Decision: collapse the debug=True/debug=False test pairs into
   single tests that construct `DuckDBSink(conn)` without the parameter. The
   property tests similarly collapse from two cases to one.

## Source

Source: Input provided by user via interactive prompt.
