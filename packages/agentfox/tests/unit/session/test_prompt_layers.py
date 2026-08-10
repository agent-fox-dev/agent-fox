"""Unit tests for 2-layer prompt assembly (spec 99).

Covers: TS-99-1, TS-99-E1
"""

from __future__ import annotations

from pathlib import Path


def test_2_layer_order(tmp_path: Path) -> None:
    """TS-99-1: Prompt layers appear in correct order.

    Layer 1: archetype profile
    Layer 2: task context

    Requirement: 99-REQ-1.1
    """
    from agentfox.session.prompt import build_system_prompt

    profiles_dir = tmp_path / ".agent-fox" / "profiles"
    profiles_dir.mkdir(parents=True)

    (profiles_dir / "coder.md").write_text("PROFILE_CONTENT_MARKER")

    task_context = "TASK_CONTEXT_MARKER"

    prompt = build_system_prompt(
        task_context,
        archetype="coder",
        project_dir=tmp_path,
    )

    idx_profile = prompt.index("PROFILE_CONTENT_MARKER")
    idx_task = prompt.index("TASK_CONTEXT_MARKER")

    assert idx_profile < idx_task


def test_backward_compat_project_agent_md(tmp_path: Path) -> None:
    """Backward compat: project-level agent.md is prepended with deprecation warning.

    When .agent-fox/profiles/agent.md exists, its content is prepended
    before the archetype profile for backward compatibility.
    """
    from agentfox.session.prompt import build_system_prompt

    profiles_dir = tmp_path / ".agent-fox" / "profiles"
    profiles_dir.mkdir(parents=True)

    (profiles_dir / "agent.md").write_text("PROJECT_AGENT_CONTENT")
    (profiles_dir / "coder.md").write_text("PROFILE_CONTENT_MARKER")

    task_context = "TASK_CONTEXT_MARKER"

    prompt = build_system_prompt(
        task_context,
        archetype="coder",
        project_dir=tmp_path,
    )

    idx_agent = prompt.index("PROJECT_AGENT_CONTENT")
    idx_profile = prompt.index("PROFILE_CONTENT_MARKER")
    idx_task = prompt.index("TASK_CONTEXT_MARKER")

    assert idx_agent < idx_profile < idx_task


def test_missing_agent_profile(tmp_path: Path) -> None:
    """TS-99-E1: Prompt assembly works without project-level agent profile.

    With 2-layer assembly, the package-default archetype profile
    provides all needed content. No agent.md is required.

    Requirement: 99-REQ-1.E1
    """
    from agentfox.session.prompt import build_system_prompt

    task_context = "TASK_CONTEXT_MARKER"

    prompt = build_system_prompt(
        task_context,
        archetype="coder",
        project_dir=tmp_path,
    )

    assert len(prompt) > 0
