"""Shared terminal IO module for agent-fox CLIs.

Re-exports exactly twelve curated public symbols for CLI output,
error formatting, progress display, and CLI group configuration.

``handle_cli_errors`` is intentionally NOT re-exported here;
import it directly from ``agentfox.io.errors`` if needed.

Requirements: 03-REQ-1
"""

from __future__ import annotations

from agentfox.io.cli import AgentFoxGroup, common_options
from agentfox.io.errors import error_envelope
from agentfox.io.help import exit_codes
from agentfox.io.json import emit, emit_error, emit_line, emit_ok, read_stdin
from agentfox.io.output import OutputManager, format_table, get_output_manager
from agentfox.io.progress import ProgressDisplay
from agentfox.io.spinner import StatusSpinner

__all__ = [
    "AgentFoxGroup",
    "OutputManager",
    "ProgressDisplay",
    "StatusSpinner",
    "common_options",
    "emit",
    "emit_error",
    "emit_line",
    "emit_ok",
    "error_envelope",
    "exit_codes",
    "format_table",
    "get_output_manager",
    "read_stdin",
]
