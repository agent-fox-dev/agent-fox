"""DeepAgentsBackend adapter wrapping the ``deepagents`` SDK.

All ``deepagents`` SDK imports are confined to this module (03-REQ-1.2).
The adapter maps LangGraph ``astream_events()`` v2 event types to the
canonical ``AgentMessage`` types defined in ``types.py``.

The ``create_backend()`` factory in ``__init__.py`` uses a lazy import so
that importing the backends package does not eagerly pull in all SDK
dependencies.

Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-2.1
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import AsyncIterator
from typing import Any

from deepagents import create_deep_agent
from langchain_core.tools import tool

from agentfox.session.backends._retry import _BACKOFF_BASE, _MAX_TRANSPORT_RETRIES
from agentfox.session.backends.types import (
    AgentMessage,
    AssistantMessage,
    PermissionCallback,
    ResultMessage,
    ToolUseMessage,
)
from agentfox.ui.progress import ActivityCallback

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Transient error classification (03-REQ-6.1, 03-REQ-6.3)
# ---------------------------------------------------------------------------
# PermissionError is a subclass of OSError in Python, so it must be
# explicitly excluded from transient classification.
_NON_TRANSIENT_ERROR_TYPES = (
    PermissionError,
    ValueError,
    TypeError,
    KeyError,
    AttributeError,
)

_TRANSIENT_ERROR_TYPES = (
    ConnectionError,
    OSError,
    TimeoutError,
)


# ---------------------------------------------------------------------------
# af SDK tool wrappers (03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3)
# ---------------------------------------------------------------------------
# Each wrapper is a thin synchronous function decorated with LangChain's
# ``@tool`` for automatic JSON-schema generation from type annotations.
#
# Errata E7: The named af SDK functions do not yet exist in the codebase.
# These are stub implementations that will be wired to real APIs when
# available.  Complete type annotations are provided so that LangChain
# schema generation succeeds (03-REQ-3.2).
# ---------------------------------------------------------------------------


@tool
def spec_read(spec_id: str) -> str:
    """Read the content of a specification by its identifier.

    Args:
        spec_id: The specification identifier to read.

    Returns:
        The specification content as a string.
    """
    return f"Specification '{spec_id}' not found"


@tool
def context_search(query: str) -> str:
    """Search the project context for relevant information.

    Args:
        query: The search query string.

    Returns:
        Matching context entries as a string.
    """
    return f"No context results for query: {query}"


@tool
def context_get(key: str) -> str:
    """Retrieve a specific context item by its key.

    Args:
        key: The context item key to retrieve.

    Returns:
        The context item value as a string.
    """
    return f"Context key '{key}' not found"


@tool
def memory_recall(topic: str) -> str:
    """Recall memory entries related to a topic.

    Args:
        topic: The topic to recall memories for.

    Returns:
        Related memory entries as a string.
    """
    return f"No memory entries for topic: {topic}"


@tool
def subtask_state(task_id: str) -> str:
    """Query the current state of a subtask.

    Args:
        task_id: The subtask identifier to query.

    Returns:
        The subtask state information as a string.
    """
    return f"No state found for task: {task_id}"


def _build_af_sdk_tools() -> list[Any]:
    """Build the list of five af SDK LangChain tools for ``create_deep_agent``.

    Returns a list of five ``BaseTool`` instances wrapping the af SDK
    functions.

    Requirements: 03-REQ-3.1
    """
    return [spec_read, context_search, context_get, memory_recall, subtask_state]


def _is_transient_error(exc: Exception) -> bool:
    """Classify an exception as transient (retriable) or non-transient.

    Transient: connection failures, timeouts, OS-level I/O errors.
    Non-transient: auth failures, value errors, permission errors.

    PermissionError is a subclass of OSError but is non-transient.
    """
    if isinstance(exc, _NON_TRANSIENT_ERROR_TYPES):
        return False
    return isinstance(exc, _TRANSIENT_ERROR_TYPES)


# ---------------------------------------------------------------------------
# Provider-specific parameter fallback (03-REQ-5.1, 03-REQ-5.2, 03-REQ-5.3)
# ---------------------------------------------------------------------------
# Parameters that may not be supported by all ``create_deep_agent()`` versions
# are tried first and removed on ``TypeError``.  The ``create_kwargs`` dict is
# modified **in place** so that once a parameter is removed it stays removed
# across transport-level retries.
_OPTIONAL_FALLBACK_PARAMS = ("thinking", "max_budget_usd", "effort")


def _create_agent_with_fallback(create_kwargs: dict[str, Any]) -> Any:
    """Call ``create_deep_agent`` with progressive parameter fallback.

    When ``TypeError`` is raised and its message mentions one of the
    known optional parameters, that parameter is removed from
    *create_kwargs* **in place** and the call is retried.  This avoids
    re-attempting unsupported parameters on subsequent transport retries.

    Parameters that do not appear in ``_OPTIONAL_FALLBACK_PARAMS`` cause
    the ``TypeError`` to propagate.

    Requirements: 03-REQ-5.1, 03-REQ-5.2, 03-REQ-5.3
    """
    while True:
        try:
            return create_deep_agent(**create_kwargs)
        except TypeError as exc:
            exc_msg = str(exc)
            removed = False
            for param in _OPTIONAL_FALLBACK_PARAMS:
                if param in create_kwargs and param in exc_msg:
                    logger.debug(
                        "create_deep_agent does not support '%s', retrying without it",
                        param,
                    )
                    del create_kwargs[param]
                    removed = True
                    break
            if not removed:
                raise


# ---------------------------------------------------------------------------
# DeepAgentsBackend adapter
# ---------------------------------------------------------------------------


class DeepAgentsBackend:
    """Backend adapter wrapping Deep Agents (LangChain-based).

    Structurally satisfies the ``Backend`` Protocol from ``protocol.py``
    so that ``isinstance(DeepAgentsBackend(), Backend)`` returns ``True``.

    All ``deepagents`` SDK imports are confined to this module.

    Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3
    """

    def __init__(self) -> None:
        self._agent: Any | None = None
        self._checkpointer: Any | None = None
        self._thread_state: Any | None = None

    @property
    def name(self) -> str:
        """Return the backend identifier string."""
        return "deepagents"

    def _build_create_kwargs(  # noqa: PLR0913
        self,
        *,
        model: str,
        system_prompt: str,
        cwd: str,
        tools: list[Any],
        max_budget_usd: float | None = None,
        thinking: dict[str, Any] | None = None,
        effort: str | None = None,
    ) -> dict[str, Any]:
        """Assemble kwargs for ``create_deep_agent()``.

        Adds provider-specific parameters only when appropriate:
        - ``thinking`` is included only for ``anthropic:`` prefix models.
        - ``max_budget_usd`` and ``effort`` are included when non-None.

        Never includes ``permissions`` (03-REQ-4.3) or ``compaction``
        (03-REQ-5.4).
        """
        kwargs: dict[str, Any] = {
            "model": model,
            "system_prompt": system_prompt,
            "cwd": cwd,
            "tools": tools,
        }

        # 03-REQ-5.1: thinking only for anthropic: prefix models
        if model.startswith("anthropic:"):
            kwargs["thinking"] = thinking if thinking is not None else {}

        # 03-REQ-5.2, 03-REQ-5.3: optional params forwarded when present
        if max_budget_usd is not None:
            kwargs["max_budget_usd"] = max_budget_usd
        if effort is not None:
            kwargs["effort"] = effort

        # 03-REQ-4.3: Never pass 'permissions'
        # 03-REQ-5.4: Never pass 'compaction'
        return kwargs

    @staticmethod
    def _create_agent_with_fallback(
        create_kwargs: dict[str, Any],
    ) -> Any:
        """Call ``create_deep_agent()`` with graceful TypeError fallback.

        Tries the full set of kwargs first.  If ``TypeError`` is raised
        (unsupported parameter in the installed version), removes the
        offending optional parameter and retries.

        Droppable parameters:
        - ``thinking`` -- removed silently (provider-level per 03-REQ-5.1)
        - ``max_budget_usd`` -- removed with DEBUG log (03-REQ-5.2)
        - ``effort`` -- removed with DEBUG log (03-REQ-5.3)

        Returns the created agent instance.
        """
        # Map of optional params that can be dropped on TypeError.
        # Value is True if a DEBUG log should be emitted on removal.
        droppable: dict[str, bool] = {
            "thinking": False,
            "max_budget_usd": True,
            "effort": True,
        }

        kwargs = dict(create_kwargs)
        while True:
            try:
                return create_deep_agent(**kwargs)
            except TypeError as exc:
                dropped = False
                for param_name, log_debug in droppable.items():
                    if param_name in kwargs:
                        del kwargs[param_name]
                        if log_debug:
                            logger.debug(
                                "create_deep_agent does not support '%s'; retrying without it",
                                param_name,
                            )
                        dropped = True
                        break
                if not dropped:
                    raise exc  # noqa: TRY201

    async def execute(  # noqa: C901, PLR0912, PLR0913, PLR0915
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
        cache_policy: str = "NONE",
        **kwargs: Any,
    ) -> AsyncIterator[AgentMessage]:
        """Execute a session via Deep Agents and yield canonical messages.

        Creates a Deep Agents agent via ``create_deep_agent()``, then
        consumes the ``astream_events()`` v2 stream, mapping events to
        canonical ``AgentMessage`` instances.

        Includes:
        - Provider-specific parameter handling with TypeError fallback
          (03-REQ-5.1-5.4).
        - Transient error retry with exponential backoff, up to 3 attempts
          (03-REQ-6.1-6.4).
        - Permission callback interrupt mapping (03-REQ-4.1-4.3).
        - No exception propagation guarantee (03-REQ-2.9).

        Requirements: 03-REQ-2.1-2.9, 03-REQ-3.1, 03-REQ-4.1-4.3,
                      03-REQ-5.1-5.4, 03-REQ-6.1-6.4
        """
        start_time = time.monotonic()

        # Build tools and assemble create_deep_agent kwargs
        tools = _build_af_sdk_tools()
        create_kwargs = self._build_create_kwargs(
            model=model,
            system_prompt=system_prompt,
            cwd=cwd,
            tools=tools,
            max_budget_usd=max_budget_usd,
            thinking=thinking,
            effort=effort,
        )

        last_exc: Exception | None = None

        # 03-REQ-6.1: Retry loop bounded to _MAX_TRANSPORT_RETRIES attempts.
        # Each retry creates a fresh agent and discards events from failed
        # attempts (03-REQ-6.E2).  Events are buffered per attempt so that
        # messages from a failed attempt are never yielded to the caller.
        for attempt in range(_MAX_TRANSPORT_RETRIES):
            if attempt > 0:
                delay = _BACKOFF_BASE * (2 ** (attempt - 1))
                logger.info(
                    "DeepAgentsBackend: transport retry %d/%d after %.1fs",
                    attempt,
                    _MAX_TRANSPORT_RETRIES,
                    delay,
                )
                await asyncio.sleep(delay)

            input_tokens_total: int | None = None
            output_tokens_total: int | None = None
            buffered_msgs: list[AgentMessage] = []

            try:
                # Fresh agent for each attempt (03-REQ-6.E2)
                agent = self._create_agent_with_fallback(create_kwargs)
                self._agent = agent

                # Stream events (03-REQ-2.2)
                async for event in agent.astream_events(
                    {"messages": [{"role": "human", "content": prompt}]},
                    version="v2",
                ):
                    try:
                        event_kind = event.get("event", "")

                        if not event_kind:
                            logger.warning(
                                "Skipping malformed event with no 'event' field: %s",
                                event,
                            )
                            continue

                        if event_kind == "on_chat_model_stream":
                            # 03-REQ-2.4: buffer AssistantMessage for each chunk
                            chunk = event.get("data", {}).get("chunk")
                            if chunk is not None:
                                text = getattr(chunk, "content", str(chunk))
                                if text:
                                    buffered_msgs.append(
                                        AssistantMessage(content=text),
                                    )

                        elif event_kind == "on_tool_start":
                            # 03-REQ-2.3: buffer ToolUseMessage for tool start
                            tool_name = event.get("name", "unknown")
                            tool_input = event.get("data", {}).get(
                                "input",
                                {},
                            )

                            # 03-REQ-4.1: permission callback (async)
                            if permission_callback is not None:
                                try:
                                    await permission_callback(
                                        tool_name,
                                        tool_input if isinstance(tool_input, dict) else {},
                                    )
                                except Exception as cb_exc:
                                    # 03-REQ-4.E1: deny + error
                                    duration_ms = int(
                                        (time.monotonic() - start_time) * 1000,
                                    )
                                    yield ResultMessage(
                                        status="error",
                                        input_tokens=(input_tokens_total or 0),
                                        output_tokens=(output_tokens_total or 0),
                                        duration_ms=duration_ms,
                                        error_message=str(cb_exc),
                                        is_error=True,
                                        is_transport_error=False,
                                    )
                                    return

                            buffered_msgs.append(
                                ToolUseMessage(
                                    tool_name=tool_name,
                                    tool_input=(tool_input if isinstance(tool_input, dict) else {}),
                                ),
                            )

                        elif event_kind == "on_tool_end":
                            # 03-REQ-2.3: buffer ToolUseMessage for tool end
                            tool_name = event.get("name", "unknown")
                            output = event.get("data", {}).get("output", "")
                            buffered_msgs.append(
                                ToolUseMessage(
                                    tool_name=tool_name,
                                    tool_input={"output": str(output)},
                                ),
                            )

                        elif event_kind == "on_llm_end":
                            # 03-REQ-2.5: accumulate token counts
                            output_data = event.get("data", {}).get("output")
                            if output_data is not None:
                                usage = getattr(
                                    output_data,
                                    "usage_metadata",
                                    None,
                                )
                                if usage is not None:
                                    inp = usage.get("input_tokens")
                                    out = usage.get("output_tokens")
                                    if inp is not None:
                                        input_tokens_total = (input_tokens_total or 0) + inp
                                    if out is not None:
                                        output_tokens_total = (output_tokens_total or 0) + out

                        # Unknown event kinds are silently ignored

                    except (KeyError, AttributeError, TypeError) as exc:
                        # 03-REQ-2.8: skip malformed events with WARNING
                        logger.warning(
                            "Skipping malformed event from astream_events: %s",
                            exc,
                        )
                        continue

                # Stream completed successfully — yield buffered messages
                # and terminal ResultMessage, then return.
                for msg in buffered_msgs:
                    yield msg

                # 03-REQ-2.6 / Errata E5: use 0 when provider omits counts
                duration_ms = int((time.monotonic() - start_time) * 1000)
                yield ResultMessage(
                    status="completed",
                    input_tokens=(input_tokens_total if input_tokens_total is not None else 0),
                    output_tokens=(output_tokens_total if output_tokens_total is not None else 0),
                    duration_ms=duration_ms,
                    error_message=None,
                    is_error=False,
                )
                return

            except asyncio.CancelledError:
                # 03-REQ-2.E2: Do not retry cancellation — propagate so
                # the asyncio task is properly cancelled.
                raise

            except Exception as exc:
                last_exc = exc

                if _is_transient_error(exc):
                    # 03-REQ-6.1: Transient — retry after backoff
                    logger.warning(
                        "DeepAgentsBackend transport error (attempt %d/%d): %s",
                        attempt + 1,
                        _MAX_TRANSPORT_RETRIES,
                        exc,
                    )
                    # 03-REQ-6.E2: Discard partial state before retry
                    self._agent = None
                    continue

                # 03-REQ-6.3: Non-transient — yield error immediately
                duration_ms = int((time.monotonic() - start_time) * 1000)
                yield ResultMessage(
                    status="error",
                    input_tokens=input_tokens_total or 0,
                    output_tokens=output_tokens_total or 0,
                    duration_ms=duration_ms,
                    error_message=str(exc),
                    is_error=True,
                    is_transport_error=False,
                )
                return

        # 03-REQ-6.2: All transport retries exhausted
        logger.error(
            "DeepAgentsBackend: all %d transport retries exhausted; last error: %s",
            _MAX_TRANSPORT_RETRIES,
            str(last_exc),
        )
        duration_ms = int((time.monotonic() - start_time) * 1000)
        yield ResultMessage(
            status="error",
            input_tokens=0,
            output_tokens=0,
            duration_ms=duration_ms,
            error_message=(f"Transport error after {_MAX_TRANSPORT_RETRIES} retries: {last_exc}"),
            is_error=True,
            is_transport_error=True,
        )

    async def close(self) -> None:
        """Release per-instance resources.  Must be idempotent.

        Safe to call multiple times; second and subsequent calls are no-ops.
        Does NOT perform ``asyncio.Task.cancel()`` or any async cancellation
        -- mid-stream teardown is delegated to ``session.py`` via
        ``asyncio.wait_for()`` / async iterator cancellation.

        Requirements: 03-REQ-7.1, 03-REQ-7.2, 03-REQ-7.3, 03-REQ-7.4
        """
        self._agent = None
        self._checkpointer = None
        self._thread_state = None
