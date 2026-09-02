"""Property tests for extract_session_summary.

Verifies that ``extract_session_summary`` never raises an exception for
any string input, always returns a 4-tuple with correct element types,
and degrades gracefully on arbitrary/adversarial inputs.

Test Spec: TS-05-P3
Requirements: 05-REQ-3.1, 05-REQ-3.3, 05-REQ-3.4
Correctness Property: 05-PROP-3
"""

from __future__ import annotations

import pytest
from agentfox.knowledge.extraction import extract_session_summary
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# TS-05-P3: extract_session_summary never raises for any string input
# ---------------------------------------------------------------------------


class TestExtractSessionSummaryNeverRaises:
    """For any string input, extract_session_summary returns a valid 4-tuple.

    Property 05-PROP-3: For any string value passed as the response
    argument to extract_session_summary, the function returns a 4-tuple
    and never raises an exception, regardless of whether the response is
    empty, structured, or malformed.

    Requirements: 05-REQ-3.1, 05-REQ-3.3, 05-REQ-3.4
    """

    @given(input_str=st.text())
    @settings(max_examples=200)
    def test_arbitrary_text_never_raises(self, input_str: str) -> None:
        """extract_session_summary never raises for arbitrary text input."""
        try:
            result = extract_session_summary(input_str)
        except Exception as exc:
            pytest.fail(f"extract_session_summary raised {type(exc).__name__}: {exc}")
        assert isinstance(result, tuple), f"Expected tuple, got {type(result)}"
        assert len(result) == 4, f"Expected 4-tuple, got length {len(result)}"

    @given(input_str=st.text())
    @settings(max_examples=200)
    def test_first_element_str_or_none(self, input_str: str) -> None:
        """First element of the return tuple is always str or None."""
        result = extract_session_summary(input_str)
        assert result[0] is None or isinstance(result[0], str), f"Expected str|None, got {type(result[0])}"

    @given(input_str=st.text())
    @settings(max_examples=200)
    def test_remaining_elements_are_lists(self, input_str: str) -> None:
        """Elements 1, 2, 3 are always lists."""
        result = extract_session_summary(input_str)
        for i in range(1, 4):
            assert isinstance(result[i], list), f"Element {i}: expected list, got {type(result[i])}"

    @given(
        input_str=st.from_regex(
            r"[\x00-\x1f\x7f-\xff]{0,100}",
            fullmatch=True,
        ),
    )
    @settings(max_examples=50)
    def test_control_chars_and_high_bytes(self, input_str: str) -> None:
        """Control characters and high bytes never cause exceptions."""
        try:
            result = extract_session_summary(input_str)
        except Exception as exc:
            pytest.fail(f"extract_session_summary raised on control/high bytes: {exc}")
        assert isinstance(result, tuple)
        assert len(result) == 4

    @pytest.mark.parametrize(
        "edge_input",
        [
            "",
            " ",
            "\n",
            "\t",
            "\x00",
            "null",
            "None",
            "{}",
            "[]",
            '{"summary": null}',
            "```json\n```",
            "```\n{}\n```",
        ],
        ids=[
            "empty",
            "space",
            "newline",
            "tab",
            "null_byte",
            "literal_null",
            "literal_none",
            "empty_object",
            "empty_array",
            "null_summary",
            "empty_json_fence",
            "empty_object_in_fence",
        ],
    )
    def test_edge_case_inputs(self, edge_input: str) -> None:
        """Known edge-case inputs never raise and return a valid 4-tuple."""
        try:
            result = extract_session_summary(edge_input)
        except Exception as exc:
            pytest.fail(f"extract_session_summary raised on edge input: {exc}")
        assert isinstance(result, tuple)
        assert len(result) == 4
        assert result[0] is None or isinstance(result[0], str)
        assert all(isinstance(result[i], list) for i in range(1, 4))
