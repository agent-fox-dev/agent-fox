---
spec_id: '05'
spec_name: spec_agentic_cli
title: Spec Agentic Cli
status: draft
created_at: '2026-06-23T08:12:40.982750+00:00'
updated_at: '2026-06-23T08:12:40.982750+00:00'
owner: ''
source: interactive
schema_version: 1
---
# spec CLI Agentic Optimization

## Intent

Migrate the `spec` CLI to use the unified `agentfox/io/` module from spec 03, adding JSON output for the `render` command and richer validation output.

## Background

Spec 03 created `agentfox/io/` — a shared terminal IO module. Spec 04 migrated the `af` CLI. This spec completes the migration by wiring the `spec` CLI, removing inline JSON/error patterns, and adding spec-specific agentic improvements.

## Problem

1. **`spec render` has no JSON mode.** It outputs raw markdown. Agents cannot programmatically access the rendered content.
2. **`spec validate` output is minimal.** It returns `{"valid": false, "errors": [...]}` with string-only errors. No structured information about which schema failed, which check failed, or the offending values.
3. **Inline JSON/error patterns.** `spec/cli.py` implements its own `_json_error_exit()` and inline `json.dumps()` calls instead of using the shared IO module.
4. **`spec/ui.py` duplicates StatusSpinner.** The `StatusSpinner` in `spec/ui.py` is superseded by the one in `agentfox/io/spinner.py`.
5. **No agent-mode support.** `spec` doesn't support `AF_AGENT=1` or `--json` flag (it always outputs JSON on stdout, but doesn't have the agent-mode env var toggle for quiet/banner suppression).

## Goals

1. `spec/cli.py` uses `cls=AgentFoxGroup` for its root group.
2. `spec render --json` wraps markdown output in a JSON envelope.
3. `spec validate` returns structured error details (schema name, check name, offending values).
4. `spec/ui.py` is deleted; all spinner usage imports from `agentfox/io/spinner.py`.
5. All inline `_json_error_exit()` and `json.dumps()` calls are replaced by `agentfox/io` functions.
6. All existing tests pass unchanged.

## Solution

### 1. Wire spec/cli.py to AgentFoxGroup

Replace the root `@click.group()` with `@click.group(cls=AgentFoxGroup)`. Remove the manual banner rendering and config loading — `AgentFoxGroup` handles these. Remove `_json_error_exit()` and `_error_type()` helpers — replaced by `agentfox/io/errors.py`.

### 2. spec render --json

When `--json` is active, `spec render` wraps its output:

```json
{"ok": true, "format": "markdown", "content": "# Requirements\n...", "sections": ["requirements", "test_spec", "tasks"]}
```

When `--combined` and `--json` are both active, a single `content` string is returned. Without `--combined`, return per-artifact content:

```json
{"ok": true, "artifacts": {"requirements": "...", "test_spec": "...", "tasks": "..."}}
```

### 3. spec validate structured output

Enhance validation output with structured error details:

```json
{
  "valid": false,
  "errors": [
    {
      "category": "schema",
      "artifact": "requirements.json",
      "path": "$.requirements[0].acceptance_criteria[2].id",
      "message": "ID format invalid: expected '03-REQ-1.3', got 'REQ-1.3'",
      "value": "REQ-1.3"
    },
    {
      "category": "integrity",
      "check": "requirement_coverage",
      "message": "Requirement 03-REQ-5 has no test cases in test_spec.json",
      "requirement_id": "03-REQ-5"
    }
  ]
}
```

### 4. Delete spec/ui.py

Replace all `from spec.ui import StatusSpinner` with `from agentfox.io import StatusSpinner`. Delete `spec/ui.py`.

### 5. Migrate all inline patterns

Replace every `click.echo(json.dumps(...))` with `emit()` or `emit_ok()`. Replace every `_json_error_exit(exc)` with letting `AgentFoxGroup` handle the error routing. Remove the `_assessment_to_json()` helper if it can be replaced by standard serialization.

## Non-Goals

- Changing spec command semantics or workflow (new/refine/generate/validate/render/status).
- Modifying the `af` CLI (done in spec 04).
- Changing `agentspec` library internals.
- Adding new spec commands.

## Tech Stack

- Python 3.12+, Click >=8.1, Rich >=13.0

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 03_unified_terminal_io | 3 | 1 | Imports OutputManager, AgentFoxGroup, StatusSpinner, emit functions from agentfox/io/ |

## Source

Source: Input provided by user via interactive prompt

