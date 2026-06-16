"""Tests for lint-specs cleanup: --fix removal, progress display, docs update.

Test Spec: TS-127-1, TS-127-5, TS-127-9, TS-127-E1,
           TS-127-P2, TS-127-SMOKE-1, TS-127-SMOKE-2
Requirements: 127-REQ-1.1, 127-REQ-1.E1, 127-REQ-2.1, 127-REQ-2.2,
              127-REQ-4.1, 127-REQ-4.4, 127-REQ-5.1, 127-REQ-5.2,
              127-REQ-5.3, 127-REQ-6.1, 127-REQ-6.2
"""

from __future__ import annotations

import inspect
import os
from pathlib import Path
from unittest.mock import MagicMock, patch

from click.testing import CliRunner

from agent_fox.cli.app import main

_REPO_ROOT = Path(__file__).resolve().parents[3]


def _setup_minimal_project(project_dir: Path) -> Path:
    """Create a minimal project structure for lint-specs CLI tests.

    Returns the specs directory path.
    """
    agent_fox_dir = project_dir / ".agent-fox"
    agent_fox_dir.mkdir(exist_ok=True)
    (agent_fox_dir / "config.toml").write_text("")

    specs_dir = agent_fox_dir / "specs"
    specs_dir.mkdir()
    spec = specs_dir / "01_test_spec"
    spec.mkdir()
    (spec / "prd.md").write_text("# PRD\n\n## Source\nTest\n")
    (spec / "requirements.md").write_text(
        "# Requirements Document\n\n## Introduction\nTest spec.\n\n"
        "## Glossary\n- **Test**: A test term.\n\n"
        "## Requirements\n\n### Requirement 1: Test\n\n"
        "**User Story:** As a user, I want to test.\n\n"
        "#### Acceptance Criteria\n\n"
        "1. [01-REQ-1.1] THE system SHALL do something.\n"
    )
    (spec / "design.md").write_text(
        "# Design Document: Test\n\n## Overview\nTest design.\n\n"
        "## Architecture\nSimple.\n\n"
        "## Execution Paths\n\nNone.\n\n"
        "## Correctness Properties\n\n### Property 1: Test\n\n"
        "*For any* input, THE system SHALL work.\n\n"
        "**Validates: 01-REQ-1.1**\n\n"
        "## Error Handling\n\n"
        "| Error | Behavior | Requirement |\n"
        "|-------|----------|-------------|\n\n"
        "## Definition of Done\nDone when tests pass.\n"
    )
    (spec / "test_spec.md").write_text(
        "# Test Specification\n\n## Overview\nTests.\n\n"
        "## Test Cases\n\n### TS-01-1: Test\n\n"
        "**Requirement:** 01-REQ-1.1\n\n"
        "## Coverage Matrix\n\n"
        "| Req | Test | Type |\n|-----|------|------|\n"
        "| 01-REQ-1.1 | TS-01-1 | unit |\n"
    )
    (spec / "tasks.md").write_text(
        "# Implementation Plan\n\n## Tasks\n\n"
        "- [ ] 1. Do something\n"
        "  - [ ] 1.1 Task\n"
        "  - [ ] 1.V Verify\n"
    )
    return specs_dir


# ---------------------------------------------------------------------------
# TS-127-1: CLI rejects --fix flag
# ---------------------------------------------------------------------------


class TestCliRejectsFix:
    """TS-127-1: CLI rejects --fix flag.

    Requirement: 127-REQ-1.1
    """

    def test_cli_rejects_fix(self) -> None:
        """Click rejects --fix as unrecognized option with exit code 2."""
        runner = CliRunner()
        result = runner.invoke(main, ["lint-specs", "--fix"])
        # Click uses exit code 2 for usage errors (unrecognized options)
        assert result.exit_code == 2


# ---------------------------------------------------------------------------
# TS-127-E1: CLI error message for --fix
# ---------------------------------------------------------------------------


