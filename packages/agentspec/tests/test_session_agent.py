"""Tests for agent-session integration — session delegates to SpecAgent.

Covers TS-03-27 through TS-03-31 (session integration),
TS-03-E13 through TS-03-E14 (session edge cases),
TS-03-P5 (property: partial artifacts preserved),
TS-03-SMOKE-1 through TS-03-SMOKE-4 (integration smoke tests).

All tests use a mocked Anthropic client; no real API calls are made.
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentspec.campaign import Campaign
from agentspec.errors import AgentError
from agentspec.session import Assessment, Question, SessionState, SpecSession
from conftest_agent import (
    SAMPLE_REQUIREMENTS_JSON,
    SAMPLE_TASKS_JSON,
    SAMPLE_TEST_SPEC_JSON,
    make_artifact_response,
    make_assessment_response,
    make_bad_request_error,
    make_rate_limit_error,
)
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers: create sessions in specific states with mocked agent
# ---------------------------------------------------------------------------


def _create_test_session(
    tmp_path: Path,
    state: SessionState = SessionState.INIT,
    prd_text: str = "# My PRD\n\n## Intent\nBuild something.",
    assessment_history: list[dict] | None = None,
) -> SpecSession:
    """Create a SpecSession in a spec directory at the given state.

    Uses Campaign.new_spec, then patches _session.json directly for
    non-init states.
    """
    camp_dir = tmp_path / "camp"
    if not (camp_dir / "campaign.yaml").exists():
        Campaign.create(camp_dir, "Test", "Desc")
    camp = Campaign.open(camp_dir)

    # Use a unique spec name per call to avoid collisions
    import time

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


# ===================================================================
# TS-03-27: Session assess delegates to SpecAgent
# ===================================================================


@pytest.mark.asyncio
async def test_session_assess_delegates_to_agent(tmp_path: Path) -> None:
    """TS-03-27: SpecSession.assess() creates a SpecAgent, calls assess_prd,
    and persists the result."""
    session = _create_test_session(tmp_path, SessionState.INIT)

    assessment = Assessment(
        quality="needs_refinement",
        summary="Needs work",
        gaps=["No goals"],
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
    ):
        await session.assess()

    mock_agent_instance.assess_prd.assert_called_once()
    assert session.state == SessionState.ASSESSING

    # Verify assessment persisted
    data = json.loads((session.spec_dir / "_session.json").read_text())
    assert len(data["assessment_history"]) == 1


# ===================================================================
# TS-03-28: Session refine delegates to SpecAgent
# ===================================================================


@pytest.mark.asyncio
async def test_session_refine_delegates_to_agent(tmp_path: Path) -> None:
    """TS-03-28: SpecSession.refine() calls SpecAgent.refine_prd,
    updates PRD file, and persists the new Assessment."""
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
    mock_agent_instance.refine_prd = AsyncMock(return_value=("# Updated PRD\n## Goals\n1. REST API", new_assessment))

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        await session.refine({"q1": "My answer"})

    mock_agent_instance.refine_prd.assert_called_once()

    # PRD file should be updated
    prd_content = (session.spec_dir / "prd.md").read_text()
    assert "Updated" in prd_content or "REST API" in prd_content

    # Assessment history should have 2 entries
    data = json.loads((session.spec_dir / "_session.json").read_text())
    assert len(data["assessment_history"]) == 2


# ===================================================================
# TS-03-29: Session generate delegates to SpecAgent and writes files
# ===================================================================


@pytest.mark.asyncio
async def test_session_generate_delegates_and_writes_files(
    tmp_path: Path,
) -> None:
    """TS-03-29: SpecSession.generate() calls generate_artifacts,
    writes files, and transitions to 'generated'."""
    from afspec import Requirements, Tasks, TestSpec

    session = _create_test_session(tmp_path, SessionState.PRD_ACCEPTED)

    # Return afspec model instances (as generate_artifacts now does)
    artifacts = {
        "requirements": Requirements(**SAMPLE_REQUIREMENTS_JSON),
        "test_spec": TestSpec(**SAMPLE_TEST_SPEC_JSON),
        "tasks": Tasks(**SAMPLE_TASKS_JSON),
    }

    mock_agent_instance = MagicMock()
    mock_agent_instance.generate_artifacts = AsyncMock(return_value=artifacts)

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        await session.generate()

    # All three artifact files should exist
    assert (session.spec_dir / "requirements.json").exists()
    assert (session.spec_dir / "test_spec.json").exists()
    assert (session.spec_dir / "tasks.json").exists()

    # State should be generated
    assert session.state == SessionState.GENERATED


# ===================================================================
# TS-03-30: Agent error prevents session state transition
# ===================================================================


@pytest.mark.asyncio
async def test_agent_error_prevents_state_transition(tmp_path: Path) -> None:
    """TS-03-30: AgentError during a session operation prevents
    state transition and is re-raised."""
    session = _create_test_session(tmp_path, SessionState.INIT)

    mock_agent_instance = MagicMock()
    mock_agent_instance.assess_prd = AsyncMock(side_effect=AgentError("API failed"))

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        with pytest.raises(AgentError):
            await session.assess()

    # State should remain INIT
    assert session.state == SessionState.INIT

    # Error should be persisted in _session.json (03-REQ-6.4)
    data = json.loads((session.spec_dir / "_session.json").read_text())
    assert "last_error" in data, "Error must be persisted in _session.json"
    assert data["last_error"]["message"] == "API failed"
    assert "category" in data["last_error"]
    assert "retryable" in data["last_error"]


# ===================================================================
# TS-03-31: Assessment history accumulates
# ===================================================================


@pytest.mark.asyncio
async def test_assessment_history_accumulates(tmp_path: Path) -> None:
    """TS-03-31: Each assess/refine call appends to assessment_history."""
    session = _create_test_session(tmp_path, SessionState.INIT)

    assessment_1 = Assessment(
        quality="needs_refinement",
        summary="Needs work",
        gaps=["No goals"],
        questions=[
            Question(
                id="q1",
                text="What are the goals?",
                context="Missing",
                options=[],
                required=True,
            )
        ],
    )
    assessment_2 = Assessment(
        quality="ready",
        summary="PRD is ready",
        gaps=[],
        questions=[],
    )

    mock_agent_instance = MagicMock()
    mock_agent_instance.assess_prd = AsyncMock(return_value=assessment_1)
    mock_agent_instance.refine_prd = AsyncMock(return_value=("# Updated PRD", assessment_2))

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        # First assessment
        await session.assess()
        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert len(data["assessment_history"]) == 1

        # Refinement adds another
        await session.refine({"q1": "answer"})
        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert len(data["assessment_history"]) == 2


# ===================================================================
# TS-03-E13: Partial generation failure preserves artifacts
# ===================================================================


@pytest.mark.asyncio
async def test_partial_generation_preserves_artifacts(tmp_path: Path) -> None:
    """TS-03-E13: When generation fails on second artifact, the first
    artifact remains on disk and session stays in 'generating'."""
    session = _create_test_session(tmp_path, SessionState.PRD_ACCEPTED)

    mock_client = MagicMock()
    mock_client.messages.create = AsyncMock(
        side_effect=[
            make_artifact_response("requirements", SAMPLE_REQUIREMENTS_JSON),
            make_bad_request_error(),
        ]
    )

    from agentspec.agent import SpecAgent as _SA

    with patch("agentspec.session._create_agent", return_value=_SA(mock_client, "claude-sonnet-4-6")):
        with pytest.raises(AgentError):
            await session.generate()

    # requirements.json should have been written during generation
    assert (session.spec_dir / "requirements.json").exists()
    # test_spec.json should not exist (failed to generate)
    assert not (session.spec_dir / "test_spec.json").exists()
    # State should be GENERATING (not GENERATED)
    assert session.state == SessionState.GENERATING


# ===================================================================
# TS-03-E14: Resume after partial generation
# ===================================================================


@pytest.mark.asyncio
async def test_resume_after_partial_generation(tmp_path: Path) -> None:
    """TS-03-E14: Resumed session detects existing artifacts and
    generates only missing ones."""
    from afspec import Requirements, marshal_json

    session = _create_test_session(tmp_path, SessionState.GENERATING)

    # Pre-write requirements.json as a valid afspec model
    req_model = Requirements(**SAMPLE_REQUIREMENTS_JSON)
    (session.spec_dir / "requirements.json").write_text(marshal_json(req_model))

    # Mock client returns only 2 responses (for the missing artifacts)
    mock_client = MagicMock()
    mock_client.messages.create = AsyncMock(
        side_effect=[
            make_artifact_response("test_spec", SAMPLE_TEST_SPEC_JSON),
            make_artifact_response("tasks", SAMPLE_TASKS_JSON),
        ]
    )

    from agentspec.agent import SpecAgent as _SA

    with patch("agentspec.session._create_agent", return_value=_SA(mock_client, "claude-sonnet-4-6")):
        await session.generate()

    # All three artifacts should now exist
    assert (session.spec_dir / "requirements.json").exists()
    assert (session.spec_dir / "test_spec.json").exists()
    assert (session.spec_dir / "tasks.json").exists()

    assert session.state == SessionState.GENERATED

    # Only 2 API calls — for missing artifacts only (not requirements)
    assert mock_client.messages.create.call_count == 2


# ===================================================================
# TS-03-P5: Failed generation preserves partial artifacts (property)
# ===================================================================


class TestPropertyPartialArtifacts:
    """Property test: failed generation preserves partial artifacts."""

    @given(failure_point=st.sampled_from([0, 1, 2]))
    @settings(
        max_examples=3,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_property_partial_artifacts_preserved(self, failure_point: int, tmp_path: Path) -> None:
        """TS-03-P5: For any generation failure at artifact N,
        artifacts 1..N-1 remain on disk."""
        import asyncio

        artifact_names = ["requirements", "test_spec", "tasks"]
        artifact_jsons = [
            SAMPLE_REQUIREMENTS_JSON,
            SAMPLE_TEST_SPEC_JSON,
            SAMPLE_TASKS_JSON,
        ]

        session = _create_test_session(tmp_path, SessionState.PRD_ACCEPTED)

        side_effects: list = []
        for i in range(failure_point):
            side_effects.append(make_artifact_response(artifact_names[i], artifact_jsons[i]))
        side_effects.append(make_bad_request_error())

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(side_effect=side_effects)

        from agentspec.agent import SpecAgent as _SA

        with patch("agentspec.session._create_agent", return_value=_SA(mock_client, "claude-sonnet-4-6")):
            with pytest.raises(AgentError):
                asyncio.run(session.generate())

        for i in range(failure_point):
            assert (session.spec_dir / f"{artifact_names[i]}.json").exists(), f"{artifact_names[i]}.json should exist"

        assert session.state != SessionState.GENERATED


# ===================================================================
# TS-03-SMOKE-1: Full assessment flow
# ===================================================================


@pytest.mark.asyncio
async def test_smoke_full_assessment_flow(tmp_path: Path) -> None:
    """TS-03-SMOKE-1: Full path from SpecSession.assess() through
    SpecAgent.assess_prd() to persisted Assessment."""
    camp = Campaign.create(tmp_path / "smoke1", "Test", "Desc")
    session = camp.new_spec("test_spec", "# My PRD\n## Intent\nBuild something")

    assessment = Assessment(
        quality="needs_refinement",
        summary="PRD needs goals section",
        gaps=["Missing Goals section"],
        questions=[
            Question(
                id="q1",
                text="What are the goals?",
                context="No goals defined",
                options=[],
                required=True,
            )
        ],
    )

    mock_agent_instance = MagicMock()
    mock_agent_instance.assess_prd = AsyncMock(return_value=assessment)

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        await session.assess()

    assert session.state == SessionState.ASSESSING
    data = json.loads((session.spec_dir / "_session.json").read_text())
    assert data["assessment_history"][-1]["quality"] == "needs_refinement"

    mock_agent_instance.assess_prd.assert_called_once()


# ===================================================================
# TS-03-SMOKE-2: Full refinement flow
# ===================================================================


@pytest.mark.asyncio
async def test_smoke_full_refinement_flow(tmp_path: Path) -> None:
    """TS-03-SMOKE-2: Full path from SpecSession.refine() through
    SpecAgent.refine_prd() to updated PRD and new Assessment."""
    prev_assessment_dict = _sample_assessment_dict()
    session = _create_test_session(
        tmp_path,
        SessionState.ASSESSING,
        assessment_history=[prev_assessment_dict],
    )

    new_assessment = Assessment(
        quality="ready",
        summary="PRD is complete",
        gaps=[],
        questions=[],
    )

    mock_agent_instance = MagicMock()
    mock_agent_instance.refine_prd = AsyncMock(
        return_value=(
            "# Updated PRD\n## Goals\n1. REST API for users",
            new_assessment,
        )
    )

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        await session.refine({"q1": "REST API for users"})

    prd = (session.spec_dir / "prd.md").read_text()
    assert "Updated" in prd or "REST API" in prd

    data = json.loads((session.spec_dir / "_session.json").read_text())
    assert len(data["assessment_history"]) == 2

    mock_agent_instance.refine_prd.assert_called_once()


# ===================================================================
# TS-03-SMOKE-3: Full generation flow
# ===================================================================


@pytest.mark.asyncio
async def test_smoke_full_generation_flow(tmp_path: Path) -> None:
    """TS-03-SMOKE-3: Full path from SpecSession.generate() through
    SpecAgent.generate_artifacts() to written artifact files."""
    from afspec import Requirements, Tasks, TestSpec

    session = _create_test_session(tmp_path, SessionState.PRD_ACCEPTED)

    artifacts = {
        "requirements": Requirements(**SAMPLE_REQUIREMENTS_JSON),
        "test_spec": TestSpec(**SAMPLE_TEST_SPEC_JSON),
        "tasks": Tasks(**SAMPLE_TASKS_JSON),
    }

    mock_agent_instance = MagicMock()
    mock_agent_instance.generate_artifacts = AsyncMock(return_value=artifacts)

    with (
        patch("agentspec.session._create_agent", return_value=mock_agent_instance),
    ):
        await session.generate()

    for name in ["requirements.json", "test_spec.json", "tasks.json"]:
        assert (session.spec_dir / name).exists()
        content = json.loads((session.spec_dir / name).read_text())
        assert isinstance(content, dict)

    assert session.state == SessionState.GENERATED

    mock_agent_instance.generate_artifacts.assert_called_once()


# ===================================================================
# TS-03-SMOKE-4: Retry and recovery
# ===================================================================


@pytest.mark.asyncio
async def test_smoke_retry_and_recovery(tmp_path: Path) -> None:
    """TS-03-SMOKE-4: Full path demonstrating retry on transient error
    followed by successful completion."""
    session = _create_test_session(tmp_path, SessionState.INIT)

    mock_client = MagicMock()
    mock_client.messages.create = AsyncMock(
        side_effect=[
            make_rate_limit_error(),
            make_assessment_response(
                quality="needs_refinement",
                summary="Needs work",
            ),
        ]
    )

    from agentspec.agent import SpecAgent as _SA

    with (
        patch("agentspec.session._create_agent", return_value=_SA(mock_client, "claude-sonnet-4-6")),
        patch("asyncio.sleep", new_callable=AsyncMock),
    ):
        await session.assess()

    assert session.state == SessionState.ASSESSING
    assert mock_client.messages.create.call_count == 2
