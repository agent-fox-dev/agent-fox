# PRD: Remove Dead, Unused, and Deprecated Config Parameters

## Problem

The configuration system has accumulated dead code over multiple refactoring
cycles. Several config parameters are defined in Pydantic models but never read
by any runtime code. The deprecated `[models]` section still exists with its
backward-compatibility shim. Obsolete archetype key validators remain for keys
that no one uses in modern configs. The `config_gen.py` module carries bounds
and description metadata for `RoutingConfig` fields that were removed long ago.

Since we do not care about backward compatibility with old `config.toml` files,
all of this dead weight can be removed outright — no deprecation warnings, no
silent stripping, no migration guidance.

## Scope

Remove the following from the codebase, tests, and documentation:

### 1. Unused Config Parameters (defined but never consumed at runtime)

| Parameter | Location |
|-----------|----------|
| `quality_gate` | `OrchestratorConfig` in `config.py:150` |
| `quality_gate_timeout` | `OrchestratorConfig` in `config.py:154` |
| `memory_extraction` | `ModelConfig` in `config.py:192` |

### 2. Deprecated `[models]` Section

Remove `ModelConfig` entirely (`config.py:180-203`), including:
- The `coding` field and its `_warn_deprecated_coding()` validator
- The `memory_extraction` field
- The `models` field on `AgentFoxConfig` that references `ModelConfig`
- All `_SCHEMA_DEPRECATED_FIELDS` entries for `models.coding` in `config_gen.py`
- The `"models"` entry in `_VISIBLE_SECTIONS` in `config_gen.py`
- All `("ModelConfig", ...)` entries in `_DEFAULT_DESCRIPTIONS` and `_BOUNDS_MAP`

### 3. Obsolete Archetype Key Validators

Remove the validators in `ArchetypesConfig` that handle:
- **Deprecated keys** (silently stripped): `triage` → `maintainer:hunt`
- **Obsolete keys** (hard error): `skeptic`, `oracle`, `auditor`,
  `skeptic_config`, `skeptic_settings`, `oracle_settings`, `auditor_config`,
  `fix_reviewer`, `fix_coder`

These validators exist only to help users migrate old configs. Since we no
longer care about old configs, the validators and their error messages should
be removed.

### 4. Stale `config_gen.py` Metadata

Remove phantom entries for non-existent `RoutingConfig` fields:
- `training_threshold` in `_BOUNDS_MAP` and `_DEFAULT_DESCRIPTIONS`
- `accuracy_threshold` in `_BOUNDS_MAP` and `_DEFAULT_DESCRIPTIONS`
- `retrain_interval` in `_BOUNDS_MAP` and `_DEFAULT_DESCRIPTIONS`

Also remove `quality_gate`-related entries from:
- `_PROMOTED_DEFAULTS`
- `_PROMOTED_DEFAULTS_OVERRIDES`
- `_DEFAULT_DESCRIPTIONS`

### 5. Unused Audit Event

Remove `QUALITY_GATE_RESULT = "quality_gate.result"` from the `AuditEvent`
enum in `audit.py` — it is defined but never emitted.

### 6. Fix Incorrect Bounds

Update `drift_review_block_threshold` bounds in `_BOUNDS_MAP` from `">=1"` to
`">=1 or None"` to reflect that `None` is a valid value (advisory-only mode).

### 7. Documentation Cleanup

Update `docs/config-reference.md`:
- Remove the `[orchestrator]` rows for `quality_gate` and
  `quality_gate_timeout`, and their TOML example
- Remove the entire `## models` section
- Remove the "Obsolete keys" paragraph from `## archetypes`
- Remove the migration example comment from `## archetypes.overrides`
- Update the table of contents to remove the `models` link

### 8. Test Cleanup

Remove or update all tests that reference removed parameters:
- Tests asserting `quality_gate` template promotion
- Tests asserting `quality_gate_timeout` in config
- Tests asserting `memory_extraction` defaults
- Tests asserting `training_threshold`, `accuracy_threshold`,
  `retrain_interval` are absent from `RoutingConfig` (now vacuously true)
- Tests asserting the `QUALITY_GATE_RESULT` enum value
- Tests exercising the obsolete-key and deprecated-key validators

## Non-Goals

- Changing any runtime behavior — this is a pure dead-code removal.
- Removing references to old archetype names (`skeptic`, `oracle`, etc.) from
  ADRs, errata, or audit docs — those are historical records and should remain.
- Removing `quality_gates` from `fix/improve.py` — that is a verdict field,
  not a config parameter.

## Design Decisions

1. **No backward compatibility.** Old `config.toml` files that use `[models]`,
   `quality_gate`, or obsolete archetype keys will get standard Pydantic
   `extra='ignore'` behavior (silently ignored) rather than explicit errors or
   warnings. This is fine because `ConfigDict(extra="ignore")` is already set
   on all config models.

2. **Keep `extra='ignore'`** on config classes. This means old keys in existing
   `config.toml` files are silently ignored, not errored. No migration is needed.

3. **Historical docs stay.** ADRs, errata, and audit docs that mention old
   archetype names or `quality_gate` are historical artifacts and are not touched.

## Clarifications

1. **`quality_gates` in `fix/improve.py`** is a verdict field, not a config
   parameter — excluded from scope.
2. **Historical docs** (ADRs, errata, audit docs) referencing old archetype
   names or `quality_gate.result` are left untouched.
3. **`QUALITY_GATE_RESULT` audit event** is included in removal scope.
4. **Bounds fix for `drift_review_block_threshold`** is included as a minor
   correctness fix bundled with the cleanup.

## Source

Source: Input provided by user via interactive prompt
