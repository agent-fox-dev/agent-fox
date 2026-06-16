"""Integration tests for Claude-only commitment (spec 55).

Test Spec: TS-55-1, TS-55-9
"""

from __future__ import annotations

from pathlib import Path

_PROJECT_ROOT = Path(__file__).resolve().parents[4]


# ---------------------------------------------------------------------------
# TS-55-9: README mentions Claude
# ---------------------------------------------------------------------------


def test_readme_claude() -> None:
    """README.md states agent-fox is built for Claude."""
    readme = (_PROJECT_ROOT / "README.md").read_text()
    readme_lower = readme.lower()
    assert "claude" in readme_lower
    assert "built" in readme_lower or "exclusively" in readme_lower or "powered by" in readme_lower, (
        "README must indicate Claude exclusivity"
    )
