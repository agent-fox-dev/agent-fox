"""Shared path constants for audit infrastructure.

This module is the single authoritative definition of ``AUDIT_DIR``.
"""

from __future__ import annotations

from pathlib import Path

AUDIT_DIR = Path(".agent-fox/audit")
