---
spec_id: '14'
spec_name: model_variant_support
title: Model Variant Support
status: draft
created_at: '2026-06-29T07:37:37.286752+00:00'
updated_at: '2026-06-29T07:45:16.226831+00:00'
owner: mickume
source: https://github.com/agent-fox-dev/agent-fox/issues/652
schema_version: 1
---
# Model Variant Support

## Intent

Enable archetypes and modes to select the most cost- and latency-efficient model within a quality tier, matching context window to actual task needs without sacrificing output quality.

## Problem

The current model selection system maps three tiers (SIMPLE, STANDARD, ADVANCED) to exactly one model each. Within a tier, there are model **variants** that differ in context window, speed, or cost — for example, `claude-opus-4-6` (200k context) vs `claude-opus-4-6[1m]` (1M context). Today, all ADVANCED work uses the same model regardless of whether the agent needs 50k or 500k tokens of context. A reviewer reading a single diff doesn't need a 1M context window (and pays a latency/cost premium for it), while a coder working across many files benefits from it.

## Background

Model variant support is designed as a foundational prerequisite for the **dynamic complexity assessment** spec (spec 15), which will introduce runtime upgrade-only semantics using `VARIANT_ORDER` and variant-aware `EscalationLadder` logic. The two specs can be developed in parallel, but spec 15 cannot be completed until spec 14 is merged and stable. This spec intentionally focuses on the structural plumbing (data model, resolution logic, registry) without introducing any dynamic or automatic variant selection — that runtime intelligence is deferred to spec 15.

## Goal

Add a `variant` dimension to model selection so that archetypes and modes can specify not just which tier to use, but which variant within that tier. Variants are abstract labels (e.g., `"standard"`, `"extended"`, `"fast"`) that control operational characteristics (context size, speed) within a quality band, without changing the tier abstraction.

### Success Criteria

- **No regression**: All callers of `resolve_model()` that do not pass a `variant` parameter return results identical to the current behavior. The existing test suite passes without modification.
- **Full unit test coverage**: All 12 requirements have explicit unit test coverage. Tests live in `packages/agentfox/tests/unit/` alongside existing tests. Example anchoring cases: (1) `resolve_model('ADVANCED', variant='extended')` returns `claude-opus-4-6[1m]`; (2) `resolve_model('SIMPLE', variant='extended')` falls back to `claude-haiku-4-5` and emits a `DEBUG` log; (3) `EscalationLadder` preserves `variant='extended'` across a STANDARD→ADVANCED escalation. Requirement 12 (`NodeSessionRunner` wiring) must be covered by a unit test using mocked `resolve_model_variant()` and `resolve_model()` calls.
- **TIER_DEFAULTS invariant test**: A dedicated unit test asserts that `TIER_DEFAULTS['ADVANCED']` equals the `model_id` of the `MODEL_REGISTRY` entry with `tier=ADVANCED` and `variant='standard'`.
- **Structural only**: This spec does not target direct cost or latency improvements — those are downstream outcomes of spec 15. Success here is correctness and completeness of the plumbing layer.

## Non-Goals

The following are explicitly out of scope for this feature:

1. **No automatic/dynamic variant selection** — variants are not selected at runtime based on estimated or measured token count. That capability belongs to spec 15 (dynamic complexity assessment).
2. **No user-facing UI or API surface** — there is no end-user interface or external API for selecting variants. Variant configuration is developer-only, via archetype/mode definitions and `config.toml`.
3. **No multi-provider support** — variant modeling applies only to Anthropic models. Non-Anthropic providers are out of scope.
4. **No migration or deprecation** — existing configurations without `model_variant` continue to work unchanged and silently. No migration scripts, no deprecation warnings, no breaking changes.
5. **No observability beyond fallback logging** — session telemetry and cost tracking for variant resolution are out of scope for this iteration. A `DEBUG` log on variant fallback is sufficient; the log message format is left to implementer discretion.

## Tech Stack

- Python 3.12+
- Pydantic v2
- dataclasses

## Detailed Requirements

