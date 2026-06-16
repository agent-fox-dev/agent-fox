"""Fixtures for session tests: spec directories, mocks, configs."""

from __future__ import annotations

from pathlib import Path

import pytest

from agent_fox.core.config import AgentFoxConfig
from agent_fox.workspace import WorkspaceInfo


@pytest.fixture
def tmp_spec_dir(tmp_path: Path) -> Path:
    """Create a temporary spec directory with v1.2 JSON spec files."""
    import json

    spec_dir = tmp_path / "specs" / "test_spec"
    spec_dir.mkdir(parents=True)

    (spec_dir / "prd.md").write_text(
        '---\nspec_id: "test"\nspec_name: "test"\ntitle: "Test"\n'
        'status: "draft"\ncreated_at: "2024-01-01T00:00:00Z"\n'
        'updated_at: "2024-01-01T00:00:00Z"\nowner: "test"\n'
        'source: "test"\nschema_version: 1\n---\n# Test\n'
    )
    (spec_dir / "requirements.json").write_text(json.dumps({
        "spec_id": "test", "spec_name": "test", "schema_version": 1,
        "introduction": "REQ content here",
        "glossary": {}, "requirements": [],
        "correctness_properties": [], "execution_paths": [], "error_handling": [],
    }))
    (spec_dir / "test_spec.json").write_text(json.dumps({
        "spec_id": "test", "spec_name": "test", "schema_version": 1,
        "test_cases": [{"id": "TS-1-1", "title": "Test spec content here",
                        "requirement_refs": [], "steps": [], "expected": "pass"}],
        "property_tests": [], "edge_case_tests": [], "smoke_tests": [],
        "coverage": {"requirements_covered": [], "properties_covered": [],
                     "paths_covered": [], "gaps": []},
    }))
    (spec_dir / "tasks.json").write_text(json.dumps({
        "spec_id": "test", "spec_name": "test", "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [{
            "id": 1, "kind": "standard", "title": "Task content here",
            "subtasks": [{"id": "1.1", "title": "Sub", "state": "pending",
                          "details": [], "test_spec_refs": [], "requirement_refs": [],
                          "optional": False}],
            "verification": {"id": "", "checks": []},
        }],
        "traceability": [],
    }))
    (spec_dir / "architecture.md").write_text("# Architecture\nDesign content here\n")

    return spec_dir


@pytest.fixture
def default_config() -> AgentFoxConfig:
    """Provide an AgentFoxConfig with test-friendly defaults."""
    return AgentFoxConfig()


@pytest.fixture
def short_timeout_config() -> AgentFoxConfig:
    """Provide an AgentFoxConfig with a very short session timeout.

    Uses 1 minute timeout for testing.
    """
    return AgentFoxConfig(
        orchestrator={"session_timeout": 1},  # type: ignore[arg-type]
    )


@pytest.fixture
def small_allowlist_config() -> AgentFoxConfig:
    """Provide an AgentFoxConfig with a restricted allowlist.

    Only allows 'git' and 'python' commands.
    """
    return AgentFoxConfig(
        security={"bash_allowlist": ["git", "python"]},  # type: ignore[arg-type]
    )


@pytest.fixture
def workspace_info(tmp_path: Path) -> WorkspaceInfo:
    """Provide a WorkspaceInfo pointing to a temp directory."""
    ws_path = tmp_path / "worktree"
    ws_path.mkdir()
    return WorkspaceInfo(
        path=ws_path,
        branch="feature/test_spec/1",
        spec_name="test_spec",
        task_group=1,
    )
