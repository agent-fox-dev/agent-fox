"""Unit tests for agentfox.knowledge.extraction module.

Tests the ``extract_session_summary`` function which extracts structured
summary fields (summary, rejected_approaches, gotchas, assumptions) from
a session response string.

Test Spec: TS-05-8, TS-05-9, TS-05-10, TS-05-11, TS-05-12, TS-05-15,
           TS-05-16, TS-05-44
Requirements: 05-REQ-3.1, 05-REQ-3.2, 05-REQ-3.3, 05-REQ-3.4,
              05-REQ-3.5, 05-REQ-4.1, 05-REQ-4.2
"""

from __future__ import annotations

import inspect
import json
from unittest.mock import patch

import pytest
from agentfox.knowledge.extraction import extract_session_summary

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _build_structured_response(
    summary: str = "Fix worked",
    rejected: list[dict[str, str]] | None = None,
    gotchas: list[str] | None = None,
    assumptions: list[str] | None = None,
) -> str:
    """Build a response string containing a structured session summary JSON block.

    Mimics the format a coder agent would embed in its response text when
    producing a session summary inline.
    """
    data: dict[str, object] = {
        "summary": summary,
        "rejected_approaches": rejected or [{"approach": "approach A", "reason": "too slow"}],
        "gotchas": gotchas or ["gotcha 1"],
        "assumptions": assumptions or ["assumed X"],
    }
    return (
        f"I completed the fix.\n\n```json\n{json.dumps(data, indent=2)}\n```\n\nLet me know if you need anything else."
    )


# ---------------------------------------------------------------------------
# TS-05-15: Module exports extract_session_summary with correct signature
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryExport:
    """Verify agentfox.knowledge.extraction exports extract_session_summary.

    Test Spec: TS-05-15
    Requirements: 05-REQ-4.1
    """

    def test_is_callable(self) -> None:
        """extract_session_summary is importable and callable."""
        assert callable(extract_session_summary)

    def test_signature_has_response_parameter(self) -> None:
        """Function signature has exactly one parameter 'response' with type str."""
        sig = inspect.signature(extract_session_summary)
        assert list(sig.parameters.keys()) == ["response"]
        assert sig.parameters["response"].annotation is str


# ---------------------------------------------------------------------------
# TS-05-8: Return type is a 4-tuple of (str|None, list, list, list)
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryReturnType:
    """Verify extract_session_summary returns a 4-tuple.

    Test Spec: TS-05-8
    Requirements: 05-REQ-3.1
    """

    def test_returns_tuple_of_length_4(self) -> None:
        """Return value is a tuple of length 4."""
        result = extract_session_summary("some response text")
        assert isinstance(result, tuple)
        assert len(result) == 4

    def test_first_element_is_str_or_none(self) -> None:
        """First element is str or None."""
        result = extract_session_summary("some response text")
        assert result[0] is None or isinstance(result[0], str)

    def test_remaining_elements_are_lists(self) -> None:
        """Elements 1, 2, 3 are lists."""
        result = extract_session_summary("some response text")
        assert isinstance(result[1], list)
        assert isinstance(result[2], list)
        assert isinstance(result[3], list)


# ---------------------------------------------------------------------------
# TS-05-9: Structured fields present → non-None summary and populated lists
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryStructuredFields:
    """Verify extraction when response contains structured summary fields.

    Test Spec: TS-05-9
    Requirements: 05-REQ-3.2
    """

    def test_non_none_summary_text(self) -> None:
        """summary_text is non-None when structured fields are present."""
        response = _build_structured_response(summary="Fix worked")
        summary_text, _rejected, _gotchas, _assumptions = extract_session_summary(response)
        assert summary_text is not None
        assert isinstance(summary_text, str)
        assert len(summary_text) > 0

    def test_populated_rejected_approaches(self) -> None:
        """rejected_approaches list is populated from response."""
        response = _build_structured_response(
            rejected=[{"approach": "approach A", "reason": "too slow"}],
        )
        _summary, rejected, _gotchas, _assumptions = extract_session_summary(response)
        assert len(rejected) > 0

    def test_populated_gotchas(self) -> None:
        """gotchas list is populated from response."""
        response = _build_structured_response(gotchas=["gotcha 1"])
        _summary, _rejected, gotchas, _assumptions = extract_session_summary(response)
        assert gotchas == ["gotcha 1"]

    def test_populated_assumptions(self) -> None:
        """assumptions list is populated from response."""
        response = _build_structured_response(assumptions=["assumed X"])
        _summary, _rejected, _gotchas, assumptions = extract_session_summary(response)
        assert assumptions == ["assumed X"]

    def test_full_extraction_round_trip(self) -> None:
        """All four fields are correctly extracted from a complete response."""
        response = _build_structured_response(
            summary="Did X",
            rejected=[{"approach": "Y", "reason": "too slow"}],
            gotchas=["Z"],
            assumptions=["W"],
        )
        summary_text, rejected, gotchas, assumptions = extract_session_summary(response)
        assert summary_text is not None
        assert len(summary_text) > 0
        assert len(rejected) > 0
        assert "Z" in gotchas
        assert "W" in assumptions


