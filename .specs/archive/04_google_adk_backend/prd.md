---
spec_id: '04'
spec_name: google_adk_backend
title: Google ADK Backend
status: draft
created_at: '2026-07-07T13:14:06.817457+00:00'
updated_at: '2026-07-07T13:32:21.318583+00:00'
owner: Michael Kuehl
source: interactive
schema_version: 1
tags:
- session
- backend
- google-adk
- gemini
---
# Google ADK Backend

## Intent

Add a `GoogleADKBackend` adapter that wraps Google's Agent Development Kit
(ADK), enabling agent-fox to run coding sessions with Gemini models natively
and other providers via ADK's LiteLLM integration, through the `Backend`
Protocol established in spec `02_backend_protocol`.

## Background

Spec `02_backend_protocol` introduced the `Backend` Protocol and
`create_backend()` factory. Spec `03_deepagents_backend` adds the first
alternative backend via LangChain Deep Agents. This spec adds a second
alternative using Google ADK — an open-source agent framework with native
Gemini support, multi-agent workflows, and a rich tool ecosystem.

Unlike Claude SDK and Deep Agents, Google ADK does **not** come with built-in
coding tools (file read/write, shell execution, etc.). This spec must provide
a coding tool layer as ADK function tools.

## Problem

agent-fox has no native integration with Google's Gemini model family via
ADK. While Deep Agents supports Gemini through LangChain's ChatGoogleGenerativeAI,
ADK provides deeper Gemini integration: native streaming, Gemini Live API,
and Google Cloud authentication. Teams already on Google Cloud infrastructure
benefit from ADK's first-party support.

## Goals

The following measurable criteria define a successful implementation:

1. `isinstance(GoogleADKBackend(), Backend)` passes — the adapter fully
   satisfies the `Backend` Protocol.
2. All six coding tools in `adk_tools.py` pass unit tests covering the happy
   path and path-containment security.
3. Event mapping produces correct canonical message types for all ADK event
   types, verified by unit tests using mocked ADK event streams.
4. `create_backend("google-adk")` returns a `GoogleADKBackend` instance.
5. All existing session tests pass without modification.
6. The containment test passes with `google.adk` isolated to `google_adk.py`.

No provider integration tests (against live Gemini endpoints) are required
at launch.

## Solution

1. **Create `session/backends/google_adk.py`** containing `GoogleADKBackend`
   that implements the `Backend` Protocol.

2. **Create a coding tools module** at `session/backends/adk_tools.py` that
   provides coding tools as ADK function tools:
   - `read_file(path: str) -> dict` — read file contents
   - `write_file(path: str, content: str) -> dict` — write/create files
   - `edit_file(path: str, old_text: str, new_text: str) -> dict` — find and
     replace in files
   - `execute(command: str) -> dict` — run shell commands
   - `list_files(path: str) -> dict` — list directory contents
   - `search_files(pattern: str, path: str) -> dict` — grep for patterns

   All tools operate relative to the `cwd` workspace directory and enforce
   path containment (no escaping the workspace root).

3. **The adapter uses ADK's `Agent` and `Runner`:**
   ```python
   from google.adk.agents import Agent
   from google.adk.runners import Runner
   from google.adk.sessions import InMemorySessionService
   ```
   - `Agent(model=model, name="coder", instruction=system_prompt,
     tools=[...coding_tools, ...af_sdk_tools])`
   - `Runner(agent=agent, session_service=InMemorySessionService())`
   - `runner.run_async(session_id, user_id, new_message)` → stream events

4. **Session and user ID lifecycle.** At the start of each `execute()` call,
   the session is initialised using the standard ADK pattern:

   ```python
   session_service = InMemorySessionService()
   session = await session_service.create_session(
       app_name="agent-fox",
       user_id=str(uuid4()),
   )
   runner = Runner(agent=agent, session_service=session_service)
   # Then:
   runner.run_async(
       session_id=session.id,
       user_id=session.user_id,
       new_message=...,
   )
   ```

   A fresh `InMemorySessionService` is created for each `execute()` call. The
   `session_id` is provided by the service's `create_session()` return value;
   only `user_id` requires a locally generated `uuid4()`. No state is carried
   across calls.

