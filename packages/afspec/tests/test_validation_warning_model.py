"""Tests for ValidationWarning Pydantic model definition.

TS-08-20: Verify that a ValidationWarning Pydantic model is defined in
afspec/validation.py with at least fields for the warning message and
offending entity identifier.

These tests are in RED PHASE — they will fail with ImportError because
ValidationWarning has not been implemented yet.
"""

from __future__ import annotations

from pydantic import BaseModel


class TestValidationWarningModel:
    """TS-08-20: ValidationWarning model definition and field access."""

    def test_is_pydantic_base_model(self) -> None:
        """ValidationWarning is a Pydantic BaseModel subclass."""
        from afspec.validation import ValidationWarning

        assert issubclass(ValidationWarning, BaseModel)

    def test_has_message_field(self) -> None:
        """ValidationWarning has a 'message' field."""
        from afspec.validation import ValidationWarning

        assert "message" in ValidationWarning.model_fields

    def test_has_entity_identifier_field(self) -> None:
        """ValidationWarning has an entity identifier field (e.g. entity_id)."""
        from afspec.validation import ValidationWarning

        entity_fields = [f for f in ValidationWarning.model_fields if "id" in f or "entity" in f or "location" in f]
        assert len(entity_fields) >= 1, (
            f"Expected at least one entity identifier field, got fields: {list(ValidationWarning.model_fields.keys())}"
        )

    def test_instantiation_and_field_access(self) -> None:
        """Can instantiate ValidationWarning and access message + entity_id."""
        from afspec.validation import ValidationWarning

        w = ValidationWarning(message="test warning", entity_id="group-1")
        assert w.message == "test warning"
        assert w.entity_id == "group-1"

    def test_string_representation_contains_message(self) -> None:
        """String representation of ValidationWarning includes the message."""
        from afspec.validation import ValidationWarning

        w = ValidationWarning(message="oversized group", entity_id="1")
        assert "oversized group" in str(w)