### 1. ModelEntry variant field

Add an optional `variant: str | None` field to the `ModelEntry` dataclass. `None` indicates the model has no variant dimension (e.g., Haiku, Sonnet have only one model per tier). String values like `"standard"`, `"extended"`, `"fast"` denote specific variants within a tier that has multiple models. These three labels (`"fast"`, `"standard"`, `"extended"`) are the canonical variant values; arbitrary strings are also accepted by the open-string design but are not expected in normal use.

### 2. MODEL_REGISTRY variant registration

Register models with their variants in `MODEL_REGISTRY`:

| Model ID | Tier | Variant |
|----------|------|---------|
| `claude-haiku-4-5` | SIMPLE | `None` |
| `claude-sonnet-4-6` | STANDARD | `None` |
| `claude-opus-4-6` | ADVANCED | `"standard"` |
| `claude-opus-4-6[1m]` | ADVANCED | `"extended"` |

### 3. Variant ordering

Define a `VARIANT_ORDER` mapping in `models.py` (alongside `MODEL_REGISTRY` and `ModelEntry`) for upgrade comparisons:

```
fast (0) < standard (1) < extended (2)
```

`None` is not in the ordering — models with `variant=None` do not participate in variant upgrades. This ordering is used by the upgrade-only semantics in the dynamic complexity assessment spec (spec 15). `VARIANT_ORDER` must be importable from `models.py` so that spec 15's logic can reference it directly.

### 4. Archetype and mode variant fields

- Add `default_model_variant: str | None = None` to `ArchetypeEntry`.
- Add `model_variant: str | None = None` to `ModeConfig`.

When both `ModeConfig.model_variant` and `ArchetypeEntry.default_model_variant` are set (neither is `None`), the **mode-level value takes precedence**. The archetype-level value is only used when the mode does not specify a variant. This priority is resolved by `resolve_effective_config()` (not by `resolve_model_variant()` directly) — mirroring how `model_tier` priority is already handled.

`None` means "inherit from tier default" (same as today for single-variant tiers). For multi-variant tiers, `None` resolves to `"standard"` (see Requirement 7 for the reconciled rule).

### 5. PerArchetypeConfig variant field

Add `model_variant: str | None = None` to `PerArchetypeConfig` for config.toml overrides.

**TOML key mapping:**

- `model_variant` under `[archetypes.overrides.<name>]` maps to `PerArchetypeConfig.model_variant` at the archetype level.
- `model_variant` under `[archetypes.overrides.<name>.modes.<mode>]` maps to the nested per-mode `PerArchetypeConfig.model_variant` (i.e., `PerArchetypeConfig` is self-referential for modes — the same struct is reused for both archetype-level and mode-level overrides).

```toml
[archetypes.overrides.coder]
model_tier = "ADVANCED"
model_variant = "extended"

[archetypes.overrides.reviewer.modes.fix-review]
model_tier = "ADVANCED"
model_variant = "standard"
```

The `model_variant` key is **not** valid at any other config.toml location outside the `archetypes.overrides` block. If it appears elsewhere, it is silently ignored — the codebase-wide `ConfigDict(extra='ignore')` Pydantic setting drops unknown keys automatically. No additional validation or warning is emitted.

### 6. resolve_model_variant()

Add `resolve_model_variant()` to `sdk_params.py` following the same 4-layer priority as `resolve_model_tier()`:

1. **Mode-level config override** (highest priority) — reads `model_variant` from the mode's `PerArchetypeConfig` entry.
2. **Per-archetype config override** — reads `model_variant` from the archetype's `PerArchetypeConfig` entry.
3. **Legacy dict override** — mirrors the existing `config.archetypes.models` dict check in `resolve_model_tier()` (the `archetypes.models.<name> = 'ADVANCED'` pattern). Since the legacy dict has no variant field, this layer **always returns `None`** for variant. Critically, this layer **causes an early return** — matching the short-circuit behavior of `resolve_model_tier()` exactly. Legacy callers that are detected at Layer 3 never reach Layer 4; they always receive `None` as the resolved variant.
4. **Archetype registry default** (lowest priority) — calls `resolve_effective_config()` and reads `default_model_variant` from the merged result. The mode-beats-archetype priority for registry-level defaults is already resolved inside `resolve_effective_config()`, so `resolve_model_variant()` does not need to re-implement that logic.