5. **Model parameter format.** The `model` parameter is accepted as a bare
   string and passed directly to ADK's `Agent(model=...)` constructor with
   no transformation or validation:
   - **Gemini models**: bare model name, e.g. `"gemini-2.0-flash"`,
     `"gemini-flash-latest"`.
   - **Non-Gemini models via LiteLLM**: ADK's LiteLLM integration convention,
     e.g. `"litellm/openai/gpt-5.5"`.
   No prefix stripping, custom routing logic, or model-name validation is
   performed in the adapter.

6. **The `execute()` method signature** is defined by the `Backend` Protocol
   from spec `02_backend_protocol`. `GoogleADKBackend` implements it exactly
   as an **async generator function** (`async def` + `yield`), consistent with
   `ClaudeBackend` and `DeepAgentsBackend`. Using `yield` internally satisfies
   `-> AsyncIterator[AgentMessage]` and ensures early-return and exception
   behaviour matches the established pattern:

   ```python
   async def execute(
       self,
       prompt: str,
       *,
       system_prompt: str,
       model: str,
       cwd: str,
       permission_callback: PermissionCallback | None = None,
       activity_callback: ActivityCallback | None = None,
       tool_error_callback: Any | None = None,
       node_id: str = "",
       archetype: str | None = None,
       max_turns: int | None = None,
       max_budget_usd: float | None = None,
       thinking: dict[str, Any] | None = None,
       effort: str | None = None,
       compaction: bool = False,
   ) -> AsyncIterator[AgentMessage]:
   ```

   The method creates an ADK Agent and Runner, sends the task prompt, and
   streams ADK events mapped to canonical types (see event mapping below).

   Parameter handling:
   - `system_prompt` → mapped directly to ADK `Agent`'s `instruction`
     parameter.
   - `max_turns` → mapped to a **manual turn counter** inside `execute()`.
     A "turn" is one tool-call/response round-trip. After `max_turns` turns,
     the event loop is stopped and a terminal `ResultMessage` is yielded
     immediately (with `is_error=False`; the caller decides whether hitting
     the limit is an error condition). This provides the same safety limit as
     `ClaudeBackend`'s `max_turns`. When `max_turns` is `None`, the loop runs
     until ADK signals completion or an error occurs.
   - `thinking` → **silently ignored in v1.** Gemini's thinking/reasoning
     features have a different API surface from Anthropic's extended thinking
     and require dedicated investigation. Thinking support will be added in a
     follow-up spec.
   - `effort` → silently ignored for models that don't support it.
   - `max_budget_usd` → accepted but silently ignored; emit a `debug`-level
     log (see Logging section).
   - `compaction` → not used; ADK's event loop handles context internally.
   - `permission_callback` → **invoked for every tool call**, mirroring
     `ClaudeBackend`. The callback checks the af security allowlist. If the
     callback returns a denial, the tool call is blocked and a structured
     error dict is returned to the model (same shape as path-containment
     errors: `{"error": "permission_denied", "detail": "..."}`) without
     executing the tool.
   - `activity_callback` → **invoked before each tool call** to report tool
     invocations for UI progress, mirroring `ClaudeBackend`. Both callbacks
     are core functionality, not optional enhancements.
   - `tool_error_callback`, `node_id`, `archetype` → accepted for protocol
     conformance; silently ignored in v1.

