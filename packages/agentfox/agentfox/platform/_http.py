"""Shared HTTP retry helper for platform implementations.

Provides ``request_with_retry``, an async function that wraps
``httpx.AsyncClient`` method calls with retry-on-transient-error logic
and exponential backoff.  Creates a new ``AsyncClient`` per invocation
(no persistent client state).

Extracted from ``GitHubPlatform._request`` (spec 04) so that both
``GitHubPlatform`` and ``GitLabPlatform`` share the same retry strategy.

Not re-exported from ``agentfox.platform.__init__``; the leading
underscore marks this module as internal.

Requirements: 04-REQ-19.1, 04-REQ-19.2
"""

from __future__ import annotations

import asyncio
import logging

import httpx

logger = logging.getLogger(__name__)

# Transport-level errors that are safe to retry (network blips, DNS timeouts).
_RETRYABLE_ERRORS = (httpx.ConnectTimeout, httpx.ConnectError, httpx.ReadTimeout)


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
    """Execute an HTTP request with retry on transient network errors.

    Creates a new ``httpx.AsyncClient`` per call via
    ``async with httpx.AsyncClient(...)``.  Delegates to
    ``getattr(client, method)(url, **kwargs)``.

    Retries on ``ConnectTimeout``, ``ConnectError``, and ``ReadTimeout``
    up to *max_retries* attempts with exponential backoff starting at
    *backoff_base* seconds (sequence: 1 s, 2 s, 4 s for defaults).

    HTTP-level responses (4xx, 5xx including 429) are returned as-is
    without retrying — the calling platform method decides whether to
    raise ``IntegrationError``.

    After all retries are exhausted the last exception is re-raised.

    Requirements: 04-REQ-19.1, 04-REQ-19.2, 04-REQ-19.E1, 04-REQ-19.E2
    """
    last_exc: Exception | None = None
    for attempt in range(max_retries):
        try:
            async with httpx.AsyncClient(
                timeout=timeout,
                transport=transport,
            ) as client:
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
