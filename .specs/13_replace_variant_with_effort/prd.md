---
spec_id: "13"
spec_name: "replace_variant_with_effort"
title: "Replace the --variant flag with --effort"
status: "active"
created_at: "2026-10-02T17:57:56.295676Z"
updated_at: "2026-10-02T17:57:56.295676Z"
intent_hash: "37a15ec61f38d7e404194153ac0a0d3f7e83cc686f691dfe5039ab146201a4b6"
schema_version: 2
source: "docs/prds/07-replace-the-variant-flag-with-effort.md"
---
## Intent

Remove the `--variant` flag and the variant dimension from the tier table, and add `--effort` (with `$AF_MODEL_EFFORT`) so that reasoning effort is an explicit, independent setting that applies to any model — whether resolved from a tier or named by catalog id — with clamping checked before the run starts.

## Goals

- Two independent settings choose a run's model: what model (`--model`, a tier or a catalog spec) and how hard it thinks (`--effort`).
- Any resolved model, whether it came from a tier or a model id, can run at any effort its catalog row supports.
- `impl`'s repair phase can run at its own effort, independent of the run's, via `--repair-model-effort`.
- The envelope reports the effort the model actually runs at; when clamping changes the requested level, an `effort_clamped` warning names both levels and the model.
- No concept remains that only one vendor's table backs (the `extended` variant existed only for Anthropic).

## Non-goals

- **Backward compatibility for `--variant`.** It is removed outright: no hidden alias, no deprecation period. A script that passes it gets the standard "flag provided but not defined" usage error.
- **A vendor-neutral way to ask for long context.** The `extended` variant was the only way to say "give me the long-context model" without naming one, and only Anthropic had one. To get long context, name the model by id.
- **Changing the tier defaults.** `SIMPLE`, `STANDARD` and `ADVANCED` keep their current models and efforts. The `openai` and `google` rows keep having no effort of their own.
- **Changing agentkit-go.** `catalog.ClampThinkingLevel` and the wire mapping are used as they are.
- **Per-phase effort beyond repair.** Every phase other than repair runs at the run's effort.

## Background

Every tool takes `--variant` from the shared flag set in `internal/toolio/cli.go` (`Common.Variant`). The name suggests "a flavour of the same model", but it selects a different model row within a tier. Today the entire feature is one entry in `agentrun.tierTable`:

| Vendor | Tier | Variant | Resolves to |
|---|---|---|---|
| `anthropic` | `ADVANCED` | `extended` | `claude-fable-5-1` · thinking `xhigh` |

On `openai` and `google` every variant falls back to the tier default without saying so. With a model named by id, the variant is ignored because the tier lookup never runs.

The setting an operator actually wants to change — reasoning effort — has no flag. Effort comes in only through a tier (`SIMPLE` is sonnet at `medium`, `STANDARD` is sonnet at `high`, `ADVANCED` is opus at `xhigh`). That leaves two gaps:

1. **A tier's effort cannot be overridden.** Running `ADVANCED` at `high` means giving up the tier and naming a model id.
2. **A model named by id gets no effort at all.** `ModelSpec` returns `ThinkingUnset`, so the vendor's default applies, and nothing can request `anthropic/claude-opus-5-5` at `max`.

The tier table's type is currently `map[string]map[ModelTier]map[string]tierEntry` (vendor → tier → variant → entry). The variant dimension is removed, flattening it to `map[string]map[ModelTier]tierEntry`. The effort override is applied by the caller (`ResolveModelNamed`), not by the tier table, so the table keeps saying what a tier means and nothing else.

agentkit-go (referenced via `replace` directive in `go.mod`) already provides: `core.ThinkingLevel` values, `core.ThinkingLevelOrder` (the ordered set of valid levels), `catalog.ClampThinkingLevel` (per-model clamp that searches upward then downward, never downward to `off`), and each provider's wire mapping.

## Requirements

### 1. The `--effort` flag and `$AF_MODEL_EFFORT`

`Common.Register` adds `--effort` as a string flag. Its legal values are `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` — the values of `core.ThinkingLevel` in `core.ThinkingLevelOrder`. It is declared as an enum via `DeclareEnum` so `--schema` lists the values. The `Common` struct gains an `Effort` field and loses `Variant`.

The run's effort is the first of these that is set:
1. `--effort` flag
2. `$AF_MODEL_EFFORT` environment variable
3. The tier's own effort, when `--model` resolves to a tier name
4. Unset (`ThinkingUnset`), meaning the vendor's default — exactly as for a model id today

A value outside the accepted set is a usage error (exit 2) that lists the accepted values. When the invalid value came from `$AF_MODEL_EFFORT`, the error message names the variable. The flag value is matched case-insensitively and stored in its canonical lower-case form.