When all layers return `None` (no variant specified anywhere), `resolve_model_variant()` returns `None`, and `resolve_model()` falls back to `TIER_DEFAULTS` per Requirement 7.

### 7. resolve_model() variant awareness

Extend `resolve_model()` in `models.py` with an optional `variant: str | None = None` parameter. Resolution rules:

- **When `variant=None`**: use the tier's entry in `TIER_DEFAULTS`. This is the backward-compatible path — all existing callers receive identical behavior. **Invariant**: `TIER_DEFAULTS['ADVANCED']` always points to `claude-opus-4-6` (the `"standard"` variant), so the `None`→`"standard"` rule from Requirement 4 and the `None`→`TIER_DEFAULTS` rule from this requirement always produce the same result. This invariant must be maintained: any future update to `TIER_DEFAULTS['ADVANCED']` must keep it pointing to the `"standard"` variant model. The invariant is enforced by a dedicated unit test (see Success Criteria).
- **When `variant` is provided and the tier has a matching model**: scan `MODEL_REGISTRY` for the entry matching `(tier, variant)` and return it.
- **When `variant` is provided but no match exists** (including completely unrecognized strings such as `"turbo"` or typos — any string not matched in `MODEL_REGISTRY` for that tier): apply fallback per Requirement 9. No distinction is made between a valid-but-unavailable variant and an entirely unknown string — both trigger the same fallback path.

### 8. EscalationLadder variant preservation

Add optional `starting_variant: str | None = None` to `EscalationLadder.__init__()` as a **keyword-only** parameter (i.e., defined after a bare `*` in the signature, or placed after all existing positional parameters with a default). This ensures no existing call sites break, regardless of how they construct `EscalationLadder` today.

Expose the variant as a **read-only** `current_variant` property. The property is set once at construction time via `starting_variant` and is **never mutated** — neither by `EscalationLadder` internally nor by external callers (no setter is provided). The ladder is frozen with respect to variant after construction. External code that needs a different variant must construct a new `EscalationLadder` instance.

The variant is preserved across tier escalations:

- STANDARD/extended → escalate → ADVANCED/extended
- STANDARD/standard → escalate → ADVANCED/standard

`EscalationLadder` never modifies the variant — it only moves up the tier axis. When the escalated-to tier does not carry the preserved variant, `EscalationLadder` passes the variant through unchanged to `resolve_model()`, which handles the fallback per Requirement 9. `EscalationLadder` does **not** perform its own fallback logic.

**Top-of-ladder behavior**: When `EscalationLadder` reaches ADVANCED (the top tier) and the preserved variant is unavailable in that tier, the ladder passes the variant through to `resolve_model()` unchanged; `resolve_model()` applies the fallback (TIER_DEFAULTS + DEBUG log) as per Requirement 9. The fallback log is emitted by `resolve_model()`, not by `EscalationLadder`.

### 9. Variant fallback

If a requested variant string does not match any `MODEL_REGISTRY` entry for the resolved tier — whether the string is a valid label unavailable in that tier (e.g., requesting `"extended"` for SIMPLE) or a completely unrecognized string (e.g., `"turbo"`) — fall back to the tier's default model (via `TIER_DEFAULTS`) and emit a `DEBUG` log. The log message format is left to implementer discretion; only the `DEBUG` level is mandated. Never raise an error for a missing variant.

### 10. Pricing for variant models

Add `claude-opus-4-6[1m]` to `_default_pricing_models()` in `config.py` as a separate entry with its own pricing rates, distinct from `claude-opus-4-6`. Use the same rate structure as the existing `claude-opus-4-6` entry, with the following fields:

