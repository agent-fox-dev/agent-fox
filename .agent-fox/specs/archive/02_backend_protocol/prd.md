---
spec_id: '02'
spec_name: backend_protocol
title: Backend Protocol and Factory
status: draft
created_at: '2026-07-07T12:50:49.793050+00:00'
updated_at: '2026-07-07T13:02:16.972698+00:00'
owner: Michael Kuehl
source: interactive
schema_version: 1
tags:
- session
- backend
- abstraction
- provider-agnostic
---
# Backend Protocol and Factory

## Intent

Introduce a runtime-checkable `Backend` Protocol and factory to decouple the session layer from `ClaudeBackend`, enabling future alternative backends without modifying `session.py`.

## Background

`spec 03_deepagents_backend` and `spec 04_google_adk_backend` are planned as the immediate next specs and depend directly on this work. This spec is the foundation they require. Beyond the planned integrations, the current hardcoded `ClaudeBackend` import in `session.py` is a structural limitation: any team wanting to experiment with or adopt an alternative LLM backend today must fork and modify core session logic.

## Problem

The session layer in `agentfox` is hardcoded to `ClaudeBackend`. The `session.py` module imports `ClaudeBackend` directly, type-hints the backend parameter as `ClaudeBackend | None`, and instantiates `ClaudeBackend()` as the default. This prevents alternative agent backends (e.g. LangChain Deep Agents, Google ADK) from being used without modifying `session.py`.

The canonical message types (`ToolUseMessage`, `AssistantMessage`, `ResultMessage`) in `session/backends/types.py` are already SDK-independent, but there is no formal `Backend` Protocol that defines the contract a backend adapter must satisfy.

The `async for` loop over `backend.execute()` in `session.py` line 256 currently requires a `# type: ignore[attr-defined]` comment because mypy cannot resolve the return type of `ClaudeBackend.execute()` through the hardcoded type hint. Once `session.py` is typed against the `Backend` Protocol, mypy will resolve the `AsyncIterator[AgentMessage]` return type correctly and the suppression comment can be removed.

## Goals

The implementation is complete and correct when all of the following criteria are met:

1. `isinstance(ClaudeBackend(), Backend)` returns `True`.
2. All existing session unit tests pass without modification.
3. Importing `session.py` with `backend="claude"` does not import `deepagents` or `google.adk` (lazy-import isolation verified by containment test).
4. `ConfigError` is raised for unregistered backend names passed to `create_backend()`.
5. The `# type: ignore[attr-defined]` comment on `session.py` line 256 is eliminated.

## Solution

1. **Define a `Backend` runtime-checkable Protocol** in `session/backends/protocol.py` that captures the `execute()` async generator contract, the `close()` lifecycle method, and the `name` property already implemented by `ClaudeBackend`.

2. **Add a `create_backend(name)` factory function** in `session/backends/__init__.py` that returns a `Backend` instance by name. The factory uses lazy imports so that SDK dependencies are only loaded when the corresponding backend is selected. The only registered backend for now is `"claude"` → `ClaudeBackend`.

3. **Add a `backend` config field** to `OrchestratorConfig` in `core/config.py` with `default="claude"` and valid values constrained to a `Literal` type.

4. **Update `session/session.py`** to accept `Backend` instead of `ClaudeBackend`. The `run_session()` function's `backend` parameter type changes from `ClaudeBackend | None` to `Backend | None`, and the default instantiation uses `create_backend(config.orchestrator.backend)`.

5. **Generalize the containment property test** in `tests/unit/session/backends/test_protocol.py` to enforce that each SDK is confined to its own backend file. The test scans the **entire `agentfox` package source tree recursively** — all `.py` files under `packages/agentfox/agentfox/` — using the `glob.glob(os.path.join(agent_fox_dir, '**', '*.py'), recursive=True)` pattern already used in `test_protocol.py`. **No test-file exclusion filter is needed** because the glob targets `packages/agentfox/agentfox/` (production source only); the `packages/agentfox/tests/` directory is outside this path and is never scanned. The scan asserts each SDK name string appears **only** in its designated file. Adding a new backend only requires extending the mapping, not rewriting the test. This test runs in CI on every PR as a **required check**.

