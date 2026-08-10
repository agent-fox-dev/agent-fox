---
spec_id: '04'
spec_name: vendor_config
title: Vendor Workflow Configuration
status: draft
created_at: '2026-08-10T00:00:00+00:00'
updated_at: '2026-08-10T00:00:00+00:00'
owner: Michael Kuehl
source: docs/proposals/github_pr.md
schema_version: 1
---
# Vendor Workflow Configuration

## Summary

Add a `[vendor]` configuration section to `.agent-fox/config.toml` that enables
and controls the vendor-branch / carry-patch workflow. This section configures
the upstream remote, deploy branch naming, rebuild strategy, tagging behavior,
and rerere integration. The configuration is consumed by the patch stack module
(spec 03), vendor-sync stream (spec 06), fix pipeline adaptation (spec 07),
and vendor CLI (spec 08).

## Goals

1. **Single config section** -- all vendor-workflow settings live under
   `[vendor]` in the existing `.agent-fox/config.toml` file.
2. **Opt-in activation** -- the vendor workflow is disabled by default
   (`vendor.enabled = false`). Existing agent-fox users are unaffected.
3. **Pydantic validation** -- all fields are validated at config load time with
   clear error messages for invalid values.
4. **Config template generation** -- the `[vendor]` section appears in the
   generated config template (`af init`) with commented defaults, following
   the existing `config_gen.py` pattern.
5. **Zero regressions** -- existing configuration loading and validation is
   unaffected.

## Non-Goals

- **Migration tooling** -- no migration script for existing config files. The
  new section uses Pydantic defaults and is simply absent from old configs.
- **Runtime validation of upstream URL** -- the URL is validated structurally
  (non-empty string) but not tested for reachability at config load time.
  Reachability is checked at runtime by the vendor-sync stream.
- **Per-patch configuration** -- patch-level settings live in the patch manifest
  (spec 03), not in `config.toml`.

## Background

agent-fox configuration lives in `.agent-fox/config.toml` and is loaded into
Pydantic models defined in `packages/agentfox/agentfox/config.py`. The existing
structure includes `[workspace]`, `[nightshift]`, `[platform]`, `[knowledge]`,
and other sections. Each section maps to a Pydantic model with defaults.

The vendor workflow requires its own configuration that cuts across nightshift
(sync interval), workspace (deploy branch, rebuild strategy), and platform
(upstream remote). Grouping these under a dedicated `[vendor]` section avoids
polluting existing sections with vendor-specific concerns.

## Tech Stack

- **Language:** Python 3.12+
- **Config validation:** Pydantic v2
- **Config format:** TOML
- **Test framework:** pytest

## Functional Requirements

### FR-1: VendorConfig model

Add a `VendorConfig` Pydantic model to `config.py`:

```python
class VendorConfig(BaseModel):
    enabled: bool = False
    upstream_remote: str = "upstream"
    upstream_url: str = ""
    upstream_branch: str = "main"
    deploy_branch: str = "deploy"
    rebuild_strategy: Literal["merge-no-ff", "linear"] = "merge-no-ff"
    tag_rebuilds: bool = True
    tag_prefix: str = "deploy-"
    rerere_enabled: bool = True
    patch_manifest: str = ".agent-fox/patches.toml"
```

**Field semantics:**

- `enabled` -- master switch. When `False`, all vendor-related operations are
  no-ops. The vendor-sync stream does not start. The fix pipeline uses the
  standard integration path.
- `upstream_remote` -- name of the git remote pointing to the vendor/upstream
  repository. Created automatically by the vendor-sync stream if it does not
  exist.
- `upstream_url` -- URL of the upstream repository. Required when `enabled` is
  `True`. Validated: must be non-empty when enabled.
- `upstream_branch` -- branch name to track on the upstream remote.
- `deploy_branch` -- name of the integration/deploy branch that is rebuilt from
  the patch list. Must differ from `upstream_branch` when both refer to the
  same remote context.
- `rebuild_strategy` -- how patches are applied during rebuild. `"merge-no-ff"`
  preserves patch boundaries via merge commits. `"linear"` replays patches
  as individual commits (cherry-pick style).
- `tag_rebuilds` -- whether to create a git tag after each successful rebuild.
- `tag_prefix` -- prefix for rebuild tags. Combined with date:
  `{tag_prefix}{YYYY-MM-DD}`.
