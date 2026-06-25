---
spec_id: '11'
spec_name: enrich_context_summaries
title: Enrich Context Summaries
status: draft
created_at: '2026-06-25T12:01:42.373359+00:00'
updated_at: '2026-06-25T12:01:42.373359+00:00'
owner: ''
source: https://github.com/agent-fox-dev/agent-fox/issues/622
schema_version: 1
---
# Enrich Same-Spec Context Summaries

## Problem

Same-spec context summaries (`[CONTEXT]` items) injected into coder prompts are too shallow to influence agent behavior. A session audit of run `20260624_165149_e74eca` shows they typically contain 87–634 characters of completion-status pings that the coder never acts on. Examples:

- `"Reviewer session completed with no findings."` — 87 chars, zero actionable content.
- `"Implemented task group 2 for spec 07: removed night-shift subcommand..."` — 452 chars, repeats what tasks.json and git log already say.

The coder gets better information by reading the codebase, git log, and tasks.json checkbox states. These shallow summaries consume 1–3% of the prompt budget with no corresponding benefit.

## Solution

Enrich context summaries to extract **non-obvious learnings** from sessions rather than completion status. Extend the `session-summary.json` schema with structured fields for rejected approaches, gotchas, and assumptions. Compose a rich summary text from these fields before persisting to the database. Skip storing summaries for sessions with no non-obvious content.

## Scope

- **In scope**: Same-spec context summaries (`[CONTEXT]` prefix) — the summary generation, storage composition, and coder profile instructions.
- **Out of scope**: Cross-spec summaries (`[CROSS-SPEC]` prefix) — left as-is.

## Requirements

### R1: Extended Session Summary Schema

The `session-summary.json` artifact schema is extended with three optional structured fields:

- `rejected_approaches`: Array of objects, each with `approach` (string, what was tried) and `reason` (string, why it was rejected). May be empty or omitted.
- `gotchas`: Array of strings describing things the next coder should watch out for (edge cases, fragile patterns, counter-intuitive behavior). May be empty or omitted.
- `assumptions`: Array of strings describing assumptions made that might not hold for later groups. May be empty or omitted.

The existing `summary` and `tests_added_or_modified` fields remain unchanged. All new fields are optional for backward compatibility — old session-summary.json files without these fields continue to work.

### R2: Enriched Coder Profile Instructions

The coder profile (`_templates/profiles/coder.md`) is updated to instruct agents to populate the new structured fields. The summary section should ask for:

1. What was surprising or non-obvious about the implementation (in the `summary` field narrative).
2. What was tried and rejected, and why (populate `rejected_approaches`).
3. What the next coder should watch out for (populate `gotchas`).
4. What assumptions were made that might not hold for later groups (populate `assumptions`).

The `summary` field instructions are updated to focus on non-obvious learnings rather than just "description of work done." Target ~500–1000 characters of genuinely useful context per session.

### R3: Summary Composition

A new `compose_enriched_summary()` function composes a single enriched summary text from the structured fields in `session-summary.json`. The composition includes:

1. The narrative `summary` field (always present).
2. Rejected approaches (if any), each formatted as `"Tried: {approach} — rejected because: {reason}"`.
3. Gotchas (if any), each formatted as `"Watch out: {gotcha}"`.
4. Assumptions (if any), each formatted as `"Assumes: {assumption}"`.

Sections are separated by newlines. The composed text replaces the raw `summary` field when storing the `SummaryRecord` in the database.

When no structured fields are present (backward compatibility with old agents or old session-summary.json files), the raw `summary` field is stored as-is — no behavior change.

The composition function lives in `session_lifecycle.py` alongside the existing summary extraction logic.

### R4: Skip Trivial Auto-Generated Summaries

The `generate_archetype_summary()` function in `formatting.py` is modified to return `None` (instead of a noise string) for:

- Reviewer sessions with no findings (currently returns "Reviewer session completed with no findings.").
- Verifier sessions with no verdicts (currently returns "Verifier session completed with no verdicts.").

Reviewer/verifier sessions WITH findings/verdicts continue to generate summaries as before — their content (severity counts, top finding descriptions, failed requirement IDs) is already useful.

When `generate_archetype_summary()` returns `None`, no summary is stored in the database. This is already handled by the existing `if summary_text:` guard in the session lifecycle code — no changes needed there.

### R5: Architecture Documentation Update

Update `docs/architecture/05-knowledge-system-architecture.md` to reflect:

- Section 4.2 (Session Summary Storage): Describe the enriched summary schema and the composition step that transforms structured fields into the stored text.
- Section 5.5 (`session_summaries` table): Note that summaries now contain non-obvious learnings (rejected approaches, gotchas, assumptions) rather than completion status pings.
- Section 6 retrieval table: Update the "Same-spec summaries" row description to reflect the enriched content.
- Section 11 (Design Principles): Update the "Cross-session continuity" paragraph to describe the enriched summary content.

## Tech Stack

- Python 3.12+
- DuckDB (knowledge store — no schema migration needed, the `summary` VARCHAR column is reused)
- Existing session lifecycle and knowledge system

## Files

| File | Change |
|------|--------|
| `packages/agentfox/agentfox/_templates/profiles/coder.md` | Updated session summary instructions and schema |
| `packages/agentfox/agentfox/engine/session_lifecycle.py` | New `compose_enriched_summary()` function, integrate into summary extraction flow |
| `packages/agentfox/agentfox/knowledge/formatting.py` | `generate_archetype_summary()` returns `None` for no-findings/no-verdicts sessions |
| `docs/architecture/05-knowledge-system-architecture.md` | Architecture documentation updates |

## Design Decisions

1. **Profile-based enrichment for coders, code-based skip for reviewers/verifiers.** Coder summaries are enriched by updating the profile prompt instructions and extending the JSON schema. Reviewer/verifier summaries are filtered at the code level because they are auto-generated by `generate_archetype_summary()` and cannot be influenced by prompt changes.

2. **Composition into existing text field.** Rather than adding new columns to the `session_summaries` table, the structured fields are composed into the existing `summary` VARCHAR column. This avoids a DuckDB schema migration and keeps the retrieval path unchanged — `[CONTEXT]` items are already formatted from the text field.

3. **No retroactive filtering of old summaries.** Existing shallow summaries in the database are not filtered out at retrieval time. They will naturally age out as new runs create richer summaries. Adding character-length filtering risks accidentally dropping legitimately short but useful summaries.

4. **Summary length is a guideline, not a hard cap.** The ~500–1000 character target is communicated through profile instructions. No enforcement at storage or injection time — hard caps risk truncating useful content.

5. **Skip no-findings reviewer/verifier summaries entirely.** Rather than trying to enrich auto-generated summaries (which have no non-obvious content to surface), these are simply not stored. "Reviewer session completed with no findings" is irrecoverable noise — there is nothing non-obvious to extract.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 10_remove_unused_knowledge_channels | — | — | Complementary: spec 10 removes unused channels but retains same-spec summaries. This spec enriches those retained summaries. No blocking dependency — either can be implemented first. |

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/622

