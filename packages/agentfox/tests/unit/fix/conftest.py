"""Fixtures for error auto-fix tests.

Provides shared fixtures for check descriptors, failure records, failure
clusters, and mock configuration used across all fix test files.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from agentfox.core.config import AgentFoxConfig
from agentfox.fix.analyzer import Improvement
from agentfox.fix.checks import CheckCategory, CheckDescriptor, FailureRecord
from agentfox.fix.clusterer import FailureCluster

# -- Check descriptor fixtures ------------------------------------------------


@pytest.fixture
def check_descriptor_pytest() -> CheckDescriptor:
    """A pytest check descriptor."""
    return CheckDescriptor(
        name="pytest",
        command=["uv", "run", "pytest"],
        category=CheckCategory.TEST,
    )


@pytest.fixture
def ruff_check_descriptor() -> CheckDescriptor:
    """A ruff check descriptor."""
    return CheckDescriptor(
        name="ruff",
        command=["uv", "run", "ruff", "check", "."],
        category=CheckCategory.LINT,
    )


# -- Failure record fixtures --------------------------------------------------


def make_failure_record(
    check: CheckDescriptor | None = None,
    output: str = "FAILED test_example.py::test_one",
    exit_code: int = 1,
) -> FailureRecord:
    """Create a FailureRecord with sensible defaults."""
    if check is None:
        check = CheckDescriptor(
            name="pytest",
            command=["uv", "run", "pytest"],
            category=CheckCategory.TEST,
        )
    return FailureRecord(check=check, output=output, exit_code=exit_code)


@pytest.fixture
def sample_failure_record(
    check_descriptor_pytest: CheckDescriptor,
) -> FailureRecord:
    """A sample failure record from pytest."""
    return make_failure_record(check=check_descriptor_pytest)


# -- Failure cluster fixtures -------------------------------------------------


@pytest.fixture
def sample_failure_cluster(
    sample_failure_record: FailureRecord,
) -> FailureCluster:
    """A sample failure cluster with one pytest failure."""
    return FailureCluster(
        label="Missing return types",
        failures=[sample_failure_record],
        suggested_approach="Add return type annotations.",
    )


# -- Config fixtures -----------------------------------------------------------


@pytest.fixture
def mock_config() -> AgentFoxConfig:
    """An AgentFoxConfig with defaults for testing."""
    return AgentFoxConfig()


# -- Auto-improve fixtures -----------------------------------------------------


def make_improvement(
    id: str = "IMP-1",
    tier: str = "quick_win",
    title: str = "Remove dead import",
    description: str = "Remove unused import os from foo.py",
    files: list[str] | None = None,
    impact: str = "low",
    confidence: float = 0.9,
) -> Improvement:
    """Create an Improvement with sensible defaults."""
    return Improvement(
        id=id,
        tier=tier,
        title=title,
        description=description,
        files=files or ["foo.py"],
        impact=impact,
        confidence=confidence,
    )


@pytest.fixture
def valid_analyzer_json() -> str:
    """Valid JSON string for analyzer response."""
    return json.dumps(
        {
            "improvements": [
                {
                    "id": "IMP-1",
                    "tier": "quick_win",
                    "title": "Remove dead import",
                    "description": "Remove unused import",
                    "files": ["foo.py"],
                    "impact": "low",
                    "confidence": "high",
                },
                {
                    "id": "IMP-2",
                    "tier": "structural",
                    "title": "Consolidate validators",
                    "description": "Merge validators",
                    "files": ["a.py", "b.py"],
                    "impact": "medium",
                    "confidence": "medium",
                },
            ],
            "summary": "Found 2 improvements.",
            "diminishing_returns": False,
        }
    )


# -- Temp project helpers ------------------------------------------------------


@pytest.fixture
def tmp_project(tmp_path: Path) -> Path:
    """A temporary project directory for detector tests."""
    return tmp_path
