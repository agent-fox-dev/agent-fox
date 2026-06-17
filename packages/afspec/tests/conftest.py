"""Shared test fixtures for afspec tests."""

from __future__ import annotations

from pathlib import Path

import pytest

GOLDEN_DIR = Path(__file__).parent / "golden"
VALID_SPEC_DIR = GOLDEN_DIR / "valid_spec"
DRAFT_SPEC_DIR = GOLDEN_DIR / "draft_spec"


@pytest.fixture
def valid_spec_dir() -> Path:
    """Path to the golden valid spec fixture."""
    return VALID_SPEC_DIR


@pytest.fixture
def draft_spec_dir() -> Path:
    """Path to the golden draft spec fixture."""
    return DRAFT_SPEC_DIR


@pytest.fixture
def tmp_spec_dir(tmp_path: Path) -> Path:
    """Empty temporary directory for save tests."""
    return tmp_path
