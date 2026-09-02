---
spec_id: '10'
spec_name: remove_unused_knowledge_channels
title: Remove Unused Knowledge Channels
status: draft
created_at: '2026-06-25T12:00:07.774641+00:00'
updated_at: '2026-06-25T12:00:07.774641+00:00'
owner: mickume
source: https://github.com/agent-fox-dev/agent-fox/issues/621
schema_version: 1
---
# Remove Unused Knowledge-System Retrieval Channels

## Summary

A session audit of run `20260624_165149_e74eca` (22 completed sessions, $78
total cost) found that 5 of the 8 knowledge-system retrieval channels produce
zero actionable value. They should be removed from the codebase and the DuckDB
schema.

The 3 channels to **keep** are:

1. **Review findings** (high value — drives fix behavior in retries)
2. **Drift findings / cross-group reviews** (medium value)
3. **Same-spec context summaries** (low but cheap)

## Channels to Remove

### 1. Cross-spec items

Session summaries from other specs injected as `[CROSS-SPEC]` tags. In
practice, these are completion-status pings that don't meaningfully inform
other specs' coders. Across 24 sessions, cross-spec items were retrieved 3
times — all were one-line completion pings.

### 2. Errata

Retrieved **0 items** across all 24 sessions. The `fox_provider` log
consistently shows `0 errata`. The indexing infrastructure runs at startup
(`index_errata_from_markdown`) but produces nothing that gets retrieved.

### 3. ADRs (Architecture Decision Records)

Retrieved **0 items** across all 24 sessions. The ingestion code
(`ingest_adr`, `parse_madr`, `detect_adr_changes`) runs but yields nothing.

### 4. Verdicts (Verification Results)

Retrieved **0 items** across all 24 sessions. The verdict channel
(`query_active_verdicts`, `query_cross_group_verdicts`) queries the
`verification_results` table but returns empty sets. Verdicts are written by
the verifier archetype but never consumed by coders. This includes cross-group
verdicts (`_query_cross_group_verdicts` in fox_provider.py).

### 5. Prior-run items

Retrieved **0 items** across all 24 sessions. This channel queries
`review_findings` and `verification_results` filtered by prior run timestamps.
With no accumulated run history, it's always empty.

## Code to Remove

### DuckDB tables to drop (add a new migration version)

Add a new migration version in `migrations.py` that DROPs:

- `errata`
- `adr_entries`
- `verification_results`

**Keep** `session_summaries` — it is used by `query_same_spec_summaries()` for
the same-spec context summaries channel (which is retained).

### Entire modules to delete

| File | Purpose |
|------|---------|
| `packages/agentfox/agentfox/knowledge/errata.py` | Errata indexing, storage, formatting |
| `packages/agentfox/agentfox/knowledge/adr.py` | ADR parsing (MADR format), ingestion, querying |

### Functions/methods to remove from fox_provider.py

| Method | Channel |
|--------|---------|
| `_query_errata()` | Errata |
| `_query_adrs()` | ADR |
| `_query_verdicts()` | Verdicts |
| `_query_cross_group_verdicts()` | Verdicts (cross-group) |
| `_query_cross_spec_summaries()` | Cross-spec |
| `_query_prior_run_findings()` | Prior-run |

Also update the retrieval orchestration in the `retrieve()` method:

- Remove calls to the 6 deleted methods
- Remove verdict_ids from `items_with_ids` tracking (simplifies to just review_ids)
- Remove cross_verdicts from `cross_group_items` (simplifies to just cross_reviews)
- Simplify the log message from 8 fields to 3:
  ```
  Retrieved {reviews} review + {cross_group} cross-group + {context} context items for {spec}
  ```
- Remove verdict ID tracking from `__init__`

### Functions to remove from review_store.py

- `query_active_verdicts()`
- `query_cross_group_verdicts()`
- `query_prior_run_findings()`
- `query_prior_run_verdicts()`
- `insert_verdicts()`
- `validate_verdict()`

### Functions to remove from summary_store.py

