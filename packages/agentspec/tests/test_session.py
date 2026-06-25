"""Tests for spec authoring session state machine and persistence.

Test Spec Entries: TS-02-10 through TS-02-19 (acceptance criteria),
TS-02-E7 through TS-02-E11 (edge cases),
TS-02-P1, TS-02-P2, TS-02-P5, TS-02-P6 (property tests),
TS-02-SMOKE-3 through TS-02-SMOKE-5 (integration smoke tests),
TS-07-1 through TS-07-7, TS-07-E1, TS-07-E2 (QA exchange unit tests),
TS-07-P1 through TS-07-P4 (QA exchange property tests),
TS-07-SMOKE-1 (QA exchange integration smoke test).
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentspec.campaign import Campaign
from agentspec.errors import AgentError, SessionError
from agentspec.session import (
    Assessment,
    Question,
    SessionState,
    SpecSession,
    ValidationResult,
)
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st

# Methods that became async in spec 03 (agent integration).
_ASYNC_METHODS = frozenset({"assess", "refine", "generate"})


def _run_sync(coro):  # type: ignore[no-untyped-def]
    """Run an async coroutine synchronously for spec-02 tests."""
    loop = asyncio.new_event_loop()
    try:
        return loop.run_until_complete(coro)
    finally:
        loop.close()


# ---------------------------------------------------------------------------
# Helper: create a session in a specific state for testing
# ---------------------------------------------------------------------------


def _create_session(tmp_path: Path, state: SessionState | None = None) -> SpecSession:
    """Create a SpecSession in a spec directory, optionally in a given state.

    Uses Campaign.new_spec to create the session in init state, then
    directly writes the desired state to _session.json for test setup.
    """
    camp_dir = tmp_path / "camp"
    if not (camp_dir / "campaign.yaml").exists():
        Campaign.create(camp_dir, "Test", "Desc")
    camp = Campaign.open(camp_dir)

    # Create a new spec each call (unique name from state)
    spec_name = f"s_{state.value if state else 'init'}".replace(" ", "_")
    session = camp.new_spec(spec_name, "PRD content")

    if state is not None and state != SessionState.INIT:
        # Directly set state in _session.json for test precondition setup
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["state"] = state.value
        session_file.write_text(json.dumps(data))
        # Resume to get a session in the desired state
        session = SpecSession.resume(session.spec_dir)

    return session


def _create_session_with_all_artifacts(tmp_path: Path) -> SpecSession:
    """Create a SpecSession whose spec dir has all four required artifacts."""
    camp_dir = tmp_path / "camp_artifacts"
    if not (camp_dir / "campaign.yaml").exists():
        Campaign.create(camp_dir, "Test", "Desc")
    camp = Campaign.open(camp_dir)
    session = camp.new_spec("full_spec", "# PRD\n\nContent")

    # Create the four required artifacts
    spec_dir = session.spec_dir
    for artifact in ["requirements.json", "test_spec.json", "tasks.json"]:
        (spec_dir / artifact).write_text(f'{{"placeholder": "{artifact}"}}')

    return session


# ---------------------------------------------------------------------------
# Acceptance criterion tests: TS-02-10 through TS-02-19
# ---------------------------------------------------------------------------


class TestSessionStateEnum:
    """Tests for SessionState enum — TS-02-10."""

    def test_ts02_10_session_state_enum_values(self) -> None:
        """TS-02-10: SessionState enum has all six required values.

        Requirement: 02-REQ-4.1
        """
        assert SessionState.INIT.value == "init"
        assert SessionState.ASSESSING.value == "assessing"
        assert SessionState.REFINING.value == "refining"
        assert SessionState.PRD_ACCEPTED.value == "prd_accepted"
        assert SessionState.GENERATING.value == "generating"
        assert SessionState.GENERATED.value == "generated"
        assert len(SessionState) == 6


class TestStateTransitions:
    """Tests for session state machine transitions — TS-02-11 through TS-02-13."""

    def test_ts02_11_legal_state_transitions(self, tmp_path: Path) -> None:
        """TS-02-11: Legal transitions succeed, illegal transitions raise SessionError.

        Requirement: 02-REQ-4.2

        Legal transitions (from design.md):
          init -> assessing (assess)
          assessing -> refining (refine)
          assessing -> prd_accepted (accept_prd)
          refining -> assessing (assess)
          refining -> prd_accepted (accept_prd)
          prd_accepted -> generating (generate)

        The async methods (assess, refine, generate) are now implemented
        by spec 03.  For legal transitions we verify that the state check
        passes (no SessionError).  Full agent behaviour is exercised in
        test_session_agent.py.
        """
        # Mock _create_agent to raise a non-SessionError so we can verify
        # the state check passes without hitting the network.
        agent_error = RuntimeError("no real agent")

        with patch("agentspec.session._create_agent", side_effect=agent_error):
            # Legal: init -> assessing via assess()
            session = _create_session(tmp_path, SessionState.INIT)
            try:
                _run_sync(session.assess())
            except SessionError:
                pytest.fail("Legal transition init->assess raised SessionError")
            except Exception:
                pass

            # Illegal: init -> refining via refine()
            session = _create_session(tmp_path, SessionState.INIT)
            with pytest.raises(SessionError):
                _run_sync(session.refine({}))

            # Illegal: init -> generating via generate()
            session = _create_session(tmp_path, SessionState.INIT)
            with pytest.raises(SessionError):
                _run_sync(session.generate())

            # Legal: assessing -> refining via refine()
            session = _create_session(tmp_path, SessionState.ASSESSING)
            try:
                _run_sync(session.refine({}))
            except SessionError:
                pytest.fail("Legal transition assessing->refine raised SessionError")
            except Exception:
                pass

        # Legal: assessing -> prd_accepted via accept_prd()
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session.accept_prd()
        assert session.state == SessionState.PRD_ACCEPTED

        with patch("agentspec.session._create_agent", side_effect=agent_error):
            # Legal: refining -> assessing via assess()
            session = _create_session(tmp_path, SessionState.REFINING)
            try:
                _run_sync(session.assess())
            except SessionError:
                pytest.fail("Legal transition refining->assess raised SessionError")
            except Exception:
                pass

        # Legal: refining -> prd_accepted via accept_prd()
        session = _create_session(tmp_path, SessionState.REFINING)
        session.accept_prd()
        assert session.state == SessionState.PRD_ACCEPTED

        with patch("agentspec.session._create_agent", side_effect=agent_error):
            # Legal: prd_accepted -> generating via generate()
            session = _create_session(tmp_path, SessionState.PRD_ACCEPTED)
            try:
                _run_sync(session.generate())
            except SessionError:
                pytest.fail("Legal transition prd_accepted->generate raised SessionError")
            except Exception:
                pass

        # Illegal: prd_accepted -> assess()
        session = _create_session(tmp_path, SessionState.PRD_ACCEPTED)
        with pytest.raises(SessionError):
            _run_sync(session.assess())

    def test_ts02_12_illegal_transition_error_message(self, tmp_path: Path) -> None:
        """TS-02-12: SessionError names current state and required state.

        Requirement: 02-REQ-4.3
        """
        session = _create_session(tmp_path, SessionState.INIT)
        with pytest.raises(SessionError) as exc_info:
            _run_sync(session.generate())

        error_msg = str(exc_info.value)
        assert "init" in error_msg
        assert "prd_accepted" in error_msg

    def test_ts02_13_accept_prd_from_assessing_and_refining(self, tmp_path: Path) -> None:
        """TS-02-13: accept_prd() works from both assessing and refining states.

        Requirement: 02-REQ-4.4
        """
        session_a = _create_session(tmp_path, SessionState.ASSESSING)
        session_a.accept_prd()
        assert session_a.state == SessionState.PRD_ACCEPTED

        session_r = _create_session(tmp_path, SessionState.REFINING)
        session_r.accept_prd()
        assert session_r.state == SessionState.PRD_ACCEPTED


class TestSessionPersistence:
    """Tests for session persistence — TS-02-14 through TS-02-16."""

    def test_ts02_14_state_persisted_on_transition(self, tmp_path: Path) -> None:
        """TS-02-14: _session.json is updated on every state transition.

        Requirement: 02-REQ-5.1
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session.accept_prd()

        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert data["state"] == "prd_accepted"

    def test_ts02_15_session_resume(self, tmp_path: Path) -> None:
        """TS-02-15: SpecSession.resume restores session state from _session.json.

        Requirement: 02-REQ-5.2
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session.accept_prd()

        resumed = SpecSession.resume(session.spec_dir)
        assert resumed.state == SessionState.PRD_ACCEPTED
        assert resumed.spec_dir == session.spec_dir

    def test_ts02_16_session_json_fields(self, tmp_path: Path) -> None:
        """TS-02-16: _session.json contains all required fields.

        Requirement: 02-REQ-5.3
        """
        camp_dir = tmp_path / "json_fields"
        camp = Campaign.create(camp_dir, "Test", "Desc")
        session = camp.new_spec("test_spec", "PRD content")

        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert "state" in data
        assert "prd_path" in data
        assert "assessment_history" in data
        assert "qa_exchanges" in data
        assert "generated_artifacts" in data
        assert "mode" in data


class TestSessionValidateRender:
    """Tests for validate() and render() — TS-02-17 through TS-02-19."""

    def test_ts02_17_validate_with_artifacts(self, tmp_path: Path) -> None:
        """TS-02-17: validate() loads spec via afspec and returns ValidationResult.

        Requirement: 02-REQ-6.1
        """
        session = _create_session_with_all_artifacts(tmp_path)

        mock_spec = MagicMock()

        with (
            patch("agentspec.session.afspec") as mock_afspec,
        ):
            mock_afspec.load_spec.return_value = mock_spec
            mock_afspec.validate.return_value = MagicMock(valid=True, errors=[], warnings=[])

            result = session.validate()

        assert isinstance(result, ValidationResult)
        assert result.valid is True
        assert result.schema_errors == []
        assert result.integrity_errors == []

    def test_ts02_18_render_combined(self, tmp_path: Path) -> None:
        """TS-02-18: render(combined=True) returns a single markdown string.

        Requirement: 02-REQ-6.2
        """
        session = _create_session_with_all_artifacts(tmp_path)

        mock_spec = MagicMock()

        with patch("agentspec.session.afspec") as mock_afspec:
            mock_afspec.load_spec.return_value = mock_spec
            mock_afspec.render_combined.return_value = "# Combined"

            result = session.render(combined=True)

        assert isinstance(result, str)
        assert len(result) > 0

    def test_ts02_19_render_individual(self, tmp_path: Path) -> None:
        """TS-02-19: render(combined=False) returns artifact name dict.

        Requirement: 02-REQ-6.3
        """
        session = _create_session_with_all_artifacts(tmp_path)

        mock_spec = MagicMock()

        with patch("agentspec.session.afspec") as mock_afspec:
            mock_afspec.load_spec.return_value = mock_spec
            mock_afspec.render_individual.return_value = {"prd": "# PRD content"}

            result = session.render(combined=False)

        assert isinstance(result, dict)
        assert all(isinstance(k, str) and isinstance(v, str) for k, v in result.items())


# ---------------------------------------------------------------------------
# Edge case tests: TS-02-E7 through TS-02-E11
# ---------------------------------------------------------------------------


class TestSessionEdgeCases:
    """Edge case tests for session operations — TS-02-E7 through TS-02-E11."""

    def test_ts02_e7_generate_from_wrong_state(self, tmp_path: Path) -> None:
        """TS-02-E7: SessionError when generate() called from non-prd_accepted state.

        Requirement: 02-REQ-4.E1
        """
        session_init = _create_session(tmp_path, SessionState.INIT)
        with pytest.raises(SessionError):
            _run_sync(session_init.generate())

        session_assessing = _create_session(tmp_path, SessionState.ASSESSING)
        with pytest.raises(SessionError):
            _run_sync(session_assessing.generate())

    def test_ts02_e8_assess_from_generated(self, tmp_path: Path) -> None:
        """TS-02-E8: SessionError when assess() called from generated terminal state.

        Requirement: 02-REQ-4.E2
        """
        session = _create_session(tmp_path, SessionState.GENERATED)
        with pytest.raises(SessionError):
            _run_sync(session.assess())

    def test_ts02_e9_resume_no_session_json(self, tmp_path: Path) -> None:
        """TS-02-E9: SessionError when resume() called without _session.json.

        Requirement: 02-REQ-5.E1
        """
        empty_dir = tmp_path / "no_session"
        empty_dir.mkdir()

        with pytest.raises(SessionError):
            SpecSession.resume(empty_dir)

    def test_ts02_e10_resume_invalid_json(self, tmp_path: Path) -> None:
        """TS-02-E10: SessionError when _session.json contains invalid JSON.

        Requirement: 02-REQ-5.E2
        """
        bad_dir = tmp_path / "bad_json"
        bad_dir.mkdir()
        (bad_dir / "_session.json").write_text("{invalid json!!!")

        with pytest.raises(SessionError):
            SpecSession.resume(bad_dir)

    def test_ts02_e11_validate_render_missing_artifacts(self, tmp_path: Path) -> None:
        """TS-02-E11: SessionError when validate/render called with missing artifacts.

        Requirement: 02-REQ-6.E1
        """
        # Session with only prd.md — missing requirements.json, test_spec.json, tasks.json
        camp_dir = tmp_path / "missing_artifacts"
        camp = Campaign.create(camp_dir, "Test", "Desc")
        session = camp.new_spec("incomplete", "PRD content")

        with pytest.raises(SessionError) as exc_info:
            session.validate()
        error_msg = str(exc_info.value)
        assert "requirements.json" in error_msg
        assert "test_spec.json" in error_msg
        assert "tasks.json" in error_msg

        with pytest.raises(SessionError) as exc_info:
            session.render()
        error_msg = str(exc_info.value)
        assert "requirements.json" in error_msg


# ---------------------------------------------------------------------------
# Property tests: TS-02-P1, TS-02-P2, TS-02-P5, TS-02-P6
# ---------------------------------------------------------------------------

# Legal transitions: (source_state, method_name) -> target_state
_LEGAL_TRANSITIONS: dict[tuple[str, str], str] = {
    ("init", "assess"): "assessing",
    ("assessing", "refine"): "refining",
    ("assessing", "accept_prd"): "prd_accepted",
    ("refining", "refine"): "refining",
    ("refining", "assess"): "assessing",
    ("refining", "accept_prd"): "prd_accepted",
    ("prd_accepted", "generate"): "generating",
    ("generating", "generate"): "generated",  # resume after partial failure
}

# All four required artifacts for validate/render
_REQUIRED_ARTIFACTS = frozenset({"prd.md", "requirements.json", "test_spec.json", "tasks.json"})


class TestSessionProperties:
    """Property tests for session state machine.

    Covers TS-02-P1, TS-02-P2, TS-02-P5, TS-02-P6.
    """

    def test_ts02_p1_property_state_machine_total(self, tmp_path: Path) -> None:
        """TS-02-P1: State machine transitions are total and exclusive.

        Property 1: For any session state and method call, the result is
        either a successful transition to a defined target state or a
        SessionError. No other outcome.

        Validates: 02-REQ-4.2, 02-REQ-4.3

        Note: assess, refine, and generate are now async (spec 03).
        For legal transitions of async methods, we verify the state
        check passes (no SessionError) without running the full agent
        implementation.  For illegal transitions, we run the coroutine
        and verify SessionError.
        """
        methods = ["assess", "refine", "accept_prd", "generate"]
        agent_error = RuntimeError("no real agent")

        with patch("agentspec.session._create_agent", side_effect=agent_error):
            for state in SessionState:
                for method_name in methods:
                    session = _create_session(tmp_path, state)
                    key = (state.value, method_name)
                    is_async = method_name in _ASYNC_METHODS

                    if key in _LEGAL_TRANSITIONS:
                        if is_async:
                            try:
                                if method_name == "refine":
                                    _run_sync(getattr(session, method_name)({}))
                                else:
                                    _run_sync(getattr(session, method_name)())
                            except SessionError:
                                pytest.fail(f"Legal transition {key} raised SessionError")
                            except Exception:
                                pass
                        else:
                            getattr(session, method_name)()
                            expected = _LEGAL_TRANSITIONS[key]
                            assert session.state == SessionState(expected), (
                                f"Expected state {expected} after "
                                f"{method_name}() from {state.value}, "
                                f"got {session.state.value}"
                            )
                    else:
                        if is_async:
                            with pytest.raises(SessionError):
                                if method_name == "refine":
                                    _run_sync(getattr(session, method_name)({}))
                                else:
                                    _run_sync(getattr(session, method_name)())
                        else:
                            with pytest.raises(SessionError):
                                if method_name == "refine":
                                    getattr(session, method_name)({})
                                else:
                                    getattr(session, method_name)()

    def test_ts02_p2_property_persistence_idempotent(self, tmp_path: Path) -> None:
        """TS-02-P2: Session persistence is idempotent on resume.

        Property 2: For any session that has undergone valid transitions,
        persisting and resuming produces an equivalent session.

        Validates: 02-REQ-5.1, 02-REQ-5.2
        """
        # Test various reachable states via non-stub transitions
        # Only accept_prd is non-stub, so reachable persisted states are:
        # init (from creation), assessing (set directly), prd_accepted (via accept_prd)
        reachable_states = [
            SessionState.INIT,
            SessionState.ASSESSING,
            SessionState.REFINING,
            SessionState.PRD_ACCEPTED,
            SessionState.GENERATING,
            SessionState.GENERATED,
        ]

        for state in reachable_states:
            session = _create_session(tmp_path, state)
            original_state = session.state
            original_dir = session.spec_dir

            resumed = SpecSession.resume(session.spec_dir)

            assert resumed.state == original_state, f"Resumed state {resumed.state} != original {original_state}"
            assert resumed.spec_dir == original_dir

    @given(
        subset_bits=st.integers(min_value=0, max_value=14),
    )
    @settings(
        max_examples=15,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts02_p5_property_artifacts_required(self, subset_bits: int, tmp_path: Path) -> None:
        """TS-02-P5: validate() and render() require all four artifacts.

        Property 5: For any strict subset of the four required artifacts,
        validate() and render() raise SessionError.

        Validates: 02-REQ-6.1, 02-REQ-6.E1
        """
        all_artifacts = sorted(_REQUIRED_ARTIFACTS)
        # Generate a subset from bits (0-14 maps to all strict subsets
        # since 15 = full set)
        subset = {a for i, a in enumerate(all_artifacts) if subset_bits & (1 << i)}

        # Skip the full set — that's not a strict subset
        if subset == _REQUIRED_ARTIFACTS:
            return

        # Create session with only the selected artifacts
        camp_dir = tmp_path / f"art_{subset_bits}"
        if not (camp_dir / "campaign.yaml").exists():
            Campaign.create(camp_dir, "Test", "Desc")
        camp = Campaign.open(camp_dir)
        session = camp.new_spec(f"s{subset_bits}", "PRD content")

        # prd.md is always created by new_spec, add the rest from subset
        for artifact in subset:
            if artifact != "prd.md":
                (session.spec_dir / artifact).write_text(f'{{"placeholder": "{artifact}"}}')

        missing = _REQUIRED_ARTIFACTS - subset - {"prd.md"}

        if missing or "prd.md" not in subset:
            # Not all artifacts present — should raise SessionError
            actual_missing = _REQUIRED_ARTIFACTS - (subset | {"prd.md"})
            if actual_missing:
                with pytest.raises(SessionError) as exc_info:
                    session.validate()
                error_msg = str(exc_info.value)
                for name in actual_missing:
                    assert name in error_msg

                with pytest.raises(SessionError):
                    session.render()

    def test_ts02_p6_property_accept_prd_states(self, tmp_path: Path) -> None:
        """TS-02-P6: accept_prd() is only callable from assessing or refining.

        Property 6: accept_prd() succeeds only from assessing or refining;
        all other states raise SessionError.

        Validates: 02-REQ-4.4
        """
        for state in SessionState:
            session = _create_session(tmp_path, state)
            if state in {SessionState.ASSESSING, SessionState.REFINING}:
                session.accept_prd()
                assert session.state == SessionState.PRD_ACCEPTED
            else:
                with pytest.raises(SessionError):
                    session.accept_prd()


# ---------------------------------------------------------------------------
# Integration smoke tests: TS-02-SMOKE-3 through TS-02-SMOKE-5
# ---------------------------------------------------------------------------


class TestSessionSmokeTests:
    """Integration smoke tests for session operations."""

    def test_ts02_smoke_3_session_lifecycle(self, tmp_path: Path) -> None:
        """TS-02-SMOKE-3: Full lifecycle through accept_prd.

        Execution Path: Path 4 from design.md.
        Must NOT satisfy with: Mocking SpecSession or its persistence layer.
        """
        camp = Campaign.create(tmp_path / "smoke3", "Test", "Desc")
        session = camp.new_spec("lifecycle", "PRD content")

        # Simulate assess having run (set state directly for test)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["state"] = SessionState.ASSESSING.value
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        session.accept_prd()
        assert session.state == SessionState.PRD_ACCEPTED

        resumed = SpecSession.resume(session.spec_dir)
        assert resumed.state == SessionState.PRD_ACCEPTED

    def test_ts02_smoke_4_session_resume(self, tmp_path: Path) -> None:
        """TS-02-SMOKE-4: Create session, transition states, resume.

        Execution Path: Path 5 from design.md.
        Must NOT satisfy with: Mocking SpecSession persistence.
        """
        camp = Campaign.create(tmp_path / "smoke4", "Test", "Desc")
        session = camp.new_spec("resume_test", "PRD")

        # Set to assessing state directly, then accept_prd
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["state"] = SessionState.ASSESSING.value
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        session.accept_prd()
        assert session.state == SessionState.PRD_ACCEPTED
        original_dir = session.spec_dir

        resumed = SpecSession.resume(original_dir)
        assert resumed.state == SessionState.PRD_ACCEPTED
        assert resumed.spec_dir == original_dir

    def test_ts02_smoke_5_validate_and_render(self, tmp_path: Path) -> None:
        """TS-02-SMOKE-5: Validation and rendering end-to-end.

        Execution Path: Path 6 from design.md.
        Must NOT satisfy with: Mocking validate/render methods themselves
        (afspec internals may be mocked).
        """
        session = _create_session_with_all_artifacts(tmp_path)

        mock_spec = MagicMock()

        with patch("agentspec.session.afspec") as mock_afspec:
            mock_afspec.load_spec.return_value = mock_spec
            mock_afspec.validate.return_value = MagicMock(valid=True, errors=[], warnings=[])
            mock_afspec.render_combined.return_value = "# Combined Output"
            mock_afspec.render_individual.return_value = {
                "prd": "# PRD",
                "requirements": "# Requirements",
            }

            result = session.validate()
            assert isinstance(result, ValidationResult)

            combined = session.render(combined=True)
            assert isinstance(combined, str)

            individual = session.render(combined=False)
            assert isinstance(individual, dict)


# ---------------------------------------------------------------------------
# pending_questions tests: TS-06 spec
# ---------------------------------------------------------------------------


class TestPendingQuestions:
    """Tests for SpecSession.pending_questions()."""

    def test_pending_questions_returns_dicts(self, tmp_path: Path) -> None:
        """TS-06-P2 (partial): pending_questions returns list of dicts with
        correct keys from latest assessment.

        Requirement: 06-REQ-2.1
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "Needs work",
                "gaps": [],
                "questions": [
                    {
                        "id": "q1",
                        "text": "What scope?",
                        "context": "Clarify scope",
                        "options": ["A", "B"],
                        "required": True,
                    },
                    {
                        "id": "q2",
                        "text": "Which DB?",
                        "context": "Pick database",
                        "options": [],
                        "required": False,
                    },
                ],
            }
        ]
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        result = session.pending_questions()

        assert len(result) == 2
        assert all(isinstance(q, dict) for q in result)
        for q in result:
            assert set(q.keys()) == {"id", "text", "context", "options", "required"}
        assert result[0]["id"] == "q1"
        assert result[0]["text"] == "What scope?"
        assert result[0]["options"] == ["A", "B"]
        assert result[0]["required"] is True
        assert result[1]["id"] == "q2"

    def test_pending_questions_empty_history(self, tmp_path: Path) -> None:
        """TS-06-P2 (partial): pending_questions returns empty list when
        no assessment exists.

        Requirement: 06-REQ-2.2
        """
        session = _create_session(tmp_path, SessionState.INIT)
        result = session.pending_questions()
        assert result == []

    def test_pending_questions_defaults(self, tmp_path: Path) -> None:
        """TS-06-E3: pending_questions uses defaults for missing optional fields.

        Requirement: 06-REQ-2.E1
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "Needs work",
                "gaps": [],
                "questions": [
                    {
                        "id": "q1",
                        "text": "Question?",
                        "context": "Context",
                    }
                ],
            }
        ]
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        result = session.pending_questions()
        assert result[0]["options"] == []
        assert result[0]["required"] is False

    def test_pending_questions_read_only(self, tmp_path: Path) -> None:
        """TS-06-P3: pending_questions does not modify session state.

        Requirement: 06-REQ-2.3
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "S",
                "gaps": [],
                "questions": [
                    {
                        "id": "q1",
                        "text": "Q?",
                        "context": "C",
                        "options": [],
                        "required": True,
                    }
                ],
            }
        ]
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        state_before = session.state
        history_before = json.loads(session_file.read_text())

        session.pending_questions()

        assert session.state == state_before
        history_after = json.loads(session_file.read_text())
        assert history_before == history_after

    @given(
        question_ids=st.lists(
            st.text(
                alphabet=st.characters(
                    whitelist_categories=("L", "N"),
                    whitelist_characters="_",
                ),
                min_size=1,
                max_size=10,
            ),
            min_size=1,
            max_size=10,
            unique=True,
        ),
    )
    @settings(
        max_examples=20,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_answer_template_key_parity(self, question_ids: list[str], tmp_path: Path) -> None:
        """TS-06-P1: Answer template keys match question IDs exactly.

        Property 1 from design.md.
        Validates: 06-REQ-1.2, 06-REQ-1.3

        For any non-empty set of questions, the answer template keys
        match the question IDs exactly — no extra, no missing.
        """
        session = _create_session(tmp_path, SessionState.ASSESSING)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "Test",
                "gaps": [],
                "questions": [
                    {
                        "id": qid,
                        "text": f"Question {qid}?",
                        "context": f"Context for {qid}",
                        "options": [],
                        "required": False,
                    }
                    for qid in question_ids
                ],
            }
        ]
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        questions = session.pending_questions()
        answers = {q["id"]: "" for q in questions}

        # Key parity: answer template keys == question IDs
        assert set(answers.keys()) == {q["id"] for q in questions}
        # All values are empty strings
        assert all(v == "" for v in answers.values())
        # Length parity
        assert len(questions) == len(question_ids)

    @given(
        num_questions=st.integers(min_value=0, max_value=10),
        has_options=st.booleans(),
        has_required=st.booleans(),
    )
    @settings(
        max_examples=20,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_pending_questions_fidelity(
        self,
        num_questions: int,
        has_options: bool,
        has_required: bool,
        tmp_path: Path,
    ) -> None:
        """TS-06-P2: pending_questions output matches assessment questions.

        Property 2 from design.md.
        Validates: 06-REQ-2.1, 06-REQ-2.E1

        For any assessment history with 0-10 questions (some with
        missing optional fields), pending_questions() returns a list
        whose length equals the number of questions, and each dict
        contains matching values for all five keys.
        """
        raw_questions = []
        for i in range(num_questions):
            q: dict = {
                "id": f"pq{i}",
                "text": f"Question {i}?",
                "context": f"Context {i}",
            }
            if has_options:
                q["options"] = [f"opt_{i}_a", f"opt_{i}_b"]
            if has_required:
                q["required"] = True
            raw_questions.append(q)

        session = _create_session(tmp_path, SessionState.ASSESSING)
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "Fidelity test",
                "gaps": [],
                "questions": raw_questions,
            }
        ]
        session_file.write_text(json.dumps(data))
        session = SpecSession.resume(session.spec_dir)

        result = session.pending_questions()

        # Length match
        assert len(result) == num_questions

        # Content fidelity
        for r, q in zip(result, raw_questions):
            assert r["id"] == q["id"]
            assert r["text"] == q["text"]
            assert r["context"] == q["context"]
            # Defaults applied for missing optional fields
            expected_options = q.get("options", [])
            expected_required = q.get("required", False)
            assert r["options"] == expected_options
            assert r["required"] == expected_required


