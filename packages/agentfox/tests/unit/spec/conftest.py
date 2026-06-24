"""Fixtures for spec discovery and parser tests.

Creates temporary .specs/ directories with sample tasks.md and prd.md files.
"""

from __future__ import annotations

from pathlib import Path

import pytest

# -- Sample tasks.md content ------------------------------------------------

TASKS_MD_STANDARD = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. Write failing tests
  - [ ] 1.1 Create test fixtures
  - [ ] 1.2 Write unit tests
  - [ ] 1.3 Write integration tests

- [ ] 2. Implement core module
  - [ ] 2.1 Create data models
  - [ ] 2.2 Add validation
"""

TASKS_MD_WITH_OPTIONAL = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. Write failing tests
  - [ ] 1.1 Create test fixtures

- [ ] 2. Implement core module
  - [ ] 2.1 Create data models

- [ ] * 3. Polish and cleanup
  - [ ] 3.1 Add docstrings
  - [ ] 3.2 Refactor utilities

- [ ] 4. Final integration
"""

TASKS_MD_NON_CONTIGUOUS = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. First task
  - [ ] 1.1 Subtask A

- [ ] 3. Third task
  - [ ] 3.1 Subtask B

- [ ] 5. Fifth task
  - [ ] 5.1 Subtask C
"""

TASKS_MD_EMPTY = """\
# Tasks

No items here.
"""

TASKS_MD_COMPLETED = """\
# Implementation Plan: Test Spec

## Tasks

- [x] 1. Completed task
  - [x] 1.1 Done subtask

- [ ] 2. Pending task
"""

# -- Sample prd.md content --------------------------------------------------

PRD_MD_WITH_DEPS = """\
# Product Requirements: Beta Spec

## Dependencies

| This Spec | Depends On | What It Uses |
|-----------|-----------|--------------|
| 02_beta | 01_alpha | Core foundation types |
"""

PRD_MD_NO_DEPS = """\
# Product Requirements: Alpha Spec

## Overview

This is the first specification with no dependencies.
"""

PRD_MD_ALT_FORMAT = """\
# PRD: CLI Banner Enhancement

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 01_core_foundation | 4 | 1 | Imports CLI framework, theme system |
| 03_session | 3 | 2 | Uses session context for banner data |
"""

PRD_MD_ALT_FORMAT_SINGLE = """\
# PRD: Init Settings

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 01_core_foundation | 3 | 1 | Extends the init command implemented in group 3 |
"""

# -- TS-F3 fixtures: tasks.md with N.V verification subtasks ----------------

TASKS_MD_WITH_VERIFY = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. Write failing tests
  - [ ] 1.1 Create test fixtures
  - [ ] 1.2 Write unit tests
  - [x] 1.V Verify task group 1
"""

TASKS_MD_WITH_VERIFY_AND_NUMERIC = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. Write failing tests
  - [ ] 1.1 Create test fixtures
  - [ ] 1.2 Write unit tests
  - [x] 1.V Verify task group 1
"""

TASKS_MD_WITH_UNKNOWN_SUFFIX = """\
# Implementation Plan: Test Spec

## Tasks

- [ ] 1. Write failing tests
  - [ ] 1.1 Create test fixtures
  - [ ] 1.X Some unknown step
"""

# -- TS-F3 fixtures: prd.md with alt table referencing bad specs/groups -----

PRD_MD_ALT_BAD_SPEC = """\
# PRD: Test

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 99_nonexistent | 1 | 1 | Does not exist |
"""

PRD_MD_ALT_BAD_FROM_GROUP = """\
# PRD: Test

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 01_core_foundation | 7 | 1 | Group 7 does not exist in 01_core_foundation |
"""

PRD_MD_ALT_BAD_TO_GROUP = """\
# PRD: Test

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 01_core_foundation | 1 | 99 | Group 99 does not exist in current spec |
"""

PRD_MD_BOTH_FORMATS_BROKEN = """\
# PRD: Test

## Dependencies (standard)

| This Spec | Depends On | What It Uses |
|-----------|-----------|--------------|
| test_spec | 99_missing_std | From standard table |

## Dependencies (alternative)

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 99_missing_alt | 1 | 1 | From alt table |
"""


# -- Fixture: specs directory with multiple specs, all with tasks.md --------


@pytest.fixture
def specs_dir_sorted(tmp_path: Path) -> Path:
    """Create .specs/ with 03_foo, 01_bar, 02_baz, each with v1.2 artifacts.

    Used by TS-02-1 (sorted discovery).
    Uses v1.2 format (requirements.json + tasks.json) so discover_specs
    includes them after the 132 format filter.
    """
    specs_dir = tmp_path / ".specs"
    specs_dir.mkdir()

    for name in ["03_foo", "01_bar", "02_baz"]:
        spec = specs_dir / name
        spec.mkdir()
        (spec / "requirements.json").write_text("{}")
        (spec / "tasks.json").write_text("{}")
        (spec / "tasks.md").write_text(TASKS_MD_STANDARD)

    return specs_dir


@pytest.fixture
def specs_dir_two_specs(tmp_path: Path) -> Path:
    """Create .specs/ with 01_alpha and 02_beta, each with v1.2 artifacts.

    Used by TS-02-2 (filter) and TS-02-E2 (filter miss).
    Uses v1.2 format (requirements.json + tasks.json) so discover_specs
    includes them after the 132 format filter.
    """
    specs_dir = tmp_path / ".specs"
    specs_dir.mkdir()

    for name in ["01_alpha", "02_beta"]:
        spec = specs_dir / name
        spec.mkdir()
        (spec / "requirements.json").write_text("{}")
        (spec / "tasks.json").write_text("{}")
        (spec / "tasks.md").write_text(TASKS_MD_STANDARD)

    return specs_dir


@pytest.fixture
def specs_dir_missing_tasks(tmp_path: Path) -> Path:
    """Create .specs/ with 01_alpha (no tasks.json) and 02_beta (with tasks.json).

    Used by TS-02-E3 (spec folder without tasks file).
    Both are v1.2 format (have requirements.json). has_tasks checks
    tasks.json for v1.2 specs.
    """
    specs_dir = tmp_path / ".specs"
    specs_dir.mkdir()

    # 01_alpha: v1.2 format, no tasks.json
    alpha = specs_dir / "01_alpha"
    alpha.mkdir()
    (alpha / "requirements.json").write_text("{}")

    # 02_beta: v1.2 format, has tasks.json
    beta = specs_dir / "02_beta"
    beta.mkdir()
    (beta / "requirements.json").write_text("{}")
    (beta / "tasks.json").write_text("{}")
    (beta / "tasks.md").write_text(TASKS_MD_STANDARD)

    return specs_dir
