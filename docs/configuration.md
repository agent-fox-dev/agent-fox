# Configuration

The spec CLI uses the Anthropic Claude API for LLM-powered spec generation.
This document covers how to set your API credentials, choose a model, switch
between LLM providers, and manage configuration files.

## Quick Start

Set your Anthropic API key and you are ready to go:

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
```

No configuration files are required. With the API key in your environment, the
tool uses `claude-sonnet-4-6` (the STANDARD tier) by default.

## Model Selection

Four registered models are available across three tiers:

| Tier | Model ID | Variant | Use case |
|------|----------|---------|----------|
| `SIMPLE` | `claude-haiku-4-5` | standard | Fast, low-cost tasks |
| `STANDARD` | `claude-sonnet-4-6` | standard | Default -- good balance of quality and speed |
| `ADVANCED` | `claude-opus-4-6` | standard | Highest quality, slower and more expensive |
| `ADVANCED` | `claude-opus-4-6[1m]` | extended | 1M-context window for large specs |

The default tier is `STANDARD`.

### Override via environment variable

Set `AF_SPEC_MODEL` to a tier name or a direct model ID:

```sh
# Use the ADVANCED tier
export AF_SPEC_MODEL=ADVANCED

# Or specify a model ID directly
export AF_SPEC_MODEL=claude-opus-4-6

# Use the 1M-context extended variant directly
export AF_SPEC_MODEL=claude-opus-4-6[1m]
```

This environment variable has the highest precedence and overrides any
file-based configuration.

### Override via config file

Add a `[model]` section to your TOML config file:

```toml
[model]
model = "ADVANCED"
```

The `model` field accepts either a tier name (`SIMPLE`, `STANDARD`, `ADVANCED`)
or a model ID (`claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`,
`claude-opus-4-6[1m]`). Tier names are matched case-insensitively.

## LLM Providers

### Anthropic API (Direct)

This is the default provider. It requires only the `ANTHROPIC_API_KEY`
environment variable:

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
```

No additional configuration is needed.

### Google Vertex AI

To use Claude through Google Cloud Vertex AI:

1. Set the environment variable to enable Vertex:

   ```sh
   export CLAUDE_CODE_USE_VERTEX=1
   ```

2. Set the `CLOUD_ML_REGION` environment variable (required by the Anthropic
   SDK):

   ```sh
   export CLOUD_ML_REGION="us-east5"
   ```

3. Authenticate with Google Cloud using Application Default Credentials
   (e.g., `gcloud auth application-default login`). The project ID is
   auto-detected from your credentials.

When Vertex AI is enabled, `ANTHROPIC_API_KEY` is not needed -- authentication
is handled through Google Cloud Application Default Credentials.

Note: The `[provider]` section in the TOML config file accepts
`vertex_project` and `vertex_region` fields, but these are not currently
passed to the Anthropic SDK client. Use the `CLOUD_ML_REGION` environment
variable and Application Default Credentials instead.

### AWS Bedrock

To use Claude through AWS Bedrock:

1. Set the environment variable to enable Bedrock:

   ```sh
   export CLAUDE_CODE_USE_BEDROCK=1
   ```

2. Configure your AWS credentials using any standard method (environment
   variables, `~/.aws/credentials`, IAM role, etc.).

When Bedrock is enabled, `ANTHROPIC_API_KEY` is not needed -- authentication
is handled through your AWS credentials.

### Provider precedence

If multiple provider environment variables are set, the first match wins:

1. `CLAUDE_CODE_USE_VERTEX` set (any non-empty value) -- use Vertex AI
2. `CLAUDE_CODE_USE_BEDROCK` set (any non-empty value) -- use Bedrock
3. Neither set -- use the direct Anthropic API

## Configuration Files

### TOML config

The tool reads TOML configuration from two locations, checked in order:

| Location | Scope |
|----------|-------|
| `.specs/config.toml` (relative to working directory) | Project-local |
| `~/.specs/config.toml` | Global (user-wide) |

The first file found is used. Project-local settings take precedence over
global settings. Symlinked config files are rejected for security.

#### Example config file

```toml
[model]
model = "ADVANCED"

[provider]
auth_method = ""
vertex_project = "my-gcp-project"
vertex_region = "us-east5"
```

The `[model]` section controls which Claude model is used. The `[provider]`
section stores authentication and cloud provider settings.

