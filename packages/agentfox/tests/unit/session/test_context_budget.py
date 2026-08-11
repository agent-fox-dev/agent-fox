"""Tests for spec rendering budget cap in context assembly.

Verifies that max_context_tokens on assemble_context and max_tokens on
render_inmemory_spec_sections / _render_spec_sections progressively
truncate spec content to stay within a token budget.

Requirements: issue #754
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import patch

import duckdb
from agentfox.session.context import (
    _render_spec_sections,
    assemble_context,
    render_inmemory_spec_sections,
)


def _make_large_spec_dir(tmp_path: Path, *, arch_size: int = 5000) -> Path:
    """Create a spec dir with a large architecture.md for budget testing."""
    spec_dir = tmp_path / "specs" / "budget_spec"
    spec_dir.mkdir(parents=True)

    (spec_dir / "prd.md").write_text(
        '---\nspec_id: "budget"\nspec_name: "budget"\ntitle: "Budget Test"\n'
        'status: "draft"\ncreated_at: "2024-01-01T00:00:00Z"\n'
        'updated_at: "2024-01-01T00:00:00Z"\nowner: "test"\n'
        'source: "test"\nschema_version: 1\n---\n# Budget Test\n'
    )
    (spec_dir / "requirements.json").write_text(
        json.dumps(
            {
                "spec_id": "budget",
                "spec_name": "budget",
                "schema_version": 1,
                "introduction": "Requirements intro",
                "glossary": {},
                "requirements": [],
                "correctness_properties": [],
                "execution_paths": [],
                "error_handling": [],
            }
        )
    )
    (spec_dir / "test_spec.json").write_text(
        json.dumps(
            {
                "spec_id": "budget",
                "spec_name": "budget",
                "schema_version": 1,
                "test_cases": [],
                "property_tests": [],
                "edge_case_tests": [],
                "smoke_tests": [],
                "coverage": {
                    "requirements_covered": [],
                    "properties_covered": [],
                    "paths_covered": [],
                    "gaps": [],
                },
            }
        )
    )
    (spec_dir / "tasks.json").write_text(
        json.dumps(
            {
                "spec_id": "budget",
                "spec_name": "budget",
                "schema_version": 1,
                "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
                "dependencies": [],
                "task_groups": [
                    {
                        "id": 1,
                        "kind": "standard",
                        "title": "Group 1",
                        "subtasks": [
                            {
                                "id": "1.1",
                                "title": "Sub",
                                "state": "pending",
                                "details": [],
                                "test_spec_refs": [],
                                "requirement_refs": [],
                                "optional": False,
                            }
                        ],
                        "verification": {"id": "", "checks": []},
                    }
                ],
                "traceability": [],
            }
        )
    )
    arch_content = "# Architecture\n\n" + ("x" * arch_size)
    (spec_dir / "architecture.md").write_text(arch_content)

    return spec_dir


class TestRenderInmemoryMaxTokens:
    """max_tokens parameter is threaded to afspec render functions."""

    def test_max_tokens_none_is_default(self, tmp_spec_dir: Path) -> None:
        """Default max_tokens=None passes through without truncation."""
        import afspec

        spec = afspec.load_spec(tmp_spec_dir)
        sections_no_budget = render_inmemory_spec_sections(spec)
        sections_none = render_inmemory_spec_sections(spec, max_tokens=None)
        assert sections_no_budget == sections_none

    def test_max_tokens_passed_to_afspec(self, tmp_spec_dir: Path) -> None:
        """max_tokens is forwarded to afspec.render_individual."""
        import afspec

        spec = afspec.load_spec(tmp_spec_dir)
        with patch("afspec.render_individual", wraps=afspec.render_individual) as mock:
            render_inmemory_spec_sections(spec, max_tokens=5000)
            mock.assert_called_once_with(spec, max_tokens=5000)

    def test_max_tokens_passed_to_afspec_scoped(self, tmp_spec_dir: Path) -> None:
        """max_tokens is forwarded to afspec.render_individual_scoped."""
        import afspec

        spec = afspec.load_spec(tmp_spec_dir)
        with patch(
            "afspec.render_individual_scoped",
            wraps=afspec.render_individual_scoped,
        ) as mock:
            render_inmemory_spec_sections(spec, task_group=1, max_tokens=5000)
            mock.assert_called_once_with(spec, 1, max_tokens=5000)


class TestRenderSpecSectionsMaxTokens:
    """_render_spec_sections drops architecture when over budget."""

    def test_architecture_included_without_budget(self, tmp_path: Path) -> None:
        """Without max_tokens, architecture.md is included."""
        spec_dir = _make_large_spec_dir(tmp_path)
        sections = _render_spec_sections(spec_dir)
        combined = "\n".join(sections)
        assert "## Architecture" in combined
        assert "xxxxx" in combined

    def test_architecture_included_under_budget(self, tmp_path: Path) -> None:
        """Architecture is included when total stays under budget."""
        spec_dir = _make_large_spec_dir(tmp_path, arch_size=100)
        sections = _render_spec_sections(spec_dir, max_tokens=50_000)
        combined = "\n".join(sections)
        assert "## Architecture" in combined
        assert "xxxxx" in combined

    def test_architecture_dropped_over_budget(self, tmp_path: Path) -> None:
        """Architecture is dropped when it would exceed the budget."""
        spec_dir = _make_large_spec_dir(tmp_path, arch_size=20_000)
        sections = _render_spec_sections(spec_dir, max_tokens=100)
        combined = "\n".join(sections)
        assert "## Architecture" in combined
        assert "excluded by token budget" in combined
        assert "x" * 100 not in combined

    def test_max_tokens_none_skips_budget_check(self, tmp_path: Path) -> None:
        """max_tokens=None means no budget — architecture always included."""
        spec_dir = _make_large_spec_dir(tmp_path, arch_size=20_000)
        sections = _render_spec_sections(spec_dir, max_tokens=None)
        combined = "\n".join(sections)
        assert "x" * 100 in combined


class TestAssembleContextBudget:
    """assemble_context max_context_tokens parameter."""

    def test_default_budget_is_30k(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Default max_context_tokens is 30_000."""
        with patch(
            "agentfox.session.context._render_spec_sections",
            wraps=_render_spec_sections,
        ) as mock:
            assemble_context(tmp_spec_dir, task_group=1, conn=knowledge_conn)
            _, kwargs = mock.call_args
            assert kwargs["max_tokens"] == 30_000

    def test_none_disables_budget(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """max_context_tokens=None disables the budget cap."""
        with patch(
            "agentfox.session.context._render_spec_sections",
            wraps=_render_spec_sections,
        ) as mock:
            assemble_context(
                tmp_spec_dir,
                task_group=1,
                conn=knowledge_conn,
                max_context_tokens=None,
            )
            _, kwargs = mock.call_args
            assert kwargs["max_tokens"] is None

    def test_custom_budget_threaded(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Custom max_context_tokens value is threaded to _render_spec_sections."""
        with patch(
            "agentfox.session.context._render_spec_sections",
            wraps=_render_spec_sections,
        ) as mock:
            assemble_context(
                tmp_spec_dir,
                task_group=1,
                conn=knowledge_conn,
                max_context_tokens=10_000,
            )
            _, kwargs = mock.call_args
            assert kwargs["max_tokens"] == 10_000

    def test_architecture_dropped_with_tight_budget(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """With a very tight budget, architecture is dropped from context."""
        spec_dir = _make_large_spec_dir(tmp_path, arch_size=20_000)
        ctx = assemble_context(
            spec_dir,
            task_group=1,
            conn=knowledge_conn,
            max_context_tokens=100,
        )
        assert "excluded by token budget" in ctx

    def test_architecture_present_with_no_budget(
        self,
        tmp_path: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """With no budget, large architecture is included in context."""
        spec_dir = _make_large_spec_dir(tmp_path, arch_size=20_000)
        ctx = assemble_context(
            spec_dir,
            task_group=1,
            conn=knowledge_conn,
            max_context_tokens=None,
        )
        assert "x" * 100 in ctx
        assert "excluded by token budget" not in ctx
