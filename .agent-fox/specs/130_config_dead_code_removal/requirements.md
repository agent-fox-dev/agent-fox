# Requirements Document

## Introduction

This spec covers the removal of dead, unused, and deprecated configuration
parameters from the agent-fox codebase. The `[models]` section, the
`quality_gate` / `quality_gate_timeout` fields, obsolete archetype key
validators, and stale `config_gen.py` metadata are all removed. No backward
compatibility is maintained — old `config.toml` files that reference removed
fields will have those fields silently ignored via `ConfigDict(extra="ignore")`.

## Glossary

| Term | Definition |
|------|-----------|
| `config_gen.py` | Module responsible for schema extraction, TOML template rendering, and config merge |
| `_BOUNDS_MAP` | Dict in `config_gen.py` mapping `(ModelClass, field_name)` to bounds strings |
| `_PROMOTED_DEFAULTS` | Set of `(section, field)` tuples rendered as active (uncommented) in config templates |
| `_VISIBLE_SECTIONS` | Set of section names included in the simplified config template |
| `_DEFAULT_DESCRIPTIONS` | Dict mapping `(ModelClass, field_name)` to description strings |
| `ModelConfig` | Pydantic model for the deprecated `[models]` config section |
| `OrchestratorConfig` | Pydantic model for the `[orchestrator]` config section |
| `ArchetypesConfig` | Pydantic model for the `[archetypes]` config section |
| `AuditEvent` | Enum of structured audit event types in `knowledge/audit.py` |
| `config-reference.md` | Documentation file listing all `config.toml` options |

## Requirements

### Requirement 1: Remove Unused Orchestrator Config Fields

**User Story:** As a maintainer, I want dead config fields removed, so that the
config schema accurately reflects what the system actually reads.

#### Acceptance Criteria

1. [130-REQ-1.1] WHEN `OrchestratorConfig` is inspected, THE system SHALL NOT
   contain a `quality_gate` field.

2. [130-REQ-1.2] WHEN `OrchestratorConfig` is inspected, THE system SHALL NOT
   contain a `quality_gate_timeout` field.

3. [130-REQ-1.3] WHEN `config_gen.py` is loaded, THE `_PROMOTED_DEFAULTS` set
   SHALL NOT contain the tuple `("orchestrator", "quality_gate")`.

4. [130-REQ-1.4] WHEN `config_gen.py` is loaded, THE `_PROMOTED_DEFAULTS_OVERRIDES`
   dict SHALL NOT contain the key `("orchestrator", "quality_gate")`.

5. [130-REQ-1.5] WHEN `config_gen.py` is loaded, THE `_DEFAULT_DESCRIPTIONS`
   dict SHALL NOT contain the key `("OrchestratorConfig", "quality_gate")`.

#### Edge Cases

1. [130-REQ-1.E1] IF an existing `config.toml` contains `quality_gate` or
   `quality_gate_timeout` under `[orchestrator]`, THEN THE system SHALL
   silently ignore those keys (via `extra="ignore"` on `OrchestratorConfig`).

### Requirement 2: Remove Deprecated `[models]` Section

**User Story:** As a maintainer, I want the deprecated `ModelConfig` class and
its `[models]` config section removed entirely.

#### Acceptance Criteria

1. [130-REQ-2.1] WHEN the `config` module is imported, THE `ModelConfig` class
   SHALL NOT be defined in `agent_fox.core.config`.

2. [130-REQ-2.2] WHEN `AgentFoxConfig` is inspected, THE system SHALL NOT
   contain a `models` field.

3. [130-REQ-2.3] WHEN `config_gen.py` is loaded, THE `_VISIBLE_SECTIONS` set
   SHALL NOT contain `"models"`.

4. [130-REQ-2.4] WHEN `config_gen.py` is loaded, THE `_SCHEMA_DEPRECATED_FIELDS`
   set SHALL NOT contain `("models", "coding")`.

5. [130-REQ-2.5] WHEN `config_gen.py` is loaded, THE `_DEFAULT_DESCRIPTIONS`
   dict SHALL NOT contain any key with `"ModelConfig"` as the first element.

#### Edge Cases

1. [130-REQ-2.E1] IF an existing `config.toml` contains a `[models]` section,
   THEN THE system SHALL silently ignore it (the `models` field is removed
   from `AgentFoxConfig`, and `extra="ignore"` handles unknown top-level keys).

