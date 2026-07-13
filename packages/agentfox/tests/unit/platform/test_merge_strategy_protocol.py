"""Tests for PlatformProtocol.create_pr() and NullPlatform stub.

Test Spec: TS-02-17 (PlatformProtocol signature), TS-02-18 (NullPlatform raises),
           TS-02-E11 (NullPlatform guard bypass — no side effects),
           TS-02-P5 (NullPlatform property test)
Requirements: 02-REQ-6.1, 02-REQ-6.2, 02-REQ-6.E1

Note on NullPlatform: The critical reviewer finding (02-REQ-6.2) identified that
NullPlatform does not exist in the current codebase — create_platform_safe() returns
GitHubPlatform | None.  These tests expect NullPlatform to be created in protocol.py
by task group 11 as the spec requires.  Tests will fail (RED) until that
implementation is done, which is the expected Group 3 behavior.
"""

from __future__ import annotations

import inspect

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# TS-02-17: PlatformProtocol declares create_pr() with exact async
#           keyword-only signature returning str
# ---------------------------------------------------------------------------


class TestPlatformProtocolCreatePrSignature:
    """TS-02-17: PlatformProtocol in protocol.py declares create_pr() with
    the exact async keyword-only signature returning str, with correct docstring.

    Requirements: 02-REQ-6.1
    """

    def test_create_pr_exists_on_protocol(self) -> None:
        """PlatformProtocol has a create_pr attribute."""
        from agentfox.platform.protocol import PlatformProtocol

        assert hasattr(PlatformProtocol, "create_pr"), (
            "PlatformProtocol must declare a create_pr method"
        )

    def test_create_pr_is_coroutine_function(self) -> None:
        """create_pr is declared as an async method (coroutine function)."""
        from agentfox.platform.protocol import PlatformProtocol

        method = PlatformProtocol.create_pr
        assert inspect.iscoroutinefunction(method), (
            "create_pr must be an async method (coroutine function)"
        )

    def test_create_pr_title_param_is_keyword_only_str(self) -> None:
        """'title' parameter is keyword-only with str annotation."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        param = sig.parameters["title"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY, (
            "title must be a keyword-only parameter"
        )
        assert param.annotation is str, (
            "title must be annotated as str"
        )

    def test_create_pr_body_param_is_keyword_only_str(self) -> None:
        """'body' parameter is keyword-only with str annotation."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        param = sig.parameters["body"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY, (
            "body must be a keyword-only parameter"
        )
        assert param.annotation is str, (
            "body must be annotated as str"
        )

    def test_create_pr_head_param_is_keyword_only_str(self) -> None:
        """'head' parameter is keyword-only with str annotation."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        param = sig.parameters["head"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY, (
            "head must be a keyword-only parameter"
        )
        assert param.annotation is str, (
            "head must be annotated as str"
        )

    def test_create_pr_base_param_is_keyword_only_str(self) -> None:
        """'base' parameter is keyword-only with str annotation."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        param = sig.parameters["base"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY, (
            "base must be a keyword-only parameter"
        )
        assert param.annotation is str, (
            "base must be annotated as str"
        )

    def test_create_pr_return_annotation_is_str(self) -> None:
        """create_pr return annotation is str."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        assert sig.return_annotation is str, (
            "create_pr must declare return type -> str"
        )

    def test_create_pr_has_exactly_four_keyword_params(self) -> None:
        """create_pr has exactly four keyword-only parameters (title, body, head, base)."""
        from agentfox.platform.protocol import PlatformProtocol

        sig = inspect.signature(PlatformProtocol.create_pr)
        kw_params = [
            name
            for name, p in sig.parameters.items()
            if p.kind == inspect.Parameter.KEYWORD_ONLY
        ]
        assert kw_params == ["title", "body", "head", "base"], (
            f"Expected keyword-only params [title, body, head, base], got {kw_params}"
        )

    def test_create_pr_docstring_mentions_html_url(self) -> None:
        """create_pr docstring references 'html_url'."""
        from agentfox.platform.protocol import PlatformProtocol

        method = PlatformProtocol.create_pr
        assert method.__doc__ is not None, "create_pr must have a docstring"
        assert "html_url" in method.__doc__, (
            "create_pr docstring must mention 'html_url'"
        )

    def test_create_pr_docstring_mentions_integration_error(self) -> None:
        """create_pr docstring references 'IntegrationError'."""
        from agentfox.platform.protocol import PlatformProtocol

        method = PlatformProtocol.create_pr
        assert method.__doc__ is not None, "create_pr must have a docstring"
        assert "IntegrationError" in method.__doc__, (
            "create_pr docstring must mention 'IntegrationError'"
        )


# ---------------------------------------------------------------------------
# TS-02-18: NullPlatform.create_pr() raises NotImplementedError with exact
#           specified message
# ---------------------------------------------------------------------------


class TestNullPlatformCreatePr:
    """TS-02-18: NullPlatform.create_pr() raises NotImplementedError with
    the exact specified message.

    Requirements: 02-REQ-6.2
    """

    async def test_create_pr_raises_not_implemented_error(self) -> None:
        """NullPlatform.create_pr() raises NotImplementedError."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError):
            await platform.create_pr(title="T", body="B", head="H", base="base")

    async def test_create_pr_error_message_mentions_null_platform(self) -> None:
        """Error message contains 'create_pr() called on NullPlatform'."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(title="T", body="B", head="H", base="base")
        assert "create_pr() called on NullPlatform" in str(exc_info.value)

    async def test_create_pr_error_message_mentions_create_platform_safe(self) -> None:
        """Error message contains 'create_platform_safe()'."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(title="T", body="B", head="H", base="base")
        assert "create_platform_safe()" in str(exc_info.value)

    async def test_create_pr_exact_error_message(self) -> None:
        """Error message matches the exact specified text from the spec."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(title="T", body="B", head="H", base="base")
        msg = str(exc_info.value)
        # Verify the full expected message
        assert "create_pr() called on NullPlatform" in msg
        assert "this should never be reached" in msg
        assert "create_platform_safe()" in msg
        assert "before calling create_pr()" in msg


# ---------------------------------------------------------------------------
# TS-02-E11: Calling create_pr() on NullPlatform (bypassed platform guard)
#            raises NotImplementedError with no network call or git state
#            mutation
# ---------------------------------------------------------------------------


class TestNullPlatformGuardBypass:
    """TS-02-E11: Calling create_pr() on NullPlatform raises
    NotImplementedError with no network call or git state mutation.

    Requirements: 02-REQ-6.E1
    """

    async def test_create_pr_raises_immediately_no_network(self) -> None:
        """NotImplementedError is raised immediately; no HTTP requests are made.

        We verify no network calls by checking that the exception is raised
        before any external interaction could occur — the method should raise
        on the first line, not after attempting a network call.
        """
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError):
            await platform.create_pr(
                title="PR Title",
                body="PR Body",
                head="feat/branch",
                base="main",
            )

    async def test_create_pr_no_git_state_mutation(self) -> None:
        """No git state is modified when NullPlatform.create_pr() is called.

        The exception is raised before any subprocess or git commands execute.
        We verify this by checking the exception is NotImplementedError (no
        side effects can occur before it is raised in the method body).
        """
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        # Should raise immediately — no subprocess calls or filesystem changes
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(
                title="Any Title",
                body="Any Body",
                head="any-branch",
                base="main",
            )
        # Verify it's the expected error, not a side-effect error
        assert "NullPlatform" in str(exc_info.value)

    async def test_create_pr_with_various_arguments_always_raises(self) -> None:
        """NullPlatform.create_pr() raises regardless of argument values."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        test_cases = [
            {"title": "", "body": "", "head": "", "base": ""},
            {"title": "x" * 1000, "body": "y" * 5000, "head": "a/b/c", "base": "main"},
            {"title": "Fix #42", "body": "Fixes #42\n\n## Summary", "head": "fix/42", "base": "develop"},
        ]
        for kwargs in test_cases:
            with pytest.raises(NotImplementedError):
                await platform.create_pr(**kwargs)


