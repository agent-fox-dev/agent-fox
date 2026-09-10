# Configuration

The tools — `spec`, `issue`, `fix` and `impl` — reach a model through
[AgentKit](https://github.com/agent-fox-dev/coder)
(module `github.com/agentfox/agentkit-go`), so it speaks every wire API that
library implements: Anthropic, both OpenAI wires, Google and Ollama, plus the
OpenAI-compatible gateways (OpenRouter, DeepSeek, Groq, xAI, Together,
Moonshot). This document covers credentials, model selection, what the model
is allowed to read, and the config file.

## Quick start

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
export GITHUB_TOKEN="ghp_..."          # only for the tools that write
```

There is no configuration file. With a key in the environment the tools use the
`STANDARD` tier, which resolves to `claude-sonnet-5` at high thinking effort.

Every run reports the model it resolved in its JSON envelope, so what actually
served a run is a field rather than something to work out:

```jsonc
"model": { "spec": "STANDARD", "id": "claude-sonnet-5", "vendor": "anthropic",
           "api": "anthropic-messages", "thinking": "high" }
```

Resolution and the credential check both happen **before** a prompt is built, so
a missing key, an unknown model or a retired platform variable fails in the
first second rather than in the tenth minute of a run you are paying for.

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

Three named tiers are what an operator chooses between, so that running a tool
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
export AF_MODEL=anthropic/claude-opus-5
export AF_MODEL=openai/gpt-6-astra
export AF_MODEL=ADVANCED

spec --model ADVANCED --variant extended ./big-idea.md
```

`--model` wins over `$AF_MODEL`, which wins over `$AGENTKIT_MODEL` (honoured so
a shell already configured for the SDK works here), which wins over the
`STANDARD` default. `--vendor` and `$AF_MODEL_VENDOR` select which tier table
the tier names resolve against, and have no effect on a model named by id.

One model serves every phase of a run. Per-phase model selection existed in the
previous CLI and is not carried over: it was configured in a file nobody edited,
and the phases of one run differ in length rather than in the kind of judgment
they need.

## What the model may read

By default a spec is written from the PRD alone. Two separate grants change
that, and they are separate because wanting a project's steering directives is
not wanting a source-reading run.

| Grant | Flag | What it does |
|---|---|---|
| Source access | `--dir` | Roots AgentKit's file tools at that directory. Every path a tool is handed is resolved against it, symlinks included, so a path that escapes is refused by the tool rather than by a paragraph in a prompt. |
| Project trust | `--trust-project` | Admits repository-authored skills and context files — `AGENTS.md`, `CLAUDE.md`, `.specs/steering.md` — into the system prompt |

Reading the source is not optional here as it was in the previous CLI: a
diagnosis written without reading the code is not a diagnosis, and a spec
written against a repository it never looked at names components that already
exist under other names. `--dir` defaults to `.`.

The read-only mandate is a mechanism rather than a prompt instruction. In every
phase but `fix`'s and `impl`'s implementation phases, the mutating tools (`write_file`,
`edit_file`, `execute`, `run_command`, `powershell`) are excluded from the
resolved set, an invariant checks that set before the first request, and
AgentKit's own unguarded-shell guard fails any run where a shell survived.
`fetch_url` is never registered by any tool, so no phase has outbound network
of any kind.

`fix`'s and `impl`'s implementation phases are the exceptions, and they run
under a second guard: `git` is limited to its read-only subcommands, `gh` is
refused outright, `find -exec`/`-delete` are refused, and `impl` additionally
refuses every write under the spec package it is implementing. See the
[tool reference](cli.md#what-the-model-may-and-may-not-do).

Project trust defaults to off because a repository that is merely the current
directory would otherwise author part of the system prompt that reads it — git
clone, cd, run.

## Bounds

One phase is bounded by turns and by spend. Both stop the run cleanly at a turn
boundary rather than aborting mid-call.

| Setting | Flag | `issue` | `fix` | `spec` | `impl` |
|---|---|---|---|---|---|
| Turns per phase | `--max-turns` | 100 | 150 | 60 | 150 |
| Spend per phase | `--budget` | $2.00 | $5.00 | $5.00 | $5.00 |
| Wall clock per phase | `--phase-timeout` | none | none | none | none |
| Attempts per model call | — | 3 | 3 | 3 | 3 |

`impl` runs one phase per task, so its per-phase ceilings multiply by the
number of tasks. `--total-budget` caps the whole run; a run that reaches it
stops between tasks with everything landed so far committed on the branch.

The turn budget is also the repair budget: a validation failure returns to the
model as a tool error and costs a turn, so a model that cannot satisfy a rule
stops costing money rather than looping until the context window ends the run.

The stop policy is turns and budget only. It deliberately does not end a run
when the terminating tool is *called*: a call the handler rejects is the repair
loop's first step, and the intended ending is the handler accepting one.

A phase that hits a ceiling is reported with `category: "max_turns"` or
`"budget"`, and its stop reason appears in `usage.phases[]`.

## No configuration file

There is none, deliberately. The previous CLI read `.specs/config.toml` and
`~/.specs/config.toml`; both are ignored now, and nothing warns about them. A
tool a program drives takes its configuration from its flags and its
environment, where the caller can see it, rather than from a file two
directories up that changes what the same command does.

## Environment variables

| Variable | Purpose |
|---|---|
| `ANTHROPIC_API_KEY` and the other vendor keys | credentials; see the table above |
| `<VENDOR>_BASE_URL` | gateway, proxy or local server for that vendor |
| `AF_MODEL` | model tier or catalog spec for every phase; `AGENTKIT_MODEL` is a fallback |
| `AF_MODEL_VENDOR` | which tier table `SIMPLE`/`STANDARD`/`ADVANCED` resolve against |
| `AF_SPEC_DIR` | the spec root (`spec` and `impl`); `--specs-dir` wins |
| `GITHUB_TOKEN`, `GH_TOKEN` | GitHub credential. Reading a public issue needs none; every write does |
| `GITHUB_API_URL` | a GitHub Enterprise host; its host is then also accepted for the `origin` remote |
| `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK` | **refused**; see above |

## See also

- [Model Usage](model-usage.md) — what each phase sends and how a failure is repaired
- [Tool Reference](cli.md) — the tools, their flags, their JSON
- [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md) — why the pipeline runs on AgentKit
- [ADR 03](adr/03-rebuild-the-skills-as-tools.md) — why the tools are shaped this way
