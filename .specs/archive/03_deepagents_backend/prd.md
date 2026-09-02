---
spec_id: '03'
spec_name: deepagents_backend
title: Deep Agents Backend
status: draft
created_at: '2026-07-07T13:13:46.635223+00:00'
updated_at: '2026-07-07T13:22:13.509884+00:00'
owner: Michael Kuehl
source: interactive
schema_version: 1
tags:
- session
- backend
- deepagents
- langchain
- provider-agnostic
---
# Deep Agents Backend

## Intent

Add a `DeepAgentsBackend` adapter that wraps LangChain Deep Agents, enabling
agent-fox to run coding sessions with any LLM provider supported by LangChain
(OpenAI, Anthropic, Google, Ollama, Fireworks, Together, etc.) through the
`Backend` Protocol established in spec `02_backend_protocol`.

## Background

Spec `02_backend_protocol` introduced the `Backend` Protocol and
`create_backend()` factory. This spec implements the first alternative backend
using LangChain Deep Agents — a coding agent harness built on LangGraph that
provides built-in filesystem tools (read_file, write_file, edit_file, glob,
grep, execute), context management, and model-agnostic execution.

Deep Agents accepts provider-prefixed model strings (e.g.
`"anthropic:claude-sonnet-4-6"`, `"openai:gpt-5.5"`,
`"ollama:north-mini-code-1.0"`) making it the provider-agnostic workhorse.

## Problem

agent-fox currently supports only Claude models via the `ClaudeBackend`. Teams
wanting to use alternative LLM providers (OpenAI, Gemini, open-weight models
via Ollama) cannot do so without forking the session layer. The `Backend`
Protocol exists but has only one implementation.

## Goals

The following measurable criteria define a complete and correct implementation:

1. **Protocol conformance**: `isinstance(DeepAgentsBackend(), Backend)` passes
   — the adapter satisfies the `Backend` Protocol from spec `02`.
2. **Event mapping correctness**: Unit tests with mocked LangGraph events
   confirm that every supported LangGraph event type produces the correct
   canonical message type (`ToolUseMessage`, `AssistantMessage`,
   `ResultMessage`).
3. **Factory integration**: `create_backend("deepagents")` returns a
   `DeepAgentsBackend` instance without error.
4. **Backward compatibility**: All existing session-layer tests pass without
   modification — `ClaudeBackend` behavior is unaffected, and no changes are
   made to `ClaudeBackend`'s import path, module structure, or any shared
   utilities in `session/backends/__init__.py`. Existing configs using
   `backend: "claude"` continue to work unchanged.
5. **Containment**: The containment test passes with the `deepagents` SDK
   import isolated exclusively to `deepagents.py`.

> **Out of scope at launch**: provider integration tests against live LLM APIs,
> latency SLOs, and reliability SLOs. Unit tests with mocked Deep Agents are
> sufficient to declare the feature complete.

## Solution

1. **Create `session/backends/deepagents.py`** containing `DeepAgentsBackend`
   that implements the `Backend` Protocol.

2. **The adapter uses `create_deep_agent()`** from the `deepagents` package to
   create an agent with:
   - `model`: The provider-prefixed model string passed from config.
   - `system_prompt`: The system prompt assembled by the steering/context layer
     and passed into `execute()` via the `system_prompt` keyword argument (see
     step 4 for the full `execute()` signature).
   - `tools`: The five af SDK functions registered as LangChain `@tool`-decorated
     sync functions: `spec_read`, `context_search`, `context_get`,
     `memory_recall`, `subtask_state`. These are thin synchronous wrappers
     that call the existing af SDK Python functions in-process, avoiding MCP
     server overhead. The af SDK source is the authoritative definition of each
     function's parameters and return types — implementers must verify that all
     five functions carry proper Python type annotations before wrapping, since
     LangChain auto-generates JSON tool schemas from those annotations and
     docstrings. Any missing annotation will produce an incomplete or incorrect
     schema passed to the LLM.
   - `cwd`: The workspace path forwarded from `execute()`'s `cwd` parameter.
   - **`permissions` is omitted.** Permission enforcement is handled
     exclusively via the `permission_callback` / interrupt mechanism described
     in step 5. The af security allowlist is evaluated inside
     `permission_callback`, not pre-configured on the agent. The `permissions`
     parameter of `create_deep_agent()` is not used.

