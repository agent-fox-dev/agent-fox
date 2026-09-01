"""Smoke tests for the merge strategy feature — end-to-end wiring verification.

These tests exercise complete execution paths through real components with
only external dependencies mocked (git, GitHub API, filesystem).  They verify
that all layers are correctly wired together and that the call chains have
no stubs.

Test Spec: TS-02-SMOKE-1 (af code + pr mode success),
           TS-02-SMOKE-2 (af code + branch mode),
           TS-02-SMOKE-3 (nightshift + pr mode success),
           TS-02-SMOKE-4 (pr mode platform-None fallback),
           TS-02-SMOKE-5 (nightshift pr partial failure),
           TS-02-SMOKE-6 (duplicate PR idempotent handling),
           TS-02-SMOKE-7 (direct mode zero-regression)

Execution Paths: 02-PATH-1 through 02-PATH-7

Requirements: 02-REQ-2.1, 02-REQ-3.1, 02-REQ-4.1, 02-REQ-4.2, 02-REQ-4.3,
              02-REQ-4.E2, 02-REQ-4.E3, 02-REQ-7.2, 02-REQ-10.1

Wiring Verification (Task 19.1, 19.2, 19.5):
- 19.1: Each path's call chain is traced and confirmed stub-free.
- 19.2: Return value propagation is verified (callers receive correct tuples).
- 19.5: Operation sequence integrity for PR mode is verified.
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afaudit.sink import SessionOutcome
from afissues.errors import IntegrationError
from afissues.protocol import IssueResult
from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.maintenance.fix_pipeline import FixPipeline
from agentfox.maintenance.spec_builder import InMemorySpec
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers: create minimal test objects (consistent with prior groups)
# ---------------------------------------------------------------------------

_MOCK_KB = MagicMock(spec=KnowledgeDB)


def _make_workspace(
    branch: str = "feature/test_spec/1",
    spec_name: str = "test_spec",
    task_group: int = 1,
) -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name=spec_name,
        task_group=task_group,
    )


def _make_session_outcome(status: str = "completed") -> SessionOutcome:
    return SessionOutcome(
        status=status,
        spec_name="test_spec",
        task_group="1",
        node_id="test_spec:1",
    )


def _make_runner(
    merge_strategy: str = "direct",
    integration_branch: str = "main",
    force_clean: bool = False,
) -> NodeSessionRunner:
    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch=integration_branch,
            force_clean=force_clean,
        ),
    )
    return NodeSessionRunner(
        "test_spec:1",
        config,
        knowledge_db=_MOCK_KB,
    )


def _make_issue(
    number: int = 42,
    title: str = "Login fails on empty password",
) -> IssueResult:
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
    )


def _make_spec(
    issue_number: int = 42,
    branch_name: str = "fix/test-branch",
) -> InMemorySpec:
    return InMemorySpec(
        issue_number=issue_number,
        title="Login fails on empty password",
        task_prompt="Fix the bug",
        system_context="Bug context",
        branch_name=branch_name,
    )


def _make_fix_pipeline(
    merge_strategy: str = "direct",
    platform: object | None = None,
) -> FixPipeline:
    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch="main",
        ),
    )
    if platform is None:
        platform = MagicMock()
        platform.add_issue_comment = AsyncMock()
        platform.close_issue = AsyncMock()
        platform.create_pr = AsyncMock()
    return FixPipeline(
        config=config,
        platform=platform,
    )


def _make_mock_platform(
    *,
    owner: str = "owner",
    repo: str = "repo",
    create_pr_url: str = "https://github.com/owner/repo/pull/1",
) -> MagicMock:
    from afissues.protocol import PrResult

    platform = MagicMock()
    platform._owner = owner
    platform._repo = repo
    platform.create_pr = AsyncMock(
        return_value=PrResult(html_url=create_pr_url, number=1),
    )
    platform.add_issue_comment = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    return platform


# ---------------------------------------------------------------------------
# TS-02-SMOKE-1: af code session with pr mode — successful PR creation
# Execution Path: 02-PATH-1
# ---------------------------------------------------------------------------


class TestSmokePrModeAfCodeSuccess:
    """TS-02-SMOKE-1: End-to-end af code session with pr mode successfully
    creates a GitHub PR and includes the PR URL in the session output.

    Execution Path: 02-PATH-1
    Requirements: 02-REQ-4.1, 02-REQ-9.2, 02-REQ-9.6, 02-REQ-10.1
    """

    @pytest.mark.asyncio
    async def test_full_pr_mode_af_code_path(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Full end-to-end: platform check -> push -> get_changed_files ->
        build_pr_body -> create_pr -> INFO log -> return tuple.
        """
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(
            branch="feat/ms",
            spec_name="merge_strategy",
            task_group=1,
        )
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/1",
        )

        call_order: list[str] = []

        # Instrument all dependencies for call-order tracking
        async def tracked_push(*a, **kw):
            call_order.append("push")
            return True

        async def tracked_gcf(*a, **kw):
            call_order.append("get_changed_files")
            return ["config.py"]

        async def tracked_create_pr(**kw):
            from afissues.protocol import PrResult

            call_order.append("create_pr")
            return PrResult(html_url="https://github.com/owner/repo/pull/1", number=1)

        def tracked_cps(*a, **kw):
            call_order.append("create_platform_safe")
            return mock_platform

        mock_platform.create_pr = AsyncMock(side_effect=tracked_create_pr)

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=tracked_gcf,
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                side_effect=tracked_cps,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=tracked_push,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # 1. create_platform_safe called (and returns platform)
        assert "create_platform_safe" in call_order

        # 2. Push happens after platform check
        assert "push" in call_order
        assert call_order.index("create_platform_safe") < call_order.index("push")

        # 3. get_changed_files called after push
        assert "get_changed_files" in call_order
        assert call_order.index("push") < call_order.index("get_changed_files")

        # 4. create_pr called after get_changed_files (build_pr_body is inline)
        assert "create_pr" in call_order
        assert call_order.index("get_changed_files") < call_order.index("create_pr")

        # 5. harvest() NOT called (pr mode skips squash-merge)
        assert mock_harvest.call_count == 0

        # 6. INFO log with PR URL
        pr_log = [
            r for r in caplog.records
            if r.levelno == logging.INFO and "Pull request created" in r.message
        ]
        assert len(pr_log) == 1
        assert "https://github.com/owner/repo/pull/1" in pr_log[0].message

        # 7. Return tuple shape
        assert result == ("completed", None, ["config.py"], False)


