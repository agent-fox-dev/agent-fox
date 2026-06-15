# Design Document: Config Dead Code Removal

## Overview

Pure deletion spec — removes unused, deprecated, and obsolete configuration
parameters from the codebase, config generation metadata, documentation, and
tests. No new code is introduced. All config models already use
`ConfigDict(extra="ignore")`, so existing `config.toml` files with removed
keys continue to load without errors.

## Architecture

No architectural changes. This spec only deletes code and metadata.

### Module Responsibilities

1. `agent_fox/core/config.py` — Config Pydantic models (deletions only)
2. `agent_fox/core/config_gen.py` — Config template generation metadata (deletions + one bounds fix)
3. `agent_fox/knowledge/audit.py` — Audit event enum (one member deletion)
4. `docs/config-reference.md` — User-facing config documentation (section removal)

## Execution Paths

### Path 1: Config loading with removed fields

1. `agent_fox/core/config.py: load_config()` — loads TOML, calls `AgentFoxConfig.model_validate()`
2. `AgentFoxConfig` no longer has a `models` field — Pydantic's `extra="ignore"` drops the `[models]` section silently
3. `OrchestratorConfig` no longer has `quality_gate` / `quality_gate_timeout` — same `extra="ignore"` behavior
4. `ArchetypesConfig` no longer has the `_handle_archetype_config_keys` validator — `extra="ignore"` drops old keys silently

### Path 2: Config template generation

1. `agent_fox/core/config_gen.py: generate_default_config()` — calls `extract_schema(AgentFoxConfig)`
2. `extract_schema()` no longer encounters `ModelConfig` (field removed from `AgentFoxConfig`)
3. `_VISIBLE_SECTIONS` no longer includes `"models"` — no `[models]` block in template
4. `_PROMOTED_DEFAULTS` no longer includes `quality_gate` — not rendered as active field

## Components and Interfaces

No new interfaces. All changes are deletions.

### Deletions in `config.py`

| Item | Lines | Action |
|------|-------|--------|
| `ModelConfig` class | 180–203 | Delete entire class |
| `AgentFoxConfig.models` field | (references ModelConfig) | Delete field |
| `OrchestratorConfig.quality_gate` | 150–153 | Delete field |
| `OrchestratorConfig.quality_gate_timeout` | 154–157 | Delete field |
| `ArchetypesConfig._handle_archetype_config_keys` validator | 500–541 | Delete validator |

### Deletions in `config_gen.py`

| Item | Action |
|------|--------|
| `_BOUNDS_MAP` entries for `training_threshold`, `accuracy_threshold`, `retrain_interval` | Delete |
| `_VISIBLE_SECTIONS` entry `"models"` | Delete |
| `_PROMOTED_DEFAULTS` entry `("orchestrator", "quality_gate")` | Delete |
| `_PROMOTED_DEFAULTS_OVERRIDES` entry `("orchestrator", "quality_gate")` | Delete |
| `_SCHEMA_DEPRECATED_FIELDS` entry `("models", "coding")` | Delete entire set (no remaining entries) |
| `_DEFAULT_DESCRIPTIONS` entries for `ModelConfig.*` and `OrchestratorConfig.quality_gate` | Delete |

### Fixes in `config_gen.py`

| Item | Action |
|------|--------|
| `_BOUNDS_MAP` `("ReviewerConfig", "drift_review_block_threshold")` | Change `">=1"` → `">=1 or None"` |

### Deletions in `audit.py`

| Item | Action |
|------|--------|
| `AuditEvent.QUALITY_GATE_RESULT` | Delete enum member |

## Data Models

No data model changes beyond field deletions described above.

## Operational Readiness

- **Rollback:** Revert the commit. No migration needed.
- **Compatibility:** Existing `config.toml` files with old keys continue to
  load silently due to `extra="ignore"`. No user action required.
- **Observability:** Deprecation warnings for `[models].coding` and
  `archetypes.triage` are removed. Old keys are now silently ignored.

## Correctness Properties

### Property 1: Field Absence

*For any* `AgentFoxConfig` instance, the config object SHALL NOT have
attributes `models`, and `OrchestratorConfig` SHALL NOT have attributes
`quality_gate` or `quality_gate_timeout`.

**Validates: Requirements 130-REQ-1.1, 130-REQ-1.2, 130-REQ-2.2**

### Property 2: Silent Ignore on Unknown Keys

*For any* TOML input dict containing keys `quality_gate`, `quality_gate_timeout`,
`models`, `triage`, `skeptic`, `oracle`, `auditor`, `skeptic_config`,
`skeptic_settings`, `oracle_settings`, `auditor_config`, `fix_reviewer`, or
`fix_coder` in their respective sections, the config model SHALL parse without
error or warning.

**Validates: Requirements 130-REQ-1.E1, 130-REQ-2.E1, 130-REQ-3.1, 130-REQ-3.2, 130-REQ-3.E1**

### Property 3: Metadata Cleanliness

*For any* key in `_BOUNDS_MAP` and `_DEFAULT_DESCRIPTIONS`, the key's model
class and field name SHALL correspond to an actual field on the named Pydantic
model class.

**Validates: Requirements 130-REQ-4.1, 130-REQ-4.2, 130-REQ-1.5, 130-REQ-2.5**

### Property 4: Bounds Accuracy

*For any* entry in `_BOUNDS_MAP`, if the corresponding Pydantic field type
allows `None`, the bounds string SHALL indicate that `None` is valid.

**Validates: Requirement 130-REQ-5.1**

### Property 5: Audit Event Absence

*For any* member of the `AuditEvent` enum, the member SHALL NOT have value
`"quality_gate.result"`.

**Validates: Requirement 130-REQ-6.1**

### Property 6: Template Excludes Removed Fields

*For any* generated config template string, the template SHALL NOT contain the
string `quality_gate` and SHALL NOT contain a `[models]` section header.

**Validates: Requirements 130-REQ-1.3, 130-REQ-1.4, 130-REQ-2.3**

## Error Handling

| Error Condition | Behavior | Requirement |
|----------------|----------|-------------|
| Old `config.toml` has `[models]` section | Silently ignored | 130-REQ-2.E1 |
| Old `config.toml` has `quality_gate` | Silently ignored | 130-REQ-1.E1 |
| Old `config.toml` has `archetypes.skeptic` | Silently ignored | 130-REQ-3.E1 |
| Old `config.toml` has `archetypes.triage` | Silently ignored | 130-REQ-3.1 |

## Technology Stack

No changes. Pure Python, Pydantic, tomlkit (existing stack).

## Definition of Done

A task group is complete when ALL of the following are true:

1. All subtasks within the group are checked off (`[x]`)
2. All spec tests (`test_spec.md` entries) for the task group pass
3. All property tests for the task group pass
4. All previously passing tests still pass (no regressions)
5. No linter warnings or errors introduced
6. Code is committed on a feature branch and merged into `develop`
7. Feature branch is merged back to `develop`
8. `tasks.md` checkboxes are updated to reflect completion

## Testing Strategy

- **Unit tests** verify field absence on config models and metadata dicts.
- **Property tests** verify that arbitrary TOML inputs with old keys parse
  without error.
- **Integration** is covered by verifying `make check` passes end-to-end.
- No new integration smoke tests needed — this is a deletion spec with no
  new execution paths.