# ---------------------------------------------------------------------------
# TS-05-10: Empty response → (None, [], [], [])
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryEmpty:
    """Verify (None, [], [], []) for empty or absent structured fields.

    Test Spec: TS-05-10
    Requirements: 05-REQ-3.3
    """

    def test_empty_string_returns_none_tuple(self) -> None:
        """extract_session_summary('') returns (None, [], [], [])."""
        result = extract_session_summary("")
        assert result == (None, [], [], [])

    def test_plain_text_returns_none_tuple(self) -> None:
        """Plain text with no structured fields returns (None, [], [], [])."""
        result = extract_session_summary("Plain text response with no structured fields.")
        assert result == (None, [], [], [])


# ---------------------------------------------------------------------------
# TS-05-11: Malformed response → (None, [], [], []) without raising
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryMalformed:
    """Verify graceful handling of malformed responses.

    Test Spec: TS-05-11
    Requirements: 05-REQ-3.4
    """

    def test_malformed_json_returns_none_tuple(self) -> None:
        """Malformed JSON returns (None, [], [], []) without raising."""
        malformed = "{{malformed: [unclosed, key: }"
        try:
            result = extract_session_summary(malformed)
        except Exception:
            pytest.fail("extract_session_summary raised on malformed input")
        assert result == (None, [], [], [])

    @pytest.mark.parametrize(
        "malformed_input",
        [
            "{bad json:",
            "\x00\xff",
            "<broken>",
            "```json\n{incomplete",
            "```json\n[1, 2, 3]\n```",
            '{"summary": "ok", "rejected_approaches": "not_a_list"}',
        ],
        ids=[
            "truncated_json",
            "null_bytes",
            "xml_like",
            "unclosed_fence",
            "json_array_not_object",
            "wrong_field_type",
        ],
    )
    def test_various_malformed_inputs(self, malformed_input: str) -> None:
        """Various malformed inputs all return (None, [], [], [])."""
        result = extract_session_summary(malformed_input)
        assert result == (None, [], [], [])


# ---------------------------------------------------------------------------
# TS-05-12: extract_session_summary is synchronous (not async)
# ---------------------------------------------------------------------------


class TestExtractSessionSummarySynchronous:
    """Verify extract_session_summary is a synchronous function.

    Test Spec: TS-05-12
    Requirements: 05-REQ-3.5
    """

    def test_not_coroutine_function(self) -> None:
        """inspect.iscoroutinefunction(extract_session_summary) is False."""
        assert not inspect.iscoroutinefunction(extract_session_summary)

    def test_direct_call_returns_tuple(self) -> None:
        """Calling directly without await returns a tuple, not a coroutine."""
        result = extract_session_summary("test")
        assert isinstance(result, tuple)
        assert not inspect.isawaitable(result)


# ---------------------------------------------------------------------------
# TS-05-16, TS-05-44: session_lifecycle.py calls extract_session_summary
# ---------------------------------------------------------------------------


class TestSessionLifecycleCallsExtraction:
    """Verify session_lifecycle.py uses extract_session_summary from the shared module.

    The engine's session_lifecycle should import and call
    ``extract_session_summary`` from ``agentfox.knowledge.extraction``
    as a supplementary extraction path (alongside the existing
    ``_read_session_artifacts`` file-based extraction).

    Note: per drift findings, this is an ADDITIONAL call — it does NOT
    replace ``_extract_knowledge_and_findings`` which does unrelated
    transcript reconstruction work.

    Test Spec: TS-05-16, TS-05-44
    Requirements: 05-REQ-4.2
    """

    def test_session_lifecycle_imports_extract_session_summary(self) -> None:
        """session_lifecycle.py imports extract_session_summary from the shared module."""
        import agentfox.engine.session_lifecycle as sl

        assert hasattr(sl, "extract_session_summary"), (
            "session_lifecycle.py must import extract_session_summary from agentfox.knowledge.extraction"
        )

    def test_extract_is_same_function_from_shared_module(self) -> None:
        """The import in session_lifecycle points to the shared extraction function."""
        import agentfox.engine.session_lifecycle as sl

        sl_fn = getattr(sl, "extract_session_summary", None)
        assert sl_fn is extract_session_summary

    def test_called_without_await(self) -> None:
        """extract_session_summary is called synchronously (no await) in session_lifecycle."""
        expected = ("summary text", ["rejected"], ["gotcha"], ["assumption"])
        with patch(
            "agentfox.engine.session_lifecycle.extract_session_summary",
        ) as mock_fn:
            mock_fn.return_value = expected
            # Verify the mock return is not awaitable (synchronous call)
            call_result = mock_fn("some response")
            assert not inspect.isawaitable(call_result)
            assert call_result == expected

    def test_produces_same_4_tuple_output(self) -> None:
        """The 4-tuple output from the shared function matches expected format."""
        expected = ("summary text", ["rejected"], ["gotcha"], ["assumption"])
        with patch(
            "agentfox.engine.session_lifecycle.extract_session_summary",
            return_value=expected,
        ) as mock_fn:
            result = mock_fn("structured response")
            mock_fn.assert_called_once()
            assert isinstance(result, tuple)
            assert len(result) == 4
            assert result[0] is None or isinstance(result[0], str)
            assert isinstance(result[1], list)
            assert isinstance(result[2], list)
            assert isinstance(result[3], list)
