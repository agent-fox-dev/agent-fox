# Erratum: Spec 13 — Global Config Loading

## 1. Test Spec Field Name Mismatches (TS-13-2, TS-13-7, TS-13-8, TS-13-9)

**Issue:** The test spec assertions reference non-existent field names on
`AgentFoxConfig`. Specifically:

- `config.orchestrator.parallelism` should be `config.orchestrator.parallel`
- `config.orchestrator.timeout` should be `config.orchestrator.session_timeout`
- `config.routing.strategy` does not exist; `RoutingConfig` has
  `retries_before_escalation`, `max_timeout_retries`, `timeout_multiplier`,
  and `timeout_ceiling_factor`
- `config.theme` is a `ThemeConfig` object (with fields like `playful`,
  `header`, etc.), not a string. Tests asserting `config.theme == 'light'`
  or `config.theme != 'canary'` cannot pass against the actual model.

**Resolution:** Tests use the actual field names:
- `orchestrator.parallel` (not `parallelism`)
- `orchestrator.session_timeout` (not `timeout`)
- `routing.retries_before_escalation` (not `routing.strategy`)
- `theme.playful` (a boolean) as the distinguishing assertion field

## 2. 13-REQ-9.1 Contradicts New Behaviors (AF_CONFIG, Symlink)

**Issue:** 13-REQ-9.1 states "the full existing test suite passes without
requiring any test file modifications." This directly contradicts:

- **13-REQ-5.1:** AF_CONFIG is now deprecated and ignored. But existing
  nightshift tests (`test_cli_behavior.py`) assert exit code 1 when
  `AF_CONFIG=/nonexistent`. Under the new behavior, AF_CONFIG is ignored so
  these tests will fail.
- **13-REQ-2.E1 / 13-REQ-3.E1:** Symlink config now raises `ConfigError`.
  But existing test `test_symlink_config_returns_defaults` asserts that
  `load_config(path=symlink)` silently returns defaults.

**Resolution:** Implementation should prioritize the behavioral requirements
(13-REQ-5.1, 13-REQ-2.E1, 13-REQ-3.E1) over the no-modification guarantee
(13-REQ-9.1). The affected existing tests must be updated to match the new
behavior. The test modifications are:
- `test_symlink_config_returns_defaults` -> assert `ConfigError` raised
- nightshift AF_CONFIG tests -> assert deprecation warning and success (not
  exit code 1)

## 3. spec_tool Explicit vs. Pydantic-Defaulted Tracking

**Issue:** The spec provides no mechanism for `agentspec.resolve_model()` to
distinguish whether `[spec_tool]` was explicitly configured vs.
Pydantic-defaulted. After `load_config()` validates the merged dict into
`AgentFoxConfig`, `spec_tool` is always a `SpecToolConfig` instance.

**Resolution:** `load_config()` should track which sections were explicitly
present in the merged raw dict and expose this information. Options include:
- Adding a `_explicitly_configured_sections: set[str]` attribute on the
  returned `AgentFoxConfig`
- Returning a tuple `(AgentFoxConfig, set[str])` from `load_config()`
- Using a sentinel value or metadata on the `spec_tool` field

The tests check for migration fallback behavior by verifying that when no
`[spec_tool]` section is in any config file, `agentspec` falls back to
`~/.af/settings.yaml`.

## 4. af init --force Flag

**Issue:** The current `af init` command has no `--force` flag. The spec
introduces entirely new behaviors including global config creation, an
all-comments local config template, and a `--force` flag.

**Resolution:** Tests for `af init --force` are written against the
specified behavior. Implementation must add the `--force` flag to the Click
command. Existing af init tests that assume the old merge-update behavior
may need updating.

## 5. spec CLI Conditional Config Loading

**Issue:** `spec/cli.py` only calls `load_config` when not in agent mode and
not quiet. In agent mode (`AF_AGENT=1`), no `AgentFoxConfig` is loaded.

**Resolution:** Implementation should ensure `load_config()` is always
called, or `agentspec` should call `load_config()` itself when no config
is passed. Tests assume `agentspec.load_config()` can be called with
`agent_fox_config=None` and will fall back to its own config loading.
