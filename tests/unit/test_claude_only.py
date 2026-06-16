"""Unit tests for Claude-only commitment (spec 55).

Test Spec: TS-55-E1
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_PROJECT_ROOT = Path(__file__).resolve().parents[2]


# ---------------------------------------------------------------------------
# TS-55-E1: ADR number non-collision
# ---------------------------------------------------------------------------


def test_adr_number_unique() -> None:
    """All ADR files have unique numeric prefixes."""
    adr_dir = _PROJECT_ROOT / "docs" / "adr"
    if not adr_dir.exists():
        pytest.skip("docs/adr/ does not exist yet")

    adrs = list(adr_dir.glob("[0-9]*.md"))
    if not adrs:
        pytest.skip("No ADR files found")

    numbers: list[str] = []
    for f in adrs:
        match = re.match(r"(\d+)", f.name)
        if match:
            numbers.append(match.group(1))

    assert len(numbers) == len(set(numbers)), f"Duplicate ADR numbers: {numbers}"
