"""Specification discovery: scan .specs/ for valid spec folders.

Requirements: 02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.3, 02-REQ-1.E1, 02-REQ-1.E2
             132-REQ-2.1, 132-REQ-2.2, 132-REQ-3.1, 132-REQ-3.2, 132-REQ-3.3, 132-REQ-3.4
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path

from agent_fox.core.errors import PlanError

logger = logging.getLogger(__name__)

# Pattern: numeric prefix (2+ digits), underscore, descriptive name
_SPEC_DIR_PATTERN = re.compile(r"^(\d+)_(.+)$")


class SpecFormat(Enum):
    """Discriminator for spec folder format.

    132-REQ-2.1: V1_MARKDOWN for legacy markdown specs, V1_2_JSON for new JSON specs.
    """

    V1_MARKDOWN = "v1_markdown"
    V1_2_JSON = "v1_2_json"


@dataclass(frozen=True)
class SpecInfo:
    """Metadata about a discovered specification folder."""

    name: str  # e.g., "01_core_foundation"
    prefix: int  # e.g., 1
    path: Path  # e.g., Path(".specs/01_core_foundation")
    has_tasks: bool  # whether tasks.json (v1.2) or tasks.md (v1) exists
    has_prd: bool  # whether prd.md exists
    format: SpecFormat = field(default=SpecFormat.V1_MARKDOWN)  # 132-REQ-2.2


def _detect_format(spec_dir: Path) -> SpecFormat:
    """Detect whether a spec folder uses v1 (markdown) or v1.2 (JSON) format.

    132-REQ-3.1: requirements.json present → V1_2_JSON
    132-REQ-3.2: requirements.md only → V1_MARKDOWN
    132-REQ-3.E1: both present → V1_2_JSON (JSON takes precedence)

    Args:
        spec_dir: Path to a spec folder.

    Returns:
        SpecFormat indicating the detected format.
    """
    if (spec_dir / "requirements.json").is_file():
        return SpecFormat.V1_2_JSON
    return SpecFormat.V1_MARKDOWN


def discover_specs(
    specs_dir: Path,
    filter_spec: str | None = None,
) -> list[SpecInfo]:
    """Discover spec folders in the given directory.

    Only returns v1.2 (JSON) format specs. Legacy v1 (markdown) specs are
    silently excluded from the results.

    Args:
        specs_dir: Path to the .specs/ directory.
        filter_spec: If set, return only this spec (by name or prefix).

    Returns:
        List of SpecInfo sorted by numeric prefix (v1.2 specs only).

    Raises:
        PlanError: If no specs found or filter matches nothing.
    """
    # 02-REQ-1.E1: missing or empty .specs/ directory
    if not specs_dir.is_dir():
        raise PlanError(f"No specifications found: '{specs_dir}' does not exist")

    # Scan for subdirectories matching NN_name pattern
    specs: list[SpecInfo] = []
    found_candidates = False
    for entry in sorted(specs_dir.iterdir()):
        if not entry.is_dir():
            continue
        match = _SPEC_DIR_PATTERN.match(entry.name)
        if not match:
            continue

        found_candidates = True
        prefix = int(match.group(1))

        # 132-REQ-2.E1: skip folders with neither requirements file
        has_req_json = (entry / "requirements.json").is_file()
        has_req_md = (entry / "requirements.md").is_file()
        if not has_req_json and not has_req_md:
            logger.debug(
                "Spec folder '%s' has no requirements file, skipping",
                entry.name,
            )
            continue

        fmt = _detect_format(entry)

        # 132-REQ-3.3: only return v1.2 specs
        if fmt == SpecFormat.V1_MARKDOWN:
            logger.debug(
                "Spec folder '%s' is v1 markdown format, skipping",
                entry.name,
            )
            continue

        # 132-REQ-3.4: for v1.2, check tasks.json (not tasks.md)
        has_tasks = (entry / "tasks.json").is_file()
        has_prd = (entry / "prd.md").is_file()

        if not has_tasks:
            logger.warning(
                "Spec folder '%s' has no tasks.json, skipping for planning",
                entry.name,
            )

        specs.append(
            SpecInfo(
                name=entry.name,
                prefix=prefix,
                path=entry,
                has_tasks=has_tasks,
                has_prd=has_prd,
                format=fmt,
            )
        )

    # 02-REQ-1.E1: no spec folders found at all
    if not specs:
        if not found_candidates:
            raise PlanError(f"No specifications found in '{specs_dir}'")
        # 132-REQ-2.E1, 132-REQ-3.3: candidates existed but all were
        # filtered out (no requirements files or v1 markdown format)
        return []

    # 02-REQ-1.1: sort by numeric prefix
    specs.sort(key=lambda s: s.prefix)

    # 02-REQ-1.2: filter to a single spec if requested
    if filter_spec is not None:
        filtered = [s for s in specs if s.name == filter_spec]
        if not filtered:
            # 02-REQ-1.E2: filter matches nothing
            available = ", ".join(s.name for s in specs)
            raise PlanError(f"Spec '{filter_spec}' not found. Available specs: {available}")
        return filtered

    return specs
