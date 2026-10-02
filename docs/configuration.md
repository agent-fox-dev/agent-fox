# Configuration

The tools — `spec`, `issue`, `fix` and `impl` — reach a model through
[AgentKit](https://github.com/agent-fox-dev/agentkit-go)
(module `github.com/agentfox/agentkit-go`), so it speaks every wire API that
library implements: Anthropic, both OpenAI wires, Google and Ollama, plus the
OpenAI-compatible gateways (OpenRouter, DeepSeek, Groq, xAI, Together,
Moonshot). This document covers credentials, model selection, what the model
is allowed to read, and why there is no config file.

## Quick start

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
export GITHUB_TOKEN="ghp_..."          # only for the tools that write; GITLAB_TOKEN for GitLab
```

There is no configuration file. With a key in the environment the tools use the
`STANDARD` tier, which resolves to `claude-sonnet-5-5` at high thinking effort.

Every run reports the model it resolved in its JSON envelope, so what actually
served a run is a field rather than something to work out:

```jsonc
"model": { "spec": "STANDARD", "id": "claude-sonnet-5-5", "vendor": "anthropic",
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
| `ollama` | `OLLAMA_API_KEY` (usually unset; Ollama is always `ambient`) | `Authorization: Bearer` |
| `openrouter`, `deepseek`, `xai`, `groq`, `together`, `moonshot` | `<VENDOR>_API_KEY` | `Authorization: Bearer` |
| anything else on an OpenAI wire | `<VENDOR>_API_KEY` | `Authorization: Bearer` |

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
| `<VENDOR>_BASE_URL` | the same for the OpenAI-compatible gateways (`OPENROUTER_BASE_URL`, …) |
| `GOOGLE_GEMINI_BASE_URL` | Gemini on Vertex AI, or a proxy |
| `OLLAMA_HOST` | your Ollama server (default `http://localhost:11434`) |

### Claude on Vertex AI

Claude on Vertex AI is served by the agent library from the same Anthropic
wire, and is selected by the same variables Claude Code uses:

| Variable | Effect |
|---|---|
| `CLAUDE_CODE_USE_VERTEX` | selects the Vertex deployment. Read for truth: `0` or `false` is an explicit off |
| `ANTHROPIC_VERTEX_PROJECT_ID` | the GCP project. On its own it selects the deployment only when no Anthropic key is set |
| `CLOUD_ML_REGION` | the Vertex location; `global` when unset |
| `ANTHROPIC_VERTEX_BASE_URL` | a proxy in front of Vertex; beats `ANTHROPIC_BASE_URL` while Vertex is on |
| `GOOGLE_CLOUD_PROJECT`, `CLOUDSDK_CORE_PROJECT` | may supply the project once Vertex is selected; they never select it |

Vertex authenticates with a Google OAuth access token. agent-fox mints it from
Application Default Credentials, as Claude Code does, and refreshes it before
it expires, so a machine on which Claude Code works on Vertex needs nothing
more:

```bash
gcloud auth application-default login   # once; or set GOOGLE_APPLICATION_CREDENTIALS
export CLAUDE_CODE_USE_VERTEX=1 ANTHROPIC_VERTEX_PROJECT_ID=my-project CLOUD_ML_REGION=us-east5
```

A token in `ANTHROPIC_AUTH_TOKEN` (or `ANTHROPIC_OAUTH_TOKEN`) takes precedence
and is sent as is; ADC is not consulted then. With neither, the preflight fails
with an `auth` error naming `gcloud auth application-default login`. A leftover
`ANTHROPIC_API_KEY` is dropped rather than sent to Google. Vertex names models
with a dated suffix, which `--model` accepts as is:
`--model anthropic/claude-sonnet-5@20260401`.

### AWS Bedrock

`CLAUDE_CODE_USE_BEDROCK` **is refused**, with an error naming what to do
instead. The agent library has no Bedrock implementation, and honouring the
variable would send the request to `api.anthropic.com` with credentials meant
for somewhere else. A Bedrock deployment is usually fronted by an
Anthropic-compatible gateway: point `ANTHROPIC_BASE_URL` at it and set a token
there. See
[`docs/errata/agentkit_model_resolution.md`](errata/agentkit_model_resolution.md).

## Model selection

### Tiers

Three named tiers are what an operator chooses between, so that running a tool
does not require knowing which model id is current this month.

| Tier | `anthropic` (default) | `openai` | `google` |
|---|---|---|---|
| `SIMPLE` | `claude-sonnet-5-5` · thinking `medium` | `gpt-5.6-luna` | `gemini-3.5-flash-lite` |
| `STANDARD` | `claude-sonnet-5-5` · thinking `high` | `gpt-5.6-terra` | `gemini-3.8-flash` |
| `ADVANCED` | `claude-opus-5-5` · thinking `xhigh` | `gpt-6-astra` | `gemini-3.1-pro-preview` |

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

### Which vendor wins when several keys are set

**Your environment never picks the vendor.** The tools do not scan for keys
and choose whichever they find. With `ANTHROPIC_API_KEY` and `GEMINI_API_KEY`
(or `GOOGLE_API_KEY`, `OPENAI_API_KEY`, …) all exported, a tier name still
resolves against the `anthropic` table, because `anthropic` is the default
vendor. The vendor is decided in this order:

1. A model named as `vendor/model-id` (`--model google/gemini-3.8-flash`) uses
   that vendor.
2. A tier name (`STANDARD`, …) uses `--vendor`, then `$AF_MODEL_VENDOR`, then
   the default, `anthropic`.
3. A bare model id is looked up in the catalog and uses the vendor that owns it.

Only after that is the credential for the **resolved** vendor checked. Keys for
other vendors are ignored. If the resolved vendor has no key, the run fails
before any prompt is built. It does not fall back to another vendor whose key
is set.

To use Gemini with both keys exported:

```sh
spec --vendor google ./idea.md                 # tier table of google
export AF_MODEL_VENDOR=google                  # the same, for the shell
spec --model google/gemini-3.8-flash ./idea.md # or name the model
```

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
phase that only reads — `issue`'s triage, every `spec` phase, `impl`'s survey
— the mutating file tools (`write_file`, `edit_file`) are excluded from the
resolved set, an invariant checks that set before the first request, and
AgentKit's own unguarded-shell guard fails any run where an unguarded shell
survived. `fetch_url` is never registered by any tool, so no phase has
outbound network of any kind. `fix`'s analysis and `impl`'s survey keep an
`execute` tool, but under a guard that allows only reporting programs (`git`,
`ls`, `cat`, `rg`, …) and refuses pipes, redirection and every mutating `git`
subcommand.

The phases that write — `fix`'s implementation phase, and `impl`'s
implementation and repair phases — run under a second guard: `git` is limited
to its read-only subcommands, `gh` is refused outright, `find -exec`/`-delete`
are refused, and `impl` additionally refuses every write under the spec
package it is implementing. See the
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
number of tasks. `--total-budget`, a shared flag, caps the whole run; zero
(the default) means no ceiling beyond `--budget`. `impl` stops between tasks
with everything landed so far committed on the branch; `fix` checks between
its analyse and implement phases; `spec` checks before each scope's PRD phase
after the first, leaving the split plan in place to resume from; `issue`, and
a `spec` input that is not split, have one phase, so the lower of
`--total-budget` and `--budget` is that phase's ceiling. Its `--repair` phase, when it runs, is bounded like any other phase and
counts toward the total; `--repair-model` changes its model and nothing
else about its bounds.

The turn budget is also the repair budget: a validation failure returns to the
model as a tool error and costs a turn, so a model that cannot satisfy a rule
stops costing money rather than looping until the context window ends the run.

The stop policy is turns and budget only. It deliberately does not end a run
when the terminating tool is *called*: a call the handler rejects is the repair
loop's first step, and the intended ending is the handler accepting one.

A phase that hits a ceiling is reported with `category: "max_turns"` or
`"budget"`, and its stop reason appears in `usage.phases[]`.

## State directory

The state directory is `$XDG_STATE_HOME/agent-fox` when `XDG_STATE_HOME` is
non-empty, else `~/.local/state/agent-fox` via `os.UserHomeDir`. It holds two
subdirectories:

- **`runs/`** — report files. Every run writes the complete envelope — the
  `full` view of `result`, whatever `--detail` was given — to
  `<tool>-<started_at>-<session_id>.json`, where `<started_at>` is the run's
  start in a colon-free form (`20260909T132030Z`) and `<session_id>` is the
  run's 32-character hex identifier. The directory is created with mode 0755
  and the file with mode 0644. `--report-file <path>` overrides the computed
  path. A run that cannot write the file records a `low`
  `report_file_not_written` warning and omits `report_file`; it does not fail.

- **`events/`** — event streams. Every run that reaches the event sink writes
  its complete JSONL event stream to
  `<tool>-<started_at>-<session_id>.jsonl`, with the same stem as the report
  file. The directory is created with mode 0700 and the file with mode 0600.
  Each event is one whole-line write, so a killed process leaves a valid
  prefix. A run that cannot create the file records a `low`
  `events_file_not_written` warning; it does not fail.

The two files are joined by `session_id`: the envelope carries it as a
top-level field, and every event carries it as the third key of its header.
`jq` and `ls` are enough to pair an events file with its report.

`--dry-run` runs still write both files, since they are local state. Nothing
prunes either directory. Tests set `XDG_STATE_HOME` to a temporary directory
so that a test run never writes to a developer's real state.

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
| `AF_LAND` | the default for `--land` (`fix` and `impl`): `pr`, `branch` or `none`; `AF_LAND=none` keeps every run on the machine. The flag wins; an invalid value is a usage error |
| `AF_BRANCH_PREFIX` | the default for `fix --branch-prefix`, e.g. `feature`; the flag wins |
| `XDG_STATE_HOME` | where report and events files go (`$XDG_STATE_HOME/agent-fox/runs` and `events/`); `~/.local/state` when unset; `--report-file` overrides the report path only. Tests set it to a temporary directory |
| `GITHUB_TOKEN`, `GH_TOKEN` | GitHub credential. Reading a public issue needs none; every write does |
| `GITHUB_API_URL` | a GitHub Enterprise host; its host is then also accepted for the `origin` remote and for issue URLs |
| `GITLAB_TOKEN` | GitLab credential, on the same terms |
| `GITLAB_API_URL` | a self-hosted GitLab host; its host is then also accepted for the `origin` remote and for issue URLs |
| `CLAUDE_CODE_USE_VERTEX` | selects Claude on Vertex AI; see above |
| `CLAUDE_CODE_USE_BEDROCK` | **refused**; see above |

## See also

- [Model Usage](model-usage.md) — what each phase sends and how a failure is repaired
- [Tool Reference](cli.md) — the tools, their flags, their JSON
- [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md) — why the pipeline runs on AgentKit
- [ADR 03](adr/03-rebuild-the-skills-as-tools.md) — why the tools are shaped this way
