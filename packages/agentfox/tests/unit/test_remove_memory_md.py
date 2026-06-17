"""Tests for docs/memory.md removal (Spec 129).

Test Spec: TS-129-1 through TS-129-9, TS-129-P1, TS-129-P2, TS-129-SMOKE-1
Requirements: 129-REQ-1.1, 129-REQ-2.1, 129-REQ-2.2, 129-REQ-3.1, 129-REQ-3.2,
              129-REQ-3.3, 129-REQ-3.4, 129-REQ-4.1, 129-REQ-4.2, 129-REQ-5.1,
              129-REQ-6.1
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from click.testing import CliRunner

# Repository root — resolved at import time so tests can reference
# absolute paths to project files.
REPO_ROOT = Path(__file__).resolve().parent.parent.parent.parent.parent
_PKG_ROOT = REPO_ROOT / "packages" / "agentfox"


class TestFileDeleted:
    """TS-129-1: Verify docs/memory.md does not exist in the repo.

    Requirement: 129-REQ-1.1
    """

    def test_file_deleted(self) -> None:
        """docs/memory.md must not exist in the repository."""
        assert not (REPO_ROOT / "docs" / "memory.md").exists()


class TestInitDoesNotCreateMemoryMd:
    """TS-129-2: Verify agent-fox init does not create docs/memory.md.

    Requirement: 129-REQ-2.1
    """

    def test_init_no_memory_md(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """agent-fox init must not create docs/memory.md."""
        from af.app import main

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        assert not (tmp_git_repo / "docs" / "memory.md").exists()


class TestConstantRemoved:
    """TS-129-3: Verify _DOCS_MEMORY_CONTENT is gone from init_project.py.

    Requirement: 129-REQ-2.2
    """

    def test_constant_removed(self) -> None:
        """init_project.py must not contain _DOCS_MEMORY_CONTENT."""
        source = (_PKG_ROOT / "agentfox" / "workspace" / "init_project.py").read_text()
        assert "_DOCS_MEMORY_CONTENT" not in source


class TestTemplateClean:
    """TS-129-4: Verify agents_md.md template has no memory.md references.

    Requirement: 129-REQ-3.1
    """

    def test_template_clean(self) -> None:
        """agents_md.md template must not reference memory.md."""
        content = (_PKG_ROOT / "agentfox" / "_templates" / "agents_md.md").read_text()
        assert "memory.md" not in content


class TestClaudeMdClean:
    """TS-129-5: Verify CLAUDE.md has no memory.md references.

    Requirement: 129-REQ-3.2
    """

    def test_claude_md_clean(self) -> None:
        """CLAUDE.md must not reference memory.md."""
        content = (REPO_ROOT / "CLAUDE.md").read_text()
        assert "memory.md" not in content


class TestAgentsMdClean:
    """TS-129-6: Verify AGENTS.md has no memory.md references.

    Requirement: 129-REQ-3.3
    """

    def test_agents_md_clean(self) -> None:
        """AGENTS.md must not reference memory.md."""
        content = (REPO_ROOT / "AGENTS.md").read_text()
        assert "memory.md" not in content


class TestProfileClean:
    """TS-129-7: Verify agent.md profile has no memory.md references.

    Requirement: 129-REQ-3.4
    """

    def test_profile_clean(self) -> None:
        """agent.md profile template must not reference memory.md."""
        content = (_PKG_ROOT / "agentfox" / "_templates" / "profiles" / "agent.md").read_text()
        assert "memory.md" not in content


class TestInitTestsRemoved:
    """TS-129-9: Verify test_init.py has no memory.md test methods.

    Requirement: 129-REQ-5.1
    """

    def test_init_tests_removed(self) -> None:
        """test_init.py must not contain memory.md-related test methods."""
        source = (_PKG_ROOT / "tests" / "integration" / "test_init.py").read_text()
        assert "test_init_creates_docs_memory_md" not in source
        assert "test_reinit_preserves_existing_seed_files" not in source


class TestNoDanglingReferences:
    """TS-129-P1: No dangling references to docs/memory.md anywhere.

    Property: No tracked .py or .md file outside docs/audits/ and
    .agent-fox/specs/ references docs/memory.md.

    Requirement: 129-REQ-6.1
    """

    def test_no_dangling_refs(self) -> None:
        """No tracked file references docs/memory.md (excluding audits and specs)."""
        result = subprocess.run(
            ["git", "ls-files"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
        )
        all_files = result.stdout.strip().splitlines()
        py_md_files = [f for f in all_files if f.endswith(".py") or f.endswith(".md")]

        violations: list[str] = []
        for rel_path in py_md_files:
            # Exclusions: docs/audits/ and .agent-fox/specs/
            if rel_path.startswith("docs/audits/"):
                continue
            if rel_path.startswith(".agent-fox/specs/"):
                continue
            # Exclude this test file itself — it necessarily contains the
            # search string in its own assertions and docstrings.
            if rel_path == "packages/agentfox/tests/unit/test_remove_memory_md.py":
                continue

            full_path = REPO_ROOT / rel_path
            if not full_path.exists():
                continue

            content = full_path.read_text(encoding="utf-8", errors="replace")
            if "docs/memory.md" in content:
                violations.append(rel_path)

        assert violations == [], f"Found docs/memory.md references in: {violations}"


class TestInitProjectNoMemoryMd:
    """TS-129-P2: init_project does not create docs/memory.md.

    Property: For any invocation of init_project() on a fresh project
    directory, docs/memory.md shall not exist after the call completes.

    Requirement: 129-REQ-2.1
    """

    def test_init_project_no_memory_md(self, tmp_git_repo: Path) -> None:
        """init_project() must not create docs/memory.md."""
        from agentfox.workspace.init_project import init_project

        init_project(tmp_git_repo)

        assert not (tmp_git_repo / "docs" / "memory.md").exists()


class TestFullInitWithoutMemoryMd:
    """TS-129-SMOKE-1: Full init run does not produce docs/memory.md.

    Integration smoke test: end-to-end init via CliRunner.

    Requirement: 129-REQ-2.1
    """

    def test_full_init_smoke(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """End-to-end init run must not produce docs/memory.md."""
        from af.app import main

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        assert (tmp_git_repo / ".agent-fox").is_dir()
        assert not (tmp_git_repo / "docs" / "memory.md").exists()
