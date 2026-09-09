# Model Usage

This document describes how the spec pipeline selects, resolves, and invokes
Claude models across its three AI-powered stages: assessment, refinement, and
artifact generation.

For credentials, provider selection, and TOML config file syntax, see
[Configuration](configuration.md).

## Model Tiers

Three named tiers map to specific Claude model IDs:

| Tier | Model ID | Use case |
|------|----------|----------|
| `SIMPLE` | `claude-haiku-4-5` | Fast and low-cost; suited for lightweight assessment calls |
| `STANDARD` | `claude-sonnet-4-6` | Balanced quality and cost; the default for all phases |
| `ADVANCED` | `claude-opus-4-6` | Highest quality; best for complex artifact generation |

The default tier is `STANDARD` unless overridden by configuration or environment
variable.

## Pipeline Phases

The spec pipeline runs three sequential AI-powered stages. Every stage calls the
Anthropic API with a fixed temperature and token budget:

| Phase | Source location | `max_tokens` | Temperature | Configurable via | Description |
|-------|----------------|-------------|-------------|------------------|-------------|
| `assess` | `agent.py:134` | 4 096 | 0.2 | `assess_model` | Sends the PRD to the model for structured quality evaluation via the `submit_assessment` tool |
| `refine` | `agent.py:198` | 16 384 | 0.2 | `refine_model` | Sends the PRD, user answers, and prior assessment; the model rewrites the PRD and issues a new assessment via `submit_prd_update` and `submit_assessment` tools |
| `generate` (per artifact) | `agent.py:289` | 65 536 | 0.2 | `generate_model` | Generates one artifact at a time (`requirements.json`, `test_spec.json`, `tasks.json`) in sequence; each call includes all prior artifacts as context |
| `generate` (repair) | `agent.py:381` | 65 536 | 0.2 | `generate_model` | Asks the model to fix schema-validation errors in a generated artifact; up to `_MAX_REPAIR_ATTEMPTS = 2` attempts per artifact |

Source files are under `packages/agentspec/agentspec/`. The repair loop uses the
same model tier as the main `generate` stage.

All phases default to `STANDARD` (`claude-sonnet-4-6`) when no per-phase
override is configured.

## How the Tier Is Resolved

Both the Python library and the Go CLI resolve the model through the same
configuration chain:

1. **`LoadConfig()` / `load_config()`** constructs an `AgentSpecConfig` with
   defaults (`model="STANDARD"`, all per-phase fields empty/`None`). It checks
   two TOML paths in order: `.specs/config.toml` (project-local), then
   `~/.specs/config.toml` (user-global). The first file found wins; files are
   not merged. Symlinked config files are rejected (Python only).

2. **`AF_SPEC_MODEL` environment variable**, if set, overrides `config.model`.
   Per-phase fields (`assess_model`, `refine_model`, `generate_model`) are not
   affected by this variable — it applies to all phases uniformly.

3. **`config.ModelForPhase(phase)` / `config.model_for_phase(phase)`** returns
   the phase-specific field when it is set; otherwise falls back to
   `config.model`.

4. **`SpecAgent(model_tier)`** / **`NewSpecAgent(model_tier)`** is constructed
   with the resolved tier string.

5. On each API call, **`resolve_model(name)`** converts the tier string to a
   concrete model ID:
   - If `name` matches `SIMPLE`, `STANDARD`, or `ADVANCED` (exact-case in
     Python via `ModelTier` StrEnum; case-insensitive in Go via
     `strings.ToUpper`), the corresponding default model ID is returned from
     `TIER_DEFAULTS`.
   - If `name` matches a known entry in `MODEL_REGISTRY`, it is returned as-is,
     allowing direct model ID strings such as `claude-sonnet-4-6`.
   - Otherwise a `ConfigError` is raised (Python) or an error is returned (Go).

Sources: `packages/agentspec/agentspec/client.py:66–84`;
`golang/agentspec/model_registry.go:60–77`;
`golang/agentspec/session.go:resolveAgent()`.