# ---------------------------------------------------------------------------
# QA Exchange helpers
# ---------------------------------------------------------------------------


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


def _create_qa_exchange_session(
    tmp_path: Path,
    state: SessionState = SessionState.ASSESSING,
    assessment_history: list[dict] | None = None,
    qa_exchanges: list[dict] | None = None,
) -> SpecSession:
    """Create a SpecSession for QA exchange tests.

    Sets up a session in the given state with specified assessment history
    and qa_exchanges. Uses Campaign.new_spec, then patches _session.json.
    """
    import time

    camp_dir = tmp_path / "camp"
    if not (camp_dir / "campaign.yaml").exists():
        Campaign.create(camp_dir, "Test", "Desc")
    camp = Campaign.open(camp_dir)

    spec_name = f"qa_{state.value}_{int(time.monotonic_ns())}"
    session = camp.new_spec(spec_name, "# My PRD\n\n## Intent\nBuild something.")

    if assessment_history is None:
        assessment_history = [_sample_assessment_dict()]

    session_file = session.spec_dir / "_session.json"
    data = json.loads(session_file.read_text())
    data["state"] = state.value
    data["assessment_history"] = assessment_history
    if qa_exchanges is not None:
        data["qa_exchanges"] = qa_exchanges
    session_file.write_text(json.dumps(data, indent=2))
    return SpecSession.resume(session.spec_dir)


