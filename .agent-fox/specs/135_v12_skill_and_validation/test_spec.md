# Test Specification: v1.2 Skill Template and Validation Migration

## Overview

Tests validate that the lint-specs backing module correctly routes v1.2 specs
to `afspec.validate()`, maps `ValidationError` to `Finding`, and preserves
the CLI interface. Skill template tests verify the updated content includes
v1.2 artifact names, ID formats, and JSON structure references.

## Test Cases

### TS-135-1: v1.2 spec routed to afspec.validate

**Requirement:** 135-REQ-1.1
**Type:** unit
**Description:** Verify that a spec with format V1_2_JSON is validated using
afspec.validate() and returns Finding instances.

**Preconditions:**
- A temp directory with a valid v1.2 spec folder (prd.md with frontmatter,
  requirements.json, test_spec.json, tasks.json).

**Input:**
- Call `run_lint_specs(tmp_root)` with a single v1.2 spec.

**Expected:**
- `afspec.validate` is called with the spec's path.
- Results are returned as `Finding` instances.

**Assertion pseudocode:**
```
with patch("afspec.validate") as mock_validate:
    mock_validate.return_value = []
    result = run_lint_specs(tmp_root)
    ASSERT mock_validate.called_with(spec_path)
    ASSERT result.exit_code == 0
```

### TS-135-2: v1 spec routed to custom validators

**Requirement:** 135-REQ-1.2
**Type:** unit
**Description:** Verify that a spec with format V1_MARKDOWN is validated
using the existing custom validate_specs() function.

**Preconditions:**
- A temp directory with a v1 markdown spec folder (requirements.md, design.md,
  test_spec.md, tasks.md, prd.md).

**Input:**
- Call `run_lint_specs(tmp_root)` with a single v1 spec.

**Expected:**
- Custom `validate_specs()` is called with the spec.
- `afspec.validate` is NOT called.

**Assertion pseudocode:**
```
with patch("agent_fox.spec.validators.validate_specs") as mock_v1:
    with patch("afspec.validate") as mock_v12:
        mock_v1.return_value = []
        result = run_lint_specs(tmp_root)
        ASSERT mock_v1.called
        ASSERT NOT mock_v12.called
```

### TS-135-3: Mixed format specs validated by correct validators

**Requirement:** 135-REQ-1.3
**Type:** integration
**Description:** Verify that a mix of v1 and v1.2 specs are each validated
by the appropriate validator and results are merged.

**Preconditions:**
- A temp directory with:
  - `01_legacy/` containing v1 markdown artifacts
  - `02_modern/` containing v1.2 JSON artifacts

**Input:**
- Call `run_lint_specs(tmp_root)` with both specs discovered.

**Expected:**
- Custom validators called for `01_legacy`.
- `afspec.validate()` called for `02_modern`.
- Combined findings sorted by spec name, file, severity.

**Assertion pseudocode:**
```
result = run_lint_specs(tmp_root)
ASSERT findings_contain_spec("01_legacy", result.findings) OR result.findings == []
ASSERT afspec_validate_called_for("02_modern")
ASSERT result.findings == sorted(result.findings, key=sort_key)
```

### TS-135-4: ValidationError mapped to Finding correctly

**Requirement:** 135-REQ-2.1
**Type:** unit
**Description:** Verify that each field of a ValidationError is mapped to
the corresponding Finding field.

**Preconditions:**
- None.

**Input:**
- A `ValidationError(file="requirements.json", rule="missing-field",
  severity="warning", message="Field 'title' is required", line=42)`.

**Expected:**
- `Finding(spec_name="02_modern", file="requirements.json",
  rule="missing-field", severity="warning",
  message="Field 'title' is required", line=42)`.

**Assertion pseudocode:**
```
ve = ValidationError(file="requirements.json", rule="missing-field",
                     severity="warning", message="Field 'title' is required",
                     line=42)
findings = _map_afspec_findings("02_modern", [ve])
ASSERT len(findings) == 1
f = findings[0]
ASSERT f.spec_name == "02_modern"
ASSERT f.file == "requirements.json"
ASSERT f.rule == "missing-field"
ASSERT f.severity == "warning"
ASSERT f.message == "Field 'title' is required"
ASSERT f.line == 42
```

