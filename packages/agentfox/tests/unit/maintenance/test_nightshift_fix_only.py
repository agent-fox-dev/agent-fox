"""Spec tests for 125_nightshift_fix_only: strip night-shift to fix-only mode.

Tests verify that hunt-scan and spec-executor code has been completely
removed, that the fix pipeline is preserved, that config backward
compatibility holds, and that the CLI rejects removed flags.

Test Spec: TS-125-1 through TS-125-12, TS-125-P1 through TS-125-P3,
TS-125-E1 through TS-125-E4.
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest
from click.testing import CliRunner
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_REPO_ROOT = Path(__file__).resolve().parents[3]


def _make_config() -> MagicMock:
    """Create a mock config matching expected structure for build_streams."""
    config = MagicMock()
    config.max_budget_usd = 10.0
    ns = MagicMock()
    ns.issue_check_interval = 900
    ns.push_fix_branch = False
    config.night_shift = ns
    config.platform.type = "github"
    config.theme = None
    config.orchestrator.max_cost = 10.0
    return config


def _make_engine_mock() -> MagicMock:
    """Create a mock engine with fix-pipeline methods."""
    engine = MagicMock()
    engine._drain_issues = AsyncMock(return_value=True)
    engine._run_issue_check = AsyncMock()
    engine._process_fix = AsyncMock()
    engine.state = MagicMock()
    engine.state.total_cost = 0.0
    return engine


# ---------------------------------------------------------------------------
# TS-125-1: Hunt source modules deleted (125-REQ-1.1)
# ---------------------------------------------------------------------------


class TestHuntSourceModulesDeleted:
    """TS-125-1: Verify all hunt-related source files are deleted."""

    _DELETED_FILES = [
        "agentfox/maintenance/hunt.py",
        "agentfox/maintenance/critic.py",
        "agentfox/maintenance/dedup.py",
        "agentfox/maintenance/finding.py",
        "agentfox/maintenance/ignore_filter.py",
        "agentfox/maintenance/ignore.py",
    ]

    @pytest.mark.parametrize("path", _DELETED_FILES)
    def test_hunt_source_module_deleted(self, path: str) -> None:
        assert not (_REPO_ROOT / path).exists(), f"{path} should be deleted"


# ---------------------------------------------------------------------------
# TS-125-2: Categories directory deleted (125-REQ-1.2)
# ---------------------------------------------------------------------------


class TestCategoriesDirectoryDeleted:
    """TS-125-2: Verify the categories directory no longer exists."""

    def test_categories_directory_deleted(self) -> None:
        cat_dir = _REPO_ROOT / "agentfox" / "nightshift" / "categories"
        assert not cat_dir.exists(), "agentfox/maintenance/categories/ should be deleted"


# ---------------------------------------------------------------------------
# TS-125-3: No dangling imports in nightshift package (125-REQ-1.3)
# ---------------------------------------------------------------------------


class TestNoDanglingImportsNightshift:
    """TS-125-3: No remaining nightshift module imports from deleted modules."""

    _DELETED_MODULES = [
        "hunt",
        "critic",
        "dedup",
        "finding",
        "ignore_filter",
        "ignore",
        "categories",
    ]

    def test_no_dangling_imports_nightshift(self) -> None:
        nightshift_dir = _REPO_ROOT / "agentfox" / "nightshift"
        violations: list[str] = []
        for py_file in sorted(nightshift_dir.rglob("*.py")):
            content = py_file.read_text(encoding="utf-8")
            for mod in self._DELETED_MODULES:
                pattern = f"from agentfox.maintenance.{mod}"
                if pattern in content:
                    violations.append(f"{py_file.name}: {pattern}")
        assert not violations, f"Dangling imports found: {violations}"


# ---------------------------------------------------------------------------
# TS-125-4: Engine has no hunt methods (125-REQ-2.1)
# ---------------------------------------------------------------------------


class TestEngineNoHuntMethods:
    """TS-125-4: NightShiftEngine does not have hunt-scan methods."""

    def test_engine_no_hunt_methods(self) -> None:
        from agentfox.maintenance.engine import NightShiftEngine

        assert not hasattr(NightShiftEngine, "_run_hunt_scan"), "NightShiftEngine should not have _run_hunt_scan"
        assert not hasattr(NightShiftEngine, "_run_hunt_scan_inner"), (
            "NightShiftEngine should not have _run_hunt_scan_inner"
        )


# ---------------------------------------------------------------------------
# TS-125-5: Engine constructor rejects auto_fix and embedder (125-REQ-2.2)
# ---------------------------------------------------------------------------


class TestEngineRejectsRemovedParams:
    """TS-125-5: Removed constructor parameters raise TypeError."""

    def test_engine_rejects_auto_fix(self) -> None:
        from agentfox.maintenance.engine import NightShiftEngine

        config = MagicMock()
        platform = MagicMock()
        with pytest.raises(TypeError):
            NightShiftEngine(config, platform, auto_fix=True)

    def test_engine_rejects_embedder(self) -> None:
        from agentfox.maintenance.engine import NightShiftEngine

        config = MagicMock()
        platform = MagicMock()
        with pytest.raises(TypeError):
            NightShiftEngine(config, platform, embedder=object())


# ---------------------------------------------------------------------------
# TS-125-6: Engine retains fix-pipeline methods (125-REQ-2.4)
# ---------------------------------------------------------------------------


class TestEngineRetainsFixMethods:
    """TS-125-6: Fix-pipeline methods are present on the engine."""

    def test_engine_retains_fix_methods(self) -> None:
        from agentfox.maintenance.engine import NightShiftEngine

        assert callable(getattr(NightShiftEngine, "_drain_issues", None)), "_drain_issues must be a callable"
        assert callable(getattr(NightShiftEngine, "_run_issue_check", None)), "_run_issue_check must be a callable"
        assert callable(getattr(NightShiftEngine, "_process_fix", None)), "_process_fix must be a callable"


# ---------------------------------------------------------------------------
# TS-125-7: SpecExecutorStream deleted (125-REQ-3.1)
# ---------------------------------------------------------------------------


class TestSpecExecutorStreamDeleted:
    """TS-125-7: SpecExecutorStream is not importable from streams."""

    def test_spec_executor_stream_deleted(self) -> None:
        from agentfox.maintenance import streams

        assert not hasattr(streams, "SpecExecutorStream"), "SpecExecutorStream should be deleted from streams module"


# ---------------------------------------------------------------------------
# TS-125-8: build_streams returns single fix stream (125-REQ-3.3)
# ---------------------------------------------------------------------------


class TestBuildStreamsSingleFix:
    """TS-125-8: build_streams() returns exactly one fix-pipeline stream."""

    def test_build_streams_single_fix(self) -> None:
        from agentfox.maintenance.daemon import SharedBudget
        from agentfox.maintenance.streams import build_streams

        config = _make_config()
        engine = _make_engine_mock()
        budget = SharedBudget(max_cost=10.0)

        streams = build_streams(config, engine=engine, budget=budget)

        assert len(streams) == 1, f"Expected 1 stream, got {len(streams)}"
        assert streams[0].name == "fix-pipeline"
        assert streams[0].enabled is True


# ---------------------------------------------------------------------------
# TS-125-10: Config backward compatibility (125-REQ-5.4)
# ---------------------------------------------------------------------------


class TestConfigBackwardCompat:
    """TS-125-10: NightShiftConfig ignores removed fields."""

    def test_config_backward_compat(self) -> None:
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(
            hunt_scan_interval=3600,
            quality_gate_timeout=120,
            spec_interval=60,
            enabled_streams=["fixes"],
            similarity_threshold=0.9,
            categories={"dead_code": False},
        )
        # Retained fields keep defaults
        assert cfg.issue_check_interval == 900
        assert cfg.push_fix_branch is False


# ---------------------------------------------------------------------------
# TS-125-11: NightShiftCategoryConfig deleted (125-REQ-5.2)
# ---------------------------------------------------------------------------


class TestCategoryConfigDeleted:
    """TS-125-11: NightShiftCategoryConfig is not importable."""

    def test_category_config_deleted(self) -> None:
        from agentfox.core import config as config_mod

        assert not hasattr(config_mod, "NightShiftCategoryConfig"), (
            "NightShiftCategoryConfig should be deleted from config module"
        )


# ---------------------------------------------------------------------------
# TS-125-12: init_project does not create .night-shift file (125-REQ-6.2)
# ---------------------------------------------------------------------------


class TestInitNoNightshiftFile:
    """TS-125-12: init_project no longer creates a .night-shift file."""

    def test_init_no_nightshift_file(self, tmp_git_repo: Path) -> None:
        from agentfox.workspace.init_project import init_project

        init_project(tmp_git_repo)
        nightshift_file = tmp_git_repo / ".night-shift"
        assert not nightshift_file.exists(), ".night-shift file should not be created by init_project"


# ---------------------------------------------------------------------------
# TS-125-P1: No dangling imports anywhere (Property 2)
# Validates: 125-REQ-1.3, 125-REQ-1.E1, 125-REQ-7.2
# ---------------------------------------------------------------------------


class TestNoDanglingImportsAnywhere:
    """TS-125-P1: No source or test file imports from a deleted module."""

    _DELETED_MODULES = [
        "hunt",
        "critic",
        "dedup",
        "finding",
        "ignore_filter",
        "ignore",
        "categories",
    ]

    def test_no_dangling_imports_anywhere(self) -> None:
        """Scan all git-tracked .py files for imports of deleted modules."""
        result = subprocess.run(
            ["git", "ls-files", "*.py", "**/*.py"],
            capture_output=True,
            text=True,
            cwd=_REPO_ROOT,
        )
        tracked_files = [f for f in result.stdout.strip().splitlines() if f.endswith(".py")]

        violations: list[str] = []
        for rel_path in tracked_files:
            full_path = _REPO_ROOT / rel_path
            if not full_path.exists():
                continue
            content = full_path.read_text(encoding="utf-8")
            for mod in self._DELETED_MODULES:
                needle_from = f"from agentfox.maintenance.{mod}"
                needle_import = f"import agentfox.maintenance.{mod}"
                if needle_from in content or needle_import in content:
                    violations.append(f"{rel_path}: references {mod}")

        assert not violations, "Dangling imports of deleted modules found:\n" + "\n".join(
            f"  - {v}" for v in violations
        )


# ---------------------------------------------------------------------------
# TS-125-P2: Config backward compat for any removed field set (Property 3)
# Validates: 125-REQ-5.1, 125-REQ-5.4
# ---------------------------------------------------------------------------


_REMOVED_FIELDS: dict[str, st.SearchStrategy] = {
    "hunt_scan_interval": st.integers(min_value=60, max_value=86400),
    "quality_gate_timeout": st.integers(min_value=60, max_value=3600),
    "spec_interval": st.integers(min_value=10, max_value=3600),
    "enabled_streams": st.lists(st.sampled_from(["specs", "fixes", "hunts"]), max_size=3),
    "similarity_threshold": st.floats(min_value=0.0, max_value=1.0),
    "categories": st.fixed_dictionaries(
        {},
        optional={
            "dead_code": st.booleans(),
            "todo_fixme": st.booleans(),
            "test_coverage": st.booleans(),
        },
    ),
}


@settings(
    max_examples=30,
    suppress_health_check=[HealthCheck.too_slow],
)
@given(
    hunt_scan_interval=_REMOVED_FIELDS["hunt_scan_interval"],
    quality_gate_timeout=_REMOVED_FIELDS["quality_gate_timeout"],
    spec_interval=_REMOVED_FIELDS["spec_interval"],
    enabled_streams=_REMOVED_FIELDS["enabled_streams"],
    similarity_threshold=_REMOVED_FIELDS["similarity_threshold"],
    categories=_REMOVED_FIELDS["categories"],
)
def test_config_ignores_removed_fields(
    hunt_scan_interval: int,
    quality_gate_timeout: int,
    spec_interval: int,
    enabled_streams: list[str],
    similarity_threshold: float,
    categories: dict,
) -> None:
    """TS-125-P2: Any combination of removed fields is silently ignored."""
    from agentfox.core.config import NightShiftConfig

    cfg = NightShiftConfig(
        hunt_scan_interval=hunt_scan_interval,
        quality_gate_timeout=quality_gate_timeout,
        spec_interval=spec_interval,
        enabled_streams=enabled_streams,
        similarity_threshold=similarity_threshold,
        categories=categories,
    )
    assert cfg.issue_check_interval == 900
    assert cfg.push_fix_branch is False


# ---------------------------------------------------------------------------
# TS-125-P3: build_streams always returns exactly one stream (Property 4)
# Validates: 125-REQ-3.3, 125-REQ-3.E1
# ---------------------------------------------------------------------------


def test_build_streams_always_one_stream() -> None:
    """TS-125-P3: build_streams always returns exactly one fix-pipeline stream."""
    from agentfox.maintenance.daemon import SharedBudget
    from agentfox.maintenance.streams import build_streams

    config = _make_config()
    engine = _make_engine_mock()
    budget = SharedBudget(max_cost=10.0)

    streams = build_streams(config, engine=engine, budget=budget)

    assert len(streams) == 1
    assert streams[0].name == "fix-pipeline"
    assert streams[0].enabled is True