def _mock_agent_for_refine(
    updated_prd: str = "# Updated PRD\n## Goals\n1. REST API",
    quality: str = "needs_refinement",
    summary: str = "More work needed",
    gaps: list[str] | None = None,
    questions: list[dict] | None = None,
):
    """Create a mock agent instance that returns from refine_prd."""
    if gaps is None:
        gaps = ["Still has gaps"]
    if questions is None:
        questions = [
            {
                "id": "q2",
                "text": "Next question?",
                "context": "Follow up",
                "options": [],
                "required": False,
            }
        ]
    new_assessment = Assessment(
        quality=quality,
        summary=summary,
        gaps=gaps,
        questions=[
            Question(
                id=q["id"],
                text=q["text"],
                context=q["context"],
                options=q.get("options", []),
                required=q.get("required", False),
            )
            for q in questions
        ],
    )
    mock_agent_instance = MagicMock()
    mock_agent_instance.refine_prd = AsyncMock(return_value=(updated_prd, new_assessment))
    return mock_agent_instance


# ---------------------------------------------------------------------------
# QA exchange recording tests: TS-07-1 through TS-07-5
# ---------------------------------------------------------------------------


class TestQAExchangeRecording:
    """Tests for QA exchange recording during refine — TS-07-1 through TS-07-5."""

    @pytest.mark.asyncio
    async def test_ts07_1_refine_appends_qa_exchange(self, tmp_path: Path) -> None:
        """TS-07-1: Refine appends QA exchange entry.

        Requirement: 07-REQ-1.1
        Verifies that a successful refine() call appends one entry to
        qa_exchanges with the required keys.
        """
        session = _create_qa_exchange_session(tmp_path)
        mock_agent = _mock_agent_for_refine()

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent),
        ):
            await session.refine({"q1": "answer1", "q2": "answer2"})

        assert len(session._qa_exchanges) == 1
        entry = session._qa_exchanges[0]
        assert "assessment_index" in entry
        assert "answers" in entry
        assert "timestamp" in entry

    @pytest.mark.asyncio
    async def test_ts07_2_qa_exchange_persisted_to_disk(self, tmp_path: Path) -> None:
        """TS-07-2: QA exchange persisted to _session.json.

        Requirement: 07-REQ-1.2
        Verifies that the QA exchange entry appears in the persisted
        _session.json file.
        """
        session = _create_qa_exchange_session(tmp_path)
        mock_agent = _mock_agent_for_refine()

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent),
        ):
            await session.refine({"q1": "a1"})

        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert len(data["qa_exchanges"]) == 1

    @pytest.mark.asyncio
    async def test_ts07_3_assessment_index_correct(self, tmp_path: Path) -> None:
        """TS-07-3: Assessment index is correct.

        Requirement: 07-REQ-1.3
        Verifies that assessment_index equals the index of the assessment
        whose questions were answered. After two refine rounds, the second
        QA exchange entry has assessment_index == 1.
        """
        # Start with 1 assessment (from assess). After first refine,
        # assessment_history will have 2 entries.
        session = _create_qa_exchange_session(tmp_path)

        # First refine: answers assessment 0's questions
        mock_agent_1 = _mock_agent_for_refine(
            quality="needs_refinement",
            summary="Still needs work",
            questions=[
                {
                    "id": "q2",
                    "text": "Next?",
                    "context": "C",
                    "options": [],
                    "required": False,
                }
            ],
        )
        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_1),
        ):
            await session.refine({"q1": "answer1"})

        assert session._qa_exchanges[0]["assessment_index"] == 0

        # Second refine: answers assessment 1's questions
        mock_agent_2 = _mock_agent_for_refine(
            quality="ready",
            summary="Ready now",
            gaps=[],
            questions=[],
        )
        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_2),
        ):
            await session.refine({"q2": "answer2"})

        assert len(session._qa_exchanges) == 2
        assert session._qa_exchanges[1]["assessment_index"] == 1

    @pytest.mark.asyncio
    async def test_ts07_4_qa_exchange_schema(self, tmp_path: Path) -> None:
        """TS-07-4: QA exchange entry has correct schema.

        Requirement: 07-REQ-2.1
        Verifies the QA exchange entry contains exactly the three required
        keys with correct types.
        """
        session = _create_qa_exchange_session(tmp_path)
        mock_agent = _mock_agent_for_refine()

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent),
        ):
            await session.refine({"q1": "a1"})

        entry = session._qa_exchanges[0]
        assert set(entry.keys()) == {"assessment_index", "answers", "timestamp"}
        assert isinstance(entry["assessment_index"], int)
        assert isinstance(entry["answers"], dict)
        assert isinstance(entry["timestamp"], str)
        assert len(entry["timestamp"]) > 0

    @pytest.mark.asyncio
    async def test_ts07_5_timestamp_patchable(self, tmp_path: Path) -> None:
        """TS-07-5: Timestamp is patchable.

        Requirement: 07-REQ-2.2
        Verifies that the timestamp comes from the patchable _utcnow()
        function.
        """
        session = _create_qa_exchange_session(tmp_path)
        mock_agent = _mock_agent_for_refine()

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent),
            patch(
                "agentspec.session._utcnow",
                return_value="2026-01-01T00:00:00+00:00",
            ),
        ):
            await session.refine({"q1": "a1"})

        assert session._qa_exchanges[0]["timestamp"] == "2026-01-01T00:00:00+00:00"


