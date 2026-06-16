"""Reporting modules for status and standup reports."""

from __future__ import annotations

import json


def parse_audit_payload(payload_raw: object) -> dict:
    """Parse a raw audit event payload into a dict.

    Handles str (JSON), dict, and unknown types gracefully.
    Used by both status and standup report builders.
    """
    try:
        if isinstance(payload_raw, str):
            return json.loads(payload_raw)
        if isinstance(payload_raw, dict):
            return payload_raw
        return {}
    except (json.JSONDecodeError, TypeError):
        return {}
