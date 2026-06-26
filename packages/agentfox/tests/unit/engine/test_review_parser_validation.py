"""Tests for field-level validation in review parser.

Regression tests for GitHub issue #186: review parser must enforce
string length limits on LLM-provided fields.
"""

from __future__ import annotations

from agentfox.session.review_parser import (
    MAX_CONTENT_LENGTH,
    MAX_REF_LENGTH,
    parse_drift_findings,
    parse_review_findings,
)


class TestReviewFindingFieldValidation:
    """parse_review_findings enforces field-level constraints."""

    def test_normal_finding_parses(self) -> None:
        objs = [{"severity": "major", "description": "Test finding"}]
        results = parse_review_findings(objs, "spec-1", "1", "session-1")
        assert len(results) == 1
        assert results[0].description == "Test finding"

    def test_oversized_description_truncated(self) -> None:
        objs = [
            {
                "severity": "major",
                "description": "x" * (MAX_CONTENT_LENGTH + 500),
            }
        ]
        results = parse_review_findings(objs, "spec-1", "1", "session-1")
        assert len(results) == 1
        assert len(results[0].description) == MAX_CONTENT_LENGTH

    def test_oversized_requirement_ref_truncated(self) -> None:
        objs = [
            {
                "severity": "minor",
                "description": "test",
                "requirement_ref": "r" * (MAX_REF_LENGTH + 100),
            }
        ]
        results = parse_review_findings(objs, "spec-1", "1", "session-1")
        assert results[0].requirement_ref is not None
        assert len(results[0].requirement_ref) == MAX_REF_LENGTH


class TestDriftFindingFieldValidation:
    """parse_drift_findings enforces field-level constraints."""

    def test_normal_drift_parses(self) -> None:
        objs = [{"severity": "minor", "description": "test drift"}]
        results = parse_drift_findings(objs, "spec-1", "1", "session-1")
        assert len(results) == 1

    def test_oversized_description_truncated(self) -> None:
        objs = [
            {
                "severity": "critical",
                "description": "d" * (MAX_CONTENT_LENGTH + 500),
            }
        ]
        results = parse_drift_findings(objs, "spec-1", "1", "session-1")
        assert len(results[0].description) == MAX_CONTENT_LENGTH

    def test_oversized_spec_ref_truncated(self) -> None:
        objs = [
            {
                "severity": "minor",
                "description": "test",
                "spec_ref": "s" * (MAX_REF_LENGTH + 100),
            }
        ]
        results = parse_drift_findings(objs, "spec-1", "1", "session-1")
        assert results[0].spec_ref is not None
        assert len(results[0].spec_ref) == MAX_REF_LENGTH

    def test_oversized_artifact_ref_truncated(self) -> None:
        objs = [
            {
                "severity": "minor",
                "description": "test",
                "artifact_ref": "a" * (MAX_REF_LENGTH + 100),
            }
        ]
        results = parse_drift_findings(objs, "spec-1", "1", "session-1")
        assert results[0].artifact_ref is not None
        assert len(results[0].artifact_ref) == MAX_REF_LENGTH
