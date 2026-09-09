---
spec_id: "09"
spec_name: "agent_mode"
title: "Agent mode for the spec CLI"
status: "draft"
created_at: "2026-09-08T09:00:00Z"
updated_at: "2026-09-08T09:00:00Z"
intent_hash: null
schema_version: 2
source: "docs/prds/gospec.md"
---
# Agent mode for the spec CLI

## Intent

Coding agents drive the `spec` CLI programmatically. When `AF_AGENT=1` is
set, every command must produce machine-readable JSON on stdout and nothing
else, so that an agent can parse results without filtering banner or
spinner text.

## Goals

- All structured output goes to stdout as one JSON object.
- Errors are reported as `{"ok": false, "error": "..."}` with exit code 1.
- Progress output never reaches stdout.

## Non-goals

- Streaming progress events.
- A machine-readable schema for every command's payload.
