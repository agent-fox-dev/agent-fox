# Replace the variant flag with effort

## Intent

Every tool takes `--variant`, from the shared flag set in
`internal/toolio/cli.go`. The name suggests "a flavour of the same model". It
does something else: it chooses a *different model row* within a tier. Today
the whole feature is one entry in `agentrun.tierTable`:

| Vendor | Tier | Variant | Resolves to |
|---|---|---|---|
| `anthropic` | `ADVANCED` | `extended` | `claude-fable-5-1` · thinking `xhigh` |

On `openai` and `google` every variant falls back to the tier default without
saying so. With a model named by id, the variant is ignored, because the tier
lookup never runs.

The setting an operator actually wants to change, the reasoning effort, has no
flag. Effort comes in only through a tier (`SIMPLE` is sonnet at `medium`,
`STANDARD` is sonnet at `high`, `ADVANCED` is opus at `xhigh`). That leaves two
gaps:

- **A tier's effort cannot be changed.** Running `ADVANCED` at `high` means
  giving up the tier and naming a model id.
- **A model named by id gets no effort at all.** `ModelSpec` returns
  `ThinkingUnset`, so the vendor's default applies, and nothing can ask for
  `anthropic/claude-opus-5-5` at `max`.

This PRD removes `--variant` and adds `--effort`. A tier keeps meaning a model
and an effort, and `--effort` overrides that effort. A model id plus
`--effort` covers everything `--variant` did: the long-context row is
`--model anthropic/claude-fable-5-1 --effort xhigh`.

The work splits between the two repositories like this:

- agent-fox owns the tiers, the flags and the precedence. This PRD changes only
  agent-fox.
- agentkit-go already provides what effort needs: the `core.ThinkingLevel`
  values, the order they are searched in (`core.ThinkingLevelOrder`), the
  per-model clamp (`catalog.ClampThinkingLevel`), and each provider's wire
  mapping.

## Goals

- Two independent settings choose a run's model: what model (`--model`, a tier
  or a catalog spec) and how hard it thinks (`--effort`).
- Any resolved model, whether it came from a tier or an id, can run at any
  effort its catalog row supports.
- `impl`'s repair phase can run at its own effort, independent of the run's.
- The envelope reports the effort the model actually runs at. If that differs
  from what was asked for, a warning says so.
- No concept is left that only one vendor's table backs.

## Non-goals

- **Backward compatibility.** `--variant` is removed outright: no hidden
  alias, no deprecation period, no translation of `--variant extended`. A
  script that passes it gets the usual "flag provided but not defined" usage
  error.
- **A vendor-neutral way to ask for long context.** `extended` was the only
  way to say "give me the long-context model" without naming one, and only
  Anthropic had one. To get long context, name the model.
- **Changing the tier defaults.** `SIMPLE`, `STANDARD` and `ADVANCED` keep their
  current models and efforts. The `openai` and `google` rows keep having no
  effort of their own.
- **Changing agentkit-go.** The clamp and the wire mapping are used as they are.
- **Per-phase effort.** The only exception is repair, below. Every other phase
  of a run runs at the run's effort.

## Functional requirements

### 1. `--effort` and `$AF_MODEL_EFFORT`

- `Common.Register` adds `--effort`. Its value is one of `off`, `minimal`,
  `low`, `medium`, `high`, `xhigh`, `max`: the values of `core.ThinkingLevel`,
  in `core.ThinkingLevelOrder`. It is declared as an enum (`DeclareEnum`) so
  `--schema` lists the values.
- `$AF_MODEL_EFFORT` supplies the value when the flag is absent, the same way
  `$AF_MODEL` and `$AF_MODEL_VENDOR` do for their flags.
- The run's effort is the first of these that is set:
  1. `--effort`
  2. `$AF_MODEL_EFFORT`
  3. the tier's own effort, when `--model` is a tier name
  4. unset, meaning the vendor's default, exactly as for a model id today
- A value outside the set is a usage error (exit 2) that lists the accepted
  values. The same applies to `$AF_MODEL_EFFORT`, and the error names the
  variable.
- `--effort` applies to a model named by id as well as to a tier. That is the
  point of the change.
- `--effort` is matched case-insensitively and stored in its canonical
  lower-case form, as tier names are.

### 2. Clamping is checked before the run

agentkit-go clamps on every request: it searches upward first, then downward,
and never downward to `off`. So a requested level can silently become another.
agent-fox resolves the model before any prompt is built, and checks the effort
at the same point:

- `ResolveModelNamed` calls `catalog.ClampThinkingLevel(model, effort)` for an
  effort that is set.
- If the result is a level other than the one requested, the run goes ahead at
  the clamped level and records a new warning code, `effort_clamped`, naming
  both levels and the model. Warning codes are an open set, so this is an
  additive change under
  [ADR 06](../adr/06-version-the-envelope-interface.md).
