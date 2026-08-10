"""Unit tests for server-side compaction feature (issue #688).

Validates:
- PerArchetypeConfig accepts a `compaction` boolean field (NS-REQ-1)
- resolve_compaction() follows mode -> per-archetype -> default priority (NS-REQ-2)
- ResolvedSessionParams carries a `compaction: bool` field (NS-REQ-2)
- ClaudeBackend.execute() passes compaction to ClaudeAgentOptions (NS-REQ-3)
- End-to-end threading from config to backend (NS-REQ-4)
- docs/config-reference.md documents the field (NS-REQ-5)

Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox.archetypes import ARCHETYPE_REGISTRY
from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    PerArchetypeConfig,
    load_config,
)
from agentfox.engine.sdk_params import (
    ResolvedSessionParams,
    resolve_compaction,
    resolve_session_params,
)

# ---------------------------------------------------------------------------
# TS-NS-1: ArchetypeEntry.default_compaction field and registry values
# Requirement: NS-REQ-1
# ---------------------------------------------------------------------------


class TestArchetypeRegistryCompaction:
    """ARCHETYPE_REGISTRY entries have correct default_compaction values."""

    def test_coder_default_compaction_is_true(self) -> None:
        """Coder archetype has default_compaction=True."""
        assert ARCHETYPE_REGISTRY["coder"].default_compaction is True

    def test_reviewer_default_compaction_is_false(self) -> None:
        """Reviewer archetype has default_compaction=False."""
        assert ARCHETYPE_REGISTRY["reviewer"].default_compaction is False

    def test_other_archetypes_default_compaction_is_false(self) -> None:
        """All non-coder archetypes have default_compaction=False."""
        for name, entry in ARCHETYPE_REGISTRY.items():
            if name != "coder":
                assert entry.default_compaction is False, (
                    f"Expected default_compaction=False for archetype '{name}'"
                )


# ---------------------------------------------------------------------------
# TS-NS-1 continued: PerArchetypeConfig compaction field round-trips through TOML
# Requirement: NS-REQ-1
# ---------------------------------------------------------------------------


class TestCompactionConfigParsing:
    """PerArchetypeConfig accepts a `compaction` boolean field."""

    def test_compaction_true_from_toml(self, tmp_path: Path) -> None:
        """compaction = true in TOML loads correctly."""
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            "[archetypes.overrides.coder]\ncompaction = true\n"
        )
        config = load_config(path=config_file)
        assert config.archetypes.overrides["coder"].compaction is True

    def test_compaction_false_from_toml(self, tmp_path: Path) -> None:
        """compaction = false in TOML loads correctly."""
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            "[archetypes.overrides.coder]\ncompaction = false\n"
        )
        config = load_config(path=config_file)
        assert config.archetypes.overrides["coder"].compaction is False

    def test_compaction_default_is_none(self) -> None:
        """Default PerArchetypeConfig has compaction as None."""
        cfg = PerArchetypeConfig()
        assert cfg.compaction is None

    def test_compaction_none_when_key_absent(self, tmp_path: Path) -> None:
        """Config without compaction key leaves the field as None."""
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            "[archetypes.overrides.coder]\nmax_turns = 100\n"
        )
        config = load_config(path=config_file)
        assert config.archetypes.overrides["coder"].compaction is None

    def test_compaction_none_when_no_overrides(self) -> None:
        """Default config has empty overrides dict."""
        config = AgentFoxConfig()
        assert config.archetypes.overrides == {}


# ---------------------------------------------------------------------------
# TS-NS-2: resolve_compaction() priority chain
# Requirement: NS-REQ-2
# ---------------------------------------------------------------------------


class TestResolveCompaction:
    """resolve_compaction follows mode -> per-archetype -> default priority."""

    def test_per_archetype_override_true(self) -> None:
        """Per-archetype compaction=True overrides default."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(compaction=True)},
            )
        )
        assert resolve_compaction(config, "coder") is True

    def test_per_archetype_override_false(self) -> None:
        """Per-archetype compaction=False is respected."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(compaction=False)},
            )
        )
        assert resolve_compaction(config, "coder") is False

    def test_coder_default_is_true(self) -> None:
        """Registry default for compaction is True for coder."""
        config = AgentFoxConfig()
        assert resolve_compaction(config, "coder") is True

    def test_non_coder_default_is_false(self) -> None:
        """Registry default for compaction is False for non-coder archetypes."""
        config = AgentFoxConfig()
        assert resolve_compaction(config, "reviewer") is False

    def test_none_override_falls_through_to_default(self) -> None:
        """PerArchetypeConfig with compaction=None falls through to default."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(compaction=None)},
            )
        )
        assert resolve_compaction(config, "coder") is True

    def test_mode_level_override_takes_precedence(self) -> None:
        """Mode-level compaction overrides per-archetype level."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        compaction=False,
                        modes={
                            "fix": PerArchetypeConfig(compaction=True),
                        },
                    ),
                },
            )
        )
        # Mode-level override should win
        assert resolve_compaction(config, "coder", mode="fix") is True
        # Without mode, per-archetype value applies
        assert resolve_compaction(config, "coder") is False

    def test_mode_none_compaction_falls_through_to_archetype(self) -> None:
        """Mode with compaction=None falls through to per-archetype."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        compaction=True,
                        modes={
                            "fix": PerArchetypeConfig(compaction=None),
                        },
                    ),
                },
            )
        )
        assert resolve_compaction(config, "coder", mode="fix") is True


