"""Tests for lint.py cleanup: fix removal, progress callback, fixers deletion.

Test Spec: TS-127-2, TS-127-3, TS-127-4, TS-127-6, TS-127-7, TS-127-8,
           TS-127-E2, TS-127-P1, TS-127-P3, TS-127-P4
Requirements: 127-REQ-1.2, 127-REQ-1.3, 127-REQ-1.4, 127-REQ-3.1,
              127-REQ-3.2, 127-REQ-4.2, 127-REQ-4.3, 127-REQ-4.E1
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from unittest.mock import MagicMock

import pytest

from agent_fox.spec.lint import LintResult, run_lint_specs

_REPO_ROOT = Path(__file__).resolve().parents[3]


def _create_spec_dir(specs_dir: Path) -> None:
    """Create a minimal valid spec directory for testing run_lint_specs."""
    spec = specs_dir / "01_test"
    spec.mkdir(parents=True, exist_ok=True)
    (spec / "prd.md").write_text("# PRD\n\n## Source\nTest\n")
    # v1.2 format markers for discover_specs format detection
    (spec / "requirements.json").write_text("{}")
    (spec / "tasks.json").write_text("{}")
    (spec / "requirements.md").write_text(
        "# Requirements Document\n\n## Introduction\nTest.\n\n"
        "## Glossary\n- **Test**: A test term.\n\n"
        "## Requirements\n\n### Requirement 1: Test\n\n"
        "**User Story:** As a user, I want to test.\n\n"
        "#### Acceptance Criteria\n\n"
        "1. [01-REQ-1.1] THE system SHALL do something.\n"
    )
    (spec / "design.md").write_text(
        "# Design Document: Test\n\n## Overview\nTest.\n\n"
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


# ---------------------------------------------------------------------------
# TS-127-2: run_lint_specs has no fix parameter
# ---------------------------------------------------------------------------


class TestNoFixParameter:
    """TS-127-2: run_lint_specs has no fix parameter.

    Requirement: 127-REQ-1.2
    """

    def test_run_lint_specs_rejects_fix_kwarg(self, tmp_path: Path) -> None:
        """run_lint_specs() raises TypeError for fix= keyword."""
        specs_dir = tmp_path / "specs"
        specs_dir.mkdir()
        with pytest.raises(TypeError):
            run_lint_specs(specs_dir, fix=True)  # type: ignore[call-arg]


# ---------------------------------------------------------------------------
# TS-127-3: LintResult has no fix_results field
# ---------------------------------------------------------------------------


class TestNoFixResults:
    """TS-127-3: LintResult has no fix_results field.

    Requirement: 127-REQ-1.3
    """

    def test_lint_result_no_fix_results(self) -> None:
        """LintResult instances do not have a fix_results attribute."""
        result = LintResult()
        assert not hasattr(result, "fix_results")


# ---------------------------------------------------------------------------
# TS-127-4: fixers package deleted
# ---------------------------------------------------------------------------


class TestFixersDeleted:
    """TS-127-4: fixers package deleted.

    Requirement: 127-REQ-1.4
    """

    def test_fixers_directory_does_not_exist(self) -> None:
        """agent_fox/spec/fixers/ directory does not exist."""
        fixers_dir = _REPO_ROOT / "agent_fox" / "spec" / "fixers"
        assert not fixers_dir.exists(), (
            f"Fixers directory must be deleted: {fixers_dir}"
        )


# ---------------------------------------------------------------------------
# TS-127-6: Backing module has no fix dispatch
# ---------------------------------------------------------------------------


class TestNoFixDispatch:
    """TS-127-6: Backing module has no fix dispatch.

    Requirements: 127-REQ-3.1, 127-REQ-3.2
    """

    def test_no_fix_dispatch_in_lint_py(self) -> None:
        """lint.py has no fix dispatch functions or fixer imports."""
        source = (_REPO_ROOT / "agent_fox" / "spec" / "lint.py").read_text()
        _FIXERS_PKG = "agent_fox.spec." + "fixers"
        forbidden = [
            "_apply_ai_fixes",
            "_build_known_specs",
            _FIXERS_PKG,
        ]
        for name in forbidden:
            assert name not in source, (
                f"Found forbidden name '{name}' in lint.py"
            )


# ---------------------------------------------------------------------------
# TS-127-7: Progress callback invoked at phases
# ---------------------------------------------------------------------------


class TestProgressCallback:
    """TS-127-7: Progress callback invoked at phases.

    Requirements: 127-REQ-4.2, 127-REQ-4.3
    """

    def test_progress_callback_invoked(self, tmp_path: Path) -> None:
        """run_lint_specs calls progress_callback at each major phase."""
        specs_dir = tmp_path / "specs"
        _create_spec_dir(specs_dir)
        callback = MagicMock()
        run_lint_specs(specs_dir, progress_callback=callback)
        # At least 2 calls: discovery + validation
        assert callback.call_count >= 2


# ---------------------------------------------------------------------------
# TS-127-8: Progress callback None is safe
# ---------------------------------------------------------------------------


class TestProgressCallbackNoneSafe:
    """TS-127-8: Progress callback None is safe.

    Requirement: 127-REQ-4.E1
    """

    def test_progress_callback_none_safe(self, tmp_path: Path) -> None:
        """Omitting progress_callback works without error."""
        specs_dir = tmp_path / "specs"
        _create_spec_dir(specs_dir)
        result = run_lint_specs(specs_dir)
        assert isinstance(result, LintResult)


# ---------------------------------------------------------------------------
# TS-127-E2: Progress callback None behaves identically
# ---------------------------------------------------------------------------


class TestProgressCallbackNoneIdentical:
    """TS-127-E2: Progress callback None behaves identically.

    Requirement: 127-REQ-4.E1
    """

    def test_default_and_none_identical(self, tmp_path: Path) -> None:
        """Omitting callback and passing None produce same results."""
        specs_dir = tmp_path / "specs"
        _create_spec_dir(specs_dir)
        result_default = run_lint_specs(specs_dir)
        result_none = run_lint_specs(specs_dir, progress_callback=None)
        assert result_default.exit_code == result_none.exit_code
        assert len(result_default.findings) == len(result_none.findings)


# ---------------------------------------------------------------------------
# TS-127-P1: No fixer imports anywhere (property)
# ---------------------------------------------------------------------------


class TestNoFixerImportsProperty:
    """TS-127-P1: No fixer imports in any tracked file.

    Property: Property 1 from design.md
    Validates: 127-REQ-1.4, 127-REQ-3.2
    """

    def test_no_fixer_imports_in_tracked_files(self) -> None:
        """No git-tracked .py file imports from the fixers package."""
        # Use string concatenation so this test file itself does not
        # contain the target string as a contiguous literal.
        _FIXERS_PKG = "agent_fox.spec." + "fixers"
        result = subprocess.run(
            ["git", "ls-files"],
            capture_output=True,
            text=True,
            check=True,
            cwd=_REPO_ROOT,
        )
        py_files = [
            f for f in result.stdout.splitlines() if f.endswith(".py")
        ]
        violations: list[str] = []
        for rel_path in py_files:
            full_path = _REPO_ROOT / rel_path
            if not full_path.is_file():
                continue
            content = full_path.read_text()
            if _FIXERS_PKG in content:
                violations.append(rel_path)
        assert not violations, (
            f"Files importing from fixers package: {violations}"
        )


# ---------------------------------------------------------------------------
# TS-127-P3: LintResult has no fix_results (property)
# ---------------------------------------------------------------------------


class TestLintResultNoFixResultsProperty:
    """TS-127-P3: LintResult has no fix_results.

    Property: Property 3 from design.md
    Validates: 127-REQ-1.3
    """

    def test_lint_result_never_has_fix_results(self) -> None:
        """LintResult instances never have fix_results attribute."""
        result = LintResult()
        assert not hasattr(result, "fix_results")


# ---------------------------------------------------------------------------
# TS-127-P4: Progress callback optional (property)
# ---------------------------------------------------------------------------


class TestProgressCallbackOptionalProperty:
    """TS-127-P4: Progress callback optional.

    Property: Property 4 from design.md
    Validates: 127-REQ-4.2, 127-REQ-4.E1
    """

    def test_works_with_and_without_callback(self, tmp_path: Path) -> None:
        """run_lint_specs works identically with and without callback."""
        specs_dir = tmp_path / "specs"
        _create_spec_dir(specs_dir)
        result_no_cb = run_lint_specs(specs_dir)
        result_with_cb = run_lint_specs(
            specs_dir, progress_callback=lambda s: None
        )
        assert type(result_no_cb) is type(result_with_cb)