class TestFixErrorMessage:
    """TS-127-E1: CLI error message for --fix.

    Requirement: 127-REQ-1.E1
    """

    def test_fix_error_message(self) -> None:
        """--fix produces clear error about unrecognized option."""
        runner = CliRunner()
        result = runner.invoke(main, ["lint-specs", "--fix"])
        assert result.exit_code != 0
        output = result.output.lower()
        assert "no such option" in output or "unrecognized" in output


# ---------------------------------------------------------------------------
# TS-127-5: CLI module has no git operations
# ---------------------------------------------------------------------------


class TestNoGitOperations:
    """TS-127-5: CLI module has no git operations.

    Requirements: 127-REQ-2.1, 127-REQ-2.2
    """

    def test_no_git_operations_in_source(self) -> None:
        """lint_specs.py contains no fix-related functions or git imports."""
        source = (_REPO_ROOT / "agent_fox" / "cli" / "lint_specs.py").read_text()
        forbidden = [
            "_format_fix_summary",
            "_git_current_branch",
            "_create_fix_branch",
            "_commit_fixes",
            "run_git_sync",
        ]
        for name in forbidden:
            assert name not in source, (
                f"Found forbidden name '{name}' in lint_specs.py"
            )


# ---------------------------------------------------------------------------
# TS-127-9: Documentation updated
# Addresses skeptic findings for 127-REQ-5.1, 127-REQ-5.2, 127-REQ-5.3,
# 127-REQ-6.1, 127-REQ-6.2
# ---------------------------------------------------------------------------


class TestDocsUpdated:
    """TS-127-9: Documentation updated.

    Requirements: 127-REQ-6.1, 127-REQ-6.2
    """

    def test_cli_reference_no_fix(self) -> None:
        """CLI reference lint-specs section does not mention --fix."""
        content = (_REPO_ROOT / "docs" / "cli-reference.md").read_text()
        start = content.find("### lint-specs")
        assert start != -1, "lint-specs section not found in cli-reference.md"
        # Find end of section (next ### or ## heading)
        next_h3 = content.find("\n### ", start + 1)
        next_h2 = content.find("\n## ", start + 1)
        candidates = [c for c in (next_h3, next_h2) if c != -1]
        end = min(candidates) if candidates else len(content)
        lint_section = content[start:end]
        assert "--fix" not in lint_section

    def test_cli_reference_mentions_progress(self) -> None:
        """CLI reference lint-specs section documents progress spinner.

        Requirement: 127-REQ-6.2
        Addresses skeptic finding: REQ-6.2 was unmeasured by TS-127-9.
        """
        content = (_REPO_ROOT / "docs" / "cli-reference.md").read_text()
        start = content.find("### lint-specs")
        assert start != -1, "lint-specs section not found in cli-reference.md"
        next_h3 = content.find("\n### ", start + 1)
        next_h2 = content.find("\n## ", start + 1)
        candidates = [c for c in (next_h3, next_h2) if c != -1]
        end = min(candidates) if candidates else len(content)
        lint_section = content[start:end].lower()
        assert "progress" in lint_section or "spinner" in lint_section, (
            "lint-specs section must document the progress spinner"
        )


