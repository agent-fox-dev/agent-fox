"""Property-based tests for local-only feature branch workflow — spec 78.

Test Spec: TS-78-P1, TS-78-P2
Requirements: 78-REQ-1.1, 78-REQ-1.2, 78-REQ-1.E1
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

try:
    from hypothesis import HealthCheck, given, settings
    from hypothesis import strategies as st

    HAS_HYPOTHESIS = True
except ImportError:
    HAS_HYPOTHESIS = False

from agentfox.workspace import WorkspaceInfo
from agentfox.workspace.harvest import post_harvest_integrate

pytestmark = pytest.mark.skipif(not HAS_HYPOTHESIS, reason="hypothesis not installed")

_feature_branch_strategy = st.from_regex(r"feature/[a-z_]+/[0-9]+", fullmatch=True)


def _run(coro):
    """Run a coroutine in a fresh event loop (compatible with Python 3.12+)."""
    return asyncio.run(coro)


# ---------------------------------------------------------------------------
# TS-78-P1: Post-harvest never calls push_to_remote directly
# ---------------------------------------------------------------------------


@given(branch=_feature_branch_strategy)
@settings(suppress_health_check=[HealthCheck.function_scoped_fixture])
def test_never_pushes_feature(branch: str) -> None:
    """Property: for any branch name, push_to_remote is never called directly.

    Test Spec: TS-78-P1
    Requirements: 78-REQ-1.1, 78-REQ-1.3
    """
    workspace = WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name="test_spec",
        task_group=1,
    )
    mock_push_remote = AsyncMock(return_value=True)

    with (
        patch(
            "agentfox.workspace.harvest.push_to_remote",
            mock_push_remote,
        ),
        patch(
            "agentfox.workspace.harvest._push_integration_branch",
            new_callable=AsyncMock,
        ),
    ):
        _run(
            post_harvest_integrate(
                repo_root=Path("/tmp/repo"),
                workspace=workspace,
                branch="develop",
            )
        )

    assert mock_push_remote.call_count == 0, (
        f"push_to_remote was called {mock_push_remote.call_count} time(s) for branch {branch!r}"
    )


# ---------------------------------------------------------------------------
# TS-78-P2: Post-harvest always calls _push_integration_branch
# ---------------------------------------------------------------------------


@given(branch=_feature_branch_strategy)
@settings(suppress_health_check=[HealthCheck.function_scoped_fixture])
def test_always_pushes_develop(branch: str) -> None:
    """Property: for any branch name, _push_integration_branch is always called.

    Test Spec: TS-78-P2
    Requirements: 78-REQ-1.2, 78-REQ-1.E1
    """
    workspace = WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name="test_spec",
        task_group=1,
    )
    repo_root = Path("/tmp/repo")

    with (
        patch(
            "agentfox.workspace.harvest._push_integration_branch",
            new_callable=AsyncMock,
        ) as mock_push_develop,
    ):
        _run(
            post_harvest_integrate(
                repo_root=repo_root,
                workspace=workspace,
                branch="develop",
            )
        )

    mock_push_develop.assert_called_once_with(repo_root, "develop")
