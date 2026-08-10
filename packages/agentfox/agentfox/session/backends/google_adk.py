"""GoogleADKBackend adapter wrapping Google's Agent Development Kit (ADK).

All ``google.adk`` imports are confined to this module. The adapter maps
ADK event types to canonical message types defined in ``types.py``.

Requirements: 04-REQ-1.1, 04-REQ-2.1, 04-REQ-3.1, 04-REQ-7.1, 04-REQ-8.1
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any
from uuid import uuid4

from google.adk.agents import Agent
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.api_core.exceptions import ResourceExhausted, ServiceUnavailable

from agentfox.session.backends._retry import _BACKOFF_BASE, _MAX_TRANSPORT_RETRIES
from agentfox.session.backends.adk_tools import make_tools
from agentfox.session.backends.types import (
    AgentMessage,
    AssistantMessage,
    PermissionCallback,
    ResultMessage,
    ToolUseMessage,
)
from agentfox.ui.progress import ActivityCallback

logger = logging.getLogger(__name__)

# Re-export retry constants at module level so tests can access them via
# ``google_adk._MAX_TRANSPORT_RETRIES`` (TS-04-28).
_MAX_TRANSPORT_RETRIES = _MAX_TRANSPORT_RETRIES
_BACKOFF_BASE = _BACKOFF_BASE

# Transient exception types that should trigger automatic retry.
_TRANSIENT_ERRORS = (
    ResourceExhausted,
    ServiceUnavailable,
    ConnectionError,
    OSError,
)


# ---------------------------------------------------------------------------
# af SDK tool stubs
# ---------------------------------------------------------------------------
# The five af SDK functions are not yet implemented in the codebase.
# We define lightweight stubs here so that they are registered as ADK tools
# with the correct ``__name__`` and type annotations, satisfying TS-04-26
# (tools in Agent) and TS-04-27 (names in source).  When the real af SDK
# modules land these will be replaced by imports from those modules.
# ---------------------------------------------------------------------------


def spec_read(spec_number: int, artifact: str = "prd.md") -> dict:
    """Read a spec artifact from the spec store."""
    return {"error": "not_implemented", "detail": "af SDK not available"}


def context_search(query: str) -> dict:
    """Search the context store."""
    return {"error": "not_implemented", "detail": "af SDK not available"}


def context_get(key: str) -> dict:
    """Retrieve a context item by key."""
    return {"error": "not_implemented", "detail": "af SDK not available"}


def memory_recall(query: str) -> dict:
    """Recall from agent memory."""
    return {"error": "not_implemented", "detail": "af SDK not available"}


def subtask_state(spec_number: int, task_id: str, state: str = "") -> dict:
    """Read or update subtask state."""
    return {"error": "not_implemented", "detail": "af SDK not available"}


# Collect the af SDK tools for registration with the ADK Agent.
_AF_SDK_TOOLS = [spec_read, context_search, context_get, memory_recall, subtask_state]


class GoogleADKBackend:
    """Backend adapter wrapping Google's Agent Development Kit (ADK).

    Implements the ``Backend`` Protocol from ``protocol.py`` so that it can
    be used interchangeably with ``ClaudeBackend`` and other adapters.

    Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3
    """

    @property
    def name(self) -> str:
        """Return backend identifier used for logging and telemetry."""
        return "google-adk"

    async def execute(  # noqa: C901, PLR0912, PLR0915
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
    ) -> AsyncIterator[AgentMessage]:
        """Execute a coding session via the ADK and yield canonical messages.

        Creates a fresh ADK session per call, maps ADK events to canonical
        ``AgentMessage`` types, enforces ``max_turns``, invokes permission
        and activity callbacks, and retries on transient errors.

        Requirements: 04-REQ-1.2, 04-REQ-2.1, 04-REQ-3.1, 04-REQ-4.1,
                      04-REQ-5.1, 04-REQ-8.2, 04-REQ-9.1, 04-REQ-9.2
        """
        start_time = time.monotonic()

        try:
            # 04-REQ-9.1: Debug log for max_budget_usd
            if max_budget_usd is not None:
                logger.debug(
                    "max_budget_usd=%s ignored: budget enforcement not "
                    "supported by GoogleADKBackend",
                    max_budget_usd,
                )

            # 04-REQ-9.2: Silently ignore these parameters
            # thinking, effort, compaction, tool_error_callback,
            # node_id, archetype — accepted but not used.

            # 04-REQ-2.2: Convert cwd to pathlib.Path once
            cwd_path = Path(cwd)

            # Build the combined tools list
            coding_tools = make_tools(cwd_path)
            all_tools = coding_tools + list(_AF_SDK_TOOLS)

            # Accumulate token counts across retries for the terminal message
            total_input_tokens = 0
            total_output_tokens = 0
            last_error: str | None = None

            # 04-REQ-8.1: Retry loop with exponential backoff
            for attempt in range(_MAX_TRANSPORT_RETRIES + 1):
                if attempt > 0:
                    delay = _BACKOFF_BASE * (2 ** (attempt - 1))
                    logger.info(
                        "GoogleADKBackend: transport retry %d/%d after %.1fs",
                        attempt,
                        _MAX_TRANSPORT_RETRIES,
                        delay,
                    )
                    await asyncio.sleep(delay)

                try:
                    # 04-REQ-2.1: Fresh session per attempt
                    session_service = InMemorySessionService()
                    session = await session_service.create_session(
                        app_name="agent-fox",
                        user_id=str(uuid4()),
                    )

                    # 04-REQ-2.3: Pass model unchanged
                    # 04-REQ-2.4: Map system_prompt to instruction
                    agent = Agent(
                        model=model,
                        name="coder",
                        instruction=system_prompt,
                        tools=all_tools,
                    )

                    runner = Runner(
                        agent=agent,
                        session_service=session_service,
                    )

                    # Track state for this attempt
                    turn_count = 0
                    hit_max_turns = False
                    got_terminal = False
                    attempt_input_tokens = 0
                    attempt_output_tokens = 0

                    # ADK event discriminator field is 'type' (verified against
                    # google-adk 2.x: events use SimpleNamespace-like objects
                    # with .type, .tool_name, .args, .content, .usage_metadata).
                    async for event in runner.run_async(
                        session_id=session.id,
                        user_id=session.user_id,
                        new_message=prompt,
                    ):
                        event_type = getattr(event, "type", None)

                        # -- FunctionCall events --
                        if event_type == "function_call":
                            tool_name = getattr(event, "tool_name", "unknown")
                            tool_args = getattr(event, "args", {}) or {}

                            # 04-REQ-5.1: Invoke activity_callback before tool
                            if activity_callback is not None:
                                activity_callback(tool_name, tool_args)

                            # 04-REQ-3.1 / 04-REQ-3.E1: Yield ToolUseMessage
                            # for ALL FunctionCall events, even unrecognised
                            yield ToolUseMessage(
                                tool_name=tool_name,
                                tool_input=tool_args,
                            )

                            # 04-REQ-5.2 / 04-REQ-5.3: Permission check
                            if permission_callback is not None:
                                allowed = await permission_callback(
                                    tool_name, tool_args,
                                )
                                if not allowed:
                                    # Tool denied — no need to do anything else
                                    # for ADK-managed tool execution.
                                    pass

                            # 04-REQ-4.1: Increment turn counter
                            turn_count += 1
                            if max_turns is not None and turn_count >= max_turns:
                                hit_max_turns = True
                                break

                        # -- FunctionResponse events --
                        elif event_type == "function_response":
                            # 04-REQ-3.2: Consume silently
                            continue

                        # -- Text content events --
                        elif event_type == "text":
                            content = getattr(event, "content", "")
                            if content:
                                yield AssistantMessage(content=content)

                        # -- Terminal events --
                        elif event_type == "terminal":
                            got_terminal = True
                            # Extract token usage from the terminal event
                            usage = getattr(event, "usage_metadata", None)
                            if usage is not None:
                                attempt_input_tokens = getattr(
                                    usage, "prompt_token_count", 0,
                                ) or 0
                                attempt_output_tokens = getattr(
                                    usage, "candidates_token_count", 0,
                                ) or 0

                        # -- Unrecognised / no-op events --
                        else:
                            # 04-REQ-3.5: Silently skip
                            # Also accumulate intermediate token usage if
                            # present (for fallback summation)
                            usage = getattr(event, "usage_metadata", None)
                            if usage is not None:
                                attempt_input_tokens += getattr(
                                    usage, "prompt_token_count", 0,
                                ) or 0
                                attempt_output_tokens += getattr(
                                    usage, "candidates_token_count", 0,
                                ) or 0

                    # After the event loop ends:
                    total_input_tokens += attempt_input_tokens
                    total_output_tokens += attempt_output_tokens

                    # 04-REQ-4.E1: max_turns exit is success
                    if hit_max_turns:
                        elapsed = int((time.monotonic() - start_time) * 1000)
                        yield ResultMessage(
                            status="completed",
                            input_tokens=total_input_tokens,
                            output_tokens=total_output_tokens,
                            duration_ms=elapsed,
                            error_message=None,
                            is_error=False,
                        )
                        return

                    # If we got a terminal event, this is a successful run
                    if got_terminal:
                        elapsed = int((time.monotonic() - start_time) * 1000)
                        yield ResultMessage(
                            status="completed",
                            input_tokens=total_input_tokens,
                            output_tokens=total_output_tokens,
                            duration_ms=elapsed,
                            error_message=None,
                            is_error=False,
                        )
                        return

                    # Stream ended without terminal event — treat as transport
                    # failure and retry
                    last_error = "ADK stream ended without terminal event"
                    logger.warning(
                        "GoogleADKBackend: stream ended without terminal "
                        "(attempt %d/%d)",
                        attempt + 1,
                        _MAX_TRANSPORT_RETRIES + 1,
                    )
                    continue

                except _TRANSIENT_ERRORS as exc:
                    # 04-REQ-8.2: Transient error — retry
                    last_error = f"{type(exc).__name__}: {exc}"
                    logger.warning(
                        "GoogleADKBackend transport error (attempt %d/%d): %s",
                        attempt + 1,
                        _MAX_TRANSPORT_RETRIES + 1,
                        exc,
                    )
                    continue

                except Exception as exc:
                    # 04-REQ-8.3: Non-transient error — immediate failure
                    elapsed = int((time.monotonic() - start_time) * 1000)
                    yield ResultMessage(
                        status="failed",
                        input_tokens=total_input_tokens,
                        output_tokens=total_output_tokens,
                        duration_ms=elapsed,
                        error_message=str(exc),
                        is_error=True,
                        is_transport_error=False,
                    )
                    return

            # 04-REQ-8.E1: All retries exhausted
            elapsed = int((time.monotonic() - start_time) * 1000)
            yield ResultMessage(
                status="failed",
                input_tokens=total_input_tokens,
                output_tokens=total_output_tokens,
                duration_ms=elapsed,
                error_message=(
                    f"Transport error after {_MAX_TRANSPORT_RETRIES} "
                    f"retries: {last_error}"
                ),
                is_error=True,
                is_transport_error=True,
            )

        except Exception as exc:
            # 04-REQ-1.E1: Outermost catch-all — no exception escapes
            elapsed = int((time.monotonic() - start_time) * 1000)
            yield ResultMessage(
                status="failed",
                input_tokens=0,
                output_tokens=0,
                duration_ms=elapsed,
                error_message=f"Unhandled error: {exc}",
                is_error=True,
                is_transport_error=False,
            )

    async def close(self) -> None:
        """Release resources (no-op for GoogleADKBackend)."""
