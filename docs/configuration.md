# Configuration

The `spec` CLI reaches a model through [AgentKit](https://github.com/agent-fox-dev/coder)
(module `github.com/agentfox/agentkit-go`), so it speaks every wire API that
library implements: Anthropic, both OpenAI wires, Google and Ollama, plus the
OpenAI-compatible gateways (OpenRouter, DeepSeek, Groq, xAI, Together,
Moonshot). This document covers credentials, model selection, what the model
is allowed to read, and the config file.

## Quick start

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
```

No configuration file is required. With a key in the environment the tool uses
the `STANDARD` tier, which resolves to `claude-sonnet-5` at high thinking effort.

`spec models` prints what each phase will actually run on, what it costs, and
whether this shell can authenticate to it:

```sh
spec models          # the three phases, as configured here
spec models --all    # every tier of every vendor
```

## Credentials

Each vendor has an **ordered** list of environment variables. The first one set
wins, and the header scheme differs per variable — which is why it is an
ordered list rather than a `<VENDOR>_API_KEY` convention.

| Vendor | Variables, in order | Sent as |
|---|---|---|
| `anthropic` | `ANTHROPIC_AUTH_TOKEN` | `Authorization: Bearer` |
| | `ANTHROPIC_OAUTH_TOKEN` | `Authorization: Bearer` |
| | `ANTHROPIC_API_KEY` | `x-api-key` |
| `openai` | `OPENAI_API_KEY` | `Authorization: Bearer` |
| `google` | `GOOGLE_GENERATIVE_AI_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY` | `x-goog-api-key` |
| `ollama` | `OLLAMA_API_KEY` (usually unset) | `Authorization: Bearer` |
| anything else | `<VENDOR>_API_KEY` | `Authorization: Bearer` |

**A credential has three states, not two.** A deployment behind a gateway that
authenticates by URL, or on an instance role, has no key this process can read
and a transport that will nonetheless authenticate. That is `ambient`, and it
passes the preflight check that an unconfigured environment fails — otherwise
every such deployment would be refused for a key it was never going to have.
Setting only a base URL puts you in that state.

### Base URLs

| Variable | Points at |
|---|---|
| `ANTHROPIC_BASE_URL` | a proxy or gateway in front of Anthropic |
| `OPENAI_BASE_URL` | Azure OpenAI, a gateway, or any OpenAI-compatible server |
| `GOOGLE_GEMINI_BASE_URL` | Vertex AI, or a proxy |
| `OLLAMA_HOST` | your Ollama server (default `http://localhost:11434`) |

### Vertex AI and AWS Bedrock

`CLAUDE_CODE_USE_VERTEX` and `CLAUDE_CODE_USE_BEDROCK` **are refused**, with an
error naming what to do instead. AgentKit has no wire for Claude on either
platform, and honouring the variable would have sent the request to
`api.anthropic.com` with credentials meant for somewhere else.

Both deployments are usually fronted by an Anthropic-compatible gateway: point
`ANTHROPIC_BASE_URL` at it and set a token there. Gemini on Vertex is a
different matter and is reached with `GOOGLE_GEMINI_BASE_URL`. See
[`docs/errata/agentkit_model_resolution.md`](errata/agentkit_model_resolution.md).

## Model selection

### Tiers

Three named tiers are what a spec author chooses between, so that writing a PRD
does not require knowing which model id is current this month.

| Tier | `anthropic` (default) | `openai` | `google` |
|---|---|---|---|
| `SIMPLE` | `claude-sonnet-5` · thinking `medium` | `gpt-5.6-luna` | `gemini-3.5-flash-lite` |
| `STANDARD` | `claude-sonnet-5` · thinking `high` | `gpt-5.6-terra` | `gemini-3.8-flash` |
| `ADVANCED` | `claude-opus-5` · thinking `xhigh` | `gpt-6-astra` | `gemini-3.1-pro-preview` |

A tier names a model **and** a reasoning effort. Separating the two lets
`SIMPLE` and `STANDARD` share a model and run it at different depth, which on
current Anthropic rows is the cheaper axis to move: effort is what the model
spends, and the row is what it costs per token. A model named by id instead of
a tier carries no thinking level — the operator chose the model, so the vendor
default applies.

The `extended` variant of `ADVANCED` selects a model with a 1 000 000-token
context window (`claude-fable-5-1` on Anthropic) for a spec too large for the
tier default.

### Model ids

Anything that is not a tier name goes to the catalog unchanged, so a
`vendor/model-id` pair, a bare unambiguous id, and a model released after this
build was cut all work:

```sh
export AF_SPEC_MODEL=anthropic/claude-opus-5
export AF_SPEC_MODEL=openai/gpt-6-astra
export AF_SPEC_MODEL=ADVANCED
```

`AF_SPEC_MODEL` has the highest precedence and overrides file-based model
resolution entirely.

### Per-phase models

```toml
[model]
model = "STANDARD"           # default for phases not listed below
vendor = "anthropic"         # which tier table SIMPLE/STANDARD/ADVANCED use
model_variant = ""           # "extended" for the long-context row of a tier
assess_model = "SIMPLE"      # a short, fast call — save cost here
refine_model = ""            # inherits model
generate_model = "ADVANCED"  # the bulk of the output
```

Each per-phase field takes a tier name or a model id. When omitted the phase
inherits `model`.

## What the model may read

By default a spec is written from the PRD alone. Two separate grants change
that, and they are separate because wanting a project's steering directives is
not wanting a source-reading run.

| Grant | Flag | What it does |
|---|---|---|
| Source access | `spec refine --read-source`, `spec generate --read-source` | Gives the model AgentKit's non-mutating tools (`read_file`, `find_files`, `search_files`, …) rooted at `--source` |
| Project trust | `--trust-project` | Admits repository-authored skills and context files — `AGENTS.md`, `CLAUDE.md`, `.specs/steering.md` — into the system prompt |

The read-only mandate is a mechanism rather than a prompt instruction: the
mutating tools (`write_file`, `edit_file`, `execute`, `run_command`,
`powershell`) are excluded from the resolved set, an invariant checks that set
before the first request, and AgentKit's own unguarded-shell guard fails any
run where a shell survived. `fetch_url` is never registered.

Project trust defaults to off because a repository that is merely the current
directory would otherwise author part of the system prompt that reads it — git
clone, cd, run.

## Bounds

One phase is bounded by turns and by spend. Both stop the run cleanly at a turn
boundary rather than aborting mid-call.

| Setting | Flag | TOML | Default |
|---|---|---|---|
| Turns per phase | `--max-turns` | `[agent] max_turns` | 40 |
| Spend per phase | `--max-budget` | `[agent] max_budget_usd` | 5.00 |
| Attempts per model call | — | `[agent] max_attempts` | 3 |

The turn budget is also the repair budget: a validation failure returns to the
model as a tool error and costs a turn, so a model that cannot satisfy a rule
stops costing money rather than looping until the context window ends the run.

## Configuration files

Read in order; the first found is used. Symlinked config files are rejected.

| Location | Scope |
|---|---|
| `.specs/config.toml` | project-local |
| `~/.specs/config.toml` | user-global |

```toml
[model]
model = "STANDARD"
vendor = "anthropic"
model_variant = ""
assess_model = ""
refine_model = ""
generate_model = ""

[agent]
read_source = false
trust_project = false
max_turns = 40
max_budget_usd = 5.0
max_attempts = 3

[provider]
auth_method = ""       # read, not used
vertex_project = ""    # read, not used
vertex_region = ""     # read, not used
```

Command-line flags win over the file for the fields they set; a flag that was
not passed leaves the file's value alone.

## Precedence

1. `AF_SPEC_MODEL` — skips file-based model resolution entirely.
2. `[model]` in `.specs/config.toml`, else `~/.specs/config.toml`.
3. `model = "STANDARD"`, resolving to `anthropic/claude-sonnet-5` at high effort.

## Environment variables

| Variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY` and the other vendor keys | credentials; see the table above |
| `<VENDOR>_BASE_URL` | gateway, proxy or local server for that vendor |
| `AF_SPEC_MODEL` | override the model tier or id for every phase |
| `AF_AGENT` | `1` for agent mode: no banner, quiet, JSON errors on stdout, no event trace |
| `SPEC_DIR` | override the default spec root (`.specs`); `--spec-dir` wins |
| `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK` | **refused**; see above |

## See also

- [Model Usage](model-usage.md) — what each phase sends and how a failure is repaired
- [Spec CLI Reference](cli.md) — commands and flags
- [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md) — why the pipeline is shaped this way