### TS-135-5: Unknown severity defaults to error

**Requirement:** 135-REQ-2.2
**Type:** unit
**Description:** Verify that a ValidationError with an unrecognized severity
is mapped to a Finding with severity "error".

**Preconditions:**
- None.

**Input:**
- A `ValidationError` with `severity="critical"` (not in the known set).

**Expected:**
- `Finding.severity == "error"`.

**Assertion pseudocode:**
```
ve = ValidationError(file="tasks.json", rule="unknown-rule",
                     severity="critical", message="Something bad", line=None)
findings = _map_afspec_findings("test_spec", [ve])
ASSERT findings[0].severity == "error"
```

### TS-135-6: CLI flags unchanged

**Requirement:** 135-REQ-3.1
**Type:** unit
**Description:** Verify that the lint-specs CLI command still accepts --ai
and --all flags and produces table/JSON output.

**Preconditions:**
- Click test runner available.

**Input:**
- Invoke `lint_specs_cmd` via Click runner with `--all` flag.

**Expected:**
- Command accepts the flag without error.
- Output format is table (default) or JSON (with --json).

**Assertion pseudocode:**
```
runner = CliRunner()
result = runner.invoke(lint_specs_cmd, ["--all"], obj={"json": False})
ASSERT result.exit_code IN {0, 1}  # valid exit codes
ASSERT "findings" in result.output OR "No findings" in result.output
```

### TS-135-7: Skill template references v1.2 artifacts

**Requirement:** 135-REQ-4.1
**Type:** unit
**Description:** Verify that the skill template content references v1.2
artifact names.

**Preconditions:**
- Skill template file exists.

**Input:**
- Read the af-spec skill template file content.

**Expected:**
- Content contains references to `requirements.json`, `test_spec.json`,
  `tasks.json`, `prd.md` with YAML frontmatter, and `architecture.md`.

**Assertion pseudocode:**
```
content = read_file("agent_fox/_templates/skills/af-spec")
ASSERT "requirements.json" IN content
ASSERT "test_spec.json" IN content
ASSERT "tasks.json" IN content
ASSERT "YAML frontmatter" IN content OR "frontmatter" IN content
ASSERT "architecture.md" IN content
```

### TS-135-8: Skill template references v1.2 ID formats

**Requirement:** 135-REQ-4.2
**Type:** unit
**Description:** Verify that the skill template references v1.2 ID format
conventions.

**Preconditions:**
- Skill template file exists.

**Input:**
- Read the af-spec skill template file content.

**Expected:**
- Content references `{spec_id}-REQ-{N}` for requirements, `{spec_id}-PROP-{N}`
  for properties.

**Assertion pseudocode:**
```
content = read_file("agent_fox/_templates/skills/af-spec")
ASSERT "{spec_id}-REQ-" IN content OR "spec_id}-REQ-" IN content
ASSERT "{spec_id}-PROP-" IN content OR "spec_id}-PROP-" IN content
```

### TS-135-9: Skill template describes EARS JSON structure

**Requirement:** 135-REQ-6.1
**Type:** unit
**Description:** Verify that the skill template describes the EARS pattern
discriminated union JSON structure.

**Preconditions:**
- Skill template file exists.

**Input:**
- Read the af-spec skill template file content.

**Expected:**
- Content describes `ears_pattern` field and pattern types (ubiquitous,
  event_driven, state_driven, unwanted).

**Assertion pseudocode:**
```
content = read_file("agent_fox/_templates/skills/af-spec")
ASSERT "ears_pattern" IN content
ASSERT "ubiquitous" IN content
ASSERT "event_driven" IN content
ASSERT "unwanted" IN content
```

### TS-135-10: Skill template describes tasks JSON structure

**Requirement:** 135-REQ-6.2
**Type:** unit
**Description:** Verify that the skill template describes the tasks JSON
structure with state machine.

**Preconditions:**
- Skill template file exists.

**Input:**
- Read the af-spec skill template file content.

**Expected:**
- Content describes task states (not_started, in_progress, completed, queued).

