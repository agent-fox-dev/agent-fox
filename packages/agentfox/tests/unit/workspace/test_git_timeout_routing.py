"""Tests for git timeout routing — rev-list and worktree get the remote timeout.

Issue #681: rev-list and worktree commands were using the 60s default timeout
instead of the 120s remote timeout, causing timeouts during high-throughput runs.

Test Spec: TS-NS-1, TS-NS-2
Requirements: NS-REQ-1, NS-REQ-2
"""

from __future__ import annotations

from agentfox.workspace.git import _GIT_REMOTE_TIMEOUT, _REMOTE_SUBCOMMANDS


class TestRemoteSubcommandsMembership:
    """Verify rev-list and worktree are in _REMOTE_SUBCOMMANDS."""

    def test_rev_list_in_remote_subcommands(self) -> None:
        """TS-NS-1: rev-list is in _REMOTE_SUBCOMMANDS for 120s timeout."""
        assert "rev-list" in _REMOTE_SUBCOMMANDS

    def test_worktree_in_remote_subcommands(self) -> None:
        """TS-NS-2: worktree is in _REMOTE_SUBCOMMANDS for 120s timeout."""
        assert "worktree" in _REMOTE_SUBCOMMANDS

    def test_existing_remote_subcommands_unchanged(self) -> None:
        """Original remote subcommands are still present."""
        for cmd in ("fetch", "push", "pull", "clone", "ls-remote"):
            assert cmd in _REMOTE_SUBCOMMANDS


class TestTimeoutRouting:
    """Verify run_git selects the correct timeout for rev-list and worktree."""

    async def test_rev_list_gets_remote_timeout(self, tmp_path) -> None:
        """run_git picks _GIT_REMOTE_TIMEOUT (120s) for rev-list subcommand."""
        import asyncio
        from unittest.mock import AsyncMock, patch

        from agentfox.workspace.git import run_git

        # We mock create_subprocess_exec to capture the timeout used
        captured_timeout = None

        mock_proc = AsyncMock()
        mock_proc.communicate = AsyncMock(return_value=(b"5\n", b""))
        mock_proc.returncode = 0
        mock_proc.kill = AsyncMock()
        mock_proc.wait = AsyncMock()

        original_wait_for = asyncio.wait_for

        async def capture_wait_for(coro, timeout=None):
            nonlocal captured_timeout
            captured_timeout = timeout
            return await original_wait_for(coro, timeout=timeout)

        with (
            patch("asyncio.create_subprocess_exec", return_value=mock_proc),
            patch("asyncio.wait_for", side_effect=capture_wait_for),
        ):
            await run_git(
                ["rev-list", "--count", "origin/main..main"],
                cwd=tmp_path,
                check=False,
            )

        assert captured_timeout == _GIT_REMOTE_TIMEOUT

    async def test_worktree_gets_remote_timeout(self, tmp_path) -> None:
        """run_git picks _GIT_REMOTE_TIMEOUT (120s) for worktree subcommand."""
        import asyncio
        from unittest.mock import AsyncMock, patch

        from agentfox.workspace.git import run_git

        captured_timeout = None

        mock_proc = AsyncMock()
        mock_proc.communicate = AsyncMock(return_value=(b"", b""))
        mock_proc.returncode = 0
        mock_proc.kill = AsyncMock()
        mock_proc.wait = AsyncMock()

        original_wait_for = asyncio.wait_for

        async def capture_wait_for(coro, timeout=None):
            nonlocal captured_timeout
            captured_timeout = timeout
            return await original_wait_for(coro, timeout=timeout)

        with (
            patch("asyncio.create_subprocess_exec", return_value=mock_proc),
            patch("asyncio.wait_for", side_effect=capture_wait_for),
        ):
            await run_git(
                ["worktree", "add", "/tmp/test-worktree", "main"],
                cwd=tmp_path,
                check=False,
            )

        assert captured_timeout == _GIT_REMOTE_TIMEOUT
