# Model Tiers, Variants, and Retry Behavior

This document describes how agent-fox selects models for each archetype and
how failed sessions are retried.

## Model Tiers and Variants

Three tiers are defined, ordered lowest to highest:

| Tier | Default Model |
|------|---------------|
| SIMPLE | claude-haiku-4-5 |
| STANDARD | claude-sonnet-4-6 |
| ADVANCED | claude-opus-4-6 |

The ADVANCED tier supports two variants, ordered by capability:

| Variant | Model ID |
|---------|----------|
| standard | claude-opus-4-6 |
| extended | claude-opus-4-6[1m] |

Variant ordering across all tiers: `fast (0) < standard (1) < extended (2)`.
Tiers with no variant (variant = `None`) never participate in variant upgrades.

## Archetype Default Assignments

Each archetype/mode pair has a default tier and variant configured in
`ARCHETYPE_REGISTRY`. These defaults are the starting point for every session.

| Agent / Mode | Default Tier / Variant |
|---|---|
| coder | STANDARD / standard |
| coder (fix) | STANDARD / standard |
| reviewer (pre-review) | ADVANCED / standard |
| reviewer (drift-review) | STANDARD / standard |
| reviewer (audit-review) | ADVANCED / standard |
| reviewer (fix-review) | ADVANCED / standard |
| verifier | STANDARD / standard |
| maintainer (hunt) | SIMPLE / standard |
| maintainer (fix-triage) | STANDARD / standard |
| maintainer (extraction) | SIMPLE / standard |

## Resolution Priority

Model tier and variant are resolved through three layers, highest priority first:

```
1. Mode-level config override        [archetypes.overrides.<name>.modes.<mode>]
2. Per-archetype config override      [archetypes.overrides.<name>]
3. Archetype registry default         (ARCHETYPE_REGISTRY in archetypes.py)
```

When any of layers 1–2 is set, it takes precedence over the registry default.
The first non-null value encountered wins.

### Configuration Example

```toml
# Override the coder to use ADVANCED with extended variant
[archetypes.overrides.coder]
model_tier = "ADVANCED"
model_variant = "extended"

# Override only the fix mode of coder
[archetypes.overrides.coder.modes.fix]
model_tier = "STANDARD"
```

## Retry Behavior

When a session fails, the orchestrator applies a simple retry counter.

### How It Works

Each task node tracks its attempt count. On each failure:

1. If the attempt count is within the `max_retries` limit (default: 2), the
   task is reset to `pending` for another attempt **at the same model tier**.
2. If retries are exhausted, the task is marked `blocked` and all transitive
   dependents are cascade-blocked.

There is no automatic tier escalation — a task retries at its configured model
tier until it either succeeds or exhausts its retries.

### Timeout Retries

Timeout failures are handled separately with dedicated settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `routing.max_timeout_retries` | 2 | Maximum timeout retries before falling through to failure |
| `routing.timeout_multiplier` | 1.5 | Factor by which max_turns and session_timeout are extended |
| `routing.timeout_ceiling_factor` | 2.0 | Maximum session_timeout as multiple of original value |

On each timeout retry, the session parameters (max turns and timeout) are
extended by the multiplier and clamped to the ceiling. The model tier remains
unchanged. Only after timeout retries are exhausted does the task fall through
to the normal failure path.

### Budget Exhaustion

When a session's cost approaches the per-session budget cap (≥90% of the limit),
the session is classified as budget-exhausted and is **not retried** — repeating
the same work would burn the same budget again.

### Transport Errors

Transient connection errors are retried internally by the Claude backend with
exponential backoff (up to 3 retries). If the error surfaces to the
orchestrator, the task is reset to `pending` without consuming a retry attempt.

### Review-Triggered Retries

Two archetype modes have `retry_predecessor = true`:

- **audit-review**: When test quality findings indicate MISSING or MISALIGNED
  tests, the preceding coder session is re-run with the findings injected as
  context. This is tracked by a separate `audit_max_retries` counter
  (default: 2).
- **verifier**: When verification fails, the preceding coder session is re-run
  with the verification results as context. Uses the standard `max_retries`
  counter.