- `rerere_enabled` -- whether to enable `git rerere` for conflict resolution
  caching. When `True`, the vendor-sync stream enables rerere in the repo
  config at startup.
- `patch_manifest` -- path to the patch manifest file, relative to repo root.

### FR-2: Validation rules

**Cross-field validation** (Pydantic `model_validator`):

1. When `enabled = True`, `upstream_url` must be non-empty. Raise
   `ValidationError` with message: `"vendor.upstream_url is required when vendor is enabled"`.
2. `deploy_branch` must not equal `upstream_branch`. Raise `ValidationError`
   with message: `"vendor.deploy_branch must differ from vendor.upstream_branch"`.
3. `tag_prefix` must be a valid ref name component (no spaces, colons, tildes,
   etc. -- same constraints as `validate_ref_name` but without the leading-dash
   check since it's a prefix).

### FR-3: Integration into root config

Add `vendor: VendorConfig = VendorConfig()` to the root `AgentFoxConfig` model
(or equivalent top-level config class). The field defaults to a disabled
`VendorConfig`, making it fully backward compatible.

### FR-4: NightShiftConfig extension

Add the following field to `NightShiftConfig`:

```python
vendor_sync_interval: int = 900  # seconds (15 minutes, matches poll_interval default)
```

This controls how frequently the vendor-sync stream checks for upstream changes.
It is separate from `poll_interval` because vendor sync and issue polling may
have different cadences.

### FR-5: Config template generation

Update `config_gen.py` to include the `[vendor]` section in the generated
config template. Following the existing pattern:

1. Add `"vendor"` to `_VISIBLE_SECTIONS`.
2. Add vendor fields to `_PROMOTED_DEFAULTS` with their default values and
   comments.

The generated template should look like:

```toml
# [vendor]
# enabled = false
# upstream_remote = "upstream"
# upstream_url = ""
# upstream_branch = "main"
# deploy_branch = "deploy"
# rebuild_strategy = "merge-no-ff"
# tag_rebuilds = true
# tag_prefix = "deploy-"
# rerere_enabled = true
# patch_manifest = ".agent-fox/patches.toml"
```

### FR-6: Config documentation

Update `docs/config-reference.md` with the new `[vendor]` section. Document
each field with its type, default, and purpose.

## Non-Functional Requirements

- **Backward compatibility** -- existing config files without a `[vendor]`
  section load successfully with `enabled = False`.
- **Test coverage** -- >=90% line coverage on `VendorConfig` model, validation
  rules, and config generation.
- **No runtime cost when disabled** -- when `vendor.enabled = False`, no
  additional git operations, file reads, or API calls occur.

## Design Decisions

1. **Dedicated `[vendor]` section.** Rather than adding vendor fields to
   `[workspace]` or `[nightshift]`, a dedicated section keeps vendor-specific
   configuration grouped and discoverable. It also makes it trivial to check
   whether vendor mode is active (`config.vendor.enabled`).

2. **`upstream_url` required only when enabled.** This avoids forcing users who
   don't use vendor mode to provide an upstream URL. The validation is
   cross-field (Pydantic `model_validator`) rather than field-level.

3. **Separate `vendor_sync_interval`.** Vendor sync (checking for upstream
   changes) and issue polling (checking for `af:fix` issues) serve different
   purposes and may run at different frequencies. A separate interval avoids
   coupling them.

4. **`patch_manifest` path is configurable.** While the default
   `.agent-fox/patches.toml` is conventional, allowing configuration supports
   non-standard project layouts or multiple patch manifests.

5. **`tag_prefix` defaults to `"deploy-"`.** This follows the convention in
   the proposal (`deploy-YYYY-MM-DD`) and is general enough for most use cases.
   Custom prefixes support teams with different tagging conventions.

## Dependencies

| Dependency | Type | Notes |
|---|---|---|
| `agentfox/config.py` | Internal | Extended with `VendorConfig` model |
| `agentfox/config_gen.py` | Internal | Updated for template generation |
| `docs/config-reference.md` | Internal | Updated documentation |
| Spec 03 (patch_stack_module) | Downstream | Consumes `patch_manifest` path |
| Spec 05 (rerere_integration) | Downstream | Consumes `rerere_enabled` flag |
| Spec 06 (vendor_sync_stream) | Downstream | Consumes all vendor config fields |
| Spec 07 (vendor_fix_pipeline) | Downstream | Consumes `enabled`, `deploy_branch` |