3. **Deep Agents built-in tools are used as-is.** Deep Agents provides
   `read_file`, `write_file`, `edit_file`, `glob`, `grep`, and `execute` —
   these serve as the coding tools for the session, analogous to Claude Code's
   Read, Edit, Write, Bash tools. They operate relative to the `cwd` path
   injected at agent creation.

4. **The `execute()` method** is an async generator matching the Backend
   Protocol from spec `02` exactly:

   ```python
   async def execute(
       self,
       prompt: str,
       *,
       system_prompt: str,
       model: str,
       cwd: str,
       permission_callback: PermissionCallback | None = None,
       **kwargs,
   ) -> AsyncIterator[AgentMessage]:
       ...
   ```

   Where `PermissionCallback` is the type alias from
   `session/backends/types.py`, identical to what `ClaudeBackend` uses:

   ```python
   PermissionCallback = Callable[[str, dict], bool]
   # permission_callback(tool_name: str, tool_input: dict) -> bool
   # True = approve, False = deny
   ```

   `execute()` creates a Deep Agent (passing `model`, `system_prompt`, `cwd`,
   and registered tools), invokes it with the task prompt, and streams
   LangGraph events via the `astream_events()` v2 API. Events are mapped to
   canonical message types and yielded as they arrive:

   - `on_tool_start` / `on_tool_end` → `ToolUseMessage`
   - `on_chat_model_stream` → `AssistantMessage` (text chunk)
   - `on_llm_end` → accumulates authoritative token usage (see step 8)
   - Stream completion → `ResultMessage` with aggregated token usage

   **Unexpected or malformed event shapes** (e.g. a missing field due to a
   `deepagents` version difference) are handled non-fatally: the adapter skips
   the event and logs a `WARNING`-level message. The session continues
   processing subsequent events. This avoids crashing the session due to minor
   schema drift across `deepagents` minor versions.

   > **Implementation note**: The exact field names within each event type may
   > vary across `deepagents` versions. The implementation must be empirically
   > verified against the installed version (`deepagents>=0.5`) rather than
   > hardcoded from documentation. Implementers should consult the
   > `deepagents>=0.5` source and LangGraph's `astream_events()` v2 schema
   > directly.

5. **Permission callback mapping**: The `permission_callback` (type:
   `PermissionCallback | None`) is mapped to Deep Agents' interrupt mechanism
   for human-in-the-loop tool approval.

   > **Implementation note**: The exact API names and call signatures for the
   > interrupt/resume mechanism (e.g. the parameter accepted by
   > `create_deep_agent()` to register an interrupt hook, and the method used
   > to resume or abort agent execution) are intentionally left to implementer
   > discovery against the installed `deepagents>=0.5` package source. Deep
   > Agents is a fast-moving library and these surface-level API names may
   > shift across minor versions. The PRD defines the **contract**, not the
   > library-specific call shape.

   The contract is:
   1. The adapter extracts `tool_name: str` and `tool_input: dict` from the
      interrupt event payload.
   2. It calls `permission_callback(tool_name, tool_input)` → `bool`.
   3. The boolean result (`True` = approve, `False` = deny) is forwarded to
      the agent's resume interface to continue or abort execution.

   If `permission_callback` is `None`, no interrupt hook is registered and all
   tool calls proceed without approval.

6. **Provider-specific features degrade gracefully** using `try/except TypeError`
   as the detection mechanism, mirroring `ClaudeBackend`'s existing pattern for
   `thinking`, `output_config`, and `context_management` parameters:
   - `thinking` parameter: Applied only for Anthropic models (detected from
     model string prefix). Ignored silently for other providers.
   - `compaction`: Not used — Deep Agents has its own context management
     (automatic summarization, large-result eviction).
   - `max_budget_usd`: Passed to `create_deep_agent()` inside a `try/except
     TypeError`; if the installed version does not accept this parameter, the
     call is retried without it and a `DEBUG`-level message is logged.
   - `effort`: Passed to `create_deep_agent()` inside a `try/except TypeError`;
     if unsupported, the call is retried without it and a `DEBUG`-level message
     is logged.