7. **Event mapping.** Events from `runner.run_async(...)` are mapped to
   canonical types:

   - **FunctionCall events** → yield `ToolUseMessage` immediately upon
     detection, before the tool result is known. This mirrors `ClaudeBackend`
     behavior, where a `ToolUseMessage` is yielded as soon as a tool call is
     detected in the SDK's assistant message. A `ToolUseMessage` is yielded
     for **all** FunctionCall events regardless of whether the tool name is
     recognised in the registered tool list — filtering would hide agent
     behaviour from the session layer's telemetry and audit logging.
   - **FunctionResponse / tool result events** → consumed internally and
     silently discarded; not yielded to the caller.
   - **Text content events** → yield `AssistantMessage`
   - **No-op / unrecognised events** → silently skipped
   - **Final/terminal event** → yield `ResultMessage` with aggregated token
     usage
   - **Error events or streaming exceptions** → yield
     `ResultMessage(is_error=True, ...)` (see Error Handling section)

   > **Implementation note:** ADK 2.x has breaking changes to the event model.
   > The exact Python class names, discriminator field values, and
   > `event.type` string constants that identify FunctionCall, text content,
   > and terminal events **must be verified empirically** against the installed
   > `google-adk>=2.0` version during development. Pin the confirmed class
   > names and discriminator values in inline code comments once established.
   > The semantic mapping above is authoritative; the ADK-specific type
   > bindings are an implementation detail.

