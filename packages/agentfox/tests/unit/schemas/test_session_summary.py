"""Unit tests for agentfox.schemas.session_summary module.

Tests the ``SessionSummary`` Pydantic model that defines the canonical
schema for ``.agent-fox/session-summary.json``.  Validates that the
model matches the inline JSON example in the coder profile template,
round-trips correctly, and provides diagnostic validation errors.

Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-4, TS-NS-5
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5
"""

from __future__ import annotations

import logging

import pytest
from agentfox.schemas.session_summary import (
    RejectedApproach,
    SessionSummary,
    TestEntry,
)
from pydantic import ValidationError

# ---------------------------------------------------------------------------
# TS-NS-1: SessionSummary model exists with all 5 fields
# ---------------------------------------------------------------------------


class TestSessionSummarySchema:
    """Verify the SessionSummary model has the correct shape.

    Test Spec: TS-NS-1
    Requirements: NS-REQ-1
    """

    def test_importable(self) -> None:
        """SessionSummary is importable from agentfox.schemas.session_summary."""
        from agentfox.schemas.session_summary import SessionSummary as SS

        assert SS is SessionSummary

    def test_has_all_five_fields(self) -> None:
        """Model exposes exactly the 5 expected fields."""
        expected = {
            "summary",
            "rejected_approaches",
            "gotchas",
            "assumptions",
            "tests_added_or_modified",
        }
        assert set(SessionSummary.model_fields.keys()) == expected

    def test_summary_is_required(self) -> None:
        """``summary`` field is required -- omitting it raises ValidationError."""
        with pytest.raises(ValidationError):
            SessionSummary()  # type: ignore[call-arg]

    def test_list_fields_default_to_empty(self) -> None:
        """All list fields default to empty lists when not provided."""
        model = SessionSummary(summary="x")
        assert model.rejected_approaches == []
        assert model.gotchas == []
        assert model.assumptions == []
        assert model.tests_added_or_modified == []

    def test_rejected_approach_submodel(self) -> None:
        """RejectedApproach has ``approach`` and ``reason`` fields."""
        ra = RejectedApproach(approach="A", reason="B")
        assert ra.approach == "A"
        assert ra.reason == "B"

    def test_test_entry_submodel(self) -> None:
        """TestEntry has ``path`` and ``description`` fields."""
        te = TestEntry(path="tests/test_foo.py", description="validates foo")
        assert te.path == "tests/test_foo.py"
        assert te.description == "validates foo"


# ---------------------------------------------------------------------------
# TS-NS-5: Round-trip from the coder.md JSON example
# ---------------------------------------------------------------------------


