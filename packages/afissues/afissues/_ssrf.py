"""SSRF guard utilities shared across platform implementations.

Requirements: 04-REQ-18.1, 04-REQ-18.2, 580-AC-1, 580-AC-4, 580-AC-5
"""

from __future__ import annotations

import ipaddress
import logging
import socket
from urllib.parse import urlsplit

import httpx

from afissues.errors import ConfigError

logger = logging.getLogger(__name__)


def _check_address(addr: ipaddress.IPv4Address | ipaddress.IPv6Address, url: str) -> None:
    """Raise ConfigError if *addr* is in a restricted IP range."""
    if (
        addr.is_private
        or addr.is_loopback
        or addr.is_link_local
        or addr.is_reserved
        or addr.is_multicast
        or addr.is_unspecified
    ):
        raise ConfigError(f"URL {url!r} resolves to a restricted IP address: {addr}")


def _validate_url(url: str) -> None:
    """Validate *url* host for SSRF safety at construction time."""
    try:
        addr = ipaddress.ip_address(url)
        _check_address(addr, url)
        return
    except ValueError:
        pass
    parsed = urlsplit(f"https://{url}")
    host = parsed.hostname or url
    try:
        infos = socket.getaddrinfo(host, None)
    except (OSError, UnicodeError):
        logger.warning("DNS resolution failed for %r; allowing URL through", url)
        return
    for info in infos:
        addr_str = info[4][0]
        try:
            addr = ipaddress.ip_address(addr_str)
        except ValueError:
            continue
        _check_address(addr, url)


def _validate_transport_address(host: str) -> None:
    """Validate a resolved host address for SSRF safety at request time."""
    try:
        addr = ipaddress.ip_address(host)
        _check_address(addr, host)
        return
    except ValueError:
        pass
    try:
        infos = socket.getaddrinfo(host, None)
    except (OSError, UnicodeError):
        return
    for info in infos:
        addr_str = info[4][0]
        try:
            addr = ipaddress.ip_address(addr_str)
        except ValueError:
            continue
        _check_address(addr, host)


class SSRFGuardTransport(httpx.AsyncHTTPTransport):
    """Custom transport that rejects requests to private/loopback IPs."""

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        host = request.url.host
        if host:
            _validate_transport_address(host)
        return await super().handle_async_request(request)