`--effort` applies to a model named by id as well as to a tier.

### 2. Clamping is checked before the run

After `ResolveModelNamed` resolves the model and determines the effective effort (from the precedence in requirement 1), it calls `catalog.ClampThinkingLevel(model, effort)` for any effort that is set (not `ThinkingUnset`).

- If the clamped level differs from the requested level, the run proceeds at the clamped level and records a warning with code `effort_clamped` (a new `WarnCode` constant in `warncode.go`, mapped to stage `preflight` in `warnStages`). The warning message names both levels and the model. This is an additive change to the warning code open set per ADR 06.
- If the model supports no reachable level (`ok == false` from `ClampThinkingLevel`) and the effort was set explicitly (steps 1 or 2 of the precedence), the run fails before the first request with a usage error naming the model. Without this check, agentkit-go would silently leave the parameter out. If the effort came from a tier (step 3), the tier table is at fault, and a test (requirement 5) prevents that.
- `model.thinking` in the envelope reports the effective (clamped) level, not the requested one. The field's name, type and meaning are unchanged.

### 3. `--repair-model-effort` on `impl`

`impl` adds `--repair-model-effort` with the same accepted values as `--effort`. It is registered in `impl`'s `Flags` func and added to `toolFlags["impl"]` in `toolflags.go`.

The repair phase's effort is the first of these that is set:
1. `--repair-model-effort`
2. If `--repair-model` is given: that model's own tier effort when it is a tier, otherwise unset. The run's `--effort` and `$AF_MODEL_EFFORT` do not carry over to a separately named repair model.
3. If `--repair-model` is not given: the run's effort, because repair runs on the run's model.

Like `--repair-model`, `--repair-model-effort` implies `--repair`. Given alone (without `--repair-model`), it runs the repair phase on the run's model at the stated effort.

`--repair-model-effort` has no environment variable, matching `--repair-model`.

The repair phase's effort goes through the same clamp check and the same `effort_clamped` warning as the run's (requirement 2). The warning message names the `repair` phase to distinguish it.

### 4. Remove the variant layer