7. **Error handling**: Mirrors `ClaudeBackend`'s `_MAX_TRANSPORT_RETRIES`
   pattern, sharing the same retry constants (extracted to a shared utility in
   the backends package if not already present):
   - **Retry constants**: `_BACKOFF_BASE = 1.0`; delay for attempt *n*
     (1-indexed) = `_BACKOFF_BASE * 2^(n-1)`, giving delays of **1 s, 2 s,
     4 s** across up to **3 attempts**. No jitter is applied.
   - Transient errors (connection failures, rate limits / HTTP 429, provider
     5xx) are caught during streaming and retried with this exponential backoff
     schedule.
   - If all retries are exhausted, a terminal `ResultMessage` with
     `is_error=True` and `is_transport_error=True` is yielded and the generator
     exits cleanly.
   - Non-transient errors (e.g. authentication failures / HTTP 401, tool
     execution errors) are yielded immediately as a terminal `ResultMessage`
     with `is_error=True` (and `is_transport_error=False`) without retry.
   - No exceptions propagate out of `execute()` — all failure modes surface
     through the canonical `ResultMessage` error fields, consistent with the
     Backend Protocol contract.

   > **`ResultMessage` reference**: `ResultMessage` and its `is_error` /
   > `is_transport_error` fields are defined in `session/backends/types.py`.
   > Implementers must cross-reference that module for the full type definition.

8. **Token usage**: Token counts are extracted exclusively from `on_llm_end`
   events, which provide the authoritative cumulative usage after each model
   call completes. Per-chunk partial counts from `on_chat_model_stream` are
   ignored to avoid double-counting. Token counts are accumulated across all
   `on_llm_end` events in the stream and reported in the terminal
   `ResultMessage`. If a provider does not return token counts, the
   corresponding fields in `ResultMessage` are set to `None` (not zero),
   signalling that usage data is unavailable rather than that no tokens were
   consumed.

9. **`close()` method contract**: `close()` is idempotent and safe to call
   multiple times. It is a no-op if `execute()` is not running. It releases
   any per-instance LangGraph state (e.g. checkpoint references) but does not
   manage the `execute()` lifecycle or perform async task cancellation.
   Mid-stream teardown is handled at the `session.py` level via
   `asyncio.wait_for()` / async iterator cancellation — not inside `close()`.

10. **Register the backend** in `create_backend()` factory: `"deepagents"` →
    lazy import of `DeepAgentsBackend`.

11. **Widen the config `Literal` type** in `OrchestratorConfig.backend` (in
    `core/config.py`) from `Literal["claude"]` to
    `Literal["claude", "deepagents"]`. No migration or validation step is
    required — existing configs with `backend: "claude"` continue to work
    unchanged, as `"claude"` remains a valid value.

12. **Add `deepagents` as an optional dependency** in `pyproject.toml`:

    ```toml
    [project.optional-dependencies]
    deepagents = ["deepagents>=0.5"]
    ```

    All `DeepAgentsBackend` tests are guarded with
    `pytest.importorskip("deepagents")` so they are automatically skipped in
    environments where the extra is absent.

    **CI coverage**: A dedicated matrix leg must be added to the existing test
    workflow that installs the extra via `pip install '.[deepagents]'` before
    running the test suite. The specific workflow file name and matrix
    configuration are determined during implementation, but the leg must exist
    to ensure `DeepAgentsBackend` tests are not silently skipped across all CI
    runs. Without this leg, all guarded tests pass by skipping and the feature
    has zero CI coverage.

13. **Update the containment test** to add `"deepagents"` → `"deepagents.py"`
    to the SDK-to-file mapping.