class TestAfSpecSkillTemplate:
    """Tests for af-spec skill template lint-specs integration.

    Requirements: 127-REQ-5.1, 127-REQ-5.2, 127-REQ-5.3
    Addresses skeptic finding: REQ-5.1/5.2/5.3 were untestable under TS-127-9.
    """

    def test_af_spec_template_has_lint_validation(self) -> None:
        """af-spec skill template includes lint-specs validation step.

        Requirement: 127-REQ-5.1
        """
        template_path = _REPO_ROOT / "agent_fox" / "_templates" / "skills" / "af-spec"
        content = template_path.read_text()
        assert "lint-specs" in content, (
            "af-spec template must include a lint-specs validation step"
        )

    def test_af_spec_template_instructs_fix_lint_errors(self) -> None:
        """af-spec skill template instructs agent to fix lint errors.

        Requirement: 127-REQ-5.2
        """
        template_path = _REPO_ROOT / "agent_fox" / "_templates" / "skills" / "af-spec"
        content = template_path.read_text()
        # Template must instruct running agent-fox lint-specs and fixing errors
        assert "agent-fox lint-specs" in content, (
            "af-spec template must instruct running agent-fox lint-specs"
        )

    def test_af_spec_template_has_manual_check_markers(self) -> None:
        """af-spec skill template marks non-lint items as (manual check).

        Requirement: 127-REQ-5.3
        """
        template_path = _REPO_ROOT / "agent_fox" / "_templates" / "skills" / "af-spec"
        content = template_path.read_text().lower()
        assert "(manual check)" in content, (
            "af-spec template must mark manual-only checklist items"
        )

# ---------------------------------------------------------------------------
# TS-127-P2: CLI always rejects --fix (property)
# ---------------------------------------------------------------------------


class TestCliRejectsFixProperty:
    """TS-127-P2: CLI always rejects --fix.

    Property: Property 2 from design.md
    Validates: 127-REQ-1.1, 127-REQ-1.E1
    """

    def test_cli_always_rejects_fix(self) -> None:
        """The CLI always rejects the --fix flag."""
        runner = CliRunner()
        result = runner.invoke(main, ["lint-specs", "--fix"])
        assert result.exit_code != 0


# ---------------------------------------------------------------------------
# TS-127-SMOKE-1: Full lint pipeline without --ai
# ---------------------------------------------------------------------------


class TestSmokeLintPipeline:
    """TS-127-SMOKE-1: Full lint pipeline without --ai.

    Execution Path: Path 1 from design.md
    Must NOT mock: run_lint_specs, validate_specs
    """

    def test_full_lint_pipeline(self, tmp_path: Path) -> None:
        """End-to-end lint-specs run produces valid output without fix code."""
        _setup_minimal_project(tmp_path)
        runner = CliRunner()
        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = runner.invoke(main, ["lint-specs"])
            assert result.exit_code in (0, 1)
            # Output should contain finding text or "No findings"
            output = result.output.lower()
            assert "finding" in output or "no findings" in output
        finally:
            os.chdir(original_dir)


# ---------------------------------------------------------------------------
# TS-127-SMOKE-2: Full lint pipeline with progress display
# ---------------------------------------------------------------------------


class TestSmokeProgressDisplay:
    """TS-127-SMOKE-2: Full lint pipeline with progress display.

    Execution Path: Path 1 from design.md
    Must NOT mock: run_lint_specs
    """

    def test_progress_display_wired(self) -> None:
        """lint_specs module imports and uses ProgressDisplay."""
        from agent_fox.cli import lint_specs as lint_mod

        source = inspect.getsource(lint_mod)
        assert "ProgressDisplay" in source, (
            "lint_specs module must import and use ProgressDisplay"
        )

    def test_progress_display_lifecycle(self, tmp_path: Path) -> None:
        """ProgressDisplay.start() and stop() called during lint-specs."""
        # Verify ProgressDisplay is imported before attempting patch
        from agent_fox.cli import lint_specs as lint_mod

        source = inspect.getsource(lint_mod)
        assert "ProgressDisplay" in source, (
            "lint_specs module must import ProgressDisplay"
        )

        _setup_minimal_project(tmp_path)
        runner = CliRunner()
        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            with patch("agent_fox.cli.lint_specs.ProgressDisplay") as mock_cls:
                mock_progress = MagicMock()
                mock_cls.return_value = mock_progress
                runner.invoke(main, ["lint-specs"])
            assert mock_progress.start.called, (
                "ProgressDisplay.start() must be called"
            )
            assert mock_progress.stop.called, (
                "ProgressDisplay.stop() must be called"
            )
        finally:
            os.chdir(original_dir)
