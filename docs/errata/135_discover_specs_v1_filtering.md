# Erratum: discover_specs() Filters Out V1 Markdown Specs

**Spec:** 135 (v1.2 Skill Template and Validation Migration)
**Date:** 2026-06-15

## Divergence

The design document (design.md) assumes that `discover_specs()` returns
both V1_MARKDOWN and V1_2_JSON specs, allowing `run_lint_specs()` to
partition by format and route each to the appropriate validator.

In reality, `discover_specs()` (from spec 132, requirement 132-REQ-3.3)
unconditionally skips all V1_MARKDOWN specs with an explicit:

```python
if fmt == SpecFormat.V1_MARKDOWN:
    continue
```

This means:
- No v1 spec is ever returned by `discover_specs()`.
- `run_lint_specs()` cannot partition specs by format -- it only ever
  sees v1.2 specs.
- Requirements 135-REQ-1.2 (v1 routing) and 135-REQ-1.3 (mixed routing)
  cannot be fully satisfied without modifying `discover_specs()`.

## Impact on Requirements

- **135-REQ-1.2** (v1 routed to custom validators): Unreachable via the
  normal discovery path. V1 specs are filtered out before routing.
- **135-REQ-1.3** (mixed format validation): Only v1.2 specs reach
  `run_lint_specs()`, so the "mixed" case never occurs naturally.
- **TS-135-2, TS-135-3, TS-135-P2, TS-135-SMOKE-1**: All test cases
  involving v1 specs must mock `discover_specs()` to inject v1 SpecInfo
  objects, since discovery will never produce them.

## Adaptation

Tests that require v1 specs in the validation pipeline mock
`agent_fox.spec.lint.discover_specs` to return SpecInfo objects with
`format=SpecFormat.V1_MARKDOWN`. This tests the routing logic in
isolation without requiring changes to the discovery module.

The implementation should still include format-aware routing in
`run_lint_specs()` -- if `discover_specs()` is later updated to return
v1 specs (e.g., by removing the 132-REQ-3.3 filter), the routing logic
will be ready.

## Impact on Design

The `afspec.validate()` function takes a `Spec` object (not a `Path`).
The correct call sequence is:

```python
spec_obj = afspec.load_spec(spec.path)
errors = afspec.validate(spec_obj)
```

This differs from the design document's stated interface:
`afspec.validate(spec.path)`. The `_validate_v12_spec()` function must
call `afspec.load_spec()` first, introducing an additional failure mode
(`afspec.LoadError`) that should be caught alongside other exceptions.
