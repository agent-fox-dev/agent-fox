"""Tests for spec landscape awareness -- session and agent layer changes.

Covers:
- TS-01-20, TS-01-21: SpecSession.assess landscape injection
- TS-01-22, TS-01-23: SpecSession.refine landscape injection
- TS-01-24, TS-01-25: SpecAgent.assess_prd and refine_prd signature updates
- TS-01-29: af-spec SKILL.md update for automated landscape injection
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentspec.campaign import Campaign
from agentspec.session import Assessment, Question, SessionState, SpecSession

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_SAMPLE_LANDSCAPE = [
    {
        "spec_id": "01",
        "spec_name": "core_foundation",
        "title": "Core Foundation",
        "status": "implemented",
        "intent": "Establish the base layer for the project.",
        "archived": False,
    },
    {
        "spec_id": "03",
        "spec_name": "backend_protocol",
        "title": "Backend Protocol",
        "status": "draft",
        "intent": "Define the backend protocol.",
        "archived": False,
    },
]


def _sample_assessment_dict(
    quality: str = "needs_refinement",
    summary: str = "Needs work",
    gaps: list[str] | None = None,
    questions: list[dict] | None = None,
) -> dict:
    """Build an assessment dict for persisting to _session.json."""
    if gaps is None:
        gaps = ["No goals"]
    if questions is None:
        questions = [
            {
                "id": "q1",
                "text": "What are the goals?",
                "context": "Goals section is missing",
                "options": [],
                "required": True,
            }
        ]
    return {
        "quality": quality,
        "summary": summary,
        "gaps": gaps,
        "questions": questions,
    }


def _create_test_session(
    tmp_path: Path,
    state: SessionState = SessionState.INIT,
    prd_text: str = "# My PRD\n\n## Intent\nBuild something.",
    assessment_history: list[dict] | None = None,
) -> SpecSession:
    """Create a SpecSession in a spec directory at the given state."""
    import time

    camp_dir = tmp_path / "camp"
    if not (camp_dir / "campaign.yaml").exists():
        Campaign.create(camp_dir, "Test", "Desc")
    camp = Campaign.open(camp_dir)

    spec_name = f"s_{state.value}_{int(time.monotonic_ns())}"
    session = camp.new_spec(spec_name, prd_text)

    if state != SessionState.INIT or assessment_history:
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["state"] = state.value
        if assessment_history is not None:
            data["assessment_history"] = assessment_history
        session_file.write_text(json.dumps(data, indent=2))
        session = SpecSession.resume(session.spec_dir)

    return session


# ===========================================================================
# TS-01-20: SpecSession.assess calls load_spec_landscape with the spec_root
# parent and current_spec_id, then passes the result to agent.assess_prd
# as spec_landscape.  (01-REQ-7.1)
# ===========================================================================


class TestSessionAssessLandscapeInjection:
    """TS-01-20: SpecSession.assess injects landscape into agent.assess_prd."""

    @pytest.mark.asyncio
    async def test_assess_passes_landscape_to_agent(self, tmp_path: Path) -> None:
        """agent.assess_prd receives spec_landscape from load_spec_landscape."""
        session = _create_test_session(tmp_path, SessionState.INIT)

        assessment = Assessment(
            quality="needs_refinement",
            summary="Needs work",
            gaps=["Missing goals"],
            questions=[
                Question(
                    id="q1",
                    text="What are the goals?",
                    context="Goals section is missing",
                    options=[],
                    required=True,
                )
            ],
        )

        mock_agent_instance = MagicMock()
        mock_agent_instance.assess_prd = AsyncMock(return_value=assessment)

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
            patch(
                "agentspec.session.load_spec_landscape",
                return_value=_SAMPLE_LANDSCAPE,
            ) as mock_load_landscape,
        ):
            result = await session.assess()

        # Verify load_spec_landscape was called with the parent dir and current spec_id
        mock_load_landscape.assert_called_once()
        call_args = mock_load_landscape.call_args
        # First positional arg should be the spec_root (parent of spec_dir)
        assert call_args[0][0] == session.spec_dir.parent
        # current_spec_id kwarg should match the spec's id
        assert "current_spec_id" in call_args[1]

        # Verify agent.assess_prd was called with spec_landscape keyword
        mock_agent_instance.assess_prd.assert_called_once()
        call_kwargs = mock_agent_instance.assess_prd.call_args[1]
        assert "spec_landscape" in call_kwargs
        assert call_kwargs["spec_landscape"] == _SAMPLE_LANDSCAPE

        # Verify result is an Assessment
        assert isinstance(result, Assessment)


# ===========================================================================
# TS-01-21: SpecSession.assess catches any exception from
# load_spec_landscape and calls agent.assess_prd with
# spec_landscape=None without propagating the error.  (01-REQ-7.2)
# ===========================================================================


class TestSessionAssessLandscapeGracefulDegradation:
    """TS-01-21: SpecSession.assess falls back to spec_landscape=None on error."""

    @pytest.mark.asyncio
    async def test_assess_catches_landscape_error(self, tmp_path: Path) -> None:
        """No exception propagates; agent.assess_prd called with spec_landscape=None."""
        session = _create_test_session(tmp_path, SessionState.INIT)

        assessment = Assessment(
            quality="needs_refinement",
            summary="Needs work",
            gaps=["Missing goals"],
            questions=[
                Question(
                    id="q1",
                    text="What?",
                    context="Ctx",
                    options=[],
                    required=True,
                )
            ],
        )

        mock_agent_instance = MagicMock()
        mock_agent_instance.assess_prd = AsyncMock(return_value=assessment)

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
            patch(
                "agentspec.session.load_spec_landscape",
                side_effect=OSError("disk failure"),
            ),
        ):
            # Should NOT raise — graceful degradation
            result = await session.assess()

        # Verify agent.assess_prd was called with spec_landscape=None
        mock_agent_instance.assess_prd.assert_called_once()
        call_kwargs = mock_agent_instance.assess_prd.call_args[1]
        assert "spec_landscape" in call_kwargs
        assert call_kwargs["spec_landscape"] is None

        # Verify an Assessment was returned
        assert isinstance(result, Assessment)


# ===========================================================================
# TS-01-22: SpecSession.refine calls load_spec_landscape and passes the
# result to agent.refine_prd as spec_landscape keyword argument.
# (01-REQ-8.1)
# ===========================================================================


class TestSessionRefineLandscapeInjection:
    """TS-01-22: SpecSession.refine injects landscape into agent.refine_prd."""

    @pytest.mark.asyncio
    async def test_refine_passes_landscape_to_agent(self, tmp_path: Path) -> None:
        """agent.refine_prd receives spec_landscape from load_spec_landscape."""
        prev_assessment_dict = _sample_assessment_dict()
        session = _create_test_session(
            tmp_path,
            SessionState.ASSESSING,
            assessment_history=[prev_assessment_dict],
        )

        new_assessment = Assessment(
            quality="ready",
            summary="PRD is now ready",
            gaps=[],
            questions=[],
        )

        mock_agent_instance = MagicMock()
        mock_agent_instance.refine_prd = AsyncMock(
            return_value=("# Updated PRD\n## Goals\n1. REST API", new_assessment)
        )

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
            patch(
                "agentspec.session.load_spec_landscape",
                return_value=_SAMPLE_LANDSCAPE,
            ) as mock_load_landscape,
        ):
            result = await session.refine({"q1": "answer1"})

        # Verify load_spec_landscape was called
        mock_load_landscape.assert_called_once()
        call_args = mock_load_landscape.call_args
        assert call_args[0][0] == session.spec_dir.parent
        assert "current_spec_id" in call_args[1]

        # Verify agent.refine_prd was called with spec_landscape keyword
        mock_agent_instance.refine_prd.assert_called_once()
        call_kwargs = mock_agent_instance.refine_prd.call_args[1]
        assert "spec_landscape" in call_kwargs
        assert call_kwargs["spec_landscape"] == _SAMPLE_LANDSCAPE

        # Verify result is an Assessment (refine returns Assessment)
        assert isinstance(result, Assessment)


# ===========================================================================
# TS-01-23: SpecSession.refine catches any exception from
# load_spec_landscape and calls agent.refine_prd with
# spec_landscape=None.  (01-REQ-8.2)
# ===========================================================================


class TestSessionRefineLandscapeGracefulDegradation:
    """TS-01-23: SpecSession.refine falls back to spec_landscape=None on error."""

    @pytest.mark.asyncio
    async def test_refine_catches_landscape_error(self, tmp_path: Path) -> None:
        """No exception propagates; agent.refine_prd called with spec_landscape=None."""
        prev_assessment_dict = _sample_assessment_dict()
        session = _create_test_session(
            tmp_path,
            SessionState.ASSESSING,
            assessment_history=[prev_assessment_dict],
        )

        new_assessment = Assessment(
            quality="ready",
            summary="PRD is ready",
            gaps=[],
            questions=[],
        )

        mock_agent_instance = MagicMock()
        mock_agent_instance.refine_prd = AsyncMock(
            return_value=("# Updated PRD", new_assessment)
        )

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
            patch(
                "agentspec.session.load_spec_landscape",
                side_effect=PermissionError("access denied"),
            ),
        ):
            # Should NOT raise — graceful degradation
            result = await session.refine({"q1": "answer1"})

        # Verify agent.refine_prd was called with spec_landscape=None
        mock_agent_instance.refine_prd.assert_called_once()
        call_kwargs = mock_agent_instance.refine_prd.call_args[1]
        assert "spec_landscape" in call_kwargs
        assert call_kwargs["spec_landscape"] is None

        # Verify result is an Assessment
        assert isinstance(result, Assessment)


# ===========================================================================
# TS-01-24: SpecAgent.assess_prd has the updated signature with
# spec_landscape keyword parameter and passes it to
# assessment_user_prompt.  (01-REQ-9.1)
# ===========================================================================


class TestAgentAssessPrdLandscape:
    """TS-01-24: SpecAgent.assess_prd accepts and forwards spec_landscape."""

    @pytest.mark.asyncio
    async def test_assess_prd_passes_landscape_to_prompt(self) -> None:
        """assessment_user_prompt receives spec_landscape from assess_prd."""
        from agentspec.agent import SpecAgent
        from conftest_agent import make_assessment_response

        agent = SpecAgent("STANDARD")

        landscape = [
            {
                "spec_id": "01",
                "spec_name": "n",
                "title": "T",
                "status": "draft",
                "intent": "I",
                "archived": False,
            },
        ]

        fake_response = make_assessment_response(
            quality="needs_refinement",
            summary="Needs work",
        )

        captured_kwargs: dict = {}

        def _capture_assessment_user_prompt(*args, **kwargs):  # type: ignore[no-untyped-def]
            captured_kwargs.update(kwargs)
            # Call a version that won't recurse — use the prompt function
            # without landscape to avoid dependency on implementation
            from agentspec.prompts import assessment_user_prompt as real_fn

            # Forward without spec_landscape to get a valid prompt
            return real_fn(args[0], args[1], **{k: v for k, v in kwargs.items() if k != "spec_landscape"})

        with (
            patch(
                "agentspec.agent.assessment_user_prompt",
                side_effect=_capture_assessment_user_prompt,
            ),
            patch.object(agent, "_call_api", new_callable=AsyncMock, return_value=fake_response),
        ):
            result = await agent.assess_prd(
                "# PRD\nContent",
                "test_spec",
                spec_landscape=landscape,
            )

        # Verify assessment_user_prompt received spec_landscape
        assert "spec_landscape" in captured_kwargs
        assert captured_kwargs["spec_landscape"] == landscape

        # Verify result is an Assessment
        assert isinstance(result, Assessment)


# ===========================================================================
# TS-01-25: SpecAgent.refine_prd has the updated signature with
# spec_landscape keyword parameter and passes it to
# refinement_user_prompt.  (01-REQ-10.1)
# ===========================================================================


class TestAgentRefinePrdLandscape:
    """TS-01-25: SpecAgent.refine_prd accepts and forwards spec_landscape."""

    @pytest.mark.asyncio
    async def test_refine_prd_passes_landscape_to_prompt(self) -> None:
        """refinement_user_prompt receives spec_landscape from refine_prd."""
        from agentspec.agent import SpecAgent
        from conftest_agent import FakeMessage, FakeToolUseBlock

        agent = SpecAgent("STANDARD")

        landscape = [
            {
                "spec_id": "01",
                "spec_name": "n",
                "title": "T",
                "status": "draft",
                "intent": "I",
                "archived": False,
            },
        ]

        prev_assessment = Assessment(
            quality="needs_refinement",
            summary="Needs work",
            gaps=["Missing goals"],
            questions=[
                Question(
                    id="q1",
                    text="What are the goals?",
                    context="Goals section is missing",
                    options=[],
                    required=True,
                )
            ],
        )

        # Build a fake response with both tool calls (prd update + assessment)
        fake_response = FakeMessage(
            content=[
                FakeToolUseBlock(
                    name="submit_prd_update",
                    input={"updated_prd": "# Updated PRD\n## Goals\n1. REST API"},
                ),
                FakeToolUseBlock(
                    name="submit_assessment",
                    input={
                        "quality": "ready",
                        "summary": "PRD is ready",
                        "gaps": [],
                        "questions": [],
                    },
                ),
            ]
        )

        captured_kwargs: dict = {}

        def _capture_refinement_user_prompt(*args, **kwargs):  # type: ignore[no-untyped-def]
            captured_kwargs.update(kwargs)
            from agentspec.prompts import refinement_user_prompt as real_fn

            return real_fn(args[0], args[1], args[2], **{k: v for k, v in kwargs.items() if k != "spec_landscape"})

        with (
            patch(
                "agentspec.agent.refinement_user_prompt",
                side_effect=_capture_refinement_user_prompt,
            ),
            patch.object(agent, "_call_api", new_callable=AsyncMock, return_value=fake_response),
        ):
            result = await agent.refine_prd(
                "# PRD\nContent",
                {"q1": "a1"},
                prev_assessment,
                spec_landscape=landscape,
            )

        # Verify refinement_user_prompt received spec_landscape
        assert "spec_landscape" in captured_kwargs
        assert captured_kwargs["spec_landscape"] == landscape

        # Verify result is a tuple(str, Assessment)
        assert isinstance(result, tuple)
        assert isinstance(result[0], str)
        assert isinstance(result[1], Assessment)


# ===========================================================================
# TS-01-29: The af-spec SKILL.md Step 2 contains the updated text about
# automated landscape injection and does not contain the old instruction
# to manually scan for existing specs.  (01-REQ-12.1)
# ===========================================================================


class TestAfSpecSkillUpdate:
    """TS-01-29: af-spec SKILL.md Step 2 reflects automated landscape injection."""

    def test_skill_md_has_automated_injection_language(self) -> None:
        """SKILL.md mentions automated injection, spec refine, and Dependencies."""
        # Find the SKILL.md relative to the project root
        # Walk up from this test file to find the project root
        skill_path = Path(__file__).resolve()
        # Navigate from packages/agentspec/tests/ to project root
        project_root = skill_path.parent.parent.parent.parent
        skill_file = project_root / ".claude" / "skills" / "af-spec" / "SKILL.md"

        assert skill_file.exists(), f"SKILL.md not found at {skill_file}"

        content = skill_file.read_text()

        # Must mention automated/automatic injection
        assert "automatically" in content.lower() or "automatic" in content.lower(), (
            "SKILL.md must mention 'automatically' or 'automatic' for landscape injection"
        )
        # Must reference spec refine
        assert "spec refine" in content, (
            "SKILL.md must reference 'spec refine'"
        )
        # Must mention Dependencies section
        assert "Dependencies" in content, (
            "SKILL.md must mention 'Dependencies'"
        )
