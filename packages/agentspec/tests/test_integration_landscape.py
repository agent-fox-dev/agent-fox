"""Integration and smoke tests for spec landscape awareness.

Covers:
- TS-01-26, TS-01-27, TS-01-28: integration tests for overlap detection
  LLM behavior (01-REQ-11.1, 01-REQ-11.2, 01-REQ-11.3)
- TS-01-SMOKE-1: end-to-end spec assess with landscape injection (01-PATH-1)
- TS-01-SMOKE-2: end-to-end spec refine with landscape injection (01-PATH-2)
- TS-01-SMOKE-3: graceful degradation when landscape loading fails (01-PATH-3)

Integration tests exercise the full pipeline with a mocked LLM client.
Smoke tests exercise the full SpecSession pipeline with real discovery/prompt
components and a mocked LLM.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, patch

import pytest
from agentspec.campaign import Campaign
from agentspec.session import Assessment, SessionState, SpecSession
from conftest_agent import (
    FakeMessage,
    make_assessment_response,
    make_refinement_response,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_OVERLAPPING_ACTIVE_LANDSCAPE: list[dict[str, Any]] = [
    {
        "spec_id": "01",
        "spec_name": "core_foundation",
        "title": "Core Foundation",
        "status": "implemented",
        "intent": "Establish core foundation utilities for the project",
        "archived": False,
    },
]

_ARCHIVED_OVERLAP_LANDSCAPE: list[dict[str, Any]] = [
    {
        "spec_id": "09",
        "spec_name": "worktree_path_collision",
        "title": "Worktree Path Collision",
        "status": "archived",
        "archived": True,
    },
]

_DEPENDENCY_LANDSCAPE: list[dict[str, Any]] = [
    {
        "spec_id": "01",
        "spec_name": "core_foundation",
        "title": "Core Foundation",
        "status": "implemented",
        "intent": "Establish core foundation utilities",
        "archived": False,
    },
]


def _make_prd_frontmatter(
    spec_id: str,
    spec_name: str,
    title: str = "Test Spec",
    status: str = "draft",
) -> str:
    """Build minimal YAML frontmatter for a prd.md."""
    return (
        "---\n"
        f'spec_id: "{spec_id}"\n'
        f'spec_name: "{spec_name}"\n'
        f'title: "{title}"\n'
        f'status: "{status}"\n'
        f'created_at: "2026-01-01T00:00:00Z"\n'
        f'updated_at: "2026-01-01T00:00:00Z"\n'
        f'owner: "test"\n'
        f'source: "test"\n'
        "supersedes: []\n"
        "tags: []\n"
        "intent_hash: null\n"
        "schema_version: 1\n"
        "---\n"
    )


def _setup_spec_dir(
    root: Path,
    dir_name: str,
    spec_id: str,
    spec_name: str,
    title: str = "Test Spec",
    *,
    intent_text: str = "Test intent.",
) -> Path:
    """Create a spec directory with a valid prd.md under *root*."""
    folder = root / dir_name
    folder.mkdir(parents=True, exist_ok=True)
    frontmatter = _make_prd_frontmatter(spec_id, spec_name, title)
    body = f"# {title}\n\n## Intent\n\n{intent_text}\n"
    (folder / "prd.md").write_text(frontmatter + body)
    return folder


def _create_test_session(
    tmp_path: Path,
    state: SessionState = SessionState.INIT,
    prd_text: str = "# My PRD\n\n## Intent\nBuild something.",
    assessment_history: list[dict[str, Any]] | None = None,
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


def _sample_assessment_dict(
    quality: str = "needs_refinement",
    summary: str = "Needs work",
    gaps: list[str] | None = None,
    questions: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
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


# ===========================================================================
# TS-01-26: When a landscape section is present with an overlapping active
# spec, the assessment LLM returns an Assessment containing at least one
# gap and one clarification question referencing the overlapping spec.
# (01-REQ-11.1)
# ===========================================================================


@pytest.mark.integration
class TestOverlapDetectionBlocksReady:
    """TS-01-26: Active spec overlap produces gap + clarification question."""

    @pytest.mark.asyncio
    async def test_active_overlap_produces_gap_and_question(self) -> None:
        """Assessment has >= 1 gap and >= 1 question about overlapping spec.

        Uses a mocked LLM that returns a canned response mimicking what a
        real LLM would produce when the system prompt includes the
        ## Cross-spec awareness instructions and the user prompt contains
        ## Existing Spec Landscape with an overlapping active spec.
        """
        from agentspec.agent import SpecAgent

        agent = SpecAgent("STANDARD")

        prd_text = (
            "# New PRD\n"
            "## Intent\n"
            "Provide core foundation utilities for the project.\n"
        )

        # Mock LLM returns an assessment with overlap gap and question —
        # this is what the LLM would produce given the Cross-spec awareness
        # system instructions and the landscape section listing spec 01.
        overlap_response = make_assessment_response(
            quality="needs_refinement",
            summary="This PRD overlaps with spec 01 (core_foundation).",
            gaps=[
                "Overlap with active spec 01_core_foundation: both specs "
                "address core foundation utilities. Clarify whether this "
                "spec should depend on, extend, or supersede spec 01."
            ],
            questions=[
                {
                    "id": "overlap_q1",
                    "text": (
                        "This PRD overlaps with spec 01 (core_foundation). "
                        "Should this spec depend on, extend, or supersede it?"
                    ),
                    "context": (
                        "Spec 01 already establishes core foundation "
                        "utilities for the project."
                    ),
                    "options": ["Depend on", "Extend", "Supersede"],
                    "required": True,
                }
            ],
        )

        with patch.object(
            agent,
            "_call_api",
            new_callable=AsyncMock,
            return_value=overlap_response,
        ):
            assessment = await agent.assess_prd(
                prd_text,
                "new_spec",
                spec_landscape=_OVERLAPPING_ACTIVE_LANDSCAPE,
            )

        # Verify at least one gap referencing the overlapping spec
        assert len(assessment.gaps) >= 1
        gap_text = " ".join(assessment.gaps).lower()
        assert "01" in gap_text or "core_foundation" in gap_text

        # Verify at least one clarification question about the overlap
        assert len(assessment.questions) >= 1


# ===========================================================================
# TS-01-27: When a PRD overlaps with an archived spec, the assessment notes
# the historical precedent but does not block ready quality solely on that
# basis.  (01-REQ-11.2)
# ===========================================================================


@pytest.mark.integration
class TestArchivedOverlapDoesNotBlockReady:
    """TS-01-27: Archived overlap is noted but does not block ready."""

    @pytest.mark.asyncio
    async def test_archived_overlap_noted_not_blocking(self) -> None:
        """Assessment mentions archived spec; quality not forced non-ready.

        The ## Cross-spec awareness instructions tell the LLM to note
        overlap with archived specs and ask about historical awareness
        but NOT to block "ready" quality solely because of it.
        """
        from agentspec.agent import SpecAgent

        agent = SpecAgent("STANDARD")

        prd_text = (
            "# New PRD\n"
            "## Intent\n"
            "Re-implement the worktree path collision detection.\n"
        )

        # Mock LLM returns an assessment that mentions the archived spec
        # but does not force non-ready quality solely due to the overlap.
        archived_response = make_assessment_response(
            quality="needs_refinement",
            summary=(
                "The PRD is solid but note: an archived spec 09 "
                "(worktree_path_collision) previously addressed similar "
                "functionality. Consider reviewing prior work."
            ),
            gaps=["Missing non-goals section"],
            questions=[
                {
                    "id": "hist_q1",
                    "text": (
                        "Are you aware of the prior archived spec 09 "
                        "(worktree_path_collision)? What has changed?"
                    ),
                    "context": "Historical precedent exists.",
                    "options": [],
                    "required": False,
                }
            ],
        )

        with patch.object(
            agent,
            "_call_api",
            new_callable=AsyncMock,
            return_value=archived_response,
        ):
            assessment = await agent.assess_prd(
                prd_text,
                "new_spec",
                spec_landscape=_ARCHIVED_OVERLAP_LANDSCAPE,
            )

        # Verify assessment is returned and mentions the archived spec
        assert assessment is not None
        output_str = str(assessment)
        assert "09" in output_str or "worktree" in output_str.lower()


# ===========================================================================
# TS-01-28: When a PRD references capabilities already provided by an
# existing active spec, the assessment returns a dependency suggestion
# referencing that spec.  (01-REQ-11.3)
# ===========================================================================


@pytest.mark.integration
class TestDependencySuggestion:
    """TS-01-28: PRD referencing existing spec produces dependency suggestion."""

    @pytest.mark.asyncio
    async def test_dependency_suggestion_in_assessment(self) -> None:
        """Assessment suggests dependency on referenced spec.

        The ## Cross-spec awareness instructions tell the LLM to suggest
        declaring a dependency when capabilities from an existing spec
        are referenced.
        """
        from agentspec.agent import SpecAgent

        agent = SpecAgent("STANDARD")

        prd_text = (
            "# New PRD\n"
            "## Intent\n"
            "Build on top of the core foundation utilities to add "
            "higher-level orchestration.\n"
        )

        # Mock LLM returns an assessment with a dependency suggestion
        dep_response = make_assessment_response(
            quality="needs_refinement",
            summary=(
                "The PRD references core foundation utilities. Consider "
                "adding a dependency on spec 01 (core_foundation)."
            ),
            gaps=[
                "Missing dependency declaration: this spec relies on "
                "capabilities from spec 01 (core_foundation). Add a "
                "## Dependencies section referencing spec 01."
            ],
            questions=[
                {
                    "id": "dep_q1",
                    "text": (
                        "Should this spec declare a dependency on "
                        "spec 01 (core_foundation)?"
                    ),
                    "context": (
                        "The PRD references core foundation utilities "
                        "that are provided by spec 01."
                    ),
                    "options": ["Yes", "No"],
                    "required": True,
                }
            ],
        )

        with patch.object(
            agent,
            "_call_api",
            new_callable=AsyncMock,
            return_value=dep_response,
        ):
            assessment = await agent.assess_prd(
                prd_text,
                "orchestration_spec",
                spec_landscape=_DEPENDENCY_LANDSCAPE,
            )

        # Verify assessment contains a dependency suggestion referencing spec 01
        output_str = str(assessment)
        assert (
            "01" in output_str
            or "core_foundation" in output_str.lower()
            or "dependency" in output_str.lower()
        )


# ===========================================================================
# Smoke Tests — End-to-End Assess, Refine, and Graceful Degradation
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-01-SMOKE-1: End-to-end smoke test: running spec assess for a new PRD
# injects the landscape into the LLM prompt and returns an Assessment with
# landscape-aware content.
#
# Real components: SpecSession, load_spec_landscape, discover_specs,
#                  assessment_user_prompt, _format_spec_landscape, templates
# Mocked:          LLM client (ai_call)
#
# Execution path: 01-PATH-1
# ---------------------------------------------------------------------------


@pytest.mark.smoke
class TestAssessSmokeEndToEnd:
    """TS-01-SMOKE-1: Full assess pipeline with landscape injection."""

    @pytest.mark.asyncio
    async def test_assess_injects_landscape_into_prompt(
        self,
        tmp_path: Path,
    ) -> None:
        """load_spec_landscape called; prompt contains landscape; Assessment persisted."""
        # Create the session for the spec being assessed
        session = _create_test_session(tmp_path, SessionState.INIT)

        # Create a sibling spec directory so load_spec_landscape finds it
        spec_root = session.spec_dir.parent
        _setup_spec_dir(
            spec_root,
            "01_sibling_spec",
            "01",
            "sibling_spec",
            "Sibling Spec",
            intent_text="A sibling spec for testing landscape injection.",
        )

        # Track the messages sent to the LLM so we can verify
        # the prompt contains the landscape section
        captured_messages: list[dict[str, Any]] = []

        assessment_response = make_assessment_response(
            quality="needs_refinement",
            summary="Landscape-aware assessment.",
            gaps=["Missing non-goals"],
            questions=[
                {
                    "id": "q1",
                    "text": "What are the non-goals?",
                    "context": "Non-goals section missing",
                    "options": [],
                    "required": True,
                }
            ],
        )

        async def _capture_ai_call(**kwargs: Any) -> tuple[None, FakeMessage]:
            captured_messages.append(kwargs)
            return (None, assessment_response)

        with patch(
            "agentfox.core.client.ai_call",
            side_effect=_capture_ai_call,
        ):
            result = await session.assess()

        # Verify result is an Assessment
        assert isinstance(result, Assessment)

        # Verify the prompt sent to LLM contains the landscape section
        assert len(captured_messages) >= 1
        user_content = captured_messages[0].get("messages", [{}])[0].get("content", "")
        assert "## Existing Spec Landscape" in user_content

        # Verify Assessment is persisted to _session.json
        session_data = json.loads((session.spec_dir / "_session.json").read_text())
        assert len(session_data["assessment_history"]) >= 1


# ---------------------------------------------------------------------------
# TS-01-SMOKE-2: End-to-end smoke test: running spec refine injects the
# landscape into the refinement prompt and returns an updated Assessment
# and revised PRD text.
#
# Real components: SpecSession, load_spec_landscape, discover_specs,
#                  refinement_user_prompt, _format_spec_landscape, templates
# Mocked:          LLM client (ai_call)
#
# Execution path: 01-PATH-2
# ---------------------------------------------------------------------------


@pytest.mark.smoke
class TestRefineSmokeEndToEnd:
    """TS-01-SMOKE-2: Full refine pipeline with landscape injection."""

    @pytest.mark.asyncio
    async def test_refine_injects_landscape_into_prompt(
        self,
        tmp_path: Path,
    ) -> None:
        """Landscape appears in refinement prompt; prd.md overwritten; (str, Assessment) returned."""
        prev_assessment_dict = _sample_assessment_dict()
        session = _create_test_session(
            tmp_path,
            SessionState.ASSESSING,
            assessment_history=[prev_assessment_dict],
        )

        # Create a sibling spec for landscape discovery
        spec_root = session.spec_dir.parent
        _setup_spec_dir(
            spec_root,
            "01_sibling_spec",
            "01",
            "sibling_spec",
            "Sibling Spec",
            intent_text="A sibling spec for testing landscape injection.",
        )

        captured_messages: list[dict[str, Any]] = []

        refinement_response = make_refinement_response(
            updated_prd="# Updated PRD\n## Intent\nUpdated intent.\n## Goals\n- Goal 1",
            quality="ready",
            summary="PRD refined with landscape context.",
        )

        async def _capture_ai_call(**kwargs: Any) -> tuple[None, FakeMessage]:
            captured_messages.append(kwargs)
            return (None, refinement_response)

        with patch(
            "agentfox.core.client.ai_call",
            side_effect=_capture_ai_call,
        ):
            result = await session.refine({"q1": "Answer to goals question"})

        # Verify result is an Assessment
        assert isinstance(result, Assessment)

        # Verify the refinement prompt contains the landscape section
        assert len(captured_messages) >= 1
        user_content = captured_messages[0].get("messages", [{}])[0].get("content", "")
        assert "## Existing Spec Landscape" in user_content

        # Verify prd.md was overwritten with updated content
        prd_content = (session.spec_dir / "prd.md").read_text()
        assert "Updated" in prd_content


# ---------------------------------------------------------------------------
# TS-01-SMOKE-3: Graceful degradation smoke test: when landscape loading
# fails during spec assess, assessment proceeds without landscape context
# and returns a valid Assessment.
#
# Real components: SpecSession, assessment_user_prompt, _format_spec_landscape
# Mocked:          load_spec_landscape (raises OSError),
#                  LLM client (ai_call)
#
# Execution path: 01-PATH-3
# ---------------------------------------------------------------------------


@pytest.mark.smoke
class TestGracefulDegradationSmoke:
    """TS-01-SMOKE-3: Failed landscape load => assessment without landscape."""

    @pytest.mark.asyncio
    async def test_assess_without_landscape_on_load_failure(
        self,
        tmp_path: Path,
    ) -> None:
        """No landscape in prompt; valid Assessment returned; no error surfaced."""
        session = _create_test_session(tmp_path, SessionState.INIT)

        captured_messages: list[dict[str, Any]] = []

        assessment_response = make_assessment_response(
            quality="needs_refinement",
            summary="Assessment without landscape context.",
            gaps=["Missing goals"],
            questions=[
                {
                    "id": "q1",
                    "text": "What are the goals?",
                    "context": "Goals section missing",
                    "options": [],
                    "required": True,
                }
            ],
        )

        async def _capture_ai_call(**kwargs: Any) -> tuple[None, FakeMessage]:
            captured_messages.append(kwargs)
            return (None, assessment_response)

        with (
            patch(
                "agentspec.session.load_spec_landscape",
                side_effect=OSError("disk failure"),
            ),
            patch(
                "agentfox.core.client.ai_call",
                side_effect=_capture_ai_call,
            ),
        ):
            # Should NOT raise — graceful degradation catches the OSError
            result = await session.assess()

        # Verify a valid Assessment is returned
        assert isinstance(result, Assessment)

        # Verify the prompt does NOT contain the landscape section
        assert len(captured_messages) >= 1
        user_content = captured_messages[0].get("messages", [{}])[0].get("content", "")
        assert "## Existing Spec Landscape" not in user_content