### Requirement 3: Remove Obsolete Archetype Key Validators

**User Story:** As a maintainer, I want the validators that handle old
archetype config keys removed, since we no longer support old config files.

#### Acceptance Criteria

1. [130-REQ-3.1] WHEN `ArchetypesConfig` receives a dict with key `"triage"`,
   THE system SHALL silently ignore it (via `extra="ignore"`) instead of
   logging a deprecation warning.

2. [130-REQ-3.2] WHEN `ArchetypesConfig` receives a dict with any of the keys
   `"skeptic"`, `"oracle"`, `"auditor"`, `"skeptic_config"`,
   `"skeptic_settings"`, `"oracle_settings"`, `"auditor_config"`,
   `"fix_reviewer"`, or `"fix_coder"`, THE system SHALL silently ignore them
   instead of raising a `ValueError`.

3. [130-REQ-3.3] WHEN the `_handle_archetype_config_keys` model validator (or
   equivalent) is inspected, THE system SHALL NOT contain references to the
   deprecated or obsolete key sets.

#### Edge Cases

1. [130-REQ-3.E1] IF an existing `config.toml` contains `archetypes.skeptic = true`,
   THEN THE system SHALL silently ignore it without error or warning.

### Requirement 4: Remove Stale `config_gen.py` Metadata

**User Story:** As a maintainer, I want phantom metadata entries for
non-existent config fields removed from `config_gen.py`.

#### Acceptance Criteria

1. [130-REQ-4.1] WHEN `config_gen.py` is loaded, THE `_BOUNDS_MAP` dict SHALL
   NOT contain entries for `("RoutingConfig", "training_threshold")`,
   `("RoutingConfig", "accuracy_threshold")`, or
   `("RoutingConfig", "retrain_interval")`.

2. [130-REQ-4.2] WHEN `config_gen.py` is loaded, THE `_DEFAULT_DESCRIPTIONS`
   dict SHALL NOT contain entries for `("RoutingConfig", "training_threshold")`,
   `("RoutingConfig", "accuracy_threshold")`, or
   `("RoutingConfig", "retrain_interval")`.

### Requirement 5: Fix `drift_review_block_threshold` Bounds

**User Story:** As a maintainer, I want the bounds metadata to accurately
reflect field constraints.

#### Acceptance Criteria

1. [130-REQ-5.1] WHEN `config_gen.py` is loaded, THE `_BOUNDS_MAP` entry for
   `("ReviewerConfig", "drift_review_block_threshold")` SHALL have a value
   that indicates `None` is valid (e.g. `">=1 or None"`).

### Requirement 6: Remove Unused Audit Event

**User Story:** As a maintainer, I want the `QUALITY_GATE_RESULT` audit event
removed since it is never emitted.

#### Acceptance Criteria

1. [130-REQ-6.1] WHEN the `AuditEvent` enum is inspected, THE system SHALL NOT
   contain a `QUALITY_GATE_RESULT` member.

### Requirement 7: Update Documentation

**User Story:** As a user, I want the config reference to only document
parameters that actually exist.

#### Acceptance Criteria

1. [130-REQ-7.1] WHEN `docs/config-reference.md` is read, THE document SHALL
   NOT contain a `## models` section.

2. [130-REQ-7.2] WHEN `docs/config-reference.md` is read, THE `## orchestrator`
   table SHALL NOT contain rows for `quality_gate` or `quality_gate_timeout`.

3. [130-REQ-7.3] WHEN `docs/config-reference.md` is read, THE `## archetypes`
   section SHALL NOT contain the "Obsolete keys" paragraph about `skeptic`,
   `oracle`, etc.

4. [130-REQ-7.4] WHEN `docs/config-reference.md` is read, THE table of contents
   SHALL NOT contain a `models` link.

5. [130-REQ-7.5] WHEN `docs/config-reference.md` is read, THE `[archetypes]`
   "General behavior" paragraph about unknown keys rejecting obsolete archetype
   names SHALL be updated to reflect that unknown keys are now silently ignored.

### Requirement 8: Test Cleanup

**User Story:** As a maintainer, I want tests that reference removed parameters
updated or removed so the test suite stays green.

#### Acceptance Criteria

1. [130-REQ-8.1] WHEN the test suite is run, THE system SHALL pass with no
   regressions AND no references to removed fields in test assertions.