## Non-Goals

- Building custom coding tools — Deep Agents provides its own.
- Modifying the canonical message types in `types.py`.
- Changing `core/client.py` or `agentspec`.
- Supporting Deep Agents' subagent/delegation features — the session layer
  manages orchestration, not the backend.
- Supporting Deep Agents' built-in memory — agent-fox has its own knowledge
  and context systems.
- Provider integration tests against live LLM APIs at launch.
- Latency or reliability SLOs.
- Using `create_deep_agent()`'s `permissions` parameter — permission enforcement
  is delegated entirely to the `permission_callback` / interrupt mechanism.

## Tech Stack

- Python 3.12+
- `deepagents>=0.5` (LangChain Deep Agents, optional extra)
- `langchain-core` (transitive, for tool abstractions)
- `langgraph` (transitive, for streaming events via `astream_events()` v2)
- pytest + pytest-asyncio for tests

## Design Decisions

1. **Model string is passed through unmodified.** The `model` parameter from
   config (e.g. `"openai:gpt-5.5"`) is passed directly to
   `create_deep_agent(model=...)`. No translation layer — Deep Agents' own
   model resolution handles provider routing.

2. **af SDK tools as sync LangChain tools.** The five af SDK functions
   (`spec_read`, `context_search`, `context_get`, `memory_recall`,
   `subtask_state`) are wrapped as LangChain `@tool`-decorated synchronous
   functions. They are thin wrappers calling existing af SDK Python functions
   in-process. Async wrapping is not used — these functions are already defined
   in the codebase and perform in-process calls without I/O blocking concerns
   at the LangChain tool boundary. The af SDK source is the authoritative
   definition; implementers must verify that all five functions carry proper
   Python type annotations before wrapping.

3. **Deep Agents' filesystem tools are NOT excluded.** Unlike `ClaudeBackend`
   (which wraps Claude Code with its own complete tool set), `DeepAgentsBackend`
   lets Deep Agents provide the coding tools natively. These tools operate on
   the `cwd` workspace directory injected at agent creation.

4. **`cwd` is injected via `execute()` keyword argument.** The workspace path
   follows the Backend Protocol signature — `session.py` passes
   `str(workspace.path)` as `cwd` to `execute()`, and the adapter forwards it
   to `create_deep_agent()` so filesystem tools operate on the correct
   directory.

5. **Interrupt/resume API is discovered empirically.** Deep Agents is a young,
   fast-moving library. The exact method names and call signatures for the
   interrupt/resume mechanism are intentionally left to implementer discovery
   against the installed `deepagents>=0.5` source. The PRD defines the
   behavioral contract — `permission_callback(tool_name, tool_input) → bool`
   forwarded to the agent — not the library API shape.

6. **Event schema is empirically verified, not hardcoded.** LangGraph's
   `astream_events()` v2 API drives the event mapping. Because field names may
   shift across `deepagents` minor versions, the mapping logic must be verified
   against the installed package version. Unexpected event shapes are skipped
   with a `WARNING` log rather than raising an exception.

7. **Token usage exclusively from `on_llm_end`.** `on_llm_end` provides the
   authoritative, cumulative token count after each model call. Per-chunk
   counts from `on_chat_model_stream` are partial and are ignored to prevent
   double-counting. Fields are `None` (not zero) when a provider omits usage
   data.

8. **No subprocess management.** Unlike `ClaudeBackend` (which spawns a Claude
   Code subprocess), Deep Agents runs in-process as a Python library. The
   `close()` method releases per-instance LangGraph state (checkpointer,
   thread state) but does not perform async task cancellation — that is
   delegated to `session.py` via `asyncio.wait_for()`.

9. **Single-use instance model.** One `DeepAgentsBackend` instance is created
   per session and discarded after `execute()` completes, matching
   `ClaudeBackend`'s lifecycle. `create_backend()` returns a new instance each
   time, and `session.py` creates a new backend per `run_session()` call.
   Concurrent `execute()` calls on the same instance are not supported and not
   required.

