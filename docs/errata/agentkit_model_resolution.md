# Erratum: model resolution and platform selection under AgentKit

Project-wide, so no numeric prefix. Recorded because each item below is a
behaviour an operator could reasonably expect from the previous
implementation and will not get from this one.

## 1. `CLAUDE_CODE_USE_VERTEX` and `CLAUDE_CODE_USE_BEDROCK` are refused

**Was:** `detectPlatform()` read these variables and selected a Vertex or
Bedrock client from the Anthropic SDK.

**Is:** either variable set fails the run before a model is resolved, with a
message naming the variable, what it used to select, and what to do instead.

**Why:** AgentKit talks to vendors over their own wire APIs. Its Vertex
support is Gemini on the Google wire (`google.Options.VertexProject`, or a
Vertex base URL); there is no Bedrock implementation and no
`anthropic-vertex` wire. Honouring the variable would have sent the request to
`api.anthropic.com` with credentials meant for a managed deployment, and the
run would have failed with an authentication error naming neither the variable
that was set nor the reason it did nothing.

**The way through:** both deployments are commonly fronted by an
Anthropic-compatible gateway. Point `ANTHROPIC_BASE_URL` at it and set a token
there. A base URL alone resolves to the `ambient` credential state and passes
the preflight, which is what a gateway that authenticates by URL needs.

## 2. `claude-opus-4-6[1m]` is no longer what `model_variant = "extended"`
means

**Was:** the `extended` variant of the `ADVANCED` tier resolved to the string
`claude-opus-4-6[1m]`, held in a local registry of four model ids.

**Is:** it resolves to `anthropic/claude-fable-5-1`, a catalog row with a
1 000 000-token context window.

**Why:** `claude-opus-4-6[1m]` is not a catalog row. Under REQ-CAT-03 an
unknown id beneath a known vendor *clones the vendor's default row*, so the
resolution succeeds and the model carries `claude-sonnet-5`'s price and
context window. Every downstream consumer of those numbers — the budget stop
policy, the `max_tokens` clamp, the cost report — would then have been quietly
wrong, and the error would have surfaced in a bill rather than in a log.
`TestEveryTierOfEveryVendorResolvesInTheCatalog` fails any tier entry that
resolves to a clone, so this cannot be reintroduced by editing the table.

The literal string still resolves, by cloning, if it is passed as a model id.
It is no longer what a tier resolves to.

## 3. Prompt-cache policy is no longer configurable here

**Was:** a `CachePolicy` of `none`, `default` or `extended`, with
`cache_control` injected when the system prompt passed a per-model token
threshold (2048 for Sonnet, 4096 otherwise), and a retry without caching if a
provider rejected the metadata.

**Is:** the provider owns the cache prefix. AgentKit stamps breakpoints over
the tool schemas and the assembled system prompt, keeps a late-added tool
*after* the prefix so its arrival does not invalidate the whole transcript,
and reports what a cache hit actually cost through `CacheMeter`.

**Why:** the estimate the threshold was computed from (`len(text)/4`) is
strictly worse than what the layer that builds the request body knows, and the
policy could only ever apply to the system prompt — not to the tool schemas,
which on a generation call are the largest stable thing in the request.

`config.toml` itself is gone with the CLI that read it: the tools take their
configuration from flags and the environment only (see
[Configuration](../configuration.md#no-configuration-file)), so
`[provider] auth_method`, `vertex_project` and `vertex_region` are no longer
read at all.

## 4. A tool cannot be forced

**Was:** `ToolChoice: {"type": "any"}` on every phase.

**Is:** nothing. AgentKit's `core.ToolChoice` is unset/auto/none — the
tri-state that is expressible on every wire it speaks.

**Consequence:** a model can end a phase by answering in prose. Every phase
declares its terminating tool and a prompt guideline says to call it; when a
run ends without a submission the error names the `RunStopReason`, because
"answered in prose" (`end_turn`), "kept failing validation" (`max_turns`) and
"blew the cost cap" (`budget_exceeded`) want three different responses from
the operator.
