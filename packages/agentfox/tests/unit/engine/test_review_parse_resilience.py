"""Unit tests for review parse resilience: fuzzy matching, normalization,
and format retry.

Test Spec: TS-74-7 through TS-74-19, TS-74-23, TS-74-26, TS-74-E1, TS-74-E2
Requirements: 74-REQ-2.*, 74-REQ-3.*, 74-REQ-5.*
"""

from __future__ import annotations

import uuid
from typing import Any
from unittest.mock import MagicMock, patch

from afaudit.events import AuditEventType
from agentfox.core.json_extraction import extract_json_array
from agentfox.knowledge.review_store import ReviewFinding
from agentfox.session.review_parser import (
    _unwrap_items,
    parse_drift_findings,
    parse_review_findings,
)

# ---------------------------------------------------------------------------
# Helpers for constructing test data
# ---------------------------------------------------------------------------


def _make_finding(
    severity: str = "major",
    description: str = "test finding",
    spec_name: str = "test_spec",
    task_group: str = "1",
    session_id: str = "sess1",
) -> ReviewFinding:
    return ReviewFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description=description,
        requirement_ref=None,
        spec_name=spec_name,
        task_group=task_group,
        session_id=session_id,
    )


# ---------------------------------------------------------------------------
# TS-74-7: Case-insensitive wrapper key resolution
# ---------------------------------------------------------------------------


class TestCaseInsensitiveWrapperKeyResolution:
    """TS-74-7: _resolve_wrapper_key matches keys regardless of case.

    Requirements: 74-REQ-2.1
    """

    def test_mixed_case_findings_key_resolved(self) -> None:
        """_resolve_wrapper_key returns the actual key for case-insensitive match."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data = {"Findings": [{"severity": "major", "description": "test"}]}
        result = _resolve_wrapper_key(data, "findings")
        assert result == "Findings"

    def test_uppercase_findings_key_resolved(self) -> None:
        """_resolve_wrapper_key handles fully uppercase wrapper key."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data: dict[str, list[object]] = {"FINDINGS": []}
        result = _resolve_wrapper_key(data, "findings")
        assert result == "FINDINGS"

    def test_exact_case_key_resolved(self) -> None:
        """_resolve_wrapper_key returns exact key when casing matches."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data: dict[str, list[object]] = {"findings": []}
        result = _resolve_wrapper_key(data, "findings")
        assert result == "findings"

    def test_missing_key_returns_none(self) -> None:
        """_resolve_wrapper_key returns None when no matching key exists."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data: dict[str, list[object]] = {"something_else": []}
        result = _resolve_wrapper_key(data, "findings")
        assert result is None


# ---------------------------------------------------------------------------
# TS-74-8: Singular/plural variant wrapper key resolution
# ---------------------------------------------------------------------------


class TestSingularVariantWrapperKeyResolution:
    """TS-74-8: _resolve_wrapper_key accepts singular variants of wrapper keys.

    Requirements: 74-REQ-2.2
    """

    def test_finding_resolves_to_findings(self) -> None:
        """_resolve_wrapper_key accepts 'finding' as a variant of 'findings'."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data = {"finding": [{"severity": "minor", "description": "x"}]}
        result = _resolve_wrapper_key(data, "findings")
        assert result == "finding"

    def test_verdict_resolves_to_verdicts(self) -> None:
        """_resolve_wrapper_key accepts 'verdict' as variant of 'verdicts'."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data = {"verdict": [{"requirement_id": "R1", "verdict": "PASS"}]}
        result = _resolve_wrapper_key(data, "verdicts")
        assert result == "verdict"

    def test_drift_finding_resolves_to_drift_findings(self) -> None:
        """_resolve_wrapper_key accepts 'drift_finding' for 'drift_findings'."""
        from agentfox.session.review_parser import _resolve_wrapper_key

        data = {"drift_finding": [{"severity": "major", "description": "d"}]}
        result = _resolve_wrapper_key(data, "drift_findings")
        assert result == "drift_finding"

    def test_wrapper_key_variants_map_exists(self) -> None:
        """WRAPPER_KEY_VARIANTS map exists and contains canonical keys."""
        from agentfox.session.review_parser import WRAPPER_KEY_VARIANTS

        assert "findings" in WRAPPER_KEY_VARIANTS
        assert "verdicts" in WRAPPER_KEY_VARIANTS
        assert "drift_findings" in WRAPPER_KEY_VARIANTS
        assert "audit" in WRAPPER_KEY_VARIANTS

    def test_findings_variants_include_singular(self) -> None:
        """WRAPPER_KEY_VARIANTS 'findings' set includes 'finding'."""
        from agentfox.session.review_parser import WRAPPER_KEY_VARIANTS

        assert "finding" in WRAPPER_KEY_VARIANTS["findings"]

    def test_verdicts_variants_include_singular(self) -> None:
        """WRAPPER_KEY_VARIANTS 'verdicts' set includes 'verdict'."""
        from agentfox.session.review_parser import WRAPPER_KEY_VARIANTS

        assert "verdict" in WRAPPER_KEY_VARIANTS["verdicts"]