- If the model supports no level reachable from the request (`ok == false`)
  and the effort was set explicitly (step 1 or 2 above), the run fails before
  the first request with a usage error naming the model. Without that check,
  agentkit-go would silently leave the parameter out, which is exactly the
  quiet substitution this PRD exists to remove. If the effort came from a tier
  (step 3), the tier table is at fault, and a test (requirement 5) keeps that
  from happening.
- `model.thinking` in the envelope reports the effective (clamped) level, not
  the requested one. The field and its meaning are unchanged. Until now the two
  could not differ for a tier, and a model id never had a level.

### 3. `--repair-model-effort` on `impl`

- `impl` adds `--repair-model-effort`, with the same values as `--effort`.
- The repair phase's effort is the first of these that is set:
  1. `--repair-model-effort`
  2. if `--repair-model` is given: that model's own tier effort when it is a
     tier, otherwise unset. **The run's `--effort` and `$AF_MODEL_EFFORT` do
     not carry over to a separately named repair model.** A stronger repair
     model running at a lower effort borrowed from the main run would be a
     surprise.
  3. if `--repair-model` is not given: the run's effort, because repair runs
     on the run's model.
- Like `--repair-model`, `--repair-model-effort` implies `--repair`. Given
  alone, it runs the repair phase on the run's model at the stated effort.
- `--repair-model-effort` has no environment variable, matching
  `--repair-model`.
- The repair phase's effort goes through the same clamp and the same
  `effort_clamped` warning as the run's (requirement 2). The warning names the
  `repair` phase.
- `toolio.toolFlags["impl"]` gains `repair-model-effort`.

### 4. Remove the variant layer

- `--variant`, `Common.Variant` and its help text are removed.
- `tierTable` loses its variant level. Its type becomes
  `map[string]map[ModelTier]tierEntry`, and the `extended` entry is deleted.
- `agentrun.ModelSpec` and `agentrun.ResolveModel` lose the `variant`
  parameter. Applying the effort override is the caller's job
  (`ResolveModelNamed`), not the tier table's, so the table keeps saying what a
  tier means and nothing else.
- `cmd/fix/main.go`'s `knownFixValueFlags` gains `effort` and drops `variant`.

### 5. Tests

- **Precedence.** A table test over flag, environment, tier and model id
  checks that the effort the runner receives follows requirement 1.
- **Validation.** An unknown `--effort` or `$AF_MODEL_EFFORT` exits 2 and
  names the source.
- **Clamping.** Using a catalog row whose thinking map lacks the requested
  level: the run carries the clamped level, `model.thinking` reports it, and
  an `effort_clamped` warning is recorded. A row that supports no level fails
  before the first request when the effort was explicit.
- **Every tier's effort is accepted by its model.** For every vendor and
  tier, a tier effort that is set clamps to itself. This extends
  `TestEveryTierOfEveryVendorResolvesInTheCatalog`.
- **Repair.** `--repair-model ADVANCED --effort low` runs repair at `xhigh`.
  `--repair-model ADVANCED --repair-model-effort high` runs repair at `high`.
  `--effort low` with no `--repair-model` runs repair at `low`.
  `--repair-model-effort` alone enables repair.
- **Removal.** `--variant` is a usage error. The `--schema` golden files of all
  four tools lose `variant` and gain `effort`; `impl`'s also gains
  `repair-model-effort`.
- `TestTheExtendedVariantHasAMillionTokenWindow` and
  `TestAnUnknownVariantFallsBackToTheTierDefault` are deleted along with the
  behaviour they test.

## Interface change

Per [ADR 06](../adr/06-version-the-envelope-interface.md), here is what kind
of change this is:

- **Envelope:** additive. The only change is a new warning code in an open set.
  `model.thinking` keeps its name, type and meaning ("the level the model runs
  at"); it now has a value in more cases. No version bump.
- **Flags:** `--variant` is removed, and `--effort` and `--repair-model-effort`
  are added. ADR 06's lists cover envelope fields, not flags. The `--schema`
  golden files carry the change, and `docs/cli.md`'s interface-versions
  section gets an entry recording the removal. No compatibility shim, per the
  non-goals.

## Documentation

- `docs/cli.md`: the shared-flags table replaces `--variant` with `--effort`.
  `impl`'s flags gain `--repair-model-effort`. The `--repair-model` paragraph
  no longer mentions `--variant`.
- `docs/configuration.md`: the tier section loses the `extended` paragraph and
  the `--variant extended` example. It gains the precedence from requirement 1,
  `$AF_MODEL_EFFORT`, and the long-context example
  `--model anthropic/claude-fable-5-1 --effort xhigh`.
- `docs/model-usage.md`: the repair paragraph states requirement 3's rule
  instead of "the same `--vendor` and `--variant`".
- `docs/errata/agentkit_model_resolution.md` §2 gets a closing note that the
  `extended` variant no longer exists, and why.
- An ADR records the decision: effort as an explicit setting, independent of
  the model, and the removal of variants without compatibility.

## Open questions

- Should the envelope also report where the effort came from (`flag`, `env`,
  `tier`, or none) next to `model.thinking`? It would be additive and cheap,
  and it would answer "why is this run at `medium`" without re-reading the
  command line. Not required here.