## Configuration

Select a tier globally or override it per pipeline phase in `.specs/config.toml`:

```toml
[model]
model = "STANDARD"             # base tier for all phases

assess_model   = "SIMPLE"      # short call — use the fast model
generate_model = "ADVANCED"    # long calls — use the most capable model
```

With the above configuration:

| Phase | Resolved tier | Model ID |
|-------|--------------|----------|
| `assess` | `SIMPLE` | `claude-haiku-4-5` |
| `refine` | `STANDARD` (inherits `model`) | `claude-sonnet-4-6` |
| `generate` + repair | `ADVANCED` | `claude-opus-4-6` |

To override a single run without editing a file:

```sh
export AF_SPEC_MODEL=ADVANCED
spec generate
```

`AF_SPEC_MODEL` accepts a tier name (`SIMPLE`, `STANDARD`, `ADVANCED`) or a
direct model ID. It has the highest precedence and applies to all phases; it
does **not** interact with per-phase fields set in the TOML file.

For the full reference — file locations, all available fields, provider
settings, and precedence rules — see [Configuration](configuration.md).

## Prompt Caching

The AI layer injects Anthropic prompt-cache metadata into system prompts when
the estimated token count of the system text exceeds a model-specific threshold.
The estimate is computed as `len(system_text) // 4`.

| Model | Cache threshold (estimated tokens) |
|-------|-------------------------------------|
| `claude-sonnet-4-6` | 2 048 |
| `claude-haiku-4-5` | 4 096 |
| `claude-opus-4-6` | 4 096 |
| Any other model | 4 096 (default) |

Source: `packages/agentspec/agentspec/client.py:91–95`
(`_CACHE_TOKEN_THRESHOLDS`).

Three cache policies control what metadata is injected:

| Policy | Behavior |
|--------|----------|
| `CachePolicy.NONE` | Caching disabled; no `cache_control` key is added |
| `CachePolicy.DEFAULT` | Injects `{"type": "ephemeral"}` on the last system block when the threshold is exceeded |
| `CachePolicy.EXTENDED` | Injects `{"type": "ephemeral", "ttl": "1h"}` when the threshold is exceeded |

`CachePolicy.DEFAULT` is the active policy for all phases. The cache policy is
not user-configurable via environment variable or config file.

If the API returns a `BadRequestError` mentioning `cache_control`, the request
is automatically retried without caching metadata.

## Retry Behavior

All API calls issued from `_call_api()` are retried with fixed backoff delays
when transient errors occur.

| Attempt | Delay before next attempt |
|---------|--------------------------|
| 1 | 2 s |
| 2 | 30 s |
| 3 | 60 s |
| 4 (final) | — (exception re-raised immediately) |

Maximum attempts: 4 (`len(_RETRY_DELAYS) + 1`, where
`_RETRY_DELAYS = (2.0, 30.0, 60.0)`). Source:
`packages/agentspec/agentspec/client.py:141`.

Errors that trigger a retry:

| Error class | Condition |
|-------------|-----------|
| `anthropic.RateLimitError` | HTTP 429 |
| `anthropic.APIStatusError` | `status_code >= 500` |
| `OSError` | Network-level failure |

After all attempts are exhausted, `SpecAgent._call_api()` wraps the final
exception as an `AgentError`. Error categories classified as retryable:
`"rate_limit"`, `"transient"`, `"overloaded"`.

## Known Limitations

- **Go CLI has no per-phase model support** ([#94](https://github.com/agent-fox-dev/spec/issues/94)):
  Per-phase fields (`assess_model`, `refine_model`, `generate_model`) are
  implemented in the Python `agentspec` library only. The Go binary uses a single
  hardcoded tier across all phases.

- **No 1M context-window variant** ([#95](https://github.com/agent-fox-dev/spec/issues/95)):
  The model registry does not include extended-context variants such as
  `claude-opus-4-6[1m]`. Specifying a 1M model ID raises a `ConfigError`
  (Python) or returns an error (Go).