**Assertion pseudocode:**
```
content = read_file("agent_fox/_templates/skills/af-spec")
ASSERT "not_started" IN content
ASSERT "in_progress" IN content
ASSERT "completed" IN content
ASSERT "queued" IN content
```

## Property Test Cases

### TS-135-P1: Finding mapping preserves all fields

**Property:** Property 2 from design.md
**Validates:** 135-REQ-2.1, 135-REQ-2.2
**Type:** property
**Description:** For any valid ValidationError, the mapped Finding preserves
all field values.

**For any:** ValidationError with arbitrary file (non-empty string), rule
(non-empty string), severity (from {error, warning, hint}), message
(non-empty string), line (None or positive int)
**Invariant:** The mapped Finding has identical file, rule, severity, message,
and line values, with spec_name set to the provided name.

**Assertion pseudocode:**
```
FOR ANY file IN non_empty_strings,
       rule IN non_empty_strings,
       severity IN {"error", "warning", "hint"},
       message IN non_empty_strings,
       line IN {None} | positive_integers:
    ve = ValidationError(file=file, rule=rule, severity=severity,
                         message=message, line=line)
    findings = _map_afspec_findings("test_spec", [ve])
    ASSERT len(findings) == 1
    f = findings[0]
    ASSERT f.spec_name == "test_spec"
    ASSERT f.file == file
    ASSERT f.rule == rule
    ASSERT f.severity == severity
    ASSERT f.message == message
    ASSERT f.line == line
```

### TS-135-P2: Format routing is exhaustive

**Property:** Property 1 from design.md
**Validates:** 135-REQ-1.1, 135-REQ-1.2
**Type:** property
**Description:** Every spec in the discovered list is validated by exactly
one validator.

**For any:** list of SpecInfo with format in {V1_MARKDOWN, V1_2_JSON}
**Invariant:** Each spec is processed by exactly one of custom validators or
afspec.validate(), never both, never neither.

**Assertion pseudocode:**
```
FOR ANY specs IN list_of_spec_info:
    v1_count = count(s for s in specs if s.format == V1_MARKDOWN)
    v12_count = count(s for s in specs if s.format == V1_2_JSON)
    ASSERT v1_count + v12_count == len(specs)
    # Each set is validated by its respective validator
    run_lint_specs(tmp_root)
    ASSERT custom_validate_called_count == (1 if v1_count > 0 else 0)
    ASSERT afspec_validate_called_count == v12_count
```

## Edge Case Tests

### TS-135-E1: afspec.validate raises exception

**Requirement:** 135-REQ-1.E1
**Type:** unit
**Description:** Verify that an exception from afspec.validate() is caught
and converted to an error Finding.

**Preconditions:**
- afspec.validate patched to raise RuntimeError.

**Input:**
- A v1.2 spec folder path.

**Expected:**
- A single Finding with rule="afspec-error", severity="error", and the
  exception message.
- Remaining specs are still validated.

**Assertion pseudocode:**
```
with patch("afspec.validate", side_effect=RuntimeError("schema broken")):
    result = run_lint_specs(tmp_root_with_v12_spec)
    ASSERT any(f.rule == "afspec-error" for f in result.findings)
    ASSERT any("schema broken" in f.message for f in result.findings)
```

### TS-135-E2: Empty validation result for clean v1.2 spec

**Requirement:** 135-REQ-2.E1
**Type:** unit
**Description:** Verify that an empty ValidationError list produces zero
findings.

**Preconditions:**
- afspec.validate patched to return [].

**Input:**
- A v1.2 spec folder path.

**Expected:**
- Zero findings from the v1.2 validator.

**Assertion pseudocode:**
```
with patch("afspec.validate", return_value=[]):
    result = run_lint_specs(tmp_root_with_v12_spec)
    v12_findings = [f for f in result.findings if f.spec_name == v12_spec_name]
    ASSERT len(v12_findings) == 0
```

### TS-135-E3: ValidationError with unknown severity

**Requirement:** 135-REQ-2.2
**Type:** unit
**Description:** Verify that ValidationError with severity not in
{error, warning, hint} defaults to "error".

