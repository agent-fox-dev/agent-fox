"""Error formatting utilities for AgentFoxGroup.

Converts exceptions into structured JSON error envelopes suitable
for agent-mode output.
"""

from __future__ import annotations

from typing import Any


def format_error_envelope(exc: Exception) -> dict[str, Any]:
    """Convert an exception into a JSON error envelope.

    Returns a dictionary with ``ok: False`` and an ``error`` field
    containing structured details about the exception.

    Args:
        exc: The exception to format.

    Returns:
        A dictionary suitable for passing to ``emit()``.
    """
    error_info: dict[str, Any] = {
        "type": type(exc).__name__,
        "message": str(exc),
    }

    # Include category if available (e.g. AgentError)
    if hasattr(exc, "category") and exc.category:  # type: ignore[attr-defined]
        error_info["category"] = exc.category  # type: ignore[attr-defined]

    # Include retryable flag if available
    if hasattr(exc, "retryable"):
        error_info["retryable"] = exc.retryable  # type: ignore[attr-defined]

    # Include HTTP status if available
    if hasattr(exc, "http_status") and exc.http_status:  # type: ignore[attr-defined]
        error_info["http_status"] = exc.http_status  # type: ignore[attr-defined]

    # Include cause if chained
    if exc.__cause__ is not None:
        error_info["cause"] = {
            "type": type(exc.__cause__).__name__,
            "message": str(exc.__cause__),
        }

    return {"ok": False, "error": error_info}