```python
{
    "model_id": "claude-opus-4-6[1m]",
    "input_price_per_m": <value>,
    "output_price_per_m": <value>,
    "cache_read_price_per_m": <value>,
    "cache_creation_price_per_m": <value>,
    # Rates retrieved from https://www.anthropic.com/pricing on <YYYY-MM-DD>
}
```

Actual numeric values must be sourced from https://www.anthropic.com/pricing at implementation time, as Anthropic pricing is subject to change. The implementer must record the date on which rates were retrieved as a code comment adjacent to the pricing entry, as shown in the structure above.

### 11. Backward compatibility

Existing configs without `model_variant` must work unchanged. `None` / absent variant defaults to the tier's default model. No migration, no deprecation warnings.

### 12. NodeSessionRunner wiring (session_lifecycle.py)

`NodeSessionRunner` in `session_lifecycle.py` must wire the new resolution calls in two explicit steps:

1. Call `resolve_model_variant()` to determine the effective variant string (or `None`).
2. Pass the result as the `variant=` keyword argument to `resolve_model()`.

These are two separate, sequential calls. This keeps each function's single responsibility clear and avoids embedding variant-resolution logic inside `resolve_model()`.

This wiring must be covered by a dedicated unit test that mocks both `resolve_model_variant()` and `resolve_model()` to verify they are called in the correct order with the correct arguments.

### 13. resolve_effective_config() update

`resolve_effective_config()` must be updated to handle the new variant fields introduced in Requirements 4 and 6. Specifically, it must add a mapping that propagates `ModeConfig.model_variant` onto the merged `ArchetypeEntry.default_model_variant`, mirroring the existing logic that maps `ModeConfig.model_tier` onto `ArchetypeEntry.default_model_tier`. When both the mode and archetype define a variant, the mode-level value wins — consistent with how `model_tier` priority is already handled.

This change is required for `resolve_model_variant()` Layer 4 to return the correct mode-beats-archetype merged value when calling `resolve_effective_config()`.

## Key Files

- `packages/agentfox/agentfox/core/models.py` — `ModelEntry`, `MODEL_REGISTRY`, `TIER_DEFAULTS`, `VARIANT_ORDER`, `resolve_model()`
- `packages/agentfox/agentfox/archetypes.py` — `ArchetypeEntry`, `ModeConfig`
- `packages/agentfox/agentfox/engine/sdk_params.py` — `resolve_model_tier()`, new `resolve_model_variant()`
- `packages/agentfox/agentfox/core/escalation.py` — `EscalationLadder`
- `packages/agentfox/agentfox/core/config.py` — `PerArchetypeConfig`, `_default_pricing_models()`
- `packages/agentfox/agentfox/engine/session_lifecycle.py` — `NodeSessionRunner` model resolution (two-step: `resolve_model_variant()` → `resolve_model(variant=...)`)
- `packages/agentfox/agentfox/resolve_effective_config.py` (or wherever `resolve_effective_config()` lives) — updated to merge `model_variant` from `ModeConfig` onto `ArchetypeEntry.default_model_variant`
- `packages/agentfox/tests/unit/` — unit tests for all 12 requirements

## Design Decisions

