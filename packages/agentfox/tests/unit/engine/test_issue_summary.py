"""Unit tests for issue_summary module.

Test Spec: TS-108-1 through TS-108-17, TS-108-E1, TS-108-E2,
           TS-NS-1 through TS-NS-4
Requirements: 108-REQ-1 through 108-REQ-6, NS-REQ-1 through NS-REQ-4
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

# Helper to build a prd.md with YAML frontmatter and optional body.
_FM_TEMPLATE = "---\n{fields}\n---\n{body}"


def _make_prd(path: Path, *, source: str | None = None, body: str = "") -> Path:
    """Write a prd.md with frontmatter ``source`` and optional body text."""
    lines = ['title: "Test PRD"']
    if source is not None:
        lines.append(f'source: "{source}"')
    path.write_text(_FM_TEMPLATE.format(fields="\n".join(lines), body=body))
    return path


# ---------------------------------------------------------------------------
# TS-NS-1, TS-NS-2, TS-NS-3
# Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3
# ---------------------------------------------------------------------------


class TestParseSourceUrl:
    """Tests for parse_source_url() — reads frontmatter ``source`` field."""

    def test_github_url_in_frontmatter_returns_source_issue(self, tmp_path: Path) -> None:
        """TS-NS-1: Frontmatter source with valid GitHub issue URL.

        Requirements: NS-REQ-1
        """
        from agentfox.engine.issue_summary import SourceIssue, parse_source_url

        prd_path = tmp_path / "prd.md"
        _make_prd(prd_path, source="https://github.com/owner/repo/issues/42")

        result = parse_source_url(prd_path)

        assert result is not None
        assert isinstance(result, SourceIssue)
        assert result.forge == "github"
        assert result.owner == "owner"
        assert result.repo == "repo"
        assert result.issue_number == 42

    def test_interactive_source_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-2: Frontmatter source='interactive' → None.

        Requirements: NS-REQ-2
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        _make_prd(prd_path, source="interactive")

        result = parse_source_url(prd_path)

        assert result is None

    def test_non_github_url_source_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-2: Frontmatter source with non-GitHub-issue-URL string → None.

        Requirements: NS-REQ-2
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        _make_prd(prd_path, source="Input provided by user via interactive prompt")

        result = parse_source_url(prd_path)

        assert result is None

    def test_missing_prd_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-3: prd.md does not exist → None.

        Requirements: NS-REQ-3
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"  # does not exist
        assert not prd_path.exists()

        result = parse_source_url(prd_path)

        assert result is None

    def test_no_frontmatter_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-3: prd.md has no YAML frontmatter delimiters → None.

        Requirements: NS-REQ-3
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        prd_path.write_text("# PRD\n\nNo frontmatter here.\n")

        result = parse_source_url(prd_path)

        assert result is None

    def test_frontmatter_missing_source_key_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-3: Frontmatter present but no source key → None.

        Requirements: NS-REQ-3
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        _make_prd(prd_path)  # no source kwarg → no source field

        result = parse_source_url(prd_path)

        assert result is None

    def test_malformed_frontmatter_returns_none(self, tmp_path: Path) -> None:
        """TS-NS-3: Malformed frontmatter → None without raising.

        Requirements: NS-REQ-3
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        prd_path.write_text("---\n[invalid yaml\n---\nBody text\n")

        result = parse_source_url(prd_path)

        assert result is None

    def test_pure_function_no_exceptions(self, tmp_path: Path) -> None:
        """parse_source_url is a pure function — never raises.

        Requirements: NS-REQ-3
        """
        from agentfox.engine.issue_summary import SourceIssue, parse_source_url

        # Variant 1: missing file
        missing = tmp_path / "nonexistent.md"

        # Variant 2: empty file
        empty = tmp_path / "empty.md"
        empty.write_text("")

        # Variant 3: frontmatter with file path source
        file_src = tmp_path / "file_path_src.md"
        _make_prd(file_src, source="/some/file/path.txt")

        # Variant 4: frontmatter with GitHub URL
        github_src = tmp_path / "github_src.md"
        _make_prd(github_src, source="https://github.com/org/repo/issues/1")

        # Variant 5: frontmatter with unknown URL format
        unknown_src = tmp_path / "unknown_src.md"
        _make_prd(unknown_src, source="https://linear.app/team/issue/123")

        variants = [missing, empty, file_src, github_src, unknown_src]

        for path in variants:
            result = parse_source_url(path)
            assert result is None or isinstance(result, SourceIssue), (
                f"Expected None or SourceIssue for {path.name}, got {type(result)}"
            )


