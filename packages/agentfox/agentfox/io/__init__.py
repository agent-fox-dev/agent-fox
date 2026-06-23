"""Shared terminal IO module for agent-fox CLIs.

Provides AgentFoxGroup (Click group with agent-mode detection, banner
suppression, and unified error routing), OutputManager utilities
(emit, emit_ok), and StatusSpinner for stderr progress feedback.

This module is the canonical import path for all CLI IO utilities.
"""

from __future__ import annotations

from agentfox.io.group import AgentFoxGroup
from agentfox.io.output import emit, emit_error, emit_line, emit_ok, read_stdin
from agentfox.io.spinner import StatusSpinner

__all__ = [
    "AgentFoxGroup",
    "StatusSpinner",
    "emit",
    "emit_error",
    "emit_line",
    "emit_ok",
    "read_stdin",
]