class TestSessionSummaryCoderTemplateRoundTrip:
    """Verify SessionSummary round-trips from the coder.md JSON example.

    Parses the exact JSON example from the coder profile template through
    the model and asserts all 5 fields are populated correctly,
    demonstrating prompt-code parity.

    Test Spec: TS-NS-5
    Requirements: NS-REQ-5
    """

    # The canonical JSON example from coder.md (copy-pasted verbatim).
    CODER_TEMPLATE_EXAMPLE = {
        "summary": (
            "What was surprising or non-obvious about the implementation. "
            "Include task group number and spec name, but focus on learnings "
            "rather than completion status. Target ~500-1000 characters of "
            "genuinely useful context."
        ),
        "rejected_approaches": [
            {
                "approach": "Used library Y for parsing",
                "reason": "Too slow for large datasets — 10x slower than hand-rolled parser",
            },
        ],
        "gotchas": [
            "DuckDB closes connection on fork — must re-open after subprocess calls",
            "Empty arrays serialize as null in some JSON paths",
        ],
        "assumptions": [
            "Spec 10 will not remove the session_summaries table",
            "DuckDB version >= 0.9 is available in CI",
        ],
        "tests_added_or_modified": [
            {
                "path": "tests/unit/test_example.py",
                "description": "validates input parsing edge cases",
            },
        ],
    }

    def test_parses_coder_example(self) -> None:
        """The exact coder.md JSON example validates successfully."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        assert model.summary == self.CODER_TEMPLATE_EXAMPLE["summary"]

    def test_rejected_approaches_populated(self) -> None:
        """rejected_approaches are parsed as RejectedApproach models."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        assert len(model.rejected_approaches) == 1
        assert model.rejected_approaches[0].approach == "Used library Y for parsing"

    def test_gotchas_populated(self) -> None:
        """gotchas list is populated correctly."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        assert len(model.gotchas) == 2

    def test_assumptions_populated(self) -> None:
        """assumptions list is populated correctly."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        assert len(model.assumptions) == 2

    def test_tests_added_or_modified_populated(self) -> None:
        """tests_added_or_modified is parsed as TestEntry models."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        assert len(model.tests_added_or_modified) == 1
        assert model.tests_added_or_modified[0].path == "tests/unit/test_example.py"
        assert model.tests_added_or_modified[0].description == "validates input parsing edge cases"

    def test_json_round_trip(self) -> None:
        """Model serializes back to JSON that re-parses identically."""
        model = SessionSummary.model_validate(self.CODER_TEMPLATE_EXAMPLE)
        json_str = model.model_dump_json()
        reparsed = SessionSummary.model_validate_json(json_str)
        assert reparsed == model


# ---------------------------------------------------------------------------
# TS-NS-4: Validation failures emit diagnostic warnings
# ---------------------------------------------------------------------------


class TestSessionSummaryValidationDiagnostics:
    """Verify validation failures provide diagnostic information.

    Test Spec: TS-NS-4
    Requirements: NS-REQ-4
    """

    def test_wrong_type_for_summary_raises(self) -> None:
        """Integer summary raises ValidationError naming the field."""
        with pytest.raises(ValidationError) as exc_info:
            SessionSummary.model_validate({"summary": 123})
        errors = exc_info.value.errors()
        field_names = [e["loc"][0] for e in errors]
        assert "summary" in field_names

    def test_wrong_type_for_gotchas_raises(self) -> None:
        """Integer gotchas raises ValidationError naming the field."""
        with pytest.raises(ValidationError) as exc_info:
            SessionSummary.model_validate({"summary": "ok", "gotchas": 42})
        errors = exc_info.value.errors()
        field_names = [e["loc"][0] for e in errors]
        assert "gotchas" in field_names

    def test_string_rejected_approaches_accepted(self) -> None:
        """String items in rejected_approaches are accepted for backward compat."""
        model = SessionSummary.model_validate(
            {
                "summary": "ok",
                "rejected_approaches": ["just a string"],
            }
        )
        assert model.rejected_approaches == ["just a string"]

    def test_mixed_rejected_approaches_accepted(self) -> None:
        """A mix of dicts and strings in rejected_approaches is accepted."""
        model = SessionSummary.model_validate(
            {
                "summary": "ok",
                "rejected_approaches": [
                    {"approach": "A", "reason": "B"},
                    "bare string",
                ],
            }
        )
        assert len(model.rejected_approaches) == 2
        assert isinstance(model.rejected_approaches[0], RejectedApproach)
        assert model.rejected_approaches[1] == "bare string"

    def test_extraction_logs_warning_on_validation_failure(self, caplog: pytest.LogCaptureFixture) -> None:
        """extract_session_summary logs WARNING when validation fails."""
        from agentfox.knowledge.extraction import extract_session_summary

        with caplog.at_level(logging.WARNING, logger="agentfox.knowledge.extraction"):
            result = extract_session_summary('{"summary": "ok", "gotchas": 42}')

        assert result == (None, [], [], [])
        assert any("gotchas" in record.message for record in caplog.records)

    def test_extraction_logs_warning_for_wrong_summary_type(self, caplog: pytest.LogCaptureFixture) -> None:
        """extract_session_summary logs WARNING when summary is wrong type."""
        from agentfox.knowledge.extraction import extract_session_summary

        with caplog.at_level(logging.WARNING, logger="agentfox.knowledge.extraction"):
            result = extract_session_summary('{"summary": 123}')

        assert result == (None, [], [], [])
        assert any("summary" in record.message for record in caplog.records)


# ---------------------------------------------------------------------------
# TS-NS-3: tests_added_or_modified is accessible
# ---------------------------------------------------------------------------


class TestTestsAddedOrModifiedAccessible:
    """Verify tests_added_or_modified is no longer silently dropped.

    Test Spec: TS-NS-3
    Requirements: NS-REQ-3
    """

    def test_tests_added_or_modified_on_model(self) -> None:
        """tests_added_or_modified is accessible as a typed attribute."""
        model = SessionSummary(
            summary="done",
            tests_added_or_modified=[
                TestEntry(path="tests/test_a.py", description="added"),
            ],
        )
        assert len(model.tests_added_or_modified) == 1
        assert model.tests_added_or_modified[0].path == "tests/test_a.py"

    def test_session_lifecycle_source_references_tests_added(self) -> None:
        """session_lifecycle.py source contains a reference to tests_added_or_modified."""
        from pathlib import Path

        # Read the source file directly to avoid importing session_lifecycle
        # (which pulls in afaudit and other heavy dependencies).
        lifecycle_path = Path(__file__).resolve().parents[3] / "agentfox" / "engine" / "session_lifecycle.py"
        source = lifecycle_path.read_text(encoding="utf-8")
        assert "tests_added_or_modified" in source
