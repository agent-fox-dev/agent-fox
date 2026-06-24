# Errata: 04-REQ-2.E1 — OutputManager fallback instead of RuntimeError

**Spec:** 04_af_agentic_cli
**Requirement:** 04-REQ-2.E1
**Test Spec:** TS-04-E2

## Divergence

The specification requires that any `af` subcommand raises a `RuntimeError`
when `ctx.obj["output"]` is `None` or missing. In practice, many existing
tests invoke subcommands directly (e.g. `runner.invoke(reset_cmd, ...)`)
without going through the group callback that creates `OutputManager`.
Strictly raising `RuntimeError` would break 100+ pre-existing tests,
violating 04-REQ-7.1 (all existing tests pass without modification).

## Implemented behavior

`get_output_manager(ctx)` creates a fallback `OutputManager` with
`json_mode=False` when:

- `ctx.obj` is `None` (uses `ctx.ensure_object(dict)` to initialise)
- `ctx.obj` exists but lacks the `"output"` key

The fallback also checks `ctx.obj.get("json_mode")` and
`ctx.obj.get("json")` to detect JSON mode from legacy test fixtures.

## Rationale

04-REQ-7.1 ("all pre-existing af CLI tests pass without modification")
takes priority over a strict RuntimeError guard. The fallback preserves
backward compatibility while ensuring all subcommands use `OutputManager`
for output dispatch.

## Test coverage

TS-04-E2 was adapted to verify the fallback behavior:
- `test_fallback_created_when_output_key_missing` — verifies default
  `OutputManager` is created and stored back in `ctx.obj`
- `test_fallback_created_when_ctx_obj_is_none` — verifies fallback
  when `ctx.obj` is `None`
- `test_returns_existing_output_manager` — verifies existing
  `OutputManager` is returned when present