6. **Update `__init__.py` exports** to include `Backend` and `create_backend` in `__all__`.

## Non-Goals

- Implementing alternative backends (Deep Agents, Google ADK) — those are separate specs (`03_deepagents_backend`, `04_google_adk_backend`).
- Changing `core/client.py` or the raw Anthropic SDK usage — one-shot LLM calls stay Anthropic-only.
- Changing `agentspec` — spec creation stays Anthropic-only.
- Modifying `ClaudeBackend` internals — it already satisfies the Protocol as-is.

## Backward Compatibility

Full backward compatibility is guaranteed. `ClaudeBackend` satisfies the `Backend` Protocol structurally, so no existing call sites require updates. The only observable change to callers is a wider type hint (`Backend` instead of `ClaudeBackend`) on the `backend` parameter of `run_session()`. No migration guide is needed.

## Tech Stack

- Python 3.12+
- `typing.Protocol` with `@runtime_checkable`
- pydantic `BaseModel` for config
- pytest for tests

## File Layout

The following files are **created** or **modified** as part of this spec:

```
session/
  backends/
    __init__.py          ← MODIFIED: add create_backend(); update __all__
    protocol.py          ← CREATED:  Backend Protocol definition
    types.py             ← unchanged (AgentMessage union already defined here)
    claude.py            ← unchanged (ClaudeBackend already satisfies Protocol)
core/
  config.py              ← MODIFIED: add backend field to OrchestratorConfig
session/
  session.py             ← MODIFIED: widen type hint; use create_backend()
tests/
  unit/session/backends/
    test_protocol.py     ← CREATED:  isinstance check + containment property test
```

