# ADR 08: Effort replaces variant

## Status

Accepted

## Context

The `--variant` flag selected a different model row within a tier. In practice
the entire feature was one entry in the tier table: the `extended` variant of
`ADVANCED` on Anthropic, which resolved to `claude-fable-5-1` at `xhigh`
thinking. On `openai` and `google` every variant fell back to the tier default
without saying so. With a model named by id, the variant was ignored because
the tier lookup never ran.

The setting an operator actually wanted to change — reasoning effort — had no
flag. Effort came in only through a tier (`SIMPLE` is sonnet at `medium`,
`STANDARD` is sonnet at `high`, `ADVANCED` is opus at `xhigh`). That left two
gaps:

1. **A tier's effort could not be overridden.** Running `ADVANCED` at `high`
   meant giving up the tier and naming a model id.
2. **A model named by id got no effort at all.** `ModelSpec` returned
   `ThinkingUnset`, so the vendor's default applied, and nothing could request
   `anthropic/claude-opus-5-5` at `max`.

## Decision

**Effort is an explicit, independent setting.** Two independent settings choose
a run's model: what model (`--model`, a tier or a catalog spec) and how hard it
thinks (`--effort` / `$AF_MODEL_EFFORT`).

The effort precedence is:

1. `--effort` flag
2. `$AF_MODEL_EFFORT` environment variable
3. The tier's own effort, when `--model` resolves to a tier name
4. Unset (`ThinkingUnset`), meaning the vendor's default

Any resolved model, whether it came from a tier or a model id, can run at any
effort its catalog row supports. When the requested effort is not supported,
`catalog.ClampThinkingLevel` adjusts it to the nearest supported level and an
`effort_clamped` warning is recorded.

**Variants are removed without compatibility.** The `--variant` flag, the
`Common.Variant` field, and the variant dimension of the tier table are deleted
outright. There is no hidden alias, no deprecation period. A script that passes
`--variant` gets a clear message directing it to `--effort` and `--model`.

The `extended` variant existed only for Anthropic and only to select a
long-context model. To get long context now, name the model by id:
`--model anthropic/claude-fable-5-1 --effort xhigh`.

**`impl` gains `--repair-model-effort`.** The repair phase can run at its own
effort, independent of the run's, via `--repair-model-effort`. Its precedence
is: (1) `--repair-model-effort`, (2) the repair model's tier effort when
`--repair-model` names a tier, (3) the run's effort when no separate repair
model is given.

## Consequences

- The tier table's type flattens from
  `map[string]map[ModelTier]map[string]tierEntry` to
  `map[string]map[ModelTier]tierEntry`.
- `ModelSpec` and `ResolveModel` lose their `variant` parameter.
- The envelope's `model.thinking` reports the effective (clamped) level, not
  the requested one. The field's name, type and meaning are unchanged.
- `$AF_MODEL_EFFORT` follows the `AF_MODEL_` naming convention for
  model-related environment variables.
- No concept remains that only one vendor's table backs (the `extended` variant
  existed only for Anthropic).
