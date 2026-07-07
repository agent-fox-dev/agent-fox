# Errata: Spec 03 — DeepAgentsBackend

Several spec 03 assumptions diverge from the actual codebase. This document
records each divergence and the adaptation applied in the implementation.

## E1: No Backend Protocol class

**Spec says:** `isinstance(DeepAgentsBackend(), Backend)` returns `True`
via a `Backend` Protocol in `session/backends/types.py` (03-REQ-1.1).

**Reality:** No `Backend` Protocol exists. The codebase uses duck typing.
`types.py` only defines message dataclasses and the `PermissionCallback`
type alias.

**Adaptation:** Tests verify structural conformance (has `execute` and
`close` methods with correct signatures) instead of isinstance checks.
If a `@runtime_checkable` Backend Protocol is added by the implementation
group, the isinstance check is also performed.

## E2: No create_backend() factory function

**Spec says:** `create_backend("deepagents")` in `session/backends/__init__.py`
(03-REQ-8.1-8.3). Also says "no changes to `__init__.py`'s existing logic"
(03-REQ-8.3).

**Reality:** No factory exists. `__init__.py` only re-exports. `session.py`
directly instantiates `ClaudeBackend()` at line 130.

**Adaptation:** Tests reference `create_backend` and will fail until the
implementation group creates it. The contradiction between "add factory" and
"don't change __init__.py" is resolved by adding the factory as new code
alongside existing exports, not modifying existing logic.

## E3: No OrchestratorConfig.backend field

**Spec says:** Widen `OrchestratorConfig.backend` from `Literal["claude"]`
to `Literal["claude", "deepagents"]` (03-REQ-9.1-9.2).

**Reality:** `OrchestratorConfig` has no `backend` field. Backend selection
is implicit via `session.py` hardcoding `ClaudeBackend()`.

**Adaptation:** Tests verify the `backend` field once added by the
implementation group (task 6.4).

## E4: PermissionCallback is async, not sync

**Spec says:** `PermissionCallback = Callable[[str, dict], bool]` (sync).

**Reality:** `PermissionCallback = Callable[[str, dict[str, Any]], Awaitable[bool]]`
(async). The callback must be `await`ed.

**Adaptation:** Implementation must `await` the permission callback, not
call it synchronously.

## E5: ResultMessage.input_tokens is int, not Optional

**Spec says:** Token fields should be `None` when unavailable (03-REQ-2.7).

**Reality:** `ResultMessage.input_tokens: int` and `output_tokens: int`
are required, non-optional integers. `ClaudeBackend` uses `0` for missing
tokens. Setting `None` would violate the frozen dataclass type contract.

**Adaptation:** Use `0` for missing tokens, consistent with ClaudeBackend.
Since spec 03-REQ-1.3 forbids modifying `types.py`, changing the field
types is not an option.

## E6: ClaudeBackend.close() is async

**Spec says:** `close()` should be "synchronous or at most a trivial
coroutine" (03-REQ-7.4).

**Reality:** `ClaudeBackend.close()` is `async def close(self) -> None`.

**Adaptation:** `DeepAgentsBackend.close()` should match `ClaudeBackend`'s
signature as `async def close(self) -> None` for drop-in compatibility.

## E7: No af SDK functions exist

**Spec says:** Five af SDK functions (`spec_read`, `context_search`,
`context_get`, `memory_recall`, `subtask_state`) must be wrapped as
LangChain tools (03-REQ-3.1-3.3).

**Reality:** None of these functions exist in any package. The af and afspec
packages expose entirely different APIs.

**Adaptation:** The implementation group must either create these functions
or map existing af/afspec APIs to the tool wrappers. Tool registration tests
will need to adapt to whatever functions are available.

## E8: No CI workflow files

**Spec says:** CI workflow in `.github/workflows/` must include a deepagents
matrix leg (03-REQ-10.3).

**Reality:** No `.github/workflows/` directory exists. The project uses
`Makefile` for quality gates (`make check`, `make test`).

**Adaptation:** The CI matrix leg test checks both `.github/workflows/`
YAML files and the `Makefile` for deepagents support.

## E9: Containment test uses content scan, not mapping dict

**Spec says:** Containment test has a mapping dict `"deepagents" -> "deepagents.py"`
(03-REQ-12.1).

**Reality:** The existing containment test in `test_protocol.py` scans file
contents for the string `claude_agent_sdk`, not a mapping dictionary. Also,
it scans an `agent_fox` directory (underscore) that doesn't match the actual
`agentfox` directory (no underscore).

**Adaptation:** New containment tests use the same content-scan pattern but
navigate from the importable `agentfox` package location for reliability.

## E10: Spec 02 dependency unfulfilled

**Spec says:** Spec 02 establishes the Backend Protocol and `create_backend()`
factory (Dependencies table).

**Reality:** Spec 02 did not create either. The codebase has no formal
Protocol class, no factory function, and no backend registry.

**Adaptation:** These foundational artifacts must be created as part of
spec 03 implementation, contrary to the dependency assumption.