8. **af SDK tools as ADK function tools.** The af SDK functions are imported
   directly from their source modules, following the same pattern as
   `DeepAgentsBackend`. Use `deepagents.py` as the canonical reference for
   import paths — mirror the exact imports used there. The five af SDK
   functions (`spec_read`, `context_search`, `context_get`, `memory_recall`,
   `subtask_state`) are wrapped as plain Python functions with type
   annotations and docstrings (ADK's native tool format). The import and
   tool registration occur inside `google_adk.py` at construction time; no
   dependency injection or registry lookup is required.

   > **Cross-reference:** `deepagents.py` is the authoritative source for
   > af SDK tool import paths. `google_adk.py` mirrors those imports exactly.
   > If `deepagents.py` is refactored, `google_adk.py` must be updated in
   > the same change.

9. **Provider-specific features degrade gracefully:**
   - `thinking`: Silently ignored in v1 (see execute() signature above).
   - `compaction`: Not used — ADK's event loop handles context internally.
   - `max_budget_usd`: Out of scope for the initial implementation. ADK does
     not natively support budget enforcement. The parameter is accepted but
     silently ignored with a `debug`-level log. If budget tracking is added
     later, `agentfox.core.config.PricingConfig` and
     `agentfox.core.models.calculate_cost()` are the canonical sources for
     pricing data; the adapter would check accumulated token counts against
     the budget after each model response.
   - `effort`: Ignored silently for models that don't support it.

10. **Register the backend** in `create_backend()` factory: `"google-adk"` →
    lazy import of `GoogleADKBackend`.

11. **Widen the config `Literal` type** in `OrchestratorConfig.backend`
    (defined in `agentfox/core/config.py`) from
    `Literal["claude", "deepagents"]` to
    `Literal["claude", "deepagents", "google-adk"]`. This change is purely
    additive and non-breaking: existing configs with `backend="claude"` or
    `backend="deepagents"` continue to work without modification. Pydantic
    validates new configs against the widened Literal. No migration or
    backward-compatibility shim is needed.

12. **Add `google-adk` as an optional dependency** in `pyproject.toml` under
    an extras group (e.g. `[project.optional-dependencies]
    google-adk = ["google-adk>=2.0"]`).

13. **Update the containment test** to add the ADK SDK name → `"google_adk.py"`
    to the SDK-to-file mapping, following the existing pattern in the
    containment test file (mirror how `deepagents.py` is registered in the
    mapping).

14. **Path containment security** for coding tools: all file operations must
    resolve paths relative to `cwd` and reject any path that escapes the
    workspace root via `..` traversal or symlinks. The `cwd` parameter is
    typed as `str` in the `Backend` Protocol; `google_adk.py` converts it to
    `pathlib.Path` internally at the start of `execute()`, and passes the
    resolved `Path` object to `adk_tools.py` tool constructors. The
    `adk_tools.py` tool functions accept `cwd` as a `pathlib.Path`. Resolution
    steps:
    - Compute `resolved = (cwd / user_path).resolve()` (follows symlinks).
    - Check `resolved.is_relative_to(cwd.resolve())`.
    - If the check fails (path escapes workspace), return a dict with
      `{"error": "path_not_allowed", "detail": "Path escapes workspace root"}`
      without performing any I/O. No exception is raised to the model; the
      structured error dict is returned so the model can react.
    This mirrors the security constraints enforced by Claude Code and Deep
    Agents' built-in tools.

## Error Handling

Error handling mirrors the `ClaudeBackend` / `DeepAgentsBackend` pattern so
callers experience a consistent interface across all backends:

- **Transient errors** (retriable): The following exception classes are
  treated as transient and trigger retry logic:
  - `google.api_core.exceptions.ResourceExhausted` (HTTP 429 — rate limit /
    quota exhaustion)
  - `google.api_core.exceptions.ServiceUnavailable` (HTTP 503)
  - `ConnectionError` (network-level failure)
  - `OSError` (network-level failure)

  This mirrors `ClaudeBackend`'s retry pattern, which treats `RateLimitError`,
  `APIStatusError` with status ≥ 500, and `OSError` as retriable.

- **Non-transient errors**: All other exceptions (e.g. model not found,
  invalid request, tool execution exception) are treated as non-transient and
  yield an immediate `ResultMessage(is_error=True, is_transport_error=False)`
  without retry.

- **Retry constants** are defined as module-level constants in `google_adk.py`,
  duplicating the values from `ClaudeBackend` for now (a shared-module
  extraction is a future cleanup once all three backends exist):
  ```python
  _MAX_TRANSPORT_RETRIES = 3
  _BACKOFF_BASE = 1.0  # seconds
  # delay = _BACKOFF_BASE * 2^(attempt-1) → 1 s, 2 s, 4 s
  ```
  After `_MAX_TRANSPORT_RETRIES` retries are exhausted, yield a
  `ResultMessage(is_error=True, is_transport_error=True)`.

- **No exceptions propagate** out of `execute()`. All error conditions are
  surfaced as a terminal `ResultMessage`.

## Token Usage

Token counts are aggregated and reported in the final `ResultMessage` using
the following strategy:

1. **Prefer the terminal event** if it carries cumulative token counts —
   ADK's final event should contain aggregated usage metadata.
2. **Fall back to summing intermediate events** if the terminal event does
   not carry cumulative counts (determined empirically during implementation).

ADK 2.x has changed the event model and the exact field names for token usage
metadata (e.g., `event.usage_metadata.*`) must be verified against the
installed `google-adk>=2.0` version during implementation. Once confirmed,
the field paths are pinned in inline code comments.

## Logging

- The `debug`-level log emitted when `max_budget_usd` is silently ignored
  must use the module's standard logger (obtained via
  `logging.getLogger(__name__)`) and follow the message format:
  `"max_budget_usd=%s ignored: budget enforcement not supported by GoogleADKBackend"`.
- No additional structured logging fields are required beyond the standard
  Python logging record.

## Non-Goals

- Using ADK's multi-agent workflow features (SequentialAgent, ParallelAgent,
  LoopAgent) — agent-fox's engine layer handles orchestration.
- Using ADK's built-in memory/session persistence — agent-fox has its own
  knowledge and context systems.
- Supporting ADK's streaming agent features (Gemini Live API) — standard
  request-response mode is sufficient for coding sessions.
- MCP tool integration via ADK — af SDK tools are registered as native
  ADK function tools for simplicity.
- `max_budget_usd` enforcement — deferred to a future iteration.
- `thinking` / reasoning support — deferred to a follow-up spec; requires
  dedicated investigation of Gemini's thinking API surface.

## Tech Stack

- Python 3.12+
- `google-adk>=2.0` (Google Agent Development Kit)
- `google-genai` (transitive, for Gemini content types)
- `google-api-core` (transitive, for exception classes used in retry logic)
- pytest + pytest-asyncio for tests

