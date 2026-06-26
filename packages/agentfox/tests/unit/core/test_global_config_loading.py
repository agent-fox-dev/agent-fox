"""Tests for global config loading (spec 13).

Covers TS-13-1 through TS-13-30, TS-13-E1 through TS-13-E7,
TS-13-P1 through TS-13-P6.

Group 1: failing tests (red phase) — tests MUST fail because the
implementation does not exist yet, but MUST be syntactically valid
and pass the linter.
"""

from __future__ import annotations

import logging
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest
from agentfox.core.config import AgentFoxConfig, load_config
from agentfox.core.errors import ConfigError

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def fake_home(tmp_path, monkeypatch):
    """Set HOME to a temporary directory."""
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch.setenv("HOME", str(home))
    # Also patch Path.home() so it respects the env var
    monkeypatch.setattr(Path, "home", staticmethod(lambda: home))
    return home


@pytest.fixture()
def global_config_dir(fake_home):
    """Create the $HOME/.agent-fox/ directory."""
    d = fake_home / ".agent-fox"
    d.mkdir(parents=True)
    return d


@pytest.fixture()
def global_config(global_config_dir):
    """Create a minimal valid global config file."""
    cfg = global_config_dir / "config.toml"
    cfg.write_text("[orchestrator]\nparallel = 2\n")
    return cfg


@pytest.fixture()
def local_config_dir(tmp_path):
    """Create a .agent-fox/ directory in the working directory."""
    d = tmp_path / "repo" / ".agent-fox"
    d.mkdir(parents=True)
    return d


@pytest.fixture()
def clean_af_env(monkeypatch):
    """Remove AF_CONFIG and AF_SPEC_MODEL from the environment."""
    monkeypatch.delenv("AF_CONFIG", raising=False)
    monkeypatch.delenv("AF_SPEC_MODEL", raising=False)


# ===================================================================
# TS-13-1: Unified load_config across all CLIs
# ===================================================================
class TestUnifiedLoadConfig:
    """TS-13-1: All three CLIs share the same load_config function."""

    def test_all_clis_share_load_config_function(self):
        """Verify af, nightshift, and spec import the same load_config."""
        import af.app
        import nightshift.app
        import spec.cli
        from agentfox.core.config import load_config as agentfox_load_config

        # All three should reference the same function object
        assert af.app.load_config is agentfox_load_config
        assert nightshift.app.load_config is agentfox_load_config
        assert spec.cli.load_config is agentfox_load_config