# ---------------------------------------------------------------------------
# No-side-effect tests: TS-07-6, TS-07-7
# ---------------------------------------------------------------------------


class TestQAExchangeNoSideEffects:
    """Tests verifying existing interfaces are unaffected — TS-07-6, TS-07-7.

    Note: TS-07-6 (question export via CLI) moved to
    packages/spec-cli/tests/test_qa_exchange_cli.py because it imports
    from both agentspec and spec_cli (per 10-REQ-4.E1).
    """

    def test_ts07_7_pending_questions_unaffected(self, tmp_path: Path) -> None:
        """TS-07-7: pending_questions unaffected.

        Requirement: 07-REQ-3.2
        Verifies pending_questions() returns the same result regardless
        of qa_exchanges content.
        """
        questions = [
            {
                "id": "q1",
                "text": "What?",
                "context": "C",
                "options": ["A"],
                "required": True,
            }
        ]
        assessment = _sample_assessment_dict(questions=questions)

        # Session with empty qa_exchanges
        session_empty = _create_qa_exchange_session(
            tmp_path,
            assessment_history=[assessment],
            qa_exchanges=[],
        )
        result_empty = session_empty.pending_questions()

        # Session with populated qa_exchanges
        session_populated = _create_qa_exchange_session(
            tmp_path,
            assessment_history=[assessment],
            qa_exchanges=[
                {
                    "assessment_index": 0,
                    "answers": {"q0": "ans"},
                    "timestamp": "2026-01-01T00:00:00+00:00",
                }
            ],
        )
        result_populated = session_populated.pending_questions()

        # Same questions returned regardless of qa_exchanges
        assert result_empty == result_populated
        assert len(result_empty) == 1
        assert result_empty[0]["id"] == "q1"