## Testing Strategy

Unit tests only — no integration tests against live Gemini endpoints are
required at launch. Tests live in `tests/backends/test_google_adk.py` and
`tests/backends/test_adk_tools.py`, following the naming convention of
`test_claude.py` and `test_deepagents.py`. The testing pattern follows
`ClaudeBackend` and `DeepAgentsBackend`:

- **ADK Agent and Runner are fully mocked.** Tests inject mocked async
  generator event streams (compatible with `async for`) to verify
  event-to-message mapping, error handling paths, retry logic, and tool
  registration. Because `runner.run_async` returns an async iterator, test
  helpers should provide factory functions that yield pre-configured sequences
  of mock ADK event objects.
- **`adk_tools.py` tools are unit-tested independently**, covering the happy
  path and path-containment security for each of the six tools.
- **Tests are guarded with `pytest.importorskip("google.adk")`** so that the
  test suite continues to pass in environments where the optional `google-adk`
  dependency is not installed.
- **Containment test** verifies that `google.adk` imports are isolated to
  `google_adk.py`, following the same mapping format used for `deepagents.py`.
- **Retry logic tests** verify that `ResourceExhausted` and
  `ServiceUnavailable` trigger retries up to `_MAX_TRANSPORT_RETRIES`, and
  that non-transient exceptions yield an immediate error `ResultMessage`.
- **Token usage tests** verify both the terminal-event-preference path and
  the intermediate-event-summation fallback path.
- **`max_turns` tests** verify that the event loop stops after the configured
  number of tool-call/response round-trips and yields a terminal
  `ResultMessage`.
- **`permission_callback` and `activity_callback` tests** verify that both
  callbacks are invoked at tool boundaries and that a permission denial
  results in the tool being blocked with a structured error dict returned to
  the model.
- **Unrecognised tool name test** verifies that a FunctionCall event for an
  unknown tool name still yields a `ToolUseMessage`.

## Design Decisions

1. **Coding tools are a separate module (`adk_tools.py`).** This keeps
   `google_adk.py` focused on the adapter/mapping logic. The tools module
   is reusable and independently testable.

2. **Tools return dicts, not strings.** ADK function tools return dicts
   which are serialized to the model. This matches ADK's convention and
   provides structured output for richer tool results. Path-containment
   violations and permission denials also return a structured error dict
   rather than raising an exception, so the model can observe and react to
   the error.

3. **InMemorySessionService for session state.** Each `execute()` call
   creates a fresh `InMemorySessionService`, calls `create_session(app_name=
   "agent-fox", user_id=str(uuid4()))` to obtain a session object, and uses
   `session.id` and `session.user_id` when calling `runner.run_async()`.
   No state persists between calls. This matches `ClaudeBackend`'s behavior
   where each session is independent and satisfies ADK's requirement that a
   session exist in the service before `run_async` is called.

4. **Model string passed through unchanged.** Bare Gemini model names (e.g.
   `"gemini-2.0-flash"`) and ADK LiteLLM-prefixed strings (e.g.
   `"litellm/openai/gpt-5.5"`) are forwarded directly to ADK's `Agent`
   constructor. The adapter does not validate, transform, or route based on
   model name, keeping the code simple and forward-compatible with new ADK
   model identifiers.

5. **Path containment via `pathlib.resolve()`.** All file paths are resolved
   against `cwd` and checked with `is_relative_to(cwd.resolve())` before any
   I/O operation. Symlinks are resolved before the check; if the resolved
   target escapes the workspace root, the operation is rejected with a
   structured error dict. The `cwd` `str` from the `Backend` Protocol is
   converted to `pathlib.Path` once at the start of `execute()` and passed
   as a `Path` to tool constructors in `adk_tools.py`.

