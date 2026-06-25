---
spec_id: '04'
spec_name: af_agentic_cli
title: Af Agentic Cli
status: draft
created_at: '2026-06-23T08:12:40.680483+00:00'
updated_at: '2026-06-23T08:12:40.680483+00:00'
owner: ''
source: interactive
schema_version: 1
---
# af CLI Agentic Optimization

## Intent

Migrate the `af` CLI to use the unified `agentfox/io/` module from spec 03, adding JSONL streaming for long-running commands and ensuring consistent `--json` support across all commands.

## Background

Spec 03 created `agentfox/io/` — a shared terminal IO module with `OutputManager`, `AgentFoxGroup`, unified error envelopes, and `AF_AGENT=1` support. This spec wires the `af` CLI to use that module, removes compatibility shims, and adds af-specific agentic features.

## Problem

1. **Inconsistent `--json` support.** Not all `af` commands fully honor the `--json` flag. `standup` and `init` may emit unstructured text even in JSON mode.
2. **No JSONL streaming for long-running commands.** `af code` and `af night-shift` run for minutes/hours. Agents go blind during execution — no machine-readable progress events.
3. **Format dispatch duplication.** Every command reimplements `if json_mode: emit(...) else: click.echo(...)` instead of using `OutputManager`.
4. **Stale shims.** `af/json_io.py` is a compatibility shim from spec 03 that must be removed.
5. **No structured help output.** `--help` returns Click's text format. Agents must parse it to discover commands.
6. **Manual table formatting.** `standup` and `findings` build tables by hand instead of using a shared utility.

## Goals

1. All `af` commands use `OutputManager` for output dispatch — no direct `click.echo` for data output.
2. `af code` and `af night-shift` emit JSONL progress events on stderr in JSON mode.
3. `af/json_io.py` shim is deleted; all imports use `agentfox.io` directly.
4. `af/app.py` uses `cls=AgentFoxGroup` (if not already wired in spec 03).
5. Structured JSON help output works when `--json --help` is passed.
6. All existing tests pass unchanged.

## Solution

### 1. Wire af/app.py to AgentFoxGroup

If spec 03 deferred the `cls=AgentFoxGroup` wiring, apply it now. Remove `BannerGroup` and `handle_agent_fox_errors` from `af/__init__.py`. Both are superseded by `AgentFoxGroup`.

### 2. Migrate all commands to OutputManager

Each `af` command (`code`, `plan`, `standup`, `insights`, `init`, `night-shift`, `reset`) is updated to:
- Retrieve `OutputManager` from `ctx.obj["output"]`
- Use `om.emit()` for format dispatch instead of inline `if json_mode`
- Remove all `from af.json_io import ...` in favor of `from agentfox.io import ...`

### 3. JSONL streaming for long-running commands

In JSON mode, `af code` and `af night-shift` emit JSONL progress events to **stderr** via `OutputManager.emit_progress()`:

```json
{"event": "task_started", "node_id": "1.1", "timestamp": "..."}
{"event": "task_completed", "node_id": "1.1", "duration_s": 42.1, "timestamp": "..."}
{"event": "task_failed", "node_id": "1.2", "error": "...", "timestamp": "..."}
```

These events are on stderr (not stdout) so they don't interfere with the final JSON result on stdout. The `ProgressDisplay` class is updated to emit these events when `json_mode=True`.

### 4. Delete af/json_io.py shim

Remove the compatibility shim. Update all internal imports in `af/` to use `from agentfox.io import ...` directly.

### 5. Structured JSON help output

Implement the JSON help renderer deferred from spec 03. When `--json --help` is passed, emit JSON describing the command's name, description, options, and exit codes instead of Click's text help. Uses the `@exit_codes` metadata decorator from `agentfox/io/help.py`.

### 6. Table formatting utility

Add `format_table()` to `agentfox/io/output.py` for rendering tabular data. Migrate `standup` and `findings` manual table formatting to use it.

## Non-Goals

- Changing command semantics, exit codes, or flag names.
- Modifying `spec` CLI (that's spec 05).
- Modifying `agentfox/io/` public API (established in spec 03).

## Tech Stack

- Python 3.12+, Click >=8.1, Rich >=15.0

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 03_unified_terminal_io | 3 | 1 | Imports OutputManager, AgentFoxGroup, emit functions from agentfox/io/ |

## Source

Source: Input provided by user via interactive prompt