# ---------------------------------------------------------------------------
# Edge case tests: TS-07-E1, TS-07-E2
# ---------------------------------------------------------------------------


class TestQAExchangeEdgeCases:
    """Edge case tests for QA exchange recording — TS-07-E1, TS-07-E2."""

    @pytest.mark.asyncio
    async def test_ts07_e1_failed_refine_no_exchange(self, tmp_path: Path) -> None:
        """TS-07-E1: Failed refine does not record exchange.

        Requirement: 07-REQ-1.E1
        Verifies qa_exchanges is unchanged when refine fails due to
        an agent error.
        """
        session = _create_qa_exchange_session(tmp_path)

        mock_agent_instance = MagicMock()
        mock_agent_instance.refine_prd = AsyncMock(side_effect=AgentError("API failed"))

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
        ):
            with pytest.raises(AgentError):
                await session.refine({"q1": "a1"})

        assert len(session._qa_exchanges) == 0

    def test_ts07_e2_existing_empty_qa_exchanges_loads(self, tmp_path: Path) -> None:
        """TS-07-E2: Existing empty qa_exchanges loads fine.

        Requirement: 07-REQ-1.E2
        Verifies sessions with empty qa_exchanges load normally.
        """
        session = _create_qa_exchange_session(
            tmp_path,
            qa_exchanges=[],
        )
        assert session._qa_exchanges == []


