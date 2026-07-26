"""Shared HTTP retry infrastructure for platform implementations.

Requirements: 04-REQ-19.1, 04-REQ-19.2, 04-REQ-19.E1, 04-REQ-19.E2
"""

from __future__ import annotations

import asyncio
import logging

import httpx

logger = logging.getLogger(__name__)
_RETRYABLE_ERRORS = (httpx.ConnectTimeout, httpx.ConnectError, httpx.ReadTimeout)
_MAX_ERROR_TEXT = 500


def _truncate_response(text: str) -> str:
    if len(text) <= _MAX_ERROR_TEXT:
        return text
    return text[:_MAX_ERROR_TEXT] + "..."


async def request_with_retry(
    method: str,
    url: str,
    *,
    timeout: httpx.Timeout,
    transport: httpx.AsyncHTTPTransport | None = None,
    max_retries: int = 3,
    backoff_base: float = 1.0,
    **kwargs: object,
) -> httpx.Response:
    last_exc: Exception | None = None
    for attempt in range(max_retries):
        try:
            async with httpx.AsyncClient(timeout=timeout, transport=transport) as client:
                resp: httpx.Response = await getattr(client, method)(url, **kwargs)
                return resp
        except _RETRYABLE_ERRORS as exc:
            last_exc = exc
            if attempt < max_retries - 1:
                delay = backoff_base * (2**attempt)
                logger.warning(
                    "Transient error on attempt %d/%d, retrying in %.1fs: %s",
                    attempt + 1,
                    max_retries,
                    delay,
                    exc,
                )
                await asyncio.sleep(delay)
    raise last_exc  # type: ignore[misc]
