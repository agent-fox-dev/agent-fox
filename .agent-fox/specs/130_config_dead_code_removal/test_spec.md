# Test Specification: Config Dead Code Removal

## Overview

Tests verify that removed config fields, metadata entries, audit events, and
documentation sections are absent, and that existing `config.toml` files with
old keys continue to load silently. Since this is a deletion spec, tests
primarily assert absence of items rather than presence of new behavior.

## Test Cases

### TS-130-1: `quality_gate` Field Absent from OrchestratorConfig

**Requirement:** 130-REQ-1.1
**Type:** unit
**Description:** Verify `OrchestratorConfig` no longer has a `quality_gate` field.

**Preconditions:**
- None.

**Input:**
- Inspect `OrchestratorConfig.model_fields`.

**Expected:**
- `"quality_gate"` is not in `OrchestratorConfig.model_fields`.

**Assertion pseudocode:**
```
fields = OrchestratorConfig.model_fields
ASSERT "quality_gate" NOT IN fields
```

### TS-130-2: `quality_gate_timeout` Field Absent from OrchestratorConfig

**Requirement:** 130-REQ-1.2
**Type:** unit
**Description:** Verify `OrchestratorConfig` no longer has a `quality_gate_timeout` field.

**Preconditions:**
- None.

**Input:**
- Inspect `OrchestratorConfig.model_fields`.

**Expected:**
- `"quality_gate_timeout"` is not in `OrchestratorConfig.model_fields`.

**Assertion pseudocode:**
```
fields = OrchestratorConfig.model_fields
ASSERT "quality_gate_timeout" NOT IN fields
```

### TS-130-3: `ModelConfig` Class Absent

**Requirement:** 130-REQ-2.1
**Type:** unit
**Description:** Verify `ModelConfig` is no longer defined in the config module.

**Preconditions:**
- None.

**Input:**
- Check `hasattr(config_module, "ModelConfig")`.

**Expected:**
- `False`.

**Assertion pseudocode:**
```
import agent_fox.core.config as config_mod
ASSERT NOT hasattr(config_mod, "ModelConfig")
```

### TS-130-4: `AgentFoxConfig.models` Field Absent

**Requirement:** 130-REQ-2.2
**Type:** unit
**Description:** Verify `AgentFoxConfig` no longer has a `models` field.

**Preconditions:**
- None.

**Input:**
- Inspect `AgentFoxConfig.model_fields`.

**Expected:**
- `"models"` is not in `AgentFoxConfig.model_fields`.

**Assertion pseudocode:**
```
fields = AgentFoxConfig.model_fields
ASSERT "models" NOT IN fields
```

### TS-130-5: `_VISIBLE_SECTIONS` Excludes `"models"`

**Requirement:** 130-REQ-2.3
**Type:** unit
**Description:** Verify `_VISIBLE_SECTIONS` no longer includes `"models"`.

**Preconditions:**
- None.

**Input:**
- Import `_VISIBLE_SECTIONS` from `config_gen`.

**Expected:**
- `"models"` is not in the set.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _VISIBLE_SECTIONS
ASSERT "models" NOT IN _VISIBLE_SECTIONS
```

### TS-130-6: `_PROMOTED_DEFAULTS` Excludes Quality Gate

**Requirement:** 130-REQ-1.3
**Type:** unit
**Description:** Verify `_PROMOTED_DEFAULTS` no longer includes `quality_gate`.

**Preconditions:**
- None.

**Input:**
- Import `_PROMOTED_DEFAULTS` from `config_gen`.

**Expected:**
- `("orchestrator", "quality_gate")` is not in the set.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _PROMOTED_DEFAULTS
ASSERT ("orchestrator", "quality_gate") NOT IN _PROMOTED_DEFAULTS
```

### TS-130-7: Phantom RoutingConfig Entries Absent from `_BOUNDS_MAP`

**Requirement:** 130-REQ-4.1
**Type:** unit
**Description:** Verify `_BOUNDS_MAP` no longer contains entries for removed RoutingConfig fields.

**Preconditions:**
- None.

**Input:**
- Import `_BOUNDS_MAP` from `config_gen`.

**Expected:**
- Keys `("RoutingConfig", "training_threshold")`, `("RoutingConfig", "accuracy_threshold")`,
  `("RoutingConfig", "retrain_interval")` are absent.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _BOUNDS_MAP
