---
spec_id: '15'
spec_name: dynamic_complexity_assessment
title: Dynamic Complexity Assessment
status: draft
created_at: '2026-06-29T07:37:41.020082+00:00'
updated_at: '2026-06-29T08:13:06.081965+00:00'
owner: mickume
source: https://github.com/agent-fox-dev/agent-fox/issues/652
schema_version: 1
---
# Dynamic Complexity Assessment

## Intent

Replace static model-tier assignments with a lightweight, LLM-driven complexity signal that upgrades model selection only when task complexity warrants it, ensuring quality-critical tasks always run at full strength while avoiding unnecessary cost for simple work.

## Background

Static model assignment per archetype was a deliberate simplicity choice at initial design — it required no runtime inference and made dispatch behaviour fully deterministic. Over time, the one-size-fits-all approach became a source of unnecessary cost (e.g., a one-line config fix burning the same Opus budget as a complex multi-module refactor) and a quality risk (e.g., architecturally complex specs processed by audit-review on Sonnet rather than Opus).

A complexity assessment concept was explored previously: migration v3 introduced a `complexity_assessments` database table with `predicted_tier` and `confidence` columns. No production code ever inserted rows into it, and the table was dropped in migration v14. This spec revives the concept with an entirely in-memory, LLM-based approach — no database, no persistence — built on top of the variant-aware model resolution introduced in spec #14 (`model_variant_support`).

## Problem

The current model selection system assigns static model tiers per archetype: coders always get ADVANCED (Opus), reviewers always get STANDARD (Sonnet), regardless of task complexity. A one-line config fix burns the same Opus cost as a complex multi-module refactor. Meanwhile, a quality-critical pre-review or audit-review always runs on Sonnet, even when the spec is architecturally complex and would benefit from Opus-level reasoning.

## Goals

1. **Quality guarantee**: All quality-critical review modes (pre-review, audit-review) run at ADVANCED tier 100% of the time via registry defaults — this is enforced by the new default assignments, independently of the assessor.
2. **Resilience**: Assessment call failure (network error, timeout, malformed response) never blocks or delays dispatch. Silent fallback to base tier/variant is always guaranteed.
3. **Test coverage**: Unit tests cover all 12 requirements — `ComplexityAssessor`, `apply_assessment()`, `AssessmentManager` integration, nightshift passthrough, config resolution priority, and error handling paths.

> **Note**: Cost reduction (fewer ADVANCED-tier calls for simple tasks) is a primary motivation and expected outcome of the new default assignments, but is not a measurable acceptance criterion at this stage. This is infrastructure; cost metrics can be derived from structured logs once the feature is live.

## Non-Goals

The following are explicitly out of scope for this feature:

1. **No downgrade logic**: The assessor can only upgrade from the registry default floor. Downgrading below a configured base is never permitted.
2. **No persistent assessment storage**: Assessment results live only in memory on the escalation ladder instance. No database table, no file-based cache, no audit log of assessment decisions.
3. **No cost dashboards or reporting**: Cost tracking and reporting based on assessment outcomes are not part of this feature.
4. **No non-Anthropic model support**: The assessor is implemented against the Anthropic SDK only. Other LLM providers are out of scope.
5. **No automatic threshold tuning**: The `confidence_threshold` is a static configuration value. Automated tuning, feedback loops, or A/B testing of threshold values are out of scope.
6. **No per-archetype upgrade ceiling enforcement**: The "Assessor Can Upgrade To" column in the default assignments table (Requirement 8) is informational only. Upgrade bounds are naturally enforced by upgrade-only semantics and the ADVANCED tier ceiling on the EscalationLadder; no additional ceiling-enforcement logic is implemented.

## Tech Stack

- Python 3.12+
- Anthropic SDK (Claude API) — for the assessment LLM call
- Pydantic v2
- dataclasses

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 14_model_variant_support | last | 1 | Uses variant-aware resolve_model(), VARIANT_ORDER, and EscalationLadder variant support (starting_tier / starting_variant constructor parameters) |