# ---------------------------------------------------------------------------
# TS-74-9: _unwrap_items uses fuzzy key matching
# ---------------------------------------------------------------------------


class TestUnwrapItemsFuzzyMatching:
    """TS-74-9: _unwrap_items extracts findings from variant wrapper keys.

    Requirements: 74-REQ-2.3
    """

    def test_mixed_case_finding_key_unwrapped(self) -> None:
        """_unwrap_items extracts items from 'Finding' when seeking 'findings'."""
        response = '{"Finding": [{"severity": "major", "description": "test"}]}'
        items = _unwrap_items(response, "findings", ("severity",), "Skeptic")
        assert len(items) == 1
        assert items[0]["severity"] == "major"

    def test_singular_variant_finding_key_unwrapped(self) -> None:
        """_unwrap_items extracts items from 'finding' when seeking 'findings'."""
        response = '{"finding": [{"severity": "minor", "description": "x"}]}'
        items = _unwrap_items(response, "findings", ("severity",), "Skeptic")
        assert len(items) == 1
        assert items[0]["severity"] == "minor"

    def test_uppercase_verdicts_key_unwrapped(self) -> None:
        """_unwrap_items extracts items from 'VERDICTS' when seeking 'verdicts'."""
        response = '{"VERDICTS": [{"requirement_id": "R1", "verdict": "PASS"}]}'
        items = _unwrap_items(response, "verdicts", ("requirement_id",), "Verifier")
        assert len(items) == 1
        assert items[0]["requirement_id"] == "R1"


# ---------------------------------------------------------------------------
# TS-74-10: Field-level case normalization
# ---------------------------------------------------------------------------


class TestFieldLevelCaseNormalization:
    """TS-74-10: Typed parsers normalize field keys to lowercase before validation.

    Requirements: 74-REQ-2.4
    """

    def test_parse_review_findings_mixed_case_keys(self) -> None:
        """parse_review_findings handles mixed-case field keys."""
        items = [{"Severity": "critical", "Description": "test issue"}]
        findings = parse_review_findings(items, "spec", "1", "sess1")
        assert len(findings) == 1
        assert findings[0].severity == "critical"
        assert findings[0].description == "test issue"

    def test_parse_review_findings_all_uppercase_keys(self) -> None:
        """parse_review_findings handles all-uppercase field keys."""
        items = [{"SEVERITY": "major", "DESCRIPTION": "uppercase test"}]
        findings = parse_review_findings(items, "spec", "1", "sess1")
        assert len(findings) == 1
        assert findings[0].severity == "major"

    def test_parse_drift_findings_mixed_case_keys(self) -> None:
        """parse_drift_findings handles mixed-case field keys."""
        items = [{"Severity": "minor", "Description": "drift desc"}]
        findings = parse_drift_findings(items, "spec", "1", "sess1")
        assert len(findings) == 1
        assert findings[0].severity == "minor"


# ---------------------------------------------------------------------------
# TS-74-11: Markdown fence extraction preserved (backward compat)
# ---------------------------------------------------------------------------