ASSERT ("RoutingConfig", "training_threshold") NOT IN _BOUNDS_MAP
ASSERT ("RoutingConfig", "accuracy_threshold") NOT IN _BOUNDS_MAP
ASSERT ("RoutingConfig", "retrain_interval") NOT IN _BOUNDS_MAP
```

### TS-130-8: Phantom RoutingConfig Entries Absent from `_DEFAULT_DESCRIPTIONS`

**Requirement:** 130-REQ-4.2
**Type:** unit
**Description:** Verify `_DEFAULT_DESCRIPTIONS` no longer contains entries for removed RoutingConfig fields.

**Preconditions:**
- None.

**Input:**
- Import `_DEFAULT_DESCRIPTIONS` from `config_gen`.

**Expected:**
- Keys `("RoutingConfig", "training_threshold")`, `("RoutingConfig", "accuracy_threshold")`,
  `("RoutingConfig", "retrain_interval")` are absent.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _DEFAULT_DESCRIPTIONS
ASSERT ("RoutingConfig", "training_threshold") NOT IN _DEFAULT_DESCRIPTIONS
ASSERT ("RoutingConfig", "accuracy_threshold") NOT IN _DEFAULT_DESCRIPTIONS
ASSERT ("RoutingConfig", "retrain_interval") NOT IN _DEFAULT_DESCRIPTIONS
```

### TS-130-9: `drift_review_block_threshold` Bounds Reflect None

**Requirement:** 130-REQ-5.1
**Type:** unit
**Description:** Verify the bounds for `drift_review_block_threshold` include `None`.

**Preconditions:**
- None.

**Input:**
- Import `_BOUNDS_MAP` from `config_gen`.

**Expected:**
- `_BOUNDS_MAP[("ReviewerConfig", "drift_review_block_threshold")]` contains
  the substring `"None"`.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _BOUNDS_MAP
bounds = _BOUNDS_MAP[("ReviewerConfig", "drift_review_block_threshold")]
ASSERT "None" IN bounds
```

### TS-130-10: `QUALITY_GATE_RESULT` Absent from AuditEvent

**Requirement:** 130-REQ-6.1
**Type:** unit
**Description:** Verify `AuditEvent` enum no longer has `QUALITY_GATE_RESULT`.

**Preconditions:**
- None.

**Input:**
- Import `AuditEvent` from `knowledge.audit`.

**Expected:**
- `"QUALITY_GATE_RESULT"` is not in `AuditEvent.__members__`.

**Assertion pseudocode:**
```
from agent_fox.knowledge.audit import AuditEvent
ASSERT "QUALITY_GATE_RESULT" NOT IN AuditEvent.__members__
```

### TS-130-11: `ModelConfig` Entries Absent from `_DEFAULT_DESCRIPTIONS`

**Requirement:** 130-REQ-2.5
**Type:** unit
**Description:** Verify no `_DEFAULT_DESCRIPTIONS` key starts with `"ModelConfig"`.

**Preconditions:**
- None.

**Input:**
- Import `_DEFAULT_DESCRIPTIONS` from `config_gen`.

**Expected:**
- No key tuple has `"ModelConfig"` as its first element.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import _DEFAULT_DESCRIPTIONS
model_config_keys = [k for k in _DEFAULT_DESCRIPTIONS if k[0] == "ModelConfig"]
ASSERT model_config_keys == []
```

### TS-130-12: Template Does Not Contain `quality_gate`

**Requirement:** 130-REQ-1.3, 130-REQ-1.4
**Type:** unit
**Description:** Verify the generated config template does not mention `quality_gate`.

**Preconditions:**
- None.

**Input:**
- Generate template via `generate_default_config()`.