#### Available `[model]` fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `model` | string | `"STANDARD"` | Default model tier or model ID for all phases |
| `model_variant` | string | *(none)* | Variant for tier resolution (e.g. `"extended"` for the 1M-context model) |
| `assess_model` | string | *(inherits `model`)* | Override for the PRD assessment phase |
| `refine_model` | string | *(inherits `model`)* | Override for the PRD refinement phase |
| `generate_model` | string | *(inherits `model`)* | Override for artifact generation (and repair) |

Each per-phase field accepts either a tier name (`SIMPLE`, `STANDARD`, `ADVANCED`) or a
direct model ID (`claude-haiku-4-5`, etc.). When omitted, the phase inherits the top-level
`model` value.

The `model_variant` field selects a variant within a tier. When set to
`"extended"` with `model = "ADVANCED"`, the 1M-context model
`claude-opus-4-6[1m]` is used instead of the standard `claude-opus-4-6`.
When the variant does not match any registered model for the resolved tier,
the tier default is used.

#### Cost-optimization example

Use a cheaper model for the assessment phase (which is a short, fast call) and a more
capable model for artifact generation (which produces the bulk of the output):

```toml
[model]
model = "STANDARD"        # default for phases not listed below
assess_model = "SIMPLE"   # fast assessment — saves cost
generate_model = "ADVANCED"  # high-quality artifact generation
```

With this configuration:
- `spec assess` uses `claude-haiku-4-5` (SIMPLE)
- `spec generate` uses `claude-opus-4-6` (ADVANCED) for all three artifact calls
- `spec refine` uses `claude-sonnet-4-6` (STANDARD, inherited from `model`)

#### Extended-context example

Use the 1M-context variant of the ADVANCED tier for large specs where the
200K context window of `claude-opus-4-6` is insufficient:

```toml
[model]
model = "ADVANCED"
model_variant = "extended"
```

This resolves to `claude-opus-4-6[1m]`. Alternatively, specify the model ID
directly:

```sh
export AF_SPEC_MODEL=claude-opus-4-6[1m]
```

#### Available `[provider]` fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `auth_method` | string | `""` | Authentication method hint |
| `vertex_project` | string | `""` | Google Cloud project ID (loaded but not currently used by the client) |
| `vertex_region` | string | `""` | Google Cloud region (loaded but not currently used by the client) |

## Configuration Precedence

Model resolution follows a strict 3-step precedence chain. The first source
that provides a value wins:

1. **`AF_SPEC_MODEL` environment variable** -- highest priority. If set, file-based
   model resolution is skipped entirely (auth fields from config files are still
   applied if available).

2. **`[model]` section in TOML config** -- if the section exists in
   `.specs/config.toml` or `~/.specs/config.toml`, its `model` value is used.
   Auth fields are read from the `[provider]` section of the same file.

3. **Hardcoded default** -- `model = "STANDARD"`, which resolves to
   `claude-sonnet-4-6`.

## Prompt Caching

The AI layer supports prompt caching with configurable policies to reduce
latency and cost for repeated system prompts. Three policies are available:

| Policy | Behavior |
|--------|----------|
| `none` | Caching disabled; no `cache_control` metadata is injected |
| `default` | Injects ephemeral `cache_control` when the system prompt exceeds the token threshold (this is the default) |
| `extended` | Same as `default` but with a 1-hour TTL on cached content |

Cache injection is conditional on the estimated token count of the system
prompt exceeding a model-specific threshold:

- **Sonnet models**: 2048 tokens
- **Haiku and Opus models**: 4096 tokens

If a provider rejects `cache_control` metadata (e.g., a 400 error mentioning
`cache_control`), the AI layer automatically retries the request without
caching.

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | API key for direct Anthropic API access |
| `AF_SPEC_MODEL` | Override model tier (`SIMPLE`, `STANDARD`, `ADVANCED`) or model ID (e.g. `claude-opus-4-6[1m]`) |
| `CLAUDE_CODE_USE_VERTEX` | Set to any non-empty value to route requests through Google Vertex AI |
| `CLOUD_ML_REGION` | Google Cloud region for Vertex AI (required when Vertex is enabled) |
| `CLAUDE_CODE_USE_BEDROCK` | Set to any non-empty value to route requests through AWS Bedrock |
| `AF_AGENT` | Set to `1` for agent mode (suppresses banner, routes errors to JSON on stdout) |

## See Also

- [Model Usage](model-usage.md) — how the spec pipeline selects and resolves
  Claude models across the assess, refine, and generate phases, with details on
  prompt caching thresholds, retry behavior, and known limitations.
