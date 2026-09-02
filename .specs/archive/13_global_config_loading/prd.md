---
spec_id: '13'
spec_name: global_config_loading
title: Global Config Loading
status: draft
created_at: '2026-06-25T12:06:07.982836+00:00'
updated_at: '2026-06-25T12:11:07.266283+00:00'
owner: mkuehl
source: https://github.com/agent-fox-dev/agent-fox/issues/633
schema_version: 1
---
# Global Configuration Loading

## Intent

Enable users to maintain a single shared configuration baseline across all repositories, eliminating repetitive per-project setup, and unify all configuration surfaces (global, local, and agentspec) into a single, consistent loading scheme.

## Background

Currently, configuration is fragmented across three independent surfaces:

1. **Per-repo config** — `.agent-fox/config.toml` loaded by `af` and `spec` on each invocation.
2. **`AF_CONFIG` env var** — used by `nightshift` to override the config path entirely.
3. **`~/.af/settings.yaml`** — used by `agentspec` for model and authentication settings, in a different format (YAML) and a different directory from the main config.

Users working across multiple repositories must duplicate common settings (orchestrator parallelism, theme, routing, pricing) in every project's config file. There is no shared baseline, so each new repository starts from scratch. The `agentspec` settings use a separate format and location from the main config, creating confusion about where settings live and making it difficult to manage configuration holistically. This feature resolves all three pain points by introducing a single user-wide global config that all CLIs share, with a consistent local-override mechanism.

## Overview

Add a central, shared configuration file at `$HOME/.agent-fox/config.toml` that provides baseline configuration shared across multiple repositories. Local per-repo config at `.agent-fox/config.toml` overrides global values using shallow section replacement semantics. All three CLIs (`af`, `nightshift`, and `spec`) use the same config loading function, and `agentspec` settings are consolidated into the main config TOML.

## Goals

- **No regressions:** The existing test suite passes without modification. All current CLI behaviors are preserved under the new loading scheme.
- **Consistent config loading:** All three CLIs (`af`, `nightshift`, `spec`) load configuration using the same `load_config()` function with the same global+local merge logic.
- **Automatic migration:** Existing `~/.af/settings.yaml` users are automatically migrated via the fallback mechanism with no manual steps required.
- **Single config surface:** Users can manage all settings (including agentspec model/auth) from a single `$HOME/.agent-fox/config.toml` file.

## User Stories

**US-1: Global baseline configuration.** As a user working across multiple repos, I want a single global config so I don't repeat orchestrator, theme, and routing settings in every project.

**US-2: Local overrides.** As a user, I want a per-repo config that overrides specific sections of the global config so each project can customize behavior without affecting others.

**US-3: Zero-config bootstrap.** As a new user running `af` for the first time, I want a sensible default global config created automatically so I don't need to set anything up manually.

**US-4: Project scaffolding.** As a user running `af init`, I want a local config template with all values commented out so I can see what's customizable and selectively override global settings.

## Desired Behaviour

### Config Loading (all CLIs)

1. On start-up, `af`, `nightshift`, and `spec` all call the same config loading function.
2. If `$HOME` cannot be resolved (e.g. in certain CI environments), global config loading is **skipped entirely** — the CLI falls back to the local config (if present) or in-memory defaults, and emits a debug-level warning that `$HOME` could not be resolved. The CLI does **not** fail fast in this case, as `$HOME`-less environments (such as restricted CI containers) are a known and supported use case.
3. If `$HOME` is resolvable, the loader looks for a global config at `$HOME/.agent-fox/config.toml`. If it exists, it is parsed and provides the baseline configuration.
4. The loader then looks for a local config at `.agent-fox/config.toml` (relative to the current working directory). If it exists, it is parsed and merged with the global config.
5. **Merge semantics are shallow section replacement:** if the local config defines a TOML section (e.g. `[orchestrator]`), that entire section replaces the corresponding section from the global config. Sections not present in the local config are inherited from the global config unchanged. Top-level scalar keys (keys not belonging to any section) follow the same rule: a top-level scalar key present in the local config overrides the same key from the global config; top-level scalar keys absent from the local config are inherited from the global config unchanged. Example: if the global config defines `theme = "dark"` at the top level and the local config does not include `theme`, the merged result has `theme = "dark"`.
6. If neither file exists (and `$HOME` is resolvable), a default global config is created at `$HOME/.agent-fox/config.toml` (creating the `$HOME/.agent-fox/` directory if needed, with owner-only permissions `0o700`) and used.
7. After merging, the result is validated and defaults applied as today.
8. Security: both global and local config files reject symlinks on the **final config file path** only (matching the current CWE-59 protection behavior). Symlink checks are not applied to intermediate directories in the path.
9. A debug-level log line is emitted for each config file found, skipped, or merged, including which sections were overridden by the local config. For example:
   - `DEBUG: Loaded global config from /home/user/.agent-fox/config.toml`
   - `DEBUG: Merging local config from .agent-fox/config.toml (sections overridden: [orchestrator])`
   - `DEBUG: No local config found at .agent-fox/config.toml, using global config only`

