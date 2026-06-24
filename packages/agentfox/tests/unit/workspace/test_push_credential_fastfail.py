"""Tests for credential fast-fail in push retry logic.

Verifies that 'terminal prompts disabled' is classified as non-retryable,
preventing wasted push retries when git credentials are unavailable.
"""

from __future__ import annotations

from agentfox.workspace.harvest import _is_non_retryable_push_error


class TestTerminalPromptsDisabled:
    """'terminal prompts disabled' should be classified as non-retryable."""

    def test_terminal_prompts_disabled_is_non_retryable(self) -> None:
        stderr = "fatal: could not read Username for 'https://github.com': terminal prompts disabled"
        assert _is_non_retryable_push_error(stderr) is True

    def test_terminal_prompts_disabled_case_insensitive(self) -> None:
        stderr = "fatal: Terminal Prompts Disabled"
        assert _is_non_retryable_push_error(stderr) is True

    def test_non_fast_forward_is_retryable(self) -> None:
        stderr = "! [rejected] develop -> develop (non-fast-forward)"
        assert _is_non_retryable_push_error(stderr) is False

    def test_existing_patterns_still_work(self) -> None:
        assert _is_non_retryable_push_error("authentication failed") is True
        assert _is_non_retryable_push_error("permission denied") is True
        assert _is_non_retryable_push_error("could not resolve host") is True
        assert _is_non_retryable_push_error("connection refused") is True
        assert _is_non_retryable_push_error("repository not found") is True