1. **Variant as open string, not enum**: Variants are plain strings so new variants can be added without code changes — only `VARIANT_ORDER` and `MODEL_REGISTRY` need updating. This future-proofs for potential "fast" variants or provider-specific operational profiles. The canonical values are `"fast"`, `"standard"`, and `"extended"`; arbitrary strings are accepted but not expected.
2. **None for single-variant models**: Models that have no variant dimension use `variant=None`, making it explicit that variant upgrades don't apply to their tier. This avoids polluting single-model tiers with a meaningless "standard" label.
3. **Keep existing TIER_DEFAULTS — with an enforced invariant**: The backward-compatible `TIER_DEFAULTS` dict remains unchanged. `TIER_DEFAULTS['ADVANCED']` is always the `"standard"` variant model, making `variant=None` and `variant="standard"` produce identical results for ADVANCED. This invariant is enforced by a dedicated unit test that asserts `TIER_DEFAULTS['ADVANCED']` equals the `model_id` of the `MODEL_REGISTRY` entry with `tier=ADVANCED` and `variant='standard'`.
4. **Add variant param to existing resolve_model()**: One function with an optional parameter is cleaner than a separate `resolve_model_with_variant()`. All existing callers continue working without changes.
5. **Separate pricing entry for 1M variant**: Different context windows may have different pricing (especially cache token rates), so each model ID gets its own pricing entry. Rates are sourced from Anthropic's public pricing page at implementation time; the retrieval date must be recorded as a code comment. The entry structure mirrors the existing `claude-opus-4-6` pricing entry.
6. **No observability for this iteration**: DEBUG-level fallback logging is the only observability requirement. The log message format is left to implementer discretion. Session-level variant telemetry is deferred until cost/latency improvements from spec 15 make tracking meaningful.
7. **EscalationLadder delegates fallback to resolve_model()**: `EscalationLadder` passes preserved variants through unchanged and never performs fallback itself. Fallback logging is emitted exclusively by `resolve_model()`. This keeps fallback logic in one place (Requirement 9).
8. **Mode-level variant beats archetype-level in registry**: When both `ModeConfig.model_variant` and `ArchetypeEntry.default_model_variant` are set, mode wins — resolved inside `resolve_effective_config()`, consistent with existing `model_tier` behavior. `resolve_effective_config()` must be updated as part of this spec (Requirement 13).
9. **Two-step wiring in NodeSessionRunner**: `resolve_model_variant()` and `resolve_model()` are called separately and sequentially, preserving single-responsibility and making each step independently testable.
10. **VARIANT_ORDER lives in models.py**: As a registry-level constant about model metadata, `VARIANT_ORDER` is colocated with `MODEL_REGISTRY` and `ModelEntry`, and is importable by spec 15 without introducing circular dependencies.
11. **resolve_model_variant() delegates mode-beats-archetype to resolve_effective_config()**: Rather than re-implementing priority logic, `resolve_model_variant()` calls `resolve_effective_config()` for Layer 4, exactly as `resolve_model_tier()` does. This avoids divergent priority implementations.
12. **Uniform fallback for all unmatched variant strings**: Any variant string not found in `MODEL_REGISTRY` for the resolved tier — whether a valid label for a different tier, an unknown string, or a typo — triggers the same fallback path (TIER_DEFAULTS + DEBUG log). No validation against `VARIANT_ORDER` is performed; fail-fast on unknown variants is explicitly rejected.
13. **EscalationLadder.current_variant is read-only**: The property has no setter. The ladder is frozen for variant after construction. External code that needs a different variant must construct a new `EscalationLadder` instance.
14. **PerArchetypeConfig is self-referential for mode overrides**: The same `PerArchetypeConfig` struct handles both archetype-level and mode-level config.toml entries. `model_variant` under `[archetypes.overrides.<name>.modes.<mode>]` populates the nested per-mode `PerArchetypeConfig`, mirroring the existing pattern for `model_tier`.
15. **Legacy dict Layer 3 always short-circuits**: `resolve_model_variant()` matches `resolve_model_tier()` exactly at Layer 3 — if the legacy dict is detected, an early return of `None` is issued and Layer 4 is never reached. This ensures legacy callers are never unexpectedly influenced by registry defaults.
16. **EscalationLadder.starting_variant is keyword-only**: The parameter is keyword-only to prevent positional argument conflicts with existing callers. No existing call sites require modification.
17. **Unknown model_variant keys outside archetypes.overrides are silently ignored**: Pydantic's codebase-wide `ConfigDict(extra='ignore')` handles this automatically. No additional validation logic is needed.

## Dependencies

| Spec | Direction | Notes |
|------|-----------|-------|
| Spec 15 — Dynamic Complexity Assessment | Downstream (depends on this spec) | Requires `VARIANT_ORDER` and variant-aware `EscalationLadder` defined here. Specs may be developed in parallel but spec 15 cannot ship before spec 14. |

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/652
