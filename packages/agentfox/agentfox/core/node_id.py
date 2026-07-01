"""Node ID parsing utilities and shared path constants.

Centralizes parsing of the ``{spec_name}:{group_number}[:{role}]`` format
used throughout the codebase for task graph node identifiers, and all
``.agent-fox/`` path constants.
"""

from __future__ import annotations

from pathlib import Path
from typing import NamedTuple

AGENT_FOX_DIR = ".agent-fox"
DEFAULT_DB_PATH = Path(".agent-fox/knowledge.duckdb")
AUDIT_DIR = Path(".agent-fox/audit")
SESSION_SUMMARY_FILENAME = "session-summary.json"


class ParsedNodeId(NamedTuple):
    """Components of a parsed node ID."""

    spec_name: str
    group_number: int
    role: str | None  # e.g. "reviewer", "verifier"


def parse_node_id(node_id: str) -> ParsedNodeId:
    """Parse a node ID into its components.

    Format: ``{spec_name}:{group_number}[:{role}]``

    Examples::

        >>> parse_node_id("11_echo_server:3")
        ParsedNodeId(spec_name='11_echo_server', group_number=3, role=None)
        >>> parse_node_id("11_echo_server:3:reviewer")
        ParsedNodeId(spec_name='11_echo_server', group_number=3, role='reviewer')
        >>> parse_node_id("11_echo_server")
        ParsedNodeId(spec_name='11_echo_server', group_number=0, role=None)
    """
    parts = node_id.split(":")
    spec_name = parts[0]
    group_number = int(parts[1]) if len(parts) > 1 else 0
    role = parts[2] if len(parts) > 2 else None
    return ParsedNodeId(spec_name, group_number, role)


def spec_name_of(node_id: str) -> str:
    """Extract just the spec name from a node ID.

    Equivalent to ``parse_node_id(node_id).spec_name`` but avoids
    parsing the group number.
    """
    idx = node_id.find(":")
    return node_id[:idx] if idx >= 0 else node_id
