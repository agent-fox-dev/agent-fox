"""Workspace operations: re-exports from focused submodules.

Re-exports commonly used symbols from:
- agent_fox.workspace.git (low-level Git wrappers)
- agent_fox.workspace.integration (integration branch management)
- agent_fox.workspace.worktree (worktree lifecycle)

For less commonly used git helpers (create_branch, delete_branch,
etc.), import directly from ``agent_fox.workspace.git``.
"""

from agentfox.workspace.git import (  # noqa: F401
    abort_rebase,
    checkout_branch,
    detect_default_branch,
    fetch_remote,
    get_changed_files,
    get_remote_url,
    has_new_commits,
    local_branch_exists,
    merge_fast_forward,
    push_to_remote,
    rebase_onto,
    run_git,
)
from agentfox.workspace.integration import (  # noqa: F401
    _sync_integration_with_remote,
    ensure_integration_branch,
)
from agentfox.workspace.worktree import (  # noqa: F401
    WorkspaceInfo,
    create_worktree,
    destroy_worktree,
)