# ---------------------------------------------------------------------------
# Edge cases for frontmatter-based parse_source_url
# ---------------------------------------------------------------------------


class TestParseSourceUrlEdgeCases:
    """Edge case tests for parse_source_url() with frontmatter."""

    def test_empty_source_value_returns_none(self, tmp_path: Path) -> None:
        """Frontmatter source is empty string → None."""
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        _make_prd(prd_path, source="")

        result = parse_source_url(prd_path)

        assert result is None

    def test_source_with_body_section_ignored(self, tmp_path: Path) -> None:
        """Only frontmatter is authoritative; ## Source body section is ignored.

        A prd.md with the URL only in a ## Source body section (not in
        frontmatter) must return None — frontmatter is the single source
        of truth per spec format v1.3.
        """
        from agentfox.engine.issue_summary import parse_source_url

        prd_path = tmp_path / "prd.md"
        prd_path.write_text(
            '---\ntitle: "Test"\nsource: "interactive"\n---\n'
            "## Source\n\nSource: https://github.com/owner/repo/issues/99\n"
        )

        result = parse_source_url(prd_path)

        # Must return None because frontmatter source is "interactive",
        # even though the body has a valid GitHub URL.
        assert result is None


# ---------------------------------------------------------------------------
# TS-108-6, TS-108-7
# Requirements: 108-REQ-3.1, 108-REQ-3.2, 108-REQ-3.3, 108-REQ-3.4
# ---------------------------------------------------------------------------


class TestBuildSummaryComment:
    """Tests for build_summary_comment() function."""

    def test_includes_required_fields(self, tmp_path: Path) -> None:
        """TS-108-6: build_summary_comment includes required fields.

        Requirements: 108-REQ-3.1, 108-REQ-3.2, 108-REQ-3.3, 108-REQ-3.4
        """
        from unittest.mock import patch

        from agentfox.engine.issue_summary import build_summary_comment
        from agentfox.spec.types import SubtaskDef, TaskGroupDef

        spec_name = "108_issue_session_summary"
        commit_sha = "abc123def"

        spec_dir = tmp_path / "108_issue_session_summary"
        spec_dir.mkdir()

        mock_groups = [
            TaskGroupDef(
                number=1,
                title="Write failing tests",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="1.1", title="t1", completed=True),),
                body="",
                archetype=None,
            ),
            TaskGroupDef(
                number=2,
                title="Implement feature",
                optional=False,
                completed=True,
                subtasks=(SubtaskDef(id="2.1", title="t2", completed=True),),
                body="",
                archetype=None,
            ),
        ]

        with patch("agentfox.spec.parser.parse_tasks", return_value=mock_groups):
            comment = build_summary_comment(spec_name, commit_sha, spec_dir, "main")

        assert spec_name in comment, "Comment must contain spec name"
        assert commit_sha in comment, "Comment must contain commit SHA"
        assert "Write failing tests" in comment, "Comment must contain group 1 title"
        assert "Implement feature" in comment, "Comment must contain group 2 title"
        assert "*Auto-generated by agent-fox.*" in comment, "Comment must contain footer"

    def test_with_missing_spec_dir_still_has_required_fields(self, tmp_path: Path) -> None:
        """TS-108-7: build_summary_comment with non-existent spec directory.

        Requirements: 108-REQ-3.1, 108-REQ-3.2, 108-REQ-3.4
        """
        from agentfox.engine.issue_summary import build_summary_comment

        spec_name = "my_spec"
        commit_sha = "sha123"
        spec_dir = tmp_path / "nonexistent_spec"  # does not exist

        # Must not raise
        comment = build_summary_comment(spec_name, commit_sha, spec_dir, "main")

        assert spec_name in comment, "Comment must still contain spec name"
        assert commit_sha in comment, "Comment must still contain commit SHA"
        assert "*Auto-generated by agent-fox.*" in comment, "Comment must still contain footer"


# ---------------------------------------------------------------------------
# TS-108-8, TS-108-9, TS-108-10, TS-108-11
# Requirements: 108-REQ-2.1, 108-REQ-2.2, 108-REQ-2.E1, 108-REQ-4.1, 108-REQ-4.E1
# ---------------------------------------------------------------------------


