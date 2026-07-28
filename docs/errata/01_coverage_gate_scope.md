# Errata: 01-REQ-9.2 Coverage Gate Scope

## Spec Divergence

**Requirement 01-REQ-9.2** states: "at least 90% line coverage on all new
flag handler code as measured by `pytest --cov`."

**Test spec TS-01-29** translates this as:
`pytest --cov=af.plan --cov-fail-under=90`

This measures the **entire** `af.plan` module, not just the new flag handler
code. The module also contains substantial pre-existing code (`_verify_plan`,
`_node_to_dict`, `_edge_to_dict`, `_metadata_to_dict`, and the main
plan-building/persistence logic at lines 195-630) which has its own tests
in other test files (e.g., `test_plan_verify.py`, `test_plan.py`).

## Actual Coverage

- **New handler code** (`_handle_clear`, `_handle_reset`, `_handle_reset_hard`,
  `_check_mode_exclusivity`, `_display_reset_result`,
  `_display_hard_reset_result`): **100% line coverage**.
- **New `run_plan()` parameters** (clear, reset, reset_hard, target branches
  in `agentfox.graph.planner`): **100% line coverage**.
- **Overall `af.plan` module**: 61% (pre-existing code is untested by the
  spec-01 test files).
- **Overall `agentfox.graph.planner`**: 44% (pre-existing code is untested
  by the spec-01 test files).

## Resolution

The requirement's intent (90% on new code) is met. The test spec command
would need to use `--cov-context` or a narrower `--cov` source filter to
match the requirement's scope. No code changes are needed.