# ---------------------------------------------------------------------------
# Property tests: TS-07-P1 through TS-07-P4
# ---------------------------------------------------------------------------


class TestQAExchangeProperties:
    """Property tests for QA exchange recording — TS-07-P1 through TS-07-P4."""

    @given(n=st.integers(min_value=1, max_value=5))
    @settings(
        max_examples=5,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts07_p1_exchange_count_matches_refine_count(self, n: int, tmp_path: Path) -> None:
        """TS-07-P1: Exchange count matches refine count.

        Property 1 from design.md.
        Validates: 07-REQ-1.1, 07-REQ-1.E1
        After N successful refine calls, qa_exchanges has N entries.
        """
        session = _create_qa_exchange_session(tmp_path)

        for i in range(n):
            mock_agent = _mock_agent_for_refine(
                quality="needs_refinement",
                summary=f"Round {i}",
                questions=[
                    {
                        "id": f"q{i + 1}",
                        "text": f"Question {i + 1}?",
                        "context": "C",
                        "options": [],
                        "required": False,
                    }
                ],
            )
            with (
                patch("agentspec.session._create_agent", return_value=mock_agent),
            ):
                _run_sync(session.refine({f"q{i}": f"answer{i}"}))

        assert len(session._qa_exchanges) == n

    @given(n=st.integers(min_value=1, max_value=5))
    @settings(
        max_examples=5,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts07_p2_assessment_index_sequential(self, n: int, tmp_path: Path) -> None:
        """TS-07-P2: Assessment index consistency.

        Property 2 from design.md.
        Validates: 07-REQ-1.3
        Each QA exchange's assessment_index is valid and sequential.
        """
        session = _create_qa_exchange_session(tmp_path)

        for i in range(n):
            mock_agent = _mock_agent_for_refine(
                quality="needs_refinement",
                summary=f"Round {i}",
                questions=[
                    {
                        "id": f"q{i + 1}",
                        "text": f"Q{i + 1}?",
                        "context": "C",
                        "options": [],
                        "required": False,
                    }
                ],
            )
            with (
                patch("agentspec.session._create_agent", return_value=mock_agent),
            ):
                _run_sync(session.refine({f"q{i}": f"a{i}"}))

        for i in range(n):
            assert session._qa_exchanges[i]["assessment_index"] == i

    @given(n=st.integers(min_value=1, max_value=5))
    @settings(
        max_examples=5,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts07_p3_exchange_schema_consistency(self, n: int, tmp_path: Path) -> None:
        """TS-07-P3: Exchange schema consistency.

        Property 3 from design.md.
        Validates: 07-REQ-2.1
        Every QA exchange entry has exactly the required keys with
        correct types.
        """
        session = _create_qa_exchange_session(tmp_path)

        for i in range(n):
            mock_agent = _mock_agent_for_refine(
                quality="needs_refinement",
                summary=f"Round {i}",
                questions=[
                    {
                        "id": f"q{i + 1}",
                        "text": f"Q{i + 1}?",
                        "context": "C",
                        "options": [],
                        "required": False,
                    }
                ],
            )
            with (
                patch("agentspec.session._create_agent", return_value=mock_agent),
            ):
                _run_sync(session.refine({f"q{i}": f"a{i}"}))

        for entry in session._qa_exchanges:
            assert set(entry.keys()) == {"assessment_index", "answers", "timestamp"}
            assert isinstance(entry["assessment_index"], int)
            assert isinstance(entry["answers"], dict)
            assert isinstance(entry["timestamp"], str)

    @given(n=st.integers(min_value=0, max_value=3))
    @settings(
        max_examples=4,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts07_p4_failed_refine_no_append(self, n: int, tmp_path: Path) -> None:
        """TS-07-P4: Failed refine no-append.

        Property 4 from design.md.
        Validates: 07-REQ-1.E1
        Agent errors never increase qa_exchanges length.
        """
        session = _create_qa_exchange_session(tmp_path)

        # Do n successful refines first
        for i in range(n):
            mock_agent = _mock_agent_for_refine(
                quality="needs_refinement",
                summary=f"Round {i}",
                questions=[
                    {
                        "id": f"q{i + 1}",
                        "text": f"Q{i + 1}?",
                        "context": "C",
                        "options": [],
                        "required": False,
                    }
                ],
            )
            with (
                patch("agentspec.session._create_agent", return_value=mock_agent),
            ):
                _run_sync(session.refine({f"q{i}": f"a{i}"}))

        len_before = len(session._qa_exchanges)

        # Now do a failing refine
        mock_agent_fail = MagicMock()
        mock_agent_fail.refine_prd = AsyncMock(side_effect=AgentError("API failed"))
        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_fail),
        ):
            with pytest.raises(AgentError):
                _run_sync(session.refine({f"q{n}": "a"}))

        assert len(session._qa_exchanges) == len_before


# ---------------------------------------------------------------------------
# Integration smoke test: TS-07-SMOKE-1
# ---------------------------------------------------------------------------


class TestQAExchangeSmoke:
    """Integration smoke test for QA exchange recording — TS-07-SMOKE-1."""

    @pytest.mark.asyncio
    async def test_ts07_smoke_1_full_refine_records_qa_exchange(self, tmp_path: Path) -> None:
        """TS-07-SMOKE-1: Full refine records exchange in persisted session.

        Execution Path: Path 1 from design.md.
        Verifies the full path from refine() through agent call to persisted
        QA exchange in _session.json.

        Must NOT satisfy with: Mocking SpecSession, _persist(), or
        _qa_exchanges.
        """
        # Create a real campaign and spec directory
        camp = Campaign.create(tmp_path / "smoke_qa", "Test", "Desc")
        session = camp.new_spec(
            "qa_smoke_test",
            "# My PRD\n\n## Intent\nBuild something.",
        )

        # Set state to assessing with one assessment containing questions
        session_file = session.spec_dir / "_session.json"
        data = json.loads(session_file.read_text())
        data["state"] = "assessing"
        data["assessment_history"] = [
            {
                "quality": "needs_refinement",
                "summary": "Needs work",
                "gaps": ["No goals"],
                "questions": [
                    {
                        "id": "q1",
                        "text": "What are the goals?",
                        "context": "Goals section is missing",
                        "options": [],
                        "required": True,
                    }
                ],
            }
        ]
        session_file.write_text(json.dumps(data, indent=2))

        # Resume the real session
        session = SpecSession.resume(session.spec_dir)

        # Mock only the agent API call
        new_assessment = Assessment(
            quality="ready",
            summary="PRD is ready",
            gaps=[],
            questions=[],
        )
        mock_agent_instance = MagicMock()
        mock_agent_instance.refine_prd = AsyncMock(
            return_value=(
                "# Updated PRD\n## Goals\n1. Build REST API",
                new_assessment,
            )
        )

        with (
            patch("agentspec.session._create_agent", return_value=mock_agent_instance),
            patch(
                "agentspec.session._utcnow",
                return_value="2026-06-10T12:00:00+00:00",
            ),
        ):
            await session.refine({"q1": "answer1"})

        # Read persisted data from disk — no mocking of _persist or _qa_exchanges
        data = json.loads((session.spec_dir / "_session.json").read_text())
        assert len(data["qa_exchanges"]) == 1
        assert data["qa_exchanges"][0]["assessment_index"] == 0
        assert data["qa_exchanges"][0]["answers"] == {"q1": "answer1"}
        assert data["qa_exchanges"][0]["timestamp"] == "2026-06-10T12:00:00+00:00"
        assert len(data["assessment_history"]) == 2