### Error Handling

- If a global or local config file exists but contains **malformed TOML**, the CLI **fails fast** with a clear error message (`ConfigError`) identifying the offending file and the parse error. No fallback or partial load is attempted.
- This matches the existing behavior of `load_config()`, which already raises `ConfigError` on malformed TOML.
- If the global config is malformed, the CLI exits with an error before any local config is read.
- If the local config is malformed, the CLI exits with an error after the global config has been successfully loaded.
- Both files must be valid TOML if they exist.

### `AF_CONFIG` Environment Variable

- Support for the `AF_CONFIG` environment variable is **removed**. The nightshift CLI currently uses `AF_CONFIG` to override the config path; this override is dropped in favour of the global+local loading scheme.
- **Deprecation warning:** If `AF_CONFIG` is set in the environment, the CLI prints a warning to `stderr` explaining that the variable is no longer supported and directing the user to move their settings to `$HOME/.agent-fox/config.toml` (global) or `.agent-fox/config.toml` (local). The variable is then **ignored**.

### agentspec Settings Consolidation

- The `~/.af/settings.yaml` file used by the `agentspec` package is consolidated into `$HOME/.agent-fox/config.toml`.
- A new `[spec_tool]` section is added to `AgentFoxConfig` with the following fields:
  - `model` (str, default `"claude-sonnet-4-6"`) — the Anthropic model used for spec generation.
  - `auth_method` (str, default `""`) — authentication method (e.g. `"vertex"`). Empty string means default Anthropic API key auth.
  - `vertex_project` (str, default `""`) — Google Cloud project for Vertex AI.
  - `vertex_region` (str, default `""`) — Google Cloud region for Vertex AI.
- The `agentspec` package's `load_config()` is updated to accept an `AgentFoxConfig` (or its `spec_tool` sub-config) instead of reading `~/.af/settings.yaml` directly.
- **`spec_tool` model resolution precedence (highest to lowest):**
  1. `AF_SPEC_MODEL` environment variable — always takes highest precedence as a backward-compatible escape hatch.
  2. `[spec_tool].model` from the merged config (global + local).
  3. `~/.af/settings.yaml` fallback (migration path, see below).
  4. Hardcoded default (`"claude-sonnet-4-6"`).
- **`~/.af/settings.yaml` migration fallback (temporary):** If `$HOME/.agent-fox/config.toml` has no `[spec_tool]` section and `~/.af/settings.yaml` exists, its values are used. When this fallback is triggered, a deprecation warning is emitted to `stderr` instructing users to migrate their settings to `$HOME/.agent-fox/config.toml` under the `[spec_tool]` section. This fallback has no fixed removal date but is considered temporary; a specific removal milestone will be announced when decided. This avoids breaking existing setups immediately.

### Generated Config File Formats

- **Global config (`$HOME/.agent-fox/config.toml`):** Generated using the same template as the current `generate_default_config()` behavior — a minimal set of promoted defaults written as active (uncommented) values, with remaining options documented in `config-reference.md`. This matches the existing `af init` global config generation.
- **Local config template (`.agent-fox/config.toml`):** Generated with all possible config values **commented out**, allowing users to selectively uncomment and override specific settings.

### Changes to `af init`

1. `af init` creates a default global config at `$HOME/.agent-fox/config.toml` if it does not exist. An existing global config is **not modified** (even when `--force` is used).
2. `af init` creates a local config at `.agent-fox/config.toml` with all possible config values **commented out**. An existing local config is **not modified** unless `--force` is specified.
3. `af init --force` **overwrites only** the local config template at `.agent-fox/config.toml`, regenerating it with all values commented out. The global config at `$HOME/.agent-fox/config.toml` is never overwritten by `af init`, with or without `--force`.
4. The `$HOME/.agent-fox/` directory is auto-created with owner-only permissions (`0o700`) if it doesn't exist.

## Acceptance Criteria

