"""DeepAgentsBackend adapter wrapping the ``deepagents`` SDK.

All ``deepagents`` SDK imports are confined to this module (03-REQ-1.2).
The adapter maps LangGraph ``astream_events()`` v2 event types to the
canonical ``AgentMessage`` types defined in ``types.py``.

If the ``deepagents`` package is not installed, importing this module raises
``ImportError`` at import time — exactly the behaviour required by
03-REQ-1.E1.  The ``create_backend()`` factory in ``__init__.py`` uses a
lazy import so that other backends remain unaffected.

Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-2.1
"""

from __future__ import annotations

import logging
import time
from collections.abc import AsyncIterator
from typing import Any

from deepagents import create_deep_agent  # noqa: F401
from langchain_core.tools import tool

from agentfox.session.backends._retry import (
    _BACKOFF_BASE,  # noqa: F401
    _MAX_TRANSPORT_RETRIES,  # noqa: F401
)
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

    async def execute(  # noqa: C901, PLR0912, PLR0913
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
        """Execute a session via Deep Agents and yield canonical messages.

        Creates a Deep Agents agent via ``create_deep_agent()``, then
        consumes the ``astream_events()`` v2 stream, mapping events to
        canonical ``AgentMessage`` instances.

        No exception is allowed to propagate out of this generator
        (03-REQ-2.9) — all failure modes surface through the terminal
        ``ResultMessage`` error fields.

        Note: Provider-specific parameter handling (thinking, effort,
        max_budget_usd fallbacks) and the transient retry loop are
        implemented in task groups 8–9.

        Requirements: 03-REQ-2.1–2.9, 03-REQ-3.1, 03-REQ-4.3, 03-REQ-5.4
        """
        start_time = time.monotonic()
        input_tokens_total: int | None = None
        output_tokens_total: int | None = None

        try:
            # Build tools and create agent (03-REQ-2.2, 03-REQ-3.1)
            tools = _build_af_sdk_tools()

            create_kwargs: dict[str, Any] = {
                "model": model,
                "system_prompt": system_prompt,
                "cwd": cwd,
                "tools": tools,
            }
            # 03-REQ-4.3: Never pass 'permissions' to create_deep_agent
            # 03-REQ-5.4: Never pass 'compaction' to create_deep_agent

            agent = create_deep_agent(**create_kwargs)
            self._agent = agent

            # Stream events from the agent (03-REQ-2.2)
            async for event in agent.astream_events(
                {"messages": [{"role": "human", "content": prompt}]},
                version="v2",
            ):
                try:
                    event_kind = event.get("event", "")

                    if event_kind == "on_chat_model_stream":
                        # 03-REQ-2.4: yield AssistantMessage for each chunk
                        chunk = event.get("data", {}).get("chunk")
                        if chunk is not None:
                            text = getattr(chunk, "content", str(chunk))
                            if text:
                                yield AssistantMessage(content=text)

                    elif event_kind == "on_tool_start":
                        # 03-REQ-2.3: yield ToolUseMessage for tool start
                        tool_name = event.get("name", "unknown")
                        tool_input = event.get("data", {}).get("input", {})
                        yield ToolUseMessage(
                            tool_name=tool_name,
                            tool_input=(tool_input if isinstance(tool_input, dict) else {}),
                        )

                    elif event_kind == "on_tool_end":
                        # 03-REQ-2.3: yield ToolUseMessage for tool end
                        tool_name = event.get("name", "unknown")
                        output = event.get("data", {}).get("output", "")
                        yield ToolUseMessage(
                            tool_name=tool_name,
                            tool_input={"output": str(output)},
                        )

                    elif event_kind == "on_llm_end":
                        # 03-REQ-2.5: accumulate token counts, yield nothing
                        output_data = event.get("data", {}).get("output")
                        if output_data is not None:
                            usage = getattr(output_data, "usage_metadata", None)
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
                    logger.warning("Skipping malformed event from astream_events: %s", exc)
                    continue

        except Exception as exc:
            # 03-REQ-2.9: no exception propagates — surface via ResultMessage
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

        # 03-REQ-2.6: terminal ResultMessage with accumulated token counts
        # 03-REQ-2.7 / Errata E5: use 0 when provider omits counts (int, not None)
        duration_ms = int((time.monotonic() - start_time) * 1000)
        yield ResultMessage(
            status="completed",
            input_tokens=input_tokens_total if input_tokens_total is not None else 0,
            output_tokens=(output_tokens_total if output_tokens_total is not None else 0),
            duration_ms=duration_ms,
            error_message=None,
            is_error=False,
        )

    async def close(self) -> None:
        """Release per-instance resources.  Must be idempotent.

        Safe to call multiple times; second and subsequent calls are no-ops.
        Does NOT perform ``asyncio.Task.cancel()`` or any async cancellation
        — mid-stream teardown is delegated to ``session.py`` via
        ``asyncio.wait_for()`` / async iterator cancellation.

        Requirements: 03-REQ-7.1, 03-REQ-7.2, 03-REQ-7.3, 03-REQ-7.4
        """
        self._agent = None
        self._checkpointer = None
        self._thread_state = None