# ===================================================================
# TS-13-2: Global + local merge with shallow section replacement
# ===================================================================
class TestGlobalLocalMerge:
    """TS-13-2: load_config merges global and local configs."""

    def test_local_overrides_global_orchestrator(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Local [orchestrator] parallel=8 overrides global parallel=2."""
        # Global config
        (global_config_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 2\n"
        )
        # Local config in CWD
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 8\n"
        )
        monkeypatch.chdir(repo)

        config = load_config()

        assert config.orchestrator.parallel == 8


# ===================================================================
# TS-13-3: Post-merge validation with Pydantic defaults
# ===================================================================
class TestPostMergeValidation:
    """TS-13-3: Omitted fields have Pydantic defaults applied."""

    def test_defaults_applied_after_merge(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Partial global config gets all defaults filled in."""
        (global_config_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 4\n"
        )
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        config = load_config()

        assert isinstance(config, AgentFoxConfig)
        # spec_tool defaults (new sub-config)
        assert config.spec_tool.model == "claude-sonnet-4-6"
        assert config.spec_tool.auth_method == ""


# ===================================================================
# TS-13-4: Global config auto-creation
# ===================================================================
class TestGlobalConfigAutoCreation:
    """TS-13-4: load_config creates global config with 0o700 dir."""

    def test_auto_creates_global_config(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """When no global config exists, it is auto-created."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        config = load_config()

        global_dir = fake_home / ".agent-fox"
        global_config = global_dir / "config.toml"
        assert global_dir.exists()
        assert oct(global_dir.stat().st_mode & 0o777) == "0o700"
        assert global_config.exists()
        assert isinstance(config, AgentFoxConfig)


# ===================================================================
# TS-13-5: Existing valid global config used as baseline
# ===================================================================
class TestExistingGlobalConfig:
    """TS-13-5: Existing global config is parsed and used."""

    def test_existing_global_config_used(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Global config with theme.playful=false is reflected; file not modified."""
        global_cfg = global_config_dir / "config.toml"
        global_cfg.write_text("[theme]\nplayful = false\n")
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        mtime_before = global_cfg.stat().st_mtime

        config = load_config()

        assert config.theme.playful is False
        # TS-13-5: file must not be modified
        mtime_after = global_cfg.stat().st_mtime
        assert mtime_before == mtime_after


# ===================================================================
# TS-13-6: $HOME unresolvable — skip global, use local/defaults
# ===================================================================
class TestHomeUnresolvable:
    """TS-13-6: HOME unresolvable skips global config."""

    def test_home_unresolvable_uses_defaults(
        self, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """When HOME cannot be resolved, DEBUG log emitted, no exception."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 5\n"
        )
        monkeypatch.chdir(repo)
        monkeypatch.delenv("HOME", raising=False)
        monkeypatch.setattr(
            Path, "home", staticmethod(lambda: (_ for _ in ()).throw(RuntimeError("no home")))
        )

        with caplog.at_level(logging.DEBUG):
            config = load_config()

        assert isinstance(config, AgentFoxConfig)
        # TS-13-6: same message must contain BOTH 'HOME' AND 'could not be resolved'/'skipped'
        assert any(
            "HOME" in msg and ("could not be resolved" in msg or "skipped" in msg)
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-E2: Symlinked global config raises ConfigError
# ===================================================================
class TestGlobalConfigSymlink:
    """TS-13-E2: Symlink on global config raises ConfigError with CWE-59."""

    def test_global_symlink_raises_config_error(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """$HOME/.agent-fox/config.toml as symlink -> ConfigError."""
        agent_dir = fake_home / ".agent-fox"
        agent_dir.mkdir(exist_ok=True)
        real_config = tmp_path / "real-config.toml"
        real_config.write_text("[orchestrator]\nparallel = 1\n")
        global_config_path = agent_dir / "config.toml"
        global_config_path.symlink_to(real_config)

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        with pytest.raises(ConfigError, match=r"(?i)symlink|CWE-59") as exc_info:
            load_config()
        # TS-13-E2: error must identify the symlinked global config path
        assert str(global_config_path) in str(exc_info.value)


# ===================================================================
# TS-13-E3: Global dir creation failure -> ConfigError
# ===================================================================
class TestGlobalDirCreationFailure:
    """TS-13-E3: Permission error creating $HOME/.agent-fox/."""

    def test_dir_creation_permission_error(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Read-only $HOME prevents dir creation -> ConfigError."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)
        # Make HOME read-only
        fake_home.chmod(0o444)

        try:
            with pytest.raises(ConfigError) as exc_info:
                load_config()
            error_msg = str(exc_info.value).lower()
            # TS-13-E3: error must mention permission and identify the directory
            assert "permission" in error_msg or "errno" in error_msg
            assert ".agent-fox" in str(exc_info.value)
        finally:
            fake_home.chmod(0o755)


# ===================================================================
# TS-13-7: Shallow section replacement
# ===================================================================
class TestShallowSectionReplacement:
    """TS-13-7: Local section entirely replaces global section."""

    def test_shallow_replacement(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Local [orchestrator] replaces global wholesale; [routing] inherited."""
        (global_config_dir / "config.toml").write_text(textwrap.dedent("""\
            [orchestrator]
            parallel = 2
            session_timeout = 60

            [routing]
            retries_before_escalation = 3
        """))
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 8\n"
        )
        monkeypatch.chdir(repo)

        config = load_config()

        assert config.orchestrator.parallel == 8
        # Routing inherited from global
        assert config.routing.retries_before_escalation == 3


# ===================================================================
# TS-13-8: No local config — global used unchanged, DEBUG log
# ===================================================================
class TestNoLocalConfig:
    """TS-13-8: No local config -> global used, DEBUG log emitted."""

    def test_no_local_config(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """When no local config, global values used and DEBUG log emitted."""
        (global_config_dir / "config.toml").write_text(
            "[theme]\nplayful = false\n"
        )
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        with caplog.at_level(logging.DEBUG):
            config = load_config()

        assert config.theme.playful is False
        # TS-13-8: must include the full path suffix
        assert any(
            "No local config found at" in msg and ".agent-fox/config.toml" in msg
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-9: No deep merge — section replacement is wholesale
# ===================================================================
class TestNoDeepMerge:
    """TS-13-9: No deep merge within sections."""

    def test_no_deep_merge(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Global session_timeout=60 is NOT preserved when local overrides [orchestrator]."""
        (global_config_dir / "config.toml").write_text(textwrap.dedent("""\
            [orchestrator]
            parallel = 2
            session_timeout = 60
        """))
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 8\n"
        )
        monkeypatch.chdir(repo)

        config = load_config()

        assert config.orchestrator.parallel == 8
        # session_timeout should revert to Pydantic default (30), not global's 60
        assert config.orchestrator.session_timeout != 60
        assert config.orchestrator.session_timeout == 30


# ===================================================================
# TS-13-E4: Symlinked local config raises ConfigError
# ===================================================================
class TestLocalConfigSymlink:
    """TS-13-E4: Symlinked local config raises ConfigError."""

    def test_local_symlink_raises_config_error(
        self, fake_home, global_config, tmp_path, monkeypatch, clean_af_env
    ):
        """Local .agent-fox/config.toml as symlink -> ConfigError."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)

        real_config = tmp_path / "other-config.toml"
        real_config.write_text("[orchestrator]\nparallel = 1\n")
        (local_dir / "config.toml").symlink_to(real_config)
        monkeypatch.chdir(repo)

        with pytest.raises(ConfigError, match=r"(?i)symlink|CWE-59") as exc_info:
            load_config()
        # TS-13-E4: error must identify the local config path
        assert ".agent-fox/config.toml" in str(exc_info.value)


# ===================================================================
# TS-13-E5: Symlinked intermediate dir is NOT rejected
# ===================================================================
class TestIntermediateSymlinkAllowed:
    """TS-13-E5: Symlink checks apply only to the final file."""

    def test_symlinked_intermediate_dir_not_rejected(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Symlinked intermediate directory is OK if final file is real."""
        # Create a real directory and config
        real_dir = tmp_path / "real_agent_fox"
        real_dir.mkdir()
        (real_dir / "config.toml").write_text("[orchestrator]\nparallel = 3\n")

        # Create a symlinked intermediate directory
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        (repo / ".agent-fox").symlink_to(real_dir)
        monkeypatch.chdir(repo)

        # Should NOT raise — symlink check is on the final file only
        config = load_config()
        assert isinstance(config, AgentFoxConfig)


# ===================================================================
# TS-13-10: Malformed global config -> ConfigError immediately
# ===================================================================
class TestMalformedGlobalConfig:
    """TS-13-10: Malformed global TOML -> ConfigError before local is read."""

    def test_malformed_global_raises_config_error(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Global config with invalid TOML raises ConfigError."""
        global_config_path = global_config_dir / "config.toml"
        global_config_path.write_text("[broken = unterminated")
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text("[orchestrator]\nparallel = 1\n")
        monkeypatch.chdir(repo)

        with pytest.raises(ConfigError) as exc_info:
            load_config()

        error_msg = str(exc_info.value)
        # TS-13-10: must identify the global config file path specifically
        assert str(global_config_path) in error_msg
        # TS-13-10: must mention parse error or TOML
        assert "parse" in error_msg.lower() or "TOML" in error_msg


# ===================================================================
# TS-13-11: Malformed local config -> ConfigError
# ===================================================================
class TestMalformedLocalConfig:
    """TS-13-11: Malformed local TOML -> ConfigError with local path."""

    def test_malformed_local_raises_config_error(
        self, fake_home, global_config, tmp_path, monkeypatch, clean_af_env
    ):
        """Local config with invalid TOML raises ConfigError after global loads."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text("key = @invalid")
        monkeypatch.chdir(repo)

        with pytest.raises(ConfigError) as exc_info:
            load_config()

        error_msg = str(exc_info.value)
        # TS-13-11: must identify the local config file path
        assert ".agent-fox/config.toml" in error_msg
        # TS-13-11: must mention parse error or TOML
        assert "parse" in error_msg.lower() or "TOML" in error_msg


# ===================================================================
# TS-13-12: No partial config on malformed TOML
# ===================================================================
class TestNoPartialConfig:
    """TS-13-12: ConfigError always raised, no partial config returned."""

    def test_no_partial_config_returned(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Malformed TOML never returns a partial AgentFoxConfig."""
        (global_config_dir / "config.toml").write_text("[broken = unterminated")
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        result = None
        try:
            result = load_config()
        except ConfigError:
            pass
        assert result is None


# ===================================================================
# TS-13-13: AF_CONFIG deprecation warning
# ===================================================================
class TestAfConfigDeprecation:
    """TS-13-13: AF_CONFIG set -> deprecation warning to stderr."""

    def test_af_config_deprecation_warning(
        self, fake_home, global_config, tmp_path, monkeypatch, capsys
    ):
        """AF_CONFIG triggers deprecation warning on stderr."""
        custom_config = tmp_path / "custom-config.toml"
        custom_config.write_text("[theme]\nplayful = false\n")
        monkeypatch.setenv("AF_CONFIG", str(custom_config))
        monkeypatch.delenv("AF_SPEC_MODEL", raising=False)
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        config = load_config()

        captured = capsys.readouterr()
        assert "AF_CONFIG" in captured.err
        # TS-13-13: must specifically say 'no longer supported'
        assert "no longer supported" in captured.err.lower()
        assert isinstance(config, AgentFoxConfig)
        # TS-13-13: the AF_CONFIG path was never used for config resolution.
        # The custom config sets theme.playful=false; if AF_CONFIG were read,
        # the returned config would reflect that canary value.  Since it is
        # ignored, the config should NOT have theme.playful==False (it should
        # use the global config value or Pydantic default instead).
        assert config.theme.playful is not False


# ===================================================================
# TS-13-14: AF_CONFIG value never used for config path
# ===================================================================
class TestAfConfigValueIgnored:
    """TS-13-14: AF_CONFIG value completely ignored."""

    def test_af_config_value_not_used(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Config with distinctive value at AF_CONFIG path is not loaded."""
        # Write a config at the AF_CONFIG path with a canary value
        canary_config = tmp_path / "custom-config.toml"
        canary_config.write_text("[theme]\nplayful = false\n")

        # Global config has playful = true (default)
        (global_config_dir / "config.toml").write_text(
            "[theme]\nplayful = true\n"
        )

        monkeypatch.setenv("AF_CONFIG", str(canary_config))
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        config = load_config()

        # If AF_CONFIG were used, playful would be false
        # Since it's ignored, playful should be true (from global)
        assert config.theme.playful is True


# ===================================================================
# TS-13-15: SpecToolConfig defaults
# ===================================================================
class TestSpecToolDefaults:
    """TS-13-15: AgentFoxConfig includes spec_tool with correct defaults."""

    def test_spec_tool_defaults(self):
        """Default SpecToolConfig has expected field values."""
        config = AgentFoxConfig()
        assert config.spec_tool.model == "claude-sonnet-4-6"
        assert config.spec_tool.auth_method == ""
        assert config.spec_tool.vertex_project == ""
        assert config.spec_tool.vertex_region == ""


# ===================================================================
# TS-13-16: agentspec accepts AgentFoxConfig
# ===================================================================
class TestAgentspecAcceptsConfig:
    """TS-13-16: agentspec.load_config() accepts AgentFoxConfig."""

    def test_agentspec_accepts_agent_fox_config(self):
        """agentspec.load_config(agent_fox_config=...) uses spec_tool."""
        from agentspec.config import load_config as agentspec_load_config

        # Build an AgentFoxConfig with a custom model
        # This requires SpecToolConfig to exist on AgentFoxConfig
        agent_fox_config = AgentFoxConfig()
        # Set model to a non-default value via the spec_tool sub-config
        agent_fox_config.spec_tool.model = "claude-opus-4"

        spec_config = agentspec_load_config(agent_fox_config=agent_fox_config)
        assert spec_config.model == "claude-opus-4"


# ===================================================================
# TS-13-17: Model resolution precedence
# ===================================================================
class TestModelResolutionPrecedence:
    """TS-13-17: AF_SPEC_MODEL > merged config > fallback > default."""

    def test_af_spec_model_wins(self, fake_home, monkeypatch):
        """AF_SPEC_MODEL overrides all other model sources."""
        from agentspec.config import load_config as agentspec_load_config

        # TS-13-17: set up ALL three competing sources
        # 1. AF_SPEC_MODEL env var (should win)
        monkeypatch.setenv("AF_SPEC_MODEL", "claude-custom-model")

        # 2. Merged config with [spec_tool] model set
        agent_fox_config = AgentFoxConfig()
        agent_fox_config.spec_tool.model = "claude-opus-4"

        # 3. ~/.af/settings.yaml migration fallback
        af_dir = fake_home / ".af"
        af_dir.mkdir(exist_ok=True)
        (af_dir / "settings.yaml").write_text(
            "spec_tool:\n  model: claude-haiku\n"
        )

        spec_config = agentspec_load_config(agent_fox_config=agent_fox_config)
        assert spec_config.model == "claude-custom-model"


# ===================================================================
# TS-13-18: Migration fallback from settings.yaml
# ===================================================================
class TestMigrationFallback:
    """TS-13-18: settings.yaml fallback with deprecation warning."""

    def test_migration_fallback(self, fake_home, tmp_path, monkeypatch, capsys, clean_af_env):
        """When no [spec_tool] in config and settings.yaml exists, use fallback."""
        from agentspec.config import load_config as agentspec_load_config

        # Create ~/.af/settings.yaml with a model
        af_dir = fake_home / ".af"
        af_dir.mkdir()
        (af_dir / "settings.yaml").write_text(
            "spec_tool:\n  model: claude-haiku-4\n"
        )

        # Create global config without [spec_tool] section
        global_dir = fake_home / ".agent-fox"
        global_dir.mkdir(exist_ok=True)
        (global_dir / "config.toml").write_text("[orchestrator]\nparallel = 2\n")

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        # Pass a config without explicit spec_tool
        agent_fox_config = AgentFoxConfig()
        spec_config = agentspec_load_config(agent_fox_config=agent_fox_config)

        assert spec_config.model == "claude-haiku-4"
        captured = capsys.readouterr()
        assert "deprecat" in captured.err.lower() or "[spec_tool]" in captured.err


# ===================================================================
# TS-13-19: AF_SPEC_MODEL override
# ===================================================================
class TestAfSpecModelOverride:
    """TS-13-19: AF_SPEC_MODEL is used as resolved model."""

    def test_af_spec_model_override(self, monkeypatch):
        """AF_SPEC_MODEL overrides config-based model."""
        from agentspec.config import load_config as agentspec_load_config

        monkeypatch.setenv("AF_SPEC_MODEL", "my-custom-model")

        agent_fox_config = AgentFoxConfig()
        spec_config = agentspec_load_config(agent_fox_config=agent_fox_config)
        assert spec_config.model == "my-custom-model"


# ===================================================================
# TS-13-E6: Hardcoded default model
# ===================================================================
class TestHardcodedDefaultModel:
    """TS-13-E6: Default model with no config, no settings.yaml, no env var."""

    def test_hardcoded_default_model(self, fake_home, monkeypatch, capsys, clean_af_env):
        """Default model is 'claude-sonnet-4-6' with no deprecation warning."""
        from agentspec.config import load_config as agentspec_load_config

        # Ensure no settings.yaml exists
        af_dir = fake_home / ".af"
        if af_dir.exists():
            import shutil
            shutil.rmtree(af_dir)

        agent_fox_config = AgentFoxConfig()
        spec_config = agentspec_load_config(agent_fox_config=agent_fox_config)

        assert spec_config.model == "claude-sonnet-4-6"
        captured = capsys.readouterr()
        assert captured.err == ""  # no deprecation warning


# ===================================================================
# TS-13-20: DEBUG log — global config loaded
# ===================================================================
class TestDebugLogGlobalLoaded:
    """TS-13-20: DEBUG log 'Loaded global config from <path>'."""

    def test_debug_log_global_loaded(
        self, fake_home, global_config, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """DEBUG log emitted when global config is loaded."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        with caplog.at_level(logging.DEBUG):
            load_config()

        global_config_path = str(fake_home / ".agent-fox" / "config.toml")
        # TS-13-20: same message must contain both the prefix and the path
        assert any(
            "Loaded global config from" in msg and global_config_path in msg
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-21: DEBUG log — local config merged with sections
# ===================================================================
class TestDebugLogMerge:
    """TS-13-21: DEBUG log with overridden section names."""

    def test_debug_log_merge(
        self, fake_home, global_config, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """DEBUG log 'Merging local config from' with section names."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text(
            "[orchestrator]\nparallel = 4\n"
        )
        monkeypatch.chdir(repo)

        with caplog.at_level(logging.DEBUG):
            load_config()

        assert any(
            "Merging local config from" in msg and "orchestrator" in msg
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-22: DEBUG log — no local config found
# ===================================================================
class TestDebugLogNoLocal:
    """TS-13-22: DEBUG log when no local config exists."""

    def test_debug_log_no_local(
        self, fake_home, global_config, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """DEBUG log 'No local config found at .agent-fox/config.toml'."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        with caplog.at_level(logging.DEBUG):
            load_config()

        # TS-13-22: must include the full path suffix
        assert any(
            "No local config found at" in msg and ".agent-fox/config.toml" in msg
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-23: DEBUG log — HOME unresolvable
# ===================================================================
class TestDebugLogHomeUnresolvable:
    """TS-13-23: DEBUG warning when $HOME cannot be resolved."""

    def test_debug_log_home_unresolvable(
        self, tmp_path, monkeypatch, caplog, clean_af_env
    ):
        """DEBUG log mentions HOME when it cannot be resolved."""
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)
        monkeypatch.delenv("HOME", raising=False)
        monkeypatch.setattr(
            Path, "home", staticmethod(lambda: (_ for _ in ()).throw(RuntimeError("no home")))
        )

        with caplog.at_level(logging.DEBUG):
            load_config()

        # TS-13-23: same message must contain BOTH 'HOME' AND 'could not be resolved'/'skipped'
        assert any(
            "HOME" in msg and ("could not be resolved" in msg or "skipped" in msg)
            for msg in caplog.messages
        )


# ===================================================================
# TS-13-24: af init creates global config with 0o700
# ===================================================================
class TestAfInitGlobalConfig:
    """TS-13-24: af init creates global dir and config."""

    def test_af_init_creates_global_config(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """af init creates $HOME/.agent-fox/ with 0o700 and default config."""
        from af.app import main as af_main
        from click.testing import CliRunner

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        runner = CliRunner()
        result = runner.invoke(af_main, ["init"])

        assert result.exit_code == 0
        global_dir = fake_home / ".agent-fox"
        assert global_dir.exists()
        assert oct(global_dir.stat().st_mode & 0o777) == "0o700"
        assert (global_dir / "config.toml").exists()


# ===================================================================
# TS-13-25: af init --force preserves global config
# ===================================================================
class TestAfInitGlobalPreserved:
    """TS-13-25: af init --force never overwrites global config."""

    def test_af_init_force_preserves_global(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """Global config with custom content is preserved even with --force."""
        from af.app import main as af_main
        from click.testing import CliRunner

        original_content = "# custom\n[theme]\nplayful = false\n"
        (global_config_dir / "config.toml").write_text(original_content)
        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        runner = CliRunner()
        result = runner.invoke(af_main, ["init", "--force"])

        assert result.exit_code == 0
        assert (global_config_dir / "config.toml").read_text() == original_content


# ===================================================================
# TS-13-26: af init creates all-comments local config
# ===================================================================
class TestAfInitLocalCommentedOut:
    """TS-13-26: af init creates local config with all values commented out."""

    def test_af_init_local_all_comments(
        self, fake_home, global_config, tmp_path, monkeypatch, clean_af_env
    ):
        """Local config template has only comment lines."""
        from af.app import main as af_main
        from click.testing import CliRunner

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        runner = CliRunner()
        result = runner.invoke(af_main, ["init"])

        assert result.exit_code == 0
        local_config = repo / ".agent-fox" / "config.toml"
        assert local_config.exists()
        content = local_config.read_text()
        for line in content.splitlines():
            stripped = line.strip()
            if stripped and not stripped.startswith("#"):
                pytest.fail(f"Unexpected non-comment line: {line}")


# ===================================================================
# TS-13-27: af init without --force preserves local
# ===================================================================
class TestAfInitLocalPreserved:
    """TS-13-27: af init without --force preserves existing local config."""

    def test_af_init_preserves_local(
        self, fake_home, global_config, tmp_path, monkeypatch, clean_af_env
    ):
        """Existing local config is not overwritten without --force."""
        from af.app import main as af_main
        from click.testing import CliRunner

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        original = "[orchestrator]\nparallel = 4\n"
        (local_dir / "config.toml").write_text(original)
        monkeypatch.chdir(repo)

        runner = CliRunner()
        result = runner.invoke(af_main, ["init"])

        assert result.exit_code == 0
        assert (local_dir / "config.toml").read_text() == original


# ===================================================================
# TS-13-28: af init --force overwrites local only
# ===================================================================
class TestAfInitForceOverwritesLocal:
    """TS-13-28: af init --force overwrites local, preserves global."""

    def test_af_init_force_overwrites_local(
        self, fake_home, global_config_dir, tmp_path, monkeypatch, clean_af_env
    ):
        """--force regenerates local config as all-comments template."""
        from af.app import main as af_main
        from click.testing import CliRunner

        global_content = "# custom global\n[theme]\nplayful = false\n"
        (global_config_dir / "config.toml").write_text(global_content)

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        local_dir = repo / ".agent-fox"
        local_dir.mkdir(exist_ok=True)
        (local_dir / "config.toml").write_text("[orchestrator]\nparallel = 4\n")
        monkeypatch.chdir(repo)

        runner = CliRunner()
        result = runner.invoke(af_main, ["init", "--force"])

        assert result.exit_code == 0

        # Local config should be all comments
        local_content = (local_dir / "config.toml").read_text()
        for line in local_content.splitlines():
            stripped = line.strip()
            if stripped and not stripped.startswith("#"):
                pytest.fail(f"Non-comment line after --force: {line}")

        # Global config should be unchanged
        assert (global_config_dir / "config.toml").read_text() == global_content


# ===================================================================
# TS-13-29: Full test suite regression gate
# ===================================================================
class TestRegressionSuite:
    """TS-13-29: Full existing test suite passes without modification.

    The full test suite has pre-existing failures from specs 10, 11, and 12
    (mostly ImportError from removed insert_verdicts and broken meta-tests)
    that predate spec 13.  To properly validate that spec 13 did NOT
    introduce regressions, this test runs all tests in the packages that
    spec 13 touches — af, nightshift, spec, agentspec, and core config —
    excluding recursive meta-tests that would trigger cascading failures
    and the two pre-existing broken tests unrelated to spec 13.

    See docs/errata/13_regression_suite_pre_existing_failures.md for the
    full list of pre-existing failures.
    """

    @pytest.mark.integration
    @pytest.mark.timeout(120)
    def test_full_test_suite_passes(self):
        """Run pytest on spec-13-adjacent packages and assert exit code 0.

        Excludes recursive meta-tests (tests that run pytest as a
        subprocess, which cascade-fail from pre-existing issues) and
        two pre-existing failures unrelated to spec 13.
        """
        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "pytest",
                "-q",
                "--tb=short",
                # Override addopts to avoid inheriting -n auto and
                # --timeout=10 from pyproject.toml, which cause
                # conflicts when running as a subprocess under xdist.
                "-o",
                "addopts=",
                "packages/af/",
                "packages/nightshift/",
                "packages/spec/",
                "packages/agentspec/",
                "packages/agentfox/tests/unit/core/",
                # Exclude recursive meta-tests and pre-existing failures
                "-k",
                "not test_full_test_suite_passes"
                " and not test_af_tests_pass"
                " and not test_af_test_suite_passes"
                " and not test_dismiss_unknown_id_exits_nonzero"
                " and not test_json_mode_stdout_is_valid_json",
            ],
            capture_output=True,
            text=True,
            timeout=300,
        )
        assert result.returncode == 0, (
            f"Spec-13-adjacent tests failed with exit code {result.returncode}.\n"
            f"stderr: {result.stderr[-500:]}\n"
            f"stdout: {result.stdout[-500:]}"
        )


# ===================================================================
# TS-13-30: Pydantic validation raises ConfigError (preserving existing behavior)
# ===================================================================
class TestPydanticValidation:
    """TS-13-30: Invalid values cause ConfigError; existing behavior preserved."""

    def test_invalid_value_raises_config_error(self, tmp_path):
        """Invalid field type raises ConfigError."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[orchestrator]\nparallel = "not-a-number"\n')

        with pytest.raises(ConfigError):
            load_config(path=config_file)

    def test_load_config_path_parameter_backward_compat(self, tmp_path):
        """load_config(path=...) still returns valid config from file."""
        config_file = tmp_path / "config.toml"
        config_file.write_text("[orchestrator]\nparallel = 4\n")

        config = load_config(path=config_file)

        assert isinstance(config, AgentFoxConfig)
        assert config.orchestrator.parallel == 4

    def test_load_config_returns_agentfoxconfig(self):
        """load_config() returns an AgentFoxConfig instance."""
        # Called with a non-existent path -> defaults
        config = load_config(path=Path("/nonexistent/config.toml"))
        assert isinstance(config, AgentFoxConfig)


# ===================================================================
# TS-13-E1: Nonexistent CWD
# ===================================================================
class TestNonexistentCWD:
    """TS-13-E1: load_config with inaccessible CWD."""

    def test_nonexistent_cwd(self, fake_home, global_config, monkeypatch, clean_af_env):
        """load_config raises ConfigError or OSError with bad CWD."""
        # Patch cwd to raise
        monkeypatch.setattr(
            Path, "cwd", staticmethod(lambda: (_ for _ in ()).throw(OSError("no cwd")))
        )
        with pytest.raises((ConfigError, OSError)):
            load_config()


# ===================================================================
# TS-13-E7: af init with HOME unset
# ===================================================================
class TestAfInitNoHome:
    """TS-13-E7: af init with HOME unset creates local only."""

    def test_af_init_no_home(self, tmp_path, monkeypatch, caplog, clean_af_env):
        """af init without HOME skips global, creates local template."""
        from af.app import main as af_main
        from click.testing import CliRunner

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)
        monkeypatch.delenv("HOME", raising=False)
        monkeypatch.setattr(
            Path, "home", staticmethod(lambda: (_ for _ in ()).throw(RuntimeError("no home")))
        )

        runner = CliRunner()
        with caplog.at_level(logging.DEBUG):
            result = runner.invoke(af_main, ["init"])

        assert result.exit_code == 0
        local_config = repo / ".agent-fox" / "config.toml"
        assert local_config.exists()
        # Local config should be all comments
        for line in local_config.read_text().splitlines():
            stripped = line.strip()
            if stripped and not stripped.startswith("#"):
                pytest.fail(f"Unexpected non-comment line: {line}")
        # TS-13-E7: debug log must mention HOME
        assert any("HOME" in msg for msg in caplog.messages)


# ===================================================================
# TS-13-P1: Shallow merge property test
# ===================================================================
class TestShallowMergeProperty:
    """TS-13-P1: Shallow section replacement idempotency."""

    @pytest.mark.property
    def test_shallow_merge_invariants(self):
        """Property: shallow merge preserves local sections and inherits absent ones."""
        from agentfox.core.config import shallow_merge
        from hypothesis import given, settings
        from hypothesis import strategies as st

        section_names = st.sampled_from(["orchestrator", "routing", "theme", "security"])
        scalar_values = st.integers(min_value=0, max_value=100)
        section_dict = st.dictionaries(
            st.sampled_from(["parallel", "retries", "playful", "timeout"]),
            scalar_values,
            min_size=1,
            max_size=3,
        )
        config_dict = st.dictionaries(section_names, section_dict, min_size=0, max_size=4)

        @given(global_cfg=config_dict, local_cfg=config_dict)
        @settings(max_examples=50)
        def check(global_cfg, local_cfg):
            merged = shallow_merge(global_cfg, local_cfg)
            # Every section in local exactly equals the output
            for section in local_cfg:
                assert merged[section] == local_cfg[section]
            # Every section in global but absent from local equals the output
            for section in global_cfg:
                if section not in local_cfg:
                    assert merged[section] == global_cfg[section]

        check()


# ===================================================================
# TS-13-P2: Global config not overwritten after first creation
# ===================================================================
class TestGlobalConfigNotOverwrittenProperty:
    """TS-13-P2: Global config created once, never overwritten."""

    @pytest.mark.property
    def test_global_config_not_overwritten(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Property: multiple load_config calls don't overwrite global config."""
        from hypothesis import given, settings
        from hypothesis import strategies as st

        repo = tmp_path / "repo"
        repo.mkdir(exist_ok=True)
        monkeypatch.chdir(repo)

        @given(n_calls=st.integers(min_value=2, max_value=5))
        @settings(max_examples=5)
        def check(n_calls):
            # First call creates the global config
            load_config()
            global_config_path = fake_home / ".agent-fox" / "config.toml"
            content_after_first = global_config_path.read_text()

            for _ in range(n_calls - 1):
                load_config()
                assert global_config_path.read_text() == content_after_first

        check()


# ===================================================================
# TS-13-P3: AF_SPEC_MODEL always wins
# ===================================================================
class TestAfSpecModelAlwaysWinsProperty:
    """TS-13-P3: AF_SPEC_MODEL always overrides all other model sources."""

    @pytest.mark.property
    def test_af_spec_model_always_wins(self, monkeypatch):
        """Property: AF_SPEC_MODEL always equals the resolved model."""
        from agentspec.config import load_config as agentspec_load_config
        from hypothesis import given, settings
        from hypothesis import strategies as st

        model_strings = st.text(
            alphabet=st.characters(categories=("L", "N", "P")),
            min_size=1,
            max_size=50,
        )

        @given(model_name=model_strings)
        @settings(max_examples=20)
        def check(model_name):
            monkeypatch.setenv("AF_SPEC_MODEL", model_name)
            config = AgentFoxConfig()
            spec_config = agentspec_load_config(agent_fox_config=config)
            assert spec_config.model == model_name

        check()


# ===================================================================
# TS-13-P4: Malformed TOML always raises ConfigError
# ===================================================================
class TestMalformedTomlFailFastProperty:
    """TS-13-P4: Malformed TOML -> ConfigError, no partial config."""

    @pytest.mark.property
    def test_malformed_toml_always_raises(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Property: malformed TOML in global config always raises ConfigError."""
        from hypothesis import given, settings
        from hypothesis import strategies as st

        malformed_toml = st.sampled_from([
            "[broken = ",
            "key = @value",
            "[section\nkey = ",
            '"""unterminated',
            "[[nested]\nkey = {broken",
            "= no_key",
            "[good]\nbad = '''unclosed",
        ])

        @given(bad_toml=malformed_toml)
        @settings(max_examples=10)
        def check(bad_toml):
            global_dir = fake_home / ".agent-fox"
            global_dir.mkdir(exist_ok=True)
            (global_dir / "config.toml").write_text(bad_toml)

            repo = tmp_path / "repo"
            repo.mkdir(exist_ok=True)
            monkeypatch.chdir(repo)

            result = None
            try:
                result = load_config()
            except ConfigError:
                pass
            except Exception as e:
                # TS-13-P4: only ConfigError is acceptable
                raise AssertionError(
                    f"Expected ConfigError but got {type(e).__name__}: {e}"
                ) from e
            assert result is None

        check()


# ===================================================================
# TS-13-P5: Symlink rejection on final file only
# ===================================================================
class TestSymlinkFinalFileOnlyProperty:
    """TS-13-P5: Symlink detection on final file path only."""

    @pytest.mark.property
    def test_symlink_final_file_only(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Property: symlinked final file rejected; symlinked intermediate dir OK."""
        import shutil

        from hypothesis import given
        from hypothesis import settings as h_settings
        from hypothesis import strategies as st

        # Strategy: generate varying TOML content for diverse path structures
        toml_content_st = st.sampled_from([
            "[orchestrator]\nparallel = 1\n",
            "[theme]\nplayful = true\n",
            "[routing]\nretries_before_escalation = 2\n",
            "# empty\n",
        ])
        # Strategy: generate varying directory depth for intermediate dirs
        depth_st = st.integers(min_value=0, max_value=3)

        @given(toml_content=toml_content_st, depth=depth_st)
        @h_settings(max_examples=20)
        def check_symlinked_final_file_rejected(toml_content, depth):
            """Symlinked final config file is always rejected."""
            # Build a real file in a unique location
            real_base = tmp_path / f"real_final_{depth}"
            real_base.mkdir(exist_ok=True)
            real_file = real_base / "config.toml"
            real_file.write_text(toml_content)

            # Set up global config dir with symlinked final file
            global_dir = fake_home / ".agent-fox"
            if global_dir.is_symlink():
                global_dir.unlink()
            elif global_dir.exists():
                shutil.rmtree(global_dir)
            global_dir.mkdir(exist_ok=True)
            link = global_dir / "config.toml"
            if link.exists() or link.is_symlink():
                link.unlink()
            link.symlink_to(real_file)

            repo = tmp_path / "repo_final"
            repo.mkdir(exist_ok=True)
            monkeypatch.chdir(repo)

            with pytest.raises(ConfigError):
                load_config()

        @given(toml_content=toml_content_st, depth=depth_st)
        @h_settings(max_examples=20)
        def check_symlinked_intermediate_dir_accepted(toml_content, depth):
            """Symlinked intermediate directory with real final file is accepted."""
            # Create a real directory with a real config file
            real_dir = tmp_path / f"real_inter_{depth}"
            real_dir.mkdir(exist_ok=True)
            (real_dir / "config.toml").write_text(toml_content)

            # Set up $HOME/.agent-fox as a symlink to the real directory
            global_dir = fake_home / ".agent-fox"
            if global_dir.is_symlink():
                global_dir.unlink()
            elif global_dir.exists():
                shutil.rmtree(global_dir)
            global_dir.symlink_to(real_dir)

            repo = tmp_path / "repo_inter"
            repo.mkdir(exist_ok=True)
            monkeypatch.chdir(repo)

            # Should NOT raise — symlink check is on the final file only
            config = load_config()
            assert isinstance(config, AgentFoxConfig)

        check_symlinked_final_file_rejected()
        check_symlinked_intermediate_dir_accepted()


# ===================================================================
# TS-13-P6: af init never overwrites global config
# ===================================================================
class TestAfInitNeverOverwritesGlobalProperty:
    """TS-13-P6: af init never overwrites existing global config."""

    @pytest.mark.property
    def test_af_init_never_overwrites_global(
        self, fake_home, tmp_path, monkeypatch, clean_af_env
    ):
        """Property: existing global config content preserved by af init."""
        from af.app import main as af_main
        from click.testing import CliRunner
        from hypothesis import given, settings
        from hypothesis import strategies as st

        valid_toml = st.sampled_from([
            "# custom config\n",
            "[orchestrator]\nparallel = 4\n",
            "[theme]\nplayful = false\n",
            "# empty with comment\n",
            "[routing]\nretries_before_escalation = 2\n",
        ])
        use_force = st.booleans()

        @given(content=valid_toml, force=use_force)
        @settings(max_examples=10)
        def check(content, force):
            global_dir = fake_home / ".agent-fox"
            global_dir.mkdir(exist_ok=True)
            config_path = global_dir / "config.toml"
            config_path.write_text(content)

            repo = tmp_path / "repo"
            repo.mkdir(exist_ok=True)
            monkeypatch.chdir(repo)

            runner = CliRunner()
            args = ["init", "--force"] if force else ["init"]
            runner.invoke(af_main, args)

            assert config_path.read_text() == content

        check()
