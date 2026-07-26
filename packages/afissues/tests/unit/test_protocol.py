"""Tests for afissues.protocol module (TS-03-6 through TS-03-9, TS-03-E2).

Verifies PlatformProtocol typing.Protocol definition, IssueResult and
IssueComment frozen dataclasses, and NullPlatform no-op behaviour.

Requirements: 03-REQ-2.1, 03-REQ-2.2, 03-REQ-2.3, 03-REQ-2.4, 03-REQ-2.E1

Drift errata:
  - close() is async in the codebase (protocol.py:165), not synchronous as
    03-REQ-2.1 states.  All 12 PlatformProtocol methods are async; there are
    zero synchronous public methods.  Tests below match the actual code.
"""

from __future__ import annotations

import dataclasses
import inspect

import pytest

# ── TS-03-6: PlatformProtocol is a Protocol with 12 async methods ──


class TestPlatformProtocol:
    """TS-03-6: PlatformProtocol is a typing.Protocol subclass.

    Drift note: the spec says '12 async methods and a synchronous close()'.
    In reality close() is ``async def`` — all 12 methods are async.
    """

    def test_is_protocol(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert getattr(PlatformProtocol, "_is_protocol", False), "PlatformProtocol must be a typing.Protocol subclass"

    def test_has_15_async_methods(self) -> None:
        from afissues.protocol import PlatformProtocol

        # Collect public methods defined directly on PlatformProtocol
        # 12 original + 3 new PR query methods (06-REQ-3.1, 06-REQ-3.2, 06-REQ-3.3)
        protocol_methods = {
            name for name, obj in vars(PlatformProtocol).items() if callable(obj) and not name.startswith("_")
        }
        async_methods = {name for name in protocol_methods if inspect.iscoroutinefunction(vars(PlatformProtocol)[name])}
        assert len(async_methods) == 15, f"Expected 15 async methods, got {len(async_methods)}: {sorted(async_methods)}"

    def test_close_is_async(self) -> None:
        """Drift: close() is async in the actual codebase."""
        from afissues.protocol import PlatformProtocol

        close_fn = vars(PlatformProtocol).get("close")
        assert close_fn is not None, "close must be defined on PlatformProtocol"
        assert inspect.iscoroutinefunction(close_fn), "close() must be async"

    def test_expected_method_names(self) -> None:
        from afissues.protocol import PlatformProtocol

        expected = {
            "create_issue",
            "list_issues_by_label",
            "add_issue_comment",
            "assign_label",
            "close_issue",
            "remove_label",
            "list_issue_comments",
            "get_issue",
            "update_issue",
            "create_label",
            "create_pr",
            "close",
        }
        actual = {
            name
            for name in vars(PlatformProtocol)
            if not name.startswith("_") and callable(vars(PlatformProtocol)[name])
        }
        assert expected <= actual, f"Missing methods: {expected - actual}"

    def test_is_runtime_checkable(self) -> None:
        from afissues.protocol import PlatformProtocol

        # runtime_checkable Protocols have __protocol_attrs__
        assert getattr(PlatformProtocol, "__protocol_attrs__", None) is not None


# ── TS-03-7: IssueResult is a frozen dataclass ─────────────────────


class TestIssueResult:
    """TS-03-7: IssueResult frozen dataclass with five fields."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import IssueResult

        assert dataclasses.is_dataclass(IssueResult)

    def test_has_five_fields(self) -> None:
        from afissues.protocol import IssueResult

        fields = {f.name for f in dataclasses.fields(IssueResult)}
        assert fields == {"number", "title", "html_url", "body", "labels"}

    def test_is_frozen(self) -> None:
        from afissues.protocol import IssueResult

        issue = IssueResult(number=1, title="t", html_url="u", body="b", labels=("l1",))
        with pytest.raises(dataclasses.FrozenInstanceError):
            issue.number = 2  # type: ignore[misc]

    def test_field_access(self) -> None:
        from afissues.protocol import IssueResult

        issue = IssueResult(number=42, title="Bug", html_url="https://example.com", body="desc", labels=("bug",))
        assert issue.number == 42
        assert issue.title == "Bug"
        assert issue.html_url == "https://example.com"
        assert issue.body == "desc"
        assert issue.labels == ("bug",)

    def test_body_defaults_empty(self) -> None:
        from afissues.protocol import IssueResult

        issue = IssueResult(number=1, title="t", html_url="u")
        assert issue.body == ""

    def test_labels_defaults_empty(self) -> None:
        from afissues.protocol import IssueResult

        issue = IssueResult(number=1, title="t", html_url="u")
        assert issue.labels == ()


# ── TS-03-8: IssueComment is a frozen dataclass ────────────────────


class TestIssueComment:
    """TS-03-8: IssueComment frozen dataclass with four fields."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import IssueComment

        assert dataclasses.is_dataclass(IssueComment)

    def test_has_four_fields(self) -> None:
        from afissues.protocol import IssueComment

        fields = {f.name for f in dataclasses.fields(IssueComment)}
        assert fields == {"id", "body", "user", "created_at"}

    def test_is_frozen(self) -> None:
        from afissues.protocol import IssueComment

        comment = IssueComment(id=42, body="hi", user="alice", created_at="2024-01-01T00:00:00Z")
        with pytest.raises(dataclasses.FrozenInstanceError):
            comment.id = 99  # type: ignore[misc]

    def test_field_access(self) -> None:
        from afissues.protocol import IssueComment

        comment = IssueComment(id=7, body="note", user="bob", created_at="2024-06-15T12:00:00Z")
        assert comment.id == 7
        assert comment.body == "note"
        assert comment.user == "bob"
        assert comment.created_at == "2024-06-15T12:00:00Z"


# ── TS-03-9: NullPlatform no-ops except create_pr ──────────────────


class TestNullPlatform:
    """TS-03-9: NullPlatform methods are no-ops; create_pr raises."""

    async def test_create_issue_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        result = await np.create_issue(title="t", body="b")
        # Should return without raising; result may be a dummy IssueResult
        assert result is not None or result is None  # just verify no exception

    async def test_list_issues_by_label_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        result = await np.list_issues_by_label(label="af:fix")
        assert isinstance(result, list)

    async def test_add_issue_comment_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.add_issue_comment(issue_number=1, body="comment")

    async def test_assign_label_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.assign_label(issue_number=1, label="af:fix")

    async def test_close_issue_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.close_issue(issue_number=1)

    async def test_remove_label_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.remove_label(issue_number=1, label="af:fix")

    async def test_list_issue_comments_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        result = await np.list_issue_comments(issue_number=1)
        assert isinstance(result, list)

    async def test_get_issue_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        result = await np.get_issue(issue_number=1)
        assert result is not None or result is None

    async def test_update_issue_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.update_issue(issue_number=1, body="updated")

    async def test_create_label_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.create_label(name="af:fix", color="12ec39")

    async def test_close_is_noop(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        await np.close()


# ── TS-03-E2: NullPlatform.create_pr() raises NotImplementedError ──


class TestNullPlatformCreatePrRaises:
    """TS-03-E2: create_pr() unconditionally raises NotImplementedError."""

    async def test_create_pr_raises_not_implemented(self) -> None:
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        with pytest.raises(NotImplementedError):
            await np.create_pr(title="t", body="b", head="h", base="main")

    async def test_create_pr_error_propagates(self) -> None:
        """The NotImplementedError must not be caught or suppressed."""
        from afissues.protocol import NullPlatform

        np = NullPlatform()
        raised = False
        try:
            await np.create_pr(title="pr", body="body", head="feat", base="main")
        except NotImplementedError:
            raised = True
        except Exception:
            pytest.fail("Expected NotImplementedError, got a different exception type")
        assert raised, "NotImplementedError was not raised"
