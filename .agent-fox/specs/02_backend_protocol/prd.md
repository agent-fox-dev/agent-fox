---
spec_id: '02'
spec_name: backend_protocol
title: Backend Protocol and Factory
status: draft
created_at: '2026-07-07T12:50:49.793050+00:00'
updated_at: '2026-07-07T12:53:15.467639+00:00'
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

## Goals

The implementation is complete and correct when all of the following criteria are met:

1. `isinstance(ClaudeBackend(), Backend)` returns `True`.
2. All existing session unit tests pass without modification.
3. Importing `session.py` with `backend="claude"` does not import `deepagents` or `google.adk` (lazy-import isolation verified by containment test).
4. `ConfigError` is raised for unregistered backend names passed to `create_backend()`.
5. The `# type: ignore[attr-defined]` comment on `session.py` line 256 is eliminated.

## Solution

1. **Define a `Backend` runtime-checkable Protocol** in `session/backends/protocol.py` that captures the `execute()` async iterator contract, the `close()` lifecycle method, and the `name` property already implemented by `ClaudeBackend`.

2. **Add a `create_backend(name)` factory function** in `session/backends/__init__.py` that returns a `Backend` instance by name. The factory uses lazy imports so that SDK dependencies are only loaded when the corresponding backend is selected. The only registered backend for now is `"claude"` → `ClaudeBackend`.

3. **Add a `backend` config field** to `OrchestratorConfig` in `core/config.py` with `default="claude"` and valid values constrained to a `Literal` type.

4. **Update `session/session.py`** to accept `Backend` instead of `ClaudeBackend`. The `run_session()` function's `backend` parameter type changes from `ClaudeBackend | None` to `Backend | None`, and the default instantiation uses `create_backend(config.orchestrator.backend)`.

5. **Generalize the containment property test** in `tests/unit/session/backends/test_protocol.py` to enforce that each SDK is confined to its own backend file: `claude_agent_sdk` only in `claude.py`, `deepagents` only in `deepagents.py` (future), `google.adk` only in `google_adk.py` (future). The test uses file content scanning (grep for SDK import strings) against a mapping of `sdk-name → allowed-file`. Adding a new backend only requires extending the mapping, not rewriting the test. This test runs in CI on every PR as a **required check**.

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

No other files require modification.

## Interface Specification

### Backend Protocol (`session/backends/protocol.py`)

```python
from typing import Any, AsyncIterator, Protocol, runtime_checkable
from .types import AgentMessage, PermissionCallback, ActivityCallback

@runtime_checkable
class Backend(Protocol):
    @property
    def name(self) -> str: ...

    def execute(
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
- `AgentMessage` is an existing type alias in `session/backends/types.py`: `AgentMessage = ToolUseMessage | AssistantMessage | ResultMessage`.
- `close()` is `async`. It is idempotent — safe to call multiple times. For `ClaudeBackend` it is a no-op.
- The `name` property identifies the backend for logging and telemetry (e.g. `"claude"`, `"deepagents"`, `"google-adk"`).
- The Protocol exactly mirrors the existing `ClaudeBackend` interface, so `ClaudeBackend` satisfies it structurally without modification.

### `create_backend(name: str) -> Backend` (`session/backends/__init__.py`)

- Accepts a backend name string (e.g. `"claude"`).
- Uses lazy imports inside the function body so SDK dependencies are only loaded when the corresponding backend is selected.
- Raises `agentfox.core.errors.ConfigError` for any unrecognised name.
- Example error message: `"Unknown backend: 'foo'. Valid backends are: ['claude']"` (consistent with the style used by `resolve_model()`).

### `OrchestratorConfig` changes (`core/config.py`)

- A new field `backend: Literal["claude"] = "claude"` is added to `OrchestratorConfig`.
- The field is **required to be present in config** but has a default of `"claude"`, so existing config files that omit the field continue to work.
- The field is **config-file only** — no environment variable override. This matches the behaviour of all other `OrchestratorConfig` fields, which use TOML file merging (global + local) rather than env vars.
- Pydantic will raise a `ValidationError` (not `ConfigError`) when an invalid literal value is read from the TOML file; `ConfigError` is only raised by the `create_backend()` factory when an invalid name string is passed at runtime.
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

Only `session/backends/__init__.py` is updated. `session/__init__.py` and `agentfox/__init__.py` are **not** modified.

## Testing Strategy

| Test | Type | CI Enforcement |
|---|---|---|
| `isinstance(ClaudeBackend(), Backend)` | Unit | Required check, every PR |
| Containment property (SDK-to-file isolation) | Unit (file content scan) | Required check, every PR |
| `ConfigError` on unknown backend name | Unit | Required check, every PR |
| Full existing session test suite passes unmodified | Unit | Required check, every PR |
| `# type: ignore` removed and mypy passes | Static analysis | Required check, every PR |

The containment test scans source file contents for SDK import strings and asserts each SDK string appears only in its designated file. It is driven by a dictionary mapping (`sdk_name → allowed_filename`) so future backends require only a one-line addition to the mapping.

## Design Decisions

1. **Protocol lives in its own file (`protocol.py`)** rather than in `types.py` — this separates the data types (frozen dataclasses) from the behavioral contract (Protocol). The Protocol imports from `types.py`.

2. **Factory uses lazy imports** — `create_backend("claude")` imports `ClaudeBackend` inside the function body, not at module level. This means `claude_agent_sdk` is never imported unless the claude backend is selected, enabling future backends that don't depend on it.

3. **Config field is `Literal["claude"]` for now** — the type will be widened to `Literal["claude", "deepagents", "google-adk"]` when those backends are added. Using `Literal` gives pydantic validation for free. The field is config-file only; no env var override.

4. **`execute()` returns `AsyncIterator[AgentMessage]`** — matching the existing `ClaudeBackend.execute()` signature exactly. The `# type: ignore[attr-defined]` on the `async for` in `session.py` line 256 can be removed once the Protocol is in place.

5. **Unknown backend names raise `ConfigError`** from the factory, not `ValueError` — consistent with how `resolve_model()` handles unknown model names. `ConfigError` already exists at `agentfox.core.errors.ConfigError` and requires no new code.

6. **`name` property on the Protocol** — backends must identify themselves with a string name (e.g. `"claude"`, `"deepagents"`, `"google-adk"`). Used for logging and telemetry.

7. **`close()` is idempotent and async** — callers may invoke `close()` multiple times without error (e.g. from both a `finally` block and an exception handler). For `ClaudeBackend` it is a no-op. There are no additional thread-safety or concurrent-call guarantees beyond what `ClaudeBackend` already provides; the Protocol does not prescribe concurrency semantics.