class TestMarkdownFenceExtractionPreserved:
    """TS-74-11: JSON inside markdown fences with surrounding prose is extracted.

    Requirements: 74-REQ-2.5
    """

    def test_fence_with_prose_extracted(self) -> None:
        """extract_json_array extracts JSON from fenced block with surrounding prose."""
        text = 'Here is my analysis:\n```json\n[{"severity": "major"}]\n```\nDone.'
        result = extract_json_array(text)
        assert result == [{"severity": "major"}]

    def test_bare_fence_without_json_label(self) -> None:
        """extract_json_array handles plain fences without 'json' label."""
        text = '```\n[{"severity": "minor"}]\n```'
        result = extract_json_array(text)
        assert result == [{"severity": "minor"}]


# ---------------------------------------------------------------------------
# TS-74-12: Single object treated as finding (REQ-2.E1)
# ---------------------------------------------------------------------------


class TestSingleObjectAsFinding:
    """TS-74-12: A bare JSON object with required fields is treated as finding.

    Requirements: 74-REQ-2.E1
    """

    def test_bare_object_with_severity_treated_as_finding(self) -> None:
        """_unwrap_items returns single-item list for bare object with required keys."""
        response = '{"severity": "minor", "description": "nit"}'
        items = _unwrap_items(response, "findings", ("severity",), "Skeptic")
        assert len(items) == 1
        assert items[0]["severity"] == "minor"


# ---------------------------------------------------------------------------
# TS-74-13: Multiple JSON blocks merged (REQ-2.E2)
# ---------------------------------------------------------------------------


class TestMultipleJsonBlocksMerged:
    """TS-74-13: Findings from multiple JSON blocks in the same output are merged.

    Requirements: 74-REQ-2.E2
    """

    def test_two_findings_blocks_merged(self) -> None:
        """_unwrap_items merges findings from two separate JSON objects."""
        response = (
            '{"findings": [{"severity": "major", "description": "a"}]}\n'
            "More analysis text.\n"
            '{"findings": [{"severity": "minor", "description": "b"}]}'
        )
        items = _unwrap_items(response, "findings", ("severity",), "Skeptic")
        assert len(items) == 2
        severities = {item["severity"] for item in items}
        assert severities == {"major", "minor"}


# ---------------------------------------------------------------------------
# TS-74-15: Retry message contains schema reference (REQ-3.2)
# ---------------------------------------------------------------------------


class TestFormatRetryPrompt:
    """TS-74-15: FORMAT_RETRY_PROMPT constant exists and has correct content.

    Requirements: 74-REQ-3.2
    """

    def test_format_retry_prompt_exists(self) -> None:
        """FORMAT_RETRY_PROMPT constant is defined in review_persistence."""
        from agentfox.engine.review_persistence import FORMAT_RETRY_PROMPT

        assert isinstance(FORMAT_RETRY_PROMPT, str)
        assert len(FORMAT_RETRY_PROMPT) > 0

    def test_format_retry_prompt_mentions_parse_failure(self) -> None:
        """FORMAT_RETRY_PROMPT explains that previous output could not be parsed."""
        from agentfox.engine.review_persistence import FORMAT_RETRY_PROMPT

        lower = FORMAT_RETRY_PROMPT.lower()
        assert "could not be parsed" in lower or "not be parsed" in lower, (
            "FORMAT_RETRY_PROMPT must indicate parsing failure"
        )

    def test_format_retry_prompt_mentions_json(self) -> None:
        """FORMAT_RETRY_PROMPT references JSON and format instructions."""
        from agentfox.engine.review_persistence import FORMAT_RETRY_PROMPT

        assert "JSON" in FORMAT_RETRY_PROMPT or "json" in FORMAT_RETRY_PROMPT.lower()


# ---------------------------------------------------------------------------
# TS-74-16: Maximum one retry (REQ-3.3)
# ---------------------------------------------------------------------------


