"""Tests for git ref name validation.

Regression tests for GitHub issue #189: Git argument injection via
unvalidated branch/ref names.
"""

from __future__ import annotations

import pytest
from agentfox.core.errors import WorkspaceError
from agentfox.workspace.git import validate_ref_name


class TestValidateRefNameRejectsInvalid:
    """Ref names that could cause argument injection are rejected."""

    def test_rejects_leading_dash(self) -> None:
        """Branch names starting with '-' are rejected."""
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("--strategy=ours")

    def test_rejects_single_dash(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("-x")

    def test_rejects_double_dash_flag(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("--allow-unrelated-histories")

    def test_rejects_empty(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("")

    def test_rejects_space(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("my branch")

    def test_rejects_colon(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("refs:heads")

    def test_rejects_tilde(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("branch~1")

    def test_rejects_caret(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("branch^2")

    def test_rejects_double_dot(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("main..develop")

    def test_rejects_backslash(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("path\\branch")

    def test_rejects_at_brace(self) -> None:
        with pytest.raises(WorkspaceError, match="Invalid git ref name"):
            validate_ref_name("branch@{upstream}")


class TestValidateRefNameAcceptsValid:
    """Valid git ref names are accepted without error."""

    def test_simple_name(self) -> None:
        validate_ref_name("main")

    def test_feature_branch(self) -> None:
        validate_ref_name("feature/my-feature")

    def test_with_dots(self) -> None:
        validate_ref_name("release/v2.5.0")

    def test_with_underscores(self) -> None:
        validate_ref_name("feature/my_feature_branch")

    def test_worktree_branch(self) -> None:
        validate_ref_name("feature/03_session/1")

    def test_origin_prefix(self) -> None:
        validate_ref_name("origin/develop")

    def test_returns_name(self) -> None:
        """validate_ref_name returns the validated name for chaining."""
        assert validate_ref_name("develop") == "develop"

    def test_at_sign_without_brace(self) -> None:
        """@ alone (without {) is valid in git ref names."""
        validate_ref_name("user@branch")
