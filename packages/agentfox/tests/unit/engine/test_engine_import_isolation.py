"""Module deletion, config cleanup, and CLI tests for spec 114.

Verifies that deleted files/directories are gone, that config classes are
cleaned up, and that CLI commands are updated.

Import-isolation checks (banned symbol names in source files) have been moved
to ruff TID251 rules in pyproject.toml — they now run at lint time instead
of test time.

Test Spec: TS-114-14, TS-114-21 through TS-114-23, TS-114-25 through TS-114-31,
           TS-114-34, TS-114-38, TS-114-E8
Requirements: 114-REQ-4.3, 114-REQ-7.1 through 114-REQ-7.5,
              114-REQ-8.1 through 114-REQ-8.5,
              114-REQ-9.1, 114-REQ-9.4, 114-REQ-10.4
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[3]
_ACTUAL_REPO_ROOT = _REPO_ROOT.parent.parent
_AF_ROOT = _ACTUAL_REPO_ROOT / "packages" / "af"


# ---------------------------------------------------------------------------
# TS-114-14 / TS-114-24: knowledge_harvest.py Deleted
# ---------------------------------------------------------------------------


class TestHarvestDeleted:
    """Verify knowledge_harvest.py no longer exists.

    Requirements: 114-REQ-4.3, 114-REQ-7.4
    """

    def test_file_does_not_exist(self) -> None:
        assert not (_REPO_ROOT / "agentfox" / "engine" / "knowledge_harvest.py").exists()


# ---------------------------------------------------------------------------
# TS-114-21: Knowledge Module Files Deleted
# ---------------------------------------------------------------------------


class TestKnowledgeFilesDeleted:
    """Verify all listed knowledge module files are deleted.

    Requirements: 114-REQ-7.1
    """

    DELETED = [
        "extraction.py",
        "embeddings.py",
        "search.py",
        "retrieval.py",
        "causal.py",
        "lifecycle.py",
        "contradiction.py",
        "consolidation.py",
        "compaction.py",
        "entity_linker.py",
        "entity_query.py",
        "entity_store.py",
        "entities.py",
        "static_analysis.py",
        "git_mining.py",
        "doc_mining.py",
        "sleep_compute.py",
        "code_analysis.py",
        "onboard.py",
        "project_model.py",
        "query_oracle.py",
        "query_patterns.py",
        "query_temporal.py",
        "rendering.py",
        "store.py",
        "ingest.py",
        "facts.py",
    ]

    def test_files_do_not_exist(self) -> None:
        knowledge_dir = _REPO_ROOT / "agentfox" / "knowledge"
        for name in self.DELETED:
            assert not (knowledge_dir / name).exists(), f"Expected {name} to be deleted from agent_fox/knowledge/"


# ---------------------------------------------------------------------------
# TS-114-22: Lang Directory Deleted
# ---------------------------------------------------------------------------


class TestLangDirDeleted:
    """Verify agent_fox/knowledge/lang/ directory is deleted.

    Requirements: 114-REQ-7.2
    """

    def test_lang_directory_gone(self) -> None:
        assert not (_REPO_ROOT / "agentfox" / "knowledge" / "lang").exists()


# ---------------------------------------------------------------------------
# TS-114-23: Sleep Tasks Directory Deleted
# ---------------------------------------------------------------------------


class TestSleepTasksDirDeleted:
    """Verify agent_fox/knowledge/sleep_tasks/ directory is deleted.

    Requirements: 114-REQ-7.3
    """

    def test_sleep_tasks_directory_gone(self) -> None:
        assert not (_REPO_ROOT / "agentfox" / "knowledge" / "sleep_tasks").exists()


# ---------------------------------------------------------------------------
# TS-114-25: Import Health After Deletions
# ---------------------------------------------------------------------------


class TestImportHealth:
    """Verify import agent_fox succeeds with zero import errors.

    Requirements: 114-REQ-7.5
    """

    def test_import_succeeds(self) -> None:
        result = subprocess.run(
            [sys.executable, "-c", "import agentfox"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0, f"import agentfox failed:\nstdout: {result.stdout}\nstderr: {result.stderr}"


# ---------------------------------------------------------------------------
# TS-114-26: KnowledgeConfig Fields Removed
# ---------------------------------------------------------------------------


class TestConfigFieldsRemoved:
    """Verify removed fields are no longer present on KnowledgeConfig.

    Requirements: 114-REQ-8.1
    """

    REMOVED = {
        "embedding_model",
        "embedding_dimensions",
        "ask_top_k",
        "ask_synthesis_model",
        "dedup_similarity_threshold",
        "contradiction_similarity_threshold",
        "contradiction_model",
        "decay_half_life_days",
        "decay_floor",
        "cleanup_fact_threshold",
        "cleanup_enabled",
        "confidence_threshold",
        "fact_cache_enabled",
    }

    def test_fields_not_present(self) -> None:
        from agentfox.core.config import KnowledgeConfig

        for field_name in self.REMOVED:
            assert field_name not in KnowledgeConfig.model_fields, (
                f"Removed field {field_name!r} still present on KnowledgeConfig"
            )


# ---------------------------------------------------------------------------
# TS-114-27: RetrievalConfig Deleted
# ---------------------------------------------------------------------------


class TestRetrievalConfigDeleted:
    """Verify RetrievalConfig no longer exists in config module.

    Requirements: 114-REQ-8.2
    """

    def test_no_retrieval_config(self) -> None:
        import agentfox.core.config as cfg

        assert not hasattr(cfg, "RetrievalConfig")


# ---------------------------------------------------------------------------
# TS-114-28: SleepConfig Deleted
# ---------------------------------------------------------------------------


class TestSleepConfigDeleted:
    """Verify SleepConfig no longer exists in config module.

    Requirements: 114-REQ-8.3
    """

    def test_no_sleep_config(self) -> None:
        import agentfox.core.config as cfg

        assert not hasattr(cfg, "SleepConfig")


# ---------------------------------------------------------------------------
# TS-114-29: KnowledgeConfig Retains store_path
# ---------------------------------------------------------------------------


class TestStorePathRetained:
    """Verify store_path field is still present on KnowledgeConfig.

    Requirements: 114-REQ-8.4
    """

    def test_store_path_exists(self) -> None:
        from agentfox.core.config import KnowledgeConfig

        assert "store_path" in KnowledgeConfig.model_fields
        kc = KnowledgeConfig()
        assert kc.store_path == ".agent-fox/knowledge.duckdb"


# ---------------------------------------------------------------------------
# TS-114-30: Old Config Fields Ignored
# ---------------------------------------------------------------------------


class TestOldConfigIgnored:
    """Verify constructing KnowledgeConfig with old fields does not raise.

    Requirements: 114-REQ-8.5
    """

    def test_old_fields_silently_ignored(self) -> None:
        from agentfox.core.config import KnowledgeConfig

        kc = KnowledgeConfig(embedding_model="foo", decay_half_life_days=30)  # type: ignore[call-arg]
        assert kc.store_path == ".agent-fox/knowledge.duckdb"
        assert not hasattr(kc, "embedding_model") or "embedding_model" not in kc.model_fields_set


# ---------------------------------------------------------------------------
# TS-114-31: Onboard CLI Removed
# ---------------------------------------------------------------------------


class TestOnboardRemoved:
    """Verify cli/onboard.py is deleted and unregistered from cli/app.py.

    Requirements: 114-REQ-9.1
    """

    def test_onboard_file_deleted(self) -> None:
        assert not (_AF_ROOT / "af" / "onboard.py").exists()

    def test_onboard_not_in_app(self) -> None:
        app_path = _AF_ROOT / "af" / "app.py"
        source = app_path.read_text(encoding="utf-8")
        assert "onboard_cmd" not in source


# ---------------------------------------------------------------------------
# TS-114-34: CLI plan.py Still Functional
# ---------------------------------------------------------------------------


class TestCliPlanFunctional:
    """Verify cli/plan.py uses open_knowledge_store.

    Requirements: 114-REQ-9.4
    """

    def test_has_open_knowledge_store(self) -> None:
        path = _AF_ROOT / "af" / "plan.py"
        source = path.read_text(encoding="utf-8")
        assert "open_knowledge_store" in source


# ---------------------------------------------------------------------------
# TS-114-38: Dead Test Files Deleted
# ---------------------------------------------------------------------------


class TestDeadTestsDeleted:
    """Verify test files that exclusively test removed functionality are deleted.

    Requirements: 114-REQ-10.4
    """

    DELETED_TESTS = [
        "tests/unit/knowledge/test_extraction.py",
        "tests/unit/knowledge/test_embeddings.py",
        "tests/unit/knowledge/test_adaptive_retrieval.py",
        "tests/unit/knowledge/test_consolidation.py",
        "tests/unit/knowledge/test_compaction.py",
        "tests/unit/knowledge/test_sleep_compute.py",
        "tests/unit/knowledge/test_entity_linker.py",
        "tests/unit/knowledge/test_entity_query.py",
        "tests/unit/knowledge/test_entity_store.py",
        "tests/unit/knowledge/test_contradiction.py",
        "tests/unit/knowledge/test_lifecycle.py",
        "tests/unit/engine/test_knowledge_harvest.py",
    ]

    def test_dead_tests_removed(self) -> None:
        for path_str in self.DELETED_TESTS:
            full_path = _REPO_ROOT / path_str
            assert not full_path.exists(), f"Dead test file {path_str} should be deleted"


# ---------------------------------------------------------------------------
# TS-114-E8: Removed CLI Command Feedback
# ---------------------------------------------------------------------------


class TestRemovedCliFeedback:
    """Verify invoking a removed CLI command produces a clear error.

    Requirements: 114-REQ-9.E1
    """

    def test_onboard_command_not_found(self) -> None:
        """The onboard command is no longer registered."""
        from af.app import main
        from click.testing import CliRunner

        runner = CliRunner()
        result = runner.invoke(main, ["onboard"])
        assert result.exit_code != 0
        assert "no such command" in result.output.lower()