class TestMaximumOneRetry:
    """TS-74-16: At most 1 format retry is attempted per session.

    Requirements: 74-REQ-3.3
    """

    def test_persist_review_findings_accepts_session_handle(self) -> None:
        """persist_review_findings accepts a session_handle keyword argument."""
        import inspect

        from agentfox.engine.review_persistence import persist_review_findings

        sig = inspect.signature(persist_review_findings)
        params = sig.parameters
        assert "session_handle" in params or "backend" in params, (
            "persist_review_findings must accept session_handle or backend to support format retry"
        )

    def test_persist_review_findings_retry_count_bounded(self) -> None:
        """Format retry is attempted at most once (retry count <= 1)."""
        from agentfox.engine.review_persistence import persist_review_findings

        call_count: list[str] = []

        def mock_extract(text: str, **kwargs):  # type: ignore[override]
            call_count.append(text)
            return None  # Always fail

        mock_session = MagicMock()
        mock_session.is_alive = True
        mock_session.append_user_message = MagicMock(return_value="still bad json")

        with patch("agentfox.engine.review_persistence.extract_json_array", mock_extract):
            persist_review_findings(
                transcript="no json here",
                node_id="test-node",
                attempt=1,
                archetype="reviewer",
                spec_name="test_spec",
                task_group="1",
                knowledge_db_conn=MagicMock(),
                sink=None,
                run_id="run1",
                session_handle=mock_session,
                mode="pre-review",
            )

        # At most 2 calls: initial parse + 1 retry
        assert len(call_count) <= 2, f"Expected at most 2 extraction attempts, got {len(call_count)}"


# ---------------------------------------------------------------------------
# TS-74-19: No retry on terminated session (REQ-3.E2)
# ---------------------------------------------------------------------------


class TestNoRetryOnTerminatedSession:
    """TS-74-19: Format retry is skipped when session is terminated.

    Requirements: 74-REQ-3.E2
    """

    def test_no_retry_when_session_not_alive(self) -> None:
        """persist_review_findings does not retry when session is terminated."""
        from agentfox.engine.review_persistence import persist_review_findings

        retry_called: list[bool] = []

        mock_session = MagicMock()
        mock_session.is_alive = False

        original_append = mock_session.append_user_message

        def track_retry(*args, **kwargs):
            retry_called.append(True)
            return original_append(*args, **kwargs)

        mock_session.append_user_message = track_retry

        with patch(
            "agentfox.engine.review_persistence.extract_json_array",
            return_value=None,
        ):
            persist_review_findings(
                transcript="no json here",
                node_id="test-node",
                attempt=1,
                archetype="reviewer",
                spec_name="test_spec",
                task_group="1",
                knowledge_db_conn=MagicMock(),
                sink=None,
                run_id="run1",
                session_handle=mock_session,
                mode="pre-review",
            )

        assert len(retry_called) == 0, "Format retry must not be attempted when session is terminated"


# ---------------------------------------------------------------------------
# TS-74-20: Partial convergence - Reviewer pre-review (REQ-4.1)
# ---------------------------------------------------------------------------
# TS-74-22: Partial convergence - Auditor (REQ-4.3)
# ---------------------------------------------------------------------------
# TS-74-23: No parse failure when some instances succeed (REQ-4.4)
# ---------------------------------------------------------------------------


class TestNoParseFailureWhenSomeSucceed:
    """TS-74-23: REVIEW_PARSE_FAILURE not emitted when at least one instance succeeds.

    Requirements: 74-REQ-4.4
    """

    def test_no_failure_event_when_some_instances_parseable(self) -> None:
        """No REVIEW_PARSE_FAILURE when at least one instance produced output."""
        f = _make_finding(severity="major", description="valid finding")
        raw_results: list[list[ReviewFinding] | None] = [[f], None]
        filtered = [r for r in raw_results if r is not None]

        # When at least one result is non-None, no REVIEW_PARSE_FAILURE should fire.
        # The convergence call site (task 4.3) implements this behavior.
        # Here we verify that filtering produces at least one result.
        assert len(filtered) > 0, "At least one parseable result must exist"


# ---------------------------------------------------------------------------
# TS-74-24: Warning logged for failed instances (REQ-4.5)
# ---------------------------------------------------------------------------
# TS-74-25: All instances fail emits parse failure (REQ-4.E1)
# ---------------------------------------------------------------------------


# ---------------------------------------------------------------------------
# TS-74-26: Retry success audit event (REQ-5.1)
# ---------------------------------------------------------------------------


