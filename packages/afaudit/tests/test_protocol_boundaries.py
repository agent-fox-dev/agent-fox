"""Integration tests for Protocol boundary verification.

TS-01-25: ExecutionState satisfies PostmortemInput structurally
TS-01-26: SessionRecord satisfies SessionRecordLike structurally
TS-01-41: Protocol boundary tests run and pass via pytest

These tests import both afaudit protocols and agentfox state classes
to verify structural compatibility. They require agentfox to be installed.
"""

from __future__ import annotations

import typing

import pytest

# These imports require agentfox to be installed — mark as integration.
try:
    from agentfox.engine.state import ExecutionState, SessionRecord

    _HAS_AGENTFOX = True
except ImportError:
    _HAS_AGENTFOX = False

from afaudit.postmortem import PostmortemInput, SessionRecordLike

pytestmark = pytest.mark.integration

POSTMORTEM_INPUT_ATTRS = {
    "run_id",
    "run_status",
    "node_states",
    "total_cost",
    "total_input_tokens",
    "total_output_tokens",
    "total_sessions",
    "blocked_reasons",
    "session_history",
    "started_at",
    "updated_at",
}

SESSION_RECORD_LIKE_ATTRS = {
    "node_id",
    "attempt",
    "status",
    "archetype",
    "model",
    "duration_ms",
    "cost",
    "error_message",
    "timestamp",
    "is_transport_error",
    "is_budget_exhausted",
    "is_non_retryable",
}


@pytest.mark.skipif(not _HAS_AGENTFOX, reason="agentfox not installed")
class TestExecutionStateSatisfiesPostmortemInput:
    """TS-01-25: ExecutionState has all 11 PostmortemInput attributes.

    Requirement: 01-REQ-6.6
    """

    def test_all_protocol_attributes_present(self) -> None:
        """ExecutionState must have every attribute defined by PostmortemInput."""
        protocol_hints = typing.get_type_hints(PostmortemInput)
        for attr in protocol_hints:
            has_attr = hasattr(ExecutionState, attr) or attr in typing.get_type_hints(ExecutionState)
            assert has_attr, f"ExecutionState missing PostmortemInput attribute: {attr}"

    def test_all_11_attributes_covered(self) -> None:
        """All 11 expected PostmortemInput attributes must be present on ExecutionState."""
        es_hints = typing.get_type_hints(ExecutionState)
        es_attrs = set(es_hints.keys())
        missing = POSTMORTEM_INPUT_ATTRS - es_attrs
        assert not missing, f"ExecutionState missing PostmortemInput attributes: {missing}"

    def test_protocol_attribute_count_matches(self) -> None:
        """PostmortemInput defines exactly 11 attributes, all present on ExecutionState."""
        protocol_hints = typing.get_type_hints(PostmortemInput)
        assert len(protocol_hints) == 11
        es_hints = typing.get_type_hints(ExecutionState)
        for attr in protocol_hints:
            assert attr in es_hints, f"ExecutionState missing: {attr}"

    def test_runtime_checkable_if_decorated(self) -> None:
        """If PostmortemInput is @runtime_checkable, isinstance check should pass on an instance."""
        if not getattr(PostmortemInput, "__protocol_attrs__", None) and not hasattr(
            PostmortemInput, "__abstractmethods__"
        ):
            pytest.skip("PostmortemInput is not @runtime_checkable")
        # Construct a minimal ExecutionState to test isinstance
        instance = ExecutionState(
            plan_hash="abc",
            node_states={},
            run_id="test_run",
            run_status="completed",
            total_cost=0.0,
            total_input_tokens=0,
            total_output_tokens=0,
            total_sessions=0,
            blocked_reasons={},
            started_at="2024-01-01T00:00:00Z",
            updated_at="2024-01-01T00:01:00Z",
        )
        assert isinstance(instance, PostmortemInput)


@pytest.mark.skipif(not _HAS_AGENTFOX, reason="agentfox not installed")
class TestSessionRecordSatisfiesSessionRecordLike:
    """TS-01-26: SessionRecord has all 12 SessionRecordLike attributes.

    Requirement: 01-REQ-6.7
    """

    def test_all_protocol_attributes_present(self) -> None:
        """SessionRecord must have every attribute defined by SessionRecordLike."""
        protocol_hints = typing.get_type_hints(SessionRecordLike)
        for attr in protocol_hints:
            has_attr = hasattr(SessionRecord, attr) or attr in typing.get_type_hints(SessionRecord)
            assert has_attr, f"SessionRecord missing SessionRecordLike attribute: {attr}"

    def test_all_12_attributes_covered(self) -> None:
        """All 12 expected SessionRecordLike attributes must be present on SessionRecord."""
        sr_hints = typing.get_type_hints(SessionRecord)
        sr_attrs = set(sr_hints.keys())
        missing = SESSION_RECORD_LIKE_ATTRS - sr_attrs
        assert not missing, f"SessionRecord missing SessionRecordLike attributes: {missing}"

    def test_protocol_attribute_count_matches(self) -> None:
        """SessionRecordLike defines exactly 12 attributes, all present on SessionRecord."""
        protocol_hints = typing.get_type_hints(SessionRecordLike)
        assert len(protocol_hints) == 12
        sr_hints = typing.get_type_hints(SessionRecord)
        for attr in protocol_hints:
            assert attr in sr_hints, f"SessionRecord missing: {attr}"

    def test_runtime_checkable_if_decorated(self) -> None:
        """If SessionRecordLike is @runtime_checkable, isinstance check should pass."""
        if not getattr(SessionRecordLike, "__protocol_attrs__", None) and not hasattr(
            SessionRecordLike, "__abstractmethods__"
        ):
            pytest.skip("SessionRecordLike is not @runtime_checkable")
        instance = SessionRecord(
            node_id="spec01/1/coder",
            attempt=1,
            status="completed",
            input_tokens=100,
            output_tokens=50,
            cost=0.01,
            duration_ms=5000,
            error_message=None,
            timestamp="2024-01-01T00:00:00Z",
            model="claude-sonnet-4-20250514",
            archetype="coder",
        )
        assert isinstance(instance, SessionRecordLike)

    def test_no_modification_to_session_record_needed(self) -> None:
        """SessionRecord satisfies SessionRecordLike without any changes to its definition.

        We verify this by checking that all required attributes exist as
        declared fields on the original dataclass, not as monkey-patched
        additions.
        """
        import dataclasses

        field_names = {f.name for f in dataclasses.fields(SessionRecord)}
        for attr in SESSION_RECORD_LIKE_ATTRS:
            assert attr in field_names, (
                f"SessionRecordLike attribute '{attr}' is not a declared dataclass field "
                f"on SessionRecord — it may have been monkey-patched"
            )
