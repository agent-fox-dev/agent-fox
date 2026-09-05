# Model Tiers and Retry Behavior

This document describes how af selects models for each archetype and
how failed sessions are retried.

## Model Tiers

Three tiers are defined, ordered lowest to highest:

| Tier | Default Model |
|------|---------------|
| SIMPLE | claude-haiku-4-5 |
| STANDARD | claude-sonnet-4-6 |
| ADVANCED | claude-opus-4-6 |

## Archetype Default Assignments

Each archetype/mode pair has a default tier and effort level configured in
`ARCHETYPE_REGISTRY`. These defaults are the starting point for every session.

| Agent / Mode | Default Tier | Effort |
|---|---|---|
| coder | STANDARD | xhigh |
| reviewer (pre-flight) | ADVANCED | high |
| reviewer (audit-review) | ADVANCED | high |
| verifier | STANDARD | high |
| gate | STANDARD | low |

## Resolution Priority

Model tier is resolved through three layers, highest priority first:

```
1. Mode-level config override        [archetypes.overrides.<name>.modes.<mode>]
2. Per-archetype config override      [archetypes.overrides.<name>]
3. Archetype registry default         (ARCHETYPE_REGISTRY in archetypes.py)
```

When any of layers 1–2 is set, it takes precedence over the registry default.
The first non-null value encountered wins.

### Configuration Example

```toml
# Override the coder to use ADVANCED tier
[archetypes.overrides.coder]
model_tier = "ADVANCED"

# Override only the audit-review mode of reviewer
[archetypes.overrides.reviewer.modes.audit-review]
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
| `routing.timeout_multiplier` | 1.5 | Factor by which `session_timeout` is extended |
| `routing.timeout_ceiling_factor` | 2.0 | Maximum session_timeout as multiple of original value |

On each timeout retry the session timeout is extended by the multiplier and
clamped to the ceiling. `max_turns` is **not** extended: a timeout means the
session ran out of wall-clock time, not out of turns. The model tier remains
unchanged. Only after timeout retries are exhausted does the task fall through
to the normal failure path.

### Budget Exhaustion

When a session's cost approaches the per-session budget cap (≥90% of the limit),
the session is classified as budget-exhausted and is **not retried** — repeating
the same work would burn the same budget again.

### Transport Errors

Transient connection errors are retried internally by the Claude backend: up
to 3 attempts with exponential backoff (1 s, then 2 s). If the error surfaces
to the orchestrator, the task is reset to `pending` without consuming a retry
attempt.

### Review-Triggered Retries

Two registry entries have `retry_predecessor = true`:

- **reviewer (audit-review)**: When test quality findings indicate MISSING or
  MISALIGNED tests, the preceding coder session is re-run with the findings
  injected as context. This is tracked by a separate `audit_max_retries`
  counter (default: 1).
- **verifier**: The flag applies on the failure path only — a verifier session
  that fails to complete resets its predecessor coder, using the standard
  `max_retries` counter. Verifier PASS/FAIL verdicts are informational and
  trigger no retry.

**pre-flight** deliberately does not have it: it runs *before* the coder, so
blocking findings block the downstream coder rather than retrying a
predecessor that has not run yet.