# ---------------------------------------------------------------------------
# TS-02-P5: Property test — NullPlatform.create_pr() always raises
#           NotImplementedError with no side effects
# ---------------------------------------------------------------------------


class TestNullPlatformCreatePrProperty:
    """TS-02-P5: For any call to NullPlatform.create_pr() with any arguments,
    NotImplementedError is always raised immediately with no side effects.

    Property: 02-PROP-5
    Validates: 02-REQ-6.2, 02-REQ-6.E1
    """

    @given(
        title=st.text(min_size=0, max_size=200),
        body=st.text(min_size=0, max_size=1000),
        head=st.text(min_size=0, max_size=100),
        base=st.text(min_size=0, max_size=100),
    )
    @settings(max_examples=50)
    async def test_always_raises_not_implemented_error(
        self, title: str, body: str, head: str, base: str
    ) -> None:
        """For any string arguments, NullPlatform.create_pr() always raises
        NotImplementedError."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError):
            await platform.create_pr(title=title, body=body, head=head, base=base)

    @given(
        title=st.text(min_size=0, max_size=100),
        body=st.text(min_size=0, max_size=100),
        head=st.text(min_size=0, max_size=50),
        base=st.text(min_size=0, max_size=50),
    )
    @settings(max_examples=30)
    async def test_error_message_always_contains_null_platform(
        self, title: str, body: str, head: str, base: str
    ) -> None:
        """For any arguments, the NotImplementedError message always mentions
        NullPlatform."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(title=title, body=body, head=head, base=base)
        assert "NullPlatform" in str(exc_info.value)

    @given(
        title=st.text(min_size=0, max_size=100),
        body=st.text(min_size=0, max_size=100),
        head=st.text(min_size=0, max_size=50),
        base=st.text(min_size=0, max_size=50),
    )
    @settings(max_examples=30)
    async def test_error_message_always_contains_create_platform_safe(
        self, title: str, body: str, head: str, base: str
    ) -> None:
        """For any arguments, the NotImplementedError message always mentions
        create_platform_safe()."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError) as exc_info:
            await platform.create_pr(title=title, body=body, head=head, base=base)
        assert "create_platform_safe()" in str(exc_info.value)

    @pytest.mark.parametrize(
        "title,body,head,base",
        [
            ("", "", "", ""),
            ("T", "B", "H", "B"),
            ("Fix #1: Bug", "## Summary\nFixes", "fix/1", "main"),
            ("a" * 500, "b" * 500, "c" * 200, "d" * 200),
        ],
    )
    async def test_parametrized_always_raises(
        self, title: str, body: str, head: str, base: str
    ) -> None:
        """Parametrized: NotImplementedError is always raised for various inputs."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        with pytest.raises(NotImplementedError):
            await platform.create_pr(title=title, body=body, head=head, base=base)

    async def test_no_return_value_possible(self) -> None:
        """NullPlatform.create_pr() never returns a value; it always raises."""
        from agentfox.platform.protocol import NullPlatform

        platform = NullPlatform()
        raised = False
        try:
            result = await platform.create_pr(
                title="T", body="B", head="H", base="main"
            )
            # If we reach here, the test should fail
            pytest.fail(f"Expected NotImplementedError but got return value: {result!r}")
        except NotImplementedError:
            raised = True
        assert raised, "NotImplementedError must be raised"