10. **Error handling and retry constants mirror `ClaudeBackend`.** Transport
    retries (up to 3, exponential backoff with `_BACKOFF_BASE = 1.0`, delays
    of 1 s / 2 s / 4 s, no jitter) and terminal `ResultMessage` error fields
    are used consistently, ensuring the session layer can handle
    `DeepAgentsBackend` errors identically to `ClaudeBackend` errors. If
    `ClaudeBackend` already defines these constants, they should be extracted
    to a shared utility in `session/backends/` rather than duplicated.

11. **Optional-parameter fallback via `try/except TypeError`.** For parameters
    that may not be supported by all `deepagents` versions (`max_budget_usd`,
    `effort`) or all providers (`thinking`), the adapter uses `try/except
    TypeError` — the same pattern `ClaudeBackend` applies to `thinking`,
    `output_config`, and `context_management`. This is simpler than
    `inspect.signature()` and consistent with the existing codebase style.

12. **`permissions` parameter omitted from `create_deep_agent()`.** Permission
    enforcement is handled entirely via the `permission_callback` / interrupt
    mechanism. The af security allowlist is evaluated inside
    `permission_callback`, not pre-configured on the agent.

13. **Backward compatibility is fully preserved.** Adding `DeepAgentsBackend`
    does not touch `ClaudeBackend`'s import path, module structure, or any
    shared utilities in `session/backends/__init__.py`. The config `Literal`
    widening is additive — existing `backend: "claude"` configs remain valid.

## Testing Strategy

All tests are **unit tests** — no live LLM API calls are made in CI.
All `DeepAgentsBackend` tests are guarded with
`pytest.importorskip("deepagents")` to skip automatically when the optional
extra is not installed. A dedicated CI matrix leg must install
`pip install '.[deepagents]'` to ensure these tests actually execute.

| Test case | Approach |
|-----------|----------|
| `isinstance(DeepAgentsBackend(), Backend)` | Direct Protocol check |
| `create_backend("deepagents")` returns correct type | Factory unit test |
| `on_tool_start` / `on_tool_end` → `ToolUseMessage` | Mocked synthetic event stream |
| `on_chat_model_stream` → `AssistantMessage` | Mocked synthetic event stream |
| `on_llm_end` → token usage accumulated into terminal `ResultMessage` | Mocked synthetic event stream |
| Token usage `None` when provider omits counts | Mocked event stream with no usage fields |
| Unexpected/malformed event shape → skipped, WARNING logged | Mocked event with missing fields |
| Transient error retries up to 3× (delays 1 s, 2 s, 4 s) then `ResultMessage(is_error=True, is_transport_error=True)` | Mock raising transient exception |
| Non-transient error yields `ResultMessage(is_error=True, is_transport_error=False)` immediately | Mock raising auth exception |
| `permission_callback(tool_name, tool_input)` invoked on interrupt; `True`/`False` forwarded to resume interface | Mocked interrupt trigger |
| `permission_callback=None` → no interrupt hook registered | Mocked agent creation |
| `thinking` applied only for Anthropic prefix, ignored otherwise | Parameterised unit test |
| `max_budget_usd` / `effort` passed through; `TypeError` triggers silent retry without param + DEBUG log | `try/except TypeError` unit test |
| `close()` is idempotent; releases checkpoint state; no-op when not running | Direct unit test |
| `cwd` forwarded correctly to `create_deep_agent()` | Constructor/factory unit test |
| Five af SDK tools registered as sync `@tool` functions with correct type annotations | Tool registration unit test |
| `permissions` parameter NOT passed to `create_deep_agent()` | Agent creation unit test |
| Containment: `deepagents` import only in `deepagents.py` | Existing containment test extended |
| All existing session tests pass unmodified | Backward-compatibility regression |

Mock strategy: `create_deep_agent()` is patched at the module boundary;
synthetic `astream_events()` async generators are injected to exercise each
event-mapping and error-handling code path without touching the real
`deepagents` or any LLM API.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 02_backend_protocol | 1 | 1 | Backend Protocol and create_backend() factory |
