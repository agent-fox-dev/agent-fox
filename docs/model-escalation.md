# Model Escalation and Complexity Assessment

This document describes how agent-fox selects and upgrades model tiers at
runtime. Two mechanisms work together: an LLM-driven complexity assessment
(spec 15) and a mechanical escalation ladder (spec 30).

## Model Tiers and Variants

Three tiers are defined in `core/models.py`, ordered lowest to highest:

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
`ARCHETYPE_REGISTRY` (`archetypes.py`). These defaults serve as the **floor**
that the complexity assessor upgrades from.

| Agent / Mode | Default Tier / Variant | Informational Max Upgrade |
|---|---|---|
| coder | STANDARD / standard | ADVANCED / extended |
| coder (fix) | STANDARD / standard | ADVANCED / standard |
| reviewer (pre-review) | ADVANCED / standard | ADVANCED / extended |
| reviewer (drift-review) | STANDARD / standard | ADVANCED / standard |
| reviewer (audit-review) | ADVANCED / standard | ADVANCED / extended |
| reviewer (fix-review) | ADVANCED / standard | ADVANCED / extended |
| verifier | STANDARD / standard | ADVANCED / standard |
| maintainer (hunt) | SIMPLE / standard | STANDARD / standard |
| maintainer (fix-triage) | STANDARD / standard | ADVANCED / standard |
| maintainer (extraction) | SIMPLE / standard | STANDARD / standard |

The "Informational Max Upgrade" column is not enforced at runtime. Upgrades
are bounded naturally by upgrade-only semantics and the ADVANCED tier ceiling
on the escalation ladder.

## Resolution Priority

Model tier is resolved through five layers, highest priority first:

```
1. Mode-level config override      (explicit — wins always)
2. Per-archetype config override   (explicit — wins always)
3. Legacy dict override            (explicit — wins always)
4. LLM assessment upgrade          (dynamic — upgrade-only from base)
5. Archetype registry default      (static — the floor)
```

When any of layers 1–3 is set, the LLM assessment is skipped entirely.

## Mechanism 1: Complexity Assessment (spec 15)

A lightweight LLM call (Haiku by default) evaluates task complexity before
dispatch and can upgrade the model tier and variant.

### How It Works

1. `prepare_launch()` extracts `node_body` from the task graph node and any
   `previous_failure` from the error tracker.
2. `AssessmentManager.assess_node()` sends the task body, archetype, mode,
   base tier/variant, and failure context to `ComplexityAssessor.assess()`.
3. The assessor returns a JSON recommendation:
   ```json
   {
     "recommended_tier": "ADVANCED",
     "recommended_variant": "extended",
     "confidence": 0.82,
     "rationale": "8 subtasks spanning 4 modules with error-handling edge cases"
   }
   ```
4. `apply_assessment()` enforces upgrade-only semantics (see below).
5. A new `EscalationLadder` is constructed from the effective tier/variant.

### Upgrade-Only Rules

`apply_assessment()` in `core/complexity.py` enforces these rules:

- **Confidence gate**: If `confidence < confidence_threshold` (default 0.6),
  the base tier/variant is returned unchanged.
- **Tier**: `effective_tier = max(base_tier, recommended_tier)` — never goes
  below the configured base.
- **Variant**:
  - If `base_variant` is `None`: variant is always `None`.
  - If `recommended_variant` is `None`: treated as "no preference",
    `base_variant` is kept.
  - Otherwise: `max(base_variant, recommended_variant)` in variant ordering.

The assessor can never downgrade from registry defaults.

### Configuration

Two settings live under `[routing]` in `config.toml`:

| Setting | Default | Description |
|---------|---------|-------------|
| `assessor_model` | `claude-haiku-4-5` | Model used for complexity assessment |
| `confidence_threshold` | `0.6` | Minimum confidence to apply an upgrade |

Higher threshold (0.7–0.8) means fewer upgrades. Lower threshold (0.4–0.5)
means more aggressive upgrading.

### Skip Conditions

The LLM assessment is skipped when:

- **No client** (`client=None`): silent fallback, no log.
- **Empty/missing node body**: fallback to base, DEBUG log.
- **Explicit config override** (layers 1–3): config wins, DEBUG log.
- **Nightshift pre-assessed**: triage already provided an `AssessedComplexity`,
  so the Haiku call is skipped and `apply_assessment()` is used directly.

### Error Handling

Any failure (network error, timeout, rate limit, malformed JSON) results in
silent fallback to the base tier/variant. A WARNING is logged but dispatch is
never blocked or delayed.

## Mechanism 2: Escalation Ladder (spec 30)

The `EscalationLadder` in `core/escalation.py` provides mechanical
retry-at-tier and escalate-to-next-tier logic independent of the complexity
assessor.

### How It Works

Each node gets an `EscalationLadder` instance tracking:

- **Starting tier**: set by the complexity assessment (or base default).
- **Tier ceiling**: always ADVANCED.
- **Retries before escalation**: configurable (default: 1).
- **Current tier**: advances through SIMPLE → STANDARD → ADVANCED.

On each `record_failure()`:

1. Retry at the **same tier** up to `retries_before_escalation` times.
2. Then **escalate to the next tier** in the ordering.
3. Once the ceiling tier's retries are exhausted, the ladder is **exhausted**
   and the node is permanently blocked.

The variant is **never changed** by the ladder — it is frozen at construction
time and preserved across all tier escalations.

### Callers

- `prepare_launch()` reads `ladder.current_tier` to select the model.
- `_retry_on_review_block()` in the result handler calls `record_failure()`
  on the coder's ladder when a reviewer blocks the coder, escalating the
  coder's tier for the retry.

## How the Two Mechanisms Interact

Both mechanisms operate on the same `EscalationLadder` instance stored in
`AssessmentManager.ladders[node_id]`. The key interaction: every call to
`assess_node()` — including on retries — **overwrites** the existing ladder
with a freshly constructed one.

### Example: Coder Retry Flow

```
First attempt:
  assess_node(node_body="fix config parsing")
    → Haiku recommends STANDARD/standard (confidence 0.7)
    → apply_assessment: max(STANDARD, STANDARD) = STANDARD
    → new ladder starts at STANDARD/standard
  → session runs with claude-sonnet-4-6
  → fails with "TypeError in nested config merge"

Retry (with failure context):
  assess_node(node_body="fix config parsing",
              previous_failure="TypeError in nested config merge")
    → Haiku sees failure, recommends ADVANCED/standard (confidence 0.85)
    → apply_assessment: max(STANDARD, ADVANCED) = ADVANCED
    → new ladder starts at ADVANCED/standard (old ladder discarded)
  → session runs with claude-opus-4-6

If that also fails:
  assess_node() runs again with the new failure context
    → another new ladder at whatever Haiku recommends
    → mechanical ladder retries/escalation governs further attempts
    → eventually ladder exhausts and node is permanently blocked
```

The complexity assessment provides a **shortcut**: instead of waiting for the
mechanical ladder to exhaust retries at STANDARD and then escalate to ADVANCED,
the assessor can jump directly to ADVANCED on the first retry if the failure
warrants it. The variant can also be upgraded (e.g., `standard` → `extended`)
— something the mechanical ladder alone cannot do.

### Variant Upgrades

Only the complexity assessor can trigger variant upgrades. The escalation
ladder preserves the variant unchanged across tier escalations. This means
upgrading from `standard` to `extended` (Opus → Opus 1M context) requires
the assessor to recommend it with sufficient confidence.