# ---------------------------------------------------------------------------
# TS-NS-2 continued: ResolvedSessionParams carries compaction field
# Requirement: NS-REQ-2
# ---------------------------------------------------------------------------


class TestResolvedSessionParamsCompaction:
    """ResolvedSessionParams carries a `compaction: bool` field."""

    def test_resolved_params_has_compaction_field(self) -> None:
        """ResolvedSessionParams includes compaction attribute."""
        params = ResolvedSessionParams(
            max_turns=100,
            thinking=None,
            max_budget_usd=20.0,
            effort="high",
            compaction=True,
            cache_policy="DEFAULT",
        )
        assert params.compaction is True

    def test_resolve_session_params_includes_compaction(self) -> None:
        """resolve_session_params() populates the compaction field."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(compaction=True)},
            )
        )
        params = resolve_session_params(config, "coder")
        assert params.compaction is True

    def test_resolve_session_params_default_compaction_coder(self) -> None:
        """resolve_session_params() defaults compaction to True for coder."""
        config = AgentFoxConfig()
        params = resolve_session_params(config, "coder")
        assert params.compaction is True

    def test_resolve_session_params_default_compaction_reviewer(self) -> None:
        """resolve_session_params() defaults compaction to False for non-coder."""
        config = AgentFoxConfig()
        params = resolve_session_params(config, "reviewer")
        assert params.compaction is False


# ---------------------------------------------------------------------------
# TS-NS-3: ClaudeBackend.execute() passes compaction to ClaudeAgentOptions
# Requirement: NS-REQ-3
# ---------------------------------------------------------------------------


class TestClaudeBackendCompaction:
    """ClaudeBackend.execute() sets context_management when compaction=True."""

    @pytest.mark.asyncio
    async def test_compaction_true_sets_context_management(self) -> None:
        """When compaction=True, options carry context_management."""
        from agentfox.session.backends.claude import ClaudeBackend

        backend = ClaudeBackend()
        captured_options = {}

        async def mock_stream(*, prompt, options):
            captured_options["options"] = options
            return
            yield  # makes this an async generator

        with patch.object(backend, "_stream_messages", mock_stream):
            async for _ in backend.execute(
                "test prompt",
                system_prompt="test system",
                model="claude-sonnet-4-6",
                cwd="/tmp",
                compaction=True,
            ):
                pass

        opts = captured_options.get("options")
        assert opts is not None
        assert hasattr(opts, "context_management")
        assert opts.context_management == {
            "edits": [{"type": "compact_20260112"}],
        }

    @pytest.mark.asyncio
    async def test_compaction_false_no_context_management(self) -> None:
        """When compaction=False, no context_management is set."""
        from agentfox.session.backends.claude import ClaudeBackend

        backend = ClaudeBackend()
        captured_options = {}

        async def mock_stream(*, prompt, options):
            captured_options["options"] = options
            return
            yield  # makes this an async generator

        with patch.object(backend, "_stream_messages", mock_stream):
            async for _ in backend.execute(
                "test prompt",
                system_prompt="test system",
                model="claude-sonnet-4-6",
                cwd="/tmp",
                compaction=False,
            ):
                pass

        opts = captured_options.get("options")
        assert opts is not None
        # context_management should not be set or should be None
        cm = getattr(opts, "context_management", None)
        assert cm is None

    @pytest.mark.asyncio
    async def test_compaction_default_no_context_management(self) -> None:
        """When compaction is omitted (default False), no context_management is set."""
        from agentfox.session.backends.claude import ClaudeBackend

        backend = ClaudeBackend()
        captured_options = {}

        async def mock_stream(*, prompt, options):
            captured_options["options"] = options
            return
            yield  # makes this an async generator

        with patch.object(backend, "_stream_messages", mock_stream):
            async for _ in backend.execute(
                "test prompt",
                system_prompt="test system",
                model="claude-sonnet-4-6",
                cwd="/tmp",
            ):
                pass

        opts = captured_options.get("options")
        assert opts is not None
        cm = getattr(opts, "context_management", None)
        assert cm is None


# ---------------------------------------------------------------------------
# TS-NS-5: docs/config-reference.md documents compaction
# Requirement: NS-REQ-5
# ---------------------------------------------------------------------------


class TestCompactionDocumentation:
    """docs/config-reference.md documents compaction under archetypes.overrides."""

    def test_compaction_documented(self) -> None:
        """Config reference contains compaction row."""
        docs_path = Path(__file__).parents[4] / "docs" / "config-reference.md"
        if not docs_path.exists():
            # Fall back to repo root
            docs_path = Path("docs/config-reference.md")
        content = docs_path.read_text(encoding="utf-8")
        assert "compaction" in content
        assert "bool" in content.lower() or "bool\\|null" in content