- `query_cross_spec_summaries()`

### Callers to update

| File | What to remove |
|------|---------------|
| `agentfox/engine/run.py` | `index_errata_from_markdown()` call at startup |
| `agentfox/engine/result_handler.py` | `_generate_errata()` method and all calls to it |
| `nightshift/_startup.py` | `index_errata_from_markdown()` call |
| `agentfox/engine/session_lifecycle.py` | Comment referencing `index_errata_from_markdown` |

### Prompt assembly cleanup

Remove formatting for removed channels:
- `[ERRATA]` tag construction
- `[ADR]` tag construction
- `[VERIFY]` / `[CROSS-GROUP]` verdict formatting
- `[CROSS-SPEC]` summary formatting
- `[PRIOR-RUN]` finding/verdict formatting

### Tests to remove or update

Test files covering removed channels:
- `test_errata.py` — delete entire file
- `test_adr.py`, `test_adr_props.py` — delete entire files
- `test_verdict_normalization.py` — delete entire file
- `test_cross_run_carryforward.py` — delete entire file
- `test_fox_provider_summaries.py` — remove cross-spec portions
- `test_summary_store.py` — remove cross-spec portions
- `test_summary_lifecycle.py` — remove cross-spec portions
- `test_review_store.py` — remove verdict portions
- `test_review_store_props.py` — remove verdict portions
- `test_retrieval_fixes_smoke.py` — remove prior-run portions
- `test_duckdb_reader_writer_smoke.py` — remove ADR/errata portions
- `test_duckdb_reader_writer_idempotency.py` — remove errata portions
- `test_assemble_context_readonly.py` — remove errata portions
- `test_errata_on_blocking.py` — delete entire file
- `test_audit_review_blocking.py` — remove errata mock references
- `test_duckdb_reader_writer_props.py` — remove errata mock references

### Architecture documentation to update

| File | What to update |
|------|---------------|
| `docs/architecture.md` | Remove errata, ADR, verdict references from §1.2 description, DuckDB schema section, and knowledge injection table |
| `docs/architecture/05-knowledge-system-architecture.md` | Remove sections covering removed channels |
| `docs/architecture/03-execution-and-archetypes.md` | Remove verdict/errata references |

## Non-Goals

- Changing the review findings channel (kept as-is)
- Changing the drift findings / cross-group reviews channel (kept as-is)
- Changing the same-spec context summaries channel (kept as-is)
- Changing the `session_summaries` table schema (kept for same-spec summaries)
- Adding new retrieval channels

## Design Decisions

1. **Migration approach**: The summary's "no database migration needed" refers
   to no complex data migration (no ALTER TABLE, no data backfill). A new
   migration version with DROP TABLE statements will be added — this is a
   simple schema cleanup, not a data migration.

2. **`_query_cross_group_verdicts()` inclusion**: The original issue table
   omitted this method from the fox_provider.py removal list, but it is clearly
   part of the verdicts channel (called at line 234 in the retrieval
   orchestration). It is included in the removal scope.

3. **`session_summaries` table retention**: Verified that
   `query_same_spec_summaries()` in `summary_store.py` queries this table.
   The table is kept; only `query_cross_spec_summaries()` and its caller are
   removed.

4. **Architecture documentation scope**: Three files require updates:
   `docs/architecture.md`, `docs/architecture/05-knowledge-system-architecture.md`,
   and `docs/architecture/03-execution-and-archetypes.md`. All references to
   errata, ADRs, verdicts, cross-spec summaries, and prior-run items will be
   removed or rewritten to reflect the simplified 3-channel retrieval system.

5. **Cross-group items simplification**: After removing cross-group verdicts,
   the `cross_group_items` variable in `retrieve()` simplifies to just
   cross-group reviews. The `max_cross_group_items` cap still applies.

6. **Injected ID tracking simplification**: The `items_with_ids` list in
   `retrieve()` will contain only `(text, review_id)` tuples after removing
   errata, ADR, and verdict entries. The None-padded entries for errata/ADR
   are removed.

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/621