> **VARIANT_ORDER reference (from spec #14)**: Variants are ordered `fast (0) < standard (1) < extended (2)`. This ordering is used by `apply_assessment()` to determine whether a variant upgrade is warranted. Models with `None` variant do not participate in variant upgrades — their variant field is never changed by the assessor.

## Detailed Requirements

### 1. ComplexityAssessor class

New `core/complexity.py` module containing:

**`ComplexityRecommendation` Protocol** (shared interface for `apply_assessment()`):

```python
from typing import Protocol

class ComplexityRecommendation(Protocol):
    recommended_tier: str
    recommended_variant: str | None
    confidence: float
```

Both `AssessmentResult` and `AssessedComplexity` are adapted to satisfy this Protocol via a wrapper function (see Requirement 11 for adapter details).

**`AssessmentResult` dataclass** (all fields required):

```python
@dataclass(frozen=True)
class AssessmentResult:
    recommended_tier: str        # Matches ModelTier values (e.g. "STANDARD", "ADVANCED")
    recommended_variant: str | None  # e.g. "standard", "extended", or None
    confidence: float            # 0.0–1.0
    rationale: str               # Human-readable explanation from the model
```

**`ComplexityAssessor` class**:

- Accepts an existing Anthropic client instance (passed via constructor, not created internally).
- Accepts configurable `assessor_model` (default: `"claude-haiku-4-5"`) and `confidence_threshold` (default: `0.6`).
- Exposes an `async assess()` method that takes: `node_body`, `archetype`, `mode`, `base_tier`, `base_variant`, and an optional `previous_failure` string.
- Returns an `AssessmentResult` dataclass instance.
- Is stateless between calls (no shared mutable state). The Anthropic client handles its own connection pooling. Concurrent calls from multiple simultaneous `assess_node()` invocations are safe without additional locking.

**Dependency injection**: `AssessmentManager` receives the Anthropic client via an optional `client` parameter in its `__init__()` and passes it to `ComplexityAssessor` at instantiation. The client is created in `_setup_infrastructure()` in `engine/run.py` using `create_async_anthropic_client()` from `core/client.py`, returned in the infra dict, and passed through `Orchestrator.__init__()` to `AssessmentManager` (see Requirement 4).

**Absent client behaviour**: If `client=None` is passed to `AssessmentManager.__init__()`, `ComplexityAssessor` is **not** instantiated. `assess_node()` detects the absent assessor and falls back to base tier/variant silently — no error, no warning. This is the correct behaviour for environments (e.g. test environments, offline configurations) that do not require the assessor. There is no log entry for the no-client path; it is treated as permanently disabled rather than a per-call failure.

### 2. Assessment prompt and structured output

The assessor sends a prompt to the configured model. The prompt must be structured with the following sections (exact wording is left to the implementer):

1. **System role**: Establishes the LLM as a "complexity assessor for an AI coding pipeline".
2. **Agent role**: Specifies the archetype and mode (e.g., `coder/fix`) or `"default"` if no mode is set.
3. **Current base tier/variant**: The configured floor (e.g., `STANDARD / standard`).
4. **Task body**: The full `Node.body` text representing the work to be assessed. Node bodies are typically 500–2,000 tokens, well within Haiku's context window. The full body is always included without truncation. If the body causes a context-limit error, it is treated as an assessment failure and falls back silently per Requirement 12 (no special truncation path).
5. **Failure context** *(retry only)*: If `previous_failure` is provided, it is appended as additional context describing why the previous attempt failed, so the assessor can factor in the failure mode.
6. **Output instruction**: Instructs the model to respond with JSON only, matching the `AssessmentResult` schema:

```json
{
  "recommended_tier": "ADVANCED",
  "recommended_variant": "extended",
  "confidence": 0.82,
  "rationale": "8 subtasks spanning 4 modules with error-handling edge cases"
}
```

The JSON response is parsed directly into an `AssessmentResult` dataclass. Validation rules (applied before constructing the dataclass):

- All four fields (`recommended_tier`, `recommended_variant`, `confidence`, `rationale`) must be present.
- `confidence` must be a float in the range `[0.0, 1.0]`.
- `recommended_tier` must be a recognised `ModelTier` value (e.g., `"SIMPLE"`, `"STANDARD"`, `"ADVANCED"`). String comparison is **case-sensitive** — values must match `ModelTier` enum members exactly (e.g., `"ADVANCED"` is valid; `"advanced"` is not).
- `recommended_variant` must be `null`, `"fast"`, `"standard"`, or `"extended"`. String comparison is **case-sensitive** — values must match exactly (e.g., `"standard"` is valid; `"Standard"` is not).

If **any** validation check fails — including malformed JSON, missing required fields, out-of-range `confidence`, or unrecognised tier/variant values — the entire response is treated as a parse failure per Requirement 12. No partial field salvaging is attempted.

**Timeout**: The assessment API call is subject to a 30-second timeout enforced via the Anthropic SDK's `timeout` parameter on the individual API call. A timeout is treated as any other failure: silent fallback with a `WARNING` log entry (see Requirement 12).

**Rate limiting**: No concurrency cap is applied. Each `assess_node()` call proceeds independently. Rate limit errors from the Anthropic API are treated as assessment failures per Requirement 12: silent fallback with a `WARNING` log entry. The Anthropic SDK handles its own internal retries.

### 3. Upgrade-only semantics

An `apply_assessment()` function enforces upgrade-only. It accepts an `AssessmentResult` (the `ComplexityRecommendation`-compatible type returned by `ComplexityAssessor.assess()` and by the `assessed_complexity_to_recommendation()` adapter for `AssessedComplexity`):

```python
def apply_assessment(
    recommendation: ComplexityRecommendation,
    base_tier: str,
    base_variant: str | None,
    confidence_threshold: float,
) -> tuple[str, str | None]:
```

Rules:

- **Confidence gate**: if `recommendation.confidence < confidence_threshold`, return `(base_tier, base_variant)` unchanged. No upgrade is applied.
- **Tier**: `max(base_tier, recommendation.recommended_tier)` using `ModelTier` ordering — never goes below the configured base.
- **Variant**: Only upgrade along `VARIANT_ORDER` (`fast < standard < extended`). Rules:
  - If `base_variant` is `None` (single-variant tier), the variant is never changed — `None` is always returned.
  - If `recommendation.recommended_variant` is `None`, treat it as "no preference" — `base_variant` is returned unchanged. A `None` recommendation never triggers a variant upgrade or downgrade.
  - Otherwise (both `base_variant` and `recommendation.recommended_variant` are non-`None`), take `max(base_variant, recommendation.recommended_variant)` in `VARIANT_ORDER`.

> The "Assessor Can Upgrade To" column in the default assignments table (Requirement 8) documents expected maximum upgrade targets per archetype/mode. It is **not** enforced at runtime — upgrades are bounded naturally by the upgrade-only logic above and the ADVANCED tier ceiling on the EscalationLadder.

### 4. AssessmentManager integration

`AssessmentManager` is defined in `engine/engine.py`. Its `__init__()` gains an optional `client` parameter alongside the existing `config` parameter. When `client` is non-`None`, `AssessmentManager` instantiates `ComplexityAssessor` at init time, passing the client along with `assessor_model` and `confidence_threshold` from `RoutingConfig`. When `client` is `None`, `ComplexityAssessor` is not instantiated and `assess_node()` falls back to base tier/variant silently for every call (see Requirement 1 — Absent client behaviour).

**Client injection chain**: The Anthropic client is created in `_setup_infrastructure()` in `packages/agentfox/agentfox/engine/run.py` using `create_async_anthropic_client()` from `core/client.py` and returned in the infra dict. `run_code()` passes the client from the infra dict to `Orchestrator.__init__()` via a `client` kwarg. `Orchestrator.__init__()` passes the client to `AssessmentManager(client=client)`. Changes required: `_setup_infrastructure()` (create client), `run_code()` (pass client in orch_kwargs), and `Orchestrator.__init__()` (accept and forward client).

Modify `AssessmentManager.assess_node()` in `engine/engine.py` with the following updated signature:

```python
async def assess_node(
    self,
    node_id: str,
    archetype: str,
    mode: str | None,
    node_body: str | None,
    previous_failure: str | None = None,
    pre_assessed: AssessedComplexity | None = None,
) -> EscalationLadder:
```

Behaviour (evaluated in order):

1. **No assessor (client=None)**: If `ComplexityAssessor` was not instantiated (client absent), skip all assessment steps and fall back to base tier/variant immediately. No log entry is emitted. Construct the `EscalationLadder` from the base tier/variant.
2. **Missing/empty body**: If `node_body` is `None` or an empty string, skip assessment entirely and fall back to base tier/variant. Log a `DEBUG` message (e.g., `"Skipping complexity assessment: node_body is absent or empty for node {node_id}"`). Proceed to construct the `EscalationLadder` from the base tier/variant.
3. **Explicit config override**: If an explicit config override exists for this archetype/mode (layers 1–3, detected via `is_explicitly_configured()` — see Requirement 6), skip the LLM assessment and use the configured tier/variant directly as the escalation ladder's starting point.
4. **Nightshift pre-assessed**: If `pre_assessed` is a valid `AssessedComplexity`, convert it to an `AssessmentResult` via `assessed_complexity_to_recommendation()` (see Requirement 11) and apply it directly via `apply_assessment()` without calling the Haiku assessor.
5. **LLM assessment**: Otherwise, call `ComplexityAssessor.assess()` with `node_body`, `archetype`, `mode`, `base_tier`, `base_variant`, and `previous_failure`.
6. **Apply upgrade-only**: Apply upgrade-only semantics via `apply_assessment()` to obtain the effective tier/variant.
7. **Construct ladder**: Construct and return a new `EscalationLadder` with `starting_tier=effective_tier` and `starting_variant=effective_variant`. The ceiling remains ADVANCED. The same per-tier retry configuration from `RoutingConfig` is used as for any other ladder — only `starting_tier` and `starting_variant` differ.

### 5. Re-assessment on retry

When a node fails and is re-dispatched, `assess_node()` must re-assess with the failure reason as additional context (passed via `previous_failure`). This replaces the current idempotent behavior (early return if ladder exists) for nodes with prior failures.

**Source of `previous_failure`**: The `previous_failure` string is the last error string from the failed attempt, stored on the `error_tracker` dict in the dispatch layer. `DispatchManager.prepare_launch()` already has access to `error_tracker` and extracts the `previous_error` string from it — this is the same value currently used for error feedback in retries.

The existing `EscalationLadder` is **discarded** and a new one is created from the new effective tier/variant. Retry state (attempt count at the old tier) is not preserved — the re-assessment effectively resets the ladder at the new starting point. The new ladder uses the same ceiling (ADVANCED) and the same per-tier retry configuration from `RoutingConfig` as the original; only `starting_tier` and `starting_variant` differ. The assessor may recommend a higher tier/variant based on the failure, allowing proactive escalation rather than waiting for the ladder to exhaust retries.

### 6. Skip when explicit config override exists

If the model tier for a given archetype/mode is set via config resolution layers 1–3 (mode-level override, per-archetype override, or legacy dict override), skip the LLM assessment entirely. Only assess when using the archetype registry default (layer 4/5).

**Detection mechanism**: `assess_node()` calls a new `is_explicitly_configured(archetype, mode)` helper function. This helper mirrors the existing `resolve_model_tier()` logic, walking layers 1–3 in order and returning `True` if any layer returns a non-`None` value for the given archetype/mode. If all three layers return `None`, the function returns `False` and assessment proceeds.

**Behaviour when `mode` is `None`**: Layer 1 (mode-level override check) is skipped entirely when `mode` is `None`. The helper proceeds directly to layers 2–3. This mirrors how `resolve_model_tier()` handles a `None` mode. A `None` mode never causes layer 1 to return `True`.

### 7. Resolution priority

The updated resolution priority with the assessment layer inserted:

```
1. Mode-level config override     (explicit — wins always)
2. Per-archetype config override  (explicit — wins always)
3. Legacy dict override           (explicit — wins always)
4. LLM assessment upgrade         (dynamic — upgrade-only from base)
5. Archetype registry default     (static — the floor)
```

### 8. Revised default model assignments

Update `ARCHETYPE_REGISTRY` in `archetypes.py` with new defaults. These defaults become the floor that the assessor upgrades from. The "Assessor Can Upgrade To" column documents expected maximum targets and is **informational only** — no runtime ceiling is enforced beyond the ADVANCED tier cap.

| Agent / Mode | Old Default | New Default (tier / variant) | Assessor Can Upgrade To |
|---|---|---|---|
| Coder | ADVANCED / None | STANDARD / standard | ADVANCED / extended |
| Coder (fix) | ADVANCED / None | STANDARD / standard | ADVANCED / standard |
| Reviewer (pre-review) | STANDARD / None | ADVANCED / standard | ADVANCED / extended |
| Reviewer (drift-review) | STANDARD / None | STANDARD / standard | ADVANCED / standard |
| Reviewer (audit-review) | STANDARD / None | ADVANCED / standard | ADVANCED / extended |
| Reviewer (fix-review) | ADVANCED / None | ADVANCED / standard | ADVANCED / extended |
| Verifier | STANDARD / None | STANDARD / standard | ADVANCED / standard |
| Maintainer (hunt) | STANDARD / None | SIMPLE / standard | STANDARD / standard |
| Maintainer (fix-triage) | STANDARD / None | STANDARD / standard | ADVANCED / standard |
| Maintainer (extraction) | STANDARD / None | SIMPLE / standard | STANDARD / standard |

Applied immediately, no migration path.

### 9. Dispatch integration

`DispatchManager.prepare_launch()` in `engine/dispatch.py` must:

- Extract `node_body` from the task graph node via `self.get_node(node_id).body`.
- Extract `previous_failure` from the `error_tracker` dict (the same `previous_error` string already used for error feedback in retries). Pass `None` if no prior failure exists for the node.
- Pass `node_body` and `previous_failure` to `self._routing.assess_node()`.

### 10. Configuration

Add to `RoutingConfig` in `core/config.py` (exposed as `[routing]` in config.toml):

- `assessor_model: str = "claude-haiku-4-5"` — model ID for the complexity assessor. Validated at config load time as a non-empty string; a typo will fail at runtime on the first API call. No allowlist or prefix check is applied — model IDs change frequently and an allowlist would require ongoing maintenance.
- `confidence_threshold: float = 0.6` — minimum confidence to apply an assessment upgrade. Values below this cause the assessment to be ignored.

**Confidence threshold guidance**: The default of `0.6` is a conservative baseline chosen to prefer dispatch stability over cost savings — it favours passing assessment upgrades that the assessor is fairly confident about. Operators can adjust this value:
  - **Higher values (0.7–0.8)**: More selective. Fewer upgrades are applied; only high-confidence recommendations are used. Prefer these values to minimise unexpected tier promotions.
  - **Lower values (0.4–0.5)**: More aggressive. More assessments result in upgrades, including lower-confidence ones. Use with caution — increases cost exposure.

**Validation**: Both fields are validated eagerly at config load time using Pydantic v2. `confidence_threshold` uses the existing `Clamped` annotation pattern (or equivalent `Field(ge=0.0, le=1.0)`) to reject out-of-range values with a `ValidationError` at startup. `assessor_model` must be a non-empty string; an empty string raises a `ValidationError` at startup. No validation is deferred to first use.

### 11. Nightshift triage integration

Extend the nightshift triage output to include complexity assessment:

```python
@dataclass(frozen=True)
class AssessedComplexity:
    tier: str            # valid ModelTier string
    variant: str | None  # None or valid variant string
    confidence: float
    rationale: str
```

**Protocol adapter**: `AssessedComplexity` uses `tier`/`variant` field names instead of `recommended_tier`/`recommended_variant`. To pass an `AssessedComplexity` to `apply_assessment()`, a dedicated adapter function converts it to an `AssessmentResult`:

```python
def assessed_complexity_to_recommendation(ac: AssessedComplexity) -> AssessmentResult:
    return AssessmentResult(
        recommended_tier=ac.tier,
        recommended_variant=ac.variant,
        confidence=ac.confidence,
        rationale=ac.rationale,
    )
```

This function is defined in `core/complexity.py`. Call sites in `assess_node()` invoke it before passing to `apply_assessment()`. Both dataclasses remain clean with no property aliases or `__getattr__` overrides.

Add `assessed_complexity: AssessedComplexity | None = None` to `TriageResult` in `nightshift/fix_pipeline.py`.

**Triage JSON output shape**: The triage model's structured JSON response must include an `assessed_complexity` nested object with fields that exactly mirror the `AssessedComplexity` dataclass:

```json
{
  "assessed_complexity": {
    "tier": "ADVANCED",
    "variant": "standard",
    "confidence": 0.8,
    "rationale": "Complex multi-module refactor with cross-cutting concerns"
  }
}
```

Field names, types, and valid values for `assessed_complexity` match those of `AssessedComplexity` exactly: `tier` is a valid `ModelTier` string (case-sensitive), `variant` is `null` or a valid variant string (case-sensitive), `confidence` is a float in `[0.0, 1.0]`, and `rationale` is a non-empty string. The same validation rules as Requirement 2 apply to these fields.

**Triage prompt update**: The triage task prompt in `fix_pipeline.py._run_triage()` is updated to request a complexity assessment as part of its structured JSON output. The triage response parser in `session/review_parser.py.parse_triage_output()` is updated to extract and validate the `assessed_complexity` sub-object. Exact prompt wording is left to the implementer. The JSON output schema defined above (field names, types, and valid values) is the normative specification for the model's output format.

> **Note**: `nightshift/triage.py` handles *batch* triage (ordering multiple issues) and has a different `TriageResult` class. Per-issue triage uses `fix_pipeline.py` for the prompt and `session/review_parser.py` for parsing.

**Parsing failures (partial failure semantics)**: If the triage response is valid outer JSON but the `assessed_complexity` field is missing, malformed, or contains out-of-range/unrecognised values, the parsing is treated as a **partial failure**: only `assessed_complexity` is set to `None` in the `TriageResult`, and a `WARNING` is logged. The rest of `TriageResult` is parsed and used normally — a malformed `assessed_complexity` sub-object does not cause a full triage parse failure. The same field-level validation rules as Requirement 2 apply (case-sensitive string matching, range checks). No partial field salvaging within the `assessed_complexity` object is attempted.

**Nightshift orchestrator integration** (`coder_reviewer.py`): The nightshift fix pipeline dispatches a single coder node per issue. `TriageResult.assessed_complexity` is extracted from the `TriageResult` and passed as `pre_assessed` to `assess_node()` for that coder node. Subsequent reviewer nodes (fix-review) follow their own assessment path and do not receive the triage `assessed_complexity`. If `pre_assessed` is a valid `AssessedComplexity`, `assess_node()` converts it via `assessed_complexity_to_recommendation()` and applies it via `apply_assessment()`, skipping the Haiku call entirely. If `pre_assessed` is `None` (parse failure or not provided), `assess_node()` falls back to the standard Haiku assessment call.

> The nightshift triage model runs at STANDARD tier (as configured in the nightshift pipeline). This is an existing property of the triage step and is not changed by this spec.

**Interface summary**:

```
TriageResult.assessed_complexity (AssessedComplexity | None)
    → nightshift orchestrator (coder_reviewer.py) — extracted once, passed for coder node only
    → assess_node(pre_assessed=assessed_complexity)
    → assessed_complexity_to_recommendation() + apply_assessment() [if non-None]
    OR ComplexityAssessor.assess() [if None]
```

### 12. Error handling

If the assessor API call fails (network error, rate limit, timeout, malformed JSON response, or any validation failure per Requirement 2), fall back silently to the base tier/variant:

- Log a `WARNING` with the exception details.
- Return the base tier/variant unchanged.
- Never block or delay the dispatch due to an assessment failure.

A **30-second timeout** applies to the assessment API call, enforced via the Anthropic SDK's `timeout` parameter on the individual API call (see Requirement 2). A timeout is treated as any other failure: silent fallback with a `WARNING` log entry.

Context-limit errors (e.g. caused by an exceptionally large `node_body`) are treated as assessment failures per this same policy: silent fallback with a `WARNING` log entry. No truncation or retry with a shortened body is attempted.

## Observability

Each successful assessment produces a structured `DEBUG` log entry using the following snake_case key names (consistent with the project's existing structured logging patterns):

| Key | Value |
|---|---|
| `node_id` | The node identifier |
| `archetype` | The archetype string |
| `mode` | The mode string, or `null` if not set |
| `effective_tier` | The resolved tier after upgrade-only logic |
| `effective_variant` | The resolved variant after upgrade-only logic, or `null` |
| `confidence` | The assessor's confidence float |
| `rationale` | The assessor's rationale string |

Skipped assessments are logged at `DEBUG` level in two cases:

- **Absent/empty `node_body`**: log message e.g. `"Skipping complexity assessment: node_body is absent or empty for node {node_id}"`.
- **Explicit config override (layers 1–3)**: log a `DEBUG` entry with `node_id`, `archetype`, `mode`, and the resolved tier, indicating the tier was set by explicit configuration and assessment was skipped (e.g., `"Skipping complexity assessment: explicit config override for node {node_id}, archetype {archetype}, mode {mode}, resolved tier {tier}"`).

The permanently-disabled path (`client=None`) produces no log entry — it is treated as a configuration state, not a per-call event.

Failed assessments are logged at `WARNING` level with the exception details, per the error handling policy in Requirement 12. Nightshift triage parsing failures that result in `assessed_complexity = None` are also logged at `WARNING` level.

## Testing

Unit tests are required for all 12 requirements, organised according to standard project conventions. The mapping of test areas to requirements is as follows:

- **Req 1 — `ComplexityAssessor`**: prompt construction (verify all required sections are present), structured output parsing, confidence gate behaviour, retry path with `previous_failure` context. Verify statelessness: concurrent calls do not interfere with each other. Verify absent-client path: when `client=None`, `assess_node()` falls back silently with no log entry.
- **Req 2 — Prompt and structured output**: validation failure cases — out-of-range `confidence`, unrecognised `recommended_tier` (including wrong-case values such as `"advanced"`), unrecognised `recommended_variant` (including wrong-case values), and missing required fields each trigger fallback (not raise) and log `WARNING`. Timeout enforcement (SDK `timeout` parameter applied). Rate limit error triggers silent fallback and `WARNING` log.
- **Req 3 — `apply_assessment()`**: upgrade-only tier logic, upgrade-only variant logic, no-change on `None` base variant, no-change when `recommended_variant` is `None` and `base_variant` is non-`None`, no-change below confidence threshold.
- **Req 4 — `AssessmentManager.assess_node()` integration**: escalation ladder `starting_tier`/`starting_variant` set correctly from effective tier/variant; client injection via optional `client` parameter; `client=None` produces silent fallback with no warning log.
- **Req 5 — Re-assessment on retry**: existing ladder discarded; new ladder created at new starting point after re-assessment with `previous_failure` sourced from `error_tracker`; new ladder uses same ceiling and per-tier retry config.
- **Req 6 — Skip on explicit override**: `is_explicitly_configured()` returns `True` when any of layers 1–3 has a non-`None` value; returns `False` when all three return `None`. Skip-on-explicit-override tested with layers 1–3 each independently. `mode=None` skips layer 1 correctly.
- **Req 7 — Resolution priority**: assessed tier/variant overrides registry default but not explicit config overrides (integration-level verification of priority ordering).
- **Req 8 — Default assignments**: `ARCHETYPE_REGISTRY` contains the updated defaults for all 10 archetype/mode combinations listed in the table.
- **Req 9 — Dispatch integration**: `DispatchManager.prepare_launch()` extracts `node_body` and extracts `previous_failure` from `error_tracker`, passing both to `assess_node()`.
- **Req 10 — Configuration**: `RoutingConfig` raises `ValidationError` at load time for out-of-range `confidence_threshold` and empty `assessor_model`.
- **Req 11 — Nightshift passthrough**: valid `pre_assessed` bypasses Haiku call and applies directly via `assessed_complexity_to_recommendation()` + `apply_assessment()`; `None` `pre_assessed` triggers Haiku fallback. Nightshift triage partial failure: malformed or missing `assessed_complexity` in triage response sets `TriageResult.assessed_complexity = None` and logs `WARNING`, while the rest of `TriageResult` is parsed normally. `assessed_complexity_to_recommendation()` correctly maps `tier`/`variant` to `recommended_tier`/`recommended_variant`. Per-node passthrough: only the coder node receives `pre_assessed`; fix-review nodes do not.
- **Req 12 — Error handling**: network error, timeout, rate limit error, and malformed JSON each produce `WARNING` log and return base tier/variant without raising. Context-limit error produces `WARNING` log and returns base tier/variant without raising. `node_body=None` and `node_body=""` each return base tier/variant and log `DEBUG`. Explicit config override skip logs `DEBUG` with tier info.

## Key Files

- **New**: `packages/agentfox/agentfox/core/complexity.py` — `ComplexityRecommendation` Protocol, `ComplexityAssessor`, `AssessmentResult`, `apply_assessment()`, `assessed_complexity_to_recommendation()`
- `packages/agentfox/agentfox/engine/engine.py` — `AssessmentManager.__init__()` (optional `client` parameter, conditional `ComplexityAssessor` instantiation), `AssessmentManager.assess_node()` (updated signature, absent-client fast-path, `is_explicitly_configured()` helper)
- `packages/agentfox/agentfox/engine/run.py` — `_setup_infrastructure()` creates Anthropic client; `run_code()` passes it to `Orchestrator`; `Orchestrator.__init__()` forwards to `AssessmentManager`
- `packages/agentfox/agentfox/engine/dispatch.py` — `DispatchManager.prepare_launch()` (extract `node_body` from task graph node; extract `previous_failure` from `error_tracker`; pass both to `assess_node()`)
- `packages/agentfox/agentfox/archetypes.py` — `ARCHETYPE_REGISTRY` default changes
- `packages/agentfox/agentfox/core/config.py` — `RoutingConfig` new fields with eager Pydantic validation
- `packages/agentfox/agentfox/nightshift/fix_pipeline.py` — `TriageResult` extension, `AssessedComplexity` dataclass
- `packages/agentfox/agentfox/nightshift/fix_pipeline.py` — `_run_triage()` prompt update to request `assessed_complexity` field
- `packages/agentfox/agentfox/session/review_parser.py` — `parse_triage_output()` parser changes for `assessed_complexity` extraction with partial failure semantics (already implemented)
- `packages/agentfox/agentfox/nightshift/coder_reviewer.py` — nightshift orchestrator: extracts `TriageResult.assessed_complexity` and passes it as `pre_assessed` to `assess_node()` for the coder node only

## Design Decisions

1. **Upgrade-only**: The assessor can never downgrade from registry defaults. This ensures the defaults are a guaranteed quality floor — the assessor only adds capability when complexity warrants it.
2. **In-memory only**: Assessment results live on the escalation ladder instance. No database table, no persistence. The `complexity_assessments` table created in migration v3 and dropped in migration v14 is not revived. Structured `DEBUG` logs of `(node_id, tier, variant, confidence, rationale)` provide traceability without schema changes.
3. **Re-assess on retry with failure context**: Failed nodes get re-assessed with the failure reason (the last error string from `error_tracker` in the dispatch layer), allowing the assessor to recommend escalation proactively rather than waiting for the escalation ladder to exhaust its retries at the current tier. The existing ladder is discarded and a new one started from the re-assessed tier/variant; prior retry state is not preserved. The new ladder uses the same ceiling (ADVANCED) and per-tier retry config from `RoutingConfig`.
4. **Configurable assessor model and threshold**: Both are under `[routing]` with sensible defaults (Haiku for cost, 0.6 for confidence). Users can swap to Sonnet for higher-quality assessments or adjust the threshold. Both are validated eagerly at config load via Pydantic v2. `assessor_model` is validated as a non-empty string only — no allowlist or prefix check, since model IDs change frequently. The `0.6` default is a conservative baseline; see Requirement 10 for tuning guidance. No automatic tuning is in scope.
5. **Pre-computed assessment for nightshift**: The triage model already analyzes the issue — adding a complexity assessment to its output avoids a redundant Haiku call and leverages the triage model's deeper context about the issue. The triage `assessed_complexity` is passed to `assess_node()` for the coder node only; subsequent reviewer nodes (fix-review) follow their own assessment path. If the triage model's complexity output is malformed or missing, `assess_node()` falls back transparently to the standard Haiku call. A malformed `assessed_complexity` sub-object does not invalidate the rest of the `TriageResult`.
6. **Immediate default changes**: The new defaults only make sense with the assessor active. No migration path — the changed defaults and the assessment logic ship together.
7. **Pass existing client**: The `ComplexityAssessor` receives an already-configured Anthropic client rather than creating its own, consistent with how the rest of the codebase handles API clients. `AssessmentManager` receives the client via an optional `client` parameter in its `__init__()`; the `session_runner_factory` closure in `engine/run.py` supplies the client when constructing `AssessmentManager`. When `client=None`, `ComplexityAssessor` is not instantiated and assessment is permanently disabled for that manager instance — no error, no warning.
8. **SDK-level timeout with silent fallback**: The assessment call is on the dispatch critical path. A 30-second timeout is enforced via the Anthropic SDK's `timeout` parameter on the individual API call (the most natural fit for Anthropic SDK usage). Silent fallback ensures the assessor never becomes a hard dependency on dispatch availability.
9. **`AssessmentResult` vs `AssessedComplexity` — wrapper adapter**: Two separate dataclasses are used. `AssessmentResult` (in `core/complexity.py`) is the internal return type of `ComplexityAssessor.assess()`, using `recommended_tier`/`recommended_variant` field names to reflect that it is a recommendation. `AssessedComplexity` (in `nightshift/fix_pipeline.py`) is the nightshift-specific type embedded in `TriageResult`, using `tier`/`variant` field names to reflect resolved values. A dedicated `assessed_complexity_to_recommendation()` adapter function in `core/complexity.py` converts `AssessedComplexity` to `AssessmentResult` before passing to `apply_assessment()`. This keeps both dataclasses clean, avoids property aliases or `__getattr__` overrides, and makes the conversion explicit at the call site.
10. **`is_explicitly_configured()` mirrors resolve_model_tier()**: Rather than threading a flag through the resolution pipeline, a dedicated helper walks layers 1–3 independently. When `mode` is `None`, layer 1 (mode-level check) is skipped entirely, mirroring `resolve_model_tier()` behaviour. This avoids coupling the config resolution return type to a "which layer was used" signal, and keeps assessment skip logic self-contained. Skipping due to explicit override is logged at `DEBUG` level for traceability.
11. **Fail-closed validation**: Any out-of-range or unrecognised value in the assessor's JSON response is treated as a total parse failure — no partial field salvaging. String comparisons for `recommended_tier` and `recommended_variant` are case-sensitive. This prevents silently using a partially-valid assessment that could produce unexpected tier/variant combinations. The same rules apply to the `assessed_complexity` sub-object in triage responses, with the exception that a sub-object failure is only a partial failure — the rest of `TriageResult` remains valid.
12. **Skip on absent node body**: When `node_body` is `None` or empty, the assessor has no signal to work with. Skipping and falling back to base tier/variant is the safest choice. A `DEBUG` log is emitted so the skip is visible in traces without polluting warning-level output.
13. **`ComplexityAssessor` is stateless**: No shared mutable state exists between calls. The Anthropic client handles its own connection pooling. Concurrent assessment calls from multiple simultaneous `assess_node()` invocations are safe without additional locking — no synchronisation primitives are required. No concurrency cap is applied; rate limit errors are handled via silent fallback.
14. **`None` recommended_variant means no preference**: When `apply_assessment()` receives a `None` `recommended_variant` alongside a non-`None` `base_variant`, it treats this as "no preference" and returns `base_variant` unchanged. This avoids ambiguity in the `max()` comparison since `None` is not in `VARIANT_ORDER`.
15. **Always include full node body**: No truncation strategy is applied to node bodies before including them in the assessment prompt. Node bodies are typically 500–2,000 tokens, well within Haiku's context window. Context-limit errors — while unexpected — are treated as standard assessment failures with silent fallback.

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/652