**Expected:**
- The string `"quality_gate"` does not appear in the template.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import generate_default_config
template = generate_default_config()
ASSERT "quality_gate" NOT IN template
```

### TS-130-13: Template Does Not Contain `[models]`

**Requirement:** 130-REQ-2.3
**Type:** unit
**Description:** Verify the generated config template has no `[models]` section.

**Preconditions:**
- None.

**Input:**
- Generate template via `generate_default_config()`.

**Expected:**
- Neither `"[models]"` nor `"# [models]"` appears in the template.

**Assertion pseudocode:**
```
from agent_fox.core.config_gen import generate_default_config
template = generate_default_config()
ASSERT "[models]" NOT IN template
```

## Property Test Cases

### TS-130-P1: Silent Ignore of Old Config Keys

**Property:** Property 2 from design.md
**Validates:** 130-REQ-1.E1, 130-REQ-2.E1, 130-REQ-3.1, 130-REQ-3.2, 130-REQ-3.E1
**Type:** property
**Description:** Config parsing silently ignores any combination of removed keys.

**For any:** Dict with arbitrary values for removed keys (`quality_gate`,
`quality_gate_timeout`, `models.coding`, `models.memory_extraction`,
`archetypes.triage`, `archetypes.skeptic`, `archetypes.oracle`,
`archetypes.auditor`, `archetypes.skeptic_config`, `archetypes.fix_reviewer`,
`archetypes.fix_coder`)

**Invariant:** `AgentFoxConfig.model_validate(input_dict)` succeeds without
error AND the resulting config has the same default values for all remaining
fields as a config parsed from an empty dict.

**Assertion pseudocode:**
```
FOR ANY removed_keys IN strategy:
    input_dict = build_dict_with_removed_keys(removed_keys)
    config = AgentFoxConfig.model_validate(input_dict)
    default_config = AgentFoxConfig()
    ASSERT config.orchestrator.parallel == default_config.orchestrator.parallel
    ASSERT NOT hasattr(config, "models")
```

### TS-130-P2: Metadata Keys Match Real Fields

**Property:** Property 3 from design.md
**Validates:** 130-REQ-4.1, 130-REQ-4.2, 130-REQ-1.5, 130-REQ-2.5
**Type:** property
**Description:** Every `_BOUNDS_MAP` key corresponds to an actual Pydantic model field.

**For any:** Key `(model_name, field_name)` in `_BOUNDS_MAP`

**Invariant:** The model class named `model_name` exists in `config.py` and
has `field_name` in its `model_fields`.

**Assertion pseudocode:**
```
FOR ANY (model_name, field_name) IN _BOUNDS_MAP:
    model_cls = get_model_by_name(model_name)
    ASSERT model_cls IS NOT None
    ASSERT field_name IN model_cls.model_fields
```

## Edge Case Tests

### TS-130-E1: Old Config with `quality_gate` Parses Silently

**Requirement:** 130-REQ-1.E1
**Type:** unit
**Description:** TOML with `quality_gate = "make check"` under `[orchestrator]` parses without error.

**Preconditions:**
- None.

**Input:**
- TOML string: `[orchestrator]\nquality_gate = "make check"\nquality_gate_timeout = 120`

**Expected:**
- `AgentFoxConfig.model_validate()` succeeds. Resulting config has default `parallel` value.

**Assertion pseudocode:**
```
raw = tomllib.loads('[orchestrator]\nquality_gate = "make check"\nquality_gate_timeout = 120')
config = AgentFoxConfig.model_validate(raw)
ASSERT config.orchestrator.parallel == 2
```

### TS-130-E2: Old Config with `[models]` Parses Silently

**Requirement:** 130-REQ-2.E1
**Type:** unit
**Description:** TOML with `[models]` section parses without error.

**Preconditions:**
- None.

**Input:**
- TOML string: `[models]\ncoding = "ADVANCED"\nmemory_extraction = "SIMPLE"`

**Expected:**
- `AgentFoxConfig.model_validate()` succeeds. Config has no `models` attribute.

**Assertion pseudocode:**
```
raw = tomllib.loads('[models]\ncoding = "ADVANCED"\nmemory_extraction = "SIMPLE"')
config = AgentFoxConfig.model_validate(raw)
ASSERT NOT hasattr(config, "models")
```

### TS-130-E3: Old Config with `archetypes.skeptic` Parses Silently

**Requirement:** 130-REQ-3.E1
**Type:** unit
**Description:** TOML with `archetypes.skeptic = true` parses without error.

**Preconditions:**
- None.

**Input:**
- TOML string: `[archetypes]\nskeptic = true`

**Expected:**
- `AgentFoxConfig.model_validate()` succeeds. Config archetypes has default reviewer value.

**Assertion pseudocode:**
```
raw = tomllib.loads('[archetypes]\nskeptic = true')
config = AgentFoxConfig.model_validate(raw)
ASSERT config.archetypes.reviewer == True
```

### TS-130-E4: Old Config with `archetypes.triage` Parses Silently

**Requirement:** 130-REQ-3.1
**Type:** unit
**Description:** TOML with `archetypes.triage = true` parses without error and no warning.

**Preconditions:**
- None.

**Input:**
- TOML string: `[archetypes]\ntriage = true`

**Expected:**
- `AgentFoxConfig.model_validate()` succeeds. No warning logged.

**Assertion pseudocode:**
```
raw = tomllib.loads('[archetypes]\ntriage = true')
with assert_no_warnings():
    config = AgentFoxConfig.model_validate(raw)