No other files require modification. Notably, `session/__init__.py` and `agentfox/__init__.py` are **not** modified — see [Import Path for Consumers](#import-path-for-consumers).

## Interface Specification

### Callback Types (existing, `session/backends/types.py` and `agentfox/ui/progress.py`)

The following types are already defined in the codebase and are imported by the Protocol:

```python
# session/backends/types.py
PermissionCallback = Callable[[str, dict[str, Any]], Awaitable[bool]]
# Takes (tool_name: str, tool_input: dict[str, Any]) and returns whether the tool call is allowed.

# agentfox/ui/progress.py
ActivityCallback = Callable[[ActivityEvent], None]
# A synchronous callback that receives ActivityEvent objects for UI updates.
```

No changes are made to these definitions. The Protocol imports `PermissionCallback` from `session/backends/types.py` and `ActivityCallback` from `agentfox/ui/progress`.

### Backend Protocol (`session/backends/protocol.py`)

`ClaudeBackend.execute()` is declared as `async def execute(self, ...) -> AsyncIterator[AgentMessage]` — it is an async generator (uses `yield` inside an `async def`). The Protocol mirrors this exactly, using `AsyncIterator[AgentMessage]` as the return type. `AsyncGenerator` is a subtype of `AsyncIterator`, so `ClaudeBackend`'s async generator satisfies the Protocol structurally. The broader `AsyncIterator` return type is correct for the Protocol: it avoids over-constraining future backends while remaining compatible with mypy. CI static analysis will validate mypy compatibility during implementation.

```python
from typing import Any, AsyncIterator, Protocol, runtime_checkable
from .types import AgentMessage, PermissionCallback
from agentfox.ui.progress import ActivityCallback

@runtime_checkable
class Backend(Protocol):
    @property
    def name(self) -> str: ...

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
        node_id: str = '',
        archetype: str | None = None,
        max_turns: int | None = None,
        max_budget_usd: float | None = None,
        thinking: dict[str, Any] | None = None,
        effort: str | None = None,
        compaction: bool = False,
    ) -> AsyncIterator[AgentMessage]: ...

    async def close(self) -> None: ...
```

**Notes:**
- `execute()` is declared `async def` returning `AsyncIterator[AgentMessage]` to match `ClaudeBackend.execute()` exactly (an async generator). `AsyncGenerator` is a subtype of `AsyncIterator`, so `ClaudeBackend`'s async generator satisfies this Protocol. Using the broader `AsyncIterator` avoids over-constraining future backends.
- `AgentMessage` is an existing type alias in `session/backends/types.py`: `AgentMessage = ToolUseMessage | AssistantMessage | ResultMessage`.
- `close()` is `async`. It is idempotent — safe to call multiple times in any state (before, during, or after `execute()`). For `ClaudeBackend` it is a no-op. See the [Lifecycle Contract](#lifecycle-contract) section for mid-stream teardown semantics.
- The `name` property identifies the backend for logging and telemetry (e.g. `"claude"`, `"deepagents"`, `"google-adk"`).
- The Protocol exactly mirrors the existing `ClaudeBackend` interface, so `ClaudeBackend` satisfies it structurally without modification.
- The `tool_error_callback` parameter is typed as `Any | None` because no concrete cross-backend callback type has been defined yet. This weakens static typing locally but avoids prematurely coupling future backends to a `ClaudeBackend`-specific type. A concrete type may be introduced in a future spec.
- **`effort` parameter:** Maps to a reasoning/effort level for models that support it. Valid string values are `'low'`, `'medium'`, `'high'`, `'xhigh'`, and `'max'`. Backends that do not support this parameter **must ignore it silently** — consistent with how `ClaudeBackend` handles unsupported parameters (try/except `TypeError` with a warning log). Backends must not raise an error for unrecognised `effort` values they receive.

#### `isinstance` Check Semantics

`isinstance(ClaudeBackend(), Backend)` returns `True` because Python's `@runtime_checkable` Protocol performs a **presence-only** check: it confirms the object has `execute`, `close`, and `name` attributes, but does not verify their signatures or return types. This is the standard Python Protocol pattern and is sufficient here because:

- Full signature validation (parameter types, return type) is enforced by **mypy/pyright at static analysis time** as a required CI check.
- The runtime check provides a meaningful guard against objects that are entirely missing required attributes (e.g. a misconfigured plugin).

No deeper runtime verification is intended or needed.

#### Lifecycle Contract

- `close()` is for **releasing resources after** the async iterator from `execute()` is exhausted or after the calling task has been cancelled.
- In practice, `session.py` uses `asyncio.wait_for()` with a timeout; asyncio task cancellation handles mid-stream teardown automatically. `close()` is then called in a `finally` block to release any remaining resources.
- The Protocol does **not** prescribe whether `close()` must interrupt an in-progress `execute()` call. Each backend documents its own teardown behavior. Future backend implementers (specs 03 and 04) should document their specific teardown contract clearly.
- The idempotency guarantee applies in all states: calling `close()` before, during, or after `execute()` must not raise an error. There are no additional thread-safety or coroutine-safety guarantees beyond what `ClaudeBackend` already provides — specifically, concurrent calls to `close()` from multiple coroutines simultaneously are not guaranteed to be safe. `ClaudeBackend.close()` is a no-op, so concurrent calls are harmless in practice, but future backends should document their own concurrency contract explicitly.

### `create_backend(name: str) -> Backend` (`session/backends/__init__.py`)

- Accepts a backend name as a plain `str`. The factory signature is intentionally `str` (not `Literal["claude"]`) to avoid coupling the signature to the set of valid backend names, which changes with each new backend spec. Runtime validation via `ConfigError` is sufficient: the factory is primarily called from `session.py` using a config-validated value, and direct callers (tests, CLI) receive a clear `ConfigError` with an actionable message.
- Uses lazy imports inside the function body so SDK dependencies are only loaded when the corresponding backend is selected.
- If the lazy import fails because the required SDK is not installed, catches `ImportError` and raises `ConfigError` with an installation hint. For example:
  ```
  ConfigError('Backend "claude" requires claude-agent-sdk. Install it with: pip install claude-agent-sdk')
  ```
  This is consistent with how `_check_vertex_deps()` and `_check_bedrock_deps()` in `core/client.py` handle missing optional dependencies.
- Raises `agentfox.core.errors.ConfigError` for any unrecognised name (not caught by the `ImportError` handler above). This includes empty strings and other malformed values — all fall through to the same unrecognised-name `ConfigError` path. In practice, pydantic `Literal` validation on `OrchestratorConfig` prevents invalid values from reaching the factory via the normal config-driven call path.
- Example error message for unknown name: `"Unknown backend: 'foo'. Valid backends are: ['claude']"` (consistent with the style used by `resolve_model()`).

#### Error Propagation and Session Startup Failure

If `create_backend()` raises a `ConfigError` during session startup (e.g. the required SDK is not installed, or an invalid backend name is supplied), the error **propagates immediately — no fallback to `"claude"` or any other backend is attempted**. The existing exception-handling pipeline in `session.py` and the engine layer already catches exceptions from `run_session()` and surfaces them to the top-level caller with a user-facing error message. No additional recovery mechanism is required. This fail-fast behavior ensures misconfiguration is surfaced immediately rather than silently masked.

### `OrchestratorConfig` changes (`core/config.py`)

- A new field `backend: Literal["claude"] = "claude"` is added to `OrchestratorConfig`.
- The field is **required to be present in config** but has a default of `"claude"`, so existing config files that omit the field continue to work.
- The field is **config-file only** — no environment variable override. This matches the behaviour of all other `OrchestratorConfig` fields, which use TOML file merging (global + local) rather than env vars.
- **Config merging behaviour:** The existing `shallow_merge` function in `config.py` replaces TOML sections wholesale only when the local TOML explicitly provides that section. If the local TOML omits `[orchestrator]` entirely, all global orchestrator fields — including `backend` — are inherited. If the local TOML includes `[orchestrator]` but omits `backend`, the global value is not inherited for that field (the pydantic default `"claude"` applies). This is the same behavior that applies to **all** `OrchestratorConfig` fields (e.g. `parallel`, `session_timeout`) and is not specific to `backend`. Teams that want a non-default backend globally should set it in the global TOML and ensure local configs either omit `[orchestrator]` entirely or also specify `backend`. This behavior is documented in Design Decision 10.
- **Validation errors:** When an invalid `backend` literal is read from the TOML file, pydantic raises a `ValidationError`. The existing `load_config()` function in `config.py` already catches pydantic `ValidationError` and re-raises it as a `ConfigError` with a user-friendly message — the `backend` field benefits from this behavior automatically, with no additional error-handling code required.
- The `Literal` type will be widened to `Literal["claude", "deepagents", "google-adk"]` when those backends are added in `spec 03` and `spec 04`.

### `__init__.py` exports (`session/backends/__init__.py`)

After this change, `__all__` will be:

```python
__all__ = [
    'AgentMessage',
    'AssistantMessage',
    'Backend',
    'ClaudeBackend',
    'PermissionCallback',
    'ResultMessage',
    'ToolUseMessage',
    'create_backend',
]
```

Only `session/backends/__init__.py` is updated. `session/__init__.py` and `agentfox/__init__.py` are **not** modified — see [Import Path for Consumers](#import-path-for-consumers) below.

### Import Path for Consumers

`create_backend` and `Backend` are **not** re-exported from `session/__init__.py` or `agentfox/__init__.py`. This is an intentional design decision to keep the public API surface minimal until the factory stabilizes across all three planned backends (specs 02, 03, 04). Downstream consumers — including the CLI, plugins, and tests — must import directly from `session.backends`:

```python
from agentfox.session.backends import create_backend, Backend
```

Once the factory API is stable across all backends, promotion to a higher-level `__init__.py` can be considered in a future spec.

## Testing Strategy

| Test | Type | CI Enforcement |
|---|---|---|
| `isinstance(ClaudeBackend(), Backend)` | Unit | Required check, every PR |
| Containment property (SDK-to-file isolation) | Unit (file content scan) | Required check, every PR |
| `ConfigError` on unknown backend name | Unit | Required check, every PR |
| `ConfigError` with install hint on missing SDK | Unit | Required check, every PR |
| Full existing session test suite passes unmodified | Unit | Required check, every PR |
| `# type: ignore` removed and mypy passes | Static analysis | Required check, every PR |

### Containment Test Detail

The containment test scans **all `.py` files under `packages/agentfox/agentfox/` recursively** using the glob pattern already used in the existing `test_protocol.py`:

```python
glob.glob(os.path.join(agent_fox_dir, '**', '*.py'), recursive=True)
```

**No test-file exclusion filter is required.** The glob targets `packages/agentfox/agentfox/` (the production package source). The `packages/agentfox/tests/` directory is a sibling path, entirely outside this glob, and is never scanned. This matches the existing `test_protocol.py` behavior exactly.

The test scans file contents for bare SDK module name strings (e.g. `'claude_agent_sdk'`) and asserts each SDK string appears **only** in its designated file. The scan uses a simple substring match (`sdk_name in file_contents`), which is intentionally broad: it catches all import styles (`import claude_agent_sdk`, `from claude_agent_sdk import ...`), inline comments, and accidental coupling. The designated file is excluded from the scan (it is the only file permitted to contain the SDK name).

The test is driven by a dictionary mapping (`sdk_name → allowed_filename`):

```python
SDK_CONTAINMENT = {
    "claude_agent_sdk": "claude.py",
    # Future entries added when specs 03 and 04 are implemented.
    # The exact SDK name strings for deepagents and google-adk will be
    # determined by those specs respectively — do not assume 'google.adk'
    # or 'google_adk' here, as namespace package import styles differ.
    # "deepagents": "deepagents.py",       # placeholder — spec 03 will define
    # "google.adk": "google_adk.py",       # placeholder — spec 04 will define
}
```

Adding a new backend only requires a one-line addition to this mapping — the test logic does not change. The exact SDK name strings for the `deepagents` and `google-adk` entries will be specified in specs 03 and 04 respectively.

## Design Decisions

1. **Protocol lives in its own file (`protocol.py`)** rather than in `types.py` — this separates the data types (frozen dataclasses) from the behavioral contract (Protocol). The Protocol imports from `types.py`.

2. **Factory uses lazy imports** — `create_backend("claude")` imports `ClaudeBackend` inside the function body, not at module level. This means `claude_agent_sdk` is never imported unless the claude backend is selected, enabling future backends that don't depend on it.

3. **Config field is `Literal["claude"]` for now** — the type will be widened to `Literal["claude", "deepagents", "google-adk"]` when those backends are added. Using `Literal` gives pydantic validation for free. The field is config-file only; no env var override.

4. **`execute()` is `async def` returning `AsyncIterator[AgentMessage]`** — matching the existing `ClaudeBackend.execute()` signature exactly (an async generator). `AsyncGenerator` is a subtype of `AsyncIterator`, so `ClaudeBackend`'s async generator satisfies the Protocol. Using the broader `AsyncIterator` avoids over-constraining future backends. The `# type: ignore[attr-defined]` on the `async for` in `session.py` line 256 can be removed once the Protocol is in place, because mypy will now resolve the `AsyncIterator[AgentMessage]` return type through the Protocol.

5. **Unknown backend names raise `ConfigError`** from the factory, not `ValueError` — consistent with how `resolve_model()` handles unknown model names. `ConfigError` already exists at `agentfox.core.errors.ConfigError` and requires no new code. Empty strings and other malformed values fall through to the same unrecognised-name `ConfigError` path; pydantic `Literal` validation prevents these from reaching the factory via the normal config-driven call path.

6. **Missing SDK raises `ConfigError` with install hint** — if a lazy import fails at runtime (e.g. `claude_agent_sdk` not installed), the factory catches `ImportError` and raises `ConfigError` with a `pip install` hint. This is consistent with `_check_vertex_deps()` and `_check_bedrock_deps()` in `core/client.py`.

7. **`name` property on the Protocol** — backends must identify themselves with a string name (e.g. `"claude"`, `"deepagents"`, `"google-adk"`). Used for logging and telemetry.

8. **`close()` is idempotent and async** — callers may invoke `close()` multiple times without error (e.g. from both a `finally` block and an exception handler). For `ClaudeBackend` it is a no-op. The Protocol does not prescribe mid-stream cancellation behavior; asyncio task cancellation handles that at the `session.py` level via `asyncio.wait_for()`. Each backend documents its own teardown contract. Concurrent calls to `close()` from multiple coroutines simultaneously are not guaranteed to be safe — future backends should document their own concurrency contract explicitly.

9. **Containment test scans production source only, no exclusion filter needed** — the glob targets `packages/agentfox/agentfox/` exclusively. Test files live under `packages/agentfox/tests/`, a sibling directory never reached by the glob. This eliminates any risk of false positives from SDK names appearing in mock/patch strings within test files. The broad substring scan catches all import styles, comments, and accidental coupling across the entire production package.

10. **Config merging follows existing `shallow_merge` semantics** — the `backend` field behaves identically to all other `OrchestratorConfig` fields (e.g. `parallel`, `session_timeout`). Global TOML values are inherited when the local TOML omits the `[orchestrator]` section entirely; if the local TOML includes `[orchestrator]` but omits `backend`, the pydantic default `"claude"` applies (not the global value). This is not specific to `backend` — it is the existing shallow_merge contract for all orchestrator fields. Teams should document this merging behaviour in their deployment guides if using a non-default backend globally.

11. **`isinstance` check is presence-only** — `@runtime_checkable` Protocol only verifies that `execute`, `close`, and `name` attributes exist on the object. Full signature validation is delegated to mypy/pyright as a required CI static analysis check. This is the standard Python Protocol pattern and requires no additional mechanism.

12. **`effort` parameter is silently ignored by unsupporting backends** — backends that do not support the `effort` parameter must ignore it silently (consistent with `ClaudeBackend`'s try/except `TypeError` with warning log pattern). Backends must not raise for unrecognised effort values. The specific logger name and warning message format for each backend are left to the implementing spec (03, 04) to define, ensuring each backend documents its own observability behavior.

13. **`tool_error_callback` typed as `Any | None`** — no concrete cross-backend type exists yet. This is a deliberate short-term trade-off to avoid coupling future backends to a `ClaudeBackend`-specific type. A concrete type may be introduced in a future spec once the cross-backend callback shape is known.

14. **`ValidationError` for invalid config `backend` value is re-raised as `ConfigError`** — the existing `load_config()` already catches pydantic `ValidationError` and produces a user-friendly `ConfigError`. No additional handling is needed for the `backend` field.

15. **`create_backend()` factory signature is `str`, not `Literal`** — the factory accepts a plain `str` to avoid coupling its signature to the set of valid backend names, which grows with each new backend spec. Runtime validation via `ConfigError` is sufficient: the factory's primary call site is `session.py`, where the value has already been validated by pydantic `Literal` on `OrchestratorConfig`. Direct callers (tests, CLI) receive a clear `ConfigError` with an actionable message for any invalid input.

16. **`create_backend()` fails fast on `ConfigError` — no fallback** — if a `ConfigError` is raised during session startup (missing SDK, unknown backend name), the error propagates immediately to the top-level caller via the existing exception-handling pipeline in `session.py` and the engine layer. No silent fallback to `"claude"` or any other backend is attempted. This fail-fast behavior ensures misconfiguration is surfaced immediately rather than silently masked.

17. **`Backend` and `create_backend` are not re-exported at higher package levels** — `session/__init__.py` and `agentfox/__init__.py` are not modified. The public API surface is kept minimal until the factory stabilizes across all three planned backends. Consumers import from `agentfox.session.backends` directly. Promotion to a higher-level `__init__.py` can be considered once specs 03 and 04 are complete.

18. **Containment test placeholder comments do not commit to Google ADK SDK import string** — the `SDK_CONTAINMENT` mapping comment for `google-adk` is left as a placeholder. The exact substring to scan (e.g. `'google.adk'` vs `'google_adk'`) will be determined by spec 04, which will define the actual import style used. Using the wrong string in a placeholder comment could create a misleading precedent.