class TestPostIssueSummaries:
    """Tests for post_issue_summaries() function."""

    def _make_spec_dir(self, base: Path, spec_name: str, issue_url: str) -> Path:
        """Create a minimal spec directory with prd.md (frontmatter source) and tasks.md."""
        spec_dir = base / spec_name
        spec_dir.mkdir(parents=True)
        (spec_dir / "prd.md").write_text(
            f'---\ntitle: "Test"\nsource: "{issue_url}"\n---\n# PRD\n'
        )
        (spec_dir / "tasks.md").write_text("- [x] 1. Implement feature\n")
        return spec_dir

    def _make_mock_platform(self, forge_type: str = "github", issue_labels: tuple[str, ...] = ()) -> MagicMock:
        """Create a mock platform with the given forge type."""
        from afissues.protocol import IssueResult

        platform = MagicMock()
        platform.forge_type = forge_type
        platform.add_issue_comment = AsyncMock()
        platform.assign_label = AsyncMock()
        platform.get_issue = AsyncMock(
            return_value=IssueResult(number=42, title="test", html_url="", labels=issue_labels),
        )
        return platform

    def _mock_git_sha(self, sha: str = "abc123") -> MagicMock:
        """Return a mock subprocess result for git rev-parse develop."""
        mock_result = MagicMock()
        mock_result.returncode = 0
        mock_result.stdout = f"{sha}\n"
        return mock_result

    @pytest.mark.asyncio
    async def test_posts_comment_for_completed_spec(self, tmp_path: Path) -> None:
        """TS-108-8: post_issue_summaries posts comment for completed spec.

        Requirements: 108-REQ-4.1, 108-REQ-2.1, 108-REQ-2.2
        """
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")

        with patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        platform.add_issue_comment.assert_called_once()
        call_args = platform.add_issue_comment.call_args
        issue_number = call_args[0][0]
        body = call_args[0][1]
        assert issue_number == 42
        assert spec_name in body
        assert spec_name in posted

    @pytest.mark.asyncio
    async def test_skips_already_posted_specs(self, tmp_path: Path) -> None:
        """TS-108-9: post_issue_summaries skips already-posted specs.

        Requirements: 108-REQ-2.E1
        """
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")

        with patch("subprocess.run", return_value=self._mock_git_sha()):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted={spec_name},  # already posted
                repo_root=tmp_path,
            )

        platform.add_issue_comment.assert_not_called()
        assert len(posted) == 0

    @pytest.mark.asyncio
    async def test_skips_spec_without_source_url(self, tmp_path: Path) -> None:
        """TS-108-10: post_issue_summaries skips spec without a valid source URL.

        Requirements: 108-REQ-1.E1, 108-REQ-1.E2
        """
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        spec_dir = specs_dir / spec_name
        spec_dir.mkdir(parents=True)
        # Non-URL source in frontmatter
        (spec_dir / "prd.md").write_text(
            '---\ntitle: "Test"\nsource: "interactive"\n---\n# PRD\n'
        )
        (spec_dir / "tasks.md").write_text("- [x] 1. Feature\n")

        platform = self._make_mock_platform()

        with patch("subprocess.run", return_value=self._mock_git_sha()):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        platform.add_issue_comment.assert_not_called()
        assert len(posted) == 0

    @pytest.mark.asyncio
    async def test_handles_comment_posting_failure(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """TS-108-11: post_issue_summaries handles add_issue_comment failure.

        Requirements: 108-REQ-4.E1
        """
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")
        platform.add_issue_comment = AsyncMock(side_effect=RuntimeError("network error"))

        with caplog.at_level(logging.WARNING), patch("subprocess.run", return_value=self._mock_git_sha()):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        # Must not propagate exception
        # Spec name must NOT be in posted (posting failed)
        assert spec_name not in posted
        # Warning must be logged
        assert any(r.levelno >= logging.WARNING for r in caplog.records)

    @pytest.mark.asyncio
    async def test_assigns_implemented_label_on_success(self, tmp_path: Path) -> None:
        """Issue #636: assign_label called with af:implemented after successful post."""
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")

        with patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        assert spec_name in posted
        platform.assign_label.assert_called_once_with(42, "af:implemented")

    @pytest.mark.asyncio
    async def test_label_failure_does_not_block_posting(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """Issue #636: assign_label failure is caught; spec still in posted set."""
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")
        platform.assign_label = AsyncMock(side_effect=RuntimeError("label API error"))

        with caplog.at_level(logging.WARNING), patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        assert spec_name in posted
        assert any("af:implemented" in r.message for r in caplog.records if r.levelno >= logging.WARNING)

    @pytest.mark.asyncio
    async def test_label_not_assigned_when_comment_fails(
        self, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Issue #636: assign_label is NOT called when add_issue_comment fails."""
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")
        platform.add_issue_comment = AsyncMock(side_effect=RuntimeError("network error"))

        with caplog.at_level(logging.WARNING), patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        platform.assign_label.assert_not_called()

    @pytest.mark.asyncio
    async def test_skips_when_implemented_label_already_present(self, tmp_path: Path) -> None:
        """Issue #648: skip posting when af:implemented label is already on the issue."""
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(
            forge_type="github",
            issue_labels=("bug", "af:implemented"),
        )

        with patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        assert spec_name in posted
        platform.add_issue_comment.assert_not_called()
        platform.assign_label.assert_not_called()

    @pytest.mark.asyncio
    async def test_posts_when_get_issue_fails(self, tmp_path: Path) -> None:
        """Issue #648: fall through to posting if get_issue raises."""
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        self._make_spec_dir(specs_dir, spec_name, "https://github.com/owner/repo/issues/42")

        platform = self._make_mock_platform(forge_type="github")
        platform.get_issue = AsyncMock(side_effect=RuntimeError("API error"))

        with patch("subprocess.run", return_value=self._mock_git_sha("abc123")):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        assert spec_name in posted
        platform.add_issue_comment.assert_called_once()


# ---------------------------------------------------------------------------
# TS-108-17
# Requirements: 108-REQ-4.E2
# ---------------------------------------------------------------------------


class TestPostIssueSummariesForgeMismatch:
    """Tests for forge type mismatch handling in post_issue_summaries()."""

    @pytest.mark.asyncio
    async def test_skips_forge_mismatch(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """TS-108-17: post_issue_summaries skips when forge type doesn't match.

        Requirements: 108-REQ-4.E2
        """
        from agentfox.engine.issue_summary import post_issue_summaries

        spec_name = "108_my_spec"
        specs_dir = tmp_path / "specs"
        spec_dir = specs_dir / spec_name
        spec_dir.mkdir(parents=True)
        # GitHub source URL in frontmatter — forge = "github"
        (spec_dir / "prd.md").write_text(
            '---\ntitle: "Test"\nsource: "https://github.com/owner/repo/issues/42"\n---\n# PRD\n'
        )
        (spec_dir / "tasks.md").write_text("- [x] 1. Feature\n")

        # Platform is NOT github (simulating a GitLab platform)
        platform = MagicMock()
        platform.forge_type = "gitlab"  # mismatch with "github"
        platform.add_issue_comment = AsyncMock()

        mock_result = MagicMock()
        mock_result.returncode = 0
        mock_result.stdout = "abc123\n"

        with caplog.at_level(logging.INFO), patch("subprocess.run", return_value=mock_result):
            posted = await post_issue_summaries(
                platform=platform,
                specs_dir=specs_dir,
                completed_specs={spec_name},
                already_posted=set(),
                repo_root=tmp_path,
            )

        platform.add_issue_comment.assert_not_called()
        assert len(posted) == 0
        # Info-level message about forge mismatch must be logged
        assert any(r.levelno == logging.INFO for r in caplog.records)


# ---------------------------------------------------------------------------
# TS-108-15, TS-108-16
# Requirements: 108-REQ-6.1, 108-REQ-6.E1
# ---------------------------------------------------------------------------


class TestGetDevelopHead:
    """Tests for _get_integration_head() function."""

    def test_returns_sha_on_success(self, tmp_path: Path) -> None:
        """TS-108-15: _get_integration_head returns SHA on successful git call.

        Requirements: 108-REQ-6.1
        """
        from agentfox.engine.issue_summary import _get_integration_head

        mock_result = MagicMock()
        mock_result.returncode = 0
        mock_result.stdout = "abc123def456\n"

        with patch("subprocess.run", return_value=mock_result):
            sha = _get_integration_head(tmp_path, "main")

        assert sha == "abc123def456"

    def test_returns_unknown_on_failure(self, tmp_path: Path) -> None:
        """TS-108-16: _get_integration_head returns 'unknown' when git fails.

        Requirements: 108-REQ-6.E1
        """
        from agentfox.engine.issue_summary import _get_integration_head

        mock_result = MagicMock()
        mock_result.returncode = 1
        mock_result.stdout = ""

        with patch("subprocess.run", return_value=mock_result):
            sha = _get_integration_head(tmp_path, "main")

        assert sha == "unknown"
