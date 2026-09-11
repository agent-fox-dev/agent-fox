# 02. Build the spec pipeline on AgentKit

**Status:** accepted
**Date:** 2026-09-09
**Supersedes:** nothing
**Related:** [`docs/errata/agentkit_model_resolution.md`](../errata/agentkit_model_resolution.md)

## Context

`agentspec` owned an LLM layer of its own: an Anthropic-shaped vocabulary of
messages, content blocks, system blocks and tools; a `Doer` interface; a retry
ladder; `cache_control` injection with per-model token thresholds; platform
detection for Vertex and Bedrock; a registry of four Claude model ids; and a
repair loop that assembled a second conversation by hand after a validation
failure.

None of it worked. `AICall` ended at

> no Doer or ClientFactory provided and real SDK client creation is not yet
> implemented

so `spec refine` and `spec generate` could not reach a model outside a test.
Every test injected a function in place of `AICall` and asserted on the
arguments it was handed, which could only ever check that the package had
built the arguments it meant to build.

Meanwhile [`coder`](https://github.com/agent-fox-dev/coder) — the AgentKit Go
SDK, module path `github.com/agentfox/agentkit-go` — ships the whole of that
layer, mutation-verified: the loop, five wire APIs across three vendors plus
the OpenAI-compatible gateways, a model catalog with cost and context and
thinking-level metadata, send-time transcript repair, credential resolution
with OAuth refresh, workspace-contained tools, skills, sessions, compaction,
stop policies and budget caps.

The question was not whether to use it. It was how much of `agentspec` should
survive contact with it.

## Decision

Everything below the prompts becomes AgentKit. What stays is what is
genuinely about specs: the prompt templates, the session state machine, the
format v2 rules, and the sequence the three artifacts are generated in.

### 1. A phase is an agent, not a call

`SpecAgent` holds configuration, not a connection. Each phase — assess,
refine, and one per generation step — builds its own `agentkit.Agent` with its
own model, tool set, turn budget and cost cap.

They are separate agents rather than one conversation because sharing a
transcript would carry the PRD critique into the requirements the critique was
supposed to improve.

### 2. The result is a tool call, not a parsed response

`submit_assessment`, `submit_prd_update` and `submit_{artifact}` are
`core.Tool` values with a `*schema.Schema`, a typed `Execute` handler, and
`Terminate: true` on acceptance. The handler decodes and validates; the value
it writes is the phase's result. Nothing parses a response afterwards, and
nothing inspects a `stop_reason` string.

### 3. The repair loop is the loop

This is the change with the largest blast radius and the smallest diff.

The old pipeline called the model, pulled the `tool_use` block out of the
response, validated it, and on a failure built a three-message conversation —
the original user prompt, the model's assistant message, a synthetic
`tool_result` carrying the error — and sent it again, up to three times.

Now `afspec.ValidateGenerationStep` runs *inside* the submit handler and a
violation is returned as `core.ErrResult`. The loop appends it to the
transcript the model is already holding and asks again. That is the same
conversation the hand-assembled one was imitating, with two differences that
matter:

- The system prompt and the tool schemas are byte-identical across the
  attempt and its correction, so the provider's cache prefix survives the
  repair instead of being rebuilt each time.
- "How many attempts does the model get" and "how many turns may this phase
  take" became the same number. `maxRepairs` is gone; `MaxTurns` bounds it.

### 4. Tiers stay, as aliases over the catalog

`SIMPLE`/`STANDARD`/`ADVANCED` is what a spec author actually chooses
between. The alternative is asking whoever writes a PRD to know which model id
is current this month.

So the tiers remain — as an alias table, per vendor, resolving to a catalog
spec that `catalog.ResolveModel` then supplies the wire API, context window,
output cap, price and thinking levels for. Anything that is not a tier name is
handed to the catalog unchanged, so `anthropic/claude-opus-4-6`, a bare
unambiguous id, and a model released after this build was cut all work with no
code change.

All five first-party wire APIs are registered. Resolving `openai/gpt-6-astra`
and then refusing to serve it would be a failure two layers from its cause.

### 5. The spec agent may read the code it describes

This is the one capability added rather than moved.

Writing a spec against a PRD alone is why the `af-spec` skill has a whole step
called "learn the context", performed by an outer agent pasting text into the
prompt. With a workspace configured (`spec generate --read-source`), the model
gets AgentKit's non-mutating built-in tools rooted at `--source` and reads the
repository itself.

The read-only mandate is a mechanism, not a sentence in a prompt: the mutating
tools are excluded from the resolved set, an invariant checks the resolved set
before the first request, and `BeforeToolCall` is left nil so AgentKit's own
`ErrUnguardedExecute` guard fails any run where a shell survived. It is off by
default — reading a repository costs turns and tokens, and that is the
operator's decision rather than a default to discover in a bill.

`--trust-project` is the separate grant that admits repository-authored
skills and context files (`AGENTS.md`, `CLAUDE.md`, `.specs/steering.md`) into
the system prompt, under AgentKit's trust gate. Until now nothing in this
pipeline read `.specs/steering.md`, which exists to be read.

### 6. Tests script a provider

`provider/faux` sits where a vendor sits, so a test drives the real loop with
no key, no network and no mock of this package's own types — and asserts on
the `core.Request` values that reached the wire.

## Consequences

**A bug is fixed that no test could see.** The tool schemas were built by
walking the JSON Schema as `map[string]any` and deleting keys by name at every
depth — including inside `properties`, whose keys are property *names*. The v2
test object has properties called `title` and `then`. Both were deleted while
`required` still listed them and `additionalProperties` stayed `false`, so the
schema declared for `submit_test_spec` could not be satisfied by any argument
object. The smoke test that should have caught this asserted that no key
called `title` appeared at any depth: it passed because the bug had removed
the evidence. The replacement converter is position-aware and decodes through
`jsonx`, which also keeps property order out of Go's unconditional map-key
sort — order is model-visible and part of the cache prefix.

**Two capabilities are lost, loudly.** `CLAUDE_CODE_USE_VERTEX` and
`CLAUDE_CODE_USE_BEDROCK` selected managed deployments of Claude through the
Anthropic SDK. AgentKit has no wire for either. They now fail with a message
naming the way through rather than being ignored, because a silent change of
destination is the one outcome an operator cannot debug. See the erratum.

**`ToolChoice` cannot force a tool.** AgentKit's tri-state is
unset/auto/none — there is no "any", which was an Anthropic-wire spelling. A
phase with no workspace declares exactly one tool and says so in its prompt
guidelines; a run that ends without a submission returns an error naming the
stop reason, which distinguishes "answered in prose" from "ran out of turns"
from "blew the cost cap".

**The dependency needs a sibling checkout.** `coder`'s module path is
`github.com/agentfox/agentkit-go` and its repository is
`github.com/agent-fox-dev/coder`, so the module proxy cannot serve it. It is
consumed through `replace github.com/agentfox/agentkit-go => ../agentkit-go`
(a checkout named after the module), which is the same arrangement `coder`'s
own `examples/flatline` uses for the spec library. Fixing this means changing
the module path upstream and is out of scope here.

**Roughly 1 000 lines of production code and 2 000 lines of test leave the
repository**, and what replaces them is tested against four wire APIs and a
conformance suite this repository does not have to maintain.