**Preconditions:**
- None.

**Input:**
- A ValidationError with severity="fatal".

**Expected:**
- Mapped Finding has severity="error".

**Assertion pseudocode:**
```
ve = ValidationError(file="x.json", rule="r", severity="fatal",
                     message="m", line=None)
findings = _map_afspec_findings("spec", [ve])
ASSERT findings[0].severity == "error"
```

## Integration Smoke Tests

### TS-135-SMOKE-1: Mixed format lint end-to-end

**Execution Path:** Path 3 from design.md
**Description:** Run lint-specs against a directory with both v1 and v1.2
specs and verify both validators are invoked and results merged.

**Setup:** Temp specs root with:
- `01_legacy/` containing valid v1 markdown spec (prd.md, requirements.md,
  design.md, test_spec.md, tasks.md)
- `02_modern/` containing valid v1.2 spec (prd.md with frontmatter,
  requirements.json, test_spec.json, tasks.json)
Mockable: afspec.validate may be real or a controlled mock returning known
findings.

**Trigger:** `run_lint_specs(tmp_root, lint_all=True)`

**Expected side effects:**
- Returns `LintResult` with findings from both validators.
- Findings are sorted by spec name, file, severity.
- Exit code reflects error-severity findings (0 if clean, 1 if errors).

**Must NOT satisfy with:** Mocking of `run_lint_specs` itself or bypassing
format detection.

**Assertion pseudocode:**
```
result = run_lint_specs(tmp_root, lint_all=True)
ASSERT isinstance(result, LintResult)
# Both specs were processed (no skips)
spec_names = {f.spec_name for f in result.findings}
# If findings exist, verify they are sorted
for i in range(len(result.findings) - 1):
    ASSERT sort_key(result.findings[i]) <= sort_key(result.findings[i+1])
```

### TS-135-SMOKE-2: Skill template content validation

**Execution Path:** Path N/A (template content test)
**Description:** Verify the skill template file contains all required v1.2
references.

**Setup:** Read the skill template from its known path.
Mockable: none -- reads the real file.

**Trigger:** Read `agent_fox/_templates/skills/af-spec`.

**Expected side effects:**
- File contains v1.2 artifact names, ID formats, EARS JSON structure,
  tasks JSON structure, and validation step.

**Must NOT satisfy with:** A hardcoded expected string -- check for semantic
markers.

**Assertion pseudocode:**
```
content = read_file("agent_fox/_templates/skills/af-spec")
# v1.2 artifacts
ASSERT "requirements.json" IN content
ASSERT "test_spec.json" IN content
ASSERT "tasks.json" IN content
# ID formats
ASSERT "spec_id" IN content
# EARS JSON
ASSERT "ears_pattern" IN content
# Tasks JSON
ASSERT "not_started" IN content
# Validation step
ASSERT "lint-specs" IN content
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 135-REQ-1.1 | TS-135-1 | unit |
| 135-REQ-1.2 | TS-135-2 | unit |
| 135-REQ-1.3 | TS-135-3 | integration |
| 135-REQ-1.E1 | TS-135-E1 | unit |
| 135-REQ-2.1 | TS-135-4 | unit |
| 135-REQ-2.2 | TS-135-5 | unit |
| 135-REQ-2.E1 | TS-135-E2 | unit |
| 135-REQ-3.1 | TS-135-6 | unit |
| 135-REQ-3.2 | TS-135-3 | integration |
| 135-REQ-4.1 | TS-135-7 | unit |
| 135-REQ-4.2 | TS-135-8 | unit |
| 135-REQ-4.3 | TS-135-9 | unit |
| 135-REQ-5.1 | TS-135-SMOKE-2 | integration |
| 135-REQ-5.2 | TS-135-SMOKE-2 | integration |
| 135-REQ-6.1 | TS-135-9 | unit |
| 135-REQ-6.2 | TS-135-10 | unit |
| Property 1 | TS-135-P2 | property |
| Property 2 | TS-135-P1 | property |
| Property 3 | TS-135-6 | unit |
| Property 4 | TS-135-SMOKE-2 | integration |