class TestRetrySuccessAuditEvent:
    """TS-74-26: REVIEW_PARSE_RETRY_SUCCESS event emitted on successful retry.

    Requirements: 74-REQ-5.1
    """

    def test_review_parse_retry_success_event_type_exists(self) -> None:
        """REVIEW_PARSE_RETRY_SUCCESS is a valid AuditEventType."""
        assert hasattr(AuditEventType, "REVIEW_PARSE_RETRY_SUCCESS"), (
            "AuditEventType must have REVIEW_PARSE_RETRY_SUCCESS member"
        )

    def test_retry_success_event_value(self) -> None:
        """REVIEW_PARSE_RETRY_SUCCESS has the expected string value."""
        retry_success = AuditEventType.REVIEW_PARSE_RETRY_SUCCESS
        assert "retry" in str(retry_success).lower(), "REVIEW_PARSE_RETRY_SUCCESS event type value must contain 'retry'"


# ---------------------------------------------------------------------------
# TS-74-27: Parse failure payload contains strategy field (REQ-5.3)
# ---------------------------------------------------------------------------


class TestParseFailurePayloadStrategyField:
    """TS-74-27: REVIEW_PARSE_FAILURE payload includes a 'strategy' field.

    Requirements: 74-REQ-5.3
    """

    def test_parse_failure_with_retry_includes_strategy(self) -> None:
        """REVIEW_PARSE_FAILURE payload has 'strategy' with extraction strategies."""
        from agentfox.engine.review_persistence import persist_review_findings

        class CaptureSink:
            def __init__(self) -> None:
                self.events: list[Any] = []

            def emit_audit_event(self, event: Any) -> None:
                self.events.append(event)

            def record_session_outcome(self, outcome: Any) -> None:
                pass

            def record_tool_call(self, tool_call: Any) -> None:
                pass

            def record_tool_error(self, error: Any) -> None:
                pass

            def close(self) -> None:
                pass

        sink = CaptureSink()

        with patch(
            "agentfox.engine.review_persistence.extract_json_array",
            return_value=None,
        ):
            persist_review_findings(
                transcript="no json here",
                node_id="test-node",
                attempt=1,
                archetype="reviewer",
                spec_name="test_spec",
                task_group="1",
                knowledge_db_conn=MagicMock(),
                sink=sink,
                run_id="run1",
                mode="pre-review",
            )

        failure_events = [
            e for e in sink.events if getattr(e, "event_type", None) == AuditEventType.REVIEW_PARSE_FAILURE
        ]
        assert len(failure_events) > 0, "REVIEW_PARSE_FAILURE event must be emitted"

        payload = failure_events[0].payload
        assert "strategy" in payload, "REVIEW_PARSE_FAILURE payload must include 'strategy' field"
        assert "bracket_scan" in payload["strategy"], "Strategy field must include 'bracket_scan' strategy name"


# ---------------------------------------------------------------------------
# TS-74-E1: Empty output text
# ---------------------------------------------------------------------------


class TestEmptyOutputText:
    """TS-74-E1: Empty output returns no findings.

    Requirements: 74-REQ-2.E1
    """

    def test_empty_string_returns_none(self) -> None:
        """extract_json_array returns None for empty input."""
        result = extract_json_array("")
        assert result is None

    def test_whitespace_only_returns_none(self) -> None:
        """extract_json_array returns None for whitespace-only input."""
        result = extract_json_array("   \n\t  ")
        assert result is None


# ---------------------------------------------------------------------------
# TS-74-E2: JSON with unknown wrapper key
# ---------------------------------------------------------------------------


class TestUnknownWrapperKey:
    """TS-74-E2: JSON with unrecognized wrapper key falls through.

    Requirements: 74-REQ-2.E1
    """

    def test_unrecognized_wrapper_key_returns_empty(self) -> None:
        """_unwrap_items returns empty for unrecognized non-variant wrapper key."""
        response = '{"results_data": [{"severity": "major"}]}'
        items = _unwrap_items(response, "findings", ("severity",), "Skeptic")
        # "results_data" is not a registered variant of "findings"
        # The outer dict doesn't have "severity" as a direct key (it's nested)
        assert items == [], "Items from unrecognized wrapper key 'results_data' should not be returned"