- `--variant`, `Common.Variant` and its help text are removed from `Common.Register`.
- `tierTable` loses its variant dimension. Its type becomes `map[string]map[ModelTier]tierEntry`. The `extended` entry is deleted.
- `agentrun.ModelSpec` and `agentrun.ResolveModel` lose the `variant` parameter. Their signatures become `ModelSpec(name, vendor string)` and `ResolveModel(name, vendor string)`.
- Every call site of `ModelSpec` and `ResolveModel` (in `cli.go`'s `ResolveModelNamed`) drops the variant argument.
- `cmd/fix/main.go`'s `knownFixValueFlags` gains `effort` and drops `variant`.
- `--variant` is added to `removedFlagMessages` in `toolflags.go` with a message directing the user to `--effort` and `--model`.

### 5. Tests

- **Precedence.** A table test over flag, environment variable, tier and model id checks that the effort the runner receives follows requirement 1's precedence.
- **Validation.** An unknown `--effort` value or `$AF_MODEL_EFFORT` value exits 2 and names the source.
- **Clamping.** Using a catalog row whose thinking map lacks the requested level: the run carries the clamped level, `model.thinking` reports it, and an `effort_clamped` warning is recorded. A row that supports no level fails before the first request when the effort was explicit.
- **Every tier's effort is accepted by its model.** For every vendor and tier, a tier effort that is set clamps to itself. This extends `TestEveryTierOfEveryVendorResolvesInTheCatalog`.
- **Repair.** `--repair-model ADVANCED --effort low` runs repair at `xhigh` (the tier's own effort, not the run's). `--repair-model ADVANCED --repair-model-effort high` runs repair at `high`. `--effort low` with no `--repair-model` runs repair at `low`. `--repair-model-effort` alone enables repair.
- **Removal.** `--variant` is a usage error (via `removedFlagMessages`). The `--schema` golden files of all four tools (`cmd/*/testdata/schema.golden.json`) lose `variant` and gain `effort`; `impl`'s also gains `repair-model-effort`.
- `TestTheExtendedVariantHasAMillionTokenWindow` and `TestAnUnknownVariantFallsBackToTheTierDefault` are deleted along with the behaviour they test.

### 6. Documentation updates

- `docs/cli.md`: the shared-flags table replaces the `--variant` row with `--effort`. `impl`'s flags gain `--repair-model-effort`. The `--repair-model` paragraph no longer mentions `--variant`. The interface-versions section gains an entry recording the `--variant` removal and the `--effort` / `--repair-model-effort` addition (these are flag changes, not envelope changes, so no version bump).
- `docs/configuration.md`: the tier section loses the `extended` paragraph and the `--variant extended` example. It gains the effort precedence from requirement 1, `$AF_MODEL_EFFORT`, and the long-context example `--model anthropic/claude-fable-5-1 --effort xhigh`.
- `docs/model-usage.md`: the repair paragraph states requirement 3's rule instead of "the same `--vendor` and `--variant`".
- `docs/errata/agentkit_model_resolution.md` §2 gets a closing note that the `extended` variant no longer exists and why.
- A new ADR (`docs/adr/08-effort-replaces-variant.md`) records the decision: effort as an explicit setting independent of the model, and the removal of variants without compatibility.

## Design Decisions

1. **No deprecation period for `--variant`.** The PRD explicitly states no backward compatibility. The variant feature was backed by exactly one entry in one vendor's table, and the replacement (`--model <id> --effort <level>`) is strictly more capable. Adding `--variant` to `removedFlagMessages` gives a clear message instead of the generic "flag provided but not defined".

2. **`$AF_MODEL_EFFORT` naming convention.** Follows the existing pattern: `$AF_MODEL` for `--model`, `$AF_MODEL_VENDOR` for `--vendor`, so `$AF_MODEL_EFFORT` for `--effort`. The `$AF_MODEL_` prefix groups all model-related environment variables.

3. **Effort precedence: flag > env > tier > unset.** This mirrors how `--model` works (`ModelSpec()` checks flag, then `$AF_MODEL`, then `$AGENTKIT_MODEL`, then default). The tier's effort is step 3, not step 2, because an explicit environment variable should override a tier's built-in effort — the operator is saying "I want this effort regardless of which tier I pick".

4. **Repair model does not inherit the run's `--effort`.** When `--repair-model` names a different model, the run's `--effort` does not carry over. A stronger repair model running at a lower effort borrowed from the main run would be a surprise. The repair model gets its tier's effort (if it is a tier) or unset (if it is an id), unless `--repair-model-effort` overrides.

5. **Clamping failure is a usage error only for explicit effort.** If the effort came from a tier (step 3), the tier table is at fault, not the operator. A test ensures every tier's effort is accepted by its model, so this path cannot be reached in a correct build. If the effort was explicit (flag or env), the operator asked for something the model cannot do, and a usage error before the first request is the right answer.

6. **`effort_clamped` warning stage is `preflight`.** Clamping is checked in `ResolveModelNamed`, which runs during the preflight stage of `app.go`'s `execute`. The existing `warnStages` pattern maps each code to exactly one stage; `preflight` is correct here.

7. **The envelope's `model.thinking` reports the effective level, not the requested one.** This is the existing behaviour (the field means "the level the model runs at") and the PRD does not change it. The field now has a value in more cases (model ids with explicit effort), which is additive.

8. **No envelope field for effort source.** The PRD's open question asked whether the envelope should also report where the effort came from (`flag`, `env`, `tier`, or none). This is deferred: it would be additive and cheap, but it is not required for the core feature. The command line and the envelope's `model.spec` together answer "why is this run at this level" for anyone debugging.

9. **`--repair-model-effort` has no environment variable.** This matches `--repair-model`, which also has no environment variable. The repair model is a per-invocation override, not a shell-wide default.

10. **Case-insensitive matching for `--effort`.** Follows the existing pattern for tier names (`strings.ToUpper` in `ModelSpec`). The effort value is normalized to lower-case on input, matching `core.ThinkingLevel`'s canonical form.

## Verified External API

| Symbol | Assumed signature | Source | Status |
|---|---|---|---|
| `core.ThinkingLevel` | `type ThinkingLevel string` | Used in `internal/agentrun/models.go` as `core.ThinkingLevel` with constants like `core.ThinkingMedium`, `core.ThinkingHigh`, `core.ThinkingXHigh`, `core.ThinkingUnset` | **Verified from call sites** |
| `core.ThinkingLevelOrder` | `var ThinkingLevelOrder []ThinkingLevel` — the ordered set of valid levels | Referenced in the PRD; not called in existing code | **Unverified** — agentkit-go source is outside the workspace |
| `catalog.ClampThinkingLevel` | `func ClampThinkingLevel(m *core.Model, level ThinkingLevel) (ThinkingLevel, bool)` — returns the nearest supported level and whether any level is reachable | Referenced in the PRD; not called in existing code | **Unverified** — agentkit-go source is outside the workspace |
| `catalog.ResolveModel` | `func ResolveModel(spec string) (*core.Model, error)` | Called in `internal/agentrun/models.go:155` | **Verified from call site** |
| `core.Model` | Struct with fields `ID`, `Provider`, `API`, `ContextWindow`, `MaxTokens`, `Cost`, `Cloned`, `ClonedFrom` | Used throughout `internal/agentrun/models_test.go` | **Verified from call sites** |
