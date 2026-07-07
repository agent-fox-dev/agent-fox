# Erratum: Spec 02 — shallow_merge Not Used by Config Loading

## Context

02-REQ-3.4 and 02-REQ-3.5 describe `shallow_merge` as the mechanism by
which global and local TOML config sections are merged, including the new
`backend` field on `OrchestratorConfig`.

## Divergence

The actual `_load_config_global_local()` implementation in `core/config.py`
does **not** use `shallow_merge()`. It uses a single-source-wins strategy:

- If a local config (`.agent-fox/config.toml`) exists, it is the **sole**
  config source — the global config is not read at all.
- If no local config exists, only the global config
  (`~/.agent-fox/config.toml`) is loaded.

The `shallow_merge()` function exists in `config.py` and works correctly,
but it is not called by the config loading flow.

## Impact on Tests

- **TS-02-15** and **TS-02-16** test `shallow_merge()` directly by building
  dicts and calling the function, which works correctly. They do not test
  the `load_config()` integration path.
- The spec's description of "inheriting via shallow_merge" (02-REQ-3.4)
  describes behavior that does not occur in practice — the local TOML either
  overrides everything or doesn't exist.

## Resolution

Tests validate `shallow_merge()` in isolation (as the spec intended),
documenting the function's correct behavior. The divergence between the
spec's assumed config loading flow and the actual implementation is
acknowledged here. No code change is needed — the `backend` field works
correctly with the actual single-source-wins loading strategy because
pydantic supplies the default `'claude'` when the field is absent.