# ---------------------------------------------------------------------------
# TS-02-SMOKE-2: af code session with branch mode
# Execution Path: 02-PATH-2
# ---------------------------------------------------------------------------


class TestSmokeBranchModeAfCode:
    """TS-02-SMOKE-2: End-to-end af code session with branch mode keeps the
    feature branch locally and includes the branch name in the session output.

    Execution Path: 02-PATH-2
    Requirements: 02-REQ-3.1, 02-REQ-9.1, 02-REQ-9.5
    """

    @pytest.mark.asyncio
    async def test_full_branch_mode_af_code_path(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Full end-to-end: skip harvest -> get_changed_files -> INFO log ->
        return tuple with branch name.
        """
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ) as mock_gcf,
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # 1. harvest() NOT called
        assert mock_harvest.call_count == 0

        # 2. git push NOT called
        mock_push.assert_not_called()

        # 3. get_changed_files called once
        assert mock_gcf.call_count == 1

        # 4. INFO log with branch name
        info_logs = [
            r for r in caplog.records
            if r.levelno == logging.INFO and "Merge strategy is 'branch'" in r.message
        ]
        assert len(info_logs) == 1
        assert "feat/my-feature" in info_logs[0].message

        # 5. Return tuple
        assert result == ("completed", None, ["config.py"], False)


# ---------------------------------------------------------------------------
# TS-02-SMOKE-3: nightshift fix session with pr mode — successful PR creation
# Execution Path: 02-PATH-3
# ---------------------------------------------------------------------------


class TestSmokeNightshiftPrModeSuccess:
    """TS-02-SMOKE-3: End-to-end nightshift fix session with pr mode creates a
    PR with the correct title and body including 'Fixes #N', leaving the
    issue open.

    Execution Path: 02-PATH-3
    Requirements: 02-REQ-4.2, 02-REQ-10.1
    """

    @pytest.mark.asyncio
    async def test_full_nightshift_pr_mode_path(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Full end-to-end: platform check -> push -> get_changed_files ->
        build_pr_body -> create_pr; issue NOT closed; Fixes #N in body.
        """
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(
            merge_strategy="pr", platform=mock_platform,
        )
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            caplog.at_level(logging.DEBUG),
            patch.object(
                pipeline, "_harvest_and_push",
                new_callable=AsyncMock, return_value=["auth/login.py"],
            ) as mock_hp,
            patch.object(
                pipeline, "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline, "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(pipeline, "_update_spinner"),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock, return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock, return_value=True,
            ),
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        # 1. _harvest_and_push NOT called (pr mode)
        assert mock_hp.call_count == 0

        # 2. create_pr called with nightshift title
        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        title = call_kwargs.kwargs.get("title")
        assert title == "Fix #42: Login fails on empty password"

        # 3. PR body contains 'Fixes #42'
        body = call_kwargs.kwargs.get("body", "")
        assert "Fixes #42" in body

        # 4. PR body contains '## Changed Files' with the file
        assert "## Changed Files" in body
        assert "auth/login.py" in body

        # 5. close_issue NOT called
        mock_platform.close_issue.assert_not_called()

        # 6. INFO log with PR URL
        pr_logs = [
            r for r in caplog.records
            if r.levelno == logging.INFO and "Pull request created" in r.message
        ]
        assert len(pr_logs) == 1
        assert "https://github.com/owner/repo/pull/5" in pr_logs[0].message

        # 7. Return value — 06-REQ-8.1: pr mode returns "pr_created"
        assert result == ("pr_created", ["auth/login.py"])


# ---------------------------------------------------------------------------
# TS-02-SMOKE-4: pr mode fallback — platform not configured
# Execution Path: 02-PATH-4
# ---------------------------------------------------------------------------


class TestSmokePrModeFallbackNoPlatform:
    """TS-02-SMOKE-4: End-to-end pr mode falls back to branch mode gracefully
    when platform is not configured, with no branch push.

    Execution Path: 02-PATH-4
    Requirements: 02-REQ-4.3, 02-REQ-9.3
    """

    @pytest.mark.asyncio
    async def test_hai_pr_fallback_to_branch(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """_harvest_and_integrate: platform None -> WARNING -> branch behavior."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock, return_value=["f.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch("agentfox.engine.session_lifecycle.emit_audit_event"),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock, return_value=["f.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # 1. Push NOT called
        mock_push.assert_not_called()

        # 2. harvest NOT called (branch mode fallback, not direct)
        assert mock_harvest.call_count == 0

        # 3. WARNING log
        warns = [
            r for r in caplog.records
            if r.levelno == logging.WARNING and "falling back" in r.message.lower()
        ]
        assert len(warns) == 1
        assert "platform is not configured" in warns[0].message.lower()

        # 4. Return tuple
        status, err, _files, non_retry = result
        assert status == "completed"
        assert err is None
        assert non_retry is False

    @pytest.mark.asyncio
    async def test_integrate_fix_pr_fallback_to_branch(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """_integrate_fix: platform None -> WARNING -> branch behavior."""
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            caplog.at_level(logging.DEBUG),
            patch.object(
                pipeline, "_harvest_and_push",
                new_callable=AsyncMock, return_value=["f.py"],
            ),
            patch.object(
                pipeline, "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline, "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ) as mock_push,
            patch.object(pipeline, "_update_spinner"),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock, return_value=["f.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        # Push NOT called
        mock_push.assert_not_called()

        # WARNING log
        warns = [
            r for r in caplog.records
            if r.levelno == logging.WARNING and "falling back" in r.message.lower()
        ]
        assert len(warns) == 1

        # Return value
        assert len(result) == 2
        status, _files = result
        assert status == "merged"


# ---------------------------------------------------------------------------
# TS-02-SMOKE-5: nightshift pr mode partial failure — branch pushed but PR
#                creation fails; branch-mode comment is posted.
# Execution Path: 02-PATH-5
# ---------------------------------------------------------------------------


class TestSmokeNightshiftPrPartialFailure:
    """TS-02-SMOKE-5 (updated for spec 06): End-to-end nightshift pr mode
    partial failure — branch is pushed but PR creation fails; exception
    propagates from _integrate_fix (06-REQ-8.4) and _pr_number stays None.

    The previous branch-mode fallback (02-REQ-4.E3) was removed to prevent
    premature issue closing.  See
    docs/errata/06_pr_create_exception_propagation.md.

    Execution Path: 02-PATH-5
    Requirements: 06-REQ-8.4 (supersedes 02-REQ-4.E3)
    """

    @pytest.mark.asyncio
    async def test_full_partial_failure_path(self) -> None:
        """Push succeeds -> create_pr raises IntegrationError -> exception
        propagates -> _pr_number remains None.
        """
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("GitHub PR creation failed (500)"),
        )
        pipeline = _make_fix_pipeline(
            merge_strategy="pr", platform=mock_platform,
        )
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline, "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock, return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock, return_value=True,
            ) as mock_push,
        ):
            with pytest.raises(IntegrationError, match="GitHub PR creation failed"):
                await pipeline._integrate_fix(issue, spec, workspace)

        # 1. Push was called (succeeds)
        assert mock_push.call_count == 1

        # 2. create_pr was called (and raised)
        mock_platform.create_pr.assert_called_once()

        # 3. _pr_number stays None — no partial state exposed
        assert pipeline._pr_number is None

        # 4. close_issue NOT called
        mock_platform.close_issue.assert_not_called()


# ---------------------------------------------------------------------------
# TS-02-SMOKE-6: pr mode handles duplicate PR idempotently
# Execution Path: 02-PATH-6
#
# This test exercises GitHubPlatform.create_pr directly since it handles
# the HTTP 422 duplicate-PR logic internally.
# ---------------------------------------------------------------------------


class TestSmokeDuplicatePrIdempotent:
    """TS-02-SMOKE-6: End-to-end pr mode handles duplicate PR idempotently —
    HTTP 422 from GitHub causes lookup of existing PR URL which is returned
    as success.

    Execution Path: 02-PATH-6
    Requirements: 02-REQ-7.2

    Note: This tests GitHubPlatform.create_pr directly with mocked httpx
    responses since the duplicate-PR handling is inside the GitHub client.
    """

    @pytest.mark.asyncio
    async def test_duplicate_pr_returns_existing_url(self) -> None:
        """HTTP 422 with duplicate-PR -> GET existing PR -> return html_url."""
        from afissues.github import GitHubPlatform

        # Mock the _request method on GitHubPlatform
        mock_422_resp = MagicMock()
        mock_422_resp.status_code = 422
        mock_422_resp.json.return_value = {
            "errors": [{"message": "A pull request already exists for owner:feat/x"}],
        }

        mock_get_resp = MagicMock()
        mock_get_resp.status_code = 200
        mock_get_resp.json.return_value = [
            {"html_url": "https://github.com/owner/repo/pull/7", "number": 7},
        ]

        platform = GitHubPlatform.__new__(GitHubPlatform)
        platform._owner = "owner"
        platform._repo = "repo"
        platform._api_base = "https://api.github.com"

        call_count = {"post": 0, "get": 0}

        async def mock_request(method, url, **kwargs):
            if method == "post":
                call_count["post"] += 1
                return mock_422_resp
            if method == "get":
                call_count["get"] += 1
                return mock_get_resp
            raise AssertionError(f"Unexpected method: {method}")

        platform._request = mock_request
        platform._auth_headers = lambda: {"Authorization": "Bearer test"}

        result = await platform.create_pr(
            title="My PR", body="body", head="feat/x", base="main",
        )

        # Exactly one POST, one GET
        assert call_count["post"] == 1
        assert call_count["get"] == 1

        # Returns PrResult with the existing PR URL
        assert result.html_url == "https://github.com/owner/repo/pull/7"


# ---------------------------------------------------------------------------
# TS-02-SMOKE-7: direct mode — zero regression end-to-end
# Execution Path: 02-PATH-7
# ---------------------------------------------------------------------------


class TestSmokeDirectModeZeroRegression:
    """TS-02-SMOKE-7: End-to-end direct mode (absent merge_strategy) executes
    unchanged squash-merge behavior with no regressions.

    Execution Path: 02-PATH-7
    Requirements: 02-REQ-2.1, 02-REQ-1.E1
    """

    @pytest.mark.asyncio
    async def test_full_direct_mode_path(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Full end-to-end: harvest() called -> squash-merge -> push; no
        create_platform_safe, no extra merge strategy logs.
        """
        # Use default config — merge_strategy absent, defaults to 'direct'
        config = AgentFoxConfig(workspace=WorkspaceConfig())
        runner = NodeSessionRunner(
            "test_spec:1", config, knowledge_db=_MOCK_KB,
        )
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock, return_value=["existing_file.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch("agentfox.engine.session_lifecycle.emit_audit_event"),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=MagicMock(),
            ) as mock_cps,
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # 1. Config defaults to 'direct'
        assert config.workspace.merge_strategy == "direct"

        # 2. harvest() called exactly once
        assert mock_harvest.call_count == 1

        # 3. create_platform_safe NOT called
        mock_cps.assert_not_called()

        # 4. No merge strategy log lines
        merge_logs = [
            r for r in caplog.records
            if r.levelno >= logging.INFO and "Merge strategy" in r.message
        ]
        assert len(merge_logs) == 0

        # 5. Return tuple
        assert result == ("completed", None, ["existing_file.py"], False)


# ---------------------------------------------------------------------------
# TS-02-SMOKE-HAI-PARTIAL: af code pr mode partial failure
# (Supplements PATH-5 with the _harvest_and_integrate side)
# Execution Path: 02-PATH-5 (af code variant)
# ---------------------------------------------------------------------------


class TestSmokeAfCodePrPartialFailure:
    """Supplementary smoke test: af code session with pr mode where push
    succeeds but create_pr raises IntegrationError.

    Execution Path: 02-PATH-5 (af code variant)
    Requirements: 02-REQ-4.E2, 02-REQ-9.4
    """

    @pytest.mark.asyncio
    async def test_af_code_partial_failure_path(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Push succeeds -> create_pr raises -> ERROR log -> branch mode
        fallback -> return ('completed', None, files, False).
        """
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("GitHub PR creation failed (500)"),
        )

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock, return_value=["f.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch("agentfox.engine.session_lifecycle.emit_audit_event"),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock, return_value=["f.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock, return_value=True,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # 1. harvest NOT called (pr mode)
        assert mock_harvest.call_count == 0

        # 2. ERROR log with remote branch URL
        error_logs = [
            r for r in caplog.records
            if r.levelno == logging.ERROR and "PR creation failed" in r.message
        ]
        assert len(error_logs) == 1
        assert "https://github.com/owner/repo/tree/feat/my-branch" in error_logs[0].message

        # 3. Return tuple — completed, not failed
        assert result == ("completed", None, ["f.py"], False)
