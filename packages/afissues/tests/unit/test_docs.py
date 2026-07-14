"""Tests for documentation updates (TS-03-39 through TS-03-40).

Verifies that packages/README.md and root README.md include afissues
entries in package tables and dependency graphs.

Requirements: 03-REQ-11.1, 03-REQ-11.2
"""

from __future__ import annotations

from pathlib import Path

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]


# ── TS-03-39: packages/README.md includes afissues ──────────────────


class TestPackagesReadme:
    """TS-03-39: packages/README.md has afissues in table and dep graph."""

    def test_afissues_in_packages_readme(self) -> None:
        """afissues must appear in packages/README.md."""
        readme_path = _WORKSPACE_ROOT / "packages" / "README.md"
        content = readme_path.read_text()
        assert "afissues" in content, "afissues not found in packages/README.md"

    def test_dependency_arrow_in_packages_readme(self) -> None:
        """Dependency edge from agentfox to afissues must appear in graph."""
        readme_path = _WORKSPACE_ROOT / "packages" / "README.md"
        content = readme_path.read_text()
        # Check for dependency arrow notation (any common format)
        has_arrow = (
            "agentfox" in content
            and "afissues" in content
            and any(arrow in content for arrow in ("──▶", "-->", "→", "──►"))
        )
        assert has_arrow, "Dependency edge 'agentfox ──▶ afissues' not found in packages/README.md"


# ── TS-03-40: root README.md includes afissues ──────────────────────


class TestRootReadme:
    """TS-03-40: Root README.md has afissues in dep graph, table, and standalone section."""

    def test_afissues_appears_in_root_readme(self) -> None:
        """afissues must appear in root README.md."""
        readme_path = _WORKSPACE_ROOT / "README.md"
        content = readme_path.read_text()
        assert "afissues" in content, "afissues not found in root README.md"

    def test_afissues_appears_multiple_times(self) -> None:
        """afissues must appear in at least 3 locations (graph, table, standalone)."""
        readme_path = _WORKSPACE_ROOT / "README.md"
        content = readme_path.read_text()
        count = content.count("afissues")
        assert count >= 3, f"afissues should appear >= 3 times in root README.md, found {count}"
