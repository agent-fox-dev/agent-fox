"""Shared retry constants for backend adapters.

Centralises the transport-layer retry parameters so that every backend
(``ClaudeBackend``, ``DeepAgentsBackend``, etc.) uses the same policy.

The exponential backoff formula is::

    delay(n) = _BACKOFF_BASE * 2 ** (n - 1)

giving delays of 1.0 s, 2.0 s, 4.0 s for the default ``_BACKOFF_BASE``
of ``1.0`` and a maximum of three attempts.

Requirements: 03-REQ-6.4
"""

_MAX_TRANSPORT_RETRIES: int = 3

_BACKOFF_BASE: float = 1.0