ASSERT config.archetypes.reviewer == True
```

## Integration Smoke Tests

### TS-130-SMOKE-1: Full Config Load After Removal

**Execution Path:** Path 1 from design.md
**Description:** A config.toml containing all removed keys loads successfully.

**Setup:** Write a temporary `config.toml` with `[orchestrator]` containing
`quality_gate`, `[models]` with `coding`, and `[archetypes]` with `triage` and
`skeptic`.

**Trigger:** Call `load_config(path)`.

**Expected side effects:**
- Config loads without error.
- No `models` attribute on result.
- No `quality_gate` attribute on `result.orchestrator`.
- Default values intact for all remaining fields.

**Must NOT satisfy with:** Mocking config.py or any Pydantic validation.

**Assertion pseudocode:**
```
config = load_config(tmp_config_path)
ASSERT NOT hasattr(config, "models")
ASSERT "quality_gate" NOT IN config.orchestrator.model_fields
ASSERT config.orchestrator.parallel == 2
```

### TS-130-SMOKE-2: Template Generation After Removal

**Execution Path:** Path 2 from design.md
**Description:** Generated config template excludes all removed items.

**Setup:** None.

**Trigger:** Call `generate_default_config()`.

**Expected side effects:**
- Template string does not contain `quality_gate`, `[models]`, or
  `memory_extraction`.
- Template has `[orchestrator]` section with `parallel` and `max_budget_usd`.

**Must NOT satisfy with:** Mocking `config_gen.py` internals.

**Assertion pseudocode:**
```
template = generate_default_config()
ASSERT "quality_gate" NOT IN template
ASSERT "[models]" NOT IN template
ASSERT "memory_extraction" NOT IN template
ASSERT "parallel" IN template
ASSERT "max_budget_usd" IN template
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 130-REQ-1.1 | TS-130-1 | unit |
| 130-REQ-1.2 | TS-130-2 | unit |
| 130-REQ-1.3 | TS-130-6, TS-130-12 | unit |
| 130-REQ-1.4 | TS-130-12 | unit |
| 130-REQ-1.5 | TS-130-11 | unit |
| 130-REQ-1.E1 | TS-130-E1 | unit |
| 130-REQ-2.1 | TS-130-3 | unit |
| 130-REQ-2.2 | TS-130-4 | unit |
| 130-REQ-2.3 | TS-130-5, TS-130-13 | unit |
| 130-REQ-2.4 | TS-130-5 | unit |
| 130-REQ-2.5 | TS-130-11 | unit |
| 130-REQ-2.E1 | TS-130-E2 | unit |
| 130-REQ-3.1 | TS-130-E4 | unit |
| 130-REQ-3.2 | TS-130-E3 | unit |
| 130-REQ-3.3 | TS-130-E3, TS-130-E4 | unit |
| 130-REQ-3.E1 | TS-130-E3 | unit |
| 130-REQ-4.1 | TS-130-7 | unit |
| 130-REQ-4.2 | TS-130-8 | unit |
| 130-REQ-5.1 | TS-130-9 | unit |
| 130-REQ-6.1 | TS-130-10 | unit |
| 130-REQ-7.1 | (doc review) | manual |
| 130-REQ-7.2 | (doc review) | manual |
| 130-REQ-7.3 | (doc review) | manual |
| 130-REQ-7.4 | (doc review) | manual |
| 130-REQ-7.5 | (doc review) | manual |
| 130-REQ-8.1 | TS-130-SMOKE-1, TS-130-SMOKE-2 | integration |
| Property 1 | TS-130-1, TS-130-2, TS-130-4 | unit |
| Property 2 | TS-130-P1 | property |
| Property 3 | TS-130-P2 | property |
| Property 4 | TS-130-9 | unit |
| Property 5 | TS-130-10 | unit |
| Property 6 | TS-130-12, TS-130-13 | unit |