- [ ] **AC-1:** Running any of `af`, `nightshift`, or `spec` in a directory with no local config and no global config auto-creates `$HOME/.agent-fox/config.toml` with correct defaults and succeeds.
- [ ] **AC-2:** Running any CLI with a valid global config and no local config uses the global config values without error.
- [ ] **AC-3:** Running any CLI with both a global and local config applies shallow section replacement correctly — local sections override global sections, absent local sections inherit global values, and top-level scalar keys present in the local config override the same keys from the global config.
- [ ] **AC-4:** Running any CLI with a malformed global or local config exits with a `ConfigError` and a message identifying the file and parse error.
- [ ] **AC-5:** If `AF_CONFIG` is set, a deprecation warning is printed to `stderr` and the variable is ignored; the global+local loading scheme proceeds normally.
- [ ] **AC-6:** `af init` with no existing configs creates both `$HOME/.agent-fox/config.toml` (active defaults) and `.agent-fox/config.toml` (all values commented out).
- [ ] **AC-7:** `af init` with existing local config does not modify the local config unless `--force` is passed.
- [ ] **AC-8:** `af init --force` regenerates the local config template but does not modify or create the global config if it already exists.
- [ ] **AC-9:** If `$HOME/.agent-fox/config.toml` has no `[spec_tool]` section and `~/.af/settings.yaml` exists, the `agentspec` package uses values from `~/.af/settings.yaml` as a fallback and emits a deprecation warning to `stderr`.
- [ ] **AC-10:** The full existing test suite passes without regressions.
- [ ] **AC-11:** `AF_SPEC_MODEL` env var takes precedence over `[spec_tool].model` in the merged config, which takes precedence over the `~/.af/settings.yaml` fallback, which takes precedence over the hardcoded default.
- [ ] **AC-12:** When `$HOME` is not resolvable, the CLI skips global config loading, emits a debug-level warning, and continues using only local config or in-memory defaults without failing.
- [ ] **AC-13:** Debug-level log lines are emitted identifying each config file found, skipped, or merged, and listing which sections were overridden by the local config.
- [ ] **AC-14:** Symlink rejection applies only to the final config file path (not intermediate directories) for both global and local config paths.

## Non-Goals

- Deep-merge of nested keys within a section. Sections are replaced wholesale.
- GUI or interactive config editor.
- Config file watching / hot-reload of global config.
- Overwriting the global config via `af init` (even with `--force`).
- Applying symlink rejection to intermediate directories in the global or local config paths.

## Tech Stack

- Python 3.12+
- TOML parsing via `tomllib` (stdlib) for reading, `tomlkit` for generation
- Pydantic v2 for validation
- Click for CLI

## Glossary

| Term | Definition |
|------|-----------|
| Global config | `$HOME/.agent-fox/config.toml` — user-wide baseline |
| Local config | `.agent-fox/config.toml` — per-repo overrides |
| Shallow section replacement | Local TOML sections entirely replace the corresponding global section; top-level scalar keys in the local config override matching keys in the global config |
| ConfigError | Exception raised by `load_config()` on malformed TOML or validation failure |
| Migration fallback | Temporary mechanism that reads `~/.af/settings.yaml` when no `[spec_tool]` section exists in the global config |

## Design Decisions

1. **Shallow section replacement over deep merge:** Shallow replacement is simpler to reason about and implement. Users who override `[orchestrator]` in a local config must specify all desired values for that section, not just the ones that differ. This avoids surprising merge artifacts in nested tables. The same logic applies to top-level scalar keys.

2. **Auto-create global config on every command:** Every `af`, `nightshift`, and `spec` invocation auto-creates the global config if missing (and `$HOME` is resolvable). This ensures zero-config bootstrap without requiring `af init` first.

3. **All three CLIs share the same loading:** `af`, `nightshift`, and `spec` all use the same `load_config()` function with the same global+local merge logic. This ensures consistent behaviour across all entry points.

4. **Drop `AF_CONFIG` with a deprecation warning:** The global+local scheme replaces the need for `AF_CONFIG`. Rather than silently breaking existing workflows, the variable is detected and a warning is printed to `stderr` directing users to the new config locations before the variable is ignored.

5. **Truly leave existing local config alone during `af init`:** Unlike the current behaviour which merges schema changes into existing configs, `af init` now skips the local config entirely if it exists. Users can re-generate it with `af init --force`, which overwrites only the local config template. The global config is never overwritten by `af init`.

6. **Consolidate `~/.af/settings.yaml` with temporary migration fallback:** Moving agentspec settings into the main config TOML gives users a single config surface. The migration fallback ensures existing setups continue working without any manual intervention. When the fallback is triggered, a deprecation warning is emitted to `stderr` to prompt users to migrate. The fallback has no fixed removal date but is considered temporary.

7. **Auto-create `$HOME/.agent-fox/` directory:** When auto-creating the global config, the directory is created with owner-only permissions (`0o700`) matching the existing security model.

8. **Fail fast on malformed TOML:** Both global and local configs must be valid TOML. Malformed files raise `ConfigError` immediately with a message identifying the file. This matches existing behavior and avoids silently loading partial or unexpected configuration.

9. **Graceful degradation when `$HOME` is unresolvable:** Rather than failing, the CLI skips global config loading and emits a debug-level warning. This ensures compatibility with restricted CI/CD environments where `$HOME` may not be set, at the cost of not providing a global config baseline.

10. **Debug-level observability for config loading:** Emitting debug-level log lines for each file found, skipped, or merged (including which sections were overridden) allows users and operators to diagnose configuration issues without requiring verbose output in normal operation.

11. **Symlink rejection on final file path only:** Symlink rejection (CWE-59 protection) is applied only to the final config file path, matching the current implementation for local configs. Intermediate directories are not checked, keeping the security model consistent and the implementation simple.

12. **`AF_SPEC_MODEL` precedence:** Environment variables always override file-based configuration. The explicit precedence order (`AF_SPEC_MODEL` > merged config > migration fallback > hardcoded default) is documented to avoid implementor ambiguity.

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/633
