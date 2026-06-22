"""Tests for agentspec.prompt_loader — template loading and resolution."""

from __future__ import annotations

import os
from pathlib import Path

import pytest
from agentspec.prompt_loader import load_prompt, load_prompt_template

_ALL_TEMPLATE_NAMES = [
    "assessment_system",
    "assessment_user",
    "refinement_system",
    "refinement_user",
    "generation_system",
    "generation_user_base",
    "generation_user_requirements",
    "generation_user_test_spec",
    "generation_user_tasks",
    "repair_user",
]


# ── default template loading ────────────────────────────────────────


@pytest.mark.parametrize("name", _ALL_TEMPLATE_NAMES)
def test_default_prompts_exist(name: str) -> None:
    """Every bundled prompt template loads successfully."""
    content = load_prompt(name)
    assert len(content.strip()) > 0


def test_assessment_system_content() -> None:
    """Assessment system prompt contains expected keywords."""
    content = load_prompt("assessment_system")
    assert "Intent" in content
    assert "Goals" in content
    assert "Non-Goals" in content
    assert "Background" in content


def test_generation_system_content() -> None:
    """Generation system prompt references JSON schema and artifacts."""
    content = load_prompt("generation_system")
    lower = content.lower()
    assert "json" in lower or "schema" in lower
    assert "artifact" in lower


# ── project override ────────────────────────────────────────────────


def test_project_override(tmp_path: Path) -> None:
    """Project-level prompt takes precedence over bundled default."""
    prompts_dir = tmp_path / ".agent-fox" / "prompts"
    prompts_dir.mkdir(parents=True)
    (prompts_dir / "assessment_system.md").write_text("CUSTOM ASSESSMENT")

    content = load_prompt("assessment_system", project_dir=tmp_path)
    assert content == "CUSTOM ASSESSMENT"


def test_default_fallback(tmp_path: Path) -> None:
    """Falls back to bundled default when project has no override."""
    content = load_prompt("assessment_system", project_dir=tmp_path)
    assert "Intent" in content


# ── symlink rejection ───────────────────────────────────────────────


def test_symlink_rejection(tmp_path: Path) -> None:
    """Symlinked prompt files are skipped; loader falls through to default."""
    project = tmp_path / "project"
    prompts_dir = project / ".agent-fox" / "prompts"
    prompts_dir.mkdir(parents=True)

    target = tmp_path / "external.md"
    target.write_text("INJECTED CONTENT")

    link = prompts_dir / "assessment_system.md"
    os.symlink(target, link)

    content = load_prompt("assessment_system", project_dir=project)
    assert "INJECTED CONTENT" not in content
    assert "Intent" in content


# ── path traversal prevention ───────────────────────────────────────


@pytest.mark.parametrize(
    "bad_name",
    [
        "../../etc/passwd",
        "../secret",
        "name.with.dots",
        "name/with/slashes",
        "",
    ],
)
def test_path_traversal_rejected(bad_name: str) -> None:
    """Names with unsafe characters raise ValueError."""
    with pytest.raises(ValueError):
        load_prompt(bad_name)


# ── missing prompt ──────────────────────────────────────────────────


def test_missing_prompt_raises() -> None:
    """Non-existent prompt name raises FileNotFoundError."""
    with pytest.raises(FileNotFoundError):
        load_prompt("nonexistent_prompt_name")


# ── frontmatter stripping ──────────────────────────────────────────


def test_frontmatter_stripping(tmp_path: Path) -> None:
    """YAML frontmatter is stripped from loaded prompts."""
    prompts_dir = tmp_path / ".agent-fox" / "prompts"
    prompts_dir.mkdir(parents=True)
    (prompts_dir / "assessment_system.md").write_text("---\ntitle: test\ntype: prompt\n---\nActual content here")

    content = load_prompt("assessment_system", project_dir=tmp_path)
    assert "---" not in content
    assert "Actual content here" in content


# ── template substitution ──────────────────────────────────────────


def test_template_substitution() -> None:
    """Template variables are substituted in the loaded prompt."""
    content = load_prompt_template(
        "assessment_user",
        prd_text="Hello PRD",
        spec_name="test_spec",
    )
    assert "Hello PRD" in content
    assert "test_spec" in content
    assert "$prd_text" not in content
    assert "$spec_name" not in content


def test_safe_substitute_leaves_unknown(tmp_path: Path) -> None:
    """Unrecognized $variables pass through unchanged."""
    prompts_dir = tmp_path / ".agent-fox" / "prompts"
    prompts_dir.mkdir(parents=True)
    (prompts_dir / "assessment_system.md").write_text("Known: $known, Unknown: $unknown_var")

    content = load_prompt_template(
        "assessment_system",
        project_dir=tmp_path,
        known="REPLACED",
    )
    assert "REPLACED" in content
    assert "$unknown_var" in content