6. **ADK event type bindings discovered empirically.** Because ADK 2.x's
   event model has evolved and class names/discriminator values are not stable
   in public documentation, the precise event type bindings must be confirmed
   against the installed version during development and pinned in code
   comments. The semantic mapping (FunctionCall → `ToolUseMessage`, text →
   `AssistantMessage`, terminal → `ResultMessage`) is fixed by this spec;
   only the ADK-specific type names are deferred.

7. **`ToolUseMessage` yielded immediately on FunctionCall, regardless of tool
   name.** Mirroring `ClaudeBackend`, a `ToolUseMessage` is emitted as soon
   as a FunctionCall event is detected. The corresponding `FunctionResponse`
   event is consumed silently. A `ToolUseMessage` is yielded for all
   FunctionCall events — even those referencing unrecognised tool names —
   because filtering would hide agent behaviour from the session layer's
   telemetry and audit logging. This keeps the event stream predictable for
   orchestration-layer callers without requiring buffering logic.

8. **Token usage aggregation: terminal-event preference with fallback.**
   The terminal event is used if it carries cumulative counts; otherwise,
   counts are summed across intermediate events. This preference is consistent
   with ADK's documented behavior and avoids double-counting. Field paths are
   pinned in code comments once confirmed empirically.

9. **`system_prompt` sourced from `execute()` signature.** The `system_prompt`
   keyword argument is part of the `Backend` Protocol and is passed by the
   caller at invocation time. It maps directly to ADK `Agent`'s `instruction`
   parameter, consistent with how `ClaudeBackend` and `DeepAgentsBackend`
   handle the system prompt.

10. **af SDK tools imported by mirroring `deepagents.py`.** Following the
    `DeepAgentsBackend` pattern, af SDK tool functions are imported directly
    from their known modules (use `deepagents.py` as the canonical import
    reference) and wrapped for ADK at construction time. This avoids
    introducing a new registry abstraction and keeps the dependency graph
    explicit. `deepagents.py` is the authoritative source; if it is
    refactored, `google_adk.py` must be updated in the same change.

11. **Error handling mirrors existing backends.** Consistent retry constants,
    transient error classification, and error surfacing (via terminal
    `ResultMessage`) across all backends ensures that orchestration-layer
    callers need no backend-specific error logic. Retry constants are
    duplicated inline for now; extraction to a shared module is a future
    cleanup.

12. **`thinking` deferred to v2.** Gemini's thinking/reasoning API surface
    differs meaningfully from Anthropic's extended thinking and requires
    dedicated investigation. Silently ignoring the `thinking` parameter in v1
    keeps the adapter simple and avoids shipping an untested partial
    implementation.

13. **`OrchestratorConfig.backend` widening is non-breaking.** Adding
    `"google-adk"` to the `Literal` in `agentfox/core/config.py` is purely
    additive. Pydantic validates existing values without change; no migration
    is needed.

14. **`permission_callback` and `activity_callback` are core functionality.**
    Both callbacks are invoked at tool boundaries in v1, mirroring
    `ClaudeBackend`. `activity_callback` fires before each tool call for UI
    progress reporting. `permission_callback` gates each tool call against
    the af security allowlist; a denial returns a structured error dict to
    the model without executing the tool. This is not optional behaviour that
    can be deferred.

15. **`max_turns` is a manual turn counter, not an ADK construct.** ADK does
    not expose a built-in turn-limit mechanism equivalent to `ClaudeBackend`'s
    `max_turns`. `execute()` maintains its own counter that increments on each
    tool-call/response round-trip and stops the event loop when the limit is
    reached, yielding a terminal `ResultMessage`. When `max_turns` is `None`,
    the loop runs until ADK signals completion or an error.

16. **`execute()` is an async generator function.** The method uses `async def`
    with `yield` statements, making it an async generator. This satisfies
    `-> AsyncIterator[AgentMessage]`, matches `ClaudeBackend` and
    `DeepAgentsBackend`, and ensures consistent early-return and exception
    surface behaviour across all backends.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 02_backend_protocol | 1 | 1 | Backend Protocol and create_backend() factory |